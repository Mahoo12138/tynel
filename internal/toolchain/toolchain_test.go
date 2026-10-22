package toolchain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败：%v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 %s 失败：%v", path, err)
	}
}

func TestLoadLockMissing(t *testing.T) {
	_, err := LoadLock(filepath.Join(t.TempDir(), "go.lock.json"))
	if err == nil {
		t.Fatal("缺少锁文件时应报错")
	}
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Errorf("退出状态为 %d，期望 %d（环境缺失）", got, diagnostic.ExitEnvironment)
	}
}

func TestLoadLockInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go.lock.json")
	writeFile(t, path, "{不是 JSON")
	if _, err := LoadLock(path); diagnostic.ExitCode(err) != diagnostic.ExitEnvironment {
		t.Fatalf("坏锁文件应按环境缺失处理，实际：%v", err)
	}
}

func TestLoadReportMissing(t *testing.T) {
	_, err := LoadReport(filepath.Join(t.TempDir(), "toolchain.json"))
	if err == nil {
		t.Fatal("缺少准备报告时应报错")
	}
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Errorf("退出状态为 %d，期望 %d", got, diagnostic.ExitEnvironment)
	}
}

func TestReportLookups(t *testing.T) {
	report := &Report{
		Installs: []Install{
			{Kind: "sdk", Name: "go1.27.3-sdk-darwin-arm64", Tools: map[string]string{"go": "/sdk/bin/go", "compile": "/sdk/compile"}},
			{Kind: "src", Name: "go1.27.3-src"},
		},
	}
	if sdk, ok := report.SDK(); !ok || sdk.Name != "go1.27.3-sdk-darwin-arm64" {
		t.Errorf("SDK 查询失败：%v %v", sdk, ok)
	}
	if src, ok := report.Source(); !ok || src.Name != "go1.27.3-src" {
		t.Errorf("源码查询失败：%v %v", src, ok)
	}
	if path, ok := report.Tool("go"); !ok || path != "/sdk/bin/go" {
		t.Errorf("工具查询失败：%q %v", path, ok)
	}
	if _, ok := report.Tool("link"); ok {
		t.Error("报告里没有 link，不应查到")
	}
}

func TestExecRunKeepsNonZeroExitAsData(t *testing.T) {
	out, err := Exec{}.Run("/bin/sh", "-c", "echo boom >&2; exit 7")
	if err != nil {
		t.Fatalf("非零退出不应作为调用失败：%v", err)
	}
	if out.ExitCode != 7 {
		t.Errorf("退出状态为 %d，期望 7", out.ExitCode)
	}
	if !strings.Contains(out.Stderr, "boom") {
		t.Errorf("stderr 应保留原始输出，实际为 %q", out.Stderr)
	}
}

func TestExecRunMissingBinary(t *testing.T) {
	_, err := Exec{}.Run(filepath.Join(t.TempDir(), "no-such-tool"))
	if err == nil {
		t.Fatal("找不到可执行文件时应返回错误")
	}
}

func TestExecLookPathMissing(t *testing.T) {
	if _, err := (Exec{}).LookPath(filepath.Join(t.TempDir(), "no-such-tool")); err == nil {
		t.Fatal("LookPath 应报告不存在的工具")
	}
}

func TestFailureIsInternalNotSource(t *testing.T) {
	out := Output{ExitCode: 7, Stderr: "compile: 内部错误\n"}
	d := out.Failure("compile", []string{"-p", "main"})
	if d.Category != diagnostic.Internal {
		t.Fatalf("类别为 %v，期望内部故障", d.Category)
	}
	if got := d.ExitCode(); got != diagnostic.ExitInternal {
		t.Errorf("退出状态为 %d，期望 %d", got, diagnostic.ExitInternal)
	}
	if !strings.Contains(d.Detail, "compile: 内部错误") {
		t.Errorf("原因应保留 stderr，实际为 %q", d.Detail)
	}
	if !strings.Contains(d.Message, "compile -p main") {
		t.Errorf("消息应包含实际命令，实际为 %q", d.Message)
	}
}

func TestMissingIsEnvironment(t *testing.T) {
	d := Missing("link", os.ErrNotExist)
	if d.Category != diagnostic.Environment {
		t.Fatalf("类别为 %v，期望环境缺失", d.Category)
	}
	if !strings.Contains(d.Message, "link") {
		t.Errorf("消息应包含工具名，实际为 %q", d.Message)
	}
}

func TestFindRootWalksUp(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "tools", "toolchain", "go.lock.json"), "{}")
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := FindRoot(nested)
	if !ok || got != root {
		t.Fatalf("找到的根为 %q（%v），期望 %q", got, ok, root)
	}
}

func TestFindRootWithoutLock(t *testing.T) {
	if _, ok := FindRoot(t.TempDir()); ok {
		t.Fatal("没有锁文件时不应报告找到项目根")
	}
}
