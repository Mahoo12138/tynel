# Tynel

Tynel 是一个学习型 TypeScript-to-native 编译器项目。目标是复用 tsgo 前端和 Go 编译器后端，将受限 TypeScript 编译为使用 Go 运行时的原生程序。

MVP 的交付物是编译器本身：通过 `tynel build input.ts -o out/program` 将约定子集的 TypeScript 编译为 `darwin/arm64` 原生二进制，并提供类型与能力检查、错误诊断和可重复构建。应用服务不是验收条件。

节点 01 已完成（`mvp/01-native-ir`）：`tools/toolchain` 能准备隔离工具链，`compiler/nir` 定义并验证最小中间表示，`bridge/go` 把手写 NIR 经修改版 Go 编译器编成原生程序，`tynel build-ir` 负责输入快照、临时输出后发布和构建记录，`tests/native` 用原生回归核对数值与反汇编——`add(40, 2)` 输出 42，改成 41 得到 43。第一篇笔记见[一份加法描述怎样成为二进制](docs/notes/01-native-ir.md)。

02.01 已完成：`tools/toolchain` 现在也按锁文件解压 TypeScript 前端源码并校验身份，`bridge/tsgo/_adapter` 在上游模块树内提供稳定的检查接口，外部宿主 `bridge/tsgo/consumer` 能检查单个 `.ts` 文件并拿到带错误码和字节位置的诊断。

02.02 已完成：`compiler/source` 把源文件固定成不可变快照，负责字节偏移与行列的换算、输入预算和诊断映射。中文注释之后的错误仍然指向正确的字符，检查期间不会重新读磁盘。

02.03 已完成：`compiler/frontend` 定义声明、引用、调用和表达式类型的事实，并校验它们是否自洽。同名参数拿到不同 ID，前向调用解析到后声明的函数，事实在会话关闭后仍可读取。

02.04 已完成：`compiler/capability` 按白名单检查所有函数体，包括没有被调用的函数。`any`、类型断言、对象、`eval`、动态 `Function`、未知库调用都按能力限制退出 3；`console.log` 只有在内置 `console` 上才被接受。

02.05 已完成：`compiler/lower` 把事实降低成 NIR，`consumer -nir` 是它的开发接口。`add.ts` 生成的 NIR 与手写版本运行结果相同（42），实参从左到右求值，一元负号折成负零的位模式。

02.06 已完成：void 函数、无值 `return`、正常结束和丢弃结果的表达式语句都落到 NIR 的 `discard` 上，`answer;` 不打印任何东西。打印器有明确的格式：有限值最短可回读，负零、NaN、正负无穷各有固定拼写。

02.07 已完成：主命令行的 `check`、`build`、`run`、`dump` 走同一条流程。`tynel build foo.ts -o out/app` 直接得到原生程序，`tynel run foo.ts` 编译并运行本次产物，`check` 不生成机器码也不需要后端。前端以子进程方式接入，构建记录串起源码、NIR 与产物的哈希。

02.08 已完成：`tests/source` 把 14 个源码用例编成原生程序并逐字节比较输出，覆盖加法、求值顺序、void、丢弃结果、特殊值与五类失败。参考输出与 Node v22.22.2 对照，唯一差异是无穷的拼写，且写进了 `reference.json`。计划分为 6 个主节点、48 个开发子节点和 6 篇主笔记：原生后端、真实源码、条件选择、局部状态与循环、静态模块、编译器交付。首版以 `number`、`boolean` 和结构化控制流为语言边界；字符串、对象和数组作为后续语言扩展，不要求先完成某个应用。

02.09 已完成：节点 02 收尾。`examples/add.ts` 是一条命令走完的示例，`tynel build` 得到 42，改实参得到 43，存下 NIR 回放得到逐字节相同的产物；能力诊断的位置改为指向记号本身，不再指到上一行的空白。第二篇笔记见[从一份 TypeScript 源文件到二进制](docs/notes/02-source.md)，阶段验收见[节点 02 累计验证记录](docs/validation/02-source.md)。

03.01 已完成：`boolean` 成为完整的标量类型。常量、参数、返回值和 `console.log` 都接受 `boolean`，`!` 降成 NIR 的 `not`，`false` 用指针与「缺字段」区分；后端用 Go 的 `bool` 与 `ONOT`，不经过数字真假转换。`invert(false)` 输出 `true`，`!1` 按能力限制拒绝。验收记录见[03.01 验收记录](docs/validation/03-01-boolean.md)。

03.02 已完成：比较与严格相等。`<` `<=` `>` `>=` 只接受 `number`，`===` `!==` 要求两侧同为 `number` 或同为 `boolean`；结果都是 `boolean`，左右操作数按源码顺序求值。NaN、负零和无穷遵循 IEEE 754，`NaN < 1` 与 `NaN >= 1` 都是 `false`。宽松 `==` / `!=` 与 `true < false` 按能力限制拒绝。比较语义见[比较语义](docs/reference/comparison.md)，验收记录见[03.02 验收记录](docs/validation/03-02-compare.md)。

03.03 已完成：`if` / `else` / `else if` 与分支作用域。条件必须是 `boolean`，只求值一次；两条分支各自保留语句、各自开作用域，分支里的 `const` 降成 NIR 的 `let`。未选中的分支照样检查、照样保留，运行时只执行一条。`pick(true)` 与 `pick(false)` 分别输出 `1` 和 `2`，两边同名的 `x` 互不影响。双路径图见[分支的两条路径](docs/reference/branches.md)，验收记录见[03.03 验收记录](docs/validation/03-03-branch.md)。

03.04 已完成：提前返回与完整返回路径。有值函数不再要求最后一条语句是 `return`，而是检查每条路径是否结束：一条 `return` 直接结束，`if` 只有两臂都存在且都结束才算覆盖。`sign` 两臂都返回、`pick` 提前返回后补 `return`、嵌套分支和 void 正常结束都能构建；缺路径在 TypeScript、降低和 NIR 验证三处分别被拒绝。`return` 之后的不可达语句照样过能力检查，NIR 保留它们，bridge 生成时丢弃。规则见[返回路径](docs/reference/returns.md)，验收记录见[03.04 验收记录](docs/validation/03-04-returns.md)。

03.05 已完成：布尔短路。`&&` / `||` 降成 NIR 的 `short` 语句，左侧只求值一次，右侧的准备工作与右值留在条件内部，结果绑定到临时值后供 const、条件、实参和 return 使用。`false && mark(2)` 与 `true || mark(4)` 不打印 `2`、`4`；嵌套短路与实参顺序保持源码顺序。两侧都必须是 `boolean`，未选中的右侧照样过能力检查。规则见[短路求值](docs/reference/short-circuit.md)，验收记录见[03.05 验收记录](docs/validation/03-05-short.md)。

03.06 已完成：三元式。`cond ? a : b` 降成 NIR 的 `select` 语句，条件求值一次，只有选中的分支执行；两条分支各自带准备语句，结果绑定到临时值后可用于 const、条件、实参和 return。`true ? pick(1)` 与 `false ? pick(3) : pick(4)` 只调用被选中的分支；嵌套三元式与分支内部的短路保持各自的作用域。条件必须是 `boolean`，两臂必须同为 `number` 或同为 `boolean`。规则见[三元式](docs/reference/ternary.md)，验收记录见[03.06 验收记录](docs/validation/03-06-ternary.md)。

03.07 已完成：条件表达式的组合行为。优先级、括号、多层选择、实参顺序与输出顺序用源码差分和 NIR 回放核对；`f(7) + (条件 ? ...)` 暴露并修正了「右侧准备语句跑到左侧调用前面」的顺序问题。协议版本、深度预算、错误作用域和坏 NIR 直接交给 `build-ir` 的拒绝路径也纳入记录。完整组合表见[03.07 验收记录](docs/validation/03-07-combine.md)。

03.08 已完成：选择实验与第三篇。`examples/choices.ts` 从比较、分支、短路和三元式得到七行输出，`build` 与 `build-ir` 回放的产物逐字节相同；缺返回路径的 `TS2366` 在正文里可复现。[操作支持表](docs/reference/support.md)单独列出支持、拒绝与计划状态，第三篇笔记见[让程序自己选一条路](docs/notes/03-choices.md)，阶段验收见[03.08 验收记录](docs/validation/03-choices.md)。节点 03 建立完成标签 `mvp/03-choices`。

04.01 已完成：声明身份与可变存储。块内 `let` 带初始值声明，类型限 `number` / `boolean`，可读取、可遮蔽外层；事实和 NIR 的 `let` 都带 `mutable` 标记，为下一步赋值做准备。未初始化的 `let`、初始化读自身、越界作用域、模块级 `let` 与 `var` 都被拒绝。规则见[声明身份与可变存储](docs/reference/mutability.md)，验收记录见[04.01 验收记录](docs/validation/04-01-mutability.md)。

04.02 已完成：局部简单赋值。`x = value;` 降成 NIR 的 `set`，目标必须是可变的 `let`，右值先求值再写入；`x = x + 1` 读到写入前的值。数值累加与布尔改写都能构建，给 `const`、参数、未声明目标或不同类型赋值被拒绝，赋值表达式、复合赋值、自增自减也拒绝。规则见[从源码到 NIR](docs/reference/lowering.md)，验收记录见[04.02 验收记录](docs/validation/04-02-assignment.md)。

04.03 已完成：while 循环。`while (cond) { body }` 降成 NIR 的 `while`，条件的准备语句每轮重新执行，循环可能零次执行；`sumTo(0)`、`sumTo(1)`、`sumTo(10)` 分别得到 `0`、`1`、`55`，条件函数的打印次数与检查次数一致。循环体里的 `let` 不泄漏，只有循环体 `return` 的函数仍被判为返回路径不完整；原生测试用 5 秒超时防止错误实现挂住。验收记录见[04.03 验收记录](docs/validation/04-03-while.md)。

04.04 已完成：break、continue 与嵌套循环。NIR 的 `while` 带循环 id，`break` / `continue` 用 `target` 指向最近循环，验证器检查目标身份；内层 `break` 不结束外层，`continue` 回到条件检查，`return` 仍退出整个函数。带标签的跳转与循环外跳转被拒绝。跳转图见[循环跳转](docs/reference/jumps.md)，验收记录见[04.04 验收记录](docs/validation/04-04-jumps.md)。

04.05 已完成：直接递归与调用层。阶乘 `fact(7)` 得到 `5040`，相互递归的 `isEven` 可以调用后面声明的 `isOdd`，`sum(4)` 的每层局部值互不覆盖。降低器、NIR 验证器和 bridge 都先登记全部签名再生成函数体；编译器不执行递归，无终止程序只构建、不运行。规则见[直接递归与调用层](docs/reference/recursion.md)，验收记录见[04.05 验收记录](docs/validation/04-05-recursion.md)。

从 [MVP 实施计划](PLAN.md) 开始阅读。范围和语义见 [SPEC.md](SPEC.md)，技术文档约定见[写作指南](docs/writing-guide.md)，提交流程见[开发流程](docs/development.md)，准备工具与复现步骤见[复现指南](docs/reproducing.md)。节点状态见[阶段清单](docs/stages.json)。

旧实现和历史实验不在当前仓库中：本仓库从独立根提交开始，没有归档分支，也不把外部归档当作当前实现证据。

每个子节点都列出依赖、交付物、正常及错误验收和文档任务，可单独开发、提交和记录证据。主节点完成后再建立对应章节标签，子任务不自动扩成独立文章。

下一步进入 04.06：验证状态和控制组合。节点 01、02、03 的全部子节点与 04.01 至 04.05 都已通过正常、错误和边界验收，04 的其余子节点与 05、06 仍为计划状态。
