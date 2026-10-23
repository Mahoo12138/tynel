// Package tyneladapter 是 Tynel 对 TypeScript 前端的唯一入口。
//
// 它必须放在上游模块树内：只有模块树内的代码才能导入 internal/*，而模块外
// 的宿主只依赖本包导出的普通数据类型。包名和导出面因此都按「稳定接口」设计，
// 不跟随上游重构；上游升级时只改本文件，不改调用方。
//
// 本包由 tools/toolchain/prep 从 bridge/tsgo/_adapter 复制进已校验的源码树，
// 复制后的位置是 <tree>/tyneladapter。不要直接编辑 .work 下的副本。
//
// 目录名以下划线开头是有意的：Go 工具忽略这样的目录，因此仓库根模块的
// go vet ./... 不会尝试在这里解析上游 internal 包。这份源码只在被复制进
// 上游模块树之后才参与编译。
package tyneladapter

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// ErrSessionClosed 在会话关闭后继续查询时返回。
var ErrSessionClosed = errors.New("tyneladapter: 会话已关闭")

// Options 是一次检查的全部输入。
//
// Source 是调用方已经读好的字节快照：Open 不再访问磁盘，因此检查期间即使
// 原文件被改写，结果仍对应快照那一刻的内容。FileName 只作为身份和相对路径
// 的基准，必须是与 Source 对应的绝对路径。
type Options struct {
	FileName string
	Source   []byte
}

// Diagnostic 是一条源级诊断。字段全是普通数据类型，不含任何上游指针。
//
// Start 和 Length 是 UTF-8 字节偏移，不是字符数：上游按字节处理源码，中文
// 注释会让同一个字符位置出现不同的字节偏移。换算成行列由调用方按自己的源码
// 快照完成，本包不缓存行表。
type Diagnostic struct {
	File     string `json:"file"`
	Code     int32  `json:"code"`
	Category string `json:"category"`
	Message  string `json:"message"`
	Start    int    `json:"start"`
	Length   int    `json:"length"`
}

// Session 持有一份程序的上游句柄。句柄不出本包：调用方拿到的只有 Session
// 指针和普通数据，无法把 *compiler.Program 或 *ast.SourceFile 存到别处继续用。
//
// 一个会话对应一次检查。关闭后句柄被清空，后续查询返回 ErrSessionClosed，
// 这样「会话结束后还能读到旧程序」不会变成隐式行为。
type Session struct {
	mu      sync.Mutex
	closed  bool
	program *compiler.Program
	file    *ast.SourceFile
}

// Open 为一个单文件程序建立检查会话。
//
// 程序在内存里组装：源文件来自 Options.Source，声明库来自编译进二进制的
// 内置副本，因此不需要磁盘上的 lib 目录，也不会读到调用方没提供的文件。
func Open(opts Options) (*Session, error) {
	if opts.FileName == "" {
		return nil, errors.New("tyneladapter: FileName 不能为空")
	}
	if len(opts.Source) == 0 {
		return nil, errors.New("tyneladapter: Source 不能为空")
	}
	name := tspath.NormalizeSlashes(opts.FileName)
	if !tspath.IsRootedDiskPath(name) {
		return nil, fmt.Errorf("tyneladapter: FileName 必须是绝对路径：%q", opts.FileName)
	}

	fs := bundled.WrapFS(vfstest.FromMap(map[string]any{name: string(opts.Source)}, true))
	options := &core.CompilerOptions{
		Target: core.ScriptTargetESNext,
		Strict: core.TSTrue,
	}
	program := compiler.NewProgram(compiler.ProgramOptions{
		Config: &tsoptions.ParsedCommandLine{
			ParsedConfig: &core.ParsedOptions{
				FileNames:       []string{name},
				CompilerOptions: options,
			},
		},
		Host: compiler.NewCompilerHost(
			tspath.NormalizeSlashes(filepath.Dir(name)),
			fs,
			bundled.LibPath(),
			nil,
			nil,
		),
	})
	file := program.GetSourceFile(name)
	if file == nil {
		return nil, fmt.Errorf("tyneladapter: 程序没有载入 %s", name)
	}
	return &Session{program: program, file: file}, nil
}

// Diagnostics 返回程序级、语法和语义诊断，顺序是程序级在前、文件级在后。
//
// 调用方要区分「源码有问题」和「前端自己出错」：本方法只返回诊断，不返回
// 错误；返回 error 只有一种情况——会话已经关闭。
func (s *Session) Diagnostics() ([]Diagnostic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrSessionClosed
	}
	ctx := context.Background()
	raw := s.program.GetProgramDiagnostics()
	raw = append(raw, s.program.GetSyntacticDiagnostics(ctx, s.file)...)
	raw = append(raw, s.program.GetSemanticDiagnostics(ctx, s.file)...)

	out := make([]Diagnostic, 0, len(raw))
	for _, d := range raw {
		item := Diagnostic{
			Code:     d.Code(),
			Category: d.Category().Name(),
			Message:  d.String(),
			Start:    d.Pos(),
			Length:   d.Len(),
		}
		if f := d.File(); f != nil {
			item.File = f.FileName()
		}
		out = append(out, item)
	}
	return out, nil
}

// Close 释放会话并清空上游句柄。
//
// 重复关闭返回 nil：关闭是幂等操作，调用方不需要记住自己是否关过。关闭后
// 再查询返回 ErrSessionClosed，而不是空诊断——空诊断会被误读成「没有错误」。
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.program = nil
	s.file = nil
	return nil
}
