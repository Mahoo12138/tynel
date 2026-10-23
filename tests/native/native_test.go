// Package native 是后端原生回归：把 tests/native/fixtures 里的 NIR 编成
// 原生程序，运行并核对输出，同时检查反汇编与重复构建的一致性。
//
// 它需要 01.01 准备的工具链和 01.05 构建的修改版编译器；缺任何一个都直接
// 失败，而不是跳过——跳过会让「零断言」看起来像通过。
package native

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/internal/build"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("无法定位仓库根：%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(toolchain.LockPath))); err != nil {
		t.Fatalf("仓库根 %s 缺少锁文件：%v", root, err)
	}
	return root
}

func sdkGo(t *testing.T, root string) string {
	t.Helper()
	report, err := toolchain.LoadReport(filepath.Join(root, filepath.FromSlash(toolchain.ReportPath)))
	if err != nil {
		t.Fatalf("工具链尚未准备：%v", err)
	}
	sdk, ok := report.SDK()
	if !ok {
		t.Fatal("准备报告里没有 SDK 记录")
	}
	return filepath.Join(sdk.Dir, "bin", "go")
}

// buildAndRun 编译一份 NIR 并运行，返回标准输出。
func buildAndRun(t *testing.T, root, fixture, output string) string {
	t.Helper()
	if _, err := build.Run(build.Options{Root: root, Input: fixture, Output: output}, toolchain.Exec{}); err != nil {
		t.Fatalf("构建 %s 失败：%v", fixture, err)
	}
	result, err := (toolchain.Exec{}).Run(output)
	if err != nil {
		t.Fatalf("运行 %s 失败：%v", output, err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("运行 %s 退出 %d：%s", output, result.ExitCode, result.Stderr)
	}
	return result.Stdout
}

func TestFixtureOutputs(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()

	cases := []struct {
		name    string
		fixture string
		want    string
	}{
		{"加法 40+2", filepath.Join("examples", "ir", "add.nir.json"), "42\n"},
		{"加法 41+2", filepath.Join("tests", "native", "fixtures", "add-41.nir.json"), "43\n"},
		{"特殊值与小数", filepath.Join("tests", "native", "fixtures", "specials.nir.json"), "-0\nNaN\n+Inf\n-Inf\n0.30000000000000004\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildAndRun(t, root, filepath.Join(root, c.fixture), filepath.Join(outDir, strings.ReplaceAll(c.name, " ", "-")))
			if got != c.want {
				t.Fatalf("输出为 %q，期望 %q", got, c.want)
			}
		})
	}
}

func TestAddDisassemblyShowsRealArithmetic(t *testing.T) {
	root := repoRoot(t)
	output := filepath.Join(t.TempDir(), "add")
	buildAndRun(t, root, filepath.Join(root, "examples", "ir", "add.nir.json"), output)

	goTool := sdkGo(t, root)
	result, err := (toolchain.Exec{}).Run(goTool, "tool", "objdump", "-s", `^main\.add$`, output)
	if err != nil {
		t.Fatalf("反汇编失败：%v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("objdump 退出 %d：%s", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "FADDD") {
		t.Fatalf("main.add 里没有浮点加法指令：\n%s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "RET") {
		t.Fatalf("main.add 里没有返回指令：\n%s", result.Stdout)
	}
}

func TestSupportFileIsNotRewritten(t *testing.T) {
	root := repoRoot(t)
	support := filepath.Join(root, "bridge", "go", "support", "support.go")
	before, err := os.ReadFile(support)
	if err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	buildAndRun(t, root, filepath.Join(root, "examples", "ir", "add.nir.json"), filepath.Join(outDir, "a"))
	buildAndRun(t, root, filepath.Join(root, "tests", "native", "fixtures", "add-41.nir.json"), filepath.Join(outDir, "b"))
	after, err := os.ReadFile(support)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("构建过程改写了固定支持文件")
	}
}

func TestReplayFromSnapshotMatchesOutput(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	first := buildAndRun(t, root, filepath.Join(root, "examples", "ir", "add.nir.json"), filepath.Join(outDir, "first"))
	if first != "42\n" {
		t.Fatalf("首次构建输出 %q", first)
	}

	// 用记录里的快照再构建一次：回放不依赖用户手里的原文件。
	report := filepath.Join(outDir, "first.build.json")
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("读取构建记录失败：%v", err)
	}
	var record struct {
		Tools struct {
			Snapshot string `json:"snapshot"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("构建记录无法解析：%v", err)
	}
	snapshot := record.Tools.Snapshot
	if _, err := os.Stat(snapshot); err != nil {
		t.Fatalf("快照不存在：%v", err)
	}
	second := buildAndRun(t, root, snapshot, filepath.Join(outDir, "second"))
	if second != first {
		t.Fatalf("回放输出 %q，与首次 %q 不同", second, first)
	}
}
