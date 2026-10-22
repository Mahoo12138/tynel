package main

import (
	"fmt"
	"io"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

const usageText = `tynel 是 TypeScript 到原生二进制的编译器。

用法：
  tynel <命令> [参数]

命令：
  version               显示版本、目标平台和锁定的工具链身份
  help [命令]           显示总体帮助或某个命令的说明
  toolchain             显示锁定工具链和各工具的实际路径（开发命令）
  toolchain which <名>  显示单个工具的路径（开发命令）

退出状态：
  0  成功
  1  源码错误
  2  用法错误
  3  能力限制
  4  环境缺失
  5  内部故障

build、check、run 尚未实现；当前版本只能显示身份并检查工具链。
`

var topicHelp = map[string]string{
	"version": `tynel version

显示程序版本、编译时记录的提交、目标平台，以及锁文件中的 Go 版本、
源码提交和许可证。锁文件或准备报告缺失时如实标注，不猜造身份。
`,
	"help": `tynel help [命令]

不带参数时输出总体用法。带命令名时输出该命令的说明；
未知命令名按用法错误退出。
`,
	"toolchain": `tynel toolchain
tynel toolchain which <名>

第一条命令读取 .work/toolchain/toolchain.json，列出 SDK 目录和
go、compile、link、asm 的实际路径，并确认每个工具都能解析。
第二条命令只解析一个工具，可用名称为 go、compile、link、asm。

两条命令都只做检查，不修改工具链；准备工具链请用 ./tools/toolchain/prep。
`,
}

func writeUsage(w io.Writer) {
	fmt.Fprint(w, usageText)
}

func (a *app) help(args []string) error {
	if len(args) == 0 {
		writeUsage(a.stdout)
		return nil
	}
	if len(args) > 1 {
		return diagnostic.New(diagnostic.Usage, "cli.unexpected-argument", "help 只接受一个命令名：%q", args[1])
	}
	text, ok := topicHelp[args[0]]
	if !ok {
		return diagnostic.New(diagnostic.Usage, "cli.unknown-topic", "没有 %q 的帮助；用 tynel help 查看命令列表", args[0])
	}
	fmt.Fprint(a.stdout, text)
	return nil
}
