package source

import (
	"strings"
	"testing"
)

func TestMapDiagnosticsBindsPosition(t *testing.T) {
	text := "// 中文注释\nconst answer: number = \"forty-two\";\n"
	s := fixture(t, text)
	start := strings.Index(text, "answer")

	mapped, err := s.MapDiagnostics([]FrontendDiagnostic{{
		File:     s.Path(),
		Code:     2322,
		Category: "error",
		Message:  "Type 'string' is not assignable to type 'number'.",
		Start:    start,
		Length:   len("answer"),
	}})
	if err != nil {
		t.Fatalf("映射失败：%v", err)
	}
	if len(mapped) != 1 {
		t.Fatalf("应得到 1 条诊断，实际 %d", len(mapped))
	}
	d := mapped[0]
	if d.Severity != SeverityError || d.Code != 2322 {
		t.Fatalf("严重程度或错误码不对：%+v", d)
	}
	if d.Location.Path != s.Path() || d.Location.FileID != s.ID() {
		t.Fatalf("文件身份不对：%+v", d.Location)
	}
	if d.Location.Start.Line != 2 || d.Location.Start.Column != 7 {
		t.Fatalf("起点应为 2:7，实际 %d:%d", d.Location.Start.Line, d.Location.Start.Column)
	}
	if d.Location.End.Column != 13 {
		t.Fatalf("终点列应为 13，实际 %d", d.Location.End.Column)
	}
	if d.Location.Bytes != (Range{Start: start, End: start + 6}) {
		t.Fatalf("字节区间应原样保留，实际 %+v", d.Location.Bytes)
	}
}

func TestMapDiagnosticsKeepsForeignFile(t *testing.T) {
	// 声明库里的诊断不属于这份快照，不能硬套行列。
	s := fixture(t, "const x = 1;\n")
	mapped, err := s.MapDiagnostics([]FrontendDiagnostic{{
		File:     "/libs/lib.es5.d.ts",
		Code:     2318,
		Category: "error",
		Message:  "Cannot find global type 'Array'.",
	}})
	if err != nil {
		t.Fatalf("映射失败：%v", err)
	}
	if len(mapped) != 1 {
		t.Fatalf("应保留这条诊断，实际 %d", len(mapped))
	}
	if mapped[0].Location.Path != "/libs/lib.es5.d.ts" {
		t.Fatalf("应保留原路径，实际 %q", mapped[0].Location.Path)
	}
	if mapped[0].Location.Start.Line != 0 {
		t.Fatalf("外来文件不应有行列，实际 %+v", mapped[0].Location.Start)
	}
}

func TestMapDiagnosticsRejectsUnknownSeverity(t *testing.T) {
	s := fixture(t, "const x = 1;\n")
	_, err := s.MapDiagnostics([]FrontendDiagnostic{{Category: "fatal", Code: 1, Message: "x"}})
	if !isCode(err, "source.bad-severity") {
		t.Fatalf("未知类别应被拒绝，实际 %v", err)
	}
}

func TestMapDiagnosticsRejectsOutOfRange(t *testing.T) {
	s := fixture(t, "const x = 1;\n")
	_, err := s.MapDiagnostics([]FrontendDiagnostic{{
		Category: "error",
		Code:     1,
		Message:  "x",
		Start:    s.Size() + 3,
		Length:   1,
	}})
	if !isCode(err, "source.bad-offset") {
		t.Fatalf("越界区间应被拒绝，实际 %v", err)
	}
}

func TestDiagnosticString(t *testing.T) {
	text := "// 中文注释\nconst answer: number = 1;\n"
	s := fixture(t, text)
	start := strings.Index(text, "answer")
	mapped, err := s.MapDiagnostics([]FrontendDiagnostic{{
		Category: "error",
		Code:     2322,
		Message:  "类型不匹配",
		Start:    start,
		Length:   6,
	}})
	if err != nil {
		t.Fatalf("映射失败：%v", err)
	}
	want := s.Path() + ":2:7: 类型不匹配 (TS2322)"
	if got := mapped[0].String(); got != want {
		t.Fatalf("展示格式应为 %q，实际 %q", want, got)
	}

	foreign := Diagnostic{Severity: SeverityError, Code: 2318, Message: "缺全局类型"}
	if got := foreign.String(); got != "<程序>: 缺全局类型 (TS2318)" {
		t.Fatalf("无位置的诊断格式不对：%q", got)
	}
}

func TestParseSeverity(t *testing.T) {
	for _, name := range []string{"error", "warning", "suggestion", "message"} {
		if _, ok := ParseSeverity(name); !ok {
			t.Fatalf("%q 应被识别", name)
		}
	}
	if _, ok := ParseSeverity("Error"); ok {
		t.Fatal("类别名区分大小写")
	}
}
