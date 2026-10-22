package main

import (
	"fmt"
	"path/filepath"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

// version 与 commit 由 tools/build 用 -ldflags 注入；直接 go build 时保留默认值。
var (
	version = "dev"
	commit  = "none"
)

// version 显示程序版本、目标平台和锁定的工具链身份。锁定的版本与提交
// 来自仓库内的锁文件，不另存一份常量，避免与准备入口各自记录。
func (a *app) version(args []string) error {
	if len(args) > 0 {
		return diagnostic.New(diagnostic.Usage, "cli.unexpected-argument", "version 不接受参数：%q", args[0])
	}
	fmt.Fprintf(a.stdout, "tynel %s (commit %s)\n", version, commit)
	if !a.hasRoot {
		fmt.Fprintln(a.stdout, "  目标平台    未知（不在项目目录内，找不到 tools/toolchain/go.lock.json）")
		return nil
	}
	lockPath := filepath.Join(a.root, filepath.FromSlash(toolchain.LockPath))
	lock, err := toolchain.LoadLock(lockPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "  目标平台    %s\n", lock.Targets.Host)
	fmt.Fprintf(a.stdout, "  工具链      %s（提交 %s，%s）\n", lock.Go.Version, shortSHA(lock.Go.SourceCommit), lock.Go.License)
	fmt.Fprintf(a.stdout, "  锁文件      %s\n", toolchain.LockPath)
	if report, err := toolchain.LoadReport(filepath.Join(a.root, filepath.FromSlash(toolchain.ReportPath))); err == nil {
		if sdk, ok := report.SDK(); ok {
			fmt.Fprintf(a.stdout, "  准备状态    已准备（%s）\n", sdk.Name)
			return nil
		}
	}
	fmt.Fprintf(a.stdout, "  准备状态    未准备（运行 ./tools/toolchain/prep）\n")
	return nil
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "未知"
	}
	return sha
}
