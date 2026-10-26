# 从源码到 NIR

本页沿一条 `add` 调用走一遍三站：源码、事实、NIR。后端怎么把 NIR 变成机器码不在本页展开，见 [bridge 说明](bridge.md)。

## 同一条语句的三站

源码（`bridge/tsgo/testdata/add.ts`）：

```typescript
function add(a: number, b: number): number {
  return a + b;
}

const answer: number = add(40, 2);

console.log(answer);
```

事实（`consumer -facts` 的节选，省略了位置）：

```json
{ "id": 1, "kind": "function", "name": "add", "result": {"kind":"number"}, "parameters": [2, 3],
  "body": {"kind": "Block", "children": [
    {"kind": "ReturnStatement", "children": [
      {"kind": "BinaryExpression", "op": "+",
       "children": [{"kind":"Identifier","name":"a","target":2},
                    {"kind":"Identifier","name":"b","target":3}]}]}]}}

{ "id": 4, "kind": "const", "name": "answer", "type": {"kind":"number"},
  "body": {"kind": "CallExpression",
           "children": [{"kind":"Identifier","name":"add","target":1},
                        {"kind":"NumericLiteral","bits":"0x4044000000000000"},
                        {"kind":"NumericLiteral","bits":"0x4000000000000000"}]}}

"module": {"kind": "Block", "children": [
  {"kind": "Const", "decl": 4},
  {"kind": "ExpressionStatement", "children": [
    {"kind": "CallExpression", "children": [
      {"kind": "PropertyAccessExpression", "name": "log",
       "children": [{"kind":"Identifier","name":"console"}]},
      {"kind": "Identifier", "name": "answer", "target": 4}]}]}]}
```

NIR（`consumer -nir`，省略了位置）：

```json
{ "name": "add",
  "params": [{"name":"a","type":"number"}, {"name":"b","type":"number"}],
  "result": "number",
  "body": [{"op":"return","value":{"op":"binary","operator":"+",
            "left":{"op":"param","name":"a"},"right":{"op":"param","name":"b"}}}] }

{ "name": "main", "params": [], "result": "void",
  "body": [
    {"op":"let","name":"answer","value":{"op":"call","function":"add","args":[
      {"op":"number","bits":"0x4044000000000000"},
      {"op":"number","bits":"0x4000000000000000"}]}},
    {"op":"print","value":{"op":"local","name":"answer"}},
    {"op":"return"} ] }
```

三站之间各丢掉了什么，值得逐个说清楚：

- **事实保留名字与绑定**：`a` 是 ID 2 还是 3、`add` 指向哪个声明，都在事实里。NIR 只留函数名和参数名——同一份 NIR 里，`add` 的参数 `a` 与外层没有关系。
- **NIR 保留求值次数**：模块级 `const answer` 变成 `let`，`console.log` 变成 `print`。事实里的 `Const` 节点只是一个占位，真正的初始值挂在声明上。
- **两者都保留位模式**：`40` 在事实里就是 `0x4044000000000000`，不是十进制字符串。降低不再解析数字，只搬运位模式。

## 模块体变成入口函数

NIR 只有函数，没有顶层语句。降低因此造一个 `main`：

| 源码 | 降到 |
| --- | --- |
| 顶层 `const` | 按源码顺序生成 `let` |
| 顶层 `console.log(x)` | `print` |
| 函数声明 | 同名的 NIR 函数，按声明顺序排列 |
| 入口本身 | 追加在最后的 `main`，无参数、返回 `void` |

用户函数不能叫 `main`，否则与入口冲突，降低报 `lower.entry-conflict`。

## 求值顺序

`console.log(add(log(1), log(2)))` 必须按 `log(1)`、`log(2)`、`add` 的顺序执行。降低保留表达式树的结构，不改写顺序；bridge 把它变成 Go 的调用表达式，Go 的实参从左到右求值，顺序因此保住。`bridge/tsgo/testdata/lower/order.ts` 就是这条验收，输出 `1 2 3`。

`const` 绑定成 `let` 是为了求值次数：`const x = f(); g(x, x)` 里 `f` 只该调用一次，直接把 `x` 内联进两处会调用两次。只有在不绑定时才谈得上「顺序」，绑定之后次数也固定了。

## 一元负号

`-0` 在 JavaScript 里是负零，`0 - 0` 是正零，两者位模式不同。NIR 没有一元运算符，降低因此把 `-<字面量>` 直接折成位模式：`-0` 变成 `0x8000000000000000`，不经过运行时的取负。

这也限制了范围：一元负号只接受数字字面量。`-x` 需要一条取负指令，本阶段不提供，降低报 `lower.bad-unary`。

## 降低阶段拒绝什么

降低不重复做类型检查，但它不盲信事实。下面这些情况会在降低时报错，并且不返回模块：

| 错误码 | 触发条件 |
| --- | --- |
| `lower.bad-type` | 操作数、实参、返回值或 `let` 值的类型摘要不是 `number` |
| `lower.bad-arity` | 二元运算不是两个操作数、`return` 多个值、实参个数与形参不符 |
| `lower.bad-operator` | 运算符不在 `+ - * /`（一元 `- +`）之内 |
| `lower.bad-unary` | 一元负号作用在非字面量上 |
| `lower.unknown-name` | 引用的声明不存在，或参数不属于当前函数 |
| `lower.module-const-in-function` | 函数内引用模块级常量（需要全局变量，本阶段没有） |
| `lower.function-value` | 把函数当值使用 |
| `lower.unknown-callee` | 调用目标不是本文件里的函数 |
| `lower.entry-conflict` | 用户函数占用了 `main` |
| `lower.missing-return` | 返回 `number` 的函数没有以 `return` 结束 |
| `lower.unsupported-statement` | 函数体或模块体里出现不支持的语句 |
| `lower.unsupported-expr` | 不支持的表达式种类 |
| `lower.bad-literal` | 数字字面量缺少位模式或位模式非法 |

这些错误都是**内部故障**（退出 5），不是源码错误：走到降低说明源码检查与能力检查都已经通过，此时对不上只能是编译器的前后两段不一致。

## 降低在会话关闭之后

降低只吃两样东西：源码快照和事实。事实是普通数据，会话早已关闭，所以降低阶段不可能「顺手再问一次检查器」。这条约束让降低可以离线重放——把一份事实存下来，之后任何时候都能生成同样的 NIR。

## 降低不做什么

- 不做常量折叠（除了 `-字面量` 这种表示上的必要折叠）、不做公共子表达式消除、不做内联。
- 不做寄存器分配或指令选择，那是 bridge 和 Go 编译器的事。
- 不生成任何 Go 源码。NIR 是唯一产物。
