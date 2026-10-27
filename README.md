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

从 [MVP 实施计划](PLAN.md) 开始阅读。范围和语义见 [SPEC.md](SPEC.md)，技术文档约定见[写作指南](docs/writing-guide.md)，提交流程见[开发流程](docs/development.md)，准备工具与复现步骤见[复现指南](docs/reproducing.md)。节点状态见[阶段清单](docs/stages.json)。

旧实现和历史实验不在当前仓库中：本仓库从独立根提交开始，没有归档分支，也不把外部归档当作当前实现证据。

每个子节点都列出依赖、交付物、正常及错误验收和文档任务，可单独开发、提交和记录证据。主节点完成后再建立对应章节标签，子任务不自动扩成独立文章。

下一步进入 03.02：实现比较与严格相等。节点 01、02 的全部子节点与 03.01 都已通过正常、错误和边界验收，其余子节点仍为计划状态。
