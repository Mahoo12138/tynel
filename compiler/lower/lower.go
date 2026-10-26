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
}

// scope 是当前可见的声明。用声明 ID 而不是名字做键：两个函数里同名的参数是
// 两个声明，按名字查会把它们混成一个。
type scope struct {
	params map[int]string
	locals map[int]string
}

func (l *lowerer) function(decl *frontend.Declaration) (*nir.Function, error) {
	if decl.Result == nil {
		return nil, l.fail(decl.Start, "lower.bad-type", "函数 %q 缺少返回类型摘要", decl.Name)
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
		fn.Params = append(fn.Params, nir.Param{Name: param.Name, Type: nir.TypeNumber, Pos: l.pos(param.Start)})
		sc.params[id] = param.Name
	}
	if decl.Body == nil {
		return nil, l.fail(decl.Start, "lower.missing-body", "函数 %q 没有函数体", decl.Name)
	}
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
			if err := l.numberOperand(decl.Body, "常量 "+decl.Name); err != nil {
				return nil, err
			}
			value, err := l.expr(decl.Body, sc, 1)
			if err != nil {
				return nil, err
			}
			fn.Body = append(fn.Body, nir.Stmt{Op: nir.OpLet, Name: decl.Name, Value: value, Pos: l.pos(stmt.Start)})
			sc.locals[decl.ID] = decl.Name
		case "ExpressionStatement":
			stmt, err := l.statement(stmt, sc)
			if err != nil {
				return nil, err
			}
			fn.Body = append(fn.Body, stmt)
		default:
			return nil, l.fail(stmt.Start, "lower.unsupported-statement", "模块级语句 %s 不在本阶段子集内", stmt.Kind)
		}
	}
	fn.Body = append(fn.Body, nir.Stmt{Op: nir.OpReturn})
	return fn, nil
}

func (l *lowerer) block(block *frontend.Node, sc *scope, result string) ([]nir.Stmt, error) {
	if block.Kind != "Block" {
		return nil, l.fail(block.Start, "lower.bad-body", "函数体是 %s，应为 Block", block.Kind)
	}
	var body []nir.Stmt
	for i := range block.Children {
		stmt, err := l.statement(&block.Children[i], sc)
		if err != nil {
			return nil, err
		}
		body = append(body, stmt)
	}
	if result == nir.TypeVoid {
		if len(body) == 0 || body[len(body)-1].Op != nir.OpReturn {
			body = append(body, nir.Stmt{Op: nir.OpReturn})
		}
		return body, nil
	}
	if len(body) == 0 || body[len(body)-1].Op != nir.OpReturn {
		return nil, l.fail(block.Start, "lower.missing-return", "返回 %s 的函数没有以 return 结束", result)
	}
	return body, nil
}

func (l *lowerer) statement(stmt *frontend.Node, sc *scope) (nir.Stmt, error) {
	switch stmt.Kind {
	case "ReturnStatement":
		if len(stmt.Children) == 0 {
			return nir.Stmt{Op: nir.OpReturn, Pos: l.pos(stmt.Start)}, nil
		}
		if len(stmt.Children) > 1 {
			return nir.Stmt{}, l.fail(stmt.Start, "lower.bad-arity", "return 有 %d 个值", len(stmt.Children))
		}
		if err := l.numberOperand(&stmt.Children[0], "返回值"); err != nil {
			return nir.Stmt{}, err
		}
		value, err := l.expr(&stmt.Children[0], sc, 1)
		if err != nil {
			return nir.Stmt{}, err
		}
		return nir.Stmt{Op: nir.OpReturn, Value: value, Pos: l.pos(stmt.Start)}, nil
	case "ExpressionStatement":
		if len(stmt.Children) != 1 {
			return nir.Stmt{}, l.fail(stmt.Start, "lower.bad-arity", "表达式语句有 %d 个子节点", len(stmt.Children))
		}
		arg, ok, err := l.consoleLog(&stmt.Children[0])
		if err != nil {
			return nir.Stmt{}, err
		}
		if !ok {
			return nir.Stmt{}, l.fail(stmt.Start, "lower.unsupported-statement", "只有 console.log 可以单独成句")
		}
		value, err := l.expr(arg, sc, 1)
		if err != nil {
			return nir.Stmt{}, err
		}
		return nir.Stmt{Op: nir.OpPrint, Value: value, Pos: l.pos(stmt.Start)}, nil
	default:
		return nir.Stmt{}, l.fail(stmt.Start, "lower.unsupported-statement", "语句 %s 不在本阶段子集内", stmt.Kind)
	}
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

func (l *lowerer) expr(n *frontend.Node, sc *scope, depth int) (*nir.Expr, error) {
	if n == nil {
		return nil, l.fail(0, "lower.missing-expr", "表达式为空")
	}
	if depth > maxDepth {
		return nil, l.fail(n.Start, "lower.too-deep", "表达式嵌套超过 %d 层", maxDepth)
	}
	switch n.Kind {
	case "Identifier":
		return l.identifier(n, sc)
	case "NumericLiteral":
		if n.Bits == "" {
			return nil, l.fail(n.Start, "lower.bad-literal", "数字字面量缺少位模式")
		}
		return &nir.Expr{Op: nir.OpNumber, Bits: n.Bits, Pos: l.pos(n.Start)}, nil
	case "BinaryExpression":
		if !isArithmetic(n.Op) {
			return nil, l.fail(n.Start, "lower.bad-operator", "运算符 %q 不在 + - * / 之内", n.Op)
		}
		if len(n.Children) != 2 {
			return nil, l.fail(n.Start, "lower.bad-arity", "二元运算有 %d 个操作数", len(n.Children))
		}
		for i := range n.Children {
			if err := l.numberOperand(&n.Children[i], "运算符 "+n.Op+" 的操作数"); err != nil {
				return nil, err
			}
		}
		left, err := l.expr(&n.Children[0], sc, depth+1)
		if err != nil {
			return nil, err
		}
		right, err := l.expr(&n.Children[1], sc, depth+1)
		if err != nil {
			return nil, err
		}
		return &nir.Expr{Op: nir.OpBinary, Operator: n.Op, Left: left, Right: right, Pos: l.pos(n.Start)}, nil
	case "PrefixUnaryExpression":
		return l.unary(n, sc, depth)
	case "CallExpression":
		return l.call(n, sc, depth)
	default:
		return nil, l.fail(n.Start, "lower.unsupported-expr", "表达式 %s 不在本阶段子集内", n.Kind)
	}
}

func (l *lowerer) identifier(n *frontend.Node, sc *scope) (*nir.Expr, error) {
	decl, ok := l.facts.Declaration(n.Target)
	if !ok || n.Target == 0 {
		return nil, l.fail(n.Start, "lower.unknown-name", "名称 %q 不是本文件里的声明", n.Name)
	}
	switch decl.Kind {
	case frontend.DeclParameter:
		name, ok := sc.params[decl.ID]
		if !ok {
			return nil, l.fail(n.Start, "lower.unknown-name", "参数 %q 不属于当前函数", n.Name)
		}
		return &nir.Expr{Op: nir.OpParam, Name: name, Pos: l.pos(n.Start)}, nil
	case frontend.DeclConst:
		name, ok := sc.locals[decl.ID]
		if !ok {
			return nil, l.fail(n.Start, "lower.module-const-in-function", "模块常量 %q 在函数内不可见", n.Name)
		}
		return &nir.Expr{Op: nir.OpLocal, Name: name, Pos: l.pos(n.Start)}, nil
	default:
		return nil, l.fail(n.Start, "lower.function-value", "函数 %q 作为值使用", n.Name)
	}
}

// unary 只接受作用在数字字面量上的一元正负号，并在降低时折成位模式。
//
// 这样 -0 会被写成 0x8000000000000000，而不是「0 再取负」：后者依赖运行时的
// 取负语义，而常量折叠在编译期就可能把符号位抹掉。
func (l *lowerer) unary(n *frontend.Node, sc *scope, depth int) (*nir.Expr, error) {
	if n.Op != "-" && n.Op != "+" {
		return nil, l.fail(n.Start, "lower.bad-operator", "一元运算符 %q 不在本阶段子集内", n.Op)
	}
	if len(n.Children) != 1 {
		return nil, l.fail(n.Start, "lower.bad-arity", "一元运算有 %d 个操作数", len(n.Children))
	}
	operand := &n.Children[0]
	if operand.Kind != "NumericLiteral" || operand.Bits == "" {
		return nil, l.fail(n.Start, "lower.bad-unary", "一元 %q 只支持数字字面量", n.Op)
	}
	bits, err := parseBits(operand.Bits)
	if err != nil {
		return nil, l.fail(n.Start, "lower.bad-literal", "位模式 %q 不合法", operand.Bits)
	}
	if n.Op == "-" {
		bits ^= 1 << 63
	}
	return &nir.Expr{Op: nir.OpNumber, Bits: formatBits(bits), Pos: l.pos(n.Start)}, nil
}

func (l *lowerer) call(n *frontend.Node, sc *scope, depth int) (*nir.Expr, error) {
	if len(n.Children) == 0 {
		return nil, l.fail(n.Start, "lower.bad-arity", "调用没有实参列表")
	}
	callee := &n.Children[0]
	if callee.Kind != "Identifier" {
		return nil, l.fail(n.Start, "lower.unknown-callee", "调用目标不是直接函数名")
	}
	decl, ok := l.facts.Declaration(callee.Target)
	if !ok || decl.Kind != frontend.DeclFunction {
		return nil, l.fail(n.Start, "lower.unknown-callee", "调用目标 %q 不是本文件里的函数", callee.Name)
	}
	args := n.Children[1:]
	if len(args) != len(decl.Parameters) {
		return nil, l.fail(n.Start, "lower.bad-arity", "调用 %q 给了 %d 个实参，声明有 %d 个参数", decl.Name, len(args), len(decl.Parameters))
	}
	expr := &nir.Expr{Op: nir.OpCall, Function: decl.Name, Pos: l.pos(n.Start)}
	for i := range args {
		if err := l.numberOperand(&args[i], "调用的实参"); err != nil {
			return nil, err
		}
		arg, err := l.expr(&args[i], sc, depth+1)
		if err != nil {
			return nil, err
		}
		expr.Args = append(expr.Args, *arg)
	}
	return expr, nil
}

// numberOperand 检查一个表达式的类型摘要是不是 number。降低不重新推导类型，
// 但也不能盲信：事实里的类型摘要与结构对不上时，生成的 NIR 会通不过验证，
// 到那时再报错已经离原因很远了。
func (l *lowerer) numberOperand(n *frontend.Node, what string) error {
	if n == nil || n.Type == nil {
		return l.fail(nodeStart(n), "lower.bad-type", "%s 缺少类型摘要", what)
	}
	if n.Type.Kind != frontend.KindNumber {
		return l.fail(n.Start, "lower.bad-type", "%s 是 %s，本阶段只支持 number", what, n.Type.Kind)
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

func nirType(k frontend.Kind) string {
	if k == frontend.KindVoid {
		return nir.TypeVoid
	}
	return nir.TypeNumber
}

func isArithmetic(op string) bool {
	switch op {
	case "+", "-", "*", "/":
		return true
	}
	return false
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
