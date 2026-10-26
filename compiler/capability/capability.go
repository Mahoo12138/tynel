// Package capability 判断一份程序是否落在原生后端的当前子集内。
//
// 它回答的问题和类型检查不同：TypeScript 接受这段代码，不代表 Tynel 能把它
// 变成机器码。类型检查通过之后，这里再走一遍所有函数体——包括没有被调用的
// 函数——按白名单核对语法、类型和调用目标，把「暂时不支持」的写法挑出来。
//
// 它不做可达性分析。一个没人调用的函数里写了对象字面量，同样会被拒绝：今天
// 不可达不代表以后不会被调用，而「因为没调用所以没检查」是最难排查的一类漏。
package capability

import (
	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/source"
)

// Check 遍历事实里的每一个声明与函数体，返回能力限制诊断。
//
// 诊断类别是 Capability（退出 3），与源码错误（退出 1）分开：前者是「写法合法
// 但后端还没实现」，后者是「代码本身有问题」。
func Check(snapshot *source.Snapshot, facts *frontend.Facts) ([]*diagnostic.Diagnostic, error) {
	// 先确认事实自洽。能力检查会按 ID 查声明，如果 ID 本身有问题，后面的
	// 结论都不可信。
	if err := facts.Validate(); err != nil {
		return nil, err
	}
	c := &checker{snapshot: snapshot, facts: facts}
	for i := range facts.Declarations {
		decl := &facts.Declarations[i]
		c.declaration(decl)
		if decl.Body != nil {
			c.inFunction = decl.Kind == frontend.DeclFunction
			c.node(decl.Body)
			c.inFunction = false
		}
		if c.err != nil {
			return nil, c.err
		}
	}
	if facts.Module != nil {
		c.inFunction = false
		c.node(facts.Module)
	}
	return c.findings, c.err
}

type checker struct {
	snapshot   *source.Snapshot
	facts      *frontend.Facts
	findings   []*diagnostic.Diagnostic
	err        error
	inFunction bool
}

func (c *checker) declaration(d *frontend.Declaration) {
	if d.Kind == frontend.DeclFunction {
		if d.Result != nil && d.Result.Kind == frontend.KindAny {
			c.report(d.Start, "cap.any", "函数 %q 的返回类型是 any 或 unknown；本阶段只支持 number、boolean 和 void", d.Name)
		}
		return
	}
	if d.Type != nil && d.Type.Kind == frontend.KindAny {
		c.report(d.Start, "cap.any", "%s %q 的类型是 any 或 unknown；本阶段只支持 number、boolean 和 void", kindName(d.Kind), d.Name)
	}
}

func (c *checker) node(n *frontend.Node) {
	if c.err != nil || n == nil {
		return
	}
	switch n.Kind {
	case "Block":
		c.children(n)
	case "Const":
		// 模块级常量本身没问题；它的初始化表达式挂在声明上，已经单独检查过。
		return
	case "ReturnStatement", "ExpressionStatement":
		if len(n.Children) > 1 {
			c.report(n.Start, "cap.unsupported-syntax", "%s 有 %d 个子节点，超出本阶段支持的形状", n.Kind, len(n.Children))
			return
		}
		c.children(n)
	case "BinaryExpression":
		if !isArithmetic(n.Op) {
			c.report(n.Start, "cap.bad-operator", "运算符 %q 不在本阶段支持的 + - * / 之内", n.Op)
			return
		}
		if len(n.Children) != 2 {
			c.report(n.Start, "cap.unsupported-syntax", "二元运算有 %d 个操作数", len(n.Children))
			return
		}
		c.children(n)
	case "PrefixUnaryExpression":
		if n.Op != "-" && n.Op != "+" {
			c.report(n.Start, "cap.bad-operator", "一元运算符 %q 不在本阶段支持的范围之内", n.Op)
			return
		}
		if len(n.Children) != 1 {
			c.report(n.Start, "cap.unsupported-syntax", "一元运算有 %d 个操作数", len(n.Children))
			return
		}
		c.children(n)
	case "Identifier":
		c.identifier(n)
	case "NumericLiteral":
		return
	case "CallExpression":
		c.call(n)
	case "PropertyAccessExpression":
		c.report(n.Start, "cap.unsupported-syntax", "属性访问 %q 不在本阶段子集内", n.Name)
	case "AsExpression", "TypeAssertionExpression", "NonNullExpression", "SatisfiesExpression":
		c.report(n.Start, "cap.type-escape", "类型断言 %s 绕过了类型检查，本阶段不支持", n.Kind)
	case "ObjectLiteralExpression":
		c.report(n.Start, "cap.unsupported-syntax", "对象字面量不在本阶段子集内")
	case "ArrayLiteralExpression":
		c.report(n.Start, "cap.unsupported-syntax", "数组字面量不在本阶段子集内")
	case "StringLiteral", "NoSubstitutionTemplateLiteral", "TemplateExpression":
		c.report(n.Start, "cap.unsupported-syntax", "字符串不在本阶段子集内")
	case "VariableStatement", "VariableDeclaration", "VariableDeclarationList":
		c.report(n.Start, "cap.unsupported-syntax", "局部变量不在本阶段子集内，顶层只支持 const")
	case "IfStatement", "WhileStatement", "DoStatement", "ForStatement", "ForInStatement", "ForOfStatement", "SwitchStatement", "TryStatement", "ThrowStatement":
		c.report(n.Start, "cap.unsupported-syntax", "控制流 %s 不在本阶段子集内", n.Kind)
	case "NewExpression":
		c.report(n.Start, "cap.unsupported-syntax", "new 表达式不在本阶段子集内")
	case "FunctionExpression", "ArrowFunction", "ClassExpression":
		c.report(n.Start, "cap.unsupported-syntax", "嵌套函数与类不在本阶段子集内")
	case "ConditionalExpression":
		c.report(n.Start, "cap.unsupported-syntax", "条件表达式不在本阶段子集内")
	default:
		c.report(n.Start, "cap.unsupported-syntax", "语法 %s 不在本阶段子集内", n.Kind)
	}
}

func (c *checker) children(n *frontend.Node) {
	for i := range n.Children {
		c.node(&n.Children[i])
	}
}

func (c *checker) identifier(n *frontend.Node) {
	decl, ok := c.facts.Declaration(n.Target)
	if !ok || n.Target == 0 {
		c.report(n.Start, "cap.unknown-name", "名称 %q 不是本文件里的参数或常量", n.Name)
		return
	}
	switch decl.Kind {
	case frontend.DeclFunction:
		c.report(n.Start, "cap.unsupported-syntax", "函数 %q 作为值使用不在本阶段子集内", n.Name)
	case frontend.DeclConst:
		if c.inFunction {
			c.report(n.Start, "cap.module-const-in-function", "函数内引用模块常量 %q 需要全局变量，本阶段不支持", n.Name)
		}
	}
}

func (c *checker) call(n *frontend.Node) {
	if len(n.Children) == 0 {
		c.report(n.Start, "cap.unsupported-syntax", "调用缺少被调用者")
		return
	}
	callee := &n.Children[0]
	args := n.Children[1:]

	switch callee.Kind {
	case "Identifier":
		if callee.Name == "eval" && callee.Target == 0 {
			c.report(n.Start, "cap.forbidden-global", "eval 不在本阶段子集内")
			return
		}
		decl, ok := c.facts.Declaration(callee.Target)
		if !ok || callee.Target == 0 || decl.Kind != frontend.DeclFunction {
			c.report(n.Start, "cap.unknown-callee", "调用目标 %q 不是本文件里声明的函数", callee.Name)
			return
		}
		// 类型检查已经拦过实参个数，这里再核对一次：能力检查拿到的事实可能
		// 不是刚从前端出来的那一份，不能假设上游一定检查过。
		if len(args) != len(decl.Parameters) {
			c.report(n.Start, "cap.arg-count", "调用 %q 给了 %d 个实参，函数声明有 %d 个参数", decl.Name, len(args), len(decl.Parameters))
			return
		}
		for i := range args {
			c.node(&args[i])
		}
	case "PropertyAccessExpression":
		c.consoleCall(n, callee, args)
	default:
		c.report(n.Start, "cap.unknown-callee", "调用目标不是直接函数名")
	}
}

// consoleCall 处理受限输出。名字叫 console.log 不代表它就是内置的那个：
// 如果 console 解析到了本文件里的声明，说明它被遮蔽了，必须拒绝。
func (c *checker) consoleCall(n *frontend.Node, callee *frontend.Node, args []frontend.Node) {
	if callee.Name != "log" || len(callee.Children) != 1 {
		c.report(n.Start, "cap.unknown-callee", "只支持 console.log，收到的是 .%s", callee.Name)
		return
	}
	base := &callee.Children[0]
	if base.Kind != "Identifier" || base.Name != "console" {
		c.report(n.Start, "cap.unknown-callee", "只支持 console.log，收到的是其他对象的方法")
		return
	}
	if base.Target != 0 {
		c.report(n.Start, "cap.shadowed-console", "console 被本文件里的声明遮蔽，不能当作内置输出使用")
		return
	}
	if len(args) != 1 {
		c.report(n.Start, "cap.bad-console-call", "console.log 只接受一个参数，收到 %d 个", len(args))
		return
	}
	if args[0].Type == nil || args[0].Type.Kind != frontend.KindNumber {
		c.report(args[0].Start, "cap.bad-console-call", "console.log 只接受 number，本阶段不支持其他类型")
		return
	}
	c.node(&args[0])
}

func (c *checker) report(offset int, code, format string, args ...any) {
	if c.err != nil {
		return
	}
	pos, err := c.snapshot.Position(offset)
	if err != nil {
		c.err = err
		return
	}
	c.findings = append(c.findings, diagnostic.New(diagnostic.Capability, code, format, args...).At(c.snapshot.Path(), pos.Line, pos.Column))
}

func isArithmetic(op string) bool {
	switch op {
	case "+", "-", "*", "/":
		return true
	}
	return false
}

func kindName(k frontend.DeclKind) string {
	switch k {
	case frontend.DeclParameter:
		return "参数"
	case frontend.DeclConst:
		return "常量"
	case frontend.DeclFunction:
		return "函数"
	}
	return string(k)
}
