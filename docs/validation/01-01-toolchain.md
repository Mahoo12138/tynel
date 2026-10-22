# 01.01 工具链准备验证记录

本记录对应子节点 01.01「核验 Go 基线并准备隔离工具链」。被测代码是包含本文件的工具链提交，其正文带 `Task: 01.01`；用 `git log --grep 'Task: 01.01'` 可以定位。本文件记录该提交的实际运行结果。

## 环境

| 项目 | 实际值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 宿主 Go | `go version go1.27.0 darwin/arm64`，`GOROOT=/usr/local/go` |
| 网络 | 可访问 `go.dev/dl` 与 `github.com` |
| 执行时间 | 2026-10-22 14:51–14:59（CST） |

## 锁定的身份

| 字段 | 值 |
| --- | --- |
| 版本 | `go1.27.3` |
| 源码标签 | `go1.27.3` |
| 源码提交 | `2aae1000ea1d4b28923b96db28acf4e3b99d6aff` |
| 许可证 | BSD-3-Clause（`LICENSE`，首行 `Copyright 2009 The Go Authors.`） |
| SDK 归档 | `go1.27.3.darwin-arm64.tar.gz`，68,196,737 字节，sha256 `dd3908cf…99c2` |
| 源码归档 | `go1.27.3.src.tar.gz`，35,134,035 字节，sha256 `6e774903…26bb` |
| 目标平台 | `darwin/arm64`（`windows/amd64` 记入 `deferredTargets`） |
| 自举要求 | `go1.24`，由 `src/cmd/dist/build.go` 的 `requiredBootstrapVersion` 对 `src/go.mod` 的 `go 1.27` 计算得出 |

归档校验和与大小取自 `https://go.dev/dl/?mode=json` 的官方元数据，并在每次准备时重新计算比对。源码提交由 `git ls-remote --tags` 解析标签得到，`--check-remote` 会在准备时复核。

## 正向验收

### 干净目录首次准备

在只含锁文件的空目录执行，得到完整的下载、校验、解压和报告过程。

```text
远程复核：标签 go1.27.3 对应提交 2aae1000ea1d，与锁文件一致
下载 https://go.dev/dl/go1.27.3.darwin-arm64.tar.gz（65.0 MiB）→ …/.work/downloads/go1.27.3.darwin-arm64.tar.gz
校验 go1.27.3.darwin-arm64.tar.gz 通过（sha256 dd3908cf2095）
已准备 SDK：…/.work/toolchain/go1.27.3-sdk-darwin-arm64
下载 https://go.dev/dl/go1.27.3.src.tar.gz（33.5 MiB）→ …/.work/downloads/go1.27.3.src.tar.gz
校验 go1.27.3.src.tar.gz 通过（sha256 6e7749035cf3）
已准备 源码：…/.work/toolchain/go1.27.3-src
```

退出码 0，耗时 9.2 秒（含 98.5 MiB 下载）。同一命令在项目目录重复执行时输出 `复用已准备的 SDK` 与 `复用已准备的 源码`，退出码 0，没有重新下载。

### 隔离 SDK 自证

```text
$ env -u GOROOT .work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go version
go version go1.27.3 darwin/arm64
$ env -u GOROOT .work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go env GOROOT
/Users/mahoo/Projects/tynel/.work/toolchain/go1.27.3-sdk-darwin-arm64
```

隔离副本报告自己的目录为 `GOROOT`，与宿主 `/usr/local/go` 无关。

### 报告中的实际工具路径

`.work/toolchain/toolchain.json` 记录本次准备实际使用的工具，全部位于 `.work/toolchain/go1.27.3-sdk-darwin-arm64/` 下：

| 工具 | 相对路径 |
| --- | --- |
| `go` | `bin/go` |
| `compile` | `pkg/tool/darwin_arm64/compile` |
| `link` | `pkg/tool/darwin_arm64/link` |
| `asm` | `pkg/tool/darwin_arm64/asm` |

### 体积

`.work/` 合计 555 MB：下载缓存 99 MB、SDK 271 MB、源码 185 MB。

## 负向验收

每个用例都在独立临时项目中进行，只复制锁文件与归档缓存，不影响项目内的 `.work/`。期望退出码与实际一致才算通过。

| 用例 | 构造方式 | 退出码 | 结果 |
| --- | --- | --- | --- |
| 坏哈希 | 改动 SDK 归档中 1 字节 | 6 | 报「缓存文件…校验失败」并给出两个 sha256；没有生成 SDK 目录 |
| 版本不符 | 锁文件声明 `go1.27.2`，归档仍是 1.27.3 | 8 | 报 `VERSION` 首行为 `go1.27.3`，与锁定版本不同；没有残留暂存目录 |
| 半成品目录 | 预建空的最终目录 | 8 | 报「缺少安装清单，目录不完整」并提示删除或加 `--force` |
| 平台不符 | 锁文件目标改为 `windows/amd64` | 4 | 报「锁文件目标为 windows/amd64，本机是 darwin/arm64」 |
| 接入文件不符 | 登记一个 sha256 不匹配的补丁文件 | 8 | 报该文件实际 sha256 与锁文件不同 |
| 坏 schemaVersion | 改为 2 | 3 | 报「不支持的 schemaVersion 2」 |
| 未知字段 | 在 `go` 下增加 `unexpected` | 3 | 报 `json: unknown field "unexpected"` |

7 个用例全部通过。失败路径不会留下半份可用目录：坏哈希用例中最终目录不存在，版本不符用例中没有 `.partial` 残留。

恢复路径也做了验证：在半成品目录上执行 `--force`，工具丢弃目录、用已校验的缓存重新解压，退出码 0，SDK 与源码目录重新可用。

## 宿主环境

- 准备前后 `go env GOROOT` 均为 `/usr/local/go`，`/usr/local/go` 的修改时间为 2026-08-19，早于本次操作。
- `find /usr/local/go -newer tools/toolchain/go.lock.json` 输出 0 个文件，说明没有写入宿主安装目录。
- 工具不读写任何全局代理配置，也没有修改 shell 启动文件；下载只在准备进程内完成，进程结束后不留下环境改动。

## 过程记录

第一次运行在解压后停止：Go 发行包的 `VERSION` 文件有两行，首行是版本号，第二行是 `time 2026-10-19T22:20:36Z`。校验最初按整份内容比较，因此把正常归档判成版本不符。改为只比较首行、并把第二行作为构建时间戳记入报告后重跑通过。这条经验也说明负向校验要能区分「内容真的不对」和「校验逻辑写错」。

## 未覆盖

- 源码自举编译没有执行，留到 01.05 首次构建修改版编译器时进行。锁文件只记录了自举最低版本及其推导来源。
- tsgo、声明库与接入补丁尚未锁定，`bridge.patchFiles` 当前为空；补丁校验逻辑已就位，等 01.05 加入实际文件后生效。
- `windows/amd64` 只有 `deferredTargets` 记录，没有可执行的准备命令，也没有交叉编译证据。
- 本记录不涉及编译器功能：没有 NIR、没有代码生成、没有 `tynel` 命令。
