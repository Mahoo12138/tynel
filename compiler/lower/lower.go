// Package lower 把前端事实降低成 NIR。
//
// 它是编译流程里唯一同时认识「事实」和「NIR」的地方：前端的事实按源码顺序
// 被摊平成 NIR 语句，模块级 const 变成局部值，函数体变成 return 与 print。
// 降低发生在检查会话关闭之后，只依赖事实里的普通数据。
//
// 降低不重复做类型检查，但它不假设事实一定合法：类型不对、操作数个数不对、
// 引用了作用域外的名字、嵌套过深，都会在这里报错并返回 nil 模块。任何一步
// 失败都不会留下半份可用的 NIR。
package lower

import (
	"fmt"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/nir"
	"github.com/Mahoo12138/tynel/compiler/source"
)

// EntryName 是模块入口的名字。用户函数不能占用它。
const EntryName = "main"

// maxDepth 是表达式嵌套的降低上限。事实校验已经限制过一次，这里再兜一层：
// 降低是递归的，深树会先把栈吃光。
const maxDepth = 100

// Lower 把事实降低成模块。失败时返回 nil，不返回半个模块。
func Lower(snapshot *source.Snapshot, facts *frontend.Facts) (*nir.Module, error) {
	if snapshot == nil || facts == nil {
		return nil, diagnostic.New(diagnostic.Internal, "lower.missing-input", "降低缺少源码快照或事实")
	}
	if err := facts.Validate(); err != nil {
		return nil, err
	}

	l := &lowerer{
		snapshot: snapshot,
		facts:    facts,
		names:    make(map[int]string, len(facts.Declarations)),
	}
	module := &nir.Module{SchemaVersion: nir.SchemaVersion, Entry: EntryName}

	for i := range facts.Declarations {
		decl := &facts.Declarations[i]
		if decl.Kind != frontend.DeclFunction {
			continue
		}
		if decl.Name == EntryName {
			return nil, l.fail(decl.Start, "lower.entry-conflict", "用户函数占用了入口名 %q", EntryName)
		}
		l.names[decl.ID] = decl.Name
	}

	for i := range facts.Declarations {
		decl := &facts.Declarations[i]
		if decl.Kind != frontend.DeclFunction {
			continue
		}
		fn, err := l.function(decl)
		if err != nil {
			return nil, err
		}
		module.Functions = append(module.Functions, *fn)
	}

	entry, err := l.entry()
	if err != nil {
		return nil, err
	}
	module.Functions = append(module.Functions, *entry)

	if err := nir.Verify(module); err != nil {
		// 走到这里说明降低产出了自己都不认的模块，属于内部故障。
		return nil, diagnostic.New(diagnostic.Internal, "lower.invalid-output", "降低产出的 NIR 没有通过验证").WithDetail(err.Error())
	}
	return module, nil
}

type lowerer struct {
	snapshot *source.Snapshot
	facts    *frontend.Facts
	names    map[int]string // 函数声明 ID → NIR 函数名
	result   string         // 当前正在降低的函数的返回类型
	temp     int            // 短路结果与实参物化的临时名序号
}

// scope 是当前可见的声明。用声明 ID 而不是名字做键：两个函数里同名的参数是
// 两个声明，按名字查会把它们混成一个。params 只在函数根作用域出现；locals
// 按语句块分层，子块可以遮蔽父块，退出子块后声明不可见。
type scope struct {
	parent *scope
	params map[int]string
	locals map[int]string
}

func (s *scope) child() *scope {
	return &scope{parent: s, locals: make(map[int]string)}
}

func (s *scope) lookup(id int) (string, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if name, ok := cur.locals[id]; ok {
			return name, true
		}
	}
	return "", false
}

func (s *scope) lookupParam(id int) (string, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if cur.params == nil {
			continue
		}
		if name, ok := cur.params[id]; ok {
			return name, true
		}
	}
	return "", false
}

func (s *scope) define(id int, name string) bool {
	if _, exists := s.locals[id]; exists {
		return false
	}
	s.locals[id] = name
	return true
}

func (l *lowerer) function(decl *frontend.Declaration) (*nir.Function, error) {
	if decl.Result == nil {
		return nil, l.fail(decl.Start, "lower.bad-type", "函数 %q 缺少返回类型摘要", decl.Name)
	}
	if decl.Result.Kind != frontend.KindNumber && decl.Result.Kind != frontend.KindBoolean && decl.Result.Kind != frontend.KindVoid {
		return nil, l.fail(decl.Start, "lower.bad-type", "函数 %q 的返回类型是 %s，本阶段只支持 number、boolean 与 void", decl.Name, decl.Result.Kind)
	}
	fn := &nir.Function{
		Name:   decl.Name,
		Result: nirType(decl.Result.Kind),
		Pos:    l.pos(decl.Start),
	}
	sc := &scope{params: make(map[int]string, len(decl.Parameters)), locals: make(map[int]string)}
	for _, id := range decl.Parameters {
		param, ok := l.facts.Declaration(id)
		if !ok || param.Kind != frontend.DeclParameter {
			return nil, l.fail(decl.Start, "lower.unknown-param", "函数 %q 的参数 ID %d 不存在", decl.Name, id)
		}
		if param.Type == nil || (param.Type.Kind != frontend.KindNumber && param.Type.Kind != frontend.KindBoolean) {
			return nil, l.fail(param.Start, "lower.bad-type", "参数 %q 不是 number 或 boolean，本阶段不支持", param.Name)
		}
		fn.Params = append(fn.Params, nir.Param{Name: param.Name, Type: nirType(param.Type.Kind), Pos: l.pos(param.Start)})
		sc.params[id] = param.Name
	}
	if decl.Body == nil {
		return nil, l.fail(decl.Start, "lower.missing-body", "函数 %q 没有函数体", decl.Name)
	}
	l.result = fn.Result
	defer func() { l.result = "" }()
	body, err := l.block(decl.Body, sc, fn.Result)
	if err != nil {
		return nil, err
	}
	fn.Body = body
	return fn, nil
}

// entry 把模块体降低成入口函数：顶层 const 按源码顺序求值并绑定成局部值。
func (l *lowerer) entry() (*nir.Function, error) {
	fn := &nir.Function{Name: EntryName, Result: nir.TypeVoid}
	sc := &scope{params: map[int]string{}, locals: map[int]string{}}
	if l.facts.Module == nil {
		fn.Body = []nir.Stmt{{Op: nir.OpReturn}}
		return fn, nil
	}
	for i := range l.facts.Module.Children {
		stmt := &l.facts.Module.Children[i]
		switch stmt.Kind {
		case "Const":
			decl, ok := l.facts.Declaration(stmt.Decl)
			if !ok || decl.Kind != frontend.DeclConst {
				return nil, l.fail(stmt.Start, "lower.unknown-const", "模块语句引用的常量 ID %d 不存在", stmt.Decl)
			}
			if _, exists := sc.locals[decl.ID]; exists {
				return nil, l.fail(stmt.Start, "lower.duplicate-const", "常量 %q 重复定义", decl.Name)
			}
			if decl.Body == nil {
				return nil, l.fail(stmt.Start, "lower.missing-value", "常量 %q 没有初始值", decl.Name)
			}
			if decl.Type == nil {
				return nil, l.fail(stmt.Start, "lower.bad-type", "常量 %q 缺少类型摘要", decl.Name)
			}
			if decl.Type.Kind != frontend.KindNumber && decl.Type.Kind != frontend.KindBoolean {
				return nil, l.fail(stmt.Start, "lower.bad-type", "常量 %q 是 %s，本阶段只支持 number 与 boolean", decl.Name, decl.Type.Kind)
			}
			if err := l.typedOperand(decl.Body, decl.Type.Kind, "常量 "+decl.Name+" "); err != nil {
				return nil, err
			}
			value, pre, err := l.expr(decl.Body, sc, 1)
			if err != nil {
				return nil, err
			}
			fn.Body = append(fn.Body, pre...)
			fn.Body = append(fn.Body, nir.Stmt{Op: nir.OpLet, Name: decl.Name, Value: value, Pos: l.pos(stmt.Start)})
			sc.locals[decl.ID] = decl.Name
		case "ExpressionStatement":
			stmt, err := l.statement(stmt, sc)
			if err != nil {
				return nil, err
			}
			fn.Body = append(fn.Body, stmt...)
		default:
			return nil, l.fail(stmt.Start, "lower.unsupported-statement", "模块级语句 %s 不在本阶段子集内", stmt.Kind)
		}
	}
	fn.Body = append(fn.Body, nir.Stmt{Op: nir.OpReturn})
	return fn, nil
}

func (l *lowerer) block(block *frontend.Node, sc *scope, result string) ([]nir.Stmt, error) {
	body, err := l.blockStmts(block, sc)
	if err != nil {
		return nil, err
	}
	if result == nir.TypeVoid {
		// void 函数正常结束等价于一条空 return；如果分支已经覆盖所有路径，
		// 不再补一条不可达的 return。
		if !nir.BlockReturns(body) {
			body = append(body, nir.Stmt{Op: nir.OpReturn})
		}
		return body, nil
	}
	if !nir.BlockReturns(body) {
		pos := nir.FirstMissingReturn(body)
		return nil, l.failNir(pos, block.Start, "lower.missing-return", "返回 %s 的函数有路径没有 return", result)
	}
	return body, nil
}

// blockStmts 降低一个语句块，不负责补 return。if 的分支用它：分支里是否
// 返回由源码决定，补齐函数返回值是函数体自己的事。
func (l *lowerer) blockStmts(block *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	if block.Kind != "Block" {
		return nil, l.fail(block.Start, "lower.bad-body", "语句块是 %s，应为 Block", block.Kind)
	}
	var body []nir.Stmt
	for i := range block.Children {
		stmt, err := l.statement(&block.Children[i], sc)
		if err != nil {
			return nil, err
		}
		body = append(body, stmt...)
	}
	return body, nil
}

func (l *lowerer) statement(stmt *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	switch stmt.Kind {
	case "ReturnStatement":
		if len(stmt.Children) == 0 {
			if l.result != nir.TypeVoid {
				return nil, l.fail(stmt.Start, "lower.missing-value", "返回 %s 的函数里 return 必须带值", l.result)
			}
			return []nir.Stmt{{Op: nir.OpReturn, Pos: l.pos(stmt.Start)}}, nil
		}
		if len(stmt.Children) > 1 {
			return nil, l.fail(stmt.Start, "lower.bad-arity", "return 有 %d 个值", len(stmt.Children))
		}
		if l.result == nir.TypeVoid {
			return nil, l.fail(stmt.Start, "lower.void-value", "void 函数的 return 不能带返回值")
		}
		if err := l.typedOperand(&stmt.Children[0], kindOf(l.result), "返回值"); err != nil {
			return nil, err
		}
		value, pre, err := l.expr(&stmt.Children[0], sc, 1)
		if err != nil {
			return nil, err
		}
		return append(pre, nir.Stmt{Op: nir.OpReturn, Value: value, Pos: l.pos(stmt.Start)}), nil
	case "ExpressionStatement":
		if len(stmt.Children) != 1 {
			return nil, l.fail(stmt.Start, "lower.bad-arity", "表达式语句有 %d 个子节点", len(stmt.Children))
		}
		inner := &stmt.Children[0]
		arg, ok, err := l.consoleLog(inner)
		if err != nil {
			return nil, err
		}
		if ok {
			if arg.Type == nil || (arg.Type.Kind != frontend.KindNumber && arg.Type.Kind != frontend.KindBoolean) {
				return nil, l.fail(arg.Start, "lower.bad-type", "console.log 的参数不是 number 或 boolean")
			}
			value, pre, err := l.expr(arg, sc, 1)
			if err != nil {
				return nil, err
			}
			return append(pre, nir.Stmt{Op: nir.OpPrint, Value: value, Pos: l.pos(stmt.Start)}), nil
		}
		// 其余表达式语句求值后丢弃结果：`f();` 的副作用要保留，`answer;`
		// 不打印任何东西。两条要求都落在 discard 上。
		value, pre, err := l.expr(inner, sc, 1)
		if err != nil {
			return nil, err
		}
		return append(pre, nir.Stmt{Op: nir.OpDiscard, Value: value, Pos: l.pos(stmt.Start)}), nil
	case "IfStatement":
		return l.ifStatement(stmt, sc)
	case "VariableStatement":
		return l.localDeclaration(stmt, sc)
	case "Block":
		return nil, l.fail(stmt.Start, "lower.unsupported-statement", "独立语句块不在本阶段子集内")
	default:
		return nil, l.fail(stmt.Start, "lower.unsupported-statement", "语句 %s 不在本阶段子集内", stmt.Kind)
	}
}

// ifStatement 降低 if / else / else if。条件在调用方的作用域里求值一次；
// 两条分支各自开一个子作用域，分支里的 let 不会泄漏到外面。
func (l *lowerer) ifStatement(stmt *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	if len(stmt.Children) < 2 || len(stmt.Children) > 3 {
		return nil, l.fail(stmt.Start, "lower.bad-arity", "if 有 %d 个子节点", len(stmt.Children))
	}
	condNode := &stmt.Children[0]
	if err := l.typedOperand(condNode, frontend.KindBoolean, "if 的条件"); err != nil {
		return nil, err
	}
	cond, pre, err := l.expr(condNode, sc, 1)
	if err != nil {
		return nil, err
	}
	then, err := l.branchStmts(&stmt.Children[1], sc.child())
	if err != nil {
		return nil, err
	}
	var els []nir.Stmt
	if len(stmt.Children) == 3 {
		els, err = l.branchStmts(&stmt.Children[2], sc.child())
		if err != nil {
			return nil, err
		}
	}
	return append(pre, nir.Stmt{Op: nir.OpIf, Cond: cond, Then: then, Else: els, Pos: l.pos(stmt.Start)}), nil
}

// branchStmts 降低一条分支。带花括号的分支是 Block；单条语句（包括 else if）
// 也合法，包成只有一个元素的语句列表。
func (l *lowerer) branchStmts(node *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	if node.Kind == "Block" {
		return l.blockStmts(node, sc)
	}
	return l.statement(node, sc)
}

// localDeclaration 降低分支里的局部 const。骨架是
// VariableStatement → VariableDeclarationList → VariableDeclaration，声明本身
// 只带一个初始化表达式。
func (l *lowerer) localDeclaration(stmt *frontend.Node, sc *scope) ([]nir.Stmt, error) {
	list := variableList(stmt)
	if list == nil {
		return nil, l.fail(stmt.Start, "lower.bad-declaration", "局部变量语句缺少声明列表")
	}
	if len(list.Children) != 1 || list.Children[0].Kind != "VariableDeclaration" {
		return nil, l.fail(stmt.Start, "lower.bad-declaration", "本阶段只支持一次声明一个局部值")
	}
	declNode := &list.Children[0]
	if declNode.Decl == 0 {
		return nil, l.fail(declNode.Start, "lower.bad-declaration", "局部变量缺少声明身份；只支持 const")
	}
	decl, ok := l.facts.Declaration(declNode.Decl)
	if !ok || decl.Kind != frontend.DeclLocal {
		return nil, l.fail(declNode.Start, "lower.bad-declaration", "局部变量的声明身份 %d 不对", declNode.Decl)
	}
	if decl.Type == nil || (decl.Type.Kind != frontend.KindNumber && decl.Type.Kind != frontend.KindBoolean) {
		return nil, l.fail(decl.Start, "lower.bad-type", "局部值 %q 不是 number 或 boolean", decl.Name)
	}
	if len(declNode.Children) != 1 {
		return nil, l.fail(declNode.Start, "lower.missing-value", "局部值 %q 缺少初始值", decl.Name)
	}
	init := &declNode.Children[0]
	if err := l.typedOperand(init, decl.Type.Kind, "局部值 "+decl.Name); err != nil {
		return nil, err
	}
	value, pre, err := l.expr(init, sc, 1)
	if err != nil {
		return nil, err
	}
	if !sc.define(decl.ID, decl.Name) {
		return nil, l.fail(decl.Start, "lower.duplicate-name", "局部值 %q 在同一个块里重复定义", decl.Name)
	}
	return append(pre, nir.Stmt{Op: nir.OpLet, Name: decl.Name, Value: value, Pos: l.pos(stmt.Start)}), nil
}

func variableList(stmt *frontend.Node) *frontend.Node {
	if stmt.Kind != "VariableStatement" {
		return nil
	}
	for i := range stmt.Children {
		if stmt.Children[i].Kind == "VariableDeclarationList" {
			return &stmt.Children[i]
		}
	}
	return nil
}

// consoleLog 识别 console.log(x) 并返回实参。ok=false 表示这不是一次受限输出。
func (l *lowerer) consoleLog(call *frontend.Node) (*frontend.Node, bool, error) {
	if call.Kind != "CallExpression" || len(call.Children) != 2 {
		return nil, false, nil
	}
	callee := &call.Children[0]
	if callee.Kind != "PropertyAccessExpression" || callee.Name != "log" || len(callee.Children) != 1 {
		return nil, false, nil
	}
	base := &callee.Children[0]
	if base.Kind != "Identifier" || base.Name != "console" || base.Target != 0 {
		return nil, false, nil
	}
	return &call.Children[1], true, nil
}

func (l *lowerer) expr(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if n == nil {
		return nil, nil, l.fail(0, "lower.missing-expr", "表达式为空")
	}
	if depth > maxDepth {
		return nil, nil, l.fail(n.Start, "lower.too-deep", "表达式嵌套超过 %d 层", maxDepth)
	}
	switch n.Kind {
	case "Identifier":
		e, err := l.identifier(n, sc)
		return e, nil, err
	case "NumericLiteral":
		if n.Bits == "" {
			return nil, nil, l.fail(n.Start, "lower.bad-literal", "数字字面量缺少位模式")
		}
		return &nir.Expr{Op: nir.OpNumber, Bits: n.Bits, Pos: l.pos(n.Start)}, nil, nil
	case "TrueKeyword", "FalseKeyword":
		v := n.Kind == "TrueKeyword"
		return &nir.Expr{Op: nir.OpBool, Value: &v, Pos: l.pos(n.Start)}, nil, nil
	case "BinaryExpression":
		if n.Op == "&&" || n.Op == "||" {
			return l.shortCircuit(n, sc, depth)
		}
		if isCompare(n.Op) {
			return l.compare(n, sc, depth)
		}
		if !isArithmetic(n.Op) {
			return nil, nil, l.fail(n.Start, "lower.bad-operator", "运算符 %q 不在 + - * / 与比较之内", n.Op)
		}
		if len(n.Children) != 2 {
			return nil, nil, l.fail(n.Start, "lower.bad-arity", "二元运算有 %d 个操作数", len(n.Children))
		}
		for i := range n.Children {
			if err := l.typedOperand(&n.Children[i], frontend.KindNumber, "运算符 "+n.Op+" 的操作数"); err != nil {
				return nil, nil, err
			}
		}
		left, leftPre, err := l.expr(&n.Children[0], sc, depth+1)
		if err != nil {
			return nil, nil, err
		}
		right, rightPre, err := l.expr(&n.Children[1], sc, depth+1)
		if err != nil {
			return nil, nil, err
		}
		pre := append(leftPre, rightPre...)
		return &nir.Expr{Op: nir.OpBinary, Operator: n.Op, Left: left, Right: right, Pos: l.pos(n.Start)}, pre, nil
	case "PrefixUnaryExpression":
		return l.unary(n, sc, depth)
	case "ConditionalExpression":
		return l.conditional(n, sc, depth)
	case "CallExpression":
		return l.call(n, sc, depth)
	default:
		return nil, nil, l.fail(n.Start, "lower.unsupported-expr", "表达式 %s 不在本阶段子集内", n.Kind)
	}
}

func (l *lowerer) identifier(n *frontend.Node, sc *scope) (*nir.Expr, error) {
	decl, ok := l.facts.Declaration(n.Target)
	if !ok || n.Target == 0 {
		return nil, l.fail(n.Start, "lower.unknown-name", "名称 %q 不是本文件里的声明", n.Name)
	}
	switch decl.Kind {
	case frontend.DeclParameter:
		name, ok := sc.lookupParam(decl.ID)
		if !ok {
			return nil, l.fail(n.Start, "lower.unknown-name", "参数 %q 不属于当前函数", n.Name)
		}
		return &nir.Expr{Op: nir.OpParam, Name: name, Pos: l.pos(n.Start)}, nil
	case frontend.DeclConst:
		name, ok := sc.lookup(decl.ID)
		if !ok {
			return nil, l.fail(n.Start, "lower.module-const-in-function", "模块常量 %q 在函数内不可见", n.Name)
		}
		return &nir.Expr{Op: nir.OpLocal, Name: name, Pos: l.pos(n.Start)}, nil
	case frontend.DeclLocal:
		name, ok := sc.lookup(decl.ID)
		if !ok {
			return nil, l.fail(n.Start, "lower.unknown-name", "局部值 %q 不在当前作用域内", n.Name)
		}
		return &nir.Expr{Op: nir.OpLocal, Name: name, Pos: l.pos(n.Start)}, nil
	default:
		return nil, l.fail(n.Start, "lower.function-value", "函数 %q 作为值使用", n.Name)
	}
}

// compare 降低有序比较与严格相等。有序比较只接受 number；严格相等要求两侧
// 是同一个标量类型。操作数顺序原样保留，求值顺序由后端负责。
func (l *lowerer) compare(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if len(n.Children) != 2 {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "比较运算有 %d 个操作数", len(n.Children))
	}
	leftNode, rightNode := &n.Children[0], &n.Children[1]
	if nir.IsOrderingOperator(n.Op) {
		if err := l.typedOperand(leftNode, frontend.KindNumber, "比较的左操作数"); err != nil {
			return nil, nil, err
		}
		if err := l.typedOperand(rightNode, frontend.KindNumber, "比较的右操作数"); err != nil {
			return nil, nil, err
		}
	} else {
		if leftNode.Type == nil || rightNode.Type == nil {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "严格相等的操作数缺少类型摘要")
		}
		if leftNode.Type.Kind != rightNode.Type.Kind {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "严格相等需要两侧同类型，实际为 %s 与 %s", leftNode.Type.Kind, rightNode.Type.Kind)
		}
		if leftNode.Type.Kind != frontend.KindNumber && leftNode.Type.Kind != frontend.KindBoolean {
			return nil, nil, l.fail(n.Start, "lower.bad-type", "严格相等只支持 number 或 boolean，实际为 %s", leftNode.Type.Kind)
		}
	}
	left, leftPre, err := l.expr(leftNode, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	right, rightPre, err := l.expr(rightNode, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	pre := append(leftPre, rightPre...)
	return &nir.Expr{Op: nir.OpCompare, Operator: n.Op, Left: left, Right: right, Pos: l.pos(n.Start)}, pre, nil
}

// unary 处理一元运算。`!` 直接降成 not；一元正负号只接受数字字面量，并在
// 降低时折成位模式。
//
// 这样 -0 会被写成 0x8000000000000000，而不是「0 再取负」：后者依赖运行时的
// 取负语义，而常量折叠在编译期就可能把符号位抹掉。
func (l *lowerer) unary(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if len(n.Children) != 1 {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "一元运算有 %d 个操作数", len(n.Children))
	}
	operand := &n.Children[0]
	if n.Op == "!" {
		if err := l.typedOperand(operand, frontend.KindBoolean, "! 的操作数"); err != nil {
			return nil, nil, err
		}
		value, pre, err := l.expr(operand, sc, depth+1)
		if err != nil {
			return nil, nil, err
		}
		return &nir.Expr{Op: nir.OpNot, Operand: value, Pos: l.pos(n.Start)}, pre, nil
	}
	if n.Op != "-" && n.Op != "+" {
		return nil, nil, l.fail(n.Start, "lower.bad-operator", "一元运算符 %q 不在本阶段子集内", n.Op)
	}
	if operand.Kind != "NumericLiteral" || operand.Bits == "" {
		return nil, nil, l.fail(n.Start, "lower.bad-unary", "一元 %q 只支持数字字面量", n.Op)
	}
	bits, err := parseBits(operand.Bits)
	if err != nil {
		return nil, nil, l.fail(n.Start, "lower.bad-literal", "位模式 %q 不合法", operand.Bits)
	}
	if n.Op == "-" {
		bits ^= 1 << 63
	}
	return &nir.Expr{Op: nir.OpNumber, Bits: formatBits(bits), Pos: l.pos(n.Start)}, nil, nil
}

func (l *lowerer) call(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if len(n.Children) == 0 {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "调用没有实参列表")
	}
	callee := &n.Children[0]
	if callee.Kind != "Identifier" {
		return nil, nil, l.fail(n.Start, "lower.unknown-callee", "调用目标不是直接函数名")
	}
	decl, ok := l.facts.Declaration(callee.Target)
	if !ok || decl.Kind != frontend.DeclFunction {
		return nil, nil, l.fail(n.Start, "lower.unknown-callee", "调用目标 %q 不是本文件里的函数", callee.Name)
	}
	args := n.Children[1:]
	if len(args) != len(decl.Parameters) {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "调用 %q 给了 %d 个实参，声明有 %d 个参数", decl.Name, len(args), len(decl.Parameters))
	}
	exprs := make([]nir.Expr, len(args))
	var pre []nir.Stmt
	var pending []int
	for i := range args {
		param, ok := l.facts.Declaration(decl.Parameters[i])
		if !ok || param.Type == nil {
			return nil, nil, l.fail(n.Start, "lower.unknown-param", "函数 %q 的第 %d 个参数没有类型摘要", decl.Name, i+1)
		}
		if err := l.typedOperand(&args[i], param.Type.Kind, "调用的实参"); err != nil {
			return nil, nil, err
		}
		arg, argPre, err := l.expr(&args[i], sc, depth+1)
		if err != nil {
			return nil, nil, err
		}
		exprs[i] = *arg
		if len(argPre) == 0 {
			pending = append(pending, i)
			continue
		}
		// 这个实参带准备工作。先把它之前还没物化的实参按顺序算出来，
		// 再执行准备工作，最后物化它自己——否则后面的副作用会跑到前面。
		for _, j := range pending {
			name := l.tempName()
			value := exprs[j]
			pre = append(pre, nir.Stmt{Op: nir.OpLet, Name: name, Value: &value, Pos: l.pos(args[j].Start)})
			exprs[j] = nir.Expr{Op: nir.OpLocal, Name: name}
		}
		pending = nil
		pre = append(pre, argPre...)
		name := l.tempName()
		value := exprs[i]
		pre = append(pre, nir.Stmt{Op: nir.OpLet, Name: name, Value: &value, Pos: l.pos(args[i].Start)})
		exprs[i] = nir.Expr{Op: nir.OpLocal, Name: name}
	}
	return &nir.Expr{Op: nir.OpCall, Function: decl.Name, Args: exprs, Pos: l.pos(n.Start)}, pre, nil
}

// shortCircuit 降低 && 与 ||。左侧的准备工作在外层执行；右侧的准备工作与
// 右值一起放进 short 的 right 列表，只有真正需要求值右侧时才执行。结果绑定
// 到一个临时值，后续表达式用 local 读取它。
func (l *lowerer) shortCircuit(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if len(n.Children) != 2 {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "短路运算有 %d 个操作数", len(n.Children))
	}
	leftNode, rightNode := &n.Children[0], &n.Children[1]
	if err := l.typedOperand(leftNode, frontend.KindBoolean, "运算符 "+n.Op+" 的左操作数"); err != nil {
		return nil, nil, err
	}
	if err := l.typedOperand(rightNode, frontend.KindBoolean, "运算符 "+n.Op+" 的右操作数"); err != nil {
		return nil, nil, err
	}
	left, leftPre, err := l.expr(leftNode, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	right, rightPre, err := l.expr(rightNode, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	name := l.tempName()
	pre := append(leftPre, nir.Stmt{
		Op:       nir.OpShort,
		Name:     name,
		Operator: n.Op,
		Left:     left,
		Right:    rightPre,
		Value:    right,
		Pos:      l.pos(n.Start),
	})
	return &nir.Expr{Op: nir.OpLocal, Name: name, Pos: l.pos(n.Start)}, pre, nil
}

func (l *lowerer) tempName() string {
	l.temp++
	return fmt.Sprintf("_tmp_%d", l.temp)
}

// conditional 降低 cond ? then : else。条件在外层求值一次；两条分支各自带
// 准备语句，只有被选中的一条执行，结果绑定到临时值。
func (l *lowerer) conditional(n *frontend.Node, sc *scope, depth int) (*nir.Expr, []nir.Stmt, error) {
	if len(n.Children) != 3 {
		return nil, nil, l.fail(n.Start, "lower.bad-arity", "三元式有 %d 个子节点", len(n.Children))
	}
	condNode, thenNode, elseNode := &n.Children[0], &n.Children[1], &n.Children[2]
	if err := l.typedOperand(condNode, frontend.KindBoolean, "三元式的条件"); err != nil {
		return nil, nil, err
	}
	if thenNode.Type == nil || elseNode.Type == nil {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "三元式的分支缺少类型摘要")
	}
	if thenNode.Type.Kind != elseNode.Type.Kind {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "三元式两条分支类型不同：%s 与 %s", thenNode.Type.Kind, elseNode.Type.Kind)
	}
	if thenNode.Type.Kind != frontend.KindNumber && thenNode.Type.Kind != frontend.KindBoolean {
		return nil, nil, l.fail(n.Start, "lower.bad-type", "三元式的结果不能是 %s", thenNode.Type.Kind)
	}
	cond, condPre, err := l.expr(condNode, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	then, thenPre, err := l.expr(thenNode, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	els, elsePre, err := l.expr(elseNode, sc, depth+1)
	if err != nil {
		return nil, nil, err
	}
	name := l.tempName()
	resultType := nir.TypeNumber
	if thenNode.Type.Kind == frontend.KindBoolean {
		resultType = nir.TypeBool
	}
	pre := append(condPre, nir.Stmt{
		Op:        nir.OpSelect,
		Name:      name,
		Cond:      cond,
		Then:      thenPre,
		ThenValue: then,
		Else:      elsePre,
		ElseValue: els,
		Type:      resultType,
		Pos:       l.pos(n.Start),
	})
	return &nir.Expr{Op: nir.OpLocal, Name: name, Pos: l.pos(n.Start)}, pre, nil
}

// typedOperand 检查一个表达式的类型摘要是不是 want。降低不重新推导类型，
// 但也不能盲信：事实里的类型摘要与结构对不上时，生成的 NIR 会通不过验证，
// 到那时再报错已经离原因很远了。
func (l *lowerer) typedOperand(n *frontend.Node, want frontend.Kind, what string) error {
	if n == nil || n.Type == nil {
		return l.fail(nodeStart(n), "lower.bad-type", "%s 缺少类型摘要", what)
	}
	if n.Type.Kind != want {
		return l.fail(n.Start, "lower.bad-type", "%s是 %s，需要 %s", what, n.Type.Kind, want)
	}
	return nil
}

func nodeStart(n *frontend.Node) int {
	if n == nil {
		return 0
	}
	return n.Start
}

func (l *lowerer) pos(offset int) *nir.Pos {
	pos, err := l.snapshot.Position(offset)
	if err != nil {
		return nil
	}
	return &nir.Pos{File: l.snapshot.Path(), Line: pos.Line, Column: pos.Column}
}

func (l *lowerer) fail(offset int, code, format string, args ...any) error {
	d := diagnostic.New(diagnostic.Internal, code, format, args...)
	if l.snapshot != nil {
		if pos, err := l.snapshot.Position(offset); err == nil {
			d.At(l.snapshot.Path(), pos.Line, pos.Column)
		}
	}
	return d
}

// failNir 用 NIR 位置报错。返回路径的缺口来自 NIR 语句，位置已经由降低填好；
// 没有位置时退回源码偏移。
func (l *lowerer) failNir(pos *nir.Pos, offset int, code, format string, args ...any) error {
	if pos != nil {
		return diagnostic.New(diagnostic.Internal, code, format, args...).At(pos.File, pos.Line, pos.Column)
	}
	return l.fail(offset, code, format, args...)
}

func nirType(k frontend.Kind) string {
	switch k {
	case frontend.KindVoid:
		return nir.TypeVoid
	case frontend.KindBoolean:
		return nir.TypeBool
	}
	return nir.TypeNumber
}

// kindOf 把 NIR 类型换回事实里的类型类别，用于核对返回值。
func kindOf(t string) frontend.Kind {
	if t == nir.TypeBool {
		return frontend.KindBoolean
	}
	return frontend.KindNumber
}

func isArithmetic(op string) bool {
	switch op {
	case "+", "-", "*", "/":
		return true
	}
	return false
}

func isCompare(op string) bool {
	return nir.IsCompareOperator(op)
}

func parseBits(s string) (uint64, error) {
	var bits uint64
	if _, err := fmt.Sscanf(s, "0x%016x", &bits); err != nil {
		return 0, err
	}
	return bits, nil
}

func formatBits(bits uint64) string {
	return fmt.Sprintf("0x%016x", bits)
}
