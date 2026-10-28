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
	"github.com/Mahoo12138/tynel/compiler/nir"
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
	switch d.Kind {
	case frontend.DeclFunction:
		c.declaredType(d.Result, d.Start, "函数 %q 的返回类型", d.Name, frontend.KindNumber, frontend.KindBoolean, frontend.KindVoid)
	case frontend.DeclParameter:
		c.declaredType(d.Type, d.Start, "参数 %q ", d.Name, frontend.KindNumber, frontend.KindBoolean)
	case frontend.DeclConst:
		c.declaredType(d.Type, d.Start, "常量 %q ", d.Name, frontend.KindNumber, frontend.KindBoolean)
		if d.Body != nil && d.Body.Type != nil && d.Body.Type.Kind == frontend.KindVoid {
			c.report(d.Body.Start, "cap.void-value", "常量 %q 的初始值是 void 调用，没有可用的值", d.Name)
		}
	case frontend.DeclLocal:
		c.declaredType(d.Type, d.Start, "局部值 %q ", d.Name, frontend.KindNumber, frontend.KindBoolean)
	}
}

// declaredType 核对声明上的类型。允许的类别按阶段放开：参数与常量是 number
// 或 boolean，函数返回值再加 void。
//
// format 是带一个 %q 的主语，调用方决定「参数 X 」还是「函数 X 的返回类型」，
// 这样中文里不会出现「返回类型 是」这种多余的空格。
func (c *checker) declaredType(t *frontend.Type, offset int, format, name string, allowed ...frontend.Kind) {
	if t == nil {
		c.report(offset, "cap.bad-type", format+"缺少类型", name)
		return
	}
	if t.Kind == frontend.KindAny {
		c.report(offset, "cap.any", format+"是 any 或 unknown；本阶段只支持 number、boolean 和 void", name)
		return
	}
	for _, k := range allowed {
		if t.Kind == k {
			return
		}
	}
	c.report(offset, "cap.bad-type", format+"是 %s，本阶段不支持", name, t.Kind)
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
	case "ReturnStatement":
		if len(n.Children) > 1 {
			c.report(n.Start, "cap.unsupported-syntax", "return 有 %d 个值，超出本阶段支持的形状", len(n.Children))
			return
		}
		if len(n.Children) == 1 {
			c.valueNode(&n.Children[0], "返回值")
		}
	case "ExpressionStatement":
		// 结果被丢弃：表达式本身可以是 number，也可以是 void 调用。值不会被
		// 打印，只有副作用保留。
		if len(n.Children) != 1 {
			c.report(n.Start, "cap.unsupported-syntax", "表达式语句有 %d 个子节点", len(n.Children))
			return
		}
		c.node(&n.Children[0])
	case "IfStatement":
		if !c.inFunction {
			c.report(n.Start, "cap.unsupported-syntax", "模块级 if 不在本阶段子集内")
			return
		}
		if len(n.Children) < 2 || len(n.Children) > 3 {
			c.report(n.Start, "cap.unsupported-syntax", "if 有 %d 个子节点", len(n.Children))
			return
		}
		c.boolOperand(&n.Children[0], "if 的条件")
		for i := 1; i < len(n.Children); i++ {
			c.node(&n.Children[i])
		}
	case "VariableStatement":
		list := variableList(n)
		if list == nil {
			c.report(n.Start, "cap.unsupported-syntax", "局部变量语句缺少声明列表")
			return
		}
		if len(list.Children) != 1 {
			c.report(n.Start, "cap.unsupported-syntax", "本阶段一次只支持声明一个局部值")
			return
		}
		c.node(list)
	case "VariableDeclarationList":
		c.children(n)
	case "VariableDeclaration":
		if n.Decl == 0 {
			c.report(n.Start, "cap.unsupported-syntax", "局部 let/var 不在本阶段子集内，只支持 const")
			return
		}
		decl, ok := c.facts.Declaration(n.Decl)
		if !ok || decl.Kind != frontend.DeclLocal {
			c.report(n.Start, "cap.unknown-name", "局部变量没有对应的声明")
			return
		}
		if len(n.Children) != 1 {
			c.report(n.Start, "cap.unsupported-syntax", "局部值 %q 必须有且只有一个初始值", decl.Name)
			return
		}
		if decl.Type == nil {
			c.report(n.Start, "cap.bad-type", "局部值 %q 缺少类型摘要", decl.Name)
			return
		}
		c.typedOperand(&n.Children[0], decl.Type.Kind, "局部值 "+decl.Name)
	case "ConditionalExpression":
		if len(n.Children) != 3 {
			c.report(n.Start, "cap.bad-arity", "三元式有 %d 个子节点", len(n.Children))
			return
		}
		c.boolOperand(&n.Children[0], "三元式的条件")
		thenNode, elseNode := &n.Children[1], &n.Children[2]
		if thenNode.Type == nil || elseNode.Type == nil {
			c.report(n.Start, "cap.bad-type", "三元式的分支缺少类型摘要")
			return
		}
		if thenNode.Type.Kind != elseNode.Type.Kind {
			c.report(n.Start, "cap.bad-type", "三元式两条分支类型不同：%s 与 %s", thenNode.Type.Kind, elseNode.Type.Kind)
			return
		}
		if thenNode.Type.Kind != frontend.KindNumber && thenNode.Type.Kind != frontend.KindBoolean {
			c.report(n.Start, "cap.bad-type", "三元式的结果不能是 %s", thenNode.Type.Kind)
			return
		}
		for i := range n.Children {
			c.node(&n.Children[i])
		}
	case "BinaryExpression":
		if len(n.Children) != 2 {
			c.report(n.Start, "cap.unsupported-syntax", "二元运算有 %d 个操作数", len(n.Children))
			return
		}
		switch {
		case isArithmetic(n.Op):
			for i := range n.Children {
				c.numberOperand(&n.Children[i], "运算符 "+n.Op+" 的操作数")
			}
		case nir.IsOrderingOperator(n.Op):
			for i := range n.Children {
				c.numberOperand(&n.Children[i], "比较 "+n.Op+" 的操作数")
			}
		case nir.IsEqualityOperator(n.Op):
			left, right := &n.Children[0], &n.Children[1]
			if left.Type == nil || right.Type == nil || left.Type.Kind != right.Type.Kind {
				c.report(n.Start, "cap.bad-type", "严格相等 %q 需要两侧同类型", n.Op)
				return
			}
			if left.Type.Kind != frontend.KindNumber && left.Type.Kind != frontend.KindBoolean {
				c.report(n.Start, "cap.bad-type", "严格相等 %q 只支持 number 或 boolean，实际为 %s", n.Op, left.Type.Kind)
				return
			}
			c.node(left)
			c.node(right)
		case n.Op == "&&" || n.Op == "||":
			for i := range n.Children {
				c.boolOperand(&n.Children[i], "运算符 "+n.Op+" 的操作数")
			}
		case n.Op == "==" || n.Op == "!=":
			c.report(n.Start, "cap.bad-operator", "宽松相等 %q 不在本阶段子集内，请使用 ===", n.Op)
		default:
			c.report(n.Start, "cap.bad-operator", "运算符 %q 不在本阶段支持的 + - * / 与比较之内", n.Op)
		}
	case "PrefixUnaryExpression":
		if n.Op == "!" {
			if len(n.Children) != 1 {
				c.report(n.Start, "cap.unsupported-syntax", "一元运算有 %d 个操作数", len(n.Children))
				return
			}
			c.boolOperand(&n.Children[0], "! 的操作数")
			return
		}
		if n.Op != "-" && n.Op != "+" {
			c.report(n.Start, "cap.bad-operator", "一元运算符 %q 不在本阶段支持的范围之内", n.Op)
			return
		}
		if len(n.Children) != 1 {
			c.report(n.Start, "cap.unsupported-syntax", "一元运算有 %d 个操作数", len(n.Children))
			return
		}
		c.valueNode(&n.Children[0], "一元 "+n.Op+" 的操作数")
	case "Identifier":
		c.identifier(n)
	case "NumericLiteral", "TrueKeyword", "FalseKeyword":
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
	case "WhileStatement", "DoStatement", "ForStatement", "ForInStatement", "ForOfStatement", "SwitchStatement", "TryStatement", "ThrowStatement":
		c.report(n.Start, "cap.unsupported-syntax", "控制流 %s 不在本阶段子集内", n.Kind)
	case "NewExpression":
		c.report(n.Start, "cap.unsupported-syntax", "new 表达式不在本阶段子集内")
	case "FunctionExpression", "ArrowFunction", "ClassExpression":
		c.report(n.Start, "cap.unsupported-syntax", "嵌套函数与类不在本阶段子集内")
	default:
		c.report(n.Start, "cap.unsupported-syntax", "语法 %s 不在本阶段子集内", n.Kind)
	}
}

// valueNode 检查一个处在「需要值」位置的表达式。void 调用只能单独成句或作为
// 丢弃结果的对象；出现在这里说明有人想用它的值，必须拒绝。
func (c *checker) valueNode(n *frontend.Node, what string) {
	if n == nil {
		return
	}
	if n.Type != nil && n.Type.Kind == frontend.KindVoid {
		c.report(n.Start, "cap.void-value", "%s需要一个值，但这里是 void 调用", what)
		return
	}
	c.node(n)
}

// boolOperand 要求一个表达式是 boolean。TypeScript 允许对任意值取反（!5 是
// false），但本阶段只把 boolean 的取反映射到原生指令，其余类型先拒绝。
func (c *checker) boolOperand(n *frontend.Node, what string) {
	c.typedOperand(n, frontend.KindBoolean, what)
}

// numberOperand 要求一个表达式是 number。算术与有序比较都靠它把「类型摘要
// 对不上」的伪造事实挡在降低之前。
func (c *checker) numberOperand(n *frontend.Node, what string) {
	c.typedOperand(n, frontend.KindNumber, what)
}

func (c *checker) typedOperand(n *frontend.Node, want frontend.Kind, what string) {
	if n == nil {
		return
	}
	if n.Type != nil && n.Type.Kind == frontend.KindVoid {
		c.report(n.Start, "cap.void-value", "%s需要一个值，但这里是 void 调用", what)
		return
	}
	if n.Type == nil || n.Type.Kind != want {
		kind := frontend.KindOther
		if n.Type != nil {
			kind = n.Type.Kind
		}
		c.report(n.Start, "cap.bad-type", "%s是 %s，需要 %s", what, kind, want)
		return
	}
	c.node(n)
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
	case frontend.DeclLocal:
		// 局部 const 可以在当前块及其子块里读取；越界读取由 TypeScript 报
		// 「找不到名称」，能力检查不重复做作用域分析。
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
			c.valueNode(&args[i], "调用 "+decl.Name+" 的实参")
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
	if args[0].Type != nil && args[0].Type.Kind == frontend.KindVoid {
		c.report(args[0].Start, "cap.void-value", "console.log 需要一个值，但这里是 void 调用")
		return
	}
	if args[0].Type == nil || (args[0].Type.Kind != frontend.KindNumber && args[0].Type.Kind != frontend.KindBoolean) {
		c.report(args[0].Start, "cap.bad-console-call", "console.log 只接受 number 或 boolean，本阶段不支持其他类型")
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

// variableList 找到 VariableStatement 下的声明列表。骨架可能来自手写事实，
// 不假设层级一定完整。
func variableList(n *frontend.Node) *frontend.Node {
	if n.Kind != "VariableStatement" {
		return nil
	}
	for i := range n.Children {
		if n.Children[i].Kind == "VariableDeclarationList" {
			return &n.Children[i]
		}
	}
	return nil
}
