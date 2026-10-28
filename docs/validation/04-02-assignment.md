# 04.02 验收记录：局部简单赋值

本文件记录 04.02 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 04.02`；用 `git log --grep 'Task: 04.02'` 可以定位。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `68e97b4e` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 正向验收

### 数值累加与布尔改写

```typescript
function counter(): number {
  let x: number = 1;
  x = x + 1;
  x = x * 2;
  return x;
}

function flag(): boolean {
  let on: boolean = false;
  on = true;
  return on;
}
```

```text
$ ./bin/tynel run tests/source/testdata/assign.ts
4
true
```

`x = x + 1` 读到写入前的 `1`，写回 `2`；再 `x = x * 2` 得到 `4`。`on` 从 `false` 改成 `true`，类型保持不变。

### 右值先求值

```typescript
function mark(n: number): number {
  console.log(n);
  return n;
}

function f(): number {
  let x: number = 1;
  x = mark(2);
  console.log(x);
  x = x + mark(3);
  return x;
}
```

```text
$ ./bin/tynel run tests/source/testdata/assign-order.ts
2
2
3
5
```

`mark(2)` 先打印 `2`，再写回；随后读 `x` 得到 `2`。第二次赋值先打印 `mark(3)` 的 `3`，再算 `2 + 3`，返回 `5`。

### NIR 的 set

```text
$ ./bin/tynel dump nir tests/source/testdata/assign.ts
```

`counter` 里的两条赋值是 `set`：

```json
{ "op": "set", "name": "x",
  "value": {"op": "binary", "operator": "+",
            "left": {"op": "local", "name": "x"},
            "right": {"op": "number", "bits": "0x3ff0000000000000"}} }
```

`let` 带 `"mutable": true`，`set` 的目标名与它一致。

### 产物与回放一致

```text
$ ./bin/tynel build tests/source/testdata/assign.ts -o out/assign
已生成 out/assign（2429970 字节）
  源码     tests/source/testdata/assign.ts（c151532a0120）
  输入     1e8e26490ec5
  编译器   68e97b4e9557
  构建记录 out/assign.build.json

$ ./bin/tynel dump nir tests/source/testdata/assign.ts > out/assign.nir.json
$ ./bin/tynel build-ir out/assign.nir.json -o out/assign-replay
已生成 out/assign-replay（2429970 字节）
  输入     1e8e26490ec5
  编译器   68e97b4e9557
  构建记录 out/assign-replay.build.json

$ shasum -a 256 out/assign out/assign-replay
000db7ba419a375fc2280b62f6aa112e05f2319bbfea24b76cb284d3f83e6103  out/assign
000db7ba419a375fc2280b62f6aa112e05f2319bbfea24b76cb284d3f83e6103  out/assign-replay
```

## 负向验收

| 输入 | 结果 | 退出 |
| --- | --- | --- |
| 给 `const` 赋值 | `TS2588` | 1 |
| 给参数赋值 | `cap.assign-immutable` | 3 |
| 给未声明目标赋值 | `TS2304` | 1 |
| `x = true` 而 `x` 是 `number` | `TS2322` | 1 |
| `const y = (x = 2)` | `cap.assignment-value` | 3 |
| `x += 1` | `cap.compound-assignment` | 3 |
| `x++` | `cap.inc-dec` | 3 |

具体输出：

```text
$ ./bin/tynel check tests/source/testdata/errors/assign-const.ts
…/errors/assign-const.ts:3:1: error TS2588: Cannot assign to 'x' because it is a constant.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/assign-param.ts
…/errors/assign-param.ts:3:3: 能力限制 cap.assign-immutable: 赋值目标 "x" 是参数，不是可变的 let
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/assign-value.ts
…/errors/assign-value.ts:4:22: 能力限制 cap.assignment-value: 赋值只能作为独立语句，不能出现在表达式里
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/assign-compound.ts
…/errors/assign-compound.ts:4:3: 能力限制 cap.compound-assignment: 复合赋值 "+=" 不在本阶段子集内
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/assign-inc.ts
…/errors/assign-inc.ts:4:3: 能力限制 cap.inc-dec: 自增自减 "++" 不在本阶段子集内
$ echo $?
3
```

`assign-undeclared` 与 `assign-type` 由 TypeScript 分别报 `TS2304`、`TS2322`，退出 1。

## NIR 层的拒绝

| 输入 | 结果 |
| --- | --- |
| `set` 目标不是 `let` | `nir.immutable-local` |
| `set` 目标不存在 | `nir.unknown-local` |
| `set` 右值与目标类型不同 | `nir.assign-type` |
| `set` 缺少 `value` | `nir.missing` |

这些用例在 `compiler/nir/verify_test.go` 与 `TestDecodeRejectsInvalidStructure` 里，直接构造 NIR，不经过源码。

## 范围契约

`SPEC.md` 第 2 节写明：简单赋值只作为独立语句，赋值表达式、复合赋值和自增自减均拒绝；参数重新赋值也拒绝。这一条与能力检查、NIR 验证器和降低器的行为一致，没有“接收了但结果被丢掉”的路径。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 339 个测试（172 个顶层 + 167 个子测试） |
| `tests/source` | 78 个用例（7 个测试函数，112 个子测试） |
| `cmd/tynel` | 25 个测试函数，6 个子测试 |
| `tests/native` | 8 个用例 |
| `./bridge/go/check` | 11 项全过 |
| `./bridge/tsgo/check` | 69 项全过 |
| `go vet ./...` | 无输出 |

## 阶段能力边界

- 支持数值累加、布尔改写和 `x = x + 1`；右值先求值，再写回。
- 目标必须是可变的 `let`；`const`、参数、模块常量、未声明目标都拒绝。
- 赋值表达式、复合赋值、自增自减拒绝。
- 没有 `while` / `for`；循环里的重复赋值属于 04.03。

## 未覆盖

- 没有测试赋值在循环体里的重复执行；循环属于 04.03。
- 没有测试跨函数共享可变存储；闭包与全局变量不在子集内。
- 只在 `darwin/arm64` 上验证。
