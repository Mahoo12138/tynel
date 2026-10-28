# 04.04 验收记录：break、continue 与嵌套循环

本文件记录 04.04 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 04.04`；用 `git log --grep 'Task: 04.04'` 可以定位。

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

### 内层 break 不结束外层

```typescript
function f(): number {
  let total: number = 0;
  let i: number = 0;
  while (i < 3) {
    let j: number = 0;
    while (j < 3) {
      if (j === 1) {
        break;
      }
      total = total + 1;
      j = j + 1;
    }
    i = i + 1;
  }
  return total;
}
```

```text
$ ./bin/tynel run tests/source/testdata/break.ts
3
```

内层在 `j === 1` 时退出，外层继续三次，所以 `total` 是 `3`，不是 `1`。

### continue 回到条件检查

```text
$ ./bin/tynel run tests/source/testdata/continue.ts
12
```

`i` 到 `3` 时 `continue`，跳过 `total + 3`；其余 `1 + 2 + 4 + 5` 得到 `12`。`continue` 之后条件重新检查，`i` 继续增加到 `5`。

### return 退出整个函数

```text
$ ./bin/tynel run tests/source/testdata/jump-return.ts
2
```

`i === 2` 时 `return i`，整个函数结束，不再执行外层循环或末尾的 `return -1`。

### NIR 的循环身份

```text
$ ./bin/tynel dump nir tests/source/testdata/break.ts
```

`f` 的循环身份与跳转目标：

```text
while id 1
  while id 2
    break target 2
```

外层 `id` 是 `1`，内层是 `2`，`break` 的 `target` 是 `2`。`continue` 同理。

### 产物与回放一致

```text
$ ./bin/tynel build tests/source/testdata/break.ts -o out/break
已生成 out/break（2429906 字节）
  源码     tests/source/testdata/break.ts（45828c720421）
  输入     a84e05acba9a
  编译器   7a75b4acf10b
  构建记录 out/break.build.json

$ ./bin/tynel dump nir tests/source/testdata/break.ts > out/break.nir.json
$ ./bin/tynel build-ir out/break.nir.json -o out/break-replay
已生成 out/break-replay（2429906 字节）
  输入     a84e05acba9a
  编译器   7a75b4acf10b
  构建记录 out/break-replay.build.json

$ shasum -a 256 out/break out/break-replay
fde944e2e37c53f4d758be45f118b398c572e45b49c29d8f627e58ce7092f017  out/break
fde944e2e37c53f4d758be45f118b398c572e45b49c29d8f627e58ce7092f017  out/break-replay
```

## 跳转图

内外两层的真实目标见[循环跳转](../reference/jumps.md)。`break` 退出最近循环，`continue` 回到最近循环的条件检查，`return` 退出整个函数。

## 负向验收

| 输入 | 结果 | 退出 |
| --- | --- | --- |
| 循环外 `break` | `TS1107` | 1 |
| 循环外 `continue` | `TS1107` | 1 |
| `break outer;` | `cap.labeled-jump` | 3 |
| `continue outer;` | `cap.labeled-jump` | 3 |
| NIR 的 `break` 在循环外 | `nir.loop-target` | 1 |
| NIR 的 `break` 指向外层循环 | `nir.loop-target` | 1 |
| NIR 的循环 id 重复 | `nir.duplicate-loop` | 1 |

具体输出：

```text
$ ./bin/tynel check tests/source/testdata/errors/break-outside.ts
…/errors/break-outside.ts:3:3: error TS1107: Jump target cannot cross function boundary.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/labeled-break.ts
…/errors/labeled-break.ts:4:3: 能力限制 cap.labeled-jump: 带标签的语句不在本阶段子集内
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/labeled-continue.ts
…/errors/labeled-continue.ts:4:3: 能力限制 cap.labeled-jump: 带标签的语句不在本阶段子集内
$ echo $?
3
```

NIR 层在 `compiler/nir/verify_test.go` 里直接构造坏模块，确认目标必须等于当前最内层循环的 id。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 362 个测试（175 个顶层 + 187 个子测试） |
| `tests/source` | 92 个用例（7 个测试函数，132 个子测试） |
| `cmd/tynel` | 25 个测试函数，6 个子测试 |
| `tests/native` | 9 个用例 |
| `./bridge/go/check` | 13 项全过 |
| `./bridge/tsgo/check` | 75 项全过 |
| `go vet ./...` | 无输出 |

## 阶段能力边界

- 支持无标签 `break` / `continue`，只作用于最近循环。
- `continue` 回到条件检查，条件准备重新执行。
- `return` 仍退出整个函数，与循环跳转分开。
- 带标签的跳转、循环外跳转拒绝。
- 没有 `for` / `do`。

## 未覆盖

- 没有测试带标签的嵌套循环；该语法被拒绝。
- 没有测试 `break` / `continue` 在 `switch` 里的语义；`switch` 不在子集内。
- 只在 `darwin/arm64` 上验证。
