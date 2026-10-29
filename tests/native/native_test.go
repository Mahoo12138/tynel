// Package native 是后端原生回归：把 tests/native/fixtures 里的 NIR 编成
// 原生程序，运行并核对输出，同时检查反汇编与重复构建的一致性。
//
// 它需要 01.01 准备的工具链和 01.05 构建的修改版编译器；缺任何一个都直接
// 失败，而不是跳过——跳过会让「零断言」看起来像通过。
package native

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

// TestLoopTerminates 用独立进程和超时运行 while 累加。条件或回边写错时，
// 程序可能永远不退出；没有超时的测试会挂住整个包，而不是给出一条失败。
func TestLoopTerminates(t *testing.T) {
	root := repoRoot(t)
	output := filepath.Join(t.TempDir(), "sum")
	if _, err := build.Run(build.Options{
		Root:   root,
		Input:  filepath.Join(root, "tests", "native", "fixtures", "sum.nir.json"),
		Output: output,
	}, toolchain.Exec{}); err != nil {
		t.Fatalf("构建循环夹具失败：%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, output)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatal("循环夹具在 5 秒内没有结束；检查条件求值与回边")
	}
	if err != nil {
		t.Fatalf("运行循环夹具失败：%v\n%s", err, out)
	}
	if got := string(out); got != "55\n" {
		t.Fatalf("循环夹具输出 %q，期望 %q", got, "55\n")
	}
}

// TestRecursionTerminates 用独立进程和超时运行递归阶乘，确认调用层正常返回。
func TestRecursionTerminates(t *testing.T) {
	root := repoRoot(t)
	output := filepath.Join(t.TempDir(), "fact")
	if _, err := build.Run(build.Options{
		Root:   root,
		Input:  filepath.Join(root, "tests", "native", "fixtures", "recursion.nir.json"),
		Output: output,
	}, toolchain.Exec{}); err != nil {
		t.Fatalf("构建递归夹具失败：%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, output)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatal("递归夹具在 5 秒内没有结束")
	}
	if err != nil {
		t.Fatalf("运行递归夹具失败：%v\n%s", err, out)
	}
	if got := string(out); got != "120\n" {
		t.Fatalf("递归夹具输出 %q，期望 %q", got, "120\n")
	}
}

// TestUnboundedRecursionBuildsWithoutRunning 只构建没有终止条件的递归函数，
// 不执行产物。编译器必须在有限时间内结束，不能尝试在编译期算出调用结果。
func TestUnboundedRecursionBuildsWithoutRunning(t *testing.T) {
	root := repoRoot(t)
	done := make(chan error, 1)
	go func() {
		_, err := build.Run(build.Options{
			Root:   root,
			Input:  filepath.Join(root, "tests", "native", "fixtures", "recursion-unbounded.nir.json"),
			Output: filepath.Join(t.TempDir(), "unbounded"),
		}, toolchain.Exec{})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("构建无终止递归失败：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("编译器在 5 秒内没有完成构建；它不应在编译期执行递归")
	}
}

// TestCombinationTerminates 在同一个模块里同时运行循环与递归，确认组合后
// 仍在有限时间内结束。
func TestCombinationTerminates(t *testing.T) {
	root := repoRoot(t)
	output := filepath.Join(t.TempDir(), "combination")
	if _, err := build.Run(build.Options{
		Root:   root,
		Input:  filepath.Join(root, "tests", "native", "fixtures", "combination.nir.json"),
		Output: output,
	}, toolchain.Exec{}); err != nil {
		t.Fatalf("构建组合夹具失败：%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, output)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatal("组合夹具在 5 秒内没有结束")
	}
	if err != nil {
		t.Fatalf("运行组合夹具失败：%v\n%s", err, out)
	}
	if got := string(out); got != "55\n120\n" {
		t.Fatalf("组合夹具输出 %q，期望 %q", got, "55\n120\n")
	}
}

// TestPrintedNumbersReadBack 把输出逐行读回成 binary64，再按位比较。
//
// 只比字符串不够：`-0` 与 `0` 是两行不同的文本，但格式化器完全可能把它们写成
// 一样。反过来，`0.30000000000000004` 只有解析回 binary64 才能确认它就是
// 0.1 + 0.2 的那个值，而不是被四舍五入过的近似。
func TestPrintedNumbersReadBack(t *testing.T) {
	root := repoRoot(t)
	output := filepath.Join(t.TempDir(), "specials")
	text := buildAndRun(t, root, filepath.Join(root, "tests", "native", "fixtures", "specials.nir.json"), output)

	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("期望 5 行输出，实际 %d 行：%q", len(lines), text)
	}
	type want struct {
		text  string
		check func(float64) bool
	}
	cases := []want{
		{"-0", func(v float64) bool { return v == 0 && math.Signbit(v) }},
		{"NaN", math.IsNaN},
		{"+Inf", func(v float64) bool { return math.IsInf(v, 1) }},
		{"-Inf", func(v float64) bool { return math.IsInf(v, -1) }},
		// 用变量相加，而不是常量表达式 0.1+0.2：Go 的常量算术是精确的，
		// 0.3 会取到最近的 binary64，与运行时 0.1 加 0.2 的结果不是同一个数。
		{"0.30000000000000004", func(v float64) bool {
			a, b := 0.1, 0.2
			return v == a+b
		}},
	}
	for i, c := range cases {
		if lines[i] != c.text {
			t.Errorf("第 %d 行是 %q，期望 %q", i+1, lines[i], c.text)
		}
		v, err := strconv.ParseFloat(lines[i], 64)
		if err != nil {
			t.Errorf("第 %d 行 %q 读不回来：%v", i+1, lines[i], err)
			continue
		}
		if !c.check(v) {
			t.Errorf("第 %d 行 %q 读回的值不对", i+1, lines[i])
		}
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
