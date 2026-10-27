package nir

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

func num(f float64) Expr     { return Number(f) }
func boolLit(v bool) Expr    { return Bool(v) }
func param(name string) Expr { return Expr{Op: OpParam, Name: name} }
func not(e Expr) Expr        { return Expr{Op: OpNot, Operand: &e} }
func call(name string, args ...Expr) Expr {
	return Expr{Op: OpCall, Function: name, Args: args}
}
func binary(op string, left, right Expr) Expr {
	return Expr{Op: OpBinary, Operator: op, Left: &left, Right: &right}
}
func compare(op string, left, right Expr) Expr {
	return Expr{Op: OpCompare, Operator: op, Left: &left, Right: &right}
}
func ret(e Expr) Stmt       { return Stmt{Op: OpReturn, Value: &e} }
func retVoid() Stmt         { return Stmt{Op: OpReturn} }
func printStmt(e Expr) Stmt { return Stmt{Op: OpPrint, Value: &e} }
func discard(e Expr) Stmt   { return Stmt{Op: OpDiscard, Value: &e} }
func letStmt(name string, e Expr) Stmt {
	return Stmt{Op: OpLet, Name: name, Value: &e}
}
func local(name string) Expr { return Expr{Op: OpLocal, Name: name} }
func ifStmt(cond Expr, then, els []Stmt) Stmt {
	return Stmt{Op: OpIf, Cond: &cond, Then: then, Else: els}
}

func fn(name, result string, params []string, body ...Stmt) Function {
	f := Function{Name: name, Result: result, Body: body}
	for _, p := range params {
		f.Params = append(f.Params, Param{Name: p, Type: TypeNumber})
	}
	return f
}

func mainWith(body ...Stmt) Function { return fn("main", TypeVoid, nil, body...) }

func module(entry string, functions ...Function) *Module {
	return &Module{SchemaVersion: SchemaVersion, Entry: entry, Functions: functions}
}

func TestVerifyExampleModule(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "ir", "add.nir.json")
	m, err := LoadFile(path, DefaultLimits())
	if err != nil {
		t.Fatalf("读取示例失败：%v", err)
	}
	if err := Verify(m); err != nil {
		t.Fatalf("示例应通过验证：%v", err)
	}
}

func TestVerifyNestedCall(t *testing.T) {
	m := module("main",
		fn("double", TypeNumber, []string{"x"}, ret(binary("+", param("x"), param("x")))),
		fn("main", TypeVoid, nil, printStmt(call("double", call("double", num(1))))),
	)
	if err := Verify(m); err != nil {
		t.Fatalf("嵌套调用应通过验证：%v", err)
	}
}

func TestVerifyEmptyVoidEntry(t *testing.T) {
	if err := Verify(module("main", fn("main", TypeVoid, nil))); err != nil {
		t.Fatalf("空 void 入口应通过验证：%v", err)
	}
	if err := Verify(module("main", fn("main", TypeVoid, nil, retVoid()))); err != nil {
		t.Fatalf("void 函数里的裸 return 应通过验证：%v", err)
	}
}

// TestVerifyDiscard 确认「求值但丢弃」对两种结果都成立：number 表达式可以丢，
// void 调用也可以丢，而且丢弃不改变函数的返回要求。
func TestVerifyDiscard(t *testing.T) {
	logFn := fn("log", TypeVoid, nil, printStmt(num(1)))
	sevenFn := fn("seven", TypeNumber, nil, ret(num(7)))
	m := module("main", mainWith(discard(num(1)), discard(call("seven")), discard(call("log"))), logFn, sevenFn)
	if err := Verify(m); err != nil {
		t.Fatalf("丢弃 number 与 void 调用都应通过验证：%v", err)
	}
}

// TestVerifyBooleanModule 走一遍完整链路：bool 参数、not、bool 返回、bool
// 实参、let 与丢弃。参数和结果都不经过数字真假转换。
func TestVerifyBooleanModule(t *testing.T) {
	invert := Function{
		Name:   "invert",
		Result: TypeBool,
		Params: []Param{{Name: "value", Type: TypeBool}},
		Body:   []Stmt{ret(not(param("value")))},
	}
	flag := Expr{Op: OpLocal, Name: "flag"}
	m := module("main",
		invert,
		mainWith(
			Stmt{Op: OpLet, Name: "flag", Value: &Expr{Op: OpBool, Value: boolPtr(false)}},
			printStmt(call("invert", flag)),
			discard(flag),
		),
	)
	if err := Verify(m); err != nil {
		t.Fatalf("boolean 模块应通过验证：%v", err)
	}
}

func boolPtr(v bool) *bool { return &v }

// TestVerifyComparisonModule 覆盖有序比较与严格相等：number 比较、boolean
// 相等、比较结果作为 print 与 let 的值。
func TestVerifyComparisonModule(t *testing.T) {
	less := Function{
		Name:   "less",
		Result: TypeBool,
		Params: []Param{{Name: "a", Type: TypeNumber}, {Name: "b", Type: TypeNumber}},
		Body:   []Stmt{ret(compare("<", param("a"), param("b")))},
	}
	same := Function{
		Name:   "same",
		Result: TypeBool,
		Params: []Param{{Name: "a", Type: TypeBool}, {Name: "b", Type: TypeBool}},
		Body:   []Stmt{ret(compare("===", param("a"), param("b")))},
	}
	result := Expr{Op: OpLocal, Name: "result"}
	two, one := num(2), num(1)
	m := module("main",
		less, same,
		mainWith(
			Stmt{Op: OpLet, Name: "result", Value: &Expr{Op: OpCompare, Operator: ">=", Left: &two, Right: &one}},
			printStmt(result),
			printStmt(call("less", num(1), num(2))),
			printStmt(call("same", boolLit(true), boolLit(false))),
			discard(compare("!==", num(1), num(1))),
		),
	)
	if err := Verify(m); err != nil {
		t.Fatalf("比较模块应通过验证：%v", err)
	}
}

// TestVerifyIfScopes 覆盖分支作用域：两条分支各自定义同名的 x，互不影响；
// 嵌套块可以遮蔽外层的 x。
func TestVerifyIfScopes(t *testing.T) {
	branch := Function{
		Name:   "branch",
		Result: TypeVoid,
		Params: []Param{{Name: "flag", Type: TypeBool}},
		Body: []Stmt{
			ifStmt(param("flag"),
				[]Stmt{letStmt("x", num(1)), printStmt(local("x"))},
				[]Stmt{letStmt("x", num(2)), printStmt(local("x"))},
			),
			letStmt("x", num(3)),
			ifStmt(boolLit(true),
				[]Stmt{letStmt("x", num(4)), printStmt(local("x"))},
				nil,
			),
			printStmt(local("x")),
			retVoid(),
		},
	}
	m := module("main", branch,
		mainWith(discard(call("branch", boolLit(true))), retVoid()),
	)
	if err := Verify(m); err != nil {
		t.Fatalf("分支作用域模块应通过验证：%v", err)
	}
}

// TestVerifyReturnPaths 覆盖完整返回路径：两臂都返回、提前返回后补 return、
// 嵌套分支、void 正常结束，以及缺一臂时按 if 的位置报错。
func TestVerifyReturnPaths(t *testing.T) {
	both := Function{
		Name:   "both",
		Result: TypeNumber,
		Body: []Stmt{
			ifStmt(boolLit(true), []Stmt{ret(num(1))}, []Stmt{ret(num(2))}),
		},
	}
	early := Function{
		Name:   "early",
		Result: TypeNumber,
		Body: []Stmt{
			ifStmt(boolLit(true), []Stmt{ret(num(1))}, nil),
			ret(num(2)),
		},
	}
	nested := Function{
		Name:   "nested",
		Result: TypeNumber,
		Body: []Stmt{
			ifStmt(boolLit(true),
				[]Stmt{ifStmt(boolLit(false), []Stmt{ret(num(1))}, []Stmt{ret(num(2))})},
				[]Stmt{ret(num(3))},
			),
		},
	}
	voidIf := Function{
		Name:   "voidIf",
		Result: TypeVoid,
		Body: []Stmt{
			ifStmt(boolLit(true), []Stmt{printStmt(num(1))}, nil),
		},
	}
	dead := Function{
		Name:   "dead",
		Result: TypeNumber,
		Body: []Stmt{
			ret(num(1)),
			letStmt("unused", num(2)),
		},
	}
	m := module("main", both, early, nested, voidIf, dead,
		mainWith(
			printStmt(call("both")),
			printStmt(call("early")),
			printStmt(call("nested")),
			discard(call("voidIf")),
			printStmt(call("dead")),
			retVoid(),
		),
	)
	if err := Verify(m); err != nil {
		t.Fatalf("完整返回路径应通过验证：%v", err)
	}

	// 缺 else：报在 if 上。
	pos := &Pos{File: "f.ts", Line: 7, Column: 3}
	cond := boolLit(true)
	missing := Stmt{Op: OpIf, Cond: &cond, Then: []Stmt{ret(num(1))}, Pos: pos}
	f := Function{Name: "missing", Result: TypeNumber, Body: []Stmt{missing}}
	err := Verify(module("main", f, mainWith(retVoid())))
	if err == nil {
		t.Fatal("缺返回路径应被拒绝")
	}
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) || d.Code != "nir.missing-return" {
		t.Fatalf("应报 nir.missing-return，实际 %v", err)
	}
	if d.Line != 7 || d.Column != 3 {
		t.Fatalf("错误位置应指向 if（7:3），实际 %d:%d", d.Line, d.Column)
	}
}

func TestVerifyRejectsSemanticErrors(t *testing.T) {
	ok := fn("main", TypeVoid, nil)
	logFn := fn("log", TypeVoid, nil, printStmt(num(1)))
	addFn := fn("add", TypeNumber, []string{"a", "b"}, ret(binary("+", param("a"), param("b"))))
	invertFn := Function{
		Name:   "invert",
		Result: TypeBool,
		Params: []Param{{Name: "value", Type: TypeBool}},
		Body:   []Stmt{ret(not(param("value")))},
	}
	ifWithValueCond := boolLit(true)
	ifWithValue := num(1)

	cases := []struct {
		name   string
		module *Module
		code   string
	}{
		{"入口不存在", module("nope", ok), "nir.entry-missing"},
		{"入口带参数", module("main", fn("main", TypeVoid, []string{"x"})), "nir.entry-signature"},
		{"入口返回 number", module("main", fn("main", TypeNumber, nil, ret(num(1)))), "nir.entry-signature"},
		{"函数重名", module("main", ok, fn("dup", TypeVoid, nil), fn("dup", TypeVoid, nil)), "nir.duplicate-name"},
		{"参数重名", module("main", ok, fn("f", TypeVoid, []string{"x", "x"})), "nir.duplicate-name"},
		{"未知参数", module("main", ok, fn("f", TypeVoid, []string{"x"}, printStmt(param("y")))), "nir.unknown-param"},
		{"未知函数", module("main", mainWith(printStmt(call("nope", num(1))))), "nir.unknown-function"},
		{"实参数量不符", module("main", mainWith(printStmt(call("add", num(1)))), addFn), "nir.arg-count"},
		{"实参类型不符", module("main", mainWith(printStmt(call("add", num(1), call("log")))), logFn, addFn), "nir.arg-type"},
		{"返回类型不符", module("main", ok, logFn, fn("f", TypeNumber, nil, ret(call("log")))), "nir.result-type"},
		{"void 函数带返回值", module("main", ok, fn("f", TypeVoid, nil, ret(num(1)))), "nir.result-type"},
		{"number 函数没有 return", module("main", ok, fn("f", TypeNumber, nil, printStmt(num(1)))), "nir.missing-return"},
		{"number 函数空函数体", module("main", ok, fn("f", TypeNumber, nil)), "nir.missing-return"},
		{"print 一个 void 调用", module("main", mainWith(printStmt(call("log"))), logFn), "nir.print-type"},
		{"运算符操作数是 void 调用", module("main", mainWith(printStmt(binary("+", call("log"), num(1)))), logFn), "nir.operand-type"},
		{"not 的操作数是 number", module("main", mainWith(printStmt(not(num(1))))), "nir.operand-type"},
		{"有序比较收到 boolean", module("main", mainWith(printStmt(compare("<", boolLit(true), num(1))))), "nir.operand-type"},
		{"严格相等两侧类型不同", module("main", mainWith(printStmt(compare("===", num(1), boolLit(true))))), "nir.operand-type"},
		{"宽松相等不是 NIR 运算符", module("main", mainWith(printStmt(compare("==", num(1), num(1))))), "nir.bad-op"},
		{"if 的条件不是 bool", module("main", mainWith(ifStmt(num(1), []Stmt{retVoid()}, nil))), "nir.condition-type"},
		{"分支内局部值在块外不可见", module("main", mainWith(
			ifStmt(boolLit(true), []Stmt{letStmt("x", num(1))}, nil),
			printStmt(local("x")),
		)), "nir.unknown-local"},
		{"同一个块里重复定义", module("main", mainWith(
			letStmt("x", num(1)),
			letStmt("x", num(2)),
		)), "nir.duplicate-name"},
		{"if 缺少条件", module("main", mainWith(Stmt{Op: OpIf, Then: []Stmt{retVoid()}})), "nir.missing"},
		{"if 不接受 value", module("main", mainWith(Stmt{Op: OpIf, Cond: &ifWithValueCond, Value: &ifWithValue})), "nir.unexpected-field"},
		{"bool 参数收到 number", module("main", mainWith(printStmt(call("invert", num(1)))), invertFn), "nir.arg-type"},
		{"bool 函数返回 number", module("main", ok, fn("f", TypeBool, nil, ret(num(1)))), "nir.result-type"},
		{"内存里构造的未知 op", module("main", mainWith(printStmt(Expr{Op: "magic"}))), "nir.bad-op"},
		{"discard 引用未知局部值", module("main", mainWith(discard(Expr{Op: OpLocal, Name: "x"}))), "nir.unknown-local"},
	}

	for _, c := range cases {
		err := Verify(c.module)
		if err == nil {
			t.Errorf("%s：应被拒绝，却通过验证", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.code) {
			t.Errorf("%s：错误码不是 %s，实际为 %v", c.name, c.code, err)
		}
	}
}

func TestVerifyErrorCarriesPosition(t *testing.T) {
	bad := param("missing")
	bad.Pos = &Pos{File: "examples/add.ts", Line: 7, Column: 9}
	m := module("main", fn("main", TypeVoid, nil, printStmt(bad)))

	err := Verify(m)
	if err == nil {
		t.Fatal("引用不存在的参数应被拒绝")
	}
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) {
		t.Fatalf("错误不是诊断：%v", err)
	}
	if d.File != "examples/add.ts" || d.Line != 7 || d.Column != 9 {
		t.Fatalf("位置为 %s:%d:%d，期望 examples/add.ts:7:9", d.File, d.Line, d.Column)
	}
	if d.Category != diagnostic.Source {
		t.Errorf("类别为 %v，期望源码错误", d.Category)
	}
}

func TestVerifyRejectsCycleWithoutHanging(t *testing.T) {
	cycle := &Expr{Op: OpBinary, Operator: "+"}
	cycle.Left = cycle
	cycle.Right = cycle
	m := module("main", fn("main", TypeVoid, nil, printStmt(*cycle)))

	done := make(chan error, 1)
	go func() { done <- Verify(m) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("循环引用应被拒绝")
		}
		if !strings.Contains(err.Error(), "nir.cycle") {
			t.Fatalf("错误码不是 nir.cycle：%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("验证循环引用时没有在 2 秒内返回，说明遍历不终止")
	}
}

func TestVerifyRejectsSharedNode(t *testing.T) {
	shared := num(1)
	expr := Expr{Op: OpBinary, Operator: "+", Left: &shared, Right: &shared}
	m := module("main", fn("main", TypeVoid, nil, printStmt(expr)))

	err := Verify(m)
	if err == nil {
		t.Fatal("同一个节点被引用两次应被拒绝")
	}
	if !strings.Contains(err.Error(), "nir.cycle") {
		t.Fatalf("错误码不是 nir.cycle：%v", err)
	}
}

func TestVerifyRejectsOverDepthInMemory(t *testing.T) {
	expr := &Expr{Op: OpNumber, Bits: BitsOf(1)}
	for i := 0; i < 70; i++ {
		expr = &Expr{Op: OpBinary, Operator: "+", Left: expr, Right: &Expr{Op: OpNumber, Bits: BitsOf(1)}}
	}
	m := module("main", fn("main", TypeVoid, nil, printStmt(*expr)))

	err := VerifyWithLimits(m, DefaultLimits())
	if err == nil {
		t.Fatal("超过深度预算应被拒绝")
	}
	if !strings.Contains(err.Error(), "nir.budget") {
		t.Fatalf("错误码不是 nir.budget：%v", err)
	}
}

func TestVerifyRejectsNilModule(t *testing.T) {
	if err := Verify(nil); err == nil {
		t.Fatal("空模块应被拒绝")
	}
}
