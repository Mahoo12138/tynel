# 03.01 验收记录：boolean 成为完整的标量

本文件记录 03.01 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 03.01`；用 `git log --grep 'Task: 03.01'` 可以定位。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `32bd71d7` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 从代码准备到产物

```text
$ ./tools/toolchain/prep --quiet
/Users/mahoo/Projects/tynel/.work/toolchain/toolchain.json

$ ./bridge/go/build
已生成 …/.work/bridge/compile（戳记 …/.work/bridge/stamp.json）

$ ./tools/build
已生成 …/bin/tynel
```

## 正向验收

### `invert(false)` 输出 true

```text
$ ./bin/tynel run tests/source/testdata/boolean.ts
true
```

源码是 `function invert(value: boolean): boolean { return !value; }`，模块体调用 `invert(false)`。

### false 与 true 两个字面量

```text
$ ./bin/tynel run tests/source/testdata/boolean-literal.ts
false
true
```

`false` 没有被当成「缺字段」，也没有被省略成空输出。

### NIR 里的 boolean

```json
{ "name": "invert",
  "params": [{"name": "value", "type": "bool"}],
  "result": "bool",
  "body": [{"op": "return", "value": {"op": "not",
            "operand": {"op": "param", "name": "value"}}}] }

{ "name": "main", "params": null, "result": "void",
  "body": [{"op": "print", "value": {"op": "call", "function": "invert",
            "args": [{"op": "bool", "value": false}]}}, {"op": "return"}] }
```

参数与返回类型都是 `bool`，字面量带 `"value": false`，取反是 `not`。没有 `number` 的 0/1 转换。

### 产物与回放一致

```text
$ ./bin/tynel build tests/source/testdata/boolean.ts -o out/bool
已生成 out/bool（2429778 字节）
  源码     tests/source/testdata/boolean.ts（314419988a96）
  输入     413888c00677
  编译器   32bd71d71da8

$ ./out/bool
true

$ ./bin/tynel dump nir tests/source/testdata/boolean.ts > out/bool.nir.json
$ ./bin/tynel build-ir out/bool.nir.json -o out/bool-replay
已生成 out/bool-replay（2429778 字节）
  输入     413888c00677
  编译器   32bd71d71da8

$ shasum -a 256 out/bool out/bool-replay
a0083044222ff7fa4acb80dafe7d10993299284691a12cd15abe49fee57ece55  out/bool
a0083044222ff7fa4acb80dafe7d10993299284691a12cd15abe49fee57ece55  out/bool-replay
```

### 保存后读回仍是 false

`TestBoolSurvivesEncoding` 对 `true` 与 `false` 各做一次编码 → 解码 → 比较，并断言编码结果里出现 `"value":`；`false` 不是靠缺省值猜出来的。

### bridge 直接构造 bool

```text
$ ./bridge/go/check
PASS 加法输出 42
PASS 空入口可执行且无输出
PASS boolean 输出 true
PASS 坏 NIR 在编译期被拒绝
PASS 普通构建不受影响
PASS 缺少支持函数被拒绝
```

`bridge/go/testdata/bool.nir.json` 是手写的 boolean NIR，经 `-toolexec` 替换的编译器生成原生程序，输出 `true`。`bridge/tsgo/check` 里也加了 `boolean.ts`：源码经前端降低成 NIR，再由 `build-ir` 编成原生程序，输出 `true`。

## 负向验收

| 输入 | 结果 | 退出 |
| --- | --- | --- |
| `!1` | `cap.bad-type`：`! 的操作数是 number，需要 boolean` | 3 |
| `invert(1)` | TS2345：`Argument of type 'number' is not assignable to parameter of type 'boolean'` | 1 |
| 缺 `value` 的 bool 节点 | `nir.missing` | 1 |
| `not` 作用在 number 上 | `nir.operand-type` | 1 |
| boolean 形参收到 number 实参 | `nir.arg-type` | 1 |

具体输出：

```text
$ ./bin/tynel check tests/source/testdata/errors/bang-number.ts
…/errors/bang-number.ts:2:14: 能力限制 cap.bad-type: ! 的操作数是 number，需要 boolean
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/bool-arg.ts
…/errors/bool-arg.ts:6:20: error TS2345: Argument of type 'number' is not assignable to parameter of type 'boolean'.
$ echo $?
1

$ ./bin/tynel build-ir /tmp/missing-bool.nir.json -o out/missing-bool
tynel: 源码错误 (nir.missing): functions[0].body[0].value 的 bool 缺少 value
$ echo $?
1

$ ./bin/tynel build-ir /tmp/not-number.nir.json -o out/not-number
tynel: 源码错误 (nir.operand-type): not 的操作数必须是 bool，实际为 number
$ echo $?
1

$ ./bin/tynel build-ir /tmp/bool-arg.nir.json -o out/bool-arg
tynel: 源码错误 (nir.arg-type): 调用 "invert" 的第 1 个实参需要 bool，实际为 number
$ echo $?
1
```

失败的构建没有创建 `out/missing-bool`、`out/not-number`、`out/bool-arg`。

## 改动范围

- `compiler/nir`：新增 `bool` 类型与 `bool`、`not` 表达式；`Expr.Value` 用 `*bool` 区分 `false` 与缺失；验证器按参数声明的类型推导 `param`，`let` 记录值的类型。
- `compiler/frontend`：事实层没有新增字段；`true`/`false` 以 `TrueKeyword`/`FalseKeyword` 出现在语法骨架里。
- `compiler/capability`：声明、`!` 与 `console.log` 接受 boolean；`!` 的非 boolean 操作数按 `cap.bad-type` 拒绝。
- `compiler/lower`：`boolean` 映射到 `bool`，`!` 降成 `not`，参数、返回值、常量与实参按声明类型核对。
- `bridge/go`：`bool` 映射到 Go 的 `TBOOL`，`not` 对应 `ONOT`，打印走 `tynelPrintBool`；局部值与丢弃临时值按表达式类型分配。
- `tests/source`：新增 `boolean.ts`、`boolean-literal.ts`、`errors/bang-number.ts`、`errors/bool-arg.ts`，用例总数 14 → 18。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 220 个测试（147 个顶层 + 73 个子测试） |
| `tests/source` | 18 个用例（7 个测试函数，28 个子测试） |
| `tests/native` | 8 个用例 |
| `./bridge/go/check` | 6 项全过 |
| `./bridge/tsgo/check` | 48 项全过 |
| `go vet ./...` | 无输出 |

Node 对照全部通过，`boolean` 与 `boolean-literal` 标记为 `shared`，输出逐字节相同。

## 阶段能力边界

- `boolean` 现在可以作为常量、参数、返回值和 `console.log` 的参数。
- `!` 只接受 `boolean`；`!number` 被拒绝，没有数字真假转换。
- 比较、`&&`、`||`、`if`、`while` 和三元式仍未实现，按能力限制拒绝。
- 字符串、对象、数组和模块导入仍不支持。

## 未覆盖

- 没有布尔运算 `&`、`|`、`^`；它们不在本阶段子集内。
- 没有短路求值：`&&` / `||` 留给 03.05。
- 没有 `boolean` 的比较或排序；比较在 03.02 加入。
- 只在 `darwin/arm64` 上验证。
