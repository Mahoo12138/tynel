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
  check <file.ts> [--root <目录>] [--json]  检查源码与原生子集，验证 NIR，不生成机器码
  build <file.ts> -o <路径> [--root <目录>] 编译成原生程序，不运行
  run <file.ts> [--root <目录>]             编译并运行本次产物
  dump facts|nir <file.ts> [--root <目录>]  输出前端事实或 NIR（JSON）
  version                       显示版本、目标平台和锁定的工具链身份
  help [命令]                   显示总体帮助或某个命令的说明
  toolchain                     显示锁定工具链和各工具的实际路径（开发命令）
  toolchain which <名>          显示单个工具的路径（开发命令）
  build-ir <NIR> -o <路径>      把 NIR 编成原生程序（开发命令）

退出状态：
  0  成功
  1  源码错误
  2  用法错误
  3  能力限制
  4  环境缺失
  5  内部故障

run 的退出状态是被运行程序的状态；编译失败时仍用 1–5。
`

var topicHelp = map[string]string{
	"check": `tynel check <file.ts> [--json]

读取一份 TypeScript 源文件，先做类型检查，再判断它是否落在原生子集内，
最后降低并验证 NIR。它不生成机器码，也不检查后端工具是否可用：check 通过
只说明这份程序可以进入后端，不保证 SDK 或修改版编译器已就绪。

默认把诊断写到标准错误，成功时在标准输出打一行摘要。加 --json 时把完整
结果写到标准输出：诊断、能力结论、函数数量与 NIR 是否通过验证。

--root 指定项目根；入口按项目根解释，而不是按当前工作目录解释。缺省时用
仓库根。

退出状态：源码错误 1，能力限制 3，通过 0。
`,
	"build": `tynel build <file.ts> -o <路径>

从 TypeScript 源文件生成原生程序。流程是检查、能力判断、降低、验证、调用
修改版 Go 编译器，最后原子发布产物；构建成功也不运行程序。

产物先写到目标目录的临时文件，成功后才改名；构建失败不会覆盖已有的产物。
成功后在产物旁边写 <产物>.build.json，记录源码、NIR、工具和产物的哈希。
构建记录写不进去时产物会被删除：没有记录的产物无法复核来源。

首次运行会按需构建前端宿主，进度写在标准错误，不混入标准输出。

--root 指定项目根；入口和显式相对导入都按这个根解释。
`,
	"run": `tynel run <file.ts>

编译并立即运行本次产物，产物放在 .work/run/app。它不执行任何先前存在的
文件：构建失败就直接返回，即使目标位置留着上一次的产物也不会被运行。

程序的标准输出与标准错误直接连到当前终端。程序退出状态原样作为本命令的
退出状态；编译失败时仍用 1–5 这几类。

--root 指定项目根；入口按这个根解释。
`,
	"dump": `tynel dump facts <file.ts>
tynel dump nir <file.ts>

输出前端事实或降低后的 NIR，只有 JSON，没有进度日志。源码有错时退出 1，
超出原生子集时退出 3，两种情况都不输出 JSON。

--root 指定项目根；入口按这个根解释。
`,
	"version": `tynel version

显示程序版本、编译时记录的提交、目标平台，以及锁文件中的 Go 版本、
源码提交和许可证。锁文件或准备报告缺失时如实标注，不猜造身份。
`,
	"help": `tynel help [命令]

不带参数时输出总体用法。带命令名时输出该命令的说明；
未知命令名按用法错误退出。
`,
	"build-ir": `tynel build-ir <NIR> -o <路径>

读取一份 NIR 文件，用修改版 Go 编译器编成原生程序。构建前先解码并验证
NIR；产物先写到目标目录的临时文件，成功后才改名发布，失败不会覆盖已有
的产物。构建本身不运行程序。

成功后在产物旁边写一份 <产物>.build.json，记录输入、工具和输出的哈希。
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
