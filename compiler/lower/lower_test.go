package lower

import (
	"errors"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/nir"
	"github.com/Mahoo12138/tynel/compiler/source"
)

func snapshot(t *testing.T) *source.Snapshot {
	t.Helper()
	s, err := source.New("/tmp/t.ts", []byte(strings.Repeat("x", 512)), source.DefaultLimits())
	if err != nil {
		t.Fatalf("建立快照失败：%v", err)
	}
	return s
}

func node(kind string, children ...frontend.Node) frontend.Node {
	return frontend.Node{Kind: kind, Start: 10, Length: 1, Children: children}
}

func typed(kind string, k frontend.Kind, children ...frontend.Node) frontend.Node {
	n := node(kind, children...)
	n.Type = &frontend.Type{Kind: k}
	return n
}

func literal(bits string) frontend.Node {
	n := typed("NumericLiteral", frontend.KindNumber)
	n.Bits = bits
	return n
}

func ident(name string, target int) frontend.Node {
	n := typed("Identifier", frontend.KindNumber)
	n.Name = name
	n.Target = target
	return n
}

func boolIdent(name string, target int) frontend.Node {
	n := typed("Identifier", frontend.KindBoolean)
	n.Name = name
	n.Target = target
	return n
}

func boolLiteral(v bool) frontend.Node {
	kind := "FalseKeyword"
	if v {
		kind = "TrueKeyword"
	}
	return typed(kind, frontend.KindBoolean)
}

func notNode(operand frontend.Node) frontend.Node {
	n := typed("PrefixUnaryExpression", frontend.KindBoolean, operand)
	n.Op = "!"
	return n
}

func binary(op string, left, right frontend.Node) frontend.Node {
	n := typed("BinaryExpression", frontend.KindNumber, left, right)
	n.Op = op
	return n
}

func compareNode(op string, left, right frontend.Node) frontend.Node {
	n := typed("BinaryExpression", frontend.KindBoolean, left, right)
	n.Op = op
	return n
}

func consoleLog(arg frontend.Node) frontend.Node {
	access := typed("PropertyAccessExpression", frontend.KindVoid, ident("console", 0))
	access.Name = "log"
	return typed("CallExpression", frontend.KindVoid, access, arg)
}

// base 是一份完整可降低的事实：
//
//	function add(a, b) { return a + b; }
//	const answer = add(40, 2);
//	console.log(answer);
func base() *frontend.Facts {
	number := frontend.Type{Kind: frontend.KindNumber}
	addBody := node("Block", node("ReturnStatement", binary("+", ident("a", 2), ident("b", 3))))
	init := typed("CallExpression", frontend.KindNumber, ident("add", 1), literal("0x4044000000000000"), literal("0x4000000000000000"))
	constNode := node("Const")
	constNode.Decl = 4
	module := node("Block", constNode, node("ExpressionStatement", consoleLog(ident("answer", 4))))

	return &frontend.Facts{
		SchemaVersion: 1,
		Session:       "s",
		File:          "/tmp/t.ts",
		Module:        &module,
		Declarations: []frontend.Declaration{
			{ID: 1, Kind: frontend.DeclFunction, Name: "add", Start: 0, Length: 20, Result: &number, Parameters: []int{2, 3}, Body: &addBody},
			{ID: 2, Kind: frontend.DeclParameter, Name: "a", Start: 4, Length: 1, Type: &number},
			{ID: 3, Kind: frontend.DeclParameter, Name: "b", Start: 6, Length: 1, Type: &number},
			{ID: 4, Kind: frontend.DeclConst, Name: "answer", Start: 30, Length: 6, Type: &number, Body: &init},
		},
	}
}

func lowerFacts(t *testing.T, facts *frontend.Facts) *nir.Module {
	t.Helper()
	module, err := Lower(snapshot(t), facts)
	if err != nil {
		t.Fatalf("降低失败：%v", err)
	}
	return module
}

func TestLowersAddProgram(t *testing.T) {
	module := lowerFacts(t, base())
	if module.Entry != "main" {
		t.Fatalf("入口应为 main，实际 %q", module.Entry)
	}
	if len(module.Functions) != 2 {
		t.Fatalf("应有 2 个函数，实际 %d", len(module.Functions))
	}
	add := module.Functions[0]
	if add.Name != "add" || add.Result != nir.TypeNumber || len(add.Params) != 2 {
		t.Fatalf("add 的形状不对：%+v", add)
	}
	if got := add.Body[0]; got.Op != nir.OpReturn || got.Value == nil || got.Value.Op != nir.OpBinary || got.Value.Operator != "+" {
		t.Fatalf("add 的函数体不对：%+v", got)
	}

	main := module.Functions[1]
	if main.Name != "main" || main.Result != nir.TypeVoid || len(main.Params) != 0 {
		t.Fatalf("main 的形状不对：%+v", main)
	}
	if len(main.Body) != 3 {
		t.Fatalf("main 应有 3 条语句，实际 %d：%+v", len(main.Body), main.Body)
	}
	if let := main.Body[0]; let.Op != nir.OpLet || let.Name != "answer" || let.Value.Op != nir.OpCall {
		t.Fatalf("第一条应为 let answer = call add：%+v", let)
	}
	if print := main.Body[1]; print.Op != nir.OpPrint || print.Value.Op != nir.OpLocal || print.Value.Name != "answer" {
		t.Fatalf("第二条应为 print local answer：%+v", print)
	}
	if ret := main.Body[2]; ret.Op != nir.OpReturn || ret.Value != nil {
		t.Fatalf("最后应为空 return：%+v", ret)
	}
}

func TestNegatedLiteralKeepsSignBit(t *testing.T) {
	// 只有模块体，没有函数：console.log(-0) 应当直接折成负零的位模式。
	neg := typed("PrefixUnaryExpression", frontend.KindNumber, literal("0x0000000000000000"))
	neg.Op = "-"
	module := node("Block", node("ExpressionStatement", consoleLog(neg)))
	facts := &frontend.Facts{
		SchemaVersion: 1, Session: "s", File: "/tmp/t.ts", Module: &module,
	}
	lowered := lowerFacts(t, facts)
	main := lowered.Functions[0]
	print := main.Body[0]
	if print.Value.Bits != "0x8000000000000000" {
		t.Fatalf("负零的位模式应为 0x8000000000000000，实际 %q", print.Value.Bits)
	}
}

// TestVoidFunctionAndDiscardedStatements 覆盖本节点新增的两种语句形状：
// void 函数正常结束（补一条空 return），以及求值后丢弃结果的表达式语句。
func TestVoidFunctionAndDiscardedStatements(t *testing.T) {
	number := frontend.Type{Kind: frontend.KindNumber}
	void := frontend.Type{Kind: frontend.KindVoid}
	body := node("Block", node("ExpressionStatement", consoleLog(ident("n", 2))))
	init := literal("0x4044000000000000")
	constNode := node("Const")
	constNode.Decl = 3
	module := node("Block",
		constNode,
		node("ExpressionStatement", typed("CallExpression", frontend.KindVoid, ident("announce", 1), literal("0x401c000000000000"))),
		node("ExpressionStatement", ident("answer", 3)),
	)
	facts := &frontend.Facts{
		SchemaVersion: 1, Session: "s", File: "/tmp/t.ts", Module: &module,
		Declarations: []frontend.Declaration{
			{ID: 1, Kind: frontend.DeclFunction, Name: "announce", Start: 0, Length: 30, Result: &void, Parameters: []int{2}, Body: &body},
			{ID: 2, Kind: frontend.DeclParameter, Name: "n", Start: 10, Length: 1, Type: &number},
			{ID: 3, Kind: frontend.DeclConst, Name: "answer", Start: 40, Length: 6, Type: &number, Body: &init},
		},
	}
	lowered := lowerFacts(t, facts)

	announce := lowered.Functions[0]
	if announce.Result != nir.TypeVoid || len(announce.Body) != 2 {
		t.Fatalf("announce 应为 void 且有两条语句：%+v", announce)
	}
	if got := announce.Body[0]; got.Op != nir.OpPrint || got.Value.Op != nir.OpParam || got.Value.Name != "n" {
		t.Fatalf("第一条应为 print param n：%+v", got)
	}
	if got := announce.Body[1]; got.Op != nir.OpReturn || got.Value != nil {
		t.Fatalf("void 函数结尾应补一条空 return：%+v", got)
	}

	main := lowered.Functions[1]
	if len(main.Body) != 4 {
		t.Fatalf("main 应有 4 条语句，实际 %d：%+v", len(main.Body), main.Body)
	}
	if got := main.Body[0]; got.Op != nir.OpLet || got.Name != "answer" {
		t.Fatalf("第一条应为 let answer：%+v", got)
	}
	if got := main.Body[1]; got.Op != nir.OpDiscard || got.Value.Op != nir.OpCall || got.Value.Function != "announce" {
		t.Fatalf("第二条应为 discard 调用 announce：%+v", got)
	}
	if got := main.Body[2]; got.Op != nir.OpDiscard || got.Value.Op != nir.OpLocal || got.Value.Name != "answer" {
		t.Fatalf("第三条应为 discard 局部值 answer：%+v", got)
	}
}

func TestRejectsBadFacts(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*frontend.Facts)
		code   string
	}{
		{"二元运算操作数不是 number", func(f *frontend.Facts) {
			body := node("Block", node("ReturnStatement", typed("BinaryExpression", frontend.KindNumber, ident("a", 2), ident("b", 3))))
			body.Children[0].Children[0].Op = "+"
			body.Children[0].Children[0].Children[0].Type = &frontend.Type{Kind: frontend.KindString}
			f.Declarations[0].Body = &body
		}, "lower.bad-type"},
		{"二元运算缺操作数", func(f *frontend.Facts) {
			body := node("Block", node("ReturnStatement", typed("BinaryExpression", frontend.KindNumber, ident("a", 2))))
			body.Children[0].Children[0].Op = "+"
			f.Declarations[0].Body = &body
		}, "lower.bad-arity"},
		{"函数内引用模块常量", func(f *frontend.Facts) {
			body := node("Block", node("ReturnStatement", ident("answer", 4)))
			f.Declarations[0].Body = &body
		}, "lower.module-const-in-function"},
		{"引用不存在的声明", func(f *frontend.Facts) {
			body := node("Block", node("ReturnStatement", ident("ghost", 0)))
			f.Declarations[0].Body = &body
		}, "lower.unknown-name"},
		{"用户函数占用入口名", func(f *frontend.Facts) {
			f.Declarations[0].Name = "main"
		}, "lower.entry-conflict"},
		{"调用实参个数不符", func(f *frontend.Facts) {
			init := typed("CallExpression", frontend.KindNumber, ident("add", 1), literal("0x4044000000000000"))
			f.Declarations[3].Body = &init
		}, "lower.bad-arity"},
		{"函数体不以 return 结束", func(f *frontend.Facts) {
			body := node("Block")
			f.Declarations[0].Body = &body
		}, "lower.missing-return"},
		{"number 函数里 return 不带值", func(f *frontend.Facts) {
			body := node("Block", node("ReturnStatement"))
			f.Declarations[0].Body = &body
		}, "lower.missing-value"},
		{"void 函数里 return 带值", func(f *frontend.Facts) {
			void := frontend.Type{Kind: frontend.KindVoid}
			f.Declarations[0].Result = &void
			body := node("Block", node("ReturnStatement", binary("+", ident("a", 2), ident("b", 3))))
			f.Declarations[0].Body = &body
		}, "lower.void-value"},
		{"参数类型不是 number", func(f *frontend.Facts) {
			f.Declarations[1].Type = &frontend.Type{Kind: frontend.KindString}
		}, "lower.bad-type"},
		{"函数返回类型不是 number 或 void", func(f *frontend.Facts) {
			f.Declarations[0].Result = &frontend.Type{Kind: frontend.KindString}
		}, "lower.bad-type"},
		{"一元负号作用在参数上", func(f *frontend.Facts) {
			neg := typed("PrefixUnaryExpression", frontend.KindNumber, ident("a", 2))
			neg.Op = "-"
			body := node("Block", node("ReturnStatement", neg))
			f.Declarations[0].Body = &body
		}, "lower.bad-unary"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			facts := base()
			c.mutate(facts)
			module, err := Lower(snapshot(t), facts)
			if err == nil {
				t.Fatal("应被拒绝")
			}
			if module != nil {
				t.Fatal("失败时不应返回模块")
			}
			var d *diagnostic.Diagnostic
			if !errors.As(err, &d) || d.Code != c.code {
				t.Fatalf("错误码应为 %s，实际 %v", c.code, err)
			}
			if d.Category != diagnostic.Internal {
				t.Fatalf("降低失败属于内部故障，实际 %v", d.Category)
			}
		})
	}
}

func TestRejectsDeepExpression(t *testing.T) {
	// 校验先拦：150 层超过事实的 100 层预算。
	expr := literal("0x0000000000000000")
	for i := 0; i < 150; i++ {
		expr = binary("+", expr, literal("0x0000000000000000"))
	}
	body := node("Block", node("ReturnStatement", expr))
	facts := base()
	facts.Declarations[0].Body = &body

	module, err := Lower(snapshot(t), facts)
	if err == nil {
		t.Fatal("过深的表达式应被拒绝")
	}
	if module != nil {
		t.Fatal("失败时不应返回模块")
	}
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) || d.Code != "facts.body-too-deep" {
		t.Fatalf("应报 facts.body-too-deep，实际 %v", err)
	}
}

// booleanFacts 对应：
//
//	function invert(value: boolean): boolean { return !value; }
//	const flag: boolean = false;
//	console.log(invert(flag));
func booleanFacts() *frontend.Facts {
	boolean := frontend.Type{Kind: frontend.KindBoolean}
	invertBody := node("Block", node("ReturnStatement", notNode(boolIdent("value", 2))))
	init := typed("CallExpression", frontend.KindBoolean, ident("invert", 1), boolIdent("flag", 3))
	constNode := node("Const")
	constNode.Decl = 3
	module := node("Block", constNode, node("ExpressionStatement", consoleLog(init)))

	return &frontend.Facts{
		SchemaVersion: 1,
		Session:       "s",
		File:          "/tmp/t.ts",
		Module:        &module,
		Declarations: []frontend.Declaration{
			{ID: 1, Kind: frontend.DeclFunction, Name: "invert", Start: 0, Length: 20, Result: &boolean, Parameters: []int{2}, Body: &invertBody},
			{ID: 2, Kind: frontend.DeclParameter, Name: "value", Start: 4, Length: 5, Type: &boolean},
			{ID: 3, Kind: frontend.DeclConst, Name: "flag", Start: 30, Length: 4, Type: &boolean, Body: boolNode(false)},
		},
	}
}

func boolNode(v bool) *frontend.Node {
	n := boolLiteral(v)
	return &n
}

func TestLowersBooleanProgram(t *testing.T) {
	module := lowerFacts(t, booleanFacts())
	invert := module.Functions[0]
	if invert.Result != nir.TypeBool || len(invert.Params) != 1 || invert.Params[0].Type != nir.TypeBool {
		t.Fatalf("invert 的签名不对：%+v", invert)
	}
	if got := invert.Body[0].Value; got == nil || got.Op != nir.OpNot || got.Operand == nil || got.Operand.Op != nir.OpParam {
		t.Fatalf("invert 的函数体应是 not(param)：%+v", got)
	}

	main := module.Functions[1]
	if len(main.Body) != 3 {
		t.Fatalf("main 应有 3 条语句，实际 %d：%+v", len(main.Body), main.Body)
	}
	let := main.Body[0]
	if let.Op != nir.OpLet || let.Value == nil || let.Value.Op != nir.OpBool || let.Value.Value == nil || *let.Value.Value {
		t.Fatalf("第一条应是 let flag = false：%+v", let)
	}
	print := main.Body[1]
	if print.Op != nir.OpPrint || print.Value == nil || print.Value.Op != nir.OpCall {
		t.Fatalf("第二条应是 print invert(flag)：%+v", print)
	}
	if arg := print.Value.Args[0]; arg.Op != nir.OpLocal || arg.Name != "flag" {
		t.Fatalf("invert 的实参应是 local flag：%+v", arg)
	}
}

func TestRejectsBooleanMismatches(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*frontend.Facts)
		code   string
	}{
		{
			name: "bool 函数返回 number",
			mutate: func(f *frontend.Facts) {
				body := node("Block", node("ReturnStatement", literal("0x3ff0000000000000")))
				f.Declarations[0].Body = &body
			},
			code: "lower.bad-type",
		},
		{
			name: "! 作用在 number 上",
			mutate: func(f *frontend.Facts) {
				body := node("Block", node("ReturnStatement", notNode(ident("value", 2))))
				f.Declarations[0].Body = &body
				f.Declarations[1].Type = &frontend.Type{Kind: frontend.KindNumber}
			},
			code: "lower.bad-type",
		},
		{
			name: "boolean 常量用 number 初始化",
			mutate: func(f *frontend.Facts) {
				n := literal("0x3ff0000000000000")
				f.Declarations[2].Body = &n
			},
			code: "lower.bad-type",
		},
		{
			name: "boolean 参数收到 number",
			mutate: func(f *frontend.Facts) {
				module := *f.Module
				console := module.Children[1].Children[0]
				invert := console.Children[1]
				invert.Children[1] = literal("0x3ff0000000000000")
				f.Module = &module
			},
			code: "lower.bad-type",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			facts := booleanFacts()
			c.mutate(facts)
			module, err := Lower(snapshot(t), facts)
			if err == nil {
				t.Fatal("应被拒绝")
			}
			if module != nil {
				t.Fatal("失败时不应返回模块")
			}
			var d *diagnostic.Diagnostic
			if !errors.As(err, &d) || d.Code != c.code {
				t.Fatalf("错误码应为 %s，实际 %v", c.code, err)
			}
			if d.Category != diagnostic.Internal {
				t.Fatalf("降低失败属于内部故障，实际 %v", d.Category)
			}
		})
	}
}

// compareFacts 对应：
//
//	function mark(n: number): number { console.log(n); return n; }
//	console.log(mark(1) < mark(2));
//	console.log(1 === 1);
//	console.log(true !== false);
func compareFacts() *frontend.Facts {
	number := frontend.Type{Kind: frontend.KindNumber}
	markBody := node("Block",
		node("ExpressionStatement", consoleLog(ident("n", 2))),
		node("ReturnStatement", ident("n", 2)),
	)
	mark := func(v string) frontend.Node {
		return typed("CallExpression", frontend.KindNumber, ident("mark", 1), literal(v))
	}
	module := node("Block",
		node("ExpressionStatement", consoleLog(compareNode("<", mark("0x3ff0000000000000"), mark("0x4000000000000000")))),
		node("ExpressionStatement", consoleLog(compareNode("===", literal("0x3ff0000000000000"), literal("0x3ff0000000000000")))),
		node("ExpressionStatement", consoleLog(compareNode("!==", boolLiteral(true), boolLiteral(false)))),
	)
	return &frontend.Facts{
		SchemaVersion: 1,
		Session:       "s",
		File:          "/tmp/t.ts",
		Module:        &module,
		Declarations: []frontend.Declaration{
			{ID: 1, Kind: frontend.DeclFunction, Name: "mark", Start: 0, Length: 20, Result: &number, Parameters: []int{2}, Body: &markBody},
			{ID: 2, Kind: frontend.DeclParameter, Name: "n", Start: 4, Length: 1, Type: &number},
		},
	}
}

func TestLowersComparisonProgram(t *testing.T) {
	module := lowerFacts(t, compareFacts())
	main := module.Functions[1]
	if main.Name != "main" || len(main.Body) != 4 {
		t.Fatalf("main 的形状不对：%+v", main)
	}
	first := main.Body[0]
	if first.Op != nir.OpPrint || first.Value == nil || first.Value.Op != nir.OpCompare || first.Value.Operator != "<" {
		t.Fatalf("第一条应是 print(mark(1) < mark(2))：%+v", first)
	}
	if first.Value.Left == nil || first.Value.Left.Op != nir.OpCall || first.Value.Right == nil || first.Value.Right.Op != nir.OpCall {
		t.Fatalf("比较两侧应保留两个调用：%+v", first.Value)
	}
	if got := main.Body[1].Value; got == nil || got.Op != nir.OpCompare || got.Operator != "===" {
		t.Fatalf("第二条应是 number 的严格相等：%+v", got)
	}
	if got := main.Body[2].Value; got == nil || got.Op != nir.OpCompare || got.Operator != "!==" {
		t.Fatalf("第三条应是 boolean 的严格不等：%+v", got)
	}
}

func TestRejectsComparisonMismatches(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*frontend.Facts)
	}{
		{
			name: "有序比较收到 boolean",
			mutate: func(f *frontend.Facts) {
				cmp := f.Module.Children[0].Children[0].Children[1]
				cmp.Children[0] = boolLiteral(true)
			},
		},
		{
			name: "严格相等两侧类型不同",
			mutate: func(f *frontend.Facts) {
				cmp := f.Module.Children[1].Children[0].Children[1]
				cmp.Children[0] = boolLiteral(true)
			},
		},
		{
			name: "严格相等不支持 string",
			mutate: func(f *frontend.Facts) {
				cmp := f.Module.Children[1].Children[0].Children[1]
				cmp.Children[0].Type = &frontend.Type{Kind: frontend.KindString}
				cmp.Children[1].Type = &frontend.Type{Kind: frontend.KindString}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			facts := compareFacts()
			c.mutate(facts)
			module, err := Lower(snapshot(t), facts)
			if err == nil {
				t.Fatal("应被拒绝")
			}
			if module != nil {
				t.Fatal("失败时不应返回模块")
			}
			var d *diagnostic.Diagnostic
			if !errors.As(err, &d) || d.Code != "lower.bad-type" {
				t.Fatalf("错误码应为 lower.bad-type，实际 %v", err)
			}
		})
	}
}
