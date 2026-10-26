package main

import (
	"testing"

	"github.com/Mahoo12138/tynel/compiler/capability"
	"github.com/Mahoo12138/tynel/compiler/lower"
	"github.com/Mahoo12138/tynel/compiler/nir"
	"github.com/Mahoo12138/tynel/compiler/source"
)

// lowerFixture 走完整前半程：源码检查、能力检查、降低。
func lowerFixture(t *testing.T, path string) (*source.Snapshot, *nir.Module) {
	t.Helper()
	snapshot, err := source.Read(path, source.DefaultLimits())
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	diagnostics, err := check(snapshot)
	if err != nil {
		t.Fatalf("源码检查失败：%v", err)
	}
	for _, d := range diagnostics {
		if d.Severity == source.SeverityError {
			t.Fatalf("%s 应先通过类型检查：%s", path, d)
		}
	}
	facts, err := factsOf(snapshot)
	if err != nil {
		t.Fatalf("读取事实失败：%v", err)
	}
	findings, err := capability.Check(snapshot, facts)
	if err != nil {
		t.Fatalf("能力检查失败：%v", err)
	}
	if len(findings) > 0 {
		t.Fatalf("%s 应通过能力检查：%v", path, findings)
	}
	module, err := lower.Lower(snapshot, facts)
	if err != nil {
		t.Fatalf("降低失败：%v", err)
	}
	return snapshot, module
}

func TestLowerAdd(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "add.ts"))
	if module.Entry != "main" || len(module.Functions) != 2 {
		t.Fatalf("模块形状不对：entry=%q functions=%d", module.Entry, len(module.Functions))
	}
	if module.Functions[0].Name != "add" || module.Functions[1].Name != "main" {
		t.Fatalf("函数顺序应为 add、main：%+v", module.Functions)
	}
	main := module.Functions[1]
	if len(main.Body) != 3 {
		t.Fatalf("main 应有 let、print、return 三条语句，实际 %d", len(main.Body))
	}
	if main.Body[0].Op != nir.OpLet || main.Body[0].Name != "answer" {
		t.Fatalf("第一条应为 let answer：%+v", main.Body[0])
	}
	if main.Body[1].Op != nir.OpPrint || main.Body[1].Value.Op != nir.OpLocal {
		t.Fatalf("第二条应为 print local：%+v", main.Body[1])
	}
}

func TestLowerNegatedZero(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "lower/negzero.ts"))
	print := module.Functions[0].Body[0]
	if print.Op != nir.OpPrint {
		t.Fatalf("第一条应为 print：%+v", print)
	}
	if print.Value.Bits != "0x8000000000000000" {
		t.Fatalf("负零应折成 0x8000000000000000，实际 %q", print.Value.Bits)
	}
}

func TestLowerForwardCall(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "lower/forward.ts"))
	// 函数按声明顺序输出：add 在前、main 在后；main 里的调用指向 add。
	if module.Functions[0].Name != "add" {
		t.Fatalf("第一个函数应为 add：%+v", module.Functions[0])
	}
	call := module.Functions[1].Body[0].Value
	if call.Op != nir.OpCall || call.Function != "add" {
		t.Fatalf("调用应指向 add：%+v", call)
	}
}

func TestLowerEvaluationOrder(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "lower/order.ts"))
	call := module.Functions[len(module.Functions)-1].Body[0].Value
	if call.Op != nir.OpCall || call.Function != "add" || len(call.Args) != 2 {
		t.Fatalf("外层应为 add 调用：%+v", call)
	}
	for i, want := range []string{"log", "log"} {
		if call.Args[i].Op != nir.OpCall || call.Args[i].Function != want {
			t.Fatalf("第 %d 个实参应为 %s 调用：%+v", i+1, want, call.Args[i])
		}
	}
}

func TestLowerSameNamedParameters(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "lower/same-name.ts"))
	first, second := module.Functions[0], module.Functions[1]
	if first.Name != "first" || second.Name != "second" {
		t.Fatalf("函数顺序不对：%+v", module.Functions)
	}
	// 两个函数的参数都叫 a，但各自引用自己的参数：NIR 里是同一个名字，作用域
	// 按函数分开，所以不会串。
	for _, fn := range []nir.Function{first, second} {
		if len(fn.Params) != 1 || fn.Params[0].Name != "a" {
			t.Fatalf("函数 %q 的参数不对：%+v", fn.Name, fn.Params)
		}
		param := fn.Body[0].Value.Left
		if param.Op != nir.OpParam || param.Name != "a" {
			t.Fatalf("函数 %q 应引用自己的参数 a：%+v", fn.Name, param)
		}
	}
}

// TestLowerVoidFunction 覆盖 void 函数：函数体以空 return 结束，调用点变成
// 丢弃结果的语句。
func TestLowerVoidFunction(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "void.ts"))
	announce := module.Functions[0]
	if announce.Name != "announce" || announce.Result != nir.TypeVoid {
		t.Fatalf("announce 应为 void 函数：%+v", announce)
	}
	if got := announce.Body[len(announce.Body)-1]; got.Op != nir.OpReturn || got.Value != nil {
		t.Fatalf("void 函数应以空 return 结束：%+v", got)
	}
	main := module.Functions[1]
	if len(main.Body) != 3 {
		t.Fatalf("main 应有两条调用语句加一条 return，实际 %d：%+v", len(main.Body), main.Body)
	}
	for i := 0; i < 2; i++ {
		if got := main.Body[i]; got.Op != nir.OpDiscard || got.Value.Op != nir.OpCall || got.Value.Function != "announce" {
			t.Fatalf("第 %d 条应为 discard 调用 announce：%+v", i+1, got)
		}
	}
}

// TestLowerDiscardedExpressions 确认 `answer;` 与 `add(1, 2);` 都只是求值，
// 不会变成 print。
func TestLowerDiscardedExpressions(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "discard.ts"))
	main := module.Functions[len(module.Functions)-1]
	if len(main.Body) != 5 {
		t.Fatalf("main 应有 5 条语句，实际 %d：%+v", len(main.Body), main.Body)
	}
	if got := main.Body[1]; got.Op != nir.OpDiscard || got.Value.Op != nir.OpLocal || got.Value.Name != "answer" {
		t.Fatalf("第二条应为 discard 局部值 answer：%+v", got)
	}
	if got := main.Body[2]; got.Op != nir.OpDiscard || got.Value.Op != nir.OpCall || got.Value.Function != "add" {
		t.Fatalf("第三条应为 discard 调用 add：%+v", got)
	}
	for _, stmt := range main.Body {
		if stmt.Op == nir.OpPrint && stmt.Value.Op != nir.OpLocal {
			t.Fatalf("只有最后一行 console.log(answer) 应当打印：%+v", stmt)
		}
	}
}

// TestLowerOutputProgram 确认输出格式夹具降低成五条 print。
func TestLowerOutputProgram(t *testing.T) {
	_, module := lowerFixture(t, fixturePath(t, "output.ts"))
	main := module.Functions[0]
	prints := 0
	for _, stmt := range main.Body {
		if stmt.Op == nir.OpPrint {
			prints++
		}
	}
	if prints != 5 {
		t.Fatalf("应有 5 条 print，实际 %d：%+v", prints, main.Body)
	}
}

func TestLoweredModulesVerify(t *testing.T) {
	for _, name := range []string{
		"add.ts", "void.ts", "discard.ts", "output.ts",
		"lower/order.ts", "lower/negzero.ts", "lower/forward.ts", "lower/same-name.ts",
	} {
		t.Run(name, func(t *testing.T) {
			_, module := lowerFixture(t, fixturePath(t, name))
			if err := nir.Verify(module); err != nil {
				t.Fatalf("降低结果应通过 NIR 验证：%v", err)
			}
			if _, err := nir.EncodeBytes(module); err != nil {
				t.Fatalf("降低结果应能编码：%v", err)
			}
		})
	}
}
