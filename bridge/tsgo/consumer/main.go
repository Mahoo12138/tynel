// Command consumer 是一个位于 Tynel 之外的原生宿主示例。
//
// 它只依赖 github.com/microsoft/typescript-go/tyneladapter 这一个包，不导入
// 任何 internal 包；这也正是适配包存在的理由——上游把所有能力都放在 internal
// 下，模块外的程序无法直接使用。
//
//	consumer <file.ts>          输出诊断 JSON；有 error 级诊断时退出 1
//	consumer -quiet <file.ts>   只输出摘要
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/microsoft/typescript-go/tyneladapter"
)

func main() {
	quiet := flag.Bool("quiet", false, "只输出摘要")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法：consumer [-quiet] <file.ts>")
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	path, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法解析路径：%v\n", err)
		os.Exit(2)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取 %s 失败：%v\n", path, err)
		os.Exit(2)
	}

	session, err := tyneladapter.Open(tyneladapter.Options{FileName: path, Source: source})
	if err != nil {
		fmt.Fprintf(os.Stderr, "建立检查会话失败：%v\n", err)
		os.Exit(3)
	}
	diagnostics, err := session.Diagnostics()
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取诊断失败：%v\n", err)
		os.Exit(3)
	}
	if err := session.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "关闭会话失败：%v\n", err)
		os.Exit(3)
	}

	errors := 0
	for _, d := range diagnostics {
		if d.Category == "error" {
			errors++
		}
	}
	if *quiet {
		fmt.Printf("%d 条诊断，其中 %d 条错误\n", len(diagnostics), errors)
	} else {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(diagnostics); err != nil {
			fmt.Fprintf(os.Stderr, "写出诊断失败：%v\n", err)
			os.Exit(3)
		}
	}
	if errors > 0 {
		os.Exit(1)
	}
}
