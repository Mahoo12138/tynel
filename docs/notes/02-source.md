# 从一份 TypeScript 源文件到二进制

上一章的输入是一份手写的 NIR，描述 `add(40, 2)`。这一章把那份描述换成真正的 TypeScript 文件 `examples/add.ts`，命令从 `build-ir` 换成 `build`，其余链路不变：同一份 NIR 格式、同一个修改版 Go 编译器、同一条链接过程。

读完这一章应该能回答五个问题：源文件在哪一步被固定，tsgo 交出了什么，类型检查通过之后为什么还有一道检查，事实到 NIR 之间少了什么，以及报错时那个行列是从哪份源码算出来的。

## 先看结果

先构建命令行入口，再编译示例、运行产物。

```zsh
# 在项目根目录构建 bin/tynel；需要 01.01 准备的隔离 Go 工具链。
./tools/build

# 从 TypeScript 直接编译出原生程序。
./bin/tynel build examples/add.ts -o out/add

# 运行产物，预期输出 42。
./out/add
```

```text
$ ./bin/tynel build examples/add.ts -o out/add
已生成 out/add（2429986 字节）
  源码     examples/add.ts（12269d671d8c）
  输入     4b599cc831a0
  编译器   1ac6e26642e2
  构建记录 out/add.build.json

$ ./out/add
42
```

三行哈希分别是源码、NIR 和编译器。产物旁边多了一份 `out/add.build.json`，把这些身份连同产物的哈希一起记下来。上一章的 `build-ir` 也有这份记录，只是没有「源码」这一行——它本来就不读源码。

改一个数字，输出跟着变：

```zsh
# 复制一份源码，把第一个实参从 40 改成 41。
cp examples/add.ts out/add-41.ts
perl -pi -e 's/add\(40, 2\)/add(41, 2)/' out/add-41.ts

# 重新编译并运行，预期输出 43。
./bin/tynel build out/add-41.ts -o out/add-41
./out/add-41
```

```text
$ ./out/add-41
43
```

`run` 把编译和运行合成一步，产物固定放在 `.work/run/app`：

```zsh
# 编译并运行本次产物，预期输出 42。
./bin/tynel run examples/add.ts
```

到这里，使用者看到的部分已经完整：一份 `.ts`、一条命令、一个原生程序。下面拆开中间的过程。

## 源文件只读一次

编译的第一步不是调用 tsgo，而是把文件读成一份字节快照。主命令行把路径交给宿主，宿主读一次盘：记下 sha256 和字节数，之后所有位置换算都基于这份快照。适配会话的 `Open` 用内存文件系统组装程序，检查期间不会再碰磁盘。

这条规则的意义在文件被改动时才显现。构建进行到一半，编辑器保存了新版本，本次构建用的仍是开始时那份内容；构建记录里的 `source.sha256` 记的也是它。想确认「编译的是哪一版」，比哈希就够了。

位置也来自这份快照。TypeScript 诊断给的是 UTF-8 字节偏移，`Snapshot.Position` 把它换算成 1 起始的行列：

```text
$ ./bin/tynel check tests/source/testdata/errors/type.ts
…/tests/source/testdata/errors/type.ts:2:7: error TS2322: Type 'string' is not assignable to type 'number'.
```

第 2 行第 7 列是 `answer` 的起点，不是 `"forty-two"` 的位置——TypeScript 把这条错误报在声明名上。偏移按字节算，列按字符算：文件里第 1 行是一句中文注释，每个汉字占 3 个字节，如果拿字节数当列号，第 2 行的列号就会被推远。`TestPositionAfterChinesePrefix` 用一句中文注释固定了这一点，`TestPositionCountsRunesNotBytes` 再补一个 emoji 的例子。

这里有一个这次复查才发现的坑。语法骨架接进事实时，节点的起点直接取了上游的 `node.Pos()`；它返回的是「完整起点」，包含节点前面的空白和注释。于是 `control.ts` 里那个 `if` 的能力诊断指到了 `2:26`——上一行 `{` 后面的换行位置；函数声明的范围也会把头顶的注释算进去。改成「第一个有效记号的位置」之后，同一个错误指向 `3:3`，正是 `if` 所在的地方。位置差一点，读者就会去错误的地方找问题。

## tsgo 交出了什么

前端不是一个库调用，而是一个子进程。原因很实际：上游所有包都在 `internal/` 下，仓库根模块不能导入它们，否则没准备前端源码的人连 `go test ./...` 都跑不起来。所以宿主 `bridge/tsgo/consumer` 单独成一个模块，主命令行用 JSON 跟它说话。

宿主交回来的东西叫事实（facts），不是语法树。一棵 AST 里全是上游的指针和节点类型，拿出去就没法在会话关闭后使用；事实是纯数据：

| 部分 | 内容 |
| --- | --- |
| `declarations` | 顶层函数、参数、顶层 `const`，每条带一个声明 ID |
| `references` | 函数体和初始化表达式里的每一次名字出现，带它指向的声明 ID |
| `calls` | 调用位置、被调用者 ID、实参类型、结果类型 |
| `expressions` | 数字字面量、二元运算、调用和标识符，各带类型摘要 |
| `body` | 函数体或初始化表达式的语法骨架 |

声明 ID 是这一层最关键的东西。`bridge/tsgo/testdata/facts.ts` 里有两个函数，各有一个叫 `a` 的参数：

```typescript
const answer: number = add(40, 2);

function add(a: number, b: number): number {
  return a + b;
}

// 同名参数：这里的 a 和 add 的 a 是两个不同的声明。
function scale(a: number): number {
  return a * 2;
}

const doubled: number = scale(answer);
```

事实里，两个 `a` 的声明 ID 分别是 3 和 6，`return a + b` 里的 `a` 指向 3，`return a * 2` 里的 `a` 指向 6。名字相同、位置不同、ID 不同，这三件事同时成立，降低阶段才能回答「这个 `a` 该读哪个参数槽」。如果只记名字，`scale` 里的 `a` 会被错当成 `add` 的参数。

类型摘要也只有类别：`number`、`boolean`、`string`、`void`、`any`、`other`。不存人类可读的类型字符串，因为字符串是给人看的——`const answer = 42` 的类型字符串是 `"42"`，它属于 `number`；联合类型更没法靠字符串判断。类别来自检查器算好的类型标志位。

事实可以单独存下来看：

```zsh
# 输出前端理解到的声明、引用、调用与语法骨架。
./bin/tynel dump facts examples/add.ts
```

这一步不需要后端，也不需要 NIR。它回答的是「前端有没有理解错」，与「后端有没有生成错」是两件事。

## 类型检查通过，不等于能编译

TypeScript 接受一段程序，只说明它符合 TypeScript 的语义；能不能变成原生代码，是另一个问题。`compiler/capability` 在白名单上再走一遍，遍历所有函数体和模块体——包括没有被调用的函数。

```zsh
# 取余不在本阶段子集内。
./bin/tynel check tests/source/testdata/errors/modulo.ts
# …/modulo.ts:2:13: 能力限制 cap.bad-operator: 运算符 "%" 不在本阶段支持的 + - * / 之内

# 控制流不在本阶段子集内。
./bin/tynel check tests/source/testdata/errors/control.ts
# …/control.ts:3:3: 能力限制 cap.unsupported-syntax: 控制流 IfStatement 不在本阶段子集内

# any 没有原生表示。
./bin/tynel check tests/source/testdata/errors/any.ts
# …/any.ts:1:1: 能力限制 cap.any: 函数 "identity" 的返回类型是 any 或 unknown；本阶段只支持 number 和 void
# …/any.ts:2:19: 能力限制 cap.any: 参数 "value" 是 any 或 unknown；本阶段只支持 number 和 void
```

三条都以退出状态 3 结束，与源码错误的 1 分开。这个区分不是分类癖：源码错误要改代码，能力限制要么等编译器支持，要么换写法，环境缺失则先去准备工具。把三者混成一个「编译失败」，使用者就得多读一遍消息才知道该做什么。

为什么不按可达性跳过没被调用的函数？因为「没被调用」是这一版程序的事实，不是这段代码的性质。今天没人调用，明天加一行调用就得重新检查；而且一次构建应该把所有不支持的地方都报出来，而不是修好一个再发现下一个。

`console.log` 也在白名单里，但认得比名字多一点。它要求 `console` 没有被本文件里的声明遮蔽，参数恰好一个，类型是 `number`。把 `console` 写成参数名再调用 `console.log`，会得到 `cap.shadowed-console`，而不是被误当成内置输出。

## 事实变成 NIR

降低只吃两样东西：源码快照和事实。它不重新问检查器，因为会话已经关闭了。这条约束让降低可以离线重放——存下一份事实，之后任何时候都能生成同样的 NIR。

拿 `examples/add.ts` 走一遍三站。源码是六行：

```typescript
// 加法：把两个数相加并打印结果。
function add(a: number, b: number): number {
  return a + b;
}

console.log(add(40, 2));
```

事实里的 `add` 声明带着参数 ID 和函数体骨架（省略位置）：

```json
{ "id": 1, "kind": "function", "name": "add", "result": {"kind":"number"}, "parameters": [2, 3],
  "body": {"kind": "Block", "children": [
    {"kind": "ReturnStatement", "children": [
      {"kind": "BinaryExpression", "op": "+",
       "children": [{"kind":"Identifier","name":"a","target":2},
                    {"kind":"Identifier","name":"b","target":3}]}]}]}}
```

NIR 里是上一章那种结构，`add` 和 `main` 两个函数（同样省略位置字段）：

```json
{ "name": "add",
  "params": [{"name":"a","type":"number"}, {"name":"b","type":"number"}],
  "result": "number",
  "body": [{"op":"return","value":{"op":"binary","operator":"+",
            "left":{"op":"param","name":"a"},"right":{"op":"param","name":"b"}}}] }

{ "name": "main", "params": null, "result": "void",
  "body": [
    {"op":"print","value":{"op":"call","function":"add","args":[
      {"op":"number","bits":"0x4044000000000000"},
      {"op":"number","bits":"0x4000000000000000"}]}},
    {"op":"return"} ] }
```

`main` 没有参数，Go 把空切片写成 `null`；读入时它与 `[]` 等价。

两站之间丢掉的东西，正好是后端不需要的东西：

- **声明 ID 消失了**。NIR 只留参数名，因为同一个函数里参数名不会重复；跨函数的身份由函数名承担。ID 解决的是「两个 `a` 谁是谁」，到了函数内部这个问题已经不存在。
- **模块体变成了 `main`**。NIR 只有函数，没有顶层语句，所以降低造一个无参数、返回 `void` 的入口，把顶层 `console.log` 变成 `print`，顶层 `const` 变成 `let`。
- **位模式原样搬运**。`40` 在事实里就是 `0x4044000000000000`，降低不解析十进制，也不做常量折叠。

`console.log` 的三种写法看起来很像，落到 NIR 是三件不同的事：

| 源码 | NIR | 结果 |
| --- | --- | --- |
| `announce(7);` | `discard` 调用 | 执行，返回值丢掉 |
| `answer;` | `discard` 局部值 | 求值，结果丢掉，不打印 |
| `console.log(answer);` | `print` | 执行并写一行输出 |

`discard` 不是「什么都不做」。调用可能带副作用，所以求值必须发生；但表达式语句的语义是「值没人要」，不能顺手打印出去。`answer;` 是最容易看出区别的例子：它求值成一个 number，然后丢掉，屏幕上什么都不该出现。

求值顺序也在这一层保住。`console.log(add(log(1), log(2)))` 必须按 `log(1)`、`log(2)`、`add` 的顺序执行。降低保留表达式树的结构，不改写顺序；bridge 把它变成 Go 的调用表达式，Go 的实参从左到右求值。`tests/source/testdata/order.ts` 就是这条验收，输出 `1`、`2`、`3`。

一元负号是个例外。NIR 没有取负操作，降低把 `-<字面量>` 直接折成位模式：`-0` 变成 `0x8000000000000000`，不经过运行时。这也限制了范围：`-x` 需要一条取负指令，本阶段不提供，降低报 `lower.bad-unary`。

## 打印格式是契约

`console.log` 只接受一个 `number`，输出格式因此必须固定，否则「结果对不对」没法逐字节比较。

| 值 | 输出 |
| --- | --- |
| 有限值 | 最短可回读的十进制写法，必要时用指数 |
| 负零 | `-0` |
| 正无穷 | `+Inf` |
| 负无穷 | `-Inf` |
| NaN | `NaN` |

有限值的最短可回读来自 Go 的 `strconv.FormatFloat(v, 'g', -1, 64)`：`0.1 + 0.2` 输出 `0.30000000000000004`，`1e21` 输出 `1e+21`。特殊值不走这条路，它们由支持包按固定拼写直接判断。这样拼写是项目约定的一部分，换 Go 版本或换格式化动词都不会改变。

`+Inf` 与 JavaScript 的 `Infinity` 不一样。这个差异没有被抹平：`tests/source/reference.json` 把这类用例标为 `spelling`，只有特殊值拼写不同，归一后必须相同。改 Tynel 去迎合 Node 会提前定下数值语义，删掉用例会让差异变成没人知道的黑洞，两种都不合适。

打印器只服务于观察，不是完整的 Node `console`。它不格式化对象，不支持多参数，也不写标准错误。

## 存下 NIR 再编译

`build` 是一条命令走到底。想确认「前端理解对了，后端只是照做」，可以把中间的 NIR 存下来，再用 `build-ir` 单独编译：

```zsh
# 导出这次降低的结果。
./bin/tynel dump nir examples/add.ts > out/add.nir.json

# 不读 TypeScript，只按这份 NIR 构建。
./bin/tynel build-ir out/add.nir.json -o out/add-replay
./out/add-replay

# 比较两次产物的哈希。
shasum -a 256 out/add out/add-replay
```

```text
$ ./out/add-replay
42
$ shasum -a 256 out/add out/add-replay
1217c68216f64e5bc771538f457fd548da969dc23d31df1c63bada45c695f542  out/add
1217c68216f64e5bc771538f457fd548da969dc23d31df1c63bada45c695f542  out/add-replay
```

两次产物的字节完全相同。这不是巧合：`build` 内部也是先把 NIR 写成快照再交给后端，`build-ir` 只是跳过了前面几步。源码、NIR、产物三者因此串成一条哈希链——`12269d67…` 对上源码，`4b599cc8…` 对上 NIR，`1217c682…` 对上产物。

手写 NIR 不再是使用前提，但它仍然是后端的固定入口。前端出问题时，把 NIR 存下来直接编译，就能判断问题在降低之前还是之后。

## 出错的时候

四类失败各有各的出口：

```text
$ ./bin/tynel check tests/source/testdata/errors/type.ts
…/tests/source/testdata/errors/type.ts:2:7: error TS2322: Type 'string' is not assignable to type 'number'.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/modulo.ts
…/tests/source/testdata/errors/modulo.ts:2:13: 能力限制 cap.bad-operator: 运算符 "%" 不在本阶段支持的 + - * / 之内
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/missing.ts
tynel: 环境缺失 (host.failed): 前端宿主执行失败（退出状态 4）
  原因: tynel: 环境缺失 (source.io): 无法读取源文件 …/errors/missing.ts
      原因: open …/errors/missing.ts: no such file or directory
$ echo $?
4
```

内部故障是 5，比如前端宿主崩溃或外部进程失败；编译器过期属于环境缺失，是 4。这几类不合并，是为了让「代码写错了」「写法还不支持」「工具没准备好」「编译器自己坏了」各自有明确的下一步。

构建失败不会覆盖旧产物。`out/add` 已经存在时，用一份坏输入构建到同一路径会失败退出，`out/add` 和它的构建记录都保持原样。但也不能把旧产物当成本次结果：记录里的输入哈希没变，就说明它还是上一次的。

## 这一章之后

现在能做到：读一份 `.ts`，经过类型检查、能力检查、降低和验证，得到原生程序。函数、`number`、`void`、顶层 `const`、四则运算、一元负号、直接调用和单参数 `console.log` 都在子集内，`add.ts` 与手写 NIR 得到同样的结果。

还不能做到：`boolean`、比较、`if`、`while`、局部 `let` 赋值、三元式和逻辑运算；字符串、对象、数组、模块导入也不支持。一个文件就是一个程序，`import` 会被能力检查拒绝。输出只有 `number`，没有 `true` 和 `false`。

下一章先让 `boolean` 成为完整的标量，再加上比较、分支、短路和三元式。到那时，`console.log` 的参数会多一种类型，而这一章的编译链路一行都不用改。

相关材料：[命令行参考](../reference/cli.md)、[声明身份与事实](../reference/facts.md)、[从源码到 NIR](../reference/lowering.md)、[原生能力白名单](../reference/capability.md)、[输出格式](../reference/output.md)、[本阶段验证记录](../validation/02-source.md)。
