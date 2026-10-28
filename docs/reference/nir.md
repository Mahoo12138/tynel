# NIR 格式

NIR 是 Tynel 的中间表示，也是前端与后端之间唯一的交接物。前端把源码理解成这个结构，后端只读这个结构，双方不共享 tsgo 或 Go 的内部对象。它同时是文件格式：手写一份 JSON 就能驱动后端，不必先有 TypeScript 前端。

本页定义当前的格式、字段约束、读入预算和错误码。语义检查（函数是否唯一、调用实参是否匹配）不在格式层，由 01.04 的独立验证器负责。

## 为什么它长得像 C 结构体

NIR 的每个节点是一个带标签的联合体：先看 `op`，再由 `op` 决定哪些字段有意义。这和 C 里「一个 `kind` 字段加一个 `union`」是同一个思路——节点只有一种含义，但字段槽位共用。

JSON 没有联合类型，所有字段只能平铺在同一层。因此文件里会出现「某个 `op` 不该有的字段」，这类输入由解码器直接拒绝，而不是忽略。这样 `{"op":"number","name":"x"}` 不会悄悄变成一个既像字面量又像参数的东西。

## 顶层结构

```json
{
  "schemaVersion": 1,
  "entry": "main",
  "functions": [ ]
}
```

| 字段 | 含义 | 约束 |
| --- | --- | --- |
| `schemaVersion` | 文件格式版本 | 必须等于 1，其他值一律拒绝 |
| `entry` | 入口函数名 | 非空；是否真的存在由验证器检查 |
| `functions` | 函数列表 | 非空，数量受预算限制 |

## 函数、参数与位置

```json
{
  "name": "add",
  "params": [{"name": "a", "type": "number"}],
  "result": "number",
  "body": [ ],
  "pos": {"file": "examples/add.ts", "line": 1, "column": 1}
}
```

`result` 接受 `number`、`bool` 或 `void`；`params[].type` 接受 `number` 或 `bool`。`pos` 可以省略，手写 NIR 时通常不写；前端产出时填写，用于把后端的错误指回源码。参数表为空时，Go 的编码会写成 `null`；读入时 `null` 与 `[]` 等价，都表示没有参数。

## 语句

| `op` | 字段 | 含义 |
| --- | --- | --- |
| `return` | `value` 可选 | 返回一个表达式；`void` 函数可以不带值 |
| `print` | `value` 必填 | 输出一个表达式的结果 |
| `let` | `name`、`value` 必填，`mutable` 可选 | 定义一个局部值，供同一块内的后续语句按名字读取；`mutable` 标记它来自 `let` 还是 `const` |
| `discard` | `value` 必填 | 求值一个表达式并丢弃结果 |
| `if` | `cond` 必填，`then` / `else` 可选 | 条件为 `bool`；两个分支各自是一个语句块 |
| `short` | `name`、`operator`、`left`、`value` 必填，`right` 可选 | `&&` / `\|\|` 的短路求值，结果绑定到 `name`；`right` 是只在需要时执行的准备块 |
| `select` | `name`、`cond`、`thenValue`、`elseValue`、`type` 必填，`then` / `else` 可选 | 条件表达式的结果绑定；`type` 是 `number` 或 `bool`，两条分支必须与它一致 |

`let` 不是语言层面的变量，它解决的是求值次数：前端把模块级 `const`、分支里的 `const` 和需要复用的中间结果落成局部值，避免在表达式里重复求值——`const x = f(); g(x, x)` 只应该调用一次 `f`。

局部值的作用域是它所在的语句块及其子块。必须先用 `let` 定义、后按名字读取；同一个块里同名重复定义和先读后定义都由验证器拒绝（`nir.duplicate-name`、`nir.unknown-local`）。子块可以遮蔽父块的同名值，退出子块后父块的值重新可见。

`mutable: true` 表示这个局部值来自块内 `let`，后续允许写入；省略或 `false` 表示 `const`。这个字段只允许出现在 `let` 语句上，其他语句带它会报 `nir.unexpected-field`。声明身份与可变存储的区分见[声明身份与可变存储](mutability.md)。

`if` 的 `then` 与 `else` 各自开一个子作用域，两条分支里同名的 `let` 互不影响。`cond` 只求值一次，结果必须是 `bool`；`else` 里再放一条 `if` 就是 `else if`。`if` 本身不产生值，不能出现在表达式位置。

`discard` 对应源码里的表达式语句：`f();` 与 `answer;` 都是这个形状。它丢弃的是结果，不是求值——调用照常执行，副作用保留，值不打印。被丢弃的表达式可以是 `number`、`bool`，也可以是 `void` 调用；后者在 bridge 里直接作为语句，前两者需要一个承接结果的位置（见 [bridge 说明](bridge.md)）。循环的语句形式随后续节点加入。

分支写成嵌套的语句块：

```json
{ "op": "if",
  "cond": {"op": "param", "name": "flag"},
  "then": [{"op": "print", "value": {"op": "number", "bits": "0x3ff0000000000000"}}],
  "else": [{"op": "print", "value": {"op": "number", "bits": "0x4000000000000000"}}] }
```

`then` 或 `else` 为空表示空分支；`else` 为空与省略等价。两条路径在编译期都被检查、都被保留，运行时才按条件选择；图示见[分支的两条路径](branches.md)。

`short` 是短路求值的结果绑定：

```json
{ "op": "short", "name": "_tmp_1", "operator": "&&",
  "left": {"op": "param", "name": "a"},
  "right": [],
  "value": {"op": "param", "name": "b"} }
```

先执行 `left` 并赋给 `name`；`&&` 在 `name` 为真时执行 `right` 列表，再把 `value` 赋给 `name`；`||` 在 `name` 为假时执行。`right` 只接受 `let` 与 `short`，它的局部值只对 `value` 可见。`short` 的 `name` 在语句之后可见，类型是 `bool`。完整语义见[短路求值](short-circuit.md)。

`select` 是条件表达式的结果绑定：

```json
{ "op": "select", "name": "_tmp_1", "type": "number",
  "cond": {"op": "param", "name": "flag"},
  "then": [],
  "thenValue": {"op": "number", "bits": "0x3ff0000000000000"},
  "else": [],
  "elseValue": {"op": "number", "bits": "0x4000000000000000"} }
```

条件为真时执行 `then` 列表，在同一个子作用域里求值 `thenValue`；为假时走 `else`。`then` / `else` 只接受 `let`、`short` 与 `select`。`name` 在语句之后可见，类型由 `type` 声明，两条分支的类型必须与它一致。完整语义见[三元式](ternary.md)。

## 表达式

| `op` | 必填字段 | 含义 |
| --- | --- | --- |
| `number` | `bits` | binary64 字面量，用位模式表示 |
| `bool` | `value` | `true` 或 `false`；`false` 也必须有这个字段 |
| `param` | `name` | 引用当前函数的参数 |
| `local` | `name` | 引用前面 `let` 定义的局部值 |
| `call` | `function`，`args` 可选 | 直接调用具名函数 |
| `binary` | `operator`、`left`、`right` | 算术运算，运算符限 `+`、`-`、`*`、`/`，结果是 `number` |
| `compare` | `operator`、`left`、`right` | 比较运算，运算符限 `<`、`<=`、`>`、`>=`、`===`、`!==`，结果是 `bool` |
| `not` | `operand` | 布尔取反，操作数必须是 `bool` |

任何表达式都可以带 `pos`。不属于该 `op` 的字段一律拒绝。

`bool` 的 `value` 用指针保存，所以 `false` 与「字段缺失」是两种不同的输入：前者是合法值，后者按 `nir.missing` 拒绝。JSON 里写成 `{"op":"bool","value":false}`。

`compare` 的输入类型按运算符分两组：`<`、`<=`、`>`、`>=` 要求两个 `number`；`===`、`!==` 要求两侧同为 `number` 或同为 `bool`。宽松相等 `==`、`!=` 不是 NIR 运算符，混入文件按 `nir.bad-op` 拒绝。浮点比较的具体结果见[比较语义](comparison.md)。

## 数字为什么存位模式

`number` 不写 `40`，写 `"bits": "0x4044000000000000"`。原因不是喜欢十六进制，而是十进制文本存不住 JavaScript 数值的全部取值：

- 负零和正零在十进制里都写作 `0`，位模式不同（`0x8000000000000000` 与 `0x0000000000000000`）；
- NaN 不是一个数，没有十进制写法，`1/0` 之类的替代品还会被当成别的表达式；
- 正无穷和负无穷同样没有字面量；
- 往返一次十进制解析可能改变最后一位，位模式不会。

所以 NIR 把 binary64 的 64 位原样存成 16 位十六进制字符串。`nir.BitsOf` 与 `nir.Float64` 是这两个方向唯一的转换点，测试覆盖了负零、NaN（含载荷位）、正负无穷和最小非零数。

## 读入预算

| 限制 | 默认值 | 超出后的行为 |
| --- | --- | --- |
| `MaxBytes` | 1 MiB | 读取阶段就停止，不进入解析 |
| `MaxDepth` | 64 层 | 表达式嵌套或语句块嵌套到第 65 层时拒绝 |
| `MaxNodes` | 10000 个 | 统计函数、参数、语句和表达式总数 |
| `MaxFunctions` | 256 个 | 函数数量超限时拒绝 |

预算是格式的一部分：没有它，一份深层嵌套的输入可以让解码器在报错前先耗尽栈或内存。数值改动要同步本表。

## 语义规则

格式合法不等于能编译。`Verify` 在格式检查之后再看语义，规则如下：

| 检查 | 规则 |
| --- | --- |
| 入口 | `entry` 必须指向存在的函数，且该函数没有参数、返回 `void` |
| 声明唯一 | 函数名不得重复；同一函数内参数名不得重复；同一个块里 `let` 名不得重复，子块可以遮蔽父块 |
| 参数引用 | `param` 的名字必须是当前函数的参数 |
| 局部值 | `local` 必须在当前块或祖先块里由前面的 `let` 定义 |
| 调用 | 目标函数必须存在；实参个数等于形参个数；每个实参类型等于对应形参类型 |
| 类型 | 字面量是 `number` 或 `bool`；参数取它声明的类型；四则运算的结果是 `number`；`compare` 的结果是 `bool`，输入按运算符分组；`not` 的操作数与结果都是 `bool`；调用表达式的类型是被调函数的返回类型 |
| 分支 | `if` 的 `cond` 必须是 `bool`；`then` 与 `else` 各自在子作用域里检查 |
| 短路 | `short` 的 `left` 与 `value` 必须是 `bool`；`name` 在当前块里唯一，类型为 `bool`；`right` 只在需要时执行 |
| 三元式 | `select` 的 `cond` 必须是 `bool`；两条分支必须同类型，且与 `type` 一致；`name` 在当前块里唯一 |
| 返回值 | 返回 `number` 或 `bool` 的函数必须在每条路径上以带值的 `return` 结束，值类型与声明一致；`void` 函数的 `return` 不带值。`if` 只有两臂都存在且都结束时才算覆盖路径 |
| 输出 | `print` 只接受 `number` 或 `bool`，`void` 调用不能作为值使用 |
| 丢弃 | `discard` 的表达式必须成立，结果类型不限 |

验证器不导入 tsgo 或 Go 内部对象，也不假设模块来自 JSON：内存里直接构造的结构同样要过这一关。表达式和语句块都必须是树，共享节点和循环节点都被拒绝——循环结构会让任何遍历不终止，共享节点在写回文件时会被复制成两份。

## 为什么在生成机器码之前停止

以调用为例：`add` 需要两个实参，NIR 里只给了一个。这个错误在验证阶段就停下，后端不会看到一张参数数量对不上的图。

如果放到后面才发现，只有两种结局：后端在生成代码时崩溃，或者生成一段在运行时打印「编译器尚未实现」的分支。前者把内部结构不一致暴露成崩溃，后者把编译期错误拖到运行期，还伪装成能力不足。两者都让「源码错误」和「编译器缺陷」分不开，所以这类检查必须发生在 bridge 之前。

## 错误码

格式与语义错误都按 `源码错误` 类别报告（退出状态 1），因为它们是输入内容不合法；读写文件的失败属于 `环境缺失`（退出状态 4）。

| 代码 | 层 | 触发条件 |
| --- | --- | --- |
| `nir.version` | 格式 | `schemaVersion` 不是 1 |
| `nir.missing` | 格式 | 缺少必填字段 |
| `nir.bad-value` | 格式 | 字段值不在允许集合内，例如 `result` 为 `string` |
| `nir.bad-op` | 格式 | 未知的 `op` 或运算符 |
| `nir.unexpected-field` | 格式 | 当前 `op` 不接受该字段 |
| `nir.bad-bits` | 格式 | 位串不是 16 位十六进制 |
| `nir.budget` | 格式 | 超过任一读入预算 |
| `nir.malformed` | 格式 | JSON 无法解析，例如截断或类型不符 |
| `nir.trailing` | 格式 | JSON 文档结束后还有内容 |
| `nir.cycle` | 格式 | 同一表达式或语句节点被引用两次（共享或循环） |
| `nir.entry-missing` | 语义 | `entry` 指向的函数不存在 |
| `nir.entry-signature` | 语义 | 入口带参数或返回非 `void` |
| `nir.duplicate-name` | 语义 | 函数名或参数名重复 |
| `nir.unknown-param` | 语义 | 引用了当前函数不存在的参数 |
| `nir.unknown-function` | 语义 | 调用了不存在的函数 |
| `nir.arg-count` | 语义 | 实参个数与形参不符 |
| `nir.arg-type` | 语义 | 实参类型与形参不符 |
| `nir.result-type` | 语义 | 返回值与函数声明不符，`void` 函数带返回值，或 `select` 的两条分支类型不同、与 `type` 不一致 |
| `nir.print-type` | 语义 | `print` 的参数不是 `number` 或 `bool` |
| `nir.local-type` | 语义 | `let` 的值不是 `number` 或 `bool` |
| `nir.condition-type` | 语义 | `if` 的条件不是 `bool` |
| `nir.unknown-local` | 语义 | 引用了尚未定义的局部值，或局部值不在当前块及祖先块里 |
| `nir.operand-type` | 语义 | 运算符的操作数类型不符，例如 `not` 收到 `number`、有序比较收到 `bool`、`short` 的操作数不是 `bool` |
| `nir.missing-return` | 语义 | 返回 `number` 或 `bool` 的函数有路径没有 `return` |
| `nir.io` | 环境 | 打开、写入或改名失败 |

带源码位置的节点出错时，诊断会附上 `文件:行:列`。

任何错误都返回空模块，调用方拿不到半个结构。写文件先写同目录临时文件再改名，编码失败不会破坏已有文件。

## 示例

`examples/ir/add.nir.json` 描述 `add(40, 2)`：`add` 返回两个参数之和，`main` 调用它并打印结果。

```json
{
  "op": "print",
  "value": {
    "op": "call",
    "function": "add",
    "args": [
      {"op": "number", "bits": "0x4044000000000000"},
      {"op": "number", "bits": "0x4000000000000000"}
    ]
  }
}
```

`0x4044000000000000` 是 40，`0x4000000000000000` 是 2。这里只保存结构，不解算结果；真正相加发生在后端（01.05）。

boolean 的形状同样直接：

```json
{ "op": "print",
  "value": { "op": "not", "operand": { "op": "bool", "value": false } } }
```

这段输出 `true`。`false` 写在 `value` 里，不是省略字段；`not` 只接受 `bool`，不会把数字的 0 当成 false。

校验示例、格式规则与语义规则：

```zsh
# 跑 NIR 包的测试：往返一致、特殊值位模式、预算、错误码和验证器规则。
./tools/test
```

命令行入口是 `tynel build-ir`，见[命令行参考](cli.md)。
