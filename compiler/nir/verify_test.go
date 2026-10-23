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
func param(name string) Expr { return Expr{Op: OpParam, Name: name} }
func call(name string, args ...Expr) Expr {
	return Expr{Op: OpCall, Function: name, Args: args}
}
func binary(op string, left, right Expr) Expr {
	return Expr{Op: OpBinary, Operator: op, Left: &left, Right: &right}
}
func ret(e Expr) Stmt       { return Stmt{Op: OpReturn, Value: &e} }
func retVoid() Stmt         { return Stmt{Op: OpReturn} }
func printStmt(e Expr) Stmt { return Stmt{Op: OpPrint, Value: &e} }

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

func TestVerifyRejectsSemanticErrors(t *testing.T) {
	ok := fn("main", TypeVoid, nil)
	logFn := fn("log", TypeVoid, nil, printStmt(num(1)))
	addFn := fn("add", TypeNumber, []string{"a", "b"}, ret(binary("+", param("a"), param("b"))))

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
		{"内存里构造的未知 op", module("main", mainWith(printStmt(Expr{Op: "magic"}))), "nir.bad-op"},
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
