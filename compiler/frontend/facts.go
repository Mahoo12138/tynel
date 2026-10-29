// Package frontend 定义编译器从 TypeScript 前端拿到的自有事实。
//
// 事实是一份纯数据：声明、引用、调用和表达式类型。它不含上游的 AST、Symbol
// 或 Type 指针，所以会话关闭之后仍然可读，也可以直接写进文件或跨进程传递。
//
// 事实由适配层产出、由本包解码并校验。校验只检查内部一致性——ID 是否重复、
// 引用是否指向存在的声明、调用实参是否与形参对得上、返回类型摘要是否与声明
// 一致。它不重新做类型检查，也不解析人类可读的类型字符串。
package frontend

import (
	"bytes"
	"encoding/json"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// Kind 是类型摘要的类别。只区分本阶段关心的几种，其余统一为 other。
type Kind string

const (
	KindNumber    Kind = "number"
	KindBoolean   Kind = "boolean"
	KindString    Kind = "string"
	KindVoid      Kind = "void"
	KindAny       Kind = "any"
	KindOther     Kind = "other"
	KindUnion     Kind = "union"
	KindInterface Kind = "interface"
)

// valid 报告类别是否在允许集合内。校验按枚举比较，不做字符串规整。
func (k Kind) valid() bool {
	switch k {
	case KindNumber, KindBoolean, KindString, KindVoid, KindAny, KindOther, KindUnion, KindInterface:
		return true
	}
	return false
}

// Type 是类型摘要。
type Type struct {
	Kind Kind `json:"kind"`
}

// DeclKind 是声明的种类。
type DeclKind string

const (
	DeclFunction  DeclKind = "function"
	DeclParameter DeclKind = "parameter"
	DeclConst     DeclKind = "const"
	DeclLocal     DeclKind = "local"
	DeclLet       DeclKind = "let"
	DeclModuleLet DeclKind = "module-let"
	DeclVar       DeclKind = "var"
)

func (k DeclKind) valid() bool {
	switch k {
	case DeclFunction, DeclParameter, DeclConst, DeclLocal, DeclLet, DeclModuleLet, DeclVar:
		return true
	}
	return false
}

// Node 是函数体或初始化表达式的语法骨架。
//
// Kind 是上游的语法种类名（ReturnStatement、BinaryExpression…）。树里只有
// 语句、表达式和变量声明，类型节点与名字令牌不进树，所以不会出现类型名冒充
// 的引用。调用方按 Kind 判断是否支持，未知的 Kind 一律当作不支持。
type Node struct {
	Kind     string `json:"kind"`
	Start    int    `json:"start"`
	Length   int    `json:"length"`
	Op       string `json:"op,omitempty"`
	Name     string `json:"name,omitempty"`
	Target   int    `json:"target,omitempty"`
	Decl     int    `json:"decl,omitempty"`
	Bits     string `json:"bits,omitempty"`
	TypeOnly bool   `json:"typeOnly,omitempty"`
	Type     *Type  `json:"type,omitempty"`
	Children []Node `json:"children,omitempty"`
}

// Declaration 是一个声明。ID 在事实内唯一，同名声明各有各的 ID。
type Declaration struct {
	ID         int      `json:"id"`
	Kind       DeclKind `json:"kind"`
	Name       string   `json:"name"`
	Start      int      `json:"start"`
	Length     int      `json:"length"`
	Type       *Type    `json:"type,omitempty"`
	Result     *Type    `json:"result,omitempty"`
	Parameters []int    `json:"parameters,omitempty"`
	Body       *Node    `json:"body,omitempty"`
	Mutable    bool     `json:"mutable,omitempty"`
}

// Alias 是一个类型别名或接口声明。Kind 是类型空间的归类：number、boolean、
// interface、union、any 或 other。别名不产生运行时值。
type Alias struct {
	Name   string `json:"name"`
	Start  int    `json:"start"`
	Length int    `json:"length"`
	Kind   Kind   `json:"kind"`
}

// Reference 是一次名称引用。Target 为 0 表示没有解析到本文件内的声明。
type Reference struct {
	Name   string `json:"name"`
	Start  int    `json:"start"`
	Length int    `json:"length"`
	Target int    `json:"target"`
}

// Call 是一次调用。Callee 为 0 表示被调用者不在本文件内。
type Call struct {
	Start     int    `json:"start"`
	Length    int    `json:"length"`
	Callee    int    `json:"callee"`
	Arguments []Type `json:"arguments"`
	Result    Type   `json:"result"`
}

// Expression 是一个表达式及其类型。
type Expression struct {
	Start  int    `json:"start"`
	Length int    `json:"length"`
	Kind   string `json:"kind"`
	Type   Type   `json:"type"`
}

// Facts 是一次检查的全部事实。
type Facts struct {
	SchemaVersion int           `json:"schemaVersion"`
	Session       string        `json:"session"`
	File          string        `json:"file"`
	Module        *Node         `json:"module,omitempty"`
	Declarations  []Declaration `json:"declarations"`
	References    []Reference   `json:"references"`
	Calls         []Call        `json:"calls"`
	Expressions   []Expression  `json:"expressions"`
	Aliases       []Alias       `json:"aliases,omitempty"`
}

// Decode 解析适配层输出的事实。未知字段直接拒绝：字段名对不上说明适配层
// 与本包的约定已经漂移，静默忽略会让一半事实悄悄消失。
func Decode(data []byte) (*Facts, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f Facts
	if err := dec.Decode(&f); err != nil {
		return nil, diagnostic.New(diagnostic.Internal, "facts.decode", "事实不是预期的 JSON 结构").WithDetail(err.Error())
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

// Declaration 按 ID 取声明。
func (f *Facts) Declaration(id int) (*Declaration, bool) {
	for i := range f.Declarations {
		if f.Declarations[i].ID == id {
			return &f.Declarations[i], true
		}
	}
	return nil, false
}

// Combine 把两份事实合并成一份。
//
// 只有来自同一个会话的事实才能合并。不同会话的声明 ID 各自从 1 开始，混在
// 一起会出现「两个 ID 3 指向不同声明」，而且没有任何办法事后分辨。
func Combine(a, b *Facts) (*Facts, error) {
	if a.Session != b.Session {
		return nil, diagnostic.New(diagnostic.Internal, "facts.session-mismatch", "两份事实来自不同会话（%s 与 %s），不能合并", a.Session, b.Session)
	}
	if a.File != b.File {
		return nil, diagnostic.New(diagnostic.Internal, "facts.file-mismatch", "两份事实来自不同文件（%s 与 %s）", a.File, b.File)
	}
	merged := &Facts{
		SchemaVersion: a.SchemaVersion,
		Session:       a.Session,
		File:          a.File,
		Declarations:  append(append([]Declaration{}, a.Declarations...), b.Declarations...),
		References:    append(append([]Reference{}, a.References...), b.References...),
		Calls:         append(append([]Call{}, a.Calls...), b.Calls...),
		Expressions:   append(append([]Expression{}, a.Expressions...), b.Expressions...),
		Aliases:       append(append([]Alias{}, a.Aliases...), b.Aliases...),
	}
	if err := merged.Validate(); err != nil {
		return nil, err
	}
	return merged, nil
}
