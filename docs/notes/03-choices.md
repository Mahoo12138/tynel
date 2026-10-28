# 让程序自己选一条路

前两章的程序都只有一条直线：读入、相加、打印。这一章加入 `boolean`、比较、`if`、短路和三元式，程序开始按运行时的值走不同的路。编译链路没有变，还是源码快照、事实、能力检查、降低、NIR 验证、Go 后端；变的是 NIR 里多了几种语句，以及“一个表达式的结果”不再总能直接塞进另一个表达式。

我先把这一章要用的例子放在 `examples/choices.ts`：

```typescript
// 选择：比较、分支、短路和三元式挑出一个值。
function pick(n: number): number {
  if (n < 0) {
    return -1;
  }
  return n === 0 ? 0 : 1;
}

function mark(n: number, value: boolean): boolean {
  console.log(n);
  return value;
}

console.log(pick(-5));
console.log(pick(0));
console.log(pick(7));
console.log(mark(1, false) && mark(2, true));
console.log(mark(3, true) || mark(4, false));
```

`pick` 先用 `if` 处理负数，再用三元式区分零和正数；`mark` 打印自己的编号，再把传入的布尔值返回。最后两行是短路：`false && ...` 不该调用右边的 `mark(2)`，`true || ...` 也不该调用 `mark(4)`。

## 先看输出

```zsh
# 在项目根目录构建命令行入口；需要 01.01 准备的隔离 Go 工具链。
./tools/build

# 编译并运行这一章的例子。
./bin/tynel run examples/choices.ts
```

```text
$ ./bin/tynel run examples/choices.ts
-1
0
1
1
false
3
true
```

前三个数是 `pick` 的三个结果。第四行只打印了 `1`，然后输出 `false`；第五行只打印了 `3`，然后输出 `true`。`2` 和 `4` 始终没有出现，这是短路在起作用，不是打印器漏了行。

## boolean 先成为一个真正的标量

在第二章里，`console.log` 只接受 `number`，`true` 和 `false` 连输出都做不到。这一章先把 `boolean` 补成完整的标量：参数可以是 `boolean`，返回值可以是 `boolean`，局部 `const` 可以是 `boolean`，比较和短路的结果也是 `boolean`。

NIR 里它长这样：

```json
{ "op": "bool", "value": true }
```

数字走位模式，布尔走 `value`，因为布尔只有两个值，没必要编码成 64 位。bridge 把它映射成 Go 的 `bool`，`print` 调用支持包里的 `tynelPrintBool`，拼写固定为 `true` 和 `false`。这样一来，“结果对不对”仍然可以逐字节比较，不需要人眼把 `1` 理解成真。

这里有一个我没想到的坑。三元式的类型在 TypeScript 里不是简单的 `number`：`true ? 1 : 2` 的结果类型是字面量联合 `1 | 2`。适配包最初只看 `NumberLike` 标志位，联合类型落进了 `other`，降低阶段于是报“返回值是 other，需要 number”。修法不是在三元式上特判，而是在类型压缩的地方把“所有成员都是 number 字面量”的联合压回 `number`，布尔联合同理；混合联合仍然是 `other`，继续拒绝。这个改动放在 `bridge/tsgo/_adapter/facts.go` 的 `typeOf` 里，短路、比较、三元式都跟着受益。

## 比较只留下能直接映射的部分

比较分成两组。`<`、`<=`、`>`、`>=` 只接受两个 `number`；`===`、`!==` 要求两侧同为 `number` 或同为 `boolean`。这两组都能直接落到 Go 的比较运算，不需要运行时帮忙。

TypeScript 还允许 `true < false`，也允许 `1 == true`。前者在 TypeScript 里能通过，但布尔没有原生顺序；后者会做隐式类型转换。两种都不在子集里：前者报 `cap.bad-type`，后者报 `cap.bad-operator`。这不是为了少写代码，而是为了不让“看起来能跑”的程序在某个边界上给出和 JavaScript 不同的答案。

`pick` 里的 `n < 0` 和 `n === 0` 就是这两组。NIR 里它们是 `compare`：

```json
{ "op": "compare", "operator": "<",
  "left": {"op": "param", "name": "n"},
  "right": {"op": "number", "bits": "0x0000000000000000"} }
```

`0` 的位模式是 `0x0000000000000000`，`-1` 是 `0xbff0000000000000`，这些数字在源码里是十进制，到了 NIR 已经是位模式，降低阶段不重新解析。

## 分支是两条都保留的路

`pick` 的第一个 `if` 在 NIR 里是一条 `if` 语句：

```json
{ "op": "if",
  "cond": {"op": "compare", "operator": "<", ...},
  "then": [{"op": "return", "value": {"op": "number", "bits": "0xbff0000000000000"}}] }
```

`else` 为空，因为源码里没有写。验证器不会因为 `else` 为空就跳过它：`if` 的两条路径都要检查，只是空分支没有语句可查。bridge 生成 Go 的 `if`，`then` 里的 `return` 直接返回，函数到此结束。

分支有自己的作用域。块里声明的 `const` 只在这个块里可见，同名声明可以遮蔽外层；离开块之后再引用，TypeScript 会先报 `TS2304`，能力检查和降低器的名字表只是再兜一层。这一层不引入新的运行时结构，作用域只是降低时的名字表。

`pick` 还有一条容易被忽略的规则：有返回值的函数，每条路径都要以 `return` 结束。`if` 的 `then` 返回了，但 `if` 之后的 `return n === 0 ? 0 : 1` 也必须存在，否则条件为假时函数会走到末尾。这条规则在 03.04 固定下来，第三章的例子正好用上。

## 短路不是普通二元运算

`mark(1, false) && mark(2, true)` 如果按普通二元运算降低，会先算左边、再算右边，最后把两个布尔值相与；这样 `mark(2)` 会被调用，输出里会多一个 `2`。短路的语义是“左边已经决定结果时，右边根本不执行”，所以 NIR 不能把它做成一个纯表达式。

这一版新增了 `short` 语句：

```json
{ "op": "short", "name": "_tmp_1", "operator": "&&",
  "left": {"op": "call", "function": "mark", "args": [...]},
  "right": [],
  "value": {"op": "call", "function": "mark", "args": [...]} }
```

它先把 `left` 赋给 `name`；`&&` 在 `name` 为真时执行 `right` 列表，再把 `value` 赋给 `name`；`||` 在 `name` 为假时做同样的事。`right` 是右侧的准备工作，不是右侧的值本身；右侧如果是嵌套短路，它的 `short` 就放在这个列表里，只有需要时才执行。

`right` 只接受 `let`、`short` 和 `select`，因为表达式准备阶段不应该出现 `return` 或 `print` 这种语句。这个限制由解码器和验证器一起把住：手写的坏 NIR 即使绕过前端，也会在 `build-ir` 阶段被拒绝。

结果绑定到 `_tmp_1` 之后，后面的表达式用 `local` 读它。这样 `mark(1, false) && mark(2, true)` 只打印 `1`，结果是 `false`；`mark(3, true) || mark(4, false)` 只打印 `3`，结果是 `true`。

## 三元式是“选一条分支，再绑定结果”

三元式看起来像表达式，但它有两条可能执行的分支，不能直接变成 Go 的三元表达式——Go 没有这个运算符。NIR 用 `select` 语句：

```json
{ "op": "select", "name": "_tmp_1", "type": "number",
  "cond": {"op": "compare", "operator": "===", ...},
  "then": [],
  "thenValue": {"op": "number", "bits": "0x0000000000000000"},
  "else": [],
  "elseValue": {"op": "number", "bits": "0x3ff0000000000000"} }
```

条件求值一次；为真时执行 `then` 列表，在同一个子作用域里求值 `thenValue`，再赋给 `name`；为假时走 `else`。`type` 显式写出结果是 `number` 还是 `bool`，验证器要求两条分支与它一致。`pick` 里的 `n === 0 ? 0 : 1` 就是这样落到 NIR 的。

分支里的准备工作也留在分支里。比如 `true ? (mark(6, false) || mark(7, true)) : mark(8, true)`，`mark(8)` 在 `else` 里，条件为真时不会执行；`then` 里的短路又只打印 `6`、`7`。如果把这些准备语句提到 `select` 外面，未选中的分支就会产生副作用，短路和三元式都会失真。

## 求值顺序是这一章最容易出错的地方

组合起来之后，表达式的准备语句不再总是空的。`f(7, 1) + (f(8, 2) < f(9, 3) ? f(10, 10) : f(11, 20))` 在修正前输出 `8, 9, 10, 7, 11`：右侧三元式的准备语句被提前执行，左侧的 `f(7)` 反而落在了后面。

原因是降低器把左右操作数都降成“表达式 + 准备语句”，然后把准备语句拼在一起，但左侧如果只是一个尚未物化的调用，它仍然留在表达式树里，等 Go 求值时才执行。右侧的准备语句先跑，顺序就反了。修法是：右侧带准备语句时，先把左侧算出来存进临时值，再执行右侧准备。算术和比较共用这条规则；调用实参在 03.05 已经有同样的物化逻辑。

修正后输出是 `7, 8, 9, 10, 11`。这类问题不会让结果数值变错，却会让“谁先打印”变错，而这一章验收的正是完整输出顺序，所以它必须算缺陷，不能算测试写得太严。

## 存下 NIR 再编译

`run` 把构建和运行合成一步。想确认“前端理解对了，后端只是照做”，还是把 NIR 存下来单独回放：

```zsh
# 构建这一章的例子，产物放在 out/choices。
./bin/tynel build examples/choices.ts -o out/choices

# 运行产物，输出应与 run 一致。
./out/choices

# 导出这次降低的 NIR。
./bin/tynel dump nir examples/choices.ts > out/choices.nir.json

# 不读 TypeScript，只按这份 NIR 构建。
./bin/tynel build-ir out/choices.nir.json -o out/choices-replay
./out/choices-replay

# 比较两次产物的哈希。
shasum -a 256 out/choices out/choices-replay
```

```text
$ ./bin/tynel build examples/choices.ts -o out/choices
已生成 out/choices（2430018 字节）
  源码     examples/choices.ts（756327498b36）
  输入     5bf3ba493346
  编译器   02ba7de95dd3
  构建记录 out/choices.build.json

$ ./out/choices
-1
0
1
1
false
3
true

$ shasum -a 256 out/choices out/choices-replay
3974006316ff1a74ed90544f5d08febf27ef866ef9ece716c5e190c77802ca39  out/choices
3974006316ff1a74ed90544f5d08febf27ef866ef9ece716c5e190c77802ca39  out/choices-replay
```

两次产物的字节完全相同。源码哈希 `75632749…` 对上这份 `.ts`，NIR 哈希 `5bf3ba49…` 对上 `out/choices.nir.json`，产物哈希 `39740063…` 对上 `out/choices`。三者串成一条链，任何一环换了内容，后面的哈希都会变。

## 出错的时候

这一章的错误分两类。类型错误由 TypeScript 先拦下，返回路径错误在 TypeScript 里也有明确诊断。拿一个缺返回路径的函数：

```typescript
// 缺返回路径：条件为假时直接走到函数末尾。
function pick(flag: boolean): number {
  if (flag) {
    return 1;
  }
}

console.log(pick(true));
```

```zsh
# 检查缺返回路径的函数。
./bin/tynel check tests/source/testdata/errors/missing-return.ts
```

```text
$ ./bin/tynel check tests/source/testdata/errors/missing-return.ts
…/tests/source/testdata/errors/missing-return.ts:2:31: error TS2366: Function lacks ending return statement and return type does not include 'undefined'.
$ echo $?
1
```

第 2 行第 31 列是函数签名的结尾，TypeScript 把“缺少结束返回”报在返回类型上。这条错误不需要能力检查再重复一遍：源码已经拒绝了，降低阶段不会收到这个函数。

能力限制是另一类。`true && 1` 在 TypeScript 里合法，结果类型是 `1`；本阶段要求短路两侧都是 `boolean`，所以 `check` 会报 `cap.bad-type`，退出状态是 3。数字条件、两臂类型不同的三元式、未选中分支里的取模，也都走这条出口。

## 这一章之后

现在能做到：`number` 和 `boolean` 两种标量，四则运算，一元 `!` 和字面量负号，有序比较与严格相等，`if` / `else if` / `else`，提前返回与完整返回路径，`&&` / `||` 的短路，以及 `cond ? a : b` 的结果绑定。`examples/choices.ts` 从源码到原生程序，输出七行；NIR 回放得到逐字节相同的产物。

还不能做到：局部 `let` 和赋值、`while` / `for`、`break` / `continue`、递归、字符串、对象、数组、模块导入。文章里没有用这些写法，因为它们会让人误以为已经支持；能力检查会把它们明确拒绝，而不是悄悄生成错误的代码。

下一章从“变量要能改”开始：先区分声明身份和可变存储，再让 `while` 每轮重新检查条件。到那时，这一章的 `pick` 会多一个用循环累加的表亲 `sumTo`，而选择、短路和三元式的规则一行都不用改。

相关材料：[操作支持表](../reference/support.md)、[短路求值](../reference/short-circuit.md)、[三元式](../reference/ternary.md)、[返回路径](../reference/returns.md)、[比较语义](../reference/comparison.md)、[分支的两条路径](../reference/branches.md)、[本阶段验证记录](../validation/03-choices.md)。
