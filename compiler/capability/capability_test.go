package capability

import (
	"errors"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/source"
)

// snapshot 造一份足够长的源文本，让所有偏移都落在范围内。
func snapshot(t *testing.T) *source.Snapshot {
	t.Helper()
	s, err := source.New("/tmp/t.ts", []byte(strings.Repeat("x", 256)), source.DefaultLimits())
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

func ident(name string, target int) frontend.Node {
	n := node("Identifier")
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

func binary(op string, left, right frontend.Node) frontend.Node {
	n := typed("BinaryExpression", frontend.KindNumber, left, right)
	n.Op = op
	return n
}

// base 是一份最小的合法事实：add(a, b) 的声明与函数体。
func base() *frontend.Facts {
	number := frontend.Type{Kind: frontend.KindNumber}
	body := node("Block",
		node("ReturnStatement",
			binary("+", ident("a", 2), ident("b", 3)),
		),
	)
	return &frontend.Facts{
		SchemaVersion: 1,
		Session:       "s",
		File:          "/tmp/t.ts",
		Declarations: []frontend.Declaration{
			{ID: 1, Kind: frontend.DeclFunction, Name: "add", Start: 0, Length: 20, Result: &number, Parameters: []int{2, 3}, Body: &body},
			{ID: 2, Kind: frontend.DeclParameter, Name: "a", Start: 4, Length: 1, Type: &number},
			{ID: 3, Kind: frontend.DeclParameter, Name: "b", Start: 6, Length: 1, Type: &number},
		},
	}
}

func check(t *testing.T, facts *frontend.Facts) []*diagnostic.Diagnostic {
	t.Helper()
	findings, err := Check(snapshot(t), facts)
	if err != nil {
		t.Fatalf("检查失败：%v", err)
	}
	return findings
}

func wantCode(t *testing.T, findings []*diagnostic.Diagnostic, code string) {
	t.Helper()
	if len(findings) == 0 {
		t.Fatalf("应报 %s，实际没有发现", code)
	}
	for _, d := range findings {
		if d.Code == code {
			if d.Category != diagnostic.Capability {
				t.Fatalf("%s 的类别应为能力限制，实际 %v", code, d.Category)
			}
			return
		}
	}
	t.Fatalf("应报 %s，实际报了 %v", code, codes(findings))
}

func codes(findings []*diagnostic.Diagnostic) []string {
	out := make([]string, 0, len(findings))
	for _, d := range findings {
		out = append(out, d.Code)
	}
	return out
}

func TestSupportedProgramPasses(t *testing.T) {
	if findings := check(t, base()); len(findings) != 0 {
		t.Fatalf("合法程序不应有发现，实际 %v", codes(findings))
	}
}

func TestAnyIsRejected(t *testing.T) {
	facts := base()
	any := frontend.Type{Kind: frontend.KindAny}
	facts.Declarations[0].Result = &any
	wantCode(t, check(t, facts), "cap.any")

	facts = base()
	facts.Declarations[1].Type = &any
	wantCode(t, check(t, facts), "cap.any")
}

func TestTypeAssertionIsRejected(t *testing.T) {
	facts := base()
	body := node("Block", node("ExpressionStatement", node("AsExpression", typed("NumericLiteral", frontend.KindNumber))))
	facts.Declarations[0].Body = &body
	wantCode(t, check(t, facts), "cap.type-escape")
}

func TestObjectAndStringAreRejected(t *testing.T) {
	facts := base()
	body := node("Block", node("ExpressionStatement", node("ObjectLiteralExpression")))
	facts.Declarations[0].Body = &body
	wantCode(t, check(t, facts), "cap.unsupported-syntax")

	facts = base()
	body = node("Block", node("ExpressionStatement", typed("StringLiteral", frontend.KindString)))
	facts.Declarations[0].Body = &body
	wantCode(t, check(t, facts), "cap.unsupported-syntax")
}

func TestUnknownCalleeAndEval(t *testing.T) {
	facts := base()
	body := node("Block", node("ExpressionStatement", node("CallExpression", ident("Math", 0))))
	facts.Declarations[0].Body = &body
	wantCode(t, check(t, facts), "cap.unknown-callee")

	facts = base()
	body = node("Block", node("ExpressionStatement", node("CallExpression", ident("eval", 0))))
	facts.Declarations[0].Body = &body
	wantCode(t, check(t, facts), "cap.forbidden-global")
}

func TestFunctionValueIsRejected(t *testing.T) {
	facts := base()
	body := node("Block", node("ExpressionStatement", ident("add", 1)))
	facts.Declarations[0].Body = &body
	wantCode(t, check(t, facts), "cap.unsupported-syntax")
}

func TestUncalledFunctionIsStillChecked(t *testing.T) {
	// 加一个没人调用的函数，里面放控制流：检查不靠可达性，照样要报。
	facts := base()
	body := node("Block", node("IfStatement", typed("NumericLiteral", frontend.KindNumber)))
	number := frontend.Type{Kind: frontend.KindNumber}
	facts.Declarations = append(facts.Declarations,
		frontend.Declaration{ID: 4, Kind: frontend.DeclFunction, Name: "never", Start: 40, Length: 10, Result: &number, Body: &body},
	)
	wantCode(t, check(t, facts), "cap.unsupported-syntax")
}

func TestBadOperatorIsRejected(t *testing.T) {
	facts := base()
	body := node("Block", node("ReturnStatement", binary("%", ident("a", 2), ident("b", 3))))
	facts.Declarations[0].Body = &body
	wantCode(t, check(t, facts), "cap.bad-operator")
}

// withVoidFunction 追加一个 void 函数，供「void 当作值」的用例调用。
func withVoidFunction(facts *frontend.Facts) {
	void := frontend.Type{Kind: frontend.KindVoid}
	body := node("Block")
	facts.Declarations = append(facts.Declarations,
		frontend.Declaration{ID: 5, Kind: frontend.DeclFunction, Name: "announce", Start: 40, Length: 10, Result: &void, Body: &body},
	)
}

func TestVoidValueIsRejected(t *testing.T) {
	// console.log(announce())：void 调用没有值，不能当参数。
	facts := base()
	withVoidFunction(facts)
	call := typed("CallExpression", frontend.KindVoid,
		propertyAccess("log", ident("console", 0)),
		typed("CallExpression", frontend.KindVoid, ident("announce", 5)),
	)
	body := node("Block", node("ExpressionStatement", call))
	facts.Declarations[0].Body = &body
	wantCode(t, check(t, facts), "cap.void-value")

	// void 调用出现在算术里。
	facts = base()
	withVoidFunction(facts)
	body = node("Block", node("ReturnStatement", binary("+",
		typed("CallExpression", frontend.KindVoid, ident("announce", 5)),
		typed("NumericLiteral", frontend.KindNumber),
	)))
	facts.Declarations[0].Body = &body
	wantCode(t, check(t, facts), "cap.void-value")
}

func TestDeclaredTypesAreChecked(t *testing.T) {
	stringType := frontend.Type{Kind: frontend.KindString}
	facts := base()
	facts.Declarations[1].Type = &stringType
	wantCode(t, check(t, facts), "cap.bad-type")

	facts = base()
	facts.Declarations[0].Result = &stringType
	wantCode(t, check(t, facts), "cap.bad-type")

	facts = base()
	facts.Declarations = append(facts.Declarations,
		frontend.Declaration{ID: 4, Kind: frontend.DeclConst, Name: "label", Start: 30, Length: 5, Type: &stringType},
	)
	wantCode(t, check(t, facts), "cap.bad-type")
}

// TestBooleanDeclarationsPass 确认 boolean 已经进入标量子集：参数、返回值与
// 常量都可以是 boolean，console.log 也接受它。
func TestBooleanDeclarationsPass(t *testing.T) {
	boolean := frontend.Type{Kind: frontend.KindBoolean}
	facts := base()
	facts.Declarations[0].Result = &boolean
	facts.Declarations[1].Type = &boolean
	body := node("Block", node("ReturnStatement", boolIdent("a", 2)))
	facts.Declarations[0].Body = &body
	facts.Declarations = append(facts.Declarations,
		frontend.Declaration{ID: 4, Kind: frontend.DeclConst, Name: "flag", Start: 30, Length: 4, Type: &boolean},
	)
	if findings := check(t, facts); len(findings) != 0 {
		t.Fatalf("boolean 声明应通过，实际 %v", codes(findings))
	}
}

// TestBangRequiresBoolean 确认 `!` 只接受 boolean。TypeScript 允许 !5，本阶段
// 不把数字的真假转换带进原生后端。
func TestBangRequiresBoolean(t *testing.T) {
	facts := base()
	not := typed("PrefixUnaryExpression", frontend.KindBoolean, ident("a", 2))
	not.Op = "!"
	body := node("Block", node("ReturnStatement", not))
	facts.Declarations[0].Body = &body
	wantCode(t, check(t, facts), "cap.bad-type")

	// 作用在 boolean 参数上是合法的。
	boolean := frontend.Type{Kind: frontend.KindBoolean}
	facts = base()
	facts.Declarations[0].Result = &boolean
	facts.Declarations[1].Type = &boolean
	not = typed("PrefixUnaryExpression", frontend.KindBoolean, boolIdent("a", 2))
	not.Op = "!"
	body = node("Block", node("ReturnStatement", not))
	facts.Declarations[0].Body = &body
	if findings := check(t, facts); len(findings) != 0 {
		t.Fatalf("!boolean 应通过，实际 %v", codes(findings))
	}
}

// TestDiscardedStatementsPass 确认「求值但丢弃」不是错误：调用语句与裸标识符
// 都只是把结果丢掉，副作用仍然保留。
func TestDiscardedStatementsPass(t *testing.T) {
	facts := base()
	withVoidFunction(facts)
	body := node("Block",
		node("ExpressionStatement", typed("CallExpression", frontend.KindNumber, ident("add", 1), ident("a", 2), ident("b", 3))),
		node("ExpressionStatement", typed("CallExpression", frontend.KindVoid, ident("announce", 5))),
		node("ExpressionStatement", ident("a", 2)),
		node("ReturnStatement", binary("+", ident("a", 2), ident("b", 3))),
	)
	facts.Declarations[0].Body = &body
	if findings := check(t, facts); len(findings) != 0 {
		t.Fatalf("丢弃结果的表达式语句应通过，实际 %v", codes(findings))
	}
}

func TestConsoleCall(t *testing.T) {
	// console.log 的合法形状：属性访问 + 一个 number 实参。
	call := typed("CallExpression", frontend.KindVoid,
		propertyAccess("log", ident("console", 0)),
		typed("NumericLiteral", frontend.KindNumber),
	)
	facts := base()
	body := node("Block", node("ExpressionStatement", call))
	facts.Declarations[0].Body = &body
	if findings := check(t, facts); len(findings) != 0 {
		t.Fatalf("console.log(number) 应通过，实际 %v", codes(findings))
	}

	// boolean 也在受限输出之内。
	boolean := typed("TrueKeyword", frontend.KindBoolean)
	call = typed("CallExpression", frontend.KindVoid,
		propertyAccess("log", ident("console", 0)),
		boolean,
	)
	facts = base()
	body = node("Block", node("ExpressionStatement", call))
	facts.Declarations[0].Body = &body
	if findings := check(t, facts); len(findings) != 0 {
		t.Fatalf("console.log(boolean) 应通过，实际 %v", codes(findings))
	}

	// 被本文件的声明遮蔽：console 解析到了某个常量。
	shadowed := typed("CallExpression", frontend.KindVoid,
		propertyAccess("log", ident("console", 2)),
		typed("NumericLiteral", frontend.KindNumber),
	)
	facts = base()
	body = node("Block", node("ExpressionStatement", shadowed))
	facts.Declarations[0].Body = &body
	wantCode(t, check(t, facts), "cap.shadowed-console")

	// 参数不是 number。
	facts = base()
	body = node("Block", node("ExpressionStatement", typed("CallExpression", frontend.KindVoid,
		propertyAccess("log", ident("console", 0)),
		typed("StringLiteral", frontend.KindString),
	)))
	facts.Declarations[0].Body = &body
	wantCode(t, check(t, facts), "cap.bad-console-call")

	// 参数个数不对。
	facts = base()
	body = node("Block", node("ExpressionStatement", typed("CallExpression", frontend.KindVoid,
		propertyAccess("log", ident("console", 0)),
	)))
	facts.Declarations[0].Body = &body
	wantCode(t, check(t, facts), "cap.bad-console-call")
}

func TestCallArgumentCountIsChecked(t *testing.T) {
	facts := base()
	body := node("Block", node("ExpressionStatement", node("CallExpression", ident("add", 1))))
	facts.Declarations[0].Body = &body
	wantCode(t, check(t, facts), "cap.arg-count")
}

func TestInvalidFactsAreRefused(t *testing.T) {
	facts := base()
	facts.Declarations[1].ID = 1 // 与函数重复
	if _, err := Check(snapshot(t), facts); err == nil {
		t.Fatal("事实不自洽时不应继续检查")
	} else {
		var d *diagnostic.Diagnostic
		if !errors.As(err, &d) || d.Code != "facts.duplicate-id" {
			t.Fatalf("应返回 facts.duplicate-id，实际 %v", err)
		}
	}
}

func propertyAccess(name string, object frontend.Node) frontend.Node {
	n := typed("PropertyAccessExpression", frontend.KindVoid, object)
	n.Name = name
	return n
}
