package frontend

import "github.com/Mahoo12138/tynel/compiler/diagnostic"

// Validate 检查事实的内部一致性。
//
// 它回答的是「这份数据自己讲得通吗」：ID 有没有重复、引用指向的声明在不在、
// 调用的实参个数与类型是否与被调函数的形参一致、返回类型摘要是否与声明一致。
// 它不重新做类型检查，也不把类型字符串解析成类别——类型在事实里已经是枚举。
func (f *Facts) Validate() error {
	if f.SchemaVersion != 1 {
		return diagnostic.New(diagnostic.Internal, "facts.schema", "事实的 schemaVersion 是 %d，本版本只认识 1", f.SchemaVersion)
	}
	if f.Session == "" {
		return diagnostic.New(diagnostic.Internal, "facts.no-session", "事实缺少会话标识")
	}
	if f.File == "" {
		return diagnostic.New(diagnostic.Internal, "facts.no-file", "事实缺少文件路径")
	}

	byID := make(map[int]*Declaration, len(f.Declarations))
	for i := range f.Declarations {
		d := &f.Declarations[i]
		if d.ID <= 0 {
			return diagnostic.New(diagnostic.Internal, "facts.bad-id", "声明 %q 的 ID 是 %d，必须为正数", d.Name, d.ID)
		}
		if _, ok := byID[d.ID]; ok {
			return diagnostic.New(diagnostic.Internal, "facts.duplicate-id", "声明 ID %d 出现了两次", d.ID)
		}
		if !d.Kind.valid() {
			return diagnostic.New(diagnostic.Internal, "facts.bad-decl-kind", "声明 %q 的种类 %q 不认识", d.Name, d.Kind)
		}
		if d.Name == "" {
			return diagnostic.New(diagnostic.Internal, "facts.bad-name", "声明 ID %d 没有名字", d.ID)
		}
		if d.Start < 0 || d.Length <= 0 {
			return diagnostic.New(diagnostic.Internal, "facts.bad-position", "声明 %q 的位置 %d+%d 不合法", d.Name, d.Start, d.Length)
		}
		switch d.Kind {
		case DeclFunction:
			if err := requireType("函数 "+d.Name, d.Result, d.Type); err != nil {
				return err
			}
		case DeclParameter, DeclConst:
			if err := requireType(d.Kind.String()+" "+d.Name, d.Type, d.Result); err != nil {
				return err
			}
			if len(d.Parameters) > 0 {
				return diagnostic.New(diagnostic.Internal, "facts.bad-decl", "%s %q 不应带参数列表", d.Kind, d.Name)
			}
		}
		byID[d.ID] = d
	}

	// 每个参数必须恰好属于一个函数：既不能没有归属，也不能被两个函数共用。
	owner := make(map[int]int, len(f.Declarations))
	for i := range f.Declarations {
		d := &f.Declarations[i]
		if d.Kind != DeclFunction {
			continue
		}
		for _, pid := range d.Parameters {
			target, ok := byID[pid]
			if !ok {
				return diagnostic.New(diagnostic.Internal, "facts.unknown-target", "函数 %q 的参数 ID %d 不存在", d.Name, pid)
			}
			if target.Kind != DeclParameter {
				return diagnostic.New(diagnostic.Internal, "facts.bad-parameter", "函数 %q 的参数 ID %d 指向 %s，不是参数", d.Name, pid, target.Kind)
			}
			if prev, ok := owner[pid]; ok {
				return diagnostic.New(diagnostic.Internal, "facts.shared-parameter", "参数 ID %d 同时属于函数 %d 和 %d", pid, prev, d.ID)
			}
			owner[pid] = d.ID
		}
	}
	for i := range f.Declarations {
		d := &f.Declarations[i]
		if d.Kind == DeclParameter {
			if _, ok := owner[d.ID]; !ok {
				return diagnostic.New(diagnostic.Internal, "facts.orphan-parameter", "参数 %q（ID %d）不属于任何函数", d.Name, d.ID)
			}
		}
	}

	for _, ref := range f.References {
		if ref.Name == "" || ref.Length <= 0 || ref.Start < 0 {
			return diagnostic.New(diagnostic.Internal, "facts.bad-position", "引用 %q 的位置 %d+%d 不合法", ref.Name, ref.Start, ref.Length)
		}
		if ref.Target == 0 {
			continue // 没有解析到本文件内的声明，例如内建全局。
		}
		if _, ok := byID[ref.Target]; !ok {
			return diagnostic.New(diagnostic.Internal, "facts.unknown-target", "引用 %q 指向不存在的声明 ID %d", ref.Name, ref.Target)
		}
	}

	for _, call := range f.Calls {
		if call.Length <= 0 || call.Start < 0 {
			return diagnostic.New(diagnostic.Internal, "facts.bad-position", "调用位置 %d+%d 不合法", call.Start, call.Length)
		}
		if !call.Result.Kind.valid() {
			return diagnostic.New(diagnostic.Internal, "facts.bad-type", "调用的返回类型 %q 不认识", call.Result.Kind)
		}
		for _, arg := range call.Arguments {
			if !arg.Kind.valid() {
				return diagnostic.New(diagnostic.Internal, "facts.bad-type", "实参类型 %q 不认识", arg.Kind)
			}
		}
		if call.Callee == 0 {
			continue // 被调用者不在本文件内，例如 console。
		}
		callee, ok := byID[call.Callee]
		if !ok {
			return diagnostic.New(diagnostic.Internal, "facts.unknown-target", "调用指向不存在的声明 ID %d", call.Callee)
		}
		if callee.Kind != DeclFunction {
			return diagnostic.New(diagnostic.Internal, "facts.call-target", "调用指向 %s %q，它不是函数", callee.Kind, callee.Name)
		}
		if len(call.Arguments) != len(callee.Parameters) {
			return diagnostic.New(diagnostic.Internal, "facts.arg-count", "调用 %q 给了 %d 个实参，函数声明有 %d 个参数", callee.Name, len(call.Arguments), len(callee.Parameters))
		}
		for i, pid := range callee.Parameters {
			param, ok := byID[pid]
			if !ok || param.Type == nil {
				return diagnostic.New(diagnostic.Internal, "facts.unknown-target", "函数 %q 的第 %d 个参数没有类型摘要", callee.Name, i+1)
			}
			if call.Arguments[i].Kind != param.Type.Kind {
				return diagnostic.New(diagnostic.Internal, "facts.arg-type", "调用 %q 的第 %d 个实参是 %s，形参是 %s", callee.Name, i+1, call.Arguments[i].Kind, param.Type.Kind)
			}
		}
		if callee.Result == nil || call.Result.Kind != callee.Result.Kind {
			return diagnostic.New(diagnostic.Internal, "facts.result-type", "调用 %q 的返回类型摘要与函数声明不一致", callee.Name)
		}
	}

	for _, expr := range f.Expressions {
		if expr.Kind == "" || expr.Length <= 0 || expr.Start < 0 {
			return diagnostic.New(diagnostic.Internal, "facts.bad-position", "表达式位置 %d+%d 或类别 %q 不合法", expr.Start, expr.Length, expr.Kind)
		}
		if !expr.Type.Kind.valid() {
			return diagnostic.New(diagnostic.Internal, "facts.bad-type", "表达式类型 %q 不认识", expr.Type.Kind)
		}
	}
	return nil
}

// requireType 要求 want 存在，且 forbid 不存在。函数有返回类型、没有自身类型；
// 参数与 const 相反。
func requireType(what string, want, forbid *Type) error {
	if want == nil {
		return diagnostic.New(diagnostic.Internal, "facts.bad-type", "%s 缺少类型摘要", what)
	}
	if !want.Kind.valid() {
		return diagnostic.New(diagnostic.Internal, "facts.bad-type", "%s 的类型 %q 不认识", what, want.Kind)
	}
	if forbid != nil {
		return diagnostic.New(diagnostic.Internal, "facts.bad-decl", "%s 不应带多余的类型摘要", what)
	}
	return nil
}

// String 返回声明种类的可读名，用于错误消息。
func (k DeclKind) String() string {
	switch k {
	case DeclFunction:
		return "函数"
	case DeclParameter:
		return "参数"
	case DeclConst:
		return "常量"
	}
	return string(k)
}
