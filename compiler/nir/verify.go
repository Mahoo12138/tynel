package nir

import (
	"fmt"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// Verify 检查模块的语义合法性，返回第一处错误。
//
// 它先按默认预算重做一次格式检查，再检查入口签名、声明唯一性、参数引用、
// 调用实参、返回要求和表达式类型。验证器不假设模块来自 JSON 解码：内存里
// 直接构造的结构同样要过这一关，因此坏引用和循环引用不能靠解码器兜住。
//
// 它不导入 tsgo 或 Go 内部对象，只看 NIR 自己的数据。
func Verify(module *Module) error {
	return VerifyWithLimits(module, DefaultLimits())
}

// VerifyWithLimits 与 Verify 相同，但使用调用方给出的预算。
func VerifyWithLimits(module *Module, limits Limits) error {
	if module == nil {
		return badf("nir.missing", "模块为空")
	}
	// 格式、字段、预算和「表达式必须是树」先过一遍。循环结构在这里被拒绝，
	// 后面的递归检查才不会卡住。
	if err := module.check(limits); err != nil {
		return err
	}

	c := &checker{funcs: make(map[string]*Function, len(module.Functions))}
	for i := range module.Functions {
		f := &module.Functions[i]
		if prev, exists := c.funcs[f.Name]; exists {
			return failAt("nir.duplicate-name", f.Pos,
				"函数 %q 重复声明，另一处在 %s", f.Name, describePos(prev.Pos))
		}
		c.funcs[f.Name] = f
	}

	entry, ok := c.funcs[module.Entry]
	if !ok {
		return badf("nir.entry-missing", "入口函数 %q 不存在", module.Entry)
	}
	if len(entry.Params) != 0 || entry.Result != TypeVoid {
		return failAt("nir.entry-signature", entry.Pos,
			"入口函数 %q 必须没有参数且返回 void，实际有 %d 个参数、返回 %s",
			entry.Name, len(entry.Params), entry.Result)
	}

	for i := range module.Functions {
		if err := c.function(&module.Functions[i]); err != nil {
			return err
		}
	}
	return nil
}

// checker 保存一次验证的函数表，供调用解析使用。
type checker struct {
	funcs map[string]*Function
}

// function 检查一个函数体。返回值类型与参数表在这一层确定，表达式只做类型
// 推导，不生成任何东西。
func (c *checker) function(f *Function) error {
	sc := &scope{params: make(map[string]bool, len(f.Params)), locals: make(map[string]bool)}
	for i := range f.Params {
		p := &f.Params[i]
		if sc.params[p.Name] {
			return failAt("nir.duplicate-name", p.Pos, "函数 %q 的参数 %q 重复", f.Name, p.Name)
		}
		sc.params[p.Name] = true
	}

	for i := range f.Body {
		s := &f.Body[i]
		switch s.Op {
		case OpReturn:
			if f.Result == TypeVoid {
				if s.Value != nil {
					return failAt("nir.result-type", s.Pos, "void 函数 %q 的 return 不能带返回值", f.Name)
				}
				continue
			}
			if s.Value == nil {
				return failAt("nir.result-type", s.Pos, "函数 %q 返回 %s，return 必须带值", f.Name, f.Result)
			}
			got, err := c.exprType(s.Value, sc)
			if err != nil {
				return err
			}
			if got != f.Result {
				return failAt("nir.result-type", s.Pos, "函数 %q 声明返回 %s，实际返回 %s", f.Name, f.Result, got)
			}
		case OpPrint:
			got, err := c.exprType(s.Value, sc)
			if err != nil {
				return err
			}
			if got != TypeNumber {
				return failAt("nir.print-type", s.Pos, "print 只接受 number，实际为 %s", got)
			}
		case OpDiscard:
			// 结果被丢弃，类型只用来确认表达式本身成立：number 与 void 都行。
			if _, err := c.exprType(s.Value, sc); err != nil {
				return err
			}
		case OpLet:
			got, err := c.exprType(s.Value, sc)
			if err != nil {
				return err
			}
			if got != TypeNumber {
				return failAt("nir.local-type", s.Pos, "let %q 的值必须是 number，实际为 %s", s.Name, got)
			}
			if sc.params[s.Name] || sc.locals[s.Name] {
				return failAt("nir.duplicate-name", s.Pos, "函数 %q 里 %q 重复定义", f.Name, s.Name)
			}
			sc.locals[s.Name] = true
		}
	}

	// 首版没有分支，函数体的最后一条语句决定是否有返回值。
	if f.Result != TypeVoid {
		if len(f.Body) == 0 || f.Body[len(f.Body)-1].Op != OpReturn {
			return failAt("nir.missing-return", f.Pos, "函数 %q 返回 %s，但函数体没有以 return 结束", f.Name, f.Result)
		}
	}
	return nil
}

// scope 是函数体内的可见名字。参数与局部值分开记：前者由调用方提供，后者
// 必须由前面的 let 定义过。
type scope struct {
	params map[string]bool
	locals map[string]bool
}

// exprType 推导表达式类型。返回 TypeNumber 或 TypeVoid（void 调用）。
func (c *checker) exprType(e *Expr, sc *scope) (string, error) {
	if e == nil {
		return "", badf("nir.missing", "表达式为空")
	}
	switch e.Op {
	case OpNumber:
		return TypeNumber, nil
	case OpParam:
		if !sc.params[e.Name] {
			return "", failAt("nir.unknown-param", e.Pos, "引用了不存在的参数 %q", e.Name)
		}
		return TypeNumber, nil
	case OpLocal:
		if !sc.locals[e.Name] {
			return "", failAt("nir.unknown-local", e.Pos, "引用了尚未定义的局部值 %q", e.Name)
		}
		return TypeNumber, nil
	case OpCall:
		target, ok := c.funcs[e.Function]
		if !ok {
			return "", failAt("nir.unknown-function", e.Pos, "调用了不存在的函数 %q", e.Function)
		}
		if len(e.Args) != len(target.Params) {
			return "", failAt("nir.arg-count", e.Pos,
				"调用 %q 需要 %d 个实参，实际 %d 个", e.Function, len(target.Params), len(e.Args))
		}
		for i := range e.Args {
			got, err := c.exprType(&e.Args[i], sc)
			if err != nil {
				return "", err
			}
			if want := target.Params[i].Type; got != want {
				return "", failAt("nir.arg-type", argPos(&e.Args[i], e.Pos),
					"调用 %q 的第 %d 个实参需要 %s，实际为 %s", e.Function, i+1, want, got)
			}
		}
		return target.Result, nil
	case OpBinary:
		for _, side := range []struct {
			name string
			expr *Expr
		}{{"左", e.Left}, {"右", e.Right}} {
			if side.expr == nil {
				return "", failAt("nir.missing", e.Pos, "binary 缺少%s操作数", side.name)
			}
			got, err := c.exprType(side.expr, sc)
			if err != nil {
				return "", err
			}
			if got != TypeNumber {
				return "", failAt("nir.operand-type", side.expr.Pos,
					"运算符 %q 的%s操作数必须是 number，实际为 %s", e.Operator, side.name, got)
			}
		}
		return TypeNumber, nil
	default:
		return "", failAt("nir.bad-op", e.Pos, "未知表达式 op %q", e.Op)
	}
}

// argPos 优先用实参自己的位置，没有就退回调用节点的位置。
func argPos(arg *Expr, fallback *Pos) *Pos {
	if arg.Pos != nil {
		return arg.Pos
	}
	return fallback
}

func failAt(code string, pos *Pos, format string, args ...any) *diagnostic.Diagnostic {
	d := badf(code, format, args...)
	if pos != nil {
		d = d.At(pos.File, pos.Line, pos.Column)
	}
	return d
}

func describePos(pos *Pos) string {
	if pos == nil {
		return "未记录位置"
	}
	return fmt.Sprintf("%s:%d:%d", pos.File, pos.Line, pos.Column)
}
