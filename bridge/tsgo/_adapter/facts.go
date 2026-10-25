// 本文件属于 tyneladapter 包，和 adapter.go 一起被复制进上游源码树。
//
// 它把检查器里的信息摊平成普通数据：声明、引用、调用和表达式类型。上游的
// AST、Symbol、Type 指针一个都不出去——调用方拿到的只有 ID、名字、位置和
// 类型摘要，所以会话关掉之后这些数据仍然可读。
package tyneladapter

import (
	"context"
	"encoding/json"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// factType 是类型摘要。只记类别，不记人类可读的类型字符串：调用方需要判断
// 「是不是 number」，而不是把 "number | undefined" 拿去解析。
type factType struct {
	Kind string `json:"kind"`
}

type factDeclaration struct {
	ID         int       `json:"id"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	Start      int       `json:"start"`
	Length     int       `json:"length"`
	Type       *factType `json:"type,omitempty"`
	Result     *factType `json:"result,omitempty"`
	Parameters []int     `json:"parameters,omitempty"`
}

type factReference struct {
	Name   string `json:"name"`
	Start  int    `json:"start"`
	Length int    `json:"length"`
	Target int    `json:"target"`
}

type factCall struct {
	Start     int        `json:"start"`
	Length    int        `json:"length"`
	Callee    int        `json:"callee"`
	Arguments []factType `json:"arguments"`
	Result    factType   `json:"result"`
}

type factExpression struct {
	Start  int      `json:"start"`
	Length int      `json:"length"`
	Kind   string   `json:"kind"`
	Type   factType `json:"type"`
}

type factsPayload struct {
	SchemaVersion int               `json:"schemaVersion"`
	Session       string            `json:"session"`
	File          string            `json:"file"`
	Declarations  []factDeclaration `json:"declarations"`
	References    []factReference   `json:"references"`
	Calls         []factCall        `json:"calls"`
	Expressions   []factExpression  `json:"expressions"`
}

// Facts 把这次检查的事实导出为 JSON。
//
// 事实只覆盖顶层函数与 const：函数签名、参数、const 的声明身份，函数体与
// 初始化表达式里的名称引用、调用目标和表达式类型。语句结构不在这里，降低
// 阶段需要时再扩展。
func (s *Session) Facts() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrSessionClosed
	}
	ctx := context.Background()
	chk, release := s.program.GetTypeCheckerForFile(ctx, s.file)
	defer release()

	b := &factBuilder{
		checker:       chk,
		ids:           make(map[*ast.Symbol]int),
		declNameNodes: make(map[*ast.Node]bool),
		payload: factsPayload{
			SchemaVersion: 1,
			Session:       s.id,
			File:          s.file.FileName(),
		},
	}
	b.collect(s.file)
	return json.MarshalIndent(b.payload, "", "  ")
}

type factBuilder struct {
	checker       *checker.Checker
	ids           map[*ast.Symbol]int
	declNameNodes map[*ast.Node]bool
	nextID        int
	payload       factsPayload
}

// idFor 给符号分配声明 ID。同一个符号在多次引用中拿到同一个 ID，不同声明
// 拿到不同 ID——两个函数里各有一个 a，它们是两个符号，也就是两个 ID。
func (b *factBuilder) idFor(sym *ast.Symbol) int {
	if sym == nil {
		return 0
	}
	if id, ok := b.ids[sym]; ok {
		return id
	}
	b.nextID++
	b.ids[sym] = b.nextID
	return b.nextID
}

func (b *factBuilder) collect(file *ast.SourceFile) {
	for _, stmt := range file.Statements.Nodes {
		switch {
		case ast.IsFunctionDeclaration(stmt):
			b.function(stmt)
		case ast.IsVariableStatement(stmt):
			for _, decl := range stmt.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
				b.variable(decl)
			}
		}
	}
}

func (b *factBuilder) function(node *ast.Node) {
	fn := node.AsFunctionDeclaration()
	name := fn.Name()
	decl := factDeclaration{
		ID:     b.idFor(b.checker.GetSymbolAtLocation(name)),
		Kind:   "function",
		Name:   name.Text(),
		Start:  node.Pos(),
		Length: node.End() - node.Pos(),
	}
	b.declNameNodes[name] = true

	// 返回类型从调用签名取，而不是从源码里的类型注解文本取：注解可能缺失，
	// 而签名是检查器算出来的结果。
	if sigs := b.checker.GetSignaturesOfType(b.checker.GetTypeAtLocation(node), checker.SignatureKindCall); len(sigs) > 0 {
		result := b.typeOf(b.checker.GetReturnTypeOfSignature(sigs[0]))
		decl.Result = &result
	}

	// 用 *Node 上的访问器，而不是 FunctionDeclaration 结构体的同名字段：
	// 结构体字段是 *ParameterList，Node 方法才返回参数节点列表。
	// 先把参数的 ID 算出来，再按「函数在前、参数在后」的顺序写入，读起来的
	// 顺序与源码一致。
	var params []factDeclaration
	for _, param := range node.Parameters() {
		paramName := param.Name()
		id := b.idFor(b.checker.GetSymbolAtLocation(paramName))
		decl.Parameters = append(decl.Parameters, id)
		typ := b.typeOf(b.checker.GetTypeAtLocation(paramName))
		params = append(params, factDeclaration{
			ID:     id,
			Kind:   "parameter",
			Name:   paramName.Text(),
			Start:  param.Pos(),
			Length: param.End() - param.Pos(),
			Type:   &typ,
		})
		b.declNameNodes[paramName] = true
	}

	b.payload.Declarations = append(b.payload.Declarations, decl)
	b.payload.Declarations = append(b.payload.Declarations, params...)
	if body := node.Body(); body != nil {
		b.walk(body)
	}
}

func (b *factBuilder) variable(node *ast.Node) {
	decl := node.AsVariableDeclaration()
	name := decl.Name()
	typ := b.typeOf(b.checker.GetTypeAtLocation(name))
	b.payload.Declarations = append(b.payload.Declarations, factDeclaration{
		ID:     b.idFor(b.checker.GetSymbolAtLocation(name)),
		Kind:   "const",
		Name:   name.Text(),
		Start:  node.Pos(),
		Length: node.End() - node.Pos(),
		Type:   &typ,
	})
	b.declNameNodes[name] = true
	if decl.Initializer != nil {
		b.walk(decl.Initializer)
	}
}

// walk 只走函数体和初始化表达式，不走类型注解：类型注解里的 number、void
// 是类型名，不是对变量的引用，混进来会造出假的引用事实。
func (b *factBuilder) walk(node *ast.Node) {
	if node == nil {
		return
	}
	switch {
	case ast.IsIdentifier(node):
		if !b.declNameNodes[node] {
			b.reference(node)
		}
		return
	case ast.IsCallExpression(node):
		b.call(node)
	case ast.IsNumericLiteral(node):
		b.expression(node, "number")
	case ast.IsBinaryExpression(node):
		b.expression(node, "binary")
	}
	node.ForEachChild(func(child *ast.Node) bool {
		b.walk(child)
		return false
	})
}

func (b *factBuilder) reference(node *ast.Node) {
	b.payload.References = append(b.payload.References, factReference{
		Name:   node.Text(),
		Start:  node.Pos(),
		Length: node.End() - node.Pos(),
		Target: b.idFor(b.checker.GetSymbolAtLocation(node)),
	})
}

func (b *factBuilder) call(node *ast.Node) {
	call := node.AsCallExpression()
	callee := 0
	if ast.IsIdentifier(call.Expression) {
		callee = b.idFor(b.checker.GetSymbolAtLocation(call.Expression))
	}
	item := factCall{
		Start:  node.Pos(),
		Length: node.End() - node.Pos(),
		Callee: callee,
		Result: b.typeOf(b.checker.GetTypeAtLocation(node)),
	}
	if call.Arguments != nil {
		for _, arg := range call.Arguments.Nodes {
			item.Arguments = append(item.Arguments, b.typeOf(b.checker.GetTypeAtLocation(arg)))
		}
	}
	b.payload.Calls = append(b.payload.Calls, item)
	b.expression(node, "call")
}

func (b *factBuilder) expression(node *ast.Node, kind string) {
	b.payload.Expressions = append(b.payload.Expressions, factExpression{
		Start:  node.Pos(),
		Length: node.End() - node.Pos(),
		Kind:   kind,
		Type:   b.typeOf(b.checker.GetTypeAtLocation(node)),
	})
}

// typeOf 把上游类型压成类别。用标志位而不是类型字符串：字面量类型 42 的
// 字符串是 "42"，但它属于 number，标志位能直接回答这个问题。
func (b *factBuilder) typeOf(t *checker.Type) factType {
	if t == nil {
		return factType{Kind: "other"}
	}
	flags := t.Flags()
	switch {
	case flags&checker.TypeFlagsNumberLike != 0:
		return factType{Kind: "number"}
	case flags&checker.TypeFlagsBooleanLike != 0:
		return factType{Kind: "boolean"}
	case flags&checker.TypeFlagsStringLike != 0:
		return factType{Kind: "string"}
	case flags&checker.TypeFlagsVoid != 0:
		return factType{Kind: "void"}
	case flags&checker.TypeFlagsAnyOrUnknown != 0:
		return factType{Kind: "any"}
	default:
		return factType{Kind: "other"}
	}
}
