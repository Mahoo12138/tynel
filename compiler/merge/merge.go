// Package merge 把多个模块的事实合成一个降低单元。
//
// 每个模块的声明 ID 只在模块内唯一。合并时重新分配全局 ID，并把函数名加上
// 模块前缀，避免两个模块都叫 helper 时在 NIR 里互相覆盖。调用和引用同时
// 重映射，所以降低阶段看到的仍是一份自洽的事实，而不是靠字符串猜目标。
package merge

import (
	"fmt"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/source"
)

// Unit 是一个模块的源码快照和事实。
type Unit struct {
	Snapshot *source.Snapshot
	Facts    *frontend.Facts
}

// Merged 是合并后的事实和模块体。
type Merged struct {
	Facts        *frontend.Facts
	EntryModule  string
	Order        []string
	Bodies       map[string]*frontend.Node
	Snapshots    map[string]*source.Snapshot
	FunctionName map[int]string // 全局声明 ID → NIR 函数名
}

// Build 合并 units。order 决定模块初始化顺序；为空时按 units 顺序。
func Build(units []Unit, order []string) (*Merged, error) {
	if len(units) == 0 {
		return nil, diagnostic.New(diagnostic.Internal, "merge.no-units", "合并缺少模块")
	}
	if len(order) == 0 {
		for _, unit := range units {
			order = append(order, unit.Facts.ModulePath)
		}
	}

	m := &Merged{
		EntryModule:  units[0].Facts.ModulePath,
		Order:        append([]string{}, order...),
		Bodies:       map[string]*frontend.Node{},
		Snapshots:    map[string]*source.Snapshot{},
		FunctionName: map[int]string{},
	}
	moduleIndex := map[string]int{}
	for i, unit := range units {
		if unit.Snapshot == nil || unit.Facts == nil {
			return nil, diagnostic.New(diagnostic.Internal, "merge.nil-unit", "第 %d 个模块缺少快照或事实", i)
		}
		if err := unit.Facts.Validate(); err != nil {
			return nil, err
		}
		path := unit.Facts.ModulePath
		if path == "" {
			return nil, diagnostic.New(diagnostic.Internal, "merge.no-path", "第 %d 个模块缺少 ModulePath", i)
		}
		if _, dup := moduleIndex[path]; dup {
			return nil, diagnostic.New(diagnostic.Source, "merge.duplicate-module", "模块 %s 出现了两次", path)
		}
		moduleIndex[path] = i
		m.Snapshots[path] = unit.Snapshot
		m.Bodies[path] = unit.Facts.Module
	}

	// 第一遍：给每个模块的每个声明分配全局 ID，并确定函数名。
	ids := make(map[string]map[int]int, len(units))
	nextID := 0
	for i, unit := range units {
		path := unit.Facts.ModulePath
		ids[path] = map[int]int{}
		for _, decl := range unit.Facts.Declarations {
			nextID++
			ids[path][decl.ID] = nextID
			if decl.Kind == frontend.DeclFunction {
				m.FunctionName[nextID] = fmt.Sprintf("m%d$%s", i, decl.Name)
			}
		}
	}

	// 导出名到全局声明 ID，用于把跨模块调用指到真实函数。
	exports := map[string]map[string]int{}
	for _, unit := range units {
		path := unit.Facts.ModulePath
		exports[path] = map[string]int{}
		for _, exp := range unit.Facts.Exports {
			if id, ok := ids[path][exp.ID]; ok {
				exports[path][exp.Name] = id
			}
		}
	}

	merged := &frontend.Facts{
		SchemaVersion: unitSchema(units),
		Session:       "merged",
		File:          units[0].Facts.File,
		ModulePath:    units[0].Facts.ModulePath,
	}
	for _, unit := range units {
		path := unit.Facts.ModulePath
		refTargets := map[int]int{}
		for _, ref := range unit.Facts.References {
			if ref.TargetModule != "" && ref.TargetModule != path {
				if id, ok := exports[ref.TargetModule][ref.TargetName]; ok {
					refTargets[ref.Start] = id
				}
			} else {
				refTargets[ref.Start] = remapID(ref.Target, ids[path])
			}
		}
		callTargets := map[int]int{}
		for _, call := range unit.Facts.Calls {
			if call.CalleeModule != "" && call.CalleeModule != path {
				if id, ok := exports[call.CalleeModule][call.CalleeName]; ok {
					callTargets[call.Start] = id
				}
			} else {
				callTargets[call.Start] = remapID(call.Callee, ids[path])
			}
		}
		m.Bodies[path] = remapNode(unit.Facts.Module, ids[path], callTargets, refTargets)
		for _, decl := range unit.Facts.Declarations {
			copy := decl
			copy.ID = ids[path][decl.ID]
			copy.Module = path
			if copy.Kind == frontend.DeclFunction {
				copy.Name = m.FunctionName[copy.ID]
			}
			copy.Parameters = remapIDs(decl.Parameters, ids[path])
			copy.Body = remapNode(decl.Body, ids[path], callTargets, refTargets)
			merged.Declarations = append(merged.Declarations, copy)
		}
		for _, ref := range unit.Facts.References {
			copy := ref
			copy.Target = remapID(ref.Target, ids[path])
			merged.References = append(merged.References, copy)
		}
		for _, call := range unit.Facts.Calls {
			copy := call
			if call.CalleeModule != "" && call.CalleeModule != path {
				if id, ok := exports[call.CalleeModule][call.CalleeName]; ok {
					copy.Callee = id
				}
			} else {
				copy.Callee = remapID(call.Callee, ids[path])
			}
			merged.Calls = append(merged.Calls, copy)
		}
		for _, alias := range unit.Facts.Aliases {
			merged.Aliases = append(merged.Aliases, alias)
		}
		for _, exp := range unit.Facts.Exports {
			copy := exp
			copy.ID = remapID(exp.ID, ids[path])
			merged.Exports = append(merged.Exports, copy)
		}
	}
	m.Facts = merged
	return m, nil
}

func unitSchema(units []Unit) int {
	for _, unit := range units {
		return unit.Facts.SchemaVersion
	}
	return 1
}

func remapID(id int, ids map[int]int) int {
	if id == 0 {
		return 0
	}
	if mapped, ok := ids[id]; ok {
		return mapped
	}
	return 0
}

func remapIDs(in []int, ids map[int]int) []int {
	out := make([]int, len(in))
	for i, id := range in {
		out[i] = remapID(id, ids)
	}
	return out
}

func remapNode(n *frontend.Node, ids map[int]int, callTargets, refTargets map[int]int) *frontend.Node {
	if n == nil {
		return nil
	}
	out := *n
	out.Decl = remapID(n.Decl, ids)
	out.Target = remapID(n.Target, ids)
	if n.Kind == "Identifier" {
		if target, ok := refTargets[n.Start]; ok && target != 0 {
			out.Target = target
		}
	}
	out.Children = make([]frontend.Node, len(n.Children))
	for i := range n.Children {
		child := remapNode(&n.Children[i], ids, callTargets, refTargets)
		if child != nil {
			out.Children[i] = *child
		}
	}
	if n.Kind == "CallExpression" && len(out.Children) > 0 && out.Children[0].Kind == "Identifier" {
		if target, ok := callTargets[n.Start]; ok && target != 0 {
			out.Children[0].Target = target
		}
	}
	return &out
}
