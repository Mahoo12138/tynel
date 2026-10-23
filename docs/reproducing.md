# 复现与工具准备

本页记录从仓库取得代码、准备隔离工具链并运行实验的步骤。当前覆盖 01.01 的工具链准备和 01.02 的构建与测试入口；后续节点在同一页追加各自的实验命令。已经发布过的步骤按标签复测，不随最新代码改写。

## 现在能做什么

`tools/toolchain` 能按锁文件在 `.work/` 内准备 go1.27.3 的 SDK 与源码，并报告实际使用的 `go`、`compile`、`link`、`asm` 路径。`./tools/build` 生成的 `bin/tynel` 能显示版本与锁定的工具链身份、检查工具是否可用。编译器本身尚未实现：没有 TypeScript 前端、NIR 或代码生成，`tynel build` 仍是未知命令。

## 前置条件

当前开发与验收基线是一台 Mac mini（Mac16,10，Apple M4，10 核，24 GB 内存，macOS 26.6.2）。换机器时，下列条件满足即可复现，机型本身不是契约。

| 项目 | 要求 | 检查方式 |
| --- | --- | --- |
| 系统 | Apple Silicon macOS，首版目标为 `darwin/arm64` | `uname -m` 输出 `arm64` |
| 宿主 Go | go1.24 或更新，用于编译准备入口，并在后续提供源码自举 | `go version` |
| 网络 | 可访问 `go.dev/dl`；加 `--check-remote` 时还需 `github.com` | 准备命令本身会报告失败原因 |
| 磁盘 | 项目盘至少 1 GB 空闲，实际占用约 555 MB | `df -h .` |
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

加 `--check-remote` 会先用 `git ls-remote` 复核标签 `go1.27.3` 指向的提交是否与锁文件一致，再继续准备。加 `--force` 会丢弃已缓存的下载和不可复用的目录重新来过。

命令成功后打印各工具的绝对路径。重复执行不会重新下载：它重新校验缓存与目录，确认无误后复用。

也可以直接用 `go run tools/toolchain/prep.go` 准备，但 `go run` 会把子进程的非零退出码统一成 1，脚本判断失败原因时应使用 `./tools/toolchain/prep`。

## 构建与测试

```zsh
# 在项目根目录构建开发用入口；使用 .work 内的隔离 Go。
./tools/build

# 显示版本、目标平台和锁定的工具链身份。
./bin/tynel version

# 运行全部 Go 测试；用 -count=1 禁用缓存，只统计真正跑过用例的包。
./tools/test
```

`bin/tynel` 不进入版本库。命令、退出状态和职责划分见[命令行参考](reference/cli.md)。

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

中间表示的格式、读入预算和错误码见 [NIR 格式](reference/nir.md)。`examples/ir/add.nir.json` 是手写的 `add(40, 2)`，目前没有命令行入口可以直接读它；往返一致、特殊值位模式、预算限制和语义规则（入口签名、引用、实参、返回类型）都由 `./tools/test` 里的 `compiler/nir` 用例检查。

## 生成了什么

| 路径 | 内容 | 能否重建 |
| --- | --- | --- |
| `.work/downloads/` | 两个官方归档，文件名与锁文件一致 | 能，重新下载 |
| `.work/toolchain/go1.27.3-sdk-darwin-arm64/` | 解压后的 Go SDK，含 `bin/go` 与 `pkg/tool/darwin_arm64/` 下的编译工具 | 能，重新解压 |
| `.work/toolchain/go1.27.3-src/` | 解压后的 Go 源码树，供 01.05 接入 bridge 使用 | 能，重新解压 |
| `.work/toolchain/manifests/` | 每份安装的来源身份与安装时间 | 能，重新准备时重写 |
| `.work/toolchain/toolchain.json` | 本次准备的报告：锁文件身份、宿主 Go、工具路径、归档校验和 | 能，每次运行重写 |
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
| 8 | 完整性校验 | 版本与锁文件不符、目录不完整、接入文件不匹配 | 按提示删除对应目录，或加 `--force` |

几种具体情况：

- 下载中断：内容先写到 `.partial`，校验通过才改名。中断只会留下被清理的临时文件，重跑即可。
- 校验和不符：说明归档内容与锁文件记录不同。先确认是不是本地缓存被改动；需要重新下载时删掉 `.work/downloads/<文件名>` 或加 `--force`。
- 半成品目录：最终目录存在但没有安装清单，或清单与锁文件不符。工具不会猜测目录是否可用，而是停止并提示删除；`--force` 会丢弃后重建。
- 版本不符：解压出的 `VERSION` 首行与锁文件不同，通常是锁文件与归档不配套。此时应核对锁文件，而不是修改归档。
- 平台不符：本机只能准备与本机匹配的 SDK。首版只锁定 `darwin/arm64`，Windows 目标尚未纳入。

## 尚未覆盖

源码自举编译、修改版编译器的构建在 01.05 进行；tsgo 与声明库的锁定在 02.01 进行；Windows 目标保留在 `deferredTargets` 中，没有可执行的准备命令。本页不提供这些步骤的命令，避免把计划写成已经可用。
