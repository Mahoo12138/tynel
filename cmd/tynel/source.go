package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/graph"
	"github.com/Mahoo12138/tynel/compiler/modules"
	"github.com/Mahoo12138/tynel/compiler/nir"
	"github.com/Mahoo12138/tynel/compiler/pipeline"
	"github.com/Mahoo12138/tynel/compiler/project"
	"github.com/Mahoo12138/tynel/internal/build"
	"github.com/Mahoo12138/tynel/internal/host"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

// silentExit 表示「细节已经报告过，只需要设置退出状态」。
//
// check 与 run 会先把诊断或程序输出写完，再决定退出状态；这时再让上层打印
// 一条汇总诊断，会把同一件事说两遍。
type silentExit struct{ code int }

func (e silentExit) Error() string { return fmt.Sprintf("退出状态 %d", e.code) }

// programRunner 执行构建产物。抽成接口是为了让测试不真的启动子进程。
type programRunner interface {
	Run(path string, stdout, stderr io.Writer) (int, error)
}

type execProgram struct{}

func (execProgram) Run(path string, stdout, stderr io.Writer) (int, error) {
	cmd := exec.Command(path)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}

func (a *app) programRunner() programRunner {
	if a.program != nil {
		return a.program
	}
	return execProgram{}
}

// sourceArgs 是 check、build、run 共用的参数形状。
type sourceArgs struct {
	file   string
	output string
	root   string
	json   bool
}

func parseSourceArgs(command string, args []string, allowOutput, allowJSON bool) (sourceArgs, error) {
	var parsed sourceArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case allowOutput && arg == "-o":
			if i+1 >= len(args) {
				return parsed, diagnostic.New(diagnostic.Usage, "cli.missing-argument", "%s 的 -o 需要输出路径", command)
			}
			parsed.output = args[i+1]
			i++
		case allowOutput && strings.HasPrefix(arg, "-o="):
			parsed.output = strings.TrimPrefix(arg, "-o=")
		case allowJSON && arg == "--json":
			parsed.json = true
		case arg == "--root":
			if i+1 >= len(args) {
				return parsed, diagnostic.New(diagnostic.Usage, "cli.missing-argument", "%s 的 --root 需要目录", command)
			}
			parsed.root = args[i+1]
			i++
		case strings.HasPrefix(arg, "--root="):
			parsed.root = strings.TrimPrefix(arg, "--root=")
		case strings.HasPrefix(arg, "-"):
			return parsed, diagnostic.New(diagnostic.Usage, "cli.bad-flag", "%s 不认识选项 %q", command, arg)
		case parsed.file == "":
			parsed.file = arg
		default:
			return parsed, diagnostic.New(diagnostic.Usage, "cli.unexpected-argument", "%s 只接受一个源文件：%q", command, arg)
		}
	}
	if parsed.file == "" {
		return parsed, diagnostic.New(diagnostic.Usage, "cli.missing-argument", "%s 需要一份 TypeScript 文件", command)
	}
	if allowOutput && parsed.output == "" {
		return parsed, diagnostic.New(diagnostic.Usage, "cli.missing-argument", "%s 需要 -o 指定输出路径", command)
	}
	return parsed, nil
}

// resolveSource 把入口固定到项目根下，并返回给前端宿主的绝对路径。
// 没有 --root 时沿用仓库根，保持单文件示例的旧行为；显式 --root 时，入口
// 按该项目根解释，而不是按当前工作目录解释。
func (a *app) resolveSource(parsed sourceArgs) (string, error) {
	if a.frontend != nil {
		// 测试用：假前端不读磁盘，路径原样传入。
		return parsed.file, nil
	}
	root := a.root
	if parsed.root != "" {
		root = parsed.root
	}
	if root == "" {
		return "", diagnostic.New(diagnostic.Environment, "project.not-found", "找不到项目根；用 --root 指定目录")
	}
	p, err := project.Open(root)
	if err != nil {
		return "", err
	}
	file, err := p.ResolveEntry(parsed.file)
	if err != nil {
		return "", err
	}
	return file.Abs, nil
}

// hostScanner 把前端宿主的导入列表适配成依赖图扫描器。
type hostScanner struct {
	host   *host.Host
	runner toolchain.Runner
}

func (s hostScanner) Imports(file project.File) ([]graph.Import, error) {
	items, err := s.host.Imports(file.Abs, s.runner)
	if err != nil {
		return nil, err
	}
	out := make([]graph.Import, 0, len(items))
	for _, item := range items {
		out = append(out, graph.Import{
			Specifier: item.Specifier,
			Start:     item.Start,
			Length:    item.Length,
			TypeOnly:  item.TypeOnly,
			Resolved:  item.Resolved,
		})
	}
	return out, nil
}

// loadGraph 在检查或降低之前把入口和全部显式相对依赖固定成快照。
func (a *app) loadGraph(parsed sourceArgs) (*graph.Graph, error) {
	if a.frontend != nil {
		return nil, nil
	}
	root := a.root
	if parsed.root != "" {
		root = parsed.root
	}
	if root == "" {
		return nil, diagnostic.New(diagnostic.Environment, "project.not-found", "找不到项目根；用 --root 指定目录")
	}
	p, err := project.Open(root)
	if err != nil {
		return nil, err
	}
	entry, err := p.ResolveEntry(parsed.file)
	if err != nil {
		return nil, err
	}
	if !a.hasRoot {
		return nil, diagnostic.New(diagnostic.Environment, "project.not-found", "不在项目目录内：向上找不到 %s", toolchain.LockPath)
	}
	front, err := host.Ensure(a.root, a.runner, a.stderr)
	if err != nil {
		return nil, err
	}
	return graph.Load(p, entry, hostScanner{host: front, runner: a.runner})
}

// loadModules 为图里的每个文件取事实，再合成模块符号表。
func (a *app) loadModules(parsed sourceArgs) (*modules.Table, error) {
	g, err := a.loadGraph(parsed)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, diagnostic.New(diagnostic.Internal, "modules.test-frontend", "假前端不能建立模块符号表")
	}
	front, err := host.Ensure(a.root, a.runner, a.stderr)
	if err != nil {
		return nil, err
	}
	facts := make([]*frontend.Facts, 0, len(g.Files))
	for _, file := range g.Files {
		deps := make([]string, 0, len(g.Files)-1)
		for _, other := range g.Files {
			if other.Abs != file.Abs {
				deps = append(deps, other.Abs)
			}
		}
		item, err := front.Facts(file.Abs, deps, a.runner)
		if err != nil {
			return nil, err
		}
		facts = append(facts, item)
	}
	return modules.Build(facts[0], facts[1:])
}

// analyze 准备前端宿主并检查一个文件。want 决定结果里附带什么。// analyze 准备前端宿主并检查一个文件。want 决定结果里附带什么。
func (a *app) analyze(file, want string, deps []string) (*pipeline.Result, error) {
	if a.frontend != nil {
		// 测试用：直接给出前端结果，不启动宿主进程。
		return a.frontend(file, want)
	}
	if !a.hasRoot {
		return nil, diagnostic.New(diagnostic.Environment, "project.not-found", "不在项目目录内：向上找不到 %s", toolchain.LockPath)
	}
	front, err := host.Ensure(a.root, a.runner, a.stderr)
	if err != nil {
		return nil, err
	}
	return front.AnalyzeFiles(file, deps, want, a.runner)
}

// render 把诊断与能力结论写到 stderr。stdout 只留给命令的结果。
func (a *app) render(result *pipeline.Result) {
	for _, d := range result.Diagnostics {
		fmt.Fprintf(a.stderr, "%s:%d:%d: %s TS%d: %s\n", d.File, d.Line, d.Column, d.Severity, d.Code, d.Message)
	}
	for _, f := range result.Capability {
		fmt.Fprintf(a.stderr, "%s:%d:%d: %s %s: %s\n", f.File, f.Line, f.Column, f.Category, f.Code, f.Message)
	}
}

// exitFor 把结果映射成「已经报告过」的退出状态。
func exitFor(result *pipeline.Result) error {
	if code := result.ExitCode(); code != 0 {
		return silentExit{code}
	}
	return nil
}

// check 只做源级检查与 NIR 验证，不生成机器码，也不要求后端工具可用。
func (a *app) check(args []string) error {
	parsed, err := parseSourceArgs("check", args, false, true)
	if err != nil {
		return err
	}
	file, err := a.resolveSource(parsed)
	if err != nil {
		return err
	}
	g, err := a.loadGraph(parsed)
	if err != nil {
		return err
	}
	result, err := a.analyze(file, "check", graphDeps(g))
	if err != nil {
		return err
	}
	if parsed.json {
		if err := pipeline.Encode(a.stdout, result); err != nil {
			return diagnostic.New(diagnostic.Internal, "cli.write-failed", "写出检查结果失败").WithDetail(err.Error())
		}
		return exitFor(result)
	}
	a.render(result)
	if err := exitFor(result); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "ok: %s（%d 个函数，NIR 已验证）\n", result.File, result.Summary.Functions)
	return nil
}

// build 从一份 TypeScript 源文件生成原生程序。构建成功也不运行它。
func (a *app) build(args []string) error {
	parsed, err := parseSourceArgs("build", args, true, false)
	if err != nil {
		return err
	}
	file, err := a.resolveSource(parsed)
	if err != nil {
		return err
	}
	g, err := a.loadGraph(parsed)
	if err != nil {
		return err
	}
	result, err := a.analyze(file, "build", graphDeps(g))
	if err != nil {
		return err
	}
	a.render(result)
	if err := exitFor(result); err != nil {
		return err
	}
	if result.NIR == nil {
		return diagnostic.New(diagnostic.Internal, "cli.no-nir", "前端没有返回 NIR")
	}
	data, err := nir.EncodeBytes(result.NIR)
	if err != nil {
		return diagnostic.New(diagnostic.Internal, "cli.encode-failed", "无法序列化降低结果").WithDetail(err.Error())
	}
	record, err := build.Run(build.Options{
		Root:      a.root,
		InputData: data,
		Output:    parsed.output,
		Source:    sourceOf(a.root, result),
		GraphHash: graphHash(g),
	}, a.runner)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "已生成 %s（%d 字节）\n", record.Output.Path, record.Output.Bytes)
	if record.Source != nil {
		fmt.Fprintf(a.stdout, "  源码     %s（%s）\n", record.Source.Path, shortSHA(record.Source.SHA256))
	}
	fmt.Fprintf(a.stdout, "  输入     %s\n", shortSHA(record.Input.SHA256))
	fmt.Fprintf(a.stdout, "  编译器   %s\n", shortSHA(record.Tools.CompilerSHA256))
	fmt.Fprintf(a.stdout, "  构建记录 %s\n", build.RecordPath(record.Output.Path))
	return nil
}

// run 编译并立即执行本次产物。它不执行任何先前存在的文件：构建失败就直接
// 返回，目标位置即使留着上一次的产物也不会被运行。
func (a *app) run(args []string) error {
	parsed, err := parseSourceArgs("run", args, false, false)
	if err != nil {
		return err
	}
	file, err := a.resolveSource(parsed)
	if err != nil {
		return err
	}
	g, err := a.loadGraph(parsed)
	if err != nil {
		return err
	}
	result, err := a.analyze(file, "build", graphDeps(g))
	if err != nil {
		return err
	}
	a.render(result)
	if err := exitFor(result); err != nil {
		return err
	}
	if result.NIR == nil {
		return diagnostic.New(diagnostic.Internal, "cli.no-nir", "前端没有返回 NIR")
	}
	data, err := nir.EncodeBytes(result.NIR)
	if err != nil {
		return diagnostic.New(diagnostic.Internal, "cli.encode-failed", "无法序列化降低结果").WithDetail(err.Error())
	}
	output := filepath.Join(a.root, ".work", "run", "app")
	record, err := build.Run(build.Options{
		Root:      a.root,
		InputData: data,
		Output:    output,
		Source:    sourceOf(a.root, result),
		GraphHash: graphHash(g),
	}, a.runner)
	if err != nil {
		return err
	}
	code, err := a.programRunner().Run(record.Output.Path, a.stdout, a.stderr)
	if err != nil {
		return diagnostic.New(diagnostic.Environment, "run.exec-failed", "无法执行 %s", record.Output.Path).WithDetail(err.Error())
	}
	if code != 0 {
		// 被运行程序的退出状态原样传出去，与 go run 一致；编译失败仍用
		// 1、3、4、5 这几类。
		return silentExit{code}
	}
	return nil
}

// dump 输出前端事实或 NIR，只写 JSON。
func (a *app) dump(args []string) error {
	if len(args) < 2 {
		return diagnostic.New(diagnostic.Usage, "cli.missing-argument", "dump 需要一份内容和一份源文件：dump facts <file.ts> 或 dump nir <file.ts>")
	}
	what := args[0]
	switch what {
	case "facts", "nir", "graph", "modules":
	default:
		return diagnostic.New(diagnostic.Usage, "cli.unknown-topic", "dump 只接受 facts、nir、graph 或 modules，收到 %q", what)
	}
	parsed, err := parseSourceArgs("dump", args[1:], false, false)
	if err != nil {
		return err
	}
	if what == "modules" {
		table, err := a.loadModules(parsed)
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(table.Report(), "", "  ")
		if err != nil {
			return diagnostic.New(diagnostic.Internal, "cli.encode-failed", "无法序列化模块符号表").WithDetail(err.Error())
		}
		if _, err := a.stdout.Write(append(data, '\n')); err != nil {
			return diagnostic.New(diagnostic.Internal, "cli.write-failed", "写出模块符号表失败").WithDetail(err.Error())
		}
		return nil
	}
	if what == "graph" {
		g, err := a.loadGraph(parsed)
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(g.Report(), "", "  ")
		if err != nil {
			return diagnostic.New(diagnostic.Internal, "cli.encode-failed", "无法序列化依赖图").WithDetail(err.Error())
		}
		if _, err := a.stdout.Write(append(data, '\n')); err != nil {
			return diagnostic.New(diagnostic.Internal, "cli.write-failed", "写出依赖图失败").WithDetail(err.Error())
		}
		return nil
	}
	file, err := a.resolveSource(parsed)
	if err != nil {
		return err
	}
	g, err := a.loadGraph(parsed)
	if err != nil {
		return err
	}
	result, err := a.analyze(file, what, graphDeps(g))
	if err != nil {
		return err
	}
	if err := exitFor(result); err != nil {
		a.render(result)
		return err
	}
	var value any
	if what == "facts" {
		value = result.Facts
	} else {
		value = result.NIR
	}
	if value == nil {
		return diagnostic.New(diagnostic.Internal, "cli.no-dump", "前端没有返回 %s", what)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return diagnostic.New(diagnostic.Internal, "cli.encode-failed", "无法序列化 %s", what).WithDetail(err.Error())
	}
	if _, err := a.stdout.Write(append(data, '\n')); err != nil {
		return diagnostic.New(diagnostic.Internal, "cli.write-failed", "写出 %s 失败", what).WithDetail(err.Error())
	}
	return nil
}

// graphDeps 返回除入口外的依赖绝对路径，供前端宿主一起载入。
func graphDeps(g *graph.Graph) []string {
	if g == nil {
		return nil
	}
	var out []string
	for _, file := range g.Files {
		if file.Abs != g.Entry.Abs {
			out = append(out, file.Abs)
		}
	}
	return out
}

func graphHash(g *graph.Graph) string {
	if g == nil {
		return ""
	}
	return g.Hash
}

// sourceOf 把结果里的源码身份转成构建记录的一项。路径尽量写成项目内相对
// 路径：绝对路径换个工作目录就失效了。
func sourceOf(root string, result *pipeline.Result) *build.Source {
	path := result.File
	if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		path = rel
	}
	return &build.Source{Path: path, SHA256: result.SourceSHA256, Bytes: result.SourceBytes}
}
