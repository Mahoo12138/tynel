# 04.06 验收记录：状态与控制组合

本文件记录 04.06 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 04.06`；用 `git log --grep 'Task: 04.06'` 可以定位。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `7a75b4ac` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 组合观察

| 程序 | 输入 | 输出 |
| --- | --- | --- |
| `combo-sum.ts` | `sumTo(0)`、`sumTo(1)`、`sumTo(10)` | `0`、`1`、`55` |
| `combo-fact.ts` | `factIter(6)`、`factRec(6)`、`factIter(7)`、`factRec(7)` | `720`、`720`、`5040`、`5040` |
| `combo-branch.ts` | `pick(true)`、`pick(false)` | `2`、`3` |
| `combo-early.ts` | `early(5)`、`early(1)` | `3`、`0` |
| `combo-mixed.ts` | `score(4, true)`、`score(4, false)` | `15`、`6` |

```text
$ ./bin/tynel run tests/source/testdata/combo-sum.ts
0
1
55

$ ./bin/tynel run tests/source/testdata/combo-fact.ts
720
720
5040
5040

$ ./bin/tynel run tests/source/testdata/combo-branch.ts
2
3

$ ./bin/tynel run tests/source/testdata/combo-early.ts
3
0

$ ./bin/tynel run tests/source/testdata/combo-mixed.ts
15
6
```

`combo-fact.ts` 的迭代与递归阶乘在每个输入上给出同一个值；`combo-early.ts` 的 `early(5)` 在 `i === 2` 时返回 `total = 3`，`early(1)` 的循环只执行一次后返回 `0`。组合表与复述见[状态与控制组合](../reference/combinations.md)。

## 产物与回放一致

```text
$ ./bin/tynel build tests/source/testdata/combo-mixed.ts -o out/combo
已生成 out/combo（2429906 字节）
  源码     tests/source/testdata/combo-mixed.ts（6e28411c2315）
  输入     76ad6cd9e0d1
  编译器   7a75b4acf10b
  构建记录 out/combo.build.json

$ ./bin/tynel dump nir tests/source/testdata/combo-mixed.ts > out/combo.nir.json
$ ./bin/tynel build-ir out/combo.nir.json -o out/combo-replay
已生成 out/combo-replay（2429906 字节）
  输入     76ad6cd9e0d1
  编译器   7a75b4acf10b
  构建记录 out/combo-replay.build.json

$ shasum -a 256 out/combo out/combo-replay
bbbf243c81d142ee2b23f2dd658454b66442cc0d0289e3a92eecb4367287d0c8  out/combo
bbbf243c81d142ee2b23f2dd658454b66442cc0d0289e3a92eecb4367287d0c8  out/combo-replay
```

## 超时记录

`tests/native/fixtures/combination.nir.json` 在同一个模块里放 `sumTo` 循环和 `factRec` 递归，入口输出 `55` 与 `120`：

```text
$ go test -count=1 -run TestCombinationTerminates -v ./tests/native
=== RUN   TestCombinationTerminates
--- PASS: TestCombinationTerminates (0.53s)
PASS
ok  	github.com/Mahoo12138/tynel/tests/native	0.941s
```

测试用独立进程和 5 秒超时；组合后的程序仍在有限时间内结束。

## 负向验收

| 破坏点 | 输入 | 结果 | 退出 |
| --- | --- | --- | --- |
| 返回路径 | `combo-bad-return.ts` 只有循环体 `return` | `TS2366` | 1 |
| 赋值目标类型 | `combo-bad-assign.ts` 把 `number` 写成 `boolean` | `TS2322` | 1 |
| 循环目标 | NIR 的 `break` 指向外层循环 | `nir.loop-target` | 1 |
| 返回路径 | NIR 的 `while` 体里只有 `return` | `nir.missing-return` | 1 |
| 赋值目标 | NIR 的 `set` 写入 `const` | `nir.immutable-local` | 1 |

```text
$ ./bin/tynel check tests/source/testdata/errors/combo-bad-return.ts
…/errors/combo-bad-return.ts:2:32: error TS2366: Function lacks ending return statement and return type does not include 'undefined'.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/combo-bad-assign.ts
…/errors/combo-bad-assign.ts:5:5: error TS2322: Type 'boolean' is not assignable to type 'number'.
$ echo $?
1
```

循环目标、NIR 返回路径和 NIR 赋值目标由 `compiler/nir/verify_test.go` 里更小的坏模块覆盖：`break 目标不是当前循环`、`while 体 return 不算完整返回`、`set 目标不可变`。没有为了组合放宽检查。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 387 个测试（179 个顶层 + 208 个子测试） |
| `tests/source` | 105 个用例（7 个测试函数，153 个子测试） |
| `cmd/tynel` | 25 个测试函数，6 个子测试 |
| `tests/native` | 12 个用例 |
| `./bridge/go/check` | 13 项全过 |
| `./bridge/tsgo/check` | 83 项全过 |
| `go vet ./...` | 无输出 |

## 阶段能力边界

- 本任务不新增语言特性，只验证已有规则的组合。
- 赋值、循环、递归、短路、三元式、提前返回共存时，求值顺序和作用域不变。
- 组合破坏仍由已有检查拒绝。

## 未覆盖

- 没有新增控制结构；`for`、`do`、`switch` 仍不在子集内。
- 没有对组合做性能或栈深度测量。
- 只在 `darwin/arm64` 上验证。
