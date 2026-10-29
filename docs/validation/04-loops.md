# 04.07 验收记录：状态、循环与真实源码实验

本文件记录 04.07 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 04.07`；用 `git log --grep 'Task: 04.07'` 可以定位。

04.07 同时收束状态与循环的设计记录与真实源码实验。设计记录见[状态、循环与调用](../design/state-and-loops.md)，它只复述已经验证的规则，并把未实现与未知单独列出；本文件记录 Collatz 实验的正向输出、NIR 回放和失败案例。

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

## 实验源码

`examples/collatz.ts` 用循环和递归两种方式计算 Collatz 步数：

```typescript
function isEven(n: number): boolean {
  if (n === 0) {
    return true;
  }
  if (n === 1) {
    return false;
  }
  return isEven(n - 2);
}

function stepsIter(n: number): number {
  let current: number = n;
  let count: number = 0;
  while (current !== 1) {
    if (isEven(current)) {
      current = current / 2;
    } else {
      current = current * 3 + 1;
    }
    count = count + 1;
  }
  return count;
}

function stepsRec(n: number): number {
  if (n === 1) {
    return 0;
  }
  if (isEven(n)) {
    return 1 + stepsRec(n / 2);
  }
  return 1 + stepsRec(n * 3 + 1);
}
```

完整文件还包含五个 `console.log`，分别输出 `stepsIter(27)`、`stepsRec(27)`、`stepsIter(6)`、`stepsRec(6)`、`stepsIter(1)`。

## 正向输出

```text
$ ./bin/tynel run examples/collatz.ts
111
111
8
8
0

$ node --experimental-strip-types examples/collatz.ts
111
111
8
8
0
```

循环版和递归版在 `27`、`6`、`1` 上一致；与 Node 逐行一致。已知 Collatz 步数 `27 → 111`、`6 → 8`、`1 → 0` 也吻合。

## NIR 与回放

```text
$ ./bin/tynel build examples/collatz.ts -o out/collatz
已生成 out/collatz（2430002 字节）
  源码     examples/collatz.ts（0c5ee6b7249b）
  输入     84d479911726
  编译器   7a75b4acf10b
  构建记录 out/collatz.build.json

$ ./bin/tynel dump nir examples/collatz.ts > out/collatz.nir.json
$ ./bin/tynel build-ir out/collatz.nir.json -o out/collatz-replay
已生成 out/collatz-replay（2430002 字节）
  输入     84d479911726
  编译器   7a75b4acf10b
  构建记录 out/collatz-replay.build.json

$ shasum -a 256 out/collatz out/collatz-replay
8177c79116c9cbbaa0f5aaa14cddd24a0413d14aaefe624be6f4f0e984b951b9  out/collatz
8177c79116c9cbbaa0f5aaa14cddd24a0413d14aaefe624be6f4f0e984b951b9  out/collatz-replay
```

`stepsIter` 的 `while` 带 `id: 1`；`stepsRec` 的递归调用走普通 `call`，没有循环 id。NIR 可重放，产物逐字节相同。

## 失败记录

### 取模被拒绝

`examples/collatz-mod.ts` 保留最初想用的 `%` 写法：

```typescript
function isEven(n: number): boolean {
  return n % 2 === 0;
}
```

```text
$ ./bin/tynel check examples/collatz-mod.ts
…/examples/collatz-mod.ts:4:10: 能力限制 cap.bad-operator: 运算符 "%" 不在本阶段支持的 + - * / 与比较之内
$ echo $?
3
```

`%` 不在能力白名单里，检查阶段直接拒绝，没有运行时兜底。修复路径是改用减法递归的 `isEven`。

### 编译通过但语义错误

第二次尝试用 `n / 2 * 2 === n` 判断偶数。它通过了编译，和 Node 的输出也一致，但输出是 `1075`、`1075`、`1076`、`1076`、`0`，不是已知的 `111`、`8`。原因是这个判断对所有有限整数都为真，程序一直在做除以二。

这不是编译器缺陷，而是源码语义错误；编译器忠实复现了错误。修复方式是用减法递归判断奇偶，并保留已知步数作为预期。

## 累计测试

本任务没有改动编译器和测试夹具，累计结果与 04.06 相同：

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 387 个测试（179 个顶层 + 208 个子测试） |
| `tests/source` | 105 个用例（7 个测试函数，153 个子测试） |
| `cmd/tynel` | 25 个测试函数，6 个子测试 |
| `tests/native` | 12 个用例 |
| `./bridge/go/check` | 13 项全过 |
| `./bridge/tsgo/check` | 83 项全过 |
| `go vet ./...` | 无输出 |

## 边界

- 实验不接入产品命令，也不新增语言特性；`examples/collatz.ts` 是一段可运行的实验源码。
- 输入写死为常量，没有命令行参数或标准输入。
- `isEven` 只适合非负整数；负数不在输入范围。
- 没有测深递归、栈溢出、尾调用或性能。
- 只在 `darwin/arm64` 上验证。
