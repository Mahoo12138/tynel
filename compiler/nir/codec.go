package nir

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// Limits 是读入预算。超过任一限制即拒绝，不做截断，也不尝试继续解析。
type Limits struct {
	// MaxBytes 是输入文件字节数上限。
	MaxBytes int
	// MaxDepth 是表达式的嵌套深度上限，一个字面量或参数算 1 层。
	MaxDepth int
	// MaxNodes 是函数、参数、语句和表达式节点总数的上限。
	MaxNodes int
	// MaxFunctions 是模块内函数数量的上限。
	MaxFunctions int
}

// DefaultLimits 返回当前使用的预算。数值随格式一起演进，改动要同步文档。
func DefaultLimits() Limits {
	return Limits{
		MaxBytes:     1 << 20,
		MaxDepth:     64,
		MaxNodes:     10000,
		MaxFunctions: 256,
	}
}

func (l Limits) check() error {
	if l.MaxBytes <= 0 || l.MaxDepth <= 0 || l.MaxNodes <= 0 || l.MaxFunctions <= 0 {
		return badf("nir.budget", "读入预算必须全为正数：%+v", l)
	}
	return nil
}

// Decode 从 r 读取模块。读取量先受 MaxBytes 限制，再交给 DecodeBytes。
func Decode(r io.Reader, limits Limits) (*Module, error) {
	if err := limits.check(); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(r, int64(limits.MaxBytes)+1))
	if err != nil {
		return nil, iof("读取输入失败：%v", err)
	}
	if len(data) > limits.MaxBytes {
		return nil, badf("nir.budget", "输入超过 %d 字节的读入预算", limits.MaxBytes)
	}
	return DecodeBytes(data, limits)
}

// DecodeBytes 解析一份 NIR JSON。任何错误都返回 nil 模块：调用方不会拿到
// 半个可用的结构。
func DecodeBytes(data []byte, limits Limits) (*Module, error) {
	if err := limits.check(); err != nil {
		return nil, err
	}
	if len(data) > limits.MaxBytes {
		return nil, badf("nir.budget", "输入为 %d 字节，超过预算 %d", len(data), limits.MaxBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var module Module
	if err := dec.Decode(&module); err != nil {
		return nil, malformed(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, badf("nir.trailing", "JSON 文档结束后还有多余内容")
	}
	if err := module.check(limits); err != nil {
		return nil, err
	}
	return &module, nil
}

// Encode 把模块写成 JSON。它先完整编码再一次性写出，编码失败时不写任何字节。
func Encode(w io.Writer, module *Module) error {
	data, err := EncodeBytes(module)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return iof("写出 NIR 失败：%v", err)
	}
	return nil
}

// EncodeBytes 返回格式化后的 JSON。编码前先按默认预算自检，避免写出自己读不
// 回来的文件。
func EncodeBytes(module *Module) ([]byte, error) {
	if module == nil {
		return nil, badf("nir.missing", "模块为空")
	}
	if err := module.check(DefaultLimits()); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(module, "", "  ")
	if err != nil {
		return nil, iof("序列化 NIR 失败：%v", err)
	}
	return append(data, '\n'), nil
}

// LoadFile 读取一个 NIR 文件。
func LoadFile(path string, limits Limits) (*Module, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, iof("打开 %s 失败：%v", path, err)
	}
	defer f.Close()
	return Decode(f, limits)
}

// SaveFile 先写同目录临时文件再改名。编码失败、写入失败或改名失败都不会留下
// 半份文件，也不会破坏已有内容。
func SaveFile(path string, module *Module) error {
	data, err := EncodeBytes(module)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".nir-*.tmp")
	if err != nil {
		return iof("创建临时文件失败：%v", err)
	}
	name := tmp.Name()
	defer os.Remove(name)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return iof("写入临时文件失败：%v", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return iof("同步临时文件失败：%v", err)
	}
	if err := tmp.Close(); err != nil {
		return iof("关闭临时文件失败：%v", err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return iof("设置文件权限失败：%v", err)
	}
	if err := os.Rename(name, path); err != nil {
		return iof("发布 %s 失败：%v", path, err)
	}
	return nil
}

// check 检查格式层面的合法性：版本、必填字段、按 op 允许的字段、位串、预算，
// 以及表达式是否为树。引用、参数数量和类型一致性由 Verify 负责。
func (m *Module) check(limits Limits) error {
	if m.SchemaVersion != SchemaVersion {
		return badf("nir.version", "schemaVersion 为 %d，本版本只接受 %d", m.SchemaVersion, SchemaVersion)
	}
	if m.Entry == "" {
		return badf("nir.missing", "缺少 entry")
	}
	if len(m.Functions) == 0 {
		return badf("nir.missing", "模块没有函数")
	}
	if len(m.Functions) > limits.MaxFunctions {
		return badf("nir.budget", "函数数量 %d 超过预算 %d", len(m.Functions), limits.MaxFunctions)
	}

	nodes := 0
	for i := range m.Functions {
		f := &m.Functions[i]
		path := fmt.Sprintf("functions[%d]", i)
		if f.Name == "" {
			return badf("nir.missing", "%s 缺少 name", path)
		}
		if f.Result != TypeNumber && f.Result != TypeBool && f.Result != TypeVoid {
			return badf("nir.bad-value", "%s.result 为 %q，只接受 number、bool 或 void", path, f.Result)
		}
		nodes++
		for j := range f.Params {
			p := &f.Params[j]
			ppath := fmt.Sprintf("%s.params[%d]", path, j)
			if p.Name == "" {
				return badf("nir.missing", "%s 缺少 name", ppath)
			}
			if p.Type != TypeNumber && p.Type != TypeBool {
				return badf("nir.bad-value", "%s.type 为 %q，只接受 number 或 bool", ppath, p.Type)
			}
			nodes++
		}
		for j := range f.Body {
			s := &f.Body[j]
			spath := fmt.Sprintf("%s.body[%d]", path, j)
			nodes++
			switch s.Op {
			case OpReturn, OpPrint, OpLet, OpDiscard:
			case "":
				return badf("nir.missing", "%s 缺少 op", spath)
			default:
				return badf("nir.bad-op", "%s.op 为 %q，只接受 return、print、let 或 discard", spath, s.Op)
			}
			if s.Op != OpLet && s.Name != "" {
				return badf("nir.unexpected-field", "%s（op=%s）不接受字段 name", spath, s.Op)
			}
			if s.Op == OpPrint && s.Value == nil {
				return badf("nir.missing", "%s 的 print 缺少 value", spath)
			}
			if s.Op == OpDiscard && s.Value == nil {
				return badf("nir.missing", "%s 的 discard 缺少 value", spath)
			}
			if s.Op == OpLet {
				if s.Name == "" {
					return badf("nir.missing", "%s 的 let 缺少 name", spath)
				}
				if s.Value == nil {
					return badf("nir.missing", "%s 的 let 缺少 value", spath)
				}
			}
			if s.Value != nil {
				if err := checkExpr(s.Value, spath+".value", 1, &nodes, limits); err != nil {
					return err
				}
			}
		}
	}
	if nodes > limits.MaxNodes {
		return badf("nir.budget", "节点总数 %d 超过预算 %d", nodes, limits.MaxNodes)
	}
	return nil
}

// checkExpr 用显式栈遍历表达式，避免为了测深度而先递归到栈溢出。
func checkExpr(root *Expr, rootPath string, depth int, nodes *int, limits Limits) error {
	type item struct {
		expr  *Expr
		path  string
		depth int
	}
	seen := make(map[*Expr]bool)
	stack := []item{{root, rootPath, depth}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		e := it.expr
		if seen[e] {
			// 表达式必须是树。共享节点在写回文件时会被复制成两份，循环节点会
			// 让任何遍历都不终止，两者都拒绝。
			return badf("nir.cycle", "%s 重复引用了同一个节点（共享或循环）", it.path)
		}
		seen[e] = true
		*nodes++
		if it.depth > limits.MaxDepth {
			return badf("nir.budget", "%s 的嵌套深度超过预算 %d", it.path, limits.MaxDepth)
		}

		switch e.Op {
		case OpNumber:
			if err := onlyFields(e, it.path, "bits"); err != nil {
				return err
			}
			if e.Bits == "" {
				return badf("nir.missing", "%s 缺少 bits", it.path)
			}
			if _, err := parseBits(e.Bits); err != nil {
				return badf("nir.bad-bits", "%s 的 bits 不合法：%v", it.path, err)
			}
		case OpBool:
			if err := onlyFields(e, it.path, "value"); err != nil {
				return err
			}
			if e.Value == nil {
				// false 是合法值，不能靠「零值」判断缺失；这里看的是指针。
				return badf("nir.missing", "%s 的 bool 缺少 value", it.path)
			}
		case OpNot:
			if err := onlyFields(e, it.path, "operand"); err != nil {
				return err
			}
			if e.Operand == nil {
				return badf("nir.missing", "%s 的 not 缺少 operand", it.path)
			}
			stack = append(stack, item{e.Operand, it.path + ".operand", it.depth + 1})
		case OpParam, OpLocal:
			if err := onlyFields(e, it.path, "name"); err != nil {
				return err
			}
			if e.Name == "" {
				return badf("nir.missing", "%s 缺少 name", it.path)
			}
		case OpCall:
			if err := onlyFields(e, it.path, "function,args"); err != nil {
				return err
			}
			if e.Function == "" {
				return badf("nir.missing", "%s 缺少 function", it.path)
			}
			for i := range e.Args {
				stack = append(stack, item{&e.Args[i], fmt.Sprintf("%s.args[%d]", it.path, i), it.depth + 1})
			}
		case OpBinary:
			if err := onlyFields(e, it.path, "operator,left,right"); err != nil {
				return err
			}
			if !IsBinaryOperator(e.Operator) {
				return badf("nir.bad-op", "%s.operator 为 %q，只接受 %s", it.path, e.Operator, strings.Join(BinaryOperators, "、"))
			}
			if e.Left == nil || e.Right == nil {
				return badf("nir.missing", "%s 的 binary 缺少 left 或 right", it.path)
			}
			stack = append(stack,
				item{e.Left, it.path + ".left", it.depth + 1},
				item{e.Right, it.path + ".right", it.depth + 1},
			)
		case "":
			return badf("nir.missing", "%s 缺少 op", it.path)
		default:
			return badf("nir.bad-op", "%s.op 为 %q，只接受 number、bool、param、local、call、binary 或 not", it.path, e.Op)
		}
	}
	return nil
}

// onlyFields 拒绝不属于当前 op 的字段。JSON 没有联合类型，只能在这里补上。
func onlyFields(e *Expr, path, allowed string) error {
	permitted := make(map[string]bool)
	for _, name := range strings.Split(allowed, ",") {
		permitted[name] = true
	}
	present := map[string]bool{
		"bits":     e.Bits != "",
		"value":    e.Value != nil,
		"name":     e.Name != "",
		"function": e.Function != "",
		"args":     len(e.Args) > 0,
		"operator": e.Operator != "",
		"left":     e.Left != nil,
		"right":    e.Right != nil,
		"operand":  e.Operand != nil,
	}
	for name, ok := range present {
		if ok && !permitted[name] {
			return badf("nir.unexpected-field", "%s（op=%s）不接受字段 %s", path, e.Op, name)
		}
	}
	return nil
}

func malformed(err error) error {
	d := badf("nir.malformed", "NIR JSON 无法解析")
	return d.WithDetail(err.Error())
}

func badf(code, format string, args ...any) *diagnostic.Diagnostic {
	return diagnostic.New(diagnostic.Source, code, format, args...)
}

func iof(format string, args ...any) *diagnostic.Diagnostic {
	return diagnostic.New(diagnostic.Environment, "nir.io", format, args...)
}
