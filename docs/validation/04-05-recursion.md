# 04.05 验收记录：直接递归与调用层

本文件记录 04.05 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 04.05`；用 `git log --grep 'Task: 04.05'` 可以定位。

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

## 正向验收

### 直接递归阶乘

```typescript
function fact(n: number): number {
  if (n <= 1) {
    return 1;
  }
  return n * fact(n - 1);
}
```

```text
$ ./bin/tynel run tests/source/testdata/recursion-fact.ts
1
1
120
5040
```

`fact(0)`、`fact(1)` 返回 `1`，`fact(5)` 返回 `120`，`fact(7)` 返回 `5040`。

### 相互递归与前向声明

```text
$ ./bin/tynel run tests/source/testdata/recursion-mutual.ts
true
true
false
```

`isEven` 调用后面才声明的 `isOdd`。降低器先登记全部函数名，NIR 验证器先登记全部签名，bridge 先建全部 `ir.Func`，三层都允许前向调用。

### 调用层局部值独立

```text
$ ./bin/tynel run tests/source/testdata/recursion-local.ts
10
```

`sum(4)` 的每一层都有自己的 `n` 和 `local`；如果共用一个槽位，结果不会是 `10`。调用层表见[直接递归与调用层](../reference/recursion.md)。

### 产物与回放一致

```text
$ ./bin/tynel build tests/source/testdata/recursion-fact.ts -o out/recursion
已生成 out/recursion（2429906 字节）
  源码     tests/source/testdata/recursion-fact.ts（e99382edbd71）
  输入     04de63332a7d
  编译器   7a75b4acf10b
  构建记录 out/recursion.build.json

$ ./bin/tynel dump nir tests/source/testdata/recursion-fact.ts > out/recursion.nir.json
$ ./bin/tynel build-ir out/recursion.nir.json -o out/recursion-replay
已生成 out/recursion-replay（2429906 字节）
  输入     04de63332a7d
  编译器   7a75b4acf10b
  构建记录 out/recursion-replay.build.json

$ shasum -a 256 out/recursion out/recursion-replay
ddd9339fe167a547fc1629f918586c3868cb953ed2cb62c8584211f4ebd358d6  out/recursion
ddd9339fe167a547fc1629f918586c3868cb953ed2cb62c8584211f4ebd358d6  out/recursion-replay
```

## 超时与编译期边界

```text
$ go test -count=1 -run 'TestRecursionTerminates' -v ./tests/native
=== RUN   TestRecursionTerminates
--- PASS: TestRecursionTerminates (0.47s)
PASS

$ go test -count=1 -run 'TestUnboundedRecursion' -v ./tests/native
=== RUN   TestUnboundedRecursionBuildsWithoutRunning
--- PASS: TestUnboundedRecursionBuildsWithoutRunning (0.31s)
PASS
```

`TestRecursionTerminates` 在独立进程里运行递归阶乘，5 秒超时。`TestUnboundedRecursionBuildsWithoutRunning` 只构建没有终止条件的递归 NIR，不执行产物；编译器必须在 5 秒内完成，证明它不会在编译期展开递归。

## 负向验收

| 输入 | 结果 | 退出 |
| --- | --- | --- |
| 递归实参类型不符 | `TS2345` | 1 |
| 未知递归目标 | `TS2304` | 1 |
| 递归返回值类型不符 | `TS2322` | 1 |
| NIR 递归调用实参类型不符 | `nir.arg-type` | 1 |
| NIR 递归目标不存在 | `nir.unknown-function` | 1 |

具体输出：

```text
$ ./bin/tynel check tests/source/testdata/errors/recursion-signature.ts
…/errors/recursion-signature.ts:6:15: error TS2345: Argument of type 'boolean' is not assignable to parameter of type 'number'.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/recursion-unknown.ts
…/errors/recursion-unknown.ts:6:10: error TS2304: Cannot find name 'missing'.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/recursion-return.ts
…/errors/recursion-return.ts:4:5: error TS2322: Type 'boolean' is not assignable to type 'number'.
$ echo $?
1
```

NIR 层的实参类型与未知目标由现有 `TestVerifyRejectsSemanticErrors` 覆盖。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 374 个测试（178 个顶层 + 196 个子测试） |
| `tests/source` | 98 个用例（7 个测试函数，141 个子测试） |
| `cmd/tynel` | 25 个测试函数，6 个子测试 |
| `tests/native` | 11 个用例 |
| `./bridge/go/check` | 13 项全过 |
| `./bridge/tsgo/check` | 78 项全过 |
| `go vet ./...` | 无输出 |

## 阶段能力边界

- 支持直接递归与相互递归，前向调用不需要特殊语法。
- 每层调用有自己的参数和局部值。
- 编译器不执行递归；无终止程序只构建，不运行。
- 运行时的栈溢出与无限递归不在编译期证明范围内。

## 未覆盖

- 没有测试很深的递归或栈溢出行为；测试只覆盖小范围值。
- 没有尾调用优化；不承诺把递归转成循环。
- 只在 `darwin/arm64` 上验证。
