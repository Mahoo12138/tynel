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
| 顶层 `f();` / `answer;` | `discard` |
| 函数声明 | 同名的 NIR 函数，按声明顺序排列 |
| 入口本身 | 追加在最后的 `main`，无参数、返回 `void` |

用户函数不能叫 `main`，否则与入口冲突，降低报 `lower.entry-conflict`。

## 调用、丢弃与打印

源码里三种「一行只有一个表达式」的写法看起来很像，落到 NIR 是三件不同的事：

| 源码 | NIR | 结果去哪 |
| --- | --- | --- |
| `f();` | `discard` 调用 | 执行，结果丢弃 |
| `answer;` | `discard` 局部值 | 求值，结果丢弃，不打印 |
| `console.log(answer);` | `print` | 执行并写一行输出 |

`discard` 不是「什么都不做」。调用可能带副作用——`announce(7)` 里可能打印——所以求值必须发生；但表达式语句的语义是「值没人要」，因此不能顺手把它打印出去。`answer;` 就是最容易看出区别的例子：它求值成一个 number，然后丢掉，屏幕上什么都不该出现。

bridge 对这两种被丢弃的表达式处理不同：`void` 调用本身就是语句形状，直接放进函数体；`number` 表达式需要一个承接结果的位置，赋给一个从不读取的临时变量（详见 [bridge 说明](bridge.md)）。

## void 函数

`function announce(n: number): void { ... }` 降到同名的 `void` 函数。它有三种正常结束方式，都在降低阶段处理：

| 源码 | 降到 |
| --- | --- |
| `return;` | 空 `return` |
| 函数体最后一条语句之后自然结束 | 补一条空 `return` |
| 显式 `return value` | 拒绝：`lower.void-value` |

补空 `return` 不是装饰。Go 编译器把「没有函数体」当成外部声明，一条语句都没有的函数只生成符号、不生成代码，链接时会报 `function main is undeclared`——01.05 已经踩过这个坑。降低保证每个 `void` 函数至少有一条 `return`。

反过来，返回 `number` 的函数里写 `return;` 也是拒绝（`lower.missing-value`）。这两条 TypeScript 自己也会报错，降低再拦一次是因为它不假设事实一定来自刚跑过的检查器。

## 求值顺序

`console.log(add(log(1), log(2)))` 必须按 `log(1)`、`log(2)`、`add` 的顺序执行。降低保留表达式树的结构，不改写顺序；bridge 把它变成 Go 的调用表达式，Go 的实参从左到右求值，顺序因此保住。`bridge/tsgo/testdata/lower/order.ts` 就是这条验收，输出 `1 2 3`。

`const` 绑定成 `let` 是为了求值次数：`const x = f(); g(x, x)` 里 `f` 只该调用一次，直接把 `x` 内联进两处会调用两次。只有在不绑定时才谈得上「顺序」，绑定之后次数也固定了。

## 局部 let 与可变性

块内 `const` 和已初始化的 `let` 都降成 NIR 的 `let`，区别在 `mutable`：`const` 是 `false`（省略），`let` 是 `true`。降低器同时把声明 ID 和可变性写进当前作用域，后续赋值用它判断目标是否可写。

```json
{ "op": "let", "name": "x", "mutable": true,
  "value": {"op": "number", "bits": "0x3ff0000000000000"} }
```

局部值必须有初始值，因为 NIR 的 `let` 需要 `value`；能力检查在更早一步就拒绝未初始化的 `let`。模块级 `let` 和 `var` 不会进入降低阶段：能力检查先按 `cap.module-let` 或 `cap.unsupported-syntax` 拒绝。作用域用声明 ID 做键，块内同名 `let` 遮蔽外层时，两个声明各自有独立的 NIR 名字。

## 简单赋值

`x = value;` 是一条独立语句，降成 NIR 的 `set`：

```json
{ "op": "set", "name": "x", "value": {"op": "binary", "operator": "+", ...} }
```

降低器先查目标声明：必须是 `let`、在当前作用域里、且 `mutable` 为真；右值按目标类型检查后先降成表达式，`set` 放在右值的准备语句之后。`x = x + 1` 里的 `local x` 因此读到写入前的值，写完后再读才是新值。

赋值出现在表达式位置（`const y = (x = 1)`）由能力检查先拒绝；降低器也会报 `lower.assignment-value`，不把它悄悄降成“先写再取值”。复合赋值和自增自减不在降低器的运算符表里，能力检查会先给出更具体的错误码。

## 一元负号与布尔取反

`-0` 在 JavaScript 里是负零，`0 - 0` 是正零，两者位模式不同。NIR 没有一元运算符，降低因此把 `-<字面量>` 直接折成位模式：`-0` 变成 `0x8000000000000000`，不经过运行时的取负。

这也限制了范围：一元负号只接受数字字面量。`-x` 需要一条取负指令，本阶段不提供，降低报 `lower.bad-unary`。

`!` 不一样，它有对应的 NIR 操作：`!value` 降成 `not`，操作数与结果都是 `bool`。TypeScript 允许对数字取反（`!1` 是 `false`），但降低要求操作数的类型摘要是 `boolean`，否则报 `lower.bad-type`。

## 比较

比较运算降成 NIR 的 `compare`，结果是 `bool`。有序比较要求两侧类型摘要是 `number`；严格相等要求两侧同类型，且只能是 `number` 或 `boolean`。降低不改变操作数顺序，`mark(1) < mark(2)` 在 NIR 里仍是左 `mark(1)`、右 `mark(2)`，求值顺序由后端保证。

`==` 与 `!=` 不是 NIR 运算符，能力检查会先拒绝；如果事实绕过前端直接进来，降低按 `lower.bad-operator` 拒绝。浮点比较的结果见[比较语义](comparison.md)。

## 分支与局部值

`IfStatement` 降成 NIR 的 `if`：条件在**外层作用域**求值一次，`then` 与 `else` 各自降成一个语句块。不带花括号的单条语句也包成只有一个元素的块。`else if` 是 `else` 里再放一条 `if`，条件仍然在外层作用域求值；每条分支的语句块各自开一个子作用域。

函数体内的 `const`（包括分支里的）降成 NIR 的 `let`。骨架是 `VariableStatement → VariableDeclarationList → VariableDeclaration`，声明本身只带一个初始化表达式；本阶段一次只支持声明一个局部值。作用域用声明 ID 做键，沿父链查找：同一个块里重复定义报 `lower.duplicate-name`，子块可以遮蔽父块，退出子块后父块的值重新可见。

未选中的分支也照样降低。`if (false) { ... }` 不会在编译期被裁掉：能力检查已经遍历过它，降低也保留它的语句，运行时由 Go 的 `if` 决定是否执行。

独立语句块 `{ ... }`（不挂在 `if` 上）目前按 `lower.unsupported-statement` 拒绝：NIR 没有裸块语句，扁平化会丢掉作用域。嵌套块通过 `if` 的分支表达。

## 返回路径

有值函数降低后要过 `nir.BlockReturns`：一条 `return` 直接结束；`if` 只有两臂都存在且都结束时才算覆盖路径。缺路径时按 `lower.missing-return` 拒绝，位置来自 NIR 里那个 `if` 节点，而不是整个函数。

`return` 之后的语句仍然降低，NIR 保留它们，验证器也照样检查。bridge 在生成阶段遇到结束语句就停止，Go 编译器因此不会看到不可达代码。

`void` 函数正常结束：如果分支已经覆盖所有路径，不再补 `return`；否则补一条空 `return`，表示函数结束。

## 短路

`&&` 与 `||` 不降成纯表达式，而是降成 NIR 的 `short` 语句：左侧先赋给一个临时值，右侧的准备工作与右值放进 `short` 的 `right` 列表，只有左侧要求时才执行。表达式位置用 `local` 读取这个临时值，所以结果可以继续用在常量、条件、实参和返回值里。

降低器给表达式带一个准备语句列表。语句层先输出准备语句，再输出 `return` / `print` / `let` / `if`；`if` 的条件准备语句在 `if` 之前。调用实参按源码顺序处理：如果某个实参带准备语句，它之前的实参先物化成临时值，再执行准备语句，最后物化它自己。这样 `pair(mark(4, true), mark(5, true) && mark(6, false))` 的打印顺序仍是 `4`、`5`、`6`。

右侧块只允许 `let` 与 `short`，它的局部值只对右值可见。`short` 的结果名由降低器生成，形如 `_tmp_1`，不会与源码里的名字冲突。

## 三元式

`cond ? then : else` 降成 NIR 的 `select`：条件在外层求值一次；两条分支各自带准备语句列表与结果表达式，只有选中的一条执行。结果绑定到临时值，`type` 由分支类型决定，后续表达式用 `local` 读取。

两条分支的类型必须一致，且都是 `number` 或 `boolean`。分支准备块只允许 `let`、`short` 与 `select`，局部值只对对应的结果表达式可见。嵌套三元式的准备留在自己的分支里，不会提前执行。

## 降低阶段拒绝什么

降低不重复做类型检查，但它不盲信事实。下面这些情况会在降低时报错，并且不返回模块：

| 错误码 | 触发条件 |
| --- | --- |
| `lower.bad-type` | 操作数、实参、返回值、`let` 值或三元式分支的类型摘要与期望类型不符（`number` 或 `boolean`）；三元式两臂类型不同 |
| `lower.bad-arity` | 二元运算不是两个操作数、`return` 多个值、实参个数与形参不符 |
| `lower.bad-operator` | 运算符不在算术、一元 `- +`、`!`、比较与短路之内（包括宽松 `==` / `!=`） |
| `lower.bad-unary` | 一元负号作用在非字面量上 |
| `lower.unknown-name` | 引用的声明不存在、参数不属于当前函数，或局部值不在当前作用域 |
| `lower.duplicate-name` | 同一个块里重复定义局部值 |
| `lower.bad-declaration` | 局部声明缺少声明身份、不是 `const` / `let`，或一次声明多个值 |
| `lower.assign-target` | 赋值目标不是直接名字 |
| `lower.assign-immutable` | 赋值目标不是可变的 `let` |
| `lower.assignment-value` | 赋值出现在表达式里 |
| `lower.module-const-in-function` | 函数内引用模块级常量（需要全局变量，本阶段没有） |
| `lower.function-value` | 把函数当值使用 |
| `lower.unknown-callee` | 调用目标不是本文件里的函数 |
| `lower.entry-conflict` | 用户函数占用了 `main` |
| `lower.missing-return` | 返回 `number` 或 `boolean` 的函数有路径没有 `return` |
| `lower.missing-value` | 返回 `number` 或 `boolean` 的函数里 `return` 不带值，或局部值缺少初始值 |
| `lower.void-value` | `void` 函数的 `return` 带返回值 |
| `lower.unsupported-statement` | 函数体或模块体里出现不支持的语句（包括独立语句块） |
| `lower.unsupported-expr` | 不支持的表达式种类 |
| `lower.bad-literal` | 数字字面量缺少位模式或位模式非法 |

这些错误都是**内部故障**（退出 5），不是源码错误：走到降低说明源码检查与能力检查都已经通过，此时对不上只能是编译器的前后两段不一致。

## 降低在会话关闭之后

降低只吃两样东西：源码快照和事实。事实是普通数据，会话早已关闭，所以降低阶段不可能「顺手再问一次检查器」。这条约束让降低可以离线重放——把一份事实存下来，之后任何时候都能生成同样的 NIR。

## 降低不做什么

- 不做常量折叠（除了 `-字面量` 这种表示上的必要折叠）、不做公共子表达式消除、不做内联。
- 不做寄存器分配或指令选择，那是 bridge 和 Go 编译器的事。
- 不生成任何 Go 源码。NIR 是唯一产物。
