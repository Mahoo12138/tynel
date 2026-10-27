# 命令行参考

本页说明 `tynel` 提供的命令、退出状态、编译流程，以及命令行与外部工具的分工。用户命令是 `check`、`build`、`run`、`dump`；`version`、`help`、`toolchain`、`build-ir` 是开发命令。

## 构建命令行本身

```zsh
# 在项目根目录构建开发用入口；需要 01.01 准备的隔离 Go。
./tools/build

# 显示版本与锁定的工具链身份。
./bin/tynel version
```

`bin/` 不进入版本库，换一份工作副本要重新构建。构建脚本使用 `.work/toolchain` 下的隔离 Go，不使用宿主安装。

## 用户命令

| 命令 | 作用 | 成功输出 |
| --- | --- | --- |
| `tynel check <file.ts> [--json]` | 类型检查、能力判断、降低并验证 NIR，不生成机器码 | 一行摘要；`--json` 时输出完整结果 |
| `tynel build <file.ts> -o <路径>` | 编译成原生程序，不运行 | 产物路径、源码与 NIR 哈希、构建记录位置 |
| `tynel run <file.ts>` | 编译并运行本次产物 | 程序自己的输出 |
| `tynel dump facts <file.ts>` | 输出前端事实 | JSON |
| `tynel dump nir <file.ts>` | 输出降低后的 NIR | JSON |

```zsh
# 检查、编译、运行；示例在 examples/add.ts。
./bin/tynel check examples/add.ts
./bin/tynel build examples/add.ts -o out/add
./out/add
./bin/tynel run examples/add.ts

# 机器可读的输出。
./bin/tynel check examples/add.ts --json
./bin/tynel dump facts examples/add.ts
./bin/tynel dump nir examples/add.ts
```

## 开发命令

| 命令 | 作用 |
| --- | --- |
| `tynel version` | 程序版本、编译时提交、目标平台、锁定的 Go 身份与准备状态 |
| `tynel help [命令]` | 总体用法或单个命令说明 |
| `tynel toolchain` | 列出 SDK 目录与 `go`、`compile`、`link`、`asm` 的实际路径 |
| `tynel toolchain which <名>` | 只解析一个工具，输出绝对路径 |
| `tynel build-ir <NIR> -o <路径>` | 把一份 NIR 编成原生程序，不读 TypeScript |

`build-ir` 保留下来是因为它跳过了前端：手写 NIR、检查后端、复现产物都能用它，不必先有一份能通过类型检查的源码。

## 一次编译经过哪些步骤

`check`、`build`、`run` 共用同一条前半程：

1. **准备前端宿主**。宿主是 `bridge/tsgo/consumer`，一个独立模块里的子进程。它缺失或与源码不一致时，命令行用隔离工具链重建它，进度写到标准错误。
2. **源码检查**。宿主把 TypeScript 诊断连同 UTF-8 字节区间与行列一起交回来。
3. **能力检查**。判断这段程序是否落在原生子集内，结论与源码错误分开。
4. **降低与验证**。事实降低成 NIR，再由 `nir.Verify` 检查引用、实参和类型。

`check` 到这里就停。`build` 与 `run` 继续：

5. **后端**。NIR 写成快照，交给修改版 Go 编译器，经 `-toolexec` 包装的 `compile` 生成目标文件，再由 Go 自己的 `link` 链接。
6. **发布**。产物先写到目标目录的临时文件，成功后才改名。`run` 在发布之后立即执行它。

## 运行依赖

要把两件事分开：

| 谁 | 需要什么 |
| --- | --- |
| 编译器本身（`tynel`） | 已准备的 `.work/toolchain`；前端源码树与模块缓存（首次构建宿主时）；`build`/`run` 还需要 `.work/bridge/compile` |
| 被编译出来的程序 | 无。它是静态链接的 Go 二进制，不依赖 Node、不依赖 Go 工具链，也不需要本项目 |

`check` 不需要后端：它不生成机器码，也不检查 SDK 或修改版编译器是否就绪。`check` 通过只说明这份程序可以进入后端，不保证 `build` 能成功——那要看工具链有没有准备好。

## check

```zsh
./bin/tynel check bridge/tsgo/testdata/add.ts
# ok: /…/bridge/tsgo/testdata/add.ts（2 个函数，NIR 已验证）

./bin/tynel check bridge/tsgo/testdata/bad.ts
# bridge/tsgo/testdata/bad.ts:2:7: error TS2322: Type 'string' is not assignable to type 'number'.

./bin/tynel check bridge/tsgo/testdata/capability/any.ts
# bridge/tsgo/testdata/capability/any.ts:1:1: 能力限制 cap.any: 函数 "identity" 的返回类型是 any 或 unknown…
```

默认把诊断写到标准错误，成功时在标准输出打一行摘要。`--json` 把完整结果写到标准输出：诊断、能力结论、函数数量与 `verified`。此时标准错误为空，进度与结果不混。

## build 与构建记录

```zsh
./bin/tynel build examples/add.ts -o out/add
```

`-o` 可以写在源文件前面或后面，也可以用 `-o=out/add`。

成功后在同目录写 `<产物>.build.json`：

| 字段 | 内容 |
| --- | --- |
| `source` | 源文件路径、sha256、字节数；从 NIR 构建时没有这一项 |
| `input` | 实际被编译的 NIR 快照路径、sha256、字节数 |
| `toolchain` | 锁定的 Go 版本与目标平台 |
| `tools` | `go`、修改版 `compiler` 及其 sha256、`toolexec`、build tag、快照路径 |
| `output` | 产物路径、sha256、字节数 |

源码 → NIR → 产物是一条哈希链：`source.sha256` 对上源码，`input.sha256` 对上降低结果，`output.sha256` 对上产物。三者都写下来，回放时才能确认「编译的是这份源码」。

产物与记录的一致性策略：

- 产物发布成功后才写记录；记录写不进去时产物会被删除。没有记录的产物无法复核来源，留着比没有更危险。
- 记录里的 `input.path` 指向 `.work/build/snapshots/` 下的快照，不是内存地址，也不是用户临时文件；快照按内容哈希命名，内容与名字不符时构建直接拒绝。
- `tools.buildTag` 同时包含 NIR 与编译器哈希。Go 的缓存键既不含 NIR 内容，也不含被 `-toolexec` 替换掉的编译器，这个标签让两者都参与缓存区分。

修改版编译器带戳记 `.work/bridge/stamp.json`。bridge 源码或 SDK 源码变了而编译器没重编时，构建按「编译器已过期」退出 4，不产出任何文件。

## run

```zsh
./bin/tynel run examples/add.ts
# 42
```

`run` 每次都重新编译，产物固定放在 `.work/run/app`，然后执行它。它不执行任何先前存在的文件：构建失败就直接返回，即使目标位置留着上一次的产物也不会被运行。

程序的标准输出、标准错误直接连到终端。程序退出状态原样作为 `run` 的退出状态——这与 `go run` 一致，也意味着程序返回 1 时看起来和源码错误一样。编译失败时仍用 1、3、4、5 这几类。

## dump

`dump facts` 与 `dump nir` 只输出 JSON，没有进度日志。源码有错退出 1，超出原生子集退出 3，两种情况都不输出 JSON。

事实的字段、ID 与校验规则见[声明身份与事实](facts.md)，NIR 的格式见 [NIR 格式](nir.md)。

## 退出状态

| 状态 | 类别 | 典型原因 |
| --- | --- | --- |
| 0 | 成功 | 命令完成 |
| 1 | 源码错误 | 语法、类型、未定义名称 |
| 2 | 用法错误 | 未知命令、缺少参数、多余的参数、未知帮助主题、未知工具名 |
| 3 | 能力限制 | 写法合法但当前原生后端不支持 |
| 4 | 环境缺失 | 不在项目目录内、工具链或前端源码未准备、编译器过期、输出目录不可写 |
| 5 | 内部故障 | 编译器自身、前端宿主或它调用的外部进程失败 |

`run` 例外：程序成功启动后，它的退出状态原样传出。

退出状态只由 `compiler/diagnostic` 的类别决定，命令实现不各自约定数字。几类失败分开，是为了让「工具没装好」不被读成「用户代码有错」。外部进程以非零状态退出时，诊断类别是内部故障，并保留该进程的 stderr 作为原因。

## 诊断与机器可读输出

- 标准输出只放命令的结果：`check` 的摘要或 JSON、`build` 的产物信息、`dump` 的 JSON、`run` 的被运行程序输出。
- 标准错误放诊断、进度和宿主的重建提示。
- `check --json` 是唯一会同时产出机器可读结果与退出状态的命令；其余命令的 JSON 只出现在 `dump`，且失败时不输出。

## 命令行与外部工具的职责

命令行负责四件事：解析参数、准备前端宿主、按结果选择诊断类别、把结果写到标准输出或标准错误。它不解析 TypeScript，也不解析 Go 的内部数据结构。

外部工具通过 `internal/toolchain` 的 `Runner` 接口调用：

- `LookPath` 解析工具路径；
- `Run` 启动工具并收集标准输出、标准错误和退出状态，非零退出作为数据返回，只有无法启动才算调用失败；
- `RunEnv` 在给定环境下做同样的事，构建与宿主重建需要它来设置 `TYNEL_NIR`、`GOMODCACHE` 等变量。

真实实现用操作系统进程；测试用固定工具表替换它，因此错误路径不依赖本机是否真的安装了某个工具。工具路径来自 01.01 写出的 `.work/toolchain/toolchain.json`，本包不重复推导目录命名。

## 身份从哪里来

`version` 的 Go 版本、源码提交和许可证来自 `tools/toolchain/go.lock.json`，准备状态来自 `.work/toolchain/toolchain.json`。程序不内置第二份常量，避免与准备入口记录的版本不一致。

不在项目目录内运行时，`version` 仍输出程序版本，但把目标平台标为未知并以 0 退出；需要项目的命令按环境缺失以 4 退出。

## 尚未提供

`string`、循环、对象和数组都不在当前子集内，`check` 会按能力限制拒绝。`import`、模块解析和标准库也不支持：一个文件就是一个程序。命令集合在 06.01 才固定，之前仍可能增删。
