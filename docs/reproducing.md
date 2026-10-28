# 复现与工具准备

本页记录从仓库取得代码、准备隔离工具链并运行实验的步骤。当前覆盖 01.01 的工具链准备、01.02 的构建与测试入口、01.05 的 bridge、02.01 的前端源码准备、02.02 的源码快照、02.03 的声明事实、02.04 的能力检查、02.05 的降低、02.06 的 void 与输出、02.07 的主命令行、02.09 的真实源码实验、03.08 的选择实验、04.01 的局部 let、04.02 的简单赋值、04.03 的 while 循环，以及 04.04 的 break / continue；后续节点在同一页追加各自的实验命令。已经发布过的步骤按标签复测，不随最新代码改写。

## 现在能做什么

`tools/toolchain` 能按锁文件在 `.work/` 内准备 go1.27.3 的 SDK 与源码，以及 TypeScript 前端的源码树。`./tools/build` 生成的 `bin/tynel` 是主入口：`check` 做类型检查、能力判断与 NIR 验证，`build` 生成原生程序，`run` 编译并运行，`dump` 输出事实或 NIR。

```zsh
# 一条命令从 TypeScript 得到原生程序；示例在 examples/add.ts。
./bin/tynel build examples/add.ts -o out/add
./out/add

# 只检查，不生成机器码。
./bin/tynel check examples/add.ts
```

首次运行会按需构建前端宿主，需要能访问 Go 模块代理；之后可以离线使用。完整实验过程见第二篇笔记[从一份 TypeScript 源文件到二进制](notes/02-source.md)。

## 选择实验

第三篇笔记使用 `examples/choices.ts`，把比较、分支、短路和三元式放在一个程序里：

```zsh
# 编译并运行选择示例，预期七行输出。
./bin/tynel run examples/choices.ts

# 保存 NIR，再单独回放，两次产物的哈希应相同。
./bin/tynel build examples/choices.ts -o out/choices
./bin/tynel dump nir examples/choices.ts > out/choices.nir.json
./bin/tynel build-ir out/choices.nir.json -o out/choices-replay
shasum -a 256 out/choices out/choices-replay
```

完整解释见第三篇笔记[让程序自己选一条路](notes/03-choices.md)，操作范围见[操作支持表](reference/support.md)。

## 局部 let

04.01 的 `tests/source/testdata/let.ts` 验证已初始化的 `let` 与块内遮蔽：

```zsh
# 预期输出 2、1、true。
./bin/tynel run tests/source/testdata/let.ts

# 导出 NIR，确认 let 带 mutable 字段。
./bin/tynel dump nir tests/source/testdata/let.ts | grep mutable
```

04.01 只建立可变性标记，赋值在下一节。规则见[声明身份与可变存储](reference/mutability.md)。

## 简单赋值

04.02 的 `tests/source/testdata/assign.ts` 让数值和布尔变量各改一次：

```zsh
# 预期输出 4、true。
./bin/tynel run tests/source/testdata/assign.ts

# 赋值顺序：预期 2、2、3、5。
./bin/tynel run tests/source/testdata/assign-order.ts
```

`x = x + 1` 先读旧值再写回；给 `const`、参数或不同类型赋值会被拒绝。规则见[从源码到 NIR](reference/lowering.md)，验收记录见[04.02 验收记录](validation/04-02-assignment.md)。

## while 循环

04.03 的 `tests/source/testdata/sum.ts` 对 0、1、10 分别累加：

```zsh
# 预期输出 0、1、55。
./bin/tynel run tests/source/testdata/sum.ts

# 条件每轮重新求值：预期 0、1、2、3、3。
./bin/tynel run tests/source/testdata/while-cond.ts
```

循环只在原生程序运行时发生；编译器不证明终止，测试用独立进程超时保护。验收记录见[04.03 验收记录](validation/04-03-while.md)。

## break 与 continue

04.04 的跳转用例分别验证内层 `break`、`continue` 和 `return`：

```zsh
# 内层 break 不结束外层：预期 3。
./bin/tynel run tests/source/testdata/break.ts

# continue 回到条件检查：预期 12。
./bin/tynel run tests/source/testdata/continue.ts

# return 退出整个函数：预期 2。
./bin/tynel run tests/source/testdata/jump-return.ts
```

跳转图见[循环跳转](reference/jumps.md)，验收记录见[04.04 验收记录](validation/04-04-jumps.md)。

## 前置条件

当前开发与验收基线是一台 Mac mini（Mac16,10，Apple M4，10 核，24 GB 内存，macOS 26.6.2）。换机器时，下列条件满足即可复现，机型本身不是契约。

| 项目 | 要求 | 检查方式 |
| --- | --- | --- |
| 系统 | Apple Silicon macOS，首版目标为 `darwin/arm64` | `uname -m` 输出 `arm64` |
| 宿主 Go | go1.24 或更新，用于编译准备入口，并在后续提供源码自举 | `go version` |
| 网络 | 可访问 `go.dev/dl`、`codeload.github.com` 与 Go 模块代理；加 `--check-remote` 时还需 `api.github.com` | 准备命令本身会报告失败原因 |
| 磁盘 | 项目盘至少 1.5 GB 空闲，实际占用约 1.1 GB（Go 工具链 555 MB、前端源码 332 MB、模块缓存 54 MB） | `df -h .` |
| Git | 用于取得代码；`--check-remote` 用它复核标签 | `git --version` |

宿主 Go 只承担「编译准备入口」这一件事。准备过程不修改它的安装目录，也不写入全局环境变量。

## 取得代码

```zsh
# 在存放项目的目录执行；已有工作副本时跳过克隆，直接进入目录。
git clone git@github.com:Mahoo12138/tynel.git
cd tynel
```

## 准备工具链

```zsh
# 在项目根目录执行。读取 tools/toolchain/go.lock.json，
# 把 SDK 与源码下载到 .work/downloads/，校验后解压到 .work/toolchain/。
./tools/toolchain/prep
```

同一条命令还会准备前端源码（见下一节）。加 `--check-remote` 会复核 Go 标签 `go1.27.3` 与前端标签 `typescript/v7.0.2` 是否仍指向锁文件登记的提交；加 `--force` 会丢弃已缓存的下载和不可复用的目录重新来过。

命令成功后打印各工具的绝对路径。重复执行不会重新下载：它重新校验缓存与目录，确认无误后复用。

也可以直接用 `go run tools/toolchain/prep.go` 准备，但 `go run` 会把子进程的非零退出码统一成 1，脚本判断失败原因时应使用 `./tools/toolchain/prep`。

## 准备前端源码

```zsh
# 解压 TypeScript 前端源码到 .work/frontend/，校验身份后复制适配包。
./tools/toolchain/prep --frontend-only

# 只校验已解压的源码与适配包副本：不下载、不解压、不写清单。
./tools/toolchain/prep --verify-only

# 复核上游标签是否仍指向锁文件登记的提交。
./tools/toolchain/prep --verify-only --check-remote
```

`--verify-only` 会检查 `go.mod` 的模块行与 `go` 指令、四个标记文件的 sha256、声明库的数量，以及树里的适配包副本是否与 `bridge/tsgo/_adapter` 逐字节一致。任何一项不符都按退出 8 失败，并指出是哪一项。

前端源码的全部来源信息（仓库、标签、提交、归档哈希、许可证）在[前端适配](reference/frontend.md)里，锁文件是 `tools/toolchain/go.lock.json` 的 `frontend` 段。

## 构建与测试

```zsh
# 在项目根目录构建开发用入口；使用 .work 内的隔离 Go。
./tools/build

# 显示版本、目标平台和锁定的工具链身份。
./bin/tynel version

# 运行全部 Go 测试；用 -count=1 禁用缓存，只统计真正跑过用例的包。
# 其中 tests/native 会真的编译并运行 NIR 夹具，因此需要先构建 bridge。
./tools/test

# 只跑源码到原生的回归集合：92 个用例，其中 40 个程序会真的编译并运行。
./.work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go test -count=1 -v ./tests/source
```

`bin/tynel` 不进入版本库。命令、退出状态和职责划分见[命令行参考](reference/cli.md)。

源码回归的用例清单在 `tests/source/testdata/cases.json`，参考输出锁在 `tests/source/reference.json`（Node v22.22.2，`--experimental-strip-types`）。有 Node 的机器会把参考输出与 Node 的实际输出对一遍；没有就跳过对照并说明原因，黄金输出比对照常进行。覆盖范围与环境见[源码回归验收](validation/02-08-source.md)。

## bridge：从 NIR 到原生程序

```zsh
# 构建修改版 Go 编译器；只加一个注入钩子，SDK 目录不被修改。
./bridge/go/build

# 编译并运行一份 NIR，预期输出 42。
./bridge/go/run examples/ir/add.nir.json

# 受控测试驱动：加法、空入口、坏 NIR、普通构建、缺支持函数五个用例。
./bridge/go/check
```

`bridge/go/run` 通过 `-toolexec` 替换 `compile`，用 `TYNEL_NIR` 指定输入；不生成任何 Go 源码。实现细节见 [bridge 说明](reference/bridge.md)。

正式入口是 `tynel build-ir`，它多了输入快照、临时输出后发布和构建记录：

```zsh
# 编译并发布到指定路径；失败不会覆盖已有产物。
./bin/tynel build-ir examples/ir/add.nir.json -o out/add

# 运行生成物。
./out/add

# 查看这次构建的输入、工具与产物哈希。
cat out/add.build.json
```

命令、选项和退出状态见[命令行参考](reference/cli.md)。完整实验过程见第一篇笔记[一份加法描述怎样成为二进制](notes/01-native-ir.md)。

## 前端适配：检查一个 TypeScript 文件

主路径是 `tynel check` / `build` / `run`，它们会自动准备宿主。下面这些命令是开发接口，直接驱动宿主，绕过主命令行：

```zsh
# 受控测试驱动：源码身份、消费者测试、干净输入、类型错误、四类拒绝。
./bridge/tsgo/check

# 手动检查一个文件；诊断以 JSON 输出，有错误级诊断时退出 1。
cd bridge/tsgo/consumer
go run . ../testdata/bad.ts

# 输出前端事实：声明、引用、调用和表达式类型。
go run . -facts ../testdata/facts.ts

# 先查源码，再查原生能力；源码有错退出 1，能力越界退出 3。
go run . -capability ../testdata/capability/ok.ts

# 降低成 NIR，再交给 build-ir 编成原生程序。
go run . -nir ../testdata/add.ts > /tmp/add.nir.json
cd ../.. && ./bin/tynel build-ir /tmp/add.nir.json -o /tmp/add && /tmp/add

# void 函数与丢弃结果：输出两行 7、8。
go run . -nir ../testdata/void.ts > /tmp/void.nir.json
./bin/tynel build-ir /tmp/void.nir.json -o /tmp/void && /tmp/void

# 表达式语句丢弃结果：只输出 42。
go run . -nir ../testdata/discard.ts > /tmp/discard.nir.json
./bin/tynel build-ir /tmp/discard.nir.json -o /tmp/discard && /tmp/discard

# 输出格式：有限值、负零、正负无穷与 NaN。
go run . -nir ../testdata/output.ts > /tmp/output.nir.json
./bin/tynel build-ir /tmp/output.nir.json -o /tmp/output && /tmp/output
```

宿主是独立模块，通过两条 `replace` 指向 `.work/frontend/typescript-go` 与仓库根目录，只导入 `tyneladapter` 和 Tynel 自己的 `compiler/source`，不导入任何 `internal` 包。诊断里的 `start` 是 UTF-8 字节偏移，`line`/`column` 是按快照换算的行列；换算规则和错误码见[源码快照与位置](reference/source.md)，适配接口与会话规则见[前端适配](reference/frontend.md)，声明 ID、类型摘要与校验规则见[声明身份与事实](reference/facts.md)，支持的写法与错误码见[原生能力白名单](reference/capability.md)，源码到 NIR 的对应关系见[从源码到 NIR](reference/lowering.md)，比较结果见[比较语义](reference/comparison.md)，分支的两条路径见[分支的两条路径](reference/branches.md)，返回路径与不可达语句见[返回路径](reference/returns.md)，短路与结果绑定见[短路求值](reference/short-circuit.md)，三元式与分支内部准备见[三元式](reference/ternary.md)，打印器的格式与读回方式见[输出格式](reference/output.md)。

`bridge/tsgo/check` 首次运行会下载前端依赖到 `.work/frontend/gomodcache`，之后可以离线构建；默认使用 `https://goproxy.cn`，可用 `TYNEL_GOPROXY` 覆盖。

中间表示的格式、读入预算和错误码见 [NIR 格式](reference/nir.md)。`examples/ir/add.nir.json` 是手写的 `add(40, 2)`，可以直接用 `tynel build-ir` 编译；往返一致、特殊值位模式、预算限制和语义规则（入口签名、引用、实参、返回类型）都由 `./tools/test` 里的 `compiler/nir` 用例检查。

## 生成了什么

| 路径 | 内容 | 能否重建 |
| --- | --- | --- |
| `.work/downloads/` | 两个官方归档，文件名与锁文件一致 | 能，重新下载 |
| `.work/toolchain/go1.27.3-sdk-darwin-arm64/` | 解压后的 Go SDK，含 `bin/go` 与 `pkg/tool/darwin_arm64/` 下的编译工具 | 能，重新解压 |
| `.work/toolchain/go1.27.3-src/` | 解压后的 Go 源码树，供 01.05 接入 bridge 使用 | 能，重新解压 |
| `.work/toolchain/manifests/` | 每份安装的来源身份与安装时间 | 能，重新准备时重写 |
| `.work/toolchain/toolchain.json` | 本次准备的报告：锁文件身份、宿主 Go、工具路径、归档校验和 | 能，每次运行重写 |
| `.work/frontend/typescript-go/` | 解压后的前端源码，含复制进来的 `tyneladapter` | 能，重新解压并同步 |
| `.work/frontend/gomodcache/` | 前端依赖的模块缓存 | 能，重新下载 |
| `.work/frontend/bin/consumer` | 前端宿主二进制 | 能，源码变化时命令行自动重建 |
| `.work/frontend/bin/consumer.stamp.json` | 宿主二进制与源码的哈希 | 能，重建时重写 |
| `.work/frontend/manifests/typescript-go.json` | 前端来源身份与适配包文件哈希 | 能，重新准备时重写 |
| `.work/run/app` | `tynel run` 的产物 | 能，每次运行重新构建 |
| `.work/bin/prep` | 准备入口的编译产物 | 能，包装脚本每次重建 |
| `bin/tynel` | 开发用的命令行入口 | 能，`./tools/build` 重建 |

这些目录都在 `.gitignore` 覆盖范围内，不进入版本库。

## 启用隔离工具链

准备不会修改当前 shell。需要用到隔离副本时，在当次 shell 中显式设置：

```zsh
# 在项目根目录执行；路径按上一次准备打印的实际值填写，只影响当前 shell。
export GOROOT="$PWD/.work/toolchain/go1.27.3-sdk-darwin-arm64"
export PATH="$GOROOT/bin:$PATH"

# 确认调用的是隔离副本，而不是宿主安装。
go version   # 应输出 go version go1.27.3 darwin/arm64
go env GOROOT
```

## 失败后的检查与恢复

退出码按失败类别区分，便于脚本判断。消息里会给出具体路径和原因。

| 退出码 | 类别 | 常见原因 | 处理 |
| --- | --- | --- | --- |
| 2 | 参数用法 | 选项拼错、多传了位置参数 | 查看命令用法 |
| 3 | 锁文件非法 | `schemaVersion` 不支持、字段拼错、`sha256` 格式不对 | 恢复锁文件；不要手改校验和去迁就本地文件 |
| 4 | 平台不符 | 锁文件目标与本机不一致 | 换匹配的机器，或另立锁文件；不要改锁文件凑合 |
| 5 | 下载失败 | 网络中断、HTTP 非 200 | 检查网络后重跑；未完成的下载不会留下半份文件 |
| 6 | 校验和不符 | 归档损坏、缓存被改动 | 删除该归档后重跑，或加 `--force` |
| 7 | 文件系统 | 目录不可写、解压失败、空间不足 | 检查权限和剩余空间 |
| 8 | 完整性校验 | 版本与锁文件不符、目录不完整、源码身份不符、声明库缺失、接入文件不匹配 | 按提示删除对应目录，或加 `--force` |

几种具体情况：

- 下载中断：内容先写到 `.partial`，校验通过才改名。中断只会留下被清理的临时文件，重跑即可。
- 校验和不符：说明归档内容与锁文件记录不同。先确认是不是本地缓存被改动；需要重新下载时删掉 `.work/downloads/<文件名>` 或加 `--force`。
- 半成品目录：最终目录存在但没有安装清单，或清单与锁文件不符。工具不会猜测目录是否可用，而是停止并提示删除；`--force` 会丢弃后重建。
- 版本不符：解压出的 `VERSION` 首行与锁文件不同，通常是锁文件与归档不配套。此时应核对锁文件，而不是修改归档。
- 平台不符：本机只能准备与本机匹配的 SDK。首版只锁定 `darwin/arm64`，Windows 目标尚未纳入。
- 源码身份不符：前端源码树的模块行、`go` 指令或标记文件哈希与锁文件不同。这通常意味着目录来自另一份归档或被改动过；删除该目录后重新准备，不要改锁文件去迁就它。

## 尚未覆盖

控制流、字符串、对象、数组和模块导入都还没有实现，`check` 会按能力限制拒绝；节点 02 的阶段笔记与完成标签已经就位，03.01 的 `boolean` 已可用，下一步从 03.02 的比较与严格相等开始。Windows 目标保留在 `deferredTargets` 中，没有可执行的准备命令。本页不提供这些步骤的命令，避免把计划写成已经可用。
