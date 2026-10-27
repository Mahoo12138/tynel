# 声明身份与事实

本页说明编译器从 TypeScript 前端拿到什么：声明、引用、调用和表达式类型，以及为什么每条声明需要一个 ID。

## 两个 a 到底指向谁

`bridge/tsgo/testdata/facts.ts` 里有两个函数，各有一个叫 `a` 的参数：

```typescript
const answer: number = add(40, 2);

function add(a: number, b: number): number {
  return a + b;
}

function scale(a: number): number {
  return a * 2;
}

const doubled: number = scale(answer);
```

只看名字，两个 `a` 没法区分；只看位置，`return a + b` 里的 `a` 是「第 167 个字节」，这说的是**某一次出现**，不是**它绑定到哪个声明**。降低阶段要回答的是后者：这个 `a` 该读哪个参数槽。

事实里两者都记：

```json
{ "id": 2, "kind": "function", "name": "add",   "start": 113, "result": {"kind":"number"}, "parameters": [3, 4] }
{ "id": 3, "kind": "parameter", "name": "a",    "start": 126, "type": {"kind":"number"} }
{ "id": 4, "kind": "parameter", "name": "b",    "start": 137, "type": {"kind":"number"} }
{ "id": 5, "kind": "function", "name": "scale", "start": 249, "result": {"kind":"number"}, "parameters": [6] }
{ "id": 6, "kind": "parameter", "name": "a",    "start": 264, "type": {"kind":"number"} }

{ "name": "a", "start": 167, "length": 2, "target": 3 }
{ "name": "a", "start": 294, "length": 2, "target": 6 }
```

两个 `a` 的声明 ID 是 3 和 6，两处引用各自指向自己的那个。名字相同、位置不同、ID 不同——这三件事同时成立，事实才够用。

同一个文件里还有一次前向调用：`add(40, 2)` 出现在 `add` 的声明之前。函数声明会提升，所以这次调用的 `callee` 是 2，指向后面那个声明：

```json
{ "start": 100, "length": 10, "callee": 2,
  "arguments": [{"kind":"number"}, {"kind":"number"}],
  "result": {"kind":"number"} }
```

## 声明 ID 是什么

- 由遍历顺序分配，从 1 开始，`0` 表示「没有解析到本文件内的声明」。
- 只在一次检查内有效。两次检查跑同一份文件，ID 可能不同——它标识的是这次会话里的声明，不是文件里的字符位置。
- 同名不同声明一定不同 ID；同一符号的多处引用一定同 ID。

需要跨运行稳定的身份时，用「名字 + 声明起点」，不要用 ID。

## 事实里有什么

| 部分 | 内容 |
| --- | --- |
| `declarations` | 顶层函数、它们的参数、顶层 const、函数体内的局部 const |
| `references` | 函数体与初始化表达式里的每一次名称出现，带解析到的声明 ID |
| `calls` | 函数体与初始化表达式里的调用位置、被调用者 ID、实参类型、结果类型 |
| `expressions` | 数字字面量、布尔字面量、二元运算、调用和标识符，各带类型摘要 |
| `body` | 函数体或初始化表达式的语法骨架（见下） |

几个字段的语义要说清楚：

- **`start` / `length` 是声明或表达式的字节区间**。函数声明覆盖整个 `function … }`；参数覆盖 `a: number`（含类型注解）；引用覆盖标识符本身（`a`，长度 2）。`start` 是第一个有效记号的位置，不含它前面的空白和注释——报错要指向 `if`，不能指到上一行末尾的换行符。
- **`target` 和 `callee` 为 0 表示没有解析到本文件内的声明**，例如 `console`。它们不是错误。
- **类型注解里的名字不算引用**。`a: number` 的 `number` 是类型名，不是变量，遍历时不进类型节点。

## 语法骨架

每个函数和 const 还带一棵 `body`：函数体是 `Block`，const 是初始化表达式。节点形状固定：

```json
{ "kind": "ReturnStatement", "start": 160, "length": 13,
  "children": [
    { "kind": "BinaryExpression", "op": "+", "start": 167, "length": 5,
      "type": {"kind":"number"},
      "children": [
        { "kind": "Identifier", "name": "a", "target": 3, "start": 167, "length": 1 },
        { "kind": "Identifier", "name": "b", "target": 4, "start": 171, "length": 1 }
      ] }
  ] }
```

- `kind` 是上游的语法种类名，去掉 `Kind` 前缀。不认识的种类由能力检查统一拒绝。
- `op` 只出现在二元与一元运算上，是符号文本（`+`、`-`、`<`、`===`），不是 token 名。
- `name` 与 `target` 只出现在标识符和属性访问上；属性访问的 `name` 是属性名，`children` 只有对象那一侧。
- 树里只有语句、表达式和变量声明。类型节点（`a: number` 的 `number`）、修饰符、名字令牌都不进树——否则类型名会被当成变量引用。
- `true` 与 `false` 是 `TrueKeyword` / `FalseKeyword` 节点，类型摘要为 `boolean`。它们没有额外字段，种类本身就区分两个值。
- 局部 `const` 的 `VariableDeclaration` 带 `decl`（指向 `kind=local` 的声明），`children` 只有初始化表达式。`let` / `var` 不登记声明，`decl` 为 0，能力检查据此拒绝。
- `IfStatement` 的 `children` 依次是条件、`then` 语句、可选的 `else` 语句；`else if` 是 `else` 位置上的另一个 `IfStatement`。

校验对骨架有预算：深度不超过 100 层、节点不超过 20000 个。超了按内部故障拒绝，避免一段异常深的输入把后续阶段拖垮。

## 类型摘要

类型只有类别，没有人类可读的字符串：

| 类别 | 含义 |
| --- | --- |
| `number` | 包括字面量类型 `42` |
| `boolean` | 包括 `true` / `false` |
| `string` | 包括字符串字面量 |
| `void` | 无返回值 |
| `any` | `any` 与 `unknown` |
| `other` | 其余类型，本阶段不处理 |

用类别而不是字符串，是因为检查器给出的字符串是给人看的：`const answer = 42` 的类型字符串是 `"42"`，它属于 `number`；`number | undefined` 这种联合类型更没法用字符串判断。类别来自类型标志位，是检查器已经算好的结论。

## 校验做什么

事实由适配层产出、由 `compiler/frontend` 解码校验。校验不重新做类型检查，只回答「这份数据自己讲得通吗」：

| 检查 | 规则 | 错误码 |
| --- | --- | --- |
| 版本 | `schemaVersion` 必须是 1 | `facts.schema` |
| 会话 | `session` 与 `file` 不能为空 | `facts.no-session` / `facts.no-file` |
| ID | 正数且不重复 | `facts.bad-id` / `facts.duplicate-id` |
| 种类 | 只能是 function / parameter / const | `facts.bad-decl-kind` |
| 类型摘要 | 类别必须在枚举内；函数有返回类型、参数与 const 有类型 | `facts.bad-type` |
| 参数归属 | 每个参数恰好属于一个函数 | `facts.unknown-target` / `facts.shared-parameter` / `facts.orphan-parameter` |
| 引用 | `target` 为 0 或指向存在的声明 | `facts.unknown-target` |
| 调用 | 被调用者是函数；实参个数与形参一致；实参类型与形参一致；结果类型与声明一致 | `facts.call-target` / `facts.arg-count` / `facts.arg-type` / `facts.result-type` |

最后一条是「错误返回类型摘要」的拦截点：如果适配层把 `add` 的结果记成 `void`，校验会在调用处发现摘要与函数声明对不上，而不是让降低阶段生成一个返回 void 的调用。

事实不一致属于**内部故障**（退出 5），不是源码错误。用户写错代码时前端会给诊断；事实自相矛盾说明编译器自己出了问题。

## 会话身份与纯数据

每条事实带一个随机的 `session` 标识。两次检查的声明 ID 各自从 1 开始，把两份事实拼在一起会出现「两个 ID 3 指向不同声明」，`Combine` 因此拒绝不同会话或不同文件的合并。

事实里没有上游的 `*ast.Node`、`*ast.Symbol`、`*checker.Type`。这不只是约定：`TestFactsHoldOnlyPlainData` 用反射走一遍结构体，要求每个字段都是基本类型、本包定义的结构体，或它们组成的切片；指针只允许指向本包的结构体。加了上游字段，这条测试会直接失败。

代价是适配层要把类型压成类别、把符号压成 ID。收益是会话关掉之后事实仍然可读，也能直接写进文件——`consumer -facts` 就是把它打印出来。

## 目前不包含什么

控制流、对象、字符串这些写法**会出现在骨架里**，但只作为「不认识或不允许的种类」——能力检查看到它们就拒绝，降低阶段也不会处理。事实描述的是源码的形状，不是编译器支持的范围；「支持什么」由[原生能力白名单](capability.md)定义。

降低阶段需要的常量值、const 折叠结果还没有单独的字段，等 02.05 加。到那时加字段，`Decode` 的严格解码会逼着适配层和本包一起改，不会出现一边加了字段另一边悄悄忽略的情况。
