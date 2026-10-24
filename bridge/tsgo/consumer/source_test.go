package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/source"
)

// snapshotOf 把源码写进临时文件，再按宿主的真实路径读成快照。
func snapshotOf(t *testing.T, name, text string) *source.Snapshot {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("写入夹具失败：%v", err)
	}
	snapshot, err := source.Read(path, source.DefaultLimits())
	if err != nil {
		t.Fatalf("读取快照失败：%v", err)
	}
	return snapshot
}

func TestPositionAfterChinesePrefixEndToEnd(t *testing.T) {
	// 中文注释让字节偏移推到 22，但行列应停在 2:7。
	snapshot := snapshotOf(t, "bad.ts", "// 中文注释\nconst answer: number = \"forty-two\";\n")
	diagnostics, err := check(snapshot)
	if err != nil {
		t.Fatalf("检查失败：%v", err)
	}
	if len(diagnostics) != 1 {
		t.Fatalf("应得到 1 条诊断，实际 %d：%v", len(diagnostics), diagnostics)
	}
	d := diagnostics[0]
	if d.Location.Start.Line != 2 || d.Location.Start.Column != 7 {
		t.Fatalf("位置应为 2:7，实际 %d:%d", d.Location.Start.Line, d.Location.Start.Column)
	}
	if d.Location.Bytes.Start != 22 {
		t.Fatalf("字节偏移应为 22，实际 %d", d.Location.Bytes.Start)
	}
	if got := d.String(); got != snapshot.Path()+":2:7: Type 'string' is not assignable to type 'number'. (TS2322)" {
		t.Fatalf("展示格式不对：%q", got)
	}
}

func TestTruncatedSourceIsSyntaxError(t *testing.T) {
	// 截断的源码是语法错误，不是崩溃，也不是空诊断。
	snapshot := snapshotOf(t, "cut.ts", "function add(a: number")
	diagnostics, err := check(snapshot)
	if err != nil {
		t.Fatalf("检查失败：%v", err)
	}
	if len(diagnostics) == 0 {
		t.Fatal("截断的源码应产生语法诊断")
	}
	for _, d := range diagnostics {
		if d.Severity != source.SeverityError {
			t.Fatalf("语法问题应为 error，实际 %q", d.Severity)
		}
		if d.Location.Start.Line != 1 {
			t.Fatalf("诊断应落在第一行，实际 %d", d.Location.Start.Line)
		}
	}
}

func TestFileChangeAfterReadDoesNotAffectResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "add.ts")
	if err := os.WriteFile(path, []byte("const answer: number = 42;\n"), 0o644); err != nil {
		t.Fatalf("写入夹具失败：%v", err)
	}
	snapshot, err := source.Read(path, source.DefaultLimits())
	if err != nil {
		t.Fatalf("读取快照失败：%v", err)
	}
	// 快照建立之后把文件改坏：检查仍然只看快照。
	if err := os.WriteFile(path, []byte("const answer: number = \"改坏了\";\n"), 0o644); err != nil {
		t.Fatalf("改写夹具失败：%v", err)
	}
	diagnostics, err := check(snapshot)
	if err != nil {
		t.Fatalf("检查失败：%v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("检查不应读到磁盘上的改动：%v", diagnostics)
	}
}

func TestMissingFileIsEnvironmentError(t *testing.T) {
	_, err := source.Read(filepath.Join(t.TempDir(), "nope.ts"), source.DefaultLimits())
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) {
		t.Fatalf("应返回诊断，实际 %v", err)
	}
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Fatalf("缺文件应退出 4，实际 %d", got)
	}
}
