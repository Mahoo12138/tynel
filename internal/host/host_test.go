package host

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/pipeline"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

type fakeRunner struct {
	calls  int
	exit   int
	stdout string
	stderr string
}

func (f *fakeRunner) LookPath(name string) (string, error) { return name, nil }

func (f *fakeRunner) Run(name string, args ...string) (toolchain.Output, error) {
	return f.RunEnv(nil, name, args...)
}

func (f *fakeRunner) RunEnv(_ []string, _ string, _ ...string) (toolchain.Output, error) {
	f.calls++
	return toolchain.Output{ExitCode: f.exit, Stdout: f.stdout, Stderr: f.stderr}, nil
}

// newRoot 造一棵最小项目：消费者源码、适配包源码、已同步的源码树和工具链报告。
func newRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("bridge/tsgo/consumer/main.go", "package main\n")
	write("bridge/tsgo/consumer/go.mod", "module example\n")
	write("bridge/tsgo/consumer/go.sum", "")
	write("bridge/tsgo/_adapter/adapter.go", "package tyneladapter\n")
	write("compiler/capability/capability.go", "package capability\n")
	write(".work/frontend/typescript-go/tyneladapter/adapter.go", "package tyneladapter\n")

	sdkDir := filepath.Join(root, ".work/toolchain/go1.27.3-sdk-darwin-arm64")
	report := toolchain.Report{SchemaVersion: 1, LockedVersion: "go1.27.3", Target: "darwin/arm64"}
	report.Installs = []toolchain.Install{{Kind: "sdk", Name: "go1.27.3-sdk-darwin-arm64", Dir: sdkDir}}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.FromSlash(toolchain.ReportPath), string(data))
	return root
}

// stampBinary 写一份与当前源码匹配的戳记，模拟「宿主已构建且不过期」。
func stampBinary(t *testing.T, root string) string {
	t.Helper()
	binary := filepath.Join(root, filepath.FromSlash(BinaryPath))
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("fake host"), 0o755); err != nil {
		t.Fatal(err)
	}
	sources, err := hashSources(root)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := hashFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeStamp(filepath.Join(root, filepath.FromSlash(StampPath)), &stamp{
		SchemaVersion: 1,
		BinarySHA256:  sum,
		BuiltAt:       time.Now().Format(time.RFC3339),
		Sources:       sources,
	}); err != nil {
		t.Fatal(err)
	}
	return binary
}

func exitOf(err error) int {
	if err == nil {
		return 0
	}
	return diagnostic.ExitCode(err)
}

func TestEnsureReusesFreshBinary(t *testing.T) {
	root := newRoot(t)
	stampBinary(t, root)
	runner := &fakeRunner{}
	h, err := Ensure(root, runner, nil)
	if err != nil {
		t.Fatalf("Ensure 失败：%v", err)
	}
	if runner.calls != 0 {
		t.Errorf("戳记匹配时不应重建，实际调用 %d 次", runner.calls)
	}
	if !strings.HasSuffix(h.Path, "consumer") {
		t.Errorf("宿主路径不对：%q", h.Path)
	}
}

func TestEnsureRebuildsWhenSourceChanges(t *testing.T) {
	root := newRoot(t)
	stampBinary(t, root)
	path := filepath.Join(root, filepath.FromSlash(ConsumerDir), "main.go")
	if err := os.WriteFile(path, []byte("package main\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{exit: 1, stderr: "cannot find module"}
	_, err := Ensure(root, runner, nil)
	if exitOf(err) != diagnostic.ExitEnvironment {
		t.Fatalf("重建失败应为环境缺失，实际 %d（%v）", exitOf(err), err)
	}
	if runner.calls != 1 {
		t.Errorf("源码变化后应重建一次，实际 %d 次", runner.calls)
	}
	if !strings.Contains(err.Error(), "前端宿主构建失败") {
		t.Errorf("错误应说明是宿主构建失败：%v", err)
	}
}

func TestEnsureRebuildsWhenCompilerChanges(t *testing.T) {
	root := newRoot(t)
	stampBinary(t, root)
	path := filepath.Join(root, "compiler", "capability", "capability.go")
	if err := os.WriteFile(path, []byte("package capability\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{exit: 1, stderr: "cannot find module"}
	_, err := Ensure(root, runner, nil)
	if exitOf(err) != diagnostic.ExitEnvironment {
		t.Fatalf("重建失败应为环境缺失，实际 %d（%v）", exitOf(err), err)
	}
	if runner.calls != 1 {
		t.Errorf("compiler 源码变化后应重建一次，实际 %d 次", runner.calls)
	}
}

func TestEnsureRejectsStaleAdapterCopy(t *testing.T) {
	root := newRoot(t)
	stampBinary(t, root)
	path := filepath.Join(root, filepath.FromSlash(TreeDir), "tyneladapter", "adapter.go")
	if err := os.WriteFile(path, []byte("package tyneladapter\n// edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	_, err := Ensure(root, runner, nil)
	if exitOf(err) != diagnostic.ExitEnvironment {
		t.Fatalf("适配包不一致应为环境缺失，实际 %d（%v）", exitOf(err), err)
	}
	if !strings.Contains(err.Error(), "prep --frontend-only") {
		t.Errorf("错误应给出修复命令：%v", err)
	}
	if runner.calls != 0 {
		t.Errorf("适配包不一致时不应尝试构建，实际调用 %d 次", runner.calls)
	}
}

func TestEnsureWithoutTree(t *testing.T) {
	root := t.TempDir()
	_, err := Ensure(root, &fakeRunner{}, nil)
	if exitOf(err) != diagnostic.ExitEnvironment {
		t.Fatalf("缺少源码树应为环境缺失，实际 %d", exitOf(err))
	}
}

func TestAnalyzeDecodesResult(t *testing.T) {
	result := &pipeline.Result{SchemaVersion: pipeline.SchemaVersion, File: "/tmp/a.ts", Summary: pipeline.Summary{Functions: 2, Verified: true}}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	h := &Host{Path: "/tmp/consumer"}
	got, err := h.Analyze("a.ts", "check", &fakeRunner{stdout: string(data)})
	if err != nil {
		t.Fatalf("Analyze 失败：%v", err)
	}
	if got.Summary.Functions != 2 || !got.Summary.Verified {
		t.Errorf("结果不对：%+v", got.Summary)
	}
}

func TestAnalyzeMapsHostExit(t *testing.T) {
	h := &Host{Path: "/tmp/consumer"}
	_, err := h.Analyze("a.ts", "check", &fakeRunner{exit: diagnostic.ExitInternal, stderr: "boom"})
	if exitOf(err) != diagnostic.ExitInternal {
		t.Fatalf("宿主内部故障应映射为 5，实际 %d", exitOf(err))
	}
	_, err = h.Analyze("a.ts", "check", &fakeRunner{exit: diagnostic.ExitEnvironment, stderr: "missing"})
	if exitOf(err) != diagnostic.ExitEnvironment {
		t.Fatalf("宿主环境失败应映射为 4，实际 %d", exitOf(err))
	}
}

func TestAnalyzeRejectsUnknownFields(t *testing.T) {
	h := &Host{Path: "/tmp/consumer"}
	_, err := h.Analyze("a.ts", "check", &fakeRunner{stdout: `{"schemaVersion":1,"surprise":true}`})
	if exitOf(err) != diagnostic.ExitInternal {
		t.Fatalf("协议不匹配应为内部故障，实际 %d（%v）", exitOf(err), err)
	}
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) || d.Code != "host.bad-result" {
		t.Errorf("错误码应为 host.bad-result：%v", err)
	}
}
