// Command consumer 是一个位于 Tynel 之外的原生宿主示例。
//
// 它做三件事：把源文件读成一次快照、通过适配包检查这份快照、把前端给的字节
// 偏移映射成行列。除了适配包，它只依赖 Tynel 自己的 compiler/source，不导入
// 上游任何 internal 包——这正是适配包存在的理由。
//
//	consumer <file.ts>          输出诊断 JSON；有 error 级诊断时退出 1
//	consumer -quiet <file.ts>   只输出摘要
//	consumer -facts <file.ts>   输出前端事实 JSON（开发接口）
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/source"
	"github.com/microsoft/typescript-go/tyneladapter"
)

// output 是宿主对外的诊断形状。字段比 compiler/source 的结构更平，方便脚本
// 直接取 line、column；行列从位置换算得来，字节区间单独保留。
type output struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"endLine"`
	EndColumn int    `json:"endColumn"`
	Start     int    `json:"start"`
	Length    int    `json:"length"`
	Severity  string `json:"severity"`
	Code      int32  `json:"code"`
	Message   string `json:"message"`
}

func main() {
	quiet := flag.Bool("quiet", false, "只输出摘要")
	facts := flag.Bool("facts", false, "输出前端事实而不是诊断")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法：consumer [-quiet] <file.ts>")
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(diagnostic.ExitUsage)
	}
	path, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法解析路径：%v\n", err)
		os.Exit(diagnostic.ExitUsage)
	}

	// 唯一一次磁盘读取。之后无论原文件怎么改，诊断都对应这一刻的内容。
	snapshot, err := source.Read(path, source.DefaultLimits())
	if err != nil {
		report(err)
	}

	if *facts {
		out, err := dumpFacts(snapshot)
		if err != nil {
			report(err)
		}
		if _, err := os.Stdout.Write(out); err != nil {
			fmt.Fprintf(os.Stderr, "写出事实失败：%v\n", err)
			os.Exit(diagnostic.ExitInternal)
		}
		return
	}

	diagnostics, err := check(snapshot)
	if err != nil {
		report(err)
	}

	errors := 0
	items := make([]output, 0, len(diagnostics))
	for _, d := range diagnostics {
		if d.Severity == source.SeverityError {
			errors++
		}
		items = append(items, flatten(d))
	}

	if *quiet {
		fmt.Printf("%d 条诊断，其中 %d 条错误\n", len(items), errors)
	} else {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(items); err != nil {
			fmt.Fprintf(os.Stderr, "写出诊断失败：%v\n", err)
			os.Exit(diagnostic.ExitInternal)
		}
	}
	if errors > 0 {
		os.Exit(diagnostic.ExitSource)
	}
}

// dumpFacts 取事实、解码并校验，然后按规范形式重新编码输出。
//
// 输出的是解码后的事实，不是适配层的原始字节：解码会拒绝未知字段，校验会
// 检查 ID、引用和调用是否自洽，能通过这两关的数据才值得给别人看。
func dumpFacts(snapshot *source.Snapshot) ([]byte, error) {
	session, err := tyneladapter.Open(tyneladapter.Options{
		FileName: snapshot.Path(),
		Source:   snapshot.Bytes(),
	})
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "frontend.session", "无法建立检查会话").WithDetail(err.Error())
	}
	raw, err := session.Facts()
	closeErr := session.Close()
	if err != nil {
		return nil, diagnostic.New(diagnostic.Internal, "frontend.facts", "读取前端事实失败").WithDetail(err.Error())
	}
	if closeErr != nil {
		return nil, diagnostic.New(diagnostic.Internal, "frontend.close", "关闭会话失败").WithDetail(closeErr.Error())
	}
	decoded, err := frontend.Decode(raw)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(decoded, "", "  ")
}

// check 建立会话、取诊断、关闭会话，再把结果映射到快照位置。
func check(snapshot *source.Snapshot) ([]source.Diagnostic, error) {
	session, err := tyneladapter.Open(tyneladapter.Options{
		FileName: snapshot.Path(),
		Source:   snapshot.Bytes(),
	})
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "frontend.session", "无法建立检查会话").WithDetail(err.Error())
	}
	raw, err := session.Diagnostics()
	closeErr := session.Close()
	if err != nil {
		return nil, diagnostic.New(diagnostic.Internal, "frontend.diagnostics", "读取前端诊断失败").WithDetail(err.Error())
	}
	if closeErr != nil {
		return nil, diagnostic.New(diagnostic.Internal, "frontend.close", "关闭会话失败").WithDetail(closeErr.Error())
	}
	return snapshot.MapDiagnostics(convert(raw))
}

// convert 把适配层的诊断摊平成 source 认识的形状。这是宿主承担的转换，
// 适配层不需要知道 Tynel 的诊断结构。
func convert(in []tyneladapter.Diagnostic) []source.FrontendDiagnostic {
	out := make([]source.FrontendDiagnostic, 0, len(in))
	for _, d := range in {
		out = append(out, source.FrontendDiagnostic{
			File:     d.File,
			Code:     d.Code,
			Category: d.Category,
			Message:  d.Message,
			Start:    d.Start,
			Length:   d.Length,
		})
	}
	return out
}

func flatten(d source.Diagnostic) output {
	return output{
		File:      d.Location.Path,
		Line:      d.Location.Start.Line,
		Column:    d.Location.Start.Column,
		EndLine:   d.Location.End.Line,
		EndColumn: d.Location.End.Column,
		Start:     d.Location.Bytes.Start,
		Length:    d.Location.Bytes.End - d.Location.Bytes.Start,
		Severity:  string(d.Severity),
		Code:      d.Code,
		Message:   d.Message,
	}
}

// report 输出一条失败并按其类别退出。源码错误与环境缺失在这里分开。
func report(err error) {
	_ = diagnostic.Write(os.Stderr, err)
	os.Exit(diagnostic.ExitCode(err))
}
