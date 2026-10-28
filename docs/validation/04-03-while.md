# 04.03 验收记录：while 循环

本文件记录 04.03 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 04.03`；用 `git log --grep 'Task: 04.03'` 可以定位。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `53496cae` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 正向验收

### 零次、一次、多次

```typescript
function sumTo(n: number): number {
  let total: number = 0;
  let i: number = 1;
  while (i <= n) {
    total = total + i;
    i = i + 1;
  }
  return total;
}
```

```text
$ ./bin/tynel run tests/source/testdata/sum.ts
0
1
55
```

`sumTo(0)` 的条件第一次就是假，循环零次执行；`sumTo(1)` 执行一次；`sumTo(10)` 执行十次，得到 `55`。循环结束后读取 `total`，看到的是最后一次写入的值。

### 条件每轮重新求值

```typescript
function mark(n: number, value: boolean): boolean {
  console.log(n);
  return value;
}

function count(): number {
  let i: number = 0;
  while (mark(i, i < 3)) {
    i = i + 1;
  }
  return i;
}
```

```text
$ ./bin/tynel run tests/source/testdata/while-cond.ts
0
1
2
3
3
```

`mark` 打印 `0`、`1`、`2`、`3`，正好是条件检查的四次；最后一行 `3` 是 `count` 的返回值。如果条件只算一次，打印只会有一次，循环也会变成死循环或直接跳过。

### 嵌套循环

```text
$ ./bin/tynel run tests/source/testdata/while-nested.ts
1
1
1
1
4
```

外层两次、内层两次，`mark(1)` 打印四次，`total` 为 `4`；内层的 `j` 每轮重新初始化，不会把上一轮的值带进来。

### NIR 的 while

```json
{ "op": "while",
  "cond": {"op": "compare", "operator": "<=",
            "left": {"op": "local", "name": "i"},
            "right": {"op": "param", "name": "n"}},
  "body": [
    {"op": "set", "name": "total", "value": {"op": "binary", "operator": "+", ...}},
    {"op": "set", "name": "i", "value": {"op": "binary", "operator": "+", ...}}
  ] }
```

条件准备放在 `condPre`，`sumTo` 的条件不需要准备，所以省略。`while` 不产生值，只改变控制流。

### 产物与回放一致

```text
$ ./bin/tynel build tests/source/testdata/sum.ts -o out/sum
已生成 out/sum（2429906 字节）
  源码     tests/source/testdata/sum.ts（1d045d5eb051）
  输入     c60795bbe706
  编译器   53496cae562b
  构建记录 out/sum.build.json

$ ./bin/tynel dump nir tests/source/testdata/sum.ts > out/sum.nir.json
$ ./bin/tynel build-ir out/sum.nir.json -o out/sum-replay
已生成 out/sum-replay（2429906 字节）
  输入     c60795bbe706
  编译器   53496cae562b
  构建记录 out/sum-replay.build.json

$ shasum -a 256 out/sum out/sum-replay
565ba7dc055052c533c23c436db2e657aa14802bfe725158500d07c18732e9bf  out/sum
565ba7dc055052c533c23c436db2e657aa14802bfe725158500d07c18732e9bf  out/sum-replay
```

## 超时保护

`tests/native` 新增 `TestLoopTerminates`：把 `tests/native/fixtures/sum.nir.json` 编成原生程序，用 `exec.CommandContext` 在独立进程里运行，超时 5 秒。

```text
$ go test -count=1 -run TestLoopTerminates -v ./tests/native
=== RUN   TestLoopTerminates
--- PASS: TestLoopTerminates (0.53s)
PASS
ok  	github.com/Mahoo12138/tynel/tests/native	0.922s
```

条件或回边写错时，这个测试会超时失败，而不是挂住整个测试包。

## 负向验收

| 输入 | 结果 | 退出 |
| --- | --- | --- |
| `while (1)` | `cap.bad-type`：条件不是 boolean | 3 |
| 循环体 `let` 在循环外读取 | `TS2304` | 1 |
| 只有循环体 `return` 的函数 | `TS2366`：返回路径不完整 | 1 |
| 模块级 `while` | `cap.unsupported-syntax` | 3 |
| NIR 的 `while` 条件不是 bool | `nir.condition-type` | 1 |
| NIR 的 `while` 体局部值泄漏 | `nir.unknown-local` | 1 |
| NIR 的 `condPre` 含 `set` | `nir.bad-op` | 1 |

具体输出：

```text
$ ./bin/tynel check tests/source/testdata/errors/while-number.ts
…/errors/while-number.ts:3:10: 能力限制 cap.bad-type: while 的条件是 number，需要 boolean
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/while-scope.ts
…/errors/while-scope.ts:6:10: error TS2304: Cannot find name 'x'.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/while-return.ts
…/errors/while-return.ts:2:24: error TS2366: Function lacks ending return statement and return type does not include 'undefined'.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/while-module.ts
…/errors/while-module.ts:2:1: 能力限制 cap.unsupported-syntax: 模块级 while 不在本阶段子集内
$ echo $?
3
```

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 351 个测试（174 个顶层 + 177 个子测试） |
| `tests/source` | 85 个用例（7 个测试函数，122 个子测试） |
| `cmd/tynel` | 25 个测试函数，6 个子测试 |
| `tests/native` | 9 个用例 |
| `./bridge/go/check` | 12 项全过 |
| `./bridge/tsgo/check` | 72 项全过 |
| `go vet ./...` | 无输出 |

## 阶段能力边界

- 支持 `while (cond) { body }`，条件每轮重新求值，循环可零次执行。
- 循环体可以包含赋值、`if` 和嵌套 `while`；循环体局部值不泄漏。
- 只有循环体里的 `return` 不算完整返回路径。
- 没有 `for`、`do`、`break`、`continue`；这些留给 04.04。

## 未覆盖

- 没有测试无限循环的运行时行为；编译器不证明终止，测试只保证正常程序会结束。
- 没有测试循环里的 `break` / `continue`；04.04 处理。
- 只在 `darwin/arm64` 上验证。
