# 02.01 前端锁定与适配会话：验收记录

记录 02.01 的实际运行结果。被测代码是正文带 `Task: 02.01` 的提交；本文件不预填不存在的哈希。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核，24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 隔离工具链 | go1.27.3 darwin/arm64（`.work/toolchain/go1.27.3-sdk-darwin-arm64`） |
| 宿主 Go | go1.27.0 darwin/arm64（只用于编译准备入口） |
| 前端源码 | `microsoft/typescript-go`，标签 `typescript/v7.0.2`，提交 `2bd066d8` |

## 正向验收

### 准备与复用

```text
$ ./tools/toolchain/prep --frontend-only
复用已准备的 前端：/Users/mahoo/Projects/tynel/.work/frontend/typescript-go

前端准备完成：typescript/v7.0.2
  上游提交      2bd066d87f5bafd315be9f40889d0a60b9e58e0b（tag typescript/v7.0.2，许可证 Apache-2.0）
  前端          typescript/v7.0.2（/Users/mahoo/Projects/tynel/.work/frontend/typescript-go，已存在并复核）
  宿主 Go       /usr/local/go/bin/go（go version go1.27.0 darwin/arm64，未修改，GOROOT /usr/local/go）
  报告          /Users/mahoo/Projects/tynel/.work/frontend/frontend.json
```

首次准备下载归档 20,375,158 字节（约 4 秒），解压 332 MiB；重复执行只做校验，不再下载。

### 身份校验与远程复核

```text
$ ./tools/toolchain/prep --verify-only
前端源码校验通过：/Users/mahoo/Projects/tynel/.work/frontend/typescript-go（typescript/v7.0.2）

$ ./tools/toolchain/prep --verify-only --check-remote
远程复核：前端 typescript/v7.0.2 对应提交 2bd066d87f5b，与锁文件一致
前端源码校验通过：/Users/mahoo/Projects/tynel/.work/frontend/typescript-go（typescript/v7.0.2）
```

复核走 GitHub API 而不是 `git ls-remote`：本机访问 `github.com` 的 git 通道返回 `CONNECT tunnel failed, response 502`，而 API 走普通 HTTPS 可用。

### 外部宿主检查单文件程序

```text
$ ./.work/frontend/bin/consumer bridge/tsgo/testdata/add.ts
[]
$ echo $?
0
```

```text
$ ./.work/frontend/bin/consumer bridge/tsgo/testdata/bad.ts
[
  {
    "file": "/Users/mahoo/Projects/tynel/bridge/tsgo/testdata/bad.ts",
    "code": 2322,
    "category": "error",
    "message": "Type 'string' is not assignable to type 'number'.",
    "start": 109,
    "length": 6
  }
]
$ echo $?
1
```

`start` 是 UTF-8 字节偏移，不是字符位置：同一段代码去掉前面那行 36 个汉字的注释后，`start` 是 6，而字符位置只从 43 变回 6。这条差异由 `TestPositionIsUTF8ByteOffset` 固定住。

### 会话规则

`bridge/tsgo/consumer` 的测试覆盖：

| 用例 | 断言 |
| --- | --- |
| `TestCleanProgramHasNoDiagnostics` | 合法源码返回空诊断 |
| `TestTypeErrorCarriesCodeAndPosition` | 错误码 2322、类别 error、消息、文件路径、偏移范围 |
| `TestPositionIsUTF8ByteOffset` | 偏移等于字节位置且不等于字符位置 |
| `TestDiagnosticsArePlainData` | 会话关闭后诊断仍可读取，说明没有夹带上游句柄 |
| `TestCloseIsIdempotentAndQueriesFailAfterwards` | 首次关闭成功、重复关闭返回 nil、关闭后查询返回 `ErrSessionClosed` |
| `TestRejectsInvalidOptions` | 空文件名、空源码、相对路径都被拒绝（3 个子用例） |
| `TestSourceSnapshotIsNotRereadFromDisk` | 检查期间改写磁盘文件，诊断不变 |

6 个测试函数、9 个 `=== RUN` 条目，全部通过：

```text
ok  	github.com/Mahoo12138/tynel/bridge/tsgo/consumer	0.505s
```

## 负向验收

`./bridge/tsgo/check` 的 8 个用例全部通过：

```text
使用 go version go1.27.3 darwin/arm64
  PASS  前端源码身份与适配包副本一致
  PASS  外部消费者测试通过
  PASS  干净输入没有诊断（退出 0）
  PASS  类型错误以 2322 报告（退出 1）
  PASS  外部导入 internal 包被拒绝
  PASS  源码身份不符按退出 8 拒绝
  PASS  声明库缺失按退出 8 拒绝
  PASS  声明库不完整按退出 8 拒绝
```

| 场景 | 做法 | 结果 |
| --- | --- | --- |
| 源码身份不符 | 临时项目里把 `go.mod` 的模块行换成 `github.com/someone/other` | 退出 8，`源码身份不符：… 的模块是 "github.com/someone/other"，锁文件是 "github.com/microsoft/typescript-go"` |
| 声明库缺失 | 临时项目只放四个标记文件，不放 `internal/bundled/libs` | 退出 8，`源码身份不符：缺少 internal/bundled/libs/lib.es5.d.ts` |
| 声明库不完整 | 同上，但 `libs` 里只放 `lib.es5.d.ts` | 退出 8，`声明库不完整：… 只有 1 个 lib.*.d.ts` |
| 外部直接导入内部包 | 临时模块 `import "…/internal/compiler"` 后 `go build` | 构建失败，`use of internal package github.com/microsoft/typescript-go/internal/compiler not allowed` |
| 关闭后查询 | `Close` 两次后调用 `Diagnostics` | 返回 `ErrSessionClosed`，不是空诊断 |
| 非法输入 | 空文件名、空源码、相对路径 | `Open` 返回错误 |

外部导入内部包这条不是靠约定，而是靠 Go 自己的访问规则：适配包位于模块树内所以能导入 `internal/*`，宿主位于模块树外所以只能导入 `tyneladapter`。复制 `internal` 包出来或用 `go:linkname` 都不在方案内。

## 耗时与体积

| 项目 | 值 |
| --- | --- |
| `./bridge/tsgo/check`，冷缓存 | 1 分 30 秒 |
| `./bridge/tsgo/check`，热缓存 | 12 秒 |
| 前端源码树 | 332 MiB |
| 模块缓存 | 54 MiB |
| 适配包 | 159 行，1 个文件 |

## 过程中发现的问题

1. **git 通道被代理掐断**。`git ls-remote` 与 `git clone` 都返回 `CONNECT tunnel failed, response 502`，`codeload.github.com` 和 `api.github.com` 则可用。准备改为从 codeload 下载归档，复核改为查 API 的 `/commits/<tag>`。这也是 `--check-remote` 对前端不用 `git ls-remote` 的原因。
2. **位置单位不是 UTF-16**。最初按 TypeScript 的字符语义把 `Start` 写成 UTF-16 偏移；用带中文注释的夹具实测发现是 UTF-8 字节偏移（109 对 43）。已改注释并加用例固定。
3. **编译缓存占满磁盘**。把 `GOCACHE` 放进 `.work` 后目录涨到 2.7 GB，而本机磁盘只剩不到 1 GB。编译缓存是纯派生数据，改回默认位置；只有模块缓存留在项目内，保证离线可用。
4. **只准备前端时覆盖了工具链报告**。`--frontend-only` 最初把 `installs` 为空的报告写进 `.work/toolchain/toolchain.json`，导致后续命令找不到 `go` 路径。现在只准备前端时写到 `.work/frontend/frontend.json`。

## 与计划的偏差

- **锁文件没有另开一份**。「锁文件的前端部分」写在 `tools/toolchain/go.lock.json` 的 `frontend` 段，而不是新建 `frontend.lock.json`：两处各记一份身份，迟早会漂移。文件名保留 01.01 建立时的 `go.lock.json`，不为了改名去改动已有的验证记录。
- **适配包用复制而不是 overlay**。01.05 的 bridge 可以用 `-overlay` 是因为它只被一个 `go build` 使用；这里要产出能被外部模块 `import` 的包，overlay 无法让 `go build` 在模块外看到它。
- **声明库缺失只能在准备阶段发现**。声明库由 `go:embed` 编进二进制，运行期不存在「找不到 lib」的状态；所以这条负向验收落在源码准备检查上，而不是会话行为上。
