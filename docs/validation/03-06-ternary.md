# 03.06 验收记录：三元式

本文件记录 03.06 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 03.06`；用 `git log --grep 'Task: 03.06'` 可以定位。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `02ba7de9` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 正向验收

### 只执行选中的分支

```text
$ ./bin/tynel run tests/source/testdata/ternary.ts
1
10
4
20
```

`true ? pick(1, 10) : pick(2, 20)` 只调用 `pick(1, 10)`；`false ? pick(3, 10) : pick(4, 20)` 只调用 `pick(4, 20)`。`2` 和 `3` 没有出现。

### 结果用于 const、参数与 return

```text
$ ./bin/tynel run tests/source/testdata/ternary-result.ts
1
10
20
5
```

`const a = true ? 1 : 2` 得到 `1`；`choose(true)` 与 `choose(false)` 分别返回 `10`、`20`；`add(true ? 1 : 2, false ? 3 : 4)` 得到 `1 + 4 = 5`，两个实参的三元式各求值一次。

### 嵌套与分支内部准备

```text
$ ./bin/tynel run tests/source/testdata/ternary-nested.ts
1
2
20
3
4
40
6
7
true
```

第一组 `mark(1, false) ? 10 : mark(2, true) ? 20 : 30` 打印 `1`、`2`，得到 `20`；第二组只打印 `3`、`4`，不调用 `mark(5)`；第三组的分支里是短路，打印 `6`、`7`，得到 `true`。

### 产物与回放一致

```text
$ ./bin/tynel build tests/source/testdata/ternary.ts -o out/ternary
已生成 out/ternary（2429906 字节）
  源码     tests/source/testdata/ternary.ts（c2d8423837dd）
  输入     9d584cf62edc
  编译器   02ba7de95dd3

$ ./bin/tynel dump nir tests/source/testdata/ternary.ts > out/ternary.nir.json
$ ./bin/tynel build-ir out/ternary.nir.json -o out/ternary-replay
已生成 out/ternary-replay（2429906 字节）
  输入     9d584cf62edc
  编译器   02ba7de95dd3

$ shasum -a 256 out/ternary out/ternary-replay
3e8bc40e742a29d75ed4024eb412da0fa5db06b41da0f0a24336ed4e22cc3b34  out/ternary
3e8bc40e742a29d75ed4024eb412da0fa5db06b41da0f0a24336ed4e22cc3b34  out/ternary-replay
```

### NIR 形状

```json
{ "op": "select", "name": "_tmp_1", "type": "number",
  "cond": {"op": "bool", "value": true},
  "then": [],
  "thenValue": {"op": "call", "function": "pick", "args": [...]},
  "else": [],
  "elseValue": {"op": "call", "function": "pick", "args": [...]} }
```

条件只出现一次；两条分支各自保留，结果绑定到 `_tmp_1`，后续用 `local` 读取。

### bridge 直接构造三元式

```text
$ ./bridge/go/check
PASS 加法输出 42
PASS 空入口可执行且无输出
PASS boolean 输出 true
PASS if 分支按条件输出
PASS 完整返回路径与不可达语句
PASS 短路跳过右侧并绑定结果
PASS 三元式只执行选中分支
PASS 坏 NIR 在编译期被拒绝
PASS 普通构建不受影响
PASS 缺少支持函数被拒绝
```

`bridge/go/testdata/ternary.nir.json` 的条件分别为 `true` 与 `false`，输出 `1`、`10`、`4`、`20`。

## 负向验收

| 输入 | 结果 | 退出 |
| --- | --- | --- |
| `1 ? 2 : 3` | `cap.bad-type`：条件不是 boolean | 3 |
| `true ? 1 : false` | `cap.bad-type`：两臂类型不同 | 3 |
| `false ? (1 % 2) : 3` | `cap.bad-operator`：未选中的分支仍检查 | 3 |
| 两臂都是 void | `cap.bad-type`：没有可绑定的结果 | 3 |
| NIR 两臂类型不同 | `nir.result-type` | 1 |
| NIR 声明类型与分支不同 | `nir.result-type` | 1 |
| NIR 分支局部值泄漏到外层 | `nir.unknown-local` | 1 |

具体输出：

```text
$ ./bin/tynel check tests/source/testdata/errors/ternary-cond.ts
…/errors/ternary-cond.ts:2:1: 能力限制 cap.bad-type: 三元式的条件是 number，需要 boolean
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/ternary-mixed.ts
…/errors/ternary-mixed.ts:2:1: 能力限制 cap.bad-type: 三元式两条分支类型不同：number 与 boolean
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/ternary-unselected.ts
…/errors/ternary-unselected.ts:2:10: 能力限制 cap.bad-operator: 运算符 "%" 不在本阶段支持的 + - * / 与比较之内
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/ternary-void.ts
…/errors/ternary-void.ts:6:1: 能力限制 cap.bad-type: 三元式的结果不能是 void
$ echo $?
3
```

NIR 层的三条：

```text
$ ./bin/tynel build-ir /tmp/select-mismatch.nir.json -o out/select-mismatch
tynel: 源码错误 (nir.result-type): select 的两条分支类型不同：number 与 bool
$ echo $?
1

$ ./bin/tynel build-ir /tmp/select-type.nir.json -o out/select-type
tynel: 源码错误 (nir.result-type): select 声明结果 bool，两条分支是 number
$ echo $?
1

$ ./bin/tynel build-ir /tmp/select-leak.nir.json -o out/select-leak
tynel: 源码错误 (nir.unknown-local): 引用了尚未定义的局部值 "x"
$ echo $?
1
```

失败的构建没有创建对应产物。

## 改动范围

- `compiler/nir`：新增 `select` 语句（`name`、`cond`、`then`、`thenValue`、`else`、`elseValue`、`type`）；分支准备块只接受 `let` / `short` / `select`；验证器检查条件 `bool`、两臂同类型且与 `type` 一致、结果名唯一。
- `compiler/lower`：`ConditionalExpression` 降成 `select` + `local`；分支准备留在各自的 `then` / `else` 列表里。
- `compiler/capability`：条件必须 `boolean`，两臂同为 `number` 或同为 `boolean`；未选中的分支照样遍历。
- `bridge/tsgo`：联合类型中的纯数字/纯布尔字面量压成标量类别，`true ? 1 : 2` 不再落到 `other`。
- `bridge/go`：`select` 展开成 `BlockStmt` 内的 `IfStmt`，两个分支各执行准备块并给同一个局部值赋值；局部值类型取自 NIR 的 `type`。
- `tests/source`：新增 3 个三元式程序与 4 个错误用例，用例总数 52 → 59。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 301 个测试（166 个顶层 + 135 个子测试） |
| `tests/source` | 59 个用例（7 个测试函数，87 个子测试） |
| `tests/native` | 8 个用例 |
| `./bridge/go/check` | 10 项全过 |
| `./bridge/tsgo/check` | 63 项全过 |
| `go vet ./...` | 无输出 |

Node 对照全部通过，`ternary`、`ternary-result`、`ternary-nested` 标记为 `shared`，输出逐字节相同。

## 阶段能力边界

- 支持 `cond ? a : b`，结果可用于 const、条件、实参和 return。
- 条件必须是 `boolean`；两臂必须同类型，且是 `number` 或 `boolean`。
- 只有选中的分支执行；分支内部的短路、嵌套三元式留在分支里。
- 联合类型（`number | false`）与 `void` 分支被拒绝。
- `while`、`for` 仍未实现。

## 未覆盖

- 没有联合类型、`undefined` 与可选值。
- 没有 `switch` 与 `try`。
- 没有把三元式直接当条件使用；结果类型必须显式为 `boolean`。
- 只在 `darwin/arm64` 上验证。
