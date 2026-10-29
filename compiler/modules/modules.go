// Package modules 把每个文件的导入、导出事实合成模块符号表。
//
// 每个文件的事实仍只描述自己；跨模块身份由「模块绝对路径 + 模块内声明 ID」
// 组成，不用字符串拼接猜目标。符号表负责三件事：确认导出存在、把导入别名
// 指到真实声明、拒绝伪造的模块引用。
package modules

import (
	"fmt"
	"sort"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/frontend"
)

// Ref 是一个跨模块声明引用。
type Ref struct {
	Module string
	ID     int
	Name   string
}

// Table 是一组模块的符号表。Entry 是入口模块路径。
type Table struct {
	Entry   string
	Modules map[string]*frontend.Facts
	Exports map[string]map[string]frontend.Export
	Imports map[string]map[string]Ref
}

// Build 用入口和依赖事实建立符号表。模块路径必须唯一；导出重名、缺导出、
// 导入指向不存在的模块都直接失败。
func Build(entry *frontend.Facts, deps []*frontend.Facts) (*Table, error) {
	if entry == nil {
		return nil, diagnostic.New(diagnostic.Internal, "modules.no-entry", "缺少入口模块事实")
	}
	t := &Table{
		Entry:   entry.ModulePath,
		Modules: map[string]*frontend.Facts{},
		Exports: map[string]map[string]frontend.Export{},
		Imports: map[string]map[string]Ref{},
	}
	all := append([]*frontend.Facts{entry}, deps...)
	for _, facts := range all {
		if facts == nil {
			return nil, diagnostic.New(diagnostic.Internal, "modules.nil", "模块事实为空")
		}
		if facts.ModulePath == "" {
			return nil, diagnostic.New(diagnostic.Internal, "modules.no-path", "模块事实缺少 ModulePath")
		}
		if _, exists := t.Modules[facts.ModulePath]; exists {
			return nil, diagnostic.New(diagnostic.Source, "module.duplicate-module", "模块 %s 出现了两次", facts.ModulePath)
		}
		t.Modules[facts.ModulePath] = facts
	}

	for path, facts := range t.Modules {
		exports := map[string]frontend.Export{}
		for _, exp := range facts.Exports {
			if _, exists := exports[exp.Name]; exists {
				return nil, diagnostic.New(diagnostic.Source, "module.duplicate-export", "模块 %s 重复导出 %q", path, exp.Name)
			}
			exports[exp.Name] = exp
		}
		t.Exports[path] = exports
	}

	for path, facts := range t.Modules {
		bindings := map[string]Ref{}
		for _, imp := range facts.Imports {
			if imp.TypeOnly {
				continue
			}
			target, ok := t.Modules[imp.Module]
			if !ok {
				return nil, diagnostic.New(diagnostic.Source, "module.missing-module", "模块 %s 导入了不存在的模块 %s", path, imp.Module)
			}
			export, ok := t.Exports[imp.Module][imp.Imported]
			if !ok {
				return nil, diagnostic.New(diagnostic.Source, "module.missing-export", "模块 %s 没有导出 %q（被 %s 导入）", imp.Module, imp.Imported, path)
			}
			if export.TypeOnly {
				return nil, diagnostic.New(diagnostic.Source, "module.type-import", "模块 %s 的值导入 %q 指向类型导出", path, imp.Imported)
			}
			bindings[imp.Local] = Ref{Module: target.ModulePath, ID: export.ID, Name: export.Name}
		}
		t.Imports[path] = bindings
	}

	if err := t.rejectCapturedModuleValues(); err != nil {
		return nil, err
	}
	return t, nil
}

// Resolve 返回入口模块里某个本地名字绑定的跨模块目标。
func (t *Table) Resolve(local string) (Ref, bool) {
	bindings, ok := t.Imports[t.Entry]
	if !ok {
		return Ref{}, false
	}
	ref, ok := bindings[local]
	return ref, ok
}

// ResolveCall 把一次调用的 CalleeName 解析到入口模块的导入绑定。
func (t *Table) ResolveCall(call frontend.Call) (Ref, bool) {
	if call.CalleeModule == "" || call.CalleeModule == t.Entry {
		return Ref{}, false
	}
	ref, ok := t.Resolve(call.CalleeName)
	if !ok || ref.Module != call.CalleeModule {
		return Ref{}, false
	}
	return ref, true
}

// rejectCapturedModuleValues 拒绝函数读取模块级运行时变量。模块级 const 在
// 初始化阶段才写入；函数捕获它需要闭包或隐式环境，本轮不实现。
func (t *Table) rejectCapturedModuleValues() error {
	for path, facts := range t.Modules {
		decls := map[int]frontend.Declaration{}
		for _, decl := range facts.Declarations {
			decls[decl.ID] = decl
		}
		for _, decl := range facts.Declarations {
			if decl.Kind != frontend.DeclFunction || decl.Body == nil {
				continue
			}
			start, end := decl.Body.Start, decl.Body.Start+decl.Body.Length
			for _, ref := range facts.References {
				if ref.TargetModule != path || ref.Start < start || ref.Start >= end {
					continue
				}
				if target, ok := decls[ref.Target]; ok && target.Kind == frontend.DeclConst && target.Module == path {
					return diagnostic.New(diagnostic.Source, "module.captured-variable",
						"函数 %q 读取模块级变量 %q；本阶段不实现闭包或隐式模块环境", decl.Name, target.Name)
				}
			}
		}
	}
	return nil
}

// ModuleReport 是一个模块的稳定报告。
type ModuleReport struct {
	Path    string            `json:"path"`
	Exports []string          `json:"exports"`
	Imports map[string]string `json:"imports"`
}

// Report 返回入口和全部模块的符号表，不含源码字节。
func (t *Table) Report() Report {
	report := Report{Entry: t.Entry}
	paths := make([]string, 0, len(t.Modules))
	for path := range t.Modules {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		item := ModuleReport{Path: path, Imports: map[string]string{}}
		for name := range t.Exports[path] {
			item.Exports = append(item.Exports, name)
		}
		sort.Strings(item.Exports)
		for local, ref := range t.Imports[path] {
			item.Imports[local] = ref.Module + "#" + ref.Name
		}
		report.Modules = append(report.Modules, item)
	}
	return report
}

// Report 是模块符号表的 JSON 形状。
type Report struct {
	Entry   string         `json:"entry"`
	Modules []ModuleReport `json:"modules"`
}

// Summary 返回稳定的模块列表，便于报告和测试。// Summary 返回稳定的模块列表，便于报告和测试。
func (t *Table) Summary() []string {
	paths := make([]string, 0, len(t.Modules))
	for path := range t.Modules {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		out = append(out, fmt.Sprintf("%s: %d exports, %d imports", path, len(t.Exports[path]), len(t.Imports[path])))
	}
	return out
}
