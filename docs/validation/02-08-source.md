# 02.08 验收记录：源码到原生的回归集合

本文件记录 02.08 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 02.08`；用 `git log --grep 'Task: 02.08'` 可以定位。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 覆盖范围

`tests/source/testdata/cases.json` 是唯一的用例清单，14 条：

| 种类 | 数量 | 内容 |
| --- | --- | --- |
| `program` | 8 | 加法、求值顺序、嵌套调用、声明提升、void 函数、丢弃结果、大数、特殊值 |
| `source-error` | 1 | 类型错误（TS2322） |
| `capability` | 4 | 取余、any、void 当值、控制流 |
| `environment` | 1 | 文件不存在 |

不在范围内：控制流、字符串、对象、数组、模块导入、并发和长时间运行的程序。这些写法目前会被能力检查拒绝，回归集合不假装支持它们。

## 参考配置

`tests/source/reference.json` 锁定对照运行时与对照方式：

```json
{"runtime": "node", "version": "v22.22.2", "major": 22,
 "command": "node --experimental-strip-types <file>"}
```

每个程序用例标注对照方式：

- `shared`：Tynel 与 Node 的输出应逐字节相同。
- `spelling`：只有特殊值拼写不同，归一后必须相同。目前只有 `specials` 属于这一类——Tynel 写 `+Inf` / `-Inf`，Node 写 `Infinity` / `-Infinity`。

这条区分是有意的：不能因为「无穷写法不一样」就放过整个用例，也不能把 Tynel 的拼写改成 Node 的来「对齐」。测试把 Tynel 的拼写归一后再比，差异只剩已记录的那一处。

`TestNodeReference` 只在 Node 主版本与锁定的 22 一致时执行；版本不符时跳过并说明原因。跳过不是通过：黄金输出比对由 `TestPrograms` 独立完成，不依赖 Node。

## 正向验收

八个程序全部编译、运行并与参考输出一致：

| 用例 | 输出 |
| --- | --- |
| `add` | `42` |
| `order` | `1`、`2`、`3`（实参从左到右求值） |
| `nested` | `11` |
| `forward` | `42`（声明提升） |
| `void` | `7`、`8`（每次调用恰好一行） |
| `discard` | `42`（`answer;` 不打印） |
| `big` | `1e+21` |
| `specials` | `0.30000000000000004`、`-0`、`+Inf`、`-Inf`、`NaN` |

身份链（`tests/source/testdata/add.ts`）：

```text
source 552736811d40448b（111 字节）
nir    985b1c376c305b87（3034 字节）
output 7acf139a9d39252e（2429986 字节）
tag    tynel_985b1c37_1ac6e266
```

用记录里的快照回放，产物哈希仍是 `7acf139a9d39252e`，逐字节相同。源码、NIR、产物三者的哈希互相对应，`TestIdentityChain` 逐项核对。

序列化与算术分开验证：`TestSerialization` 只检查 NIR 编码往返（编码 → 解码 → 编码，字节相同）和事实解码往返（结构相同）；算术结果由 `TestPrograms` 的运行时输出负责。

## 负向验收

| 用例 | 结果 | 退出 |
| --- | --- | --- |
| `type-error` | TS2322 | 1 |
| `modulo` | `cap.bad-operator` | 3 |
| `any` | `cap.any` | 3 |
| `void-value` | `cap.void-value` | 3 |
| `control-flow` | `cap.unsupported-syntax` | 3 |
| `missing-file` | 环境缺失 | 4 |

另外三条约束由测试结构保证：

- **零用例不算通过**：`TestManifest` 在清单为空、缺某一类用例或名字重复时直接失败；`tools/test` 在没有任何包报告 `ok` 时也按失败处理。
- **超时不算通过**：运行产物带 30 秒超时，超时直接失败，不会挂住测试进程。
- **不能删行迎合参考**：比较的是完整标准输出，没有任何过滤；黄金输出只能手工改 `cases.json`，没有 `-update` 开关。

`TestBuildFailureKeepsArtifact` 覆盖构建失败时的产物保护：先成功构建，再用一份入口指向不存在函数的 NIR 构建到同一路径，产物与构建记录的哈希都不变。

## 测试计数

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 203 个测试（含子测试） |
| `tests/source` | 29 个用例（6 个测试函数，23 个子测试） |
| `tests/native` | 8 个用例 |
| `./bridge/tsgo/check` | 47 项全过 |
| `go vet ./...` | 无输出 |

## 过程中的取舍

### 无穷的写法差异没有被「抹平」

第一次把 `specials` 标成 `shared` 时对照失败：Node 输出 `Infinity`，Tynel 输出 `+Inf`。可以改 Tynel 去迎合 Node，也可以把这一条从对照里删掉。前者会提前定下数值语义（03.x 才该定），后者会让「唯一差异」变成没人知道的黑洞。最后选择第三种：记录差异，测试把 Tynel 的拼写归一后再比，差异只剩这一处，且写在 `reference.json` 里。

### 黄金输出没有自动更新开关

`go test -update` 之类的开关会让「改代码 → 一键更新参考 → 测试通过」变成默认路径，参考结果就不再是独立证据。这里没有这个开关：输出变了就要手工改 `cases.json`，改的时候必须看一眼旧值和新值。

### Node 版本不符时跳过而不是失败

锁定的是主版本 22。主版本相同、次版本不同时语义没有变化，照常对照；主版本不同时跳过并说明，因为 `--experimental-strip-types` 的行为可能变。跳过会在测试输出里显示 SKIP，不伪装成 PASS。

## 怎么运行

```zsh
# 全部测试；前置检查会提示缺哪个依赖。
./tools/test

# 只跑源码回归。
./.work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go test -count=1 -v ./tests/source
```
