package toolchain

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// Output 是一次外部进程调用的结果。非零退出是数据，不是调用失败。
type Output struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Runner 是外部工具调用的可替换接口。测试用它注入假的工具表，
// 避免为了验证错误路径去真的安装或删除工具。
type Runner interface {
	// LookPath 解析工具路径。找不到时返回错误。
	LookPath(name string) (string, error)
	// Run 启动工具并等待结束。只有无法启动才返回 error；
	// 进程以非零状态退出时，状态记录在 Output.ExitCode。
	Run(name string, args ...string) (Output, error)
}

// Exec 是用真实操作系统进程实现的 Runner。
type Exec struct{}

// LookPath 调用 exec.LookPath。传入绝对路径时，它检查该文件是否可执行。
func (Exec) LookPath(name string) (string, error) { return exec.LookPath(name) }

// Run 启动外部进程并收集输出。非零退出不算 error。
func (Exec) Run(name string, args ...string) (Output, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := Output{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return out, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		out.ExitCode = exitErr.ExitCode()
		return out, nil
	}
	return out, err
}

// Failure 把非零退出转换为内部故障诊断，保留工具名、退出状态和 stderr。
// 它不返回源码错误：外部进程失败不是用户代码的问题。
func (o Output) Failure(name string, args []string) *diagnostic.Diagnostic {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	d := diagnostic.New(diagnostic.Internal, "tool.failed", "外部工具执行失败：%s（退出状态 %d）", command, o.ExitCode)
	if detail := strings.TrimSpace(o.Stderr); detail != "" {
		d = d.WithDetail(detail)
	} else if detail := strings.TrimSpace(o.Stdout); detail != "" {
		d = d.WithDetail(detail)
	}
	return d
}

// Missing 把找不到工具转换为环境缺失诊断。
func Missing(name string, cause error) *diagnostic.Diagnostic {
	d := diagnostic.New(diagnostic.Environment, "tool.missing", "找不到工具 %s", name)
	if cause != nil {
		d = d.WithDetail(fmt.Sprintf("%v", cause))
	}
	return d
}
