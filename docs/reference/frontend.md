# TypeScript 前端适配

本页说明 Tynel 怎样使用上游 TypeScript 前端：为什么需要一个适配包、这个包提供哪些信息、会话为什么必须短命，以及源码身份怎样判定。完整来源清单在文末。

## 上游没有对外入口

上游模块 `github.com/microsoft/typescript-go` 的所有能力都放在 `internal/` 下：`internal/compiler`、`internal/ast`、`internal/tsoptions` 都是。Go 的访问规则不允许模块外的代码导入 `internal` 包，所以外部宿主既不能 `import "…/internal/compiler"`，也不能通过 `go:linkname` 之外的任何正当方式拿到程序对象。

这不是可以绕过的限制，也不该绕过：绕过它等于把「上游随时可以改内部结构」变成自己的维护负担。可行的做法只有一个——把适配代码放进模块树内，让它成为上游模块的一部分，再由外部模块导入这个非 internal 的包。

```
.work/frontend/typescript-go/          上游源码（解压自锁定归档，不改动）
  internal/…                           上游内部包
  tyneladapter/                        由 bridge/tsgo/_adapter 复制进来
bridge/tsgo/_adapter/adapter.go         唯一源文件，随仓库版本控制
bridge/tsgo/consumer/                  外部宿主：独立模块，只导入 tyneladapter
```

复制是单向的：`bridge/tsgo/_adapter` 是唯一可编辑的位置，`.work` 里的副本每次准备都被覆盖。这样不会出现「改了副本、忘了源文件」的情况。

## 适配接口提供什么

只有三个导出：`Open`、`Session.Diagnostics`、`Session.Close`，加上两个数据结构。

| 名称 | 内容 |
| --- | --- |
| `Options{FileName, Source}` | 一次检查的输入。`Source` 是调用方读好的字节快照，`Open` 不再读磁盘 |
| `Diagnostic{File, Code, Category, Message, Start, Length}` | 一条源级诊断，字段全是普通类型 |
| `Session` | 一次检查的会话，持有上游句柄 |
| `ErrSessionClosed` | 会话关闭后查询的返回值 |

`Start` 和 `Length` 是 UTF-8 字节偏移，不是字符数，也不是 UTF-16 码元数。这一点用中文注释很容易看出来：`const answer: number = "forty-two";` 前面加一行 36 个汉字（102 字节）的注释后，`answer` 的位置从 6 变成 109，而字符位置只从 6 变成 43。行列换算留给调用方，因为它要和调用方的源码快照对齐（见 02.02）。诊断顺序是程序级在前、文件级在后。

`Diagnostic` 里没有 `*ast.Node`、没有 `*checker.Checker`、也没有 `types.Type`。这不是洁癖：一旦把上游指针交出去，调用方就会开始依赖它们的字段，上游一次重构就会同时打断两边。适配层的价值就在于把「上游长什么样」关在包内。

## 为什么句柄只在会话里

`compiler.Program` 内部持有绑定后的语法树、符号表和检查器池。它的生命周期由一次检查决定：

- `Open` 组装程序并取到入口源文件；
- `Diagnostics` 在会话内取诊断；
- `Close` 把 `program` 和 `file` 置空，句柄随之不可达。

关闭后 `Diagnostics` 返回 `ErrSessionClosed`，而不是空诊断。空诊断会被读成「没有错误」，这是最危险的误读方式。`Close` 可以重复调用并返回 `nil`，调用方不需要记住自己关过没有。

会话不缓存行表，也不跨会话复用程序。上游有 checker pool，但那是它内部的事；Tynel 每次检查都新建一个程序，避免「上一个文件的状态影响了下一个文件」这类难以定位的问题。等 02.07 需要连续编译多个文件时，再考虑复用，并且要先用测试固定住复用的边界。

## 源码身份怎样判定

锁文件 `tools/toolchain/go.lock.json` 的 `frontend` 段固定了仓库、标签、提交、许可证、归档哈希，以及四个标记文件的 sha256：

| 标记 | 作用 |
| --- | --- |
| `go.mod` | 模块路径与 `go` 指令必须与锁文件一致；含 `replace` 直接拒绝 |
| `LICENSE` | 许可证文本不能换 |
| `internal/compiler/program.go` | 程序构造入口确实存在且是登记的那一份 |
| `internal/bundled/libs/lib.es5.d.ts` | 声明库确实随源码一起存在 |

声明库另有一道数量检查：`internal/bundled/libs` 下至少要有 100 个 `lib.*.d.ts`。这些文件由 `go:embed` 编进二进制，缺文件时构建会报「匹配不到文件」；提前检查能把这句话换成「声明库不完整，只有 N 个」。

`./tools/toolchain/prep --verify-only` 只读地跑完这些检查，不下载、不解压、不写清单。它同时比较树里的适配包副本与 `bridge/tsgo/_adapter` 的字节内容——副本过期时提示重新同步，而不是让人对着一个旧包调试。

## 外部宿主怎样接入

宿主是一个独立模块，靠一条 `replace` 指向准备好的源码树：

```
require github.com/microsoft/typescript-go v0.0.0

replace github.com/microsoft/typescript-go => ../../../.work/frontend/typescript-go
```

这条 `replace` 不改变访问规则：`tyneladapter` 位于模块树内，所以它能导入 `internal/*`；宿主位于模块树外，所以它只能导入 `tyneladapter`。`bridge/tsgo/check` 里有一个用例专门验证这一点：外部模块直接导入 `internal/compiler` 时，`go build` 必须报 `use of internal package … not allowed`。

`replace` 指向 `.work`，因此全新克隆要先运行准备命令；这是有意为之——前端源码不进版本库，身份由锁文件与归档哈希保证。

## 上游升级时改什么

1. 改 `frontend` 段的 `version`、`commit`、`archive`（新归档的 url、sha256、size）、`archiveRoot`；
2. 重算四个标记文件的 sha256；
3. `./tools/toolchain/prep --force --frontend-only` 重新解压；
4. `./bridge/tsgo/check`，它会同时验证适配包还能编译、诊断还能取到、关闭规则没有变。

适配层只有 159 行，升级时最可能需要改的是 `Open` 里的程序构造参数，而不是接口本身。

## 来源清单

| 项目 | 值 |
| --- | --- |
| 仓库 | `microsoft/typescript-go` |
| 标签 | `typescript/v7.0.2` |
| 提交 | `2bd066d87f5bafd315be9f40889d0a60b9e58e0b` |
| 许可证 | Apache-2.0（`LICENSE`） |
| 归档 | `typescript-go-2bd066d8.tar.gz`，20,375,158 字节 |
| 归档 sha256 | `5ccb47dbb3f68cd0da58b71e6f445eee36a50bd3e9f9f330cc23a97b88500119` |
| 模块 `go` 指令 | `go 1.26`（本机工具链 go1.27.3 满足） |
| 解压后体积 | 332 MiB |

归档地址是 `https://codeload.github.com/microsoft/typescript-go/tar.gz/<commit>`。本机访问 `github.com` 的 git 通道会被代理掐断（`CONNECT tunnel failed`），所以准备与复核都走 HTTPS：下载用 codeload，`--check-remote` 用 GitHub API 的 `/commits/<tag>` 比较提交。上游标签被移动时，复核按退出 8 失败，提示人工确认后再更新锁文件。
