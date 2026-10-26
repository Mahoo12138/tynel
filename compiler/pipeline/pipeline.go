// Package pipeline 定义前端宿主与主命令行之间的一次性结果协议。
//
// 主命令行不能导入 tsgo：前端在另一个模块里，仓库根模块不依赖它，否则没准备
// 前端源码的人连 go test ./... 都跑不起来。所以前端以子进程方式使用，子进程
// 用一份 JSON 把诊断、能力结论、事实和 NIR 一起交回来。
//
// 这份结构因此是两边唯一的契约。字段变更必须同时改两边：解码拒绝未知字段，
// 少改一边会立刻失败，而不是悄悄丢字段。
package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/nir"
)

// SchemaVersion 是协议版本。字段含义变化时递增，不做向后兼容的猜测。
const SchemaVersion = 1

// Result 是一次前端检查的完整结果。
//
// Diagnostics 是 TypeScript 的诊断，Capability 是「写法合法但后端不支持」的
// 结论，两者分开是因为退出状态不同（1 与 3）。Facts 与 NIR 按需附带：check
// 只要结论，build 要 NIR，dump 要其中一份。
type Result struct {
	SchemaVersion int             `json:"schemaVersion"`
	File          string          `json:"file"`
	SourceSHA256  string          `json:"sourceSha256"`
	SourceBytes   int             `json:"sourceBytes"`
	Diagnostics   []Diagnostic    `json:"diagnostics"`
	Capability    []Finding       `json:"capability"`
	Summary       Summary         `json:"summary"`
	Facts         *frontend.Facts `json:"facts,omitempty"`
	NIR           *nir.Module     `json:"nir,omitempty"`
}

// Summary 记录降低结果。Verified 为真表示 NIR 通过了验证器，而不只是「生成过」。
type Summary struct {
	Functions int  `json:"functions"`
	Verified  bool `json:"verified"`
}

// Diagnostic 是一条源级诊断。位置同时给出行列与 UTF-8 字节区间：行列给人看，
// 字节区间给需要精确切片的调用方。
type Diagnostic struct {
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

// Finding 是一条能力限制。Code 是字符串（cap.*），与 TypeScript 的数字错误码
// 不同，所以不与 Diagnostic 合并。
type Finding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Category string `json:"category"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// HasSourceErrors 报告是否存在错误级诊断。警告不算：它们不影响能否生成代码。
func (r *Result) HasSourceErrors() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}

// ExitCode 把结果映射成进程退出状态：源码错误 1，能力限制 3，通过 0。
func (r *Result) ExitCode() int {
	switch {
	case r.HasSourceErrors():
		return 1
	case len(r.Capability) > 0:
		return 3
	}
	return 0
}

// Encode 按缩进 JSON 写出结果。
func Encode(w io.Writer, r *Result) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(r)
}

// Decode 解析一份结果，并拒绝未知字段、尾随内容和版本不符。
func Decode(data []byte) (*Result, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result Result
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("前端结果无法解析：%w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("前端结果在 JSON 之后还有内容")
	}
	if result.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("前端结果的 schemaVersion 为 %d，本版本只接受 %d", result.SchemaVersion, SchemaVersion)
	}
	if result.Diagnostics == nil {
		result.Diagnostics = []Diagnostic{}
	}
	if result.Capability == nil {
		result.Capability = []Finding{}
	}
	return &result, nil
}
