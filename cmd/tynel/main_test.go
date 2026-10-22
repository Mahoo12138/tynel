package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

// fakeRunner 用固定表代替真实进程，让错误路径不依赖磁盘上的工具链。
type fakeRunner struct {
	paths map[string]string
	err   error
}

func (f fakeRunner) LookPath(name string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if path, ok := f.paths[name]; ok {
		return path, nil
	}
	return "", errors.New("不在工具表中")
}

func (f fakeRunner) Run(string, ...string) (toolchain.Output, error) {
	return toolchain.Output{}, nil
}

// newFakeProject 造一个只有锁文件和准备报告的项目根。
func newFakeProject(t *testing.T) (string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	sdkDir := filepath.Join(root, ".work", "toolchain", "go1.27.3-sdk-darwin-arm64")
	tools := map[string]string{
		"go":      filepath.Join(sdkDir, "bin", "go"),
		"compile": filepath.Join(sdkDir, "pkg", "tool", "darwin_arm64", "compile"),
		"link":    filepath.Join(sdkDir, "pkg", "tool", "darwin_arm64", "link"),
		"asm":     filepath.Join(sdkDir, "pkg", "tool", "darwin_arm64", "asm"),
	}
	lock := toolchain.Lock{SchemaVersion: 1}
	lock.Go.Version = "go1.27.3"
	lock.Go.SourceCommit = "2aae1000ea1d4b28923b96db28acf4e3b99d6aff"
	lock.Go.License = "BSD-3-Clause"
	lock.Targets.Host = "darwin/arm64"
	lock.Targets.Primary = "darwin/arm64"

	report := toolchain.Report{SchemaVersion: 1, LockedVersion: "go1.27.3", Target: "darwin/arm64"}
	report.Installs = []toolchain.Install{
		{Kind: "sdk", Name: "go1.27.3-sdk-darwin-arm64", Dir: sdkDir, Tools: tools},
		{Kind: "src", Name: "go1.27.3-src", Dir: filepath.Join(root, ".work", "toolchain", "go1.27.3-src")},
	}
	writeJSON(t, filepath.Join(root, filepath.FromSlash(toolchain.LockPath)), lock)
	writeJSON(t, filepath.Join(root, filepath.FromSlash(toolchain.ReportPath)), report)
	return root, tools
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败：%v", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写入 %s 失败：%v", path, err)
	}
}

func newTestApp(t *testing.T, runner toolchain.Runner) (*app, *bytes.Buffer) {
	t.Helper()
	root, _ := newFakeProject(t)
	out := &bytes.Buffer{}
	return &app{stdout: out, stderr: &bytes.Buffer{}, runner: runner, root: root, hasRoot: true}, out
}

func TestRunVersionShowsLockedIdentity(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != diagnostic.ExitOK {
		t.Fatalf("退出状态为 %d，期望 0；stderr：%s", code, errOut.String())
	}
	for _, want := range []string{"tynel dev", "darwin/arm64", "go1.27.3", "BSD-3-Clause", "tools/toolchain/go.lock.json"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("版本输出缺少 %q：\n%s", want, out.String())
		}
	}
}

func TestVersionReportsPreparedToolchain(t *testing.T) {
	a, out := newTestApp(t, fakeRunner{})
	if err := a.version(nil); err != nil {
		t.Fatalf("version 失败：%v", err)
	}
	if !strings.Contains(out.String(), "准备状态    已准备（go1.27.3-sdk-darwin-arm64）") {
		t.Errorf("应报告已准备状态：\n%s", out.String())
	}
}

func TestVersionWithoutProjectStillWorks(t *testing.T) {
	out := &bytes.Buffer{}
	a := &app{stdout: out, stderr: &bytes.Buffer{}, runner: fakeRunner{}}
	if err := a.version(nil); err != nil {
		t.Fatalf("不在项目目录内时 version 不应失败：%v", err)
	}
	if !strings.Contains(out.String(), "未知") {
		t.Errorf("应标注目标平台未知：\n%s", out.String())
	}
}

func TestHelpListsCommandsAndExitCodes(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"help"}, &out, &errOut); code != diagnostic.ExitOK {
		t.Fatalf("退出状态为 %d，期望 0", code)
	}
	for _, want := range []string{"version", "toolchain which", "退出状态", "环境缺失"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("帮助输出缺少 %q", want)
		}
	}
}

func TestHelpTopic(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"help", "toolchain"}, &out, &errOut); code != diagnostic.ExitOK {
		t.Fatalf("退出状态为 %d，期望 0", code)
	}
	if !strings.Contains(out.String(), "toolchain which") {
		t.Errorf("主题帮助应说明子命令：\n%s", out.String())
	}
}

func TestNoArgumentsIsUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != diagnostic.ExitUsage {
		t.Fatalf("退出状态为 %d，期望 %d", code, diagnostic.ExitUsage)
	}
	if !strings.Contains(errOut.String(), "用法") {
		t.Errorf("应把用法写到 stderr：%q", errOut.String())
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"build"}, &out, &errOut); code != diagnostic.ExitUsage {
		t.Fatalf("退出状态为 %d，期望 %d", code, diagnostic.ExitUsage)
	}
	if !strings.Contains(errOut.String(), "未知命令") {
		t.Errorf("应说明是未知命令：%q", errOut.String())
	}
}

func TestUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"version 多余参数", []string{"version", "extra"}},
		{"help 未知主题", []string{"help", "nope"}},
		{"help 多余参数", []string{"help", "version", "extra"}},
		{"toolchain 未知子命令", []string{"toolchain", "show"}},
		{"toolchain which 缺参数", []string{"toolchain", "which"}},
		{"toolchain which 未知工具", []string{"toolchain", "which", "nope"}},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		if code := run(c.args, &out, &errOut); code != diagnostic.ExitUsage {
			t.Errorf("%s：退出状态为 %d，期望 %d", c.name, code, diagnostic.ExitUsage)
		}
	}
}

func TestToolchainWhichResolves(t *testing.T) {
	root, tools := newFakeProject(t)
	out := &bytes.Buffer{}
	a := &app{stdout: out, stderr: &bytes.Buffer{}, runner: fakeRunner{paths: map[string]string{tools["go"]: tools["go"]}}, root: root, hasRoot: true}
	if err := a.whichTool("go"); err != nil {
		t.Fatalf("whichTool 失败：%v", err)
	}
	if got := strings.TrimSpace(out.String()); got != tools["go"] {
		t.Errorf("输出为 %q，期望 %q", got, tools["go"])
	}
}

func TestToolchainWhichMissingToolIsEnvironmentError(t *testing.T) {
	a, _ := newTestApp(t, fakeRunner{err: errors.New("permission denied")})
	err := a.whichTool("go")
	if err == nil {
		t.Fatal("工具不可用时应报错")
	}
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Errorf("退出状态为 %d，期望 %d", got, diagnostic.ExitEnvironment)
	}
	if !strings.Contains(err.Error(), "go") {
		t.Errorf("错误应包含工具名：%v", err)
	}
}

func TestToolchainShowReportsMissingTools(t *testing.T) {
	a, _ := newTestApp(t, fakeRunner{err: errors.New("gone")})
	err := a.showToolchain()
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Fatalf("退出状态为 %d，期望 %d（%v）", got, diagnostic.ExitEnvironment, err)
	}
	if !strings.Contains(err.Error(), "go") || !strings.Contains(err.Error(), "link") {
		t.Errorf("应列出不可用的工具：%v", err)
	}
}

func TestToolchainNotPrepared(t *testing.T) {
	root := t.TempDir()
	out := &bytes.Buffer{}
	a := &app{stdout: out, stderr: &bytes.Buffer{}, runner: fakeRunner{}, root: root, hasRoot: true}
	err := a.showToolchain()
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Fatalf("未准备工具链时退出状态为 %d，期望 %d", got, diagnostic.ExitEnvironment)
	}
}

func TestToolchainOutsideProject(t *testing.T) {
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: fakeRunner{}}
	err := a.toolchainCommand(nil)
	if got := diagnostic.ExitCode(err); got != diagnostic.ExitEnvironment {
		t.Fatalf("不在项目目录内时退出状态为 %d，期望 %d", got, diagnostic.ExitEnvironment)
	}
}
