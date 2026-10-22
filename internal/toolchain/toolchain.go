// Package toolchain 读取工具链锁文件与准备报告，并封装可替换的外部工具调用。
//
// 它只描述「锁定的是什么」和「怎么调用外部进程」，不决定编译器怎么用这些
// 工具。路径来自 01.01 的 .work/toolchain/toolchain.json，本包不重复推导
// 目录命名，避免与准备入口各自维护一套规则。
package toolchain

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// 仓库内的固定位置。
const (
	LockPath   = "tools/toolchain/go.lock.json"
	ReportPath = ".work/toolchain/toolchain.json"
)

// ToolNames 是当前锁定的外部工具名，顺序用于展示。
var ToolNames = []string{"go", "compile", "link", "asm"}

// Lock 是锁文件中与命令行相关的部分。其余字段由准备入口严格校验，
// 这里宽松读取，避免两处对同一份文件提出不同的格式要求。
type Lock struct {
	SchemaVersion int `json:"schemaVersion"`
	Go            struct {
		Version      string `json:"version"`
		SourceTag    string `json:"sourceTag"`
		SourceCommit string `json:"sourceCommit"`
		License      string `json:"license"`
	} `json:"go"`
	Targets struct {
		Host    string `json:"host"`
		Primary string `json:"primary"`
	} `json:"targets"`
}

// Install 是一次已完成的准备。
type Install struct {
	Kind  string            `json:"kind"`
	Name  string            `json:"name"`
	Dir   string            `json:"dir"`
	Tools map[string]string `json:"tools"`
}

// Report 是准备入口写出的报告。
type Report struct {
	SchemaVersion int       `json:"schemaVersion"`
	LockedVersion string    `json:"lockedVersion"`
	Target        string    `json:"target"`
	Installs      []Install `json:"installs"`
}

// LoadLock 读取锁文件。找不到或内容不可解析都按环境缺失处理。
func LoadLock(path string) (*Lock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, diagnostic.New(diagnostic.Environment, "lock.missing", "找不到锁文件 %s", path)
		}
		return nil, diagnostic.New(diagnostic.Environment, "lock.unreadable", "无法读取锁文件 %s", path).WithDetail(err.Error())
	}
	var lock Lock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "lock.invalid", "锁文件 %s 不是预期的 JSON 结构", path).WithDetail(err.Error())
	}
	if lock.Go.Version == "" || lock.Targets.Host == "" {
		return nil, diagnostic.New(diagnostic.Environment, "lock.incomplete", "锁文件 %s 缺少 go.version 或 targets.host", path)
	}
	return &lock, nil
}

// LoadReport 读取准备报告。报告不存在意味着还没运行准备入口。
func LoadReport(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, diagnostic.New(diagnostic.Environment, "toolchain.not-prepared", "工具链尚未准备：找不到 %s", path)
		}
		return nil, diagnostic.New(diagnostic.Environment, "toolchain.unreadable", "无法读取准备报告 %s", path).WithDetail(err.Error())
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "toolchain.invalid", "准备报告 %s 不是预期的 JSON 结构", path).WithDetail(err.Error())
	}
	if len(report.Installs) == 0 {
		return nil, diagnostic.New(diagnostic.Environment, "toolchain.incomplete", "准备报告 %s 没有安装记录", path)
	}
	return &report, nil
}

// SDK 返回 SDK 安装记录。
func (r *Report) SDK() (Install, bool) {
	for _, inst := range r.Installs {
		if inst.Kind == "sdk" {
			return inst, true
		}
	}
	return Install{}, false
}

// Source 返回源码安装记录。
func (r *Report) Source() (Install, bool) {
	for _, inst := range r.Installs {
		if inst.Kind == "src" {
			return inst, true
		}
	}
	return Install{}, false
}

// Tool 返回某个工具在报告中的路径。
func (r *Report) Tool(name string) (string, bool) {
	for _, inst := range r.Installs {
		if path, ok := inst.Tools[name]; ok {
			return path, true
		}
	}
	return "", false
}

// FindRoot 从 start 向上查找项目根：含锁文件的目录。
func FindRoot(start string) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(LockPath))); err == nil && !info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
