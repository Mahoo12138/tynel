// 本文件属于 tyneladapter 包，和 adapter.go 一起被复制进上游源码树。
//
// 它把检查器里的信息摊平成普通数据：声明、引用、调用和表达式类型。上游的
// AST、Symbol、Type 指针一个都不出去——调用方拿到的只有 ID、名字、位置和
// 类型摘要，所以会话关掉之后这些数据仍然可读。
package tyneladapter

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/scanner"
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
	Body       *factNode `json:"body,omitempty"`
	Mutable    bool      `json:"mutable,omitempty"`
}

// factNode 是函数体或初始化表达式的语法骨架。
//
// Kind 用上游的语法种类名（ReturnStatement、BinaryExpression…），调用方按
// 名字判断支持与否。只保留语句、表达式和变量声明这三类结构，类型节点、
// 修饰符、名字令牌都不进树——它们不参与能力判断。
type factNode struct {
	Kind     string     `json:"kind"`
	Start    int        `json:"start"`
	Length   int        `json:"length"`
	Op       string     `json:"op,omitempty"`
	Name     string     `json:"name,omitempty"`
	Target   int        `json:"target,omitempty"`
	Decl     int        `json:"decl,omitempty"`
	Bits     string     `json:"bits,omitempty"`
	Type     *factType  `json:"type,omitempty"`
	Children []factNode `json:"children,omitempty"`
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
	Module        *factNode         `json:"module,omitempty"`
	Declarations  []factDeclaration `json:"declarations"`
	References    []factReference   `json:"references"`
	Calls         []factCall        `json:"calls"`
	Expressions   []factExpression  `json:"expressions"`
}

// Facts 把这次检查的事实导出为 JSON。
//
// 事实覆盖顶层函数与 const，以及函数体内的局部 const：函数签名、参数、
// 每个声明的身份与类型，函数体与初始化表达式里的名称引用、调用目标和表达式
// 类型。局部 const 用 kind=local 记录，作用域由降低阶段按语句块重建。
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
		file:          s.file,
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
	if b.err != nil {
		return nil, b.err
	}
	return json.MarshalIndent(b.payload, "", "  ")
}

type factBuilder struct {
	err           error
	file          *ast.SourceFile
	checker       *checker.Checker
	ids           map[*ast.Symbol]int
	declNameNodes map[*ast.Node]bool
	nextID        int
	payload       factsPayload
}

// startOf 返回节点第一个有效记号的位置，跳过前导空白与注释。
//
// 上游的 Node.Pos() 是「完整起点」，包含节点之前的空白和注释。把它直接当成
// 源码位置，报错会指到上一行末尾的换行符，函数声明也会把头顶的注释算进自己
// 的范围。位置要回答「哪个字符」，所以用记号的起点。
func (b *factBuilder) startOf(node *ast.Node) int {
	return scanner.GetTokenPosOfNode(node, b.file, false)
}

// declare 给本文件里的声明分配 ID。同一个符号在多次引用中拿到同一个 ID，
// 不同声明拿到不同 ID——两个函数里各有一个 a，它们是两个符号，也就是两个 ID。
func (b *factBuilder) declare(sym *ast.Symbol) int {
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

// lookup 只查已经声明过的符号。库里的 Math、console 也能解析到符号，但它们
// 不是本文件里的声明，不能因此拿到一个没有对应声明的 ID——那会让事实校验
// 报「引用指向不存在的声明」。
func (b *factBuilder) lookup(sym *ast.Symbol) int {
	if sym == nil {
		return 0
	}
	return b.ids[sym]
}

// deferredNode 是模块级语句在 Children 里的占位。
type deferredNode struct {
	index int
	node  *ast.Node
}

// pending 记录一个已经登记、但函数体还没处理的声明。
type pending struct {
	index  int
	node   *ast.Node
	isFunc bool
}

// collect 分两趟：先登记所有顶层声明，再遍历函数体与初始化表达式。
//
// 顺序不能反过来。函数声明会提升，调用可以出现在声明之前；如果边登记边遍历，
// 前面那次调用查不到后面的函数，callee 就成了 0。
func (b *factBuilder) collect(file *ast.SourceFile) {
	var queue []pending
	var deferred []deferredNode
	module := &factNode{Kind: "Block", Start: 0, Length: len(file.Text())}
	for _, stmt := range file.Statements.Nodes {
		switch {
		case ast.IsFunctionDeclaration(stmt):
			queue = append(queue, b.declareFunction(stmt))
		case ast.IsVariableStatement(stmt):
			list := stmt.AsVariableStatement().DeclarationList.AsVariableDeclarationList()
			constDecl := list.Flags&ast.NodeFlagsConst != 0
			for _, decl := range list.Declarations.Nodes {
				item := b.declareVariable(decl)
				queue = append(queue, item)
				if constDecl {
					// 顶层 const 在模块体里占一个位置，降低阶段按顺序求值。
					module.Children = append(module.Children, factNode{
						Kind:   "Const",
						Start:  b.startOf(decl),
						Length: decl.End() - b.startOf(decl),
						Decl:   b.payload.Declarations[item.index].ID,
					})
				} else {
					// let/var 不在子集内，保留原种类让能力检查拒绝。
					module.Children = append(module.Children, factNode{
						Kind:   "VariableStatement",
						Start:  b.startOf(stmt),
						Length: stmt.End() - b.startOf(stmt),
						Decl:   b.payload.Declarations[item.index].ID,
					})
				}
			}
		default:
			// 模块级表达式要等所有声明登记完再转：它可能调用后面才声明的函数。
			// 先占位，保持源码顺序。
			index := len(module.Children)
			module.Children = append(module.Children, factNode{})
			deferred = append(deferred, deferredNode{index: index, node: stmt})
		}
	}
	for _, item := range deferred {
		if node := b.bodyNode(item.node); node != nil {
			module.Children[item.index] = *node
		}
	}
	b.payload.Module = module
	for _, item := range queue {
		if item.isFunc {
			b.fillFunction(item)
		} else {
			b.fillVariable(item)
		}
	}
}

// declareFunction 只登记签名与参数，不碰函数体。
func (b *factBuilder) declareFunction(node *ast.Node) pending {
	fn := node.AsFunctionDeclaration()
	name := fn.Name()
	decl := factDeclaration{
		ID:     b.declare(b.checker.GetSymbolAtLocation(name)),
		Kind:   "function",
		Name:   name.Text(),
		Start:  b.startOf(node),
		Length: node.End() - b.startOf(node),
	}
	b.declNameNodes[name] = true

	// 返回类型从调用签名取，而不是从源码里的类型注解文本取：注解可能缺失，
	// 而签名是检查器算出来的结果。
	if sigs := b.checker.GetSignaturesOfType(b.checker.GetTypeAtLocation(node), checker.SignatureKindCall); len(sigs) > 0 {
		result := b.typeOf(b.checker.GetReturnTypeOfSignature(sigs[0]))
		decl.Result = &result
	}

	// 先把参数的 ID 算出来，再按「函数在前、参数在后」的顺序写入，读起来的
	// 顺序与源码一致。
	var params []factDeclaration
	for _, param := range node.Parameters() {
		paramName := param.Name()
		id := b.declare(b.checker.GetSymbolAtLocation(paramName))
		decl.Parameters = append(decl.Parameters, id)
		typ := b.typeOf(b.checker.GetTypeAtLocation(paramName))
		params = append(params, factDeclaration{
			ID:     id,
			Kind:   "parameter",
			Name:   paramName.Text(),
			Start:  b.startOf(param),
			Length: param.End() - b.startOf(param),
			Type:   &typ,
		})
		b.declNameNodes[paramName] = true
	}

	b.payload.Declarations = append(b.payload.Declarations, decl)
	b.payload.Declarations = append(b.payload.Declarations, params...)
	return pending{index: len(b.payload.Declarations) - len(params) - 1, node: node, isFunc: true}
}

// fillFunction 在全部声明登记完之后再处理函数体。
func (b *factBuilder) fillFunction(item pending) {
	body := item.node.Body()
	if body != nil {
		// 先登记局部 const，再转骨架：VariableDeclaration 节点要靠这些 ID
		// 指回声明，引用也要能查到同一个 ID。
		b.declareLocals(body)
	}
	b.payload.Declarations[item.index].Body = b.bodyNode(body)
	if body != nil {
		b.walk(body)
	}
}

// declareLocals 登记函数体里的局部 const。let/var 不登记，骨架里它们的
// Decl 保持 0，能力检查据此拒绝。
func (b *factBuilder) declareLocals(node *ast.Node) {
	if node == nil {
		return
	}
	if ast.IsVariableDeclaration(node) && (ast.IsVarConst(node) || ast.IsVarLet(node)) {
		if name := node.Name(); name != nil && ast.IsIdentifier(name) {
			typ := b.typeOf(b.checker.GetTypeAtLocation(name))
			id := b.declare(b.checker.GetSymbolAtLocation(name))
			kind := "local"
			mutable := false
			if ast.IsVarLet(node) {
				kind = "let"
				mutable = true
			}
			b.payload.Declarations = append(b.payload.Declarations, factDeclaration{
				ID:      id,
				Kind:    kind,
				Name:    name.Text(),
				Start:   b.startOf(node),
				Length:  node.End() - b.startOf(node),
				Type:    &typ,
				Mutable: mutable,
			})
			b.declNameNodes[name] = true
		}
	}
	node.ForEachChild(func(child *ast.Node) bool {
		b.declareLocals(child)
		return false
	})
}

func (b *factBuilder) declareVariable(node *ast.Node) pending {
	decl := node.AsVariableDeclaration()
	name := decl.Name()
	typ := b.typeOf(b.checker.GetTypeAtLocation(name))
	kind := "const"
	mutable := false
	switch {
	case ast.IsVarLet(node):
		kind = "module-let"
		mutable = true
	case !ast.IsVarConst(node):
		kind = "var"
		mutable = true
	}
	b.payload.Declarations = append(b.payload.Declarations, factDeclaration{
		ID:      b.declare(b.checker.GetSymbolAtLocation(name)),
		Kind:    kind,
		Name:    name.Text(),
		Start:   b.startOf(node),
		Length:  node.End() - b.startOf(node),
		Type:    &typ,
		Mutable: mutable,
	})
	b.declNameNodes[name] = true
	return pending{index: len(b.payload.Declarations) - 1, node: node, isFunc: false}
}

func (b *factBuilder) fillVariable(item pending) {
	decl := item.node.AsVariableDeclaration()
	if decl.Initializer == nil {
		return
	}
	b.payload.Declarations[item.index].Body = b.bodyNode(decl.Initializer)
	b.walk(decl.Initializer)
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
	case ast.IsBooleanLiteral(node):
		b.expression(node, "boolean")
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
		Start:  b.startOf(node),
		Length: node.End() - b.startOf(node),
		Target: b.lookup(b.checker.GetSymbolAtLocation(node)),
	})
}

func (b *factBuilder) call(node *ast.Node) {
	call := node.AsCallExpression()
	callee := 0
	if ast.IsIdentifier(call.Expression) {
		callee = b.lookup(b.checker.GetSymbolAtLocation(call.Expression))
	}
	item := factCall{
		Start:  b.startOf(node),
		Length: node.End() - b.startOf(node),
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
		Start:  b.startOf(node),
		Length: node.End() - b.startOf(node),
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
	if flags&checker.TypeFlagsUnion != 0 {
		// 条件表达式常见 10 | 20 这种字面量联合。所有成员同属 number 或
		// boolean 时，结果类别就是该标量；混合联合仍然是 other。
		members := t.AsUnionOrIntersectionType().Types()
		allNumber, allBool := len(members) > 0, len(members) > 0
		for _, member := range members {
			mf := member.Flags()
			if mf&checker.TypeFlagsNumberLike == 0 {
				allNumber = false
			}
			if mf&checker.TypeFlagsBooleanLike == 0 {
				allBool = false
			}
		}
		if allNumber {
			return factType{Kind: "number"}
		}
		if allBool {
			return factType{Kind: "boolean"}
		}
		return factType{Kind: "other"}
	}
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

// bodyNode 把一个语句或表达式转成语法骨架。
//
// 递归时只保留语句、表达式和变量声明：类型节点（a: number 的 number）、
// 修饰符和名字令牌都被跳过，所以树里不会出现类型名冒充的引用。
func (b *factBuilder) bodyNode(node *ast.Node) *factNode {
	if node == nil {
		return nil
	}
	out := &factNode{
		Kind:   kindName(node.Kind),
		Start:  b.startOf(node),
		Length: node.End() - b.startOf(node),
	}
	if ast.IsExpression(node) {
		typ := b.typeOf(b.checker.GetTypeAtLocation(node))
		out.Type = &typ
	}
	switch {
	case ast.IsParenthesizedExpression(node):
		// 括号不改变求值顺序，也不该在骨架里占一层；直接返回里面的表达式。
		return b.bodyNode(node.AsParenthesizedExpression().Expression)
	case ast.IsIdentifier(node):
		out.Name = node.Text()
		if !b.declNameNodes[node] {
			out.Target = b.lookup(b.checker.GetSymbolAtLocation(node))
		}
	case ast.IsBinaryExpression(node):
		if token := node.AsBinaryExpression().OperatorToken; token != nil {
			out.Op = operatorText(token.Kind)
		}
	case ast.IsPrefixUnaryExpression(node):
		out.Op = operatorText(node.AsPrefixUnaryExpression().Operator)
	case ast.IsPropertyAccessExpression(node):
		// 只收对象那一侧。属性名本身也是 Identifier，通用递归会把它一起收进来，
		// 让 console.log 看起来有两个子节点。
		access := node.AsPropertyAccessExpression()
		if name := access.Name(); name != nil {
			out.Name = name.Text()
		}
		if access.Expression != nil {
			if child := b.bodyNode(access.Expression); child != nil {
				out.Children = append(out.Children, *child)
			}
		}
		return out
	case ast.IsNumericLiteral(node):
		bits, err := numericBits(node.Text())
		if err != nil {
			b.err = fmt.Errorf("数字字面量 %q 无法解析：%w", node.Text(), err)
			return out
		}
		out.Bits = fmt.Sprintf("0x%016x", bits)
	case ast.IsVariableDeclaration(node):
		if name := node.Name(); name != nil {
			b.declNameNodes[name] = true
			out.Decl = b.lookup(b.checker.GetSymbolAtLocation(name))
		}
		// 只收初始化表达式。名字本身是声明，不是引用；把它留在 Children 里
		// 会让降低阶段多出一个无意义的标识符。
		if init := node.AsVariableDeclaration().Initializer; init != nil {
			if child := b.bodyNode(init); child != nil {
				out.Children = append(out.Children, *child)
			}
		}
		return out
	}
	node.ForEachChild(func(child *ast.Node) bool {
		if !isStructure(child) {
			return false
		}
		if c := b.bodyNode(child); c != nil {
			out.Children = append(out.Children, *c)
		}
		return false
	})
	return out
}

// isStructure 报告一个子节点是否属于语法骨架。
func isStructure(node *ast.Node) bool {
	if ast.IsStatement(node) || ast.IsExpression(node) {
		return true
	}
	switch node.Kind {
	case ast.KindVariableDeclaration, ast.KindVariableDeclarationList:
		return true
	}
	return false
}

// kindName 去掉 stringer 生成的 Kind 前缀：KindBlock 变成 Block。
func kindName(k ast.Kind) string {
	return strings.TrimPrefix(k.String(), "Kind")
}

// operatorText 把运算符 token 换成符号文本。
//
// 不能对 token 调 Text()：上游对 *ast.Token 没有实现这条路径，会 panic。
// 未知的运算符保留种类名，能力检查会按「不在支持范围内」拒绝。
func operatorText(k ast.Kind) string {
	switch k {
	case ast.KindPlusToken:
		return "+"
	case ast.KindMinusToken:
		return "-"
	case ast.KindAsteriskToken:
		return "*"
	case ast.KindSlashToken:
		return "/"
	case ast.KindPercentToken:
		return "%"
	case ast.KindAsteriskAsteriskToken:
		return "**"
	case ast.KindExclamationToken:
		return "!"
	case ast.KindLessThanToken:
		return "<"
	case ast.KindLessThanEqualsToken:
		return "<="
	case ast.KindGreaterThanToken:
		return ">"
	case ast.KindGreaterThanEqualsToken:
		return ">="
	case ast.KindEqualsEqualsToken:
		return "=="
	case ast.KindEqualsEqualsEqualsToken:
		return "==="
	case ast.KindExclamationEqualsToken:
		return "!="
	case ast.KindExclamationEqualsEqualsToken:
		return "!=="
	case ast.KindAmpersandAmpersandToken:
		return "&&"
	case ast.KindBarBarToken:
		return "||"
	case ast.KindQuestionQuestionToken:
		return "??"
	case ast.KindAmpersandToken:
		return "&"
	case ast.KindBarToken:
		return "|"
	case ast.KindCaretToken:
		return "^"
	}
	return kindName(k)
}

// numericBits 把 TypeScript 的数字字面量文本转成 binary64 位模式。
//
// 只在字面量上做这件事：值就写在源码里，按字面量的写法解析不会丢信息。
// 类型字符串不这样处理——那需要理解类型语义，本阶段不做。
func numericBits(text string) (uint64, error) {
	s := strings.ReplaceAll(text, "_", "")
	lower := strings.ToLower(s)
	base := 10
	switch {
	case strings.HasPrefix(lower, "0x"):
		base, s = 16, s[2:]
	case strings.HasPrefix(lower, "0b"):
		base, s = 2, s[2:]
	case strings.HasPrefix(lower, "0o"):
		base, s = 8, s[2:]
	}
	if base != 10 {
		v, err := strconv.ParseUint(s, base, 64)
		if err != nil {
			return 0, err
		}
		return math.Float64bits(float64(v)), nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return math.Float64bits(v), nil
}
