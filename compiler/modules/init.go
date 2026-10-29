package modules

import (
	"math"
	"strconv"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// InitEdge 是初始化计划里的一条导入边。Value 为真时目标模块必须先执行。
type InitEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Local  string `json:"local"`
	Value  bool   `json:"value"`
	Type   bool   `json:"typeOnly"`
	Start  int    `json:"start"`
	Length int    `json:"length"`
}

// Print 是初始化阶段可静态确认的一次 console.log。它只用于展示顺序；
// 真正的执行在 05.06 合并后的 NIR 里。
type Print struct {
	Module string `json:"module"`
	Value  string `json:"value"`
}

// InitPlan 是模块初始化顺序。Order 只包含需要执行的模块，依赖在前、入口在
// 最后；类型模块只出现在 TypeEdges 里，不进入 Order。
type InitPlan struct {
	Entry      string     `json:"entry"`
	Order      []string   `json:"order"`
	ValueEdges []InitEdge `json:"valueEdges"`
	TypeEdges  []InitEdge `json:"typeEdges"`
	Prints     []Print    `json:"prints"`
}

// Plan 从入口构建初始化顺序。它按源码导入顺序深度优先：先完成值依赖，再
// 记录当前模块；同一个模块只执行一次。类型导入不触发执行。
func Plan(t *Table) (*InitPlan, error) {
	if t == nil {
		return nil, diagnostic.New(diagnostic.Internal, "init.no-table", "缺少模块符号表")
	}
	plan := &InitPlan{Entry: t.Entry}
	state := map[string]int{}
	var stack []string

	var visit func(string) error
	visit = func(path string) error {
		switch state[path] {
		case 1:
			return diagnostic.New(diagnostic.Source, "init.cycle", "初始化环：%s", cyclePath(stack, path))
		case 2:
			return nil
		}
		facts, ok := t.Modules[path]
		if !ok {
			return diagnostic.New(diagnostic.Source, "init.missing-module", "初始化计划里没有模块 %s", path)
		}
		state[path] = 1
		stack = append(stack, path)
		for _, imp := range facts.Imports {
			edge := InitEdge{From: path, To: imp.Module, Local: imp.Local, Value: !imp.TypeOnly, Type: imp.TypeOnly, Start: imp.Start, Length: imp.Length}
			if imp.TypeOnly {
				plan.TypeEdges = append(plan.TypeEdges, edge)
				continue
			}
			if _, ok := t.Modules[imp.Module]; !ok {
				return diagnostic.New(diagnostic.Source, "init.missing-module", "模块 %s 的值依赖 %s 不存在", path, imp.Module)
			}
			plan.ValueEdges = append(plan.ValueEdges, edge)
			if err := visit(imp.Module); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[path] = 2
		plan.Order = append(plan.Order, path)
		return nil
	}
	if err := visit(t.Entry); err != nil {
		return nil, err
	}
	if err := checkTypeValueMix(t, plan); err != nil {
		return nil, err
	}
	if err := checkUninitialized(t, plan); err != nil {
		return nil, err
	}
	plan.Prints = collectPrints(t, plan.Order)
	return plan, nil
}

func cyclePath(stack []string, repeated string) string {
	start := 0
	for i, path := range stack {
		if path == repeated {
			start = i
			break
		}
	}
	out := ""
	for i := start; i < len(stack); i++ {
		if out != "" {
			out += " -> "
		}
		out += stack[i]
	}
	if out != "" {
		out += " -> "
	}
	return out + repeated
}

// checkTypeValueMix 拒绝把 import type 绑定当值使用。类型注解不会产生
// Reference；一旦出现同名引用，就说明它进入了运行时表达式。
func checkTypeValueMix(t *Table, plan *InitPlan) error {
	for _, facts := range t.Modules {
		for _, imp := range facts.Imports {
			if !imp.TypeOnly {
				continue
			}
			for _, ref := range facts.References {
				if ref.Name == imp.Local && ref.TargetModule == imp.Module {
					return diagnostic.New(diagnostic.Source, "init.type-value-mix",
						"模块 %s 把 import type 绑定 %q 当值使用", facts.ModulePath, imp.Local)
				}
			}
		}
	}
	return nil
}

// checkUninitialized 确认每个顶层值引用都指向已经执行过的模块。DFS 顺序已经
// 保证值依赖在前；这里再按顺序表核对一次，防止事实与计划漂移。
func checkUninitialized(t *Table, plan *InitPlan) error {
	index := map[string]int{}
	for i, path := range plan.Order {
		index[path] = i
	}
	for _, path := range plan.Order {
		facts := t.Modules[path]
		for _, ref := range facts.References {
			if ref.TargetModule == "" || ref.TargetModule == path {
				continue
			}
			if _, known := t.Modules[ref.TargetModule]; !known {
				// 内置声明库不属于项目模块，不参与初始化顺序。
				continue
			}
			target, ok := index[ref.TargetModule]
			if !ok {
				return diagnostic.New(diagnostic.Source, "init.uninitialized",
					"模块 %s 在初始化时读取未执行模块 %s 的 %q", path, ref.TargetModule, ref.Name)
			}
			if target >= index[path] {
				return diagnostic.New(diagnostic.Source, "init.uninitialized",
					"模块 %s 在初始化时读取尚未执行的 %s 的 %q", path, ref.TargetModule, ref.Name)
			}
		}
	}
	return nil
}

// collectPrints 只识别模块体里可直接求值的 console.log(数字字面量)。它不执行
// 任意表达式；完整执行留给 05.06。
func collectPrints(t *Table, order []string) []Print {
	var out []Print
	for _, path := range order {
		facts := t.Modules[path]
		if facts.Module == nil {
			continue
		}
		for i := range facts.Module.Children {
			stmt := &facts.Module.Children[i]
			if stmt.Kind != "ExpressionStatement" || len(stmt.Children) != 1 {
				continue
			}
			call := &stmt.Children[0]
			if call.Kind != "CallExpression" || len(call.Children) < 2 {
				continue
			}
			callee := &call.Children[0]
			if callee.Kind != "PropertyAccessExpression" || callee.Name != "log" {
				continue
			}
			arg := &call.Children[1]
			if arg.Kind != "NumericLiteral" || arg.Bits == "" {
				continue
			}
			bits, err := strconv.ParseUint(arg.Bits, 0, 64)
			if err != nil {
				continue
			}
			out = append(out, Print{Module: path, Value: strconv.FormatFloat(math.Float64frombits(bits), 'g', -1, 64)})
		}
	}
	return out
}
