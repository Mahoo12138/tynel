// Package diagnostic 定义 Tynel 的失败类别、稳定代码和退出状态。
//
// 类别是编译器与命令行之间唯一的退出码来源：调用方按类别取退出状态，
// 不再各自约定数字。四类失败必须分开，否则「编译器没准备好」会被误读成
// 「用户代码有错」，而「这个写法暂不支持」又会被误读成语法错误。
package diagnostic

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// Category 是失败的类别。类别的顺序即优先级的语义顺序，不要随意调整。
type Category int

const (
	// Source 表示源码本身不合法：语法错误、类型错误、未定义名称。
	Source Category = iota + 1
	// Usage 表示命令用法不对：未知命令、缺少参数、多余的参数。
	Usage
	// Capability 表示写法合法，但当前原生后端尚未实现该能力。
	Capability
	// Environment 表示编译器自身可用，但依赖不满足：缺工具、版本不符、目录不可用。
	Environment
	// Internal 表示编译器或它调用的进程出现不该出现的故障。
	Internal
)

// 退出状态与类别一一对应。0 表示成功，其余值由类别决定。
const (
	ExitOK          = 0
	ExitSource      = 1
	ExitUsage       = 2
	ExitCapability  = 3
	ExitEnvironment = 4
	ExitInternal    = 5
)

// String 返回用于诊断文本的中文类别名。
func (c Category) String() string {
	switch c {
	case Source:
		return "源码错误"
	case Usage:
		return "用法错误"
	case Capability:
		return "能力限制"
	case Environment:
		return "环境缺失"
	case Internal:
		return "内部故障"
	default:
		return "未知类别"
	}
}

// ExitCode 返回该类别的进程退出状态。
func (c Category) ExitCode() int {
	switch c {
	case Source:
		return ExitSource
	case Usage:
		return ExitUsage
	case Capability:
		return ExitCapability
	case Environment:
		return ExitEnvironment
	default:
		return ExitInternal
	}
}

// Diagnostic 是一条可展示的失败。Code 是稳定标识，供脚本匹配；Message 是
// 面向使用者的中文说明；Detail 保存外部进程输出等原始原因，不做改写。
type Diagnostic struct {
	Category Category
	Code     string
	Message  string
	File     string
	Line     int
	Column   int
	Detail   string
}

// New 构造一条诊断。format 与 args 的用法同 fmt.Sprintf。
func New(category Category, code, format string, args ...any) *Diagnostic {
	return &Diagnostic{Category: category, Code: code, Message: fmt.Sprintf(format, args...)}
}

// At 补充源码位置。
func (d *Diagnostic) At(file string, line, column int) *Diagnostic {
	d.File = file
	d.Line = line
	d.Column = column
	return d
}

// WithDetail 附加原始原因，例如外部进程的 stderr。
func (d *Diagnostic) WithDetail(detail string) *Diagnostic {
	d.Detail = strings.TrimRight(detail, "\n")
	return d
}

// Error 让诊断满足 error 接口。它只用于传递，展示请用 WriteTo。
func (d *Diagnostic) Error() string {
	if d.Code == "" {
		return fmt.Sprintf("%s: %s", d.Category, d.Message)
	}
	return fmt.Sprintf("%s (%s): %s", d.Category, d.Code, d.Message)
}

// ExitCode 返回这条诊断对应的进程退出状态。
func (d *Diagnostic) ExitCode() int { return d.Category.ExitCode() }

// WriteTo 按统一格式输出诊断：首行给类别与代码，位置和原因各占一行。
func (d *Diagnostic) WriteTo(w io.Writer) (int64, error) {
	var b strings.Builder
	b.WriteString("tynel: ")
	b.WriteString(d.Category.String())
	if d.Code != "" {
		b.WriteString(" (")
		b.WriteString(d.Code)
		b.WriteString(")")
	}
	b.WriteString(": ")
	b.WriteString(d.Message)
	if d.File != "" {
		b.WriteString("\n  位置: ")
		b.WriteString(d.File)
		if d.Line > 0 {
			fmt.Fprintf(&b, ":%d", d.Line)
			if d.Column > 0 {
				fmt.Fprintf(&b, ":%d", d.Column)
			}
		}
	}
	if d.Detail != "" {
		b.WriteString("\n  原因: ")
		b.WriteString(strings.ReplaceAll(d.Detail, "\n", "\n    "))
	}
	b.WriteString("\n")
	n, err := io.WriteString(w, b.String())
	return int64(n), err
}

// ExitCode 把任意错误映射为退出状态：空错误为 0，诊断按类别，其余按内部故障。
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var d *Diagnostic
	if errors.As(err, &d) {
		return d.ExitCode()
	}
	return ExitInternal
}

// Write 输出错误。非诊断错误会被包成内部故障，避免静默吞掉原因。
func Write(w io.Writer, err error) error {
	if err == nil {
		return nil
	}
	var d *Diagnostic
	if !errors.As(err, &d) {
		d = New(Internal, "internal.unclassified", "%v", err)
	}
	_, writeErr := d.WriteTo(w)
	return writeErr
}
