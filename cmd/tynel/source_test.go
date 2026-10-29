package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/nir"
	"github.com/Mahoo12138/tynel/compiler/pipeline"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

// fileRunner 模拟后端工具：被调用时把 -o 指向的文件写出来，让构建能走完
// 「产物存在才发布」那条路径。
type fileRunner struct {
	calls  int
	exit   int
	stderr string
}

func (r *fileRunner) LookPath(name string) (string, error) { return name, nil }

func (r *fileRunner) Run(name string, args ...string) (toolchain.Output, error) {
	return r.RunEnv(nil, name, args...)
}

func (r *fileRunner) RunEnv(_ []string, _ string, args ...string) (toolchain.Output, error) {
	r.calls++
	if r.exit != 0 {
		return toolchain.Output{ExitCode: r.exit, Stderr: r.stderr}, nil
	}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-o" {
			if err := os.WriteFile(args[i+1], []byte("fake binary"), 0o755); err != nil {
				return toolchain.Output{}, err
			}
		}
	}
	return toolchain.Output{}, nil
}

// fakeProgram 记录被执行的产物路径，不真的启动进程。
type fakeProgram struct {
	calls int
	path  string
	code  int
}

func (p *fakeProgram) Run(path string, _, _ io.Writer) (int, error) {
	p.calls++
	p.path = path
	return p.code, nil
}

// newBackendProject 在假项目里补上修改版编译器与戳记，让 build.Run 能走到
// 调用外部工具那一步。
func newBackendProject(t *testing.T) string {
	t.Helper()
	root, _ := newFakeProject(t)
	compiler := filepath.Join(root, ".work", "bridge", "compile")
	if err := os.MkdirAll(filepath.Dir(compiler), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(compiler, []byte("fake compiler"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("fake compiler"))
	writeJSON(t, filepath.Join(root, ".work", "bridge", "stamp.json"), map[string]any{
		"schemaVersion":  1,
		"compilerSHA256": hex.EncodeToString(sum[:]),
		"sdkVersion":     "go1.27.3",
		"builtAt":        "2026-10-26T00:00:00+08:00",
		"sources":        map[string]string{},
	})
	return root
}

func okResult(file string) *pipeline.Result {
	return &pipeline.Result{
		SchemaVersion: pipeline.SchemaVersion,
		File:          file,
		SourceSHA256:  strings.Repeat("a", 64),
		SourceBytes:   32,
		Diagnostics:   []pipeline.Diagnostic{},
		Capability:    []pipeline.Finding{},
		Summary:       pipeline.Summary{Functions: 1, Verified: true},
		NIR: &nir.Module{
			SchemaVersion: nir.SchemaVersion,
			Entry:         "main",
			Functions: []nir.Function{
				{Name: "main", Result: nir.TypeVoid, Body: []nir.Stmt{{Op: nir.OpReturn}}},
			},
		},
	}
}

func sourceErrorResult(file string) *pipeline.Result {
	return &pipeline.Result{
		SchemaVersion: pipeline.SchemaVersion,
		File:          file,
		Diagnostics: []pipeline.Diagnostic{
			{File: file, Line: 2, Column: 7, Severity: "error", Code: 2322, Message: "Type 'string' is not assignable to type 'number'."},
		},
		Capability: []pipeline.Finding{},
	}
}

func capabilityResult(file string) *pipeline.Result {
	return &pipeline.Result{
		SchemaVersion: pipeline.SchemaVersion,
		File:          file,
		Diagnostics:   []pipeline.Diagnostic{},
		Capability: []pipeline.Finding{
			{File: file, Line: 6, Column: 13, Category: "能力限制", Code: "cap.void-value", Message: "void 不能当值"},
		},
	}
}

func exitOf(err error) int {
	if err == nil {
		return 0
	}
	var silent silentExit
	if errors.As(err, &silent) {
		return silent.code
	}
	return diagnostic.ExitCode(err)
}

func TestCheckReportsSourceErrorAndExitsOne(t *testing.T) {
	var out, errOut bytes.Buffer
	a := &app{stdout: &out, stderr: &errOut, runner: &fileRunner{}}
	a.frontend = func(file, want string) (*pipeline.Result, error) {
		return sourceErrorResult(file), nil
	}
	err := a.check([]string{"bad.ts"})
	if code := exitOf(err); code != diagnostic.ExitSource {
		t.Fatalf("退出状态为 %d，期望 1", code)
	}
	if !strings.Contains(errOut.String(), "bad.ts:2:7: error TS2322") {
		t.Errorf("诊断应写到 stderr 并带位置：%q", errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("失败时不应往 stdout 写东西：%q", out.String())
	}
}

func TestCheckCapabilityExitsThree(t *testing.T) {
	var errOut bytes.Buffer
	a := &app{stdout: &bytes.Buffer{}, stderr: &errOut, runner: &fileRunner{}}
	a.frontend = func(file, want string) (*pipeline.Result, error) {
		return capabilityResult(file), nil
	}
	if code := exitOf(a.check([]string{"x.ts"})); code != diagnostic.ExitCapability {
		t.Fatalf("退出状态为 %d，期望 3", code)
	}
	if !strings.Contains(errOut.String(), "cap.void-value") {
		t.Errorf("应报告能力错误码：%q", errOut.String())
	}
}

func TestCheckJSONGoesToStdoutOnly(t *testing.T) {
	var out, errOut bytes.Buffer
	a := &app{stdout: &out, stderr: &errOut, runner: &fileRunner{}}
	a.frontend = func(file, want string) (*pipeline.Result, error) {
		return okResult(file), nil
	}
	if err := a.check([]string{"add.ts", "--json"}); err != nil {
		t.Fatalf("check 失败：%v", err)
	}
	var decoded pipeline.Result
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout 不是合法 JSON：%v\n%s", err, out.String())
	}
	if decoded.Summary.Functions != 1 || !decoded.Summary.Verified {
		t.Errorf("结果里的摘要不对：%+v", decoded.Summary)
	}
	if errOut.Len() != 0 {
		t.Errorf("--json 时进度与诊断不应混进 stderr：%q", errOut.String())
	}
}

func TestBuildWritesArtifactAndRecord(t *testing.T) {
	root := newBackendProject(t)
	var out, errOut bytes.Buffer
	runner := &fileRunner{}
	a := &app{stdout: &out, stderr: &errOut, runner: runner, root: root, hasRoot: true}
	a.frontend = func(file, want string) (*pipeline.Result, error) {
		return okResult(file), nil
	}
	output := filepath.Join(root, "out", "app")
	if err := a.build([]string{"add.ts", "-o", output}); err != nil {
		t.Fatalf("build 失败：%v", err)
	}
	if runner.calls != 1 {
		t.Errorf("应调用一次后端工具，实际 %d 次", runner.calls)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("产物不存在：%v", err)
	}
	data, err := os.ReadFile(output + ".build.json")
	if err != nil {
		t.Fatalf("构建记录不存在：%v", err)
	}
	var record struct {
		Source struct {
			Path string `json:"path"`
		} `json:"source"`
		Input struct {
			Path string `json:"path"`
		} `json:"input"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("构建记录无法解析：%v", err)
	}
	if record.Source.Path != "add.ts" {
		t.Errorf("记录里的源码路径为 %q，期望 add.ts", record.Source.Path)
	}
	if !strings.Contains(record.Input.Path, ".work/build/snapshots/") {
		t.Errorf("内存 NIR 的记录应指向快照：%q", record.Input.Path)
	}
}

func TestBuildSourceErrorDoesNotTouchBackend(t *testing.T) {
	root := newBackendProject(t)
	runner := &fileRunner{}
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: runner, root: root, hasRoot: true}
	a.frontend = func(file, want string) (*pipeline.Result, error) {
		return sourceErrorResult(file), nil
	}
	err := a.build([]string{"bad.ts", "-o", filepath.Join(root, "out", "app")})
	if code := exitOf(err); code != diagnostic.ExitSource {
		t.Fatalf("退出状态为 %d，期望 1", code)
	}
	if runner.calls != 0 {
		t.Errorf("源码有错时不应调用后端，实际 %d 次", runner.calls)
	}
	if _, err := os.Stat(filepath.Join(root, "out", "app")); !os.IsNotExist(err) {
		t.Errorf("源码有错时不应产出文件")
	}
}

func TestRunDoesNotExecuteAfterBuildFailure(t *testing.T) {
	root := newBackendProject(t)
	runner := &fileRunner{exit: 1, stderr: "link: undefined reference"}
	program := &fakeProgram{}
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: runner, program: program, root: root, hasRoot: true}
	a.frontend = func(file, want string) (*pipeline.Result, error) {
		return okResult(file), nil
	}
	err := a.run([]string{"add.ts"})
	if code := exitOf(err); code != diagnostic.ExitInternal {
		t.Fatalf("链接失败应算内部故障，实际退出 %d（%v）", code, err)
	}
	if program.calls != 0 {
		t.Fatalf("构建失败后不应执行任何产物，实际执行了 %d 次", program.calls)
	}
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) || !strings.Contains(d.Detail, "link: undefined reference") {
		t.Errorf("应保留工具输出：%v", err)
	}
}

func TestRunPropagatesProgramExit(t *testing.T) {
	root := newBackendProject(t)
	program := &fakeProgram{code: 7}
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: &fileRunner{}, program: program, root: root, hasRoot: true}
	a.frontend = func(file, want string) (*pipeline.Result, error) {
		return okResult(file), nil
	}
	err := a.run([]string{"add.ts"})
	if code := exitOf(err); code != 7 {
		t.Fatalf("应原样传出程序退出状态 7，实际 %d", code)
	}
	if program.calls != 1 {
		t.Fatalf("应执行一次产物，实际 %d 次", program.calls)
	}
	if !strings.Contains(program.path, filepath.Join(".work", "run", "app")) {
		t.Errorf("产物应放在 .work/run/app，实际 %q", program.path)
	}
}

func TestDumpPrintsJSONOnly(t *testing.T) {
	var out, errOut bytes.Buffer
	a := &app{stdout: &out, stderr: &errOut, runner: &fileRunner{}}
	a.frontend = func(file, want string) (*pipeline.Result, error) {
		result := okResult(file)
		result.Facts = &frontend.Facts{SchemaVersion: 1, Session: "s", File: file}
		return result, nil
	}
	if err := a.dump([]string{"facts", "add.ts"}); err != nil {
		t.Fatalf("dump 失败：%v", err)
	}
	var facts frontend.Facts
	if err := json.Unmarshal(out.Bytes(), &facts); err != nil {
		t.Fatalf("stdout 不是合法 JSON：%v\n%s", err, out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("dump 不应写 stderr：%q", errOut.String())
	}
}

func TestDumpNIRRefusesSourceError(t *testing.T) {
	var out, errOut bytes.Buffer
	a := &app{stdout: &out, stderr: &errOut, runner: &fileRunner{}}
	a.frontend = func(file, want string) (*pipeline.Result, error) {
		return sourceErrorResult(file), nil
	}
	err := a.dump([]string{"nir", "bad.ts"})
	if code := exitOf(err); code != diagnostic.ExitSource {
		t.Fatalf("退出状态为 %d，期望 1", code)
	}
	if out.Len() != 0 {
		t.Errorf("失败时不应输出 JSON：%q", out.String())
	}
}

func TestFrontendEnvironmentErrorIsNotSourceError(t *testing.T) {
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: &fileRunner{}}
	a.frontend = func(file, want string) (*pipeline.Result, error) {
		return nil, diagnostic.New(diagnostic.Environment, "host.no-tree", "找不到前端源码")
	}
	err := a.check([]string{"x.ts"})
	if code := exitOf(err); code != diagnostic.ExitEnvironment {
		t.Fatalf("环境缺失应为 4，实际 %d", code)
	}
}

func TestResolveSourceUsesProjectRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "子 目录"), 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "子 目录", "入口.ts")
	if err := os.WriteFile(entry, []byte("console.log(1);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(entry)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{}
	got, err := a.resolveSource(sourceArgs{root: root, file: "子 目录/入口.ts"})
	if err != nil {
		t.Fatalf("解析项目根失败：%v", err)
	}
	if got != want {
		t.Fatalf("入口为 %q，期望 %q", got, want)
	}
}

func TestResolveSourceRejectsMissing(t *testing.T) {
	a := &app{}
	_, err := a.resolveSource(sourceArgs{root: t.TempDir(), file: "缺失.ts"})
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) || d.Code != "project.missing" {
		t.Fatalf("错误为 %v，期望 project.missing", err)
	}
}
