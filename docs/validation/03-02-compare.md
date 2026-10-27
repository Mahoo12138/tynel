# 03.02 验收记录：比较与严格相等

本文件记录 03.02 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 03.02`；用 `git log --grep 'Task: 03.02'` 可以定位。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `385030c5` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 正向验收

### 有序比较与严格相等

```text
$ ./bin/tynel run tests/source/testdata/compare.ts
true
true
true
false
true
false
true
false
```

八行依次是 `1 < 2`、`2 <= 2`、`3 > 2`、`2 >= 3`、`1 === 1`、`1 !== 1`、`same(true, true)`、`same(true, false)`。

### 特殊值

```text
$ ./bin/tynel run tests/source/testdata/compare-specials.ts
false
false
false
false
false
true
true
false
true
true
```

十行依次是 `NaN < 1`、`NaN <= 1`、`NaN > 1`、`NaN >= 1`、`NaN === NaN`、`NaN !== NaN`、`-0 === 0`、`-0 < 0`、`1/0 > 1e308`、`-1/0 < 0`。

如果实现把 `a < b` 改写成 `!(a >= b)`，前四行里会出现 `true`，这组用例会立刻失败。

### 求值顺序

```text
$ ./bin/tynel run tests/source/testdata/compare-order.ts
1
2
true
```

`mark(1)` 与 `mark(2)` 各自打印一次，顺序与源码一致，最后才打印比较结果。

### 产物与回放一致

```text
$ ./bin/tynel build tests/source/testdata/compare.ts -o out/compare
已生成 out/compare（2429778 字节）
  源码     tests/source/testdata/compare.ts（2f78dbfef709）
  输入     33180ac2aa9a
  编译器   385030c529bd

$ ./bin/tynel dump nir tests/source/testdata/compare.ts > out/compare.nir.json
$ ./bin/tynel build-ir out/compare.nir.json -o out/compare-replay
已生成 out/compare-replay（2429778 字节）
  输入     33180ac2aa9a
  编译器   385030c529bd

$ shasum -a 256 out/compare out/compare-replay
4d361784cfa57ab4020d3f05485794b103e5649fb54caad3b722ccf329efb440  out/compare
4d361784cfa57ab4020d3f05485794b103e5649fb54caad3b722ccf329efb440  out/compare-replay
```

### NIR 形状

```json
{"op": "compare", "operator": "<",
 "left":  {"op": "number", "bits": "0x3ff0000000000000"},
 "right": {"op": "number", "bits": "0x4000000000000000"}}
```

比较是一个独立的 `compare` 节点，不是把 `<` 塞进算术 `binary`。这样结果类型由 `op` 决定：`binary` 永远是 `number`，`compare` 永远是 `bool`。

## 负向验收

| 输入 | 结果 | 退出 |
| --- | --- | --- |
| `1 == 1` | `cap.bad-operator`：宽松相等不在子集内 | 3 |
| `1 === true` | TS2367：两侧类型没有重叠 | 1 |
| `true < false` | `cap.bad-type`：比较操作数是 boolean | 3 |
| NIR 里 `compare` 用 `==` | `nir.bad-op` | 1 |
| NIR 里有序比较用 bool | `nir.operand-type` | 1 |
| NIR 里严格相等混合类型 | `nir.operand-type` | 1 |

具体输出：

```text
$ ./bin/tynel check tests/source/testdata/errors/loose-equal.ts
…/errors/loose-equal.ts:2:13: 能力限制 cap.bad-operator: 宽松相等 "==" 不在本阶段子集内，请使用 ===
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/mixed-compare.ts
…/errors/mixed-compare.ts:2:13: error TS2367: This comparison appears to be unintentional because the types 'number' and 'boolean' have no overlap.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/bool-order.ts
…/errors/bool-order.ts:2:13: 能力限制 cap.bad-type: 比较 < 的操作数是 boolean，需要 number
…/errors/bool-order.ts:2:20: 能力限制 cap.bad-type: 比较 < 的操作数是 boolean，需要 number
$ echo $?
3

$ ./bin/tynel build-ir /tmp/compare-loose.nir.json -o out/compare-loose
tynel: 源码错误 (nir.bad-op): functions[0].body[0].value.operator 为 "=="，只接受 <、<=、>、>=、===、!==
$ echo $?
1

$ ./bin/tynel build-ir /tmp/compare-bool.nir.json -o out/compare-bool
tynel: 源码错误 (nir.operand-type): 有序比较 "<" 需要两个 number，实际为 bool 与 bool
$ echo $?
1

$ ./bin/tynel build-ir /tmp/compare-mixed.nir.json -o out/compare-mixed
tynel: 源码错误 (nir.operand-type): 严格相等 "===" 需要两侧同类型，实际为 number 与 bool
$ echo $?
1
```

失败的构建没有创建 `out/compare-loose`、`out/compare-bool`、`out/compare-mixed`。

## 过程中发现的问题：宿主没有跟踪 compiler 包

第一次运行 `compare-order.ts` 时，命令行仍报旧的「运算符 `<` 不在 `+ - * /` 之内」。原因是前端宿主的戳记只哈希了 `bridge/tsgo/consumer` 与适配包，没有哈希宿主通过 `replace` 链接的 `compiler/*`。改了 `compiler/capability` 之后，宿主二进制没有重建，命令行继续用旧的能力规则。

修正方式是让 `internal/host.hashSources` 递归遍历 `compiler/`，把所有非测试 `.go` 文件纳入戳记。`TestEnsureRebuildsWhenCompilerChanges` 锁定这条行为：改 `compiler/capability/capability.go` 后，`Ensure` 必须重建一次。这个修复对 03.01 也有意义——那一次宿主重建是因为适配包变了，属于碰巧。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 238 个测试（153 个顶层 + 85 个子测试） |
| `tests/source` | 24 个用例（7 个测试函数，37 个子测试） |
| `tests/native` | 8 个用例 |
| `./bridge/go/check` | 6 项全过 |
| `./bridge/tsgo/check` | 48 项全过 |
| `go vet ./...` | 无输出 |

Node 对照全部通过，`compare`、`compare-specials`、`compare-order` 标记为 `shared`，输出逐字节相同。

## 阶段能力边界

- 比较支持 `<` `<=` `>` `>=` `===` `!==`；宽松 `==` / `!=` 拒绝。
- 有序比较只接受 `number`；`boolean` 没有顺序，`true < false` 按能力限制拒绝。
- 严格相等只支持同类 `number` 或同类 `boolean`。
- 字符串、对象和数组还不能比较；`if`、`while`、`&&`、`||` 与三元式仍未实现。

## 未覆盖

- 没有字符串相等、对象身份比较和数组比较。
- 没有宽松相等的隐式转换。
- 没有短路求值；`&&` / `||` 留给 03.05。
- 只在 `darwin/arm64` 上验证。
