package build

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

// fakeRunner 记录调用并按设置产出文件，避免测试依赖真实的 Go 工具链。
type fakeRunner struct {
	exit        int
	stderr      string
	err         error
	writeOutput bool
	calls       int
	lastEnv     []string
	lastArgs    []string
}

func (f *fakeRunner) LookPath(name string) (string, error) { return name, nil }
func (f *fakeRunner) Run(name string, args ...string) (toolchain.Output, error) {
	return f.RunEnv(nil, name, args...)
}

func (f *fakeRunner) RunEnv(env []string, name string, args ...string) (toolchain.Output, error) {
	f.calls++
	f.lastEnv = env
	f.lastArgs = args
	if f.err != nil {
		return toolchain.Output{}, f.err
	}
	if f.writeOutput {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-o" {
				if err := os.WriteFile(args[i+1], []byte("binary-content"), 0o755); err != nil {
					return toolchain.Output{}, err
				}
			}
		}
	}
	return toolchain.Output{ExitCode: f.exit, Stderr: f.stderr}, nil
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败：%v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 %s 失败：%v", path, err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	writeFile(t, path, string(data))
}

// newProject 造一个最小项目：准备报告、bridge 源码、编译器二进制和一致的戳记。
func newProject(t *testing.T) (string, *fakeRunner) {
	t.Helper()
	root := t.TempDir()
	sdk := filepath.Join(root, ".work", "toolchain", "go1.27.3-sdk-darwin-arm64")
	writeFile(t, filepath.Join(sdk, "src", "cmd", "compile", "internal", "gc", "main.go"), "package gc\n")
	writeFile(t, filepath.Join(sdk, "bin", "go"), "#!/bin/sh\n")

	report := toolchain.Report{SchemaVersion: 1, LockedVersion: "go1.27.3", Target: "darwin/arm64"}
	report.Installs = []toolchain.Install{{Kind: "sdk", Name: "go1.27.3-sdk-darwin-arm64", Dir: sdk}}
	writeJSON(t, filepath.Join(root, filepath.FromSlash(toolchain.ReportPath)), report)

	bridgeSrc := filepath.Join(root, "bridge", "go", "gc", "tynel_bridge.go")
	patchSrc := filepath.Join(root, "bridge", "go", "patches", "0001-inject.patch")
	writeFile(t, bridgeSrc, "package gc\n")
	writeFile(t, patchSrc, "patch\n")

	compiler := filepath.Join(root, filepath.FromSlash(CompilerPath))
	writeFile(t, compiler, "compiler-binary")

	stamp := stamp{
		SchemaVersion:  1,
		CompilerSHA256: sha256Hex([]byte("compiler-binary")),
		SDKVersion:     "go1.27.3",
		Sources: map[string]string{
			"bridge/go/gc/tynel_bridge.go":        sha256Hex([]byte("package gc\n")),
			"bridge/go/patches/0001-inject.patch": sha256Hex([]byte("patch\n")),
			"sdk/cmd/compile/internal/gc/main.go": sha256Hex([]byte("package gc\n")),
		},
	}
	writeJSON(t, filepath.Join(root, filepath.FromSlash(StampPath)), stamp)

	return root, &fakeRunner{}
}

func exampleNIR(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "ir", "add.nir.json"))
	if err != nil {
		t.Fatalf("读取示例 NIR 失败：%v", err)
	}
	return data
}

func TestRunPublishesArtifactAndRecord(t *testing.T) {
	root, runner := newProject(t)
	runner.writeOutput = true
	input := filepath.Join(root, "add.nir.json")
	writeFile(t, input, string(exampleNIR(t)))
	output := filepath.Join(root, "out", "app")

	record, err := Run(Options{Root: root, Input: input, Output: output}, runner)
	if err != nil {
		t.Fatalf("构建失败：%v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "binary-content" {
		t.Fatalf("产物内容不符：%q %v", data, err)
	}
	if record.Output.Bytes != int64(len("binary-content")) {
		t.Errorf("记录里的产物大小为 %d", record.Output.Bytes)
	}
	if record.Input.SHA256 != sha256Hex(exampleNIR(t)) {
		t.Errorf("记录里的输入哈希不符")
	}
	if _, err := os.Stat(SnapshotDirPath(root, record)); err != nil {
		t.Errorf("快照文件不存在：%v", err)
	}
	if _, err := os.Stat(RecordPath(output)); err != nil {
		t.Errorf("构建记录不存在：%v", err)
	}
}

func SnapshotDirPath(root string, record *Record) string {
	if filepath.IsAbs(record.Tools.Snapshot) {
		return record.Tools.Snapshot
	}
	return filepath.Join(root, record.Tools.Snapshot)
}

func TestRunRejectsBadNIRBeforeBuild(t *testing.T) {
	root, runner := newProject(t)
	input := filepath.Join(root, "bad.nir.json")
	writeFile(t, input, `{"schemaVersion": 99}`)
	output := filepath.Join(root, "out", "app")
	writeFile(t, output, "old")

	_, err := Run(Options{Root: root, Input: input, Output: output}, runner)
	if err == nil {
		t.Fatal("坏 NIR 应被拒绝")
	}
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitSource {
		t.Errorf("退出状态为 %d，期望 %d", got, diagnostic.ExitSource)
	}
	if runner.calls != 0 {
		t.Errorf("坏输入不应启动构建，实际调用了 %d 次", runner.calls)
	}
	if data, _ := os.ReadFile(output); string(data) != "old" {
		t.Errorf("失败时改动了旧产物：%q", data)
	}
}

func TestRunRejectsMissingCompiler(t *testing.T) {
	root, runner := newProject(t)
	os.Remove(filepath.Join(root, filepath.FromSlash(CompilerPath)))
	input := filepath.Join(root, "add.nir.json")
	writeFile(t, input, string(exampleNIR(t)))

	_, err := Run(Options{Root: root, Input: input, Output: filepath.Join(root, "out", "app")}, runner)
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Fatalf("退出状态为 %d，期望 %d（%v）", got, diagnostic.ExitEnvironment, err)
	}
	if !strings.Contains(err.Error(), "bridge/go/build") {
		t.Errorf("错误应提示先构建编译器：%v", err)
	}
}

func TestRunRejectsStaleCompiler(t *testing.T) {
	root, runner := newProject(t)
	writeFile(t, filepath.Join(root, "bridge", "go", "gc", "tynel_bridge.go"), "package gc\n// changed\n")
	input := filepath.Join(root, "add.nir.json")
	writeFile(t, input, string(exampleNIR(t)))
	output := filepath.Join(root, "out", "app")
	writeFile(t, output, "old")

	_, err := Run(Options{Root: root, Input: input, Output: output}, runner)
	if err == nil {
		t.Fatal("过期的编译器应被拒绝")
	}
	if !strings.Contains(err.Error(), "stale-compiler") {
		t.Errorf("错误码不是 build.stale-compiler：%v", err)
	}
	if runner.calls != 0 {
		t.Errorf("过期编译器不应启动构建")
	}
	if data, _ := os.ReadFile(output); string(data) != "old" {
		t.Errorf("失败时改动了旧产物：%q", data)
	}
}

func TestRunKeepsOldArtifactOnBuildFailure(t *testing.T) {
	root, runner := newProject(t)
	runner.exit = 1
	runner.stderr = "link: undefined: main.add\n"
	input := filepath.Join(root, "add.nir.json")
	writeFile(t, input, string(exampleNIR(t)))
	output := filepath.Join(root, "out", "app")
	writeFile(t, output, "old")

	_, err := Run(Options{Root: root, Input: input, Output: output}, runner)
	if err == nil {
		t.Fatal("构建失败应返回错误")
	}
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitInternal {
		t.Errorf("退出状态为 %d，期望 %d（内部故障）", got, diagnostic.ExitInternal)
	}
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) || !strings.Contains(d.Detail, "link: undefined") {
		t.Errorf("错误应保留工具输出：%v", err)
	}
	if data, _ := os.ReadFile(output); string(data) != "old" {
		t.Errorf("失败时覆盖了旧产物：%q", data)
	}
	entries, _ := os.ReadDir(filepath.Dir(output))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tynel-build-") {
			t.Errorf("留下了临时文件 %s", e.Name())
		}
	}
}

func TestRunRejectsUnwritableOutput(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("以 root 运行时权限检查不生效")
	}
	root, runner := newProject(t)
	runner.writeOutput = true
	input := filepath.Join(root, "add.nir.json")
	writeFile(t, input, string(exampleNIR(t)))
	dir := filepath.Join(root, "out")
	writeFile(t, filepath.Join(dir, "app"), "old")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)

	_, err := Run(Options{Root: root, Input: input, Output: filepath.Join(dir, "app")}, runner)
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Fatalf("退出状态为 %d，期望 %d（%v）", got, diagnostic.ExitEnvironment, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "app")); string(data) != "old" {
		t.Errorf("失败时改动了旧产物：%q", data)
	}
}

func TestRunAcceptsPathsWithSpaces(t *testing.T) {
	root, runner := newProject(t)
	runner.writeOutput = true
	dir := filepath.Join(root, "带 空格 的目录")
	input := filepath.Join(dir, "add.nir.json")
	writeFile(t, input, string(exampleNIR(t)))
	output := filepath.Join(dir, "out dir", "app")

	record, err := Run(Options{Root: root, Input: input, Output: output}, runner)
	if err != nil {
		t.Fatalf("带空格的路径应可用：%v", err)
	}
	if record.Output.Path != output {
		t.Errorf("记录里的输出路径为 %q", record.Output.Path)
	}
	if _, err := os.Stat(output); err != nil {
		t.Errorf("产物不存在：%v", err)
	}
}

func TestRunRejectsMissingArguments(t *testing.T) {
	_, runner := newProject(t)
	for _, opts := range []Options{{}, {Root: "x"}, {Root: "x", Input: "y"}} {
		if err := func() error { _, err := Run(opts, runner); return err }(); diagnostic.ExitCode(err) != diagnostic.ExitUsage {
			t.Errorf("%+v：应返回用法错误", opts)
		}
	}
}
