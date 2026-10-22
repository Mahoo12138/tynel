// Command tynel 是 Tynel 编译器的命令行入口。
//
// 当前只提供身份查询与工具链检查。build、check、run 尚未实现，
// 遇到未知命令一律按用法错误退出，不提供看似可用的空壳。
package main

import (
	"io"
	"os"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// app 保存一次调用的输出目标和可替换依赖。测试通过替换 runner 和 root
// 来构造环境缺失、工具不存在等路径，不需要真的改动磁盘上的工具链。
type app struct {
	stdout  io.Writer
	stderr  io.Writer
	runner  toolchain.Runner
	root    string
	hasRoot bool
}

func newApp(stdout, stderr io.Writer) *app {
	root, ok := toolchain.FindRoot(".")
	return &app{stdout: stdout, stderr: stderr, runner: toolchain.Exec{}, root: root, hasRoot: ok}
}

// run 执行一次命令行调用并返回进程退出状态。
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeUsage(stderr)
		return diagnostic.ExitUsage
	}
	a := newApp(stdout, stderr)
	if err := a.dispatch(args[0], args[1:]); err != nil {
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
	default:
		return diagnostic.New(diagnostic.Usage, "cli.unknown-command", "未知命令 %q；用 tynel help 查看可用命令", command)
	}
}
