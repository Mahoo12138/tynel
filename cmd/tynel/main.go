// Command tynel 是 Tynel 编译器的命令行入口。
//
// 它负责编排：找项目根、准备前端宿主、按需降低、调用后端、发布产物。它自己
// 不解析 TypeScript，也不生成机器码——那两件事分别在宿主进程和修改版 Go
// 编译器里。
package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/pipeline"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// app 保存一次调用的输出目标和可替换依赖。测试通过替换 runner 和 root
// 来构造环境缺失、工具不存在等路径，不需要真的改动磁盘上的工具链。
type app struct {
	stdout   io.Writer
	stderr   io.Writer
	runner   toolchain.Runner
	program  programRunner
	frontend func(file, want string) (*pipeline.Result, error)
	root     string
	hasRoot  bool
}

func newApp(stdout, stderr io.Writer) *app {
	root, ok := findRepoRoot()
	return &app{stdout: stdout, stderr: stderr, runner: toolchain.Exec{}, root: root, hasRoot: ok}
}

// findRepoRoot 先按当前工作目录向上找锁文件；找不到时再从可执行文件所在
// 目录向上找。后一条让 bin/tynel 可以从任意工作目录启动，但项目根仍由
// --root 或入口路径决定，不会把工作目录误当成项目根。
func findRepoRoot() (string, bool) {
	if root, ok := toolchain.FindRoot("."); ok {
		return root, true
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	return toolchain.FindRoot(filepath.Dir(exe))
}

// run 执行一次命令行调用并返回进程退出状态。
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeUsage(stderr)
		return diagnostic.ExitUsage
	}
	a := newApp(stdout, stderr)
	if err := a.dispatch(args[0], args[1:]); err != nil {
		var silent silentExit
		if errors.As(err, &silent) {
			return silent.code
		}
		_ = diagnostic.Write(stderr, err)
		return diagnostic.ExitCode(err)
	}
	return diagnostic.ExitOK
}

func (a *app) dispatch(command string, args []string) error {
	switch command {
	case "version":
		return a.version(args)
	case "help", "-h", "--help":
		return a.help(args)
	case "toolchain":
		return a.toolchainCommand(args)
	case "check":
		return a.check(args)
	case "build":
		return a.build(args)
	case "run":
		return a.run(args)
	case "dump":
		return a.dump(args)
	case "build-ir":
		return a.buildIR(args)
	default:
		return diagnostic.New(diagnostic.Usage, "cli.unknown-command", "未知命令 %q；用 tynel help 查看可用命令", command)
	}
}
