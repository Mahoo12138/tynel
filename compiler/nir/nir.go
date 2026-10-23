// Package nir 定义 Tynel 的中间表示及其 JSON 文件格式。
//
// NIR 是前端与后端之间唯一的交接物：前端把源码理解成这个结构，后端只读这个
// 结构，双方不共享 tsgo 或 Go 的内部对象。本包只负责形状、编码和格式预算；
// 函数引用、参数数量和类型一致性由独立的验证器检查（01.04），不在这里顺手
// 做一半。
//
// 结构上它接近一个 C 结构体：每个节点有 op 标签，其余字段是标签决定用哪几个
// 的联合体。JSON 没有联合类型，所以同一层字段名固定，由 check 按 op 拒绝
// 不属于该节点的字段。
package nir

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// SchemaVersion 是当前文件格式版本。格式不兼容时递增，旧文件必须被拒绝，
// 不能靠缺省值兼容。
const SchemaVersion = 1

// 结果类型与参数类型。首版只有 number 和 void。
const (
	TypeNumber = "number"
	TypeVoid   = "void"
)

// 表达式操作。
const (
	OpNumber = "number"
	OpParam  = "param"
	OpCall   = "call"
	OpBinary = "binary"
)

// 语句操作。
const (
	OpReturn = "return"
	OpPrint  = "print"
)

// BinaryOperators 是首版允许的二元运算符，顺序用于报错提示。
var BinaryOperators = []string{"+", "-", "*", "/"}

// Module 是一个可编译单元。
type Module struct {
	SchemaVersion int        `json:"schemaVersion"`
	Entry         string     `json:"entry"`
	Functions     []Function `json:"functions"`
}

// Function 是具名函数。首版只有顶层函数，没有闭包和方法。
type Function struct {
	Name   string  `json:"name"`
	Params []Param `json:"params"`
	Result string  `json:"result"`
	Body   []Stmt  `json:"body"`
	Pos    *Pos    `json:"pos,omitempty"`
}

// Param 是函数参数。首版只有 number。
type Param struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Pos  *Pos   `json:"pos,omitempty"`
}

// Pos 是源码位置。手写 NIR 时可以省略，前端产出时填写。
type Pos struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// Stmt 是一条语句。Value 为 nil 只对无返回值的 return 合法。
type Stmt struct {
	Op    string `json:"op"`
	Value *Expr  `json:"value,omitempty"`
	Pos   *Pos   `json:"pos,omitempty"`
}

// Expr 是一个表达式节点。字段按 Op 取用，其余字段必须为空。
type Expr struct {
	Op       string `json:"op"`
	Bits     string `json:"bits,omitempty"`
	Name     string `json:"name,omitempty"`
	Function string `json:"function,omitempty"`
	Args     []Expr `json:"args,omitempty"`
	Operator string `json:"operator,omitempty"`
	Left     *Expr  `json:"left,omitempty"`
	Right    *Expr  `json:"right,omitempty"`
	Pos      *Pos   `json:"pos,omitempty"`
}

// BitsOf 返回 f 的 binary64 位模式，格式与文件中的 bits 字段一致。
func BitsOf(f float64) string {
	return fmt.Sprintf("0x%016x", math.Float64bits(f))
}

// Float64 把 bits 字段解析为 float64。它保留负零、NaN 和无穷的位模式。
func Float64(bits string) (float64, error) {
	v, err := parseBits(bits)
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(v), nil
}

// Number 构造一个 number 字面量节点。
func Number(f float64) Expr {
	return Expr{Op: OpNumber, Bits: BitsOf(f)}
}

// parseBits 要求 16 位十六进制，允许 0x 前缀，不区分大小写。
func parseBits(bits string) (uint64, error) {
	s := strings.TrimSpace(bits)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if len(s) != 16 {
		return 0, fmt.Errorf("bits %q 必须是 16 位十六进制", bits)
	}
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("bits %q 不是合法的十六进制：%w", bits, err)
	}
	return v, nil
}

// IsBinaryOperator 报告 op 是否为支持的二元运算符。
func IsBinaryOperator(op string) bool {
	for _, candidate := range BinaryOperators {
		if candidate == op {
			return true
		}
	}
	return false
}
