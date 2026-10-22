package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

// toolchainCommand 实现开发用的工具链检查。它不属于用户命令，
// 用户命令集合在 06.01 固定。
func (a *app) toolchainCommand(args []string) error {
	if len(args) == 0 {
		return a.showToolchain()
	}
	switch args[0] {
	case "which":
		if len(args) < 2 {
			return diagnostic.New(diagnostic.Usage, "cli.missing-argument", "toolchain which 需要工具名；可用：%s", strings.Join(toolchain.ToolNames, "、"))
		}
		if len(args) > 2 {
			return diagnostic.New(diagnostic.Usage, "cli.unexpected-argument", "toolchain which 只接受一个工具名：%q", args[2])
		}
		return a.whichTool(args[1])
	default:
		return diagnostic.New(diagnostic.Usage, "cli.unknown-subcommand", "toolchain 没有子命令 %q；可用：which", args[0])
	}
}

func (a *app) loadReport() (*toolchain.Report, error) {
	if !a.hasRoot {
		return nil, diagnostic.New(diagnostic.Environment, "project.not-found", "不在项目目录内：向上找不到 %s", toolchain.LockPath)
	}
	return toolchain.LoadReport(filepath.Join(a.root, filepath.FromSlash(toolchain.ReportPath)))
}

// showToolchain 列出锁定的身份和每个工具的实际路径，并确认工具可用。
func (a *app) showToolchain() error {
	report, err := a.loadReport()
	if err != nil {
		return err
	}
	lock, err := toolchain.LoadLock(filepath.Join(a.root, filepath.FromSlash(toolchain.LockPath)))
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "锁定工具链 %s（提交 %s，%s）\n", lock.Go.Version, shortSHA(lock.Go.SourceCommit), lock.Go.License)
	fmt.Fprintf(a.stdout, "目标平台     %s\n", lock.Targets.Host)
	if sdk, ok := report.SDK(); ok {
		fmt.Fprintf(a.stdout, "SDK 目录     %s\n", sdk.Dir)
	}
	var missing []string
	for _, name := range toolchain.ToolNames {
		path, ok := report.Tool(name)
		if !ok {
			missing = append(missing, name)
			continue
		}
		resolved, err := a.runner.LookPath(path)
		if err != nil {
			missing = append(missing, name)
			continue
		}
		fmt.Fprintf(a.stdout, "  %-8s %s\n", name, resolved)
	}
	if src, ok := report.Source(); ok {
		fmt.Fprintf(a.stdout, "源码目录     %s\n", src.Dir)
	}
	if len(missing) > 0 {
		return diagnostic.New(diagnostic.Environment, "tool.missing", "准备报告里的工具不可用：%s", strings.Join(missing, "、"))
	}
	return nil
}

// whichTool 只解析一个工具，成功时只打印路径，便于脚本使用。
func (a *app) whichTool(name string) error {
	if !isToolName(name) {
		return diagnostic.New(diagnostic.Usage, "cli.unknown-tool", "未知工具 %q；可用：%s", name, strings.Join(toolchain.ToolNames, "、"))
	}
	report, err := a.loadReport()
	if err != nil {
		return err
	}
	path, ok := report.Tool(name)
	if !ok {
		return diagnostic.New(diagnostic.Environment, "tool.not-recorded", "准备报告里没有工具 %s；重新运行 ./tools/toolchain/prep", name)
	}
	resolved, err := a.runner.LookPath(path)
	if err != nil {
		return toolchain.Missing(name, err)
	}
	fmt.Fprintln(a.stdout, resolved)
	return nil
}

func isToolName(name string) bool {
	for _, candidate := range toolchain.ToolNames {
		if candidate == name {
			return true
		}
	}
	return false
}
