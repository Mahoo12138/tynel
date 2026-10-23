package main

import (
	"fmt"
	"strings"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/internal/build"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

// buildIR 实现开发用的 build-ir：把一份 NIR 编成原生程序。
func (a *app) buildIR(args []string) error {
	// 手写解析：flag 包遇到第一个位置参数就停止，而 build-ir 允许
	// `tynel build-ir input.nir.json -o out/app` 这种写法。
	var input, output string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-o":
			if i+1 >= len(args) {
				return diagnostic.New(diagnostic.Usage, "cli.missing-argument", "-o 需要输出路径")
			}
			output = args[i+1]
			i++
		case strings.HasPrefix(arg, "-o="):
			output = strings.TrimPrefix(arg, "-o=")
		case strings.HasPrefix(arg, "-"):
			return diagnostic.New(diagnostic.Usage, "cli.bad-flag", "build-ir 不认识选项 %q", arg)
		case input == "":
			input = arg
		default:
			return diagnostic.New(diagnostic.Usage, "cli.unexpected-argument", "build-ir 只接受一份 NIR 文件：%q", arg)
		}
	}
	if input == "" {
		return diagnostic.New(diagnostic.Usage, "cli.missing-argument", "build-ir 需要一份 NIR 文件：tynel build-ir input.nir.json -o out/app")
	}
	if output == "" {
		return diagnostic.New(diagnostic.Usage, "cli.missing-argument", "build-ir 需要 -o 指定输出路径")
	}
	if !a.hasRoot {
		return diagnostic.New(diagnostic.Environment, "project.not-found", "不在项目目录内：向上找不到 %s", toolchain.LockPath)
	}

	record, err := build.Run(build.Options{Root: a.root, Input: input, Output: output}, a.runner)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "已生成 %s（%d 字节）\n", record.Output.Path, record.Output.Bytes)
	fmt.Fprintf(a.stdout, "  输入     %s\n", shortSHA(record.Input.SHA256))
	fmt.Fprintf(a.stdout, "  编译器   %s\n", shortSHA(record.Tools.CompilerSHA256))
	fmt.Fprintf(a.stdout, "  构建记录 %s\n", build.RecordPath(record.Output.Path))
	return nil
}
