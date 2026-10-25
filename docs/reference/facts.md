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

只看名字，两个 `a` 没法区分；只看位置，`return a + b` 里的 `a` 是「第 166 个字节」，这说的是**某一次出现**，不是**它绑定到哪个声明**。降低阶段要回答的是后者：这个 `a` 该读哪个参数槽。

事实里两者都记：

```json
{ "id": 2, "kind": "function", "name": "add",   "start": 111, "result": {"kind":"number"}, "parameters": [3, 4] }
{ "id": 3, "kind": "parameter", "name": "a",    "start": 126, "type": {"kind":"number"} }
{ "id": 4, "kind": "parameter", "name": "b",    "start": 136, "type": {"kind":"number"} }
{ "id": 5, "kind": "function", "name": "scale", "start": 175, "result": {"kind":"number"}, "parameters": [6] }
{ "id": 6, "kind": "parameter", "name": "a",    "start": 264, "type": {"kind":"number"} }

{ "name": "a", "start": 166, "length": 2, "target": 3 }
{ "name": "a", "start": 293, "length": 2, "target": 6 }
```

两个 `a` 的声明 ID 是 3 和 6，两处引用各自指向自己的那个。名字相同、位置不同、ID 不同——这三件事同时成立，事实才够用。

同一个文件里还有一次前向调用：`add(40, 2)` 出现在 `add` 的声明之前。函数声明会提升，所以这次调用的 `callee` 是 2，指向后面那个声明：

```json
{ "start": 99, "length": 11, "callee": 2,
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
| `declarations` | 顶层函数、它们的参数、顶层 const |
| `references` | 函数体与初始化表达式里的每一次名称出现，带解析到的声明 ID |
| `calls` | 调用位置、被调用者 ID、实参类型、结果类型 |
| `expressions` | 数字字面量、二元运算、调用和标识符，各带类型摘要 |

几个字段的语义要说清楚：

- **`start` / `length` 是声明或表达式的字节区间**。函数声明覆盖整个 `function … }`；参数覆盖 `a: number`（含类型注解）；引用覆盖标识符本身（`a`，长度 2）。
- **`target` 和 `callee` 为 0 表示没有解析到本文件内的声明**，例如 `console`。它们不是错误。
- **类型注解里的名字不算引用**。`a: number` 的 `number` 是类型名，不是变量，遍历时不进类型节点。

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

语句结构、控制流、字符串与对象类型都不在事实里。`return`、`print`、`if` 的形态要等 02.04 与 02.05 扩展；到那时加字段，`Decode` 的严格解码会逼着适配层和本包一起改，不会出现一边加了字段另一边悄悄忽略的情况。
