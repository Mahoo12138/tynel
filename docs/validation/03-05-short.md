# 03.05 验收记录：布尔短路

本文件记录 03.05 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 03.05`；用 `git log --grep 'Task: 03.05'` 可以定位。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `e8c17577` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 正向验收

### 右侧被跳过

```text
$ ./bin/tynel run tests/source/testdata/short.ts
1
false
3
4
true
5
true
7
8
true
```

`mark(1, false) && mark(2, true)` 只打印 `1`，结果是 `false`；`mark(5, true) || mark(6, false)` 只打印 `5`，结果是 `true`。`2` 和 `6` 没有出现。

### 结果用于 const、条件与参数

```text
$ ./bin/tynel run tests/source/testdata/short-result.ts
1
2
false
3
5
6
7
true
```

`const a = mark(1, true) && mark(2, false)` 打印 `1`、`2`，`a` 是 `false`；`if (mark(3, true) || mark(4, false))` 只打印 `3`，然后打印 `5`；`both(mark(6), mark(7))` 打印 `6`、`7`。

### 嵌套与实参顺序

```text
$ ./bin/tynel run tests/source/testdata/short-nested.ts
1
2
3
false
4
5
6
false
7
8
9
false
```

第二组 `pair(mark(4, true), mark(5, true) && mark(6, false))` 先打印 `4`，再进入右侧短路打印 `5`、`6`；第三组先算左侧短路 `7`、`8`，再算第二个实参 `9`。前面的实参没有被推迟到短路之后。

### 产物与回放一致

```text
$ ./bin/tynel build tests/source/testdata/short.ts -o out/short
已生成 out/short（2429986 字节）
  源码     tests/source/testdata/short.ts（ad12b0222356）
  输入     a5373e3c1802
  编译器   e8c175774af2

$ ./bin/tynel dump nir tests/source/testdata/short.ts > out/short.nir.json
$ ./bin/tynel build-ir out/short.nir.json -o out/short-replay
已生成 out/short-replay（2429986 字节）
  输入     a5373e3c1802
  编译器   e8c175774af2

$ shasum -a 256 out/short out/short-replay
b9506843af30718341c46be3ff2cec3f4042cec827d7531f34d2ff6f32e018f6  out/short
b9506843af30718341c46be3ff2cec3f4042cec827d7531f34d2ff6f32e018f6  out/short-replay
```

### NIR 形状

```json
{ "op": "short", "name": "_tmp_1", "operator": "&&",
  "left":  {"op": "call", "function": "mark", "args": [...]},
  "right": [],
  "value": {"op": "call", "function": "mark", "args": [...]} }
```

左侧只出现一次；右侧准备列表为空时 `right` 省略。结果在后续表达式里用 `{"op":"local","name":"_tmp_1"}` 读取。

### bridge 直接构造短路

```text
$ ./bridge/go/check
PASS 加法输出 42
PASS 空入口可执行且无输出
PASS boolean 输出 true
PASS if 分支按条件输出
PASS 完整返回路径与不可达语句
PASS 短路跳过右侧并绑定结果
PASS 坏 NIR 在编译期被拒绝
PASS 普通构建不受影响
PASS 缺少支持函数被拒绝
```

`bridge/go/testdata/short.nir.json` 里 `false && mark(2)` 与 `true || mark(4)` 都跳过右侧，输出 `1`、`false`、`3`、`true`。

## 负向验收

| 输入 | 结果 | 退出 |
| --- | --- | --- |
| `true && 1` | `cap.bad-type`：右侧不是 boolean | 3 |
| `1 && true` | `cap.bad-type`：左侧不是 boolean | 3 |
| `false && (1 % 2 === 0)` | `cap.bad-operator`：未选中的右侧仍检查 | 3 |
| `true && announce()` | `cap.void-value`：void 没有值 | 3 |
| NIR 的 short 左侧为 number | `nir.operand-type` | 1 |
| NIR 的右侧局部值泄漏到外层 | `nir.unknown-local` | 1 |
| NIR 的 short 结果名与 let 重复 | `nir.duplicate-name` | 1 |

具体输出：

```text
$ ./bin/tynel check tests/source/testdata/errors/short-number.ts
…/errors/short-number.ts:2:21: 能力限制 cap.bad-type: 运算符 && 的操作数是 number，需要 boolean
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/short-left-number.ts
…/errors/short-left-number.ts:2:13: 能力限制 cap.bad-type: 运算符 && 的操作数是 number，需要 boolean
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/short-unselected.ts
…/errors/short-unselected.ts:2:23: 能力限制 cap.bad-operator: 运算符 "%" 不在本阶段支持的 + - * / 与比较之内
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/short-void.ts
…/errors/short-void.ts:6:9: 能力限制 cap.void-value: 运算符 && 的操作数需要一个值，但这里是 void 调用
$ echo $?
3
```

NIR 层的三条：

```text
$ ./bin/tynel build-ir /tmp/short-left.nir.json -o out/short-left
tynel: 源码错误 (nir.operand-type): short "&&" 的左侧必须是 bool，实际为 number
$ echo $?
1

$ ./bin/tynel build-ir /tmp/short-leak.nir.json -o out/short-leak
tynel: 源码错误 (nir.unknown-local): 引用了尚未定义的局部值 "x"
$ echo $?
1

$ ./bin/tynel build-ir /tmp/short-dup.nir.json -o out/short-dup
tynel: 源码错误 (nir.duplicate-name): 函数 "main" 里 "t" 重复定义
$ echo $?
1
```

失败的构建没有创建对应产物。

## 改动范围

- `compiler/nir`：新增 `short` 语句（`name`、`operator`、`left`、`right`、`value`），右侧准备块只接受 `let` / `short`；验证器检查两侧 `bool`、结果名唯一，并在右侧子作用域里检查 `value`。
- `compiler/lower`：表达式降低带准备语句列表；`&&` / `||` 降成 `short` + `local`；调用实参在需要时物化，保持源码求值顺序。
- `compiler/capability`：`&&` / `||` 两侧都必须是 `boolean`；未选中的右侧照样遍历。
- `bridge/tsgo`：括号表达式在骨架里直接展开，不再作为独立节点。
- `bridge/go`：`short` 展开成 `BlockStmt`：左侧赋值、条件 `if`、右侧准备块、结果写回。
- `tests/source`：新增 3 个短路程序与 4 个错误用例，用例总数 45 → 52。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 287 个测试（162 个顶层 + 125 个子测试） |
| `tests/source` | 52 个用例（7 个测试函数，77 个子测试） |
| `tests/native` | 8 个用例 |
| `./bridge/go/check` | 9 项全过 |
| `./bridge/tsgo/check` | 60 项全过 |
| `go vet ./...` | 无输出 |

Node 对照全部通过，`short`、`short-result`、`short-nested` 标记为 `shared`，输出逐字节相同。

## 阶段能力边界

- 支持 `boolean` 的 `&&` / `||`，结果绑定到临时值，可用于 const、条件、实参和返回值。
- 左侧恰好求值一次；`false && right()` 与 `true || right()` 跳过右侧。
- 两侧都必须是 `boolean`；不做数字真假转换。
- 右侧的准备工作留在条件内部；右侧局部值不泄漏。
- `while`、`for` 与三元式仍未实现。

## 未覆盖

- 没有非布尔操作数的短路（TypeScript 允许，本阶段拒绝）。
- 没有 `??`、`?.` 与可选链。
- 没有把短路结果直接当数值使用；类型系统不允许。
- 只在 `darwin/arm64` 上验证。
