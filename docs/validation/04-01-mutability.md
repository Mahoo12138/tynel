# 04.01 验收记录：声明身份与可变存储

本文件记录 04.01 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 04.01`；用 `git log --grep 'Task: 04.01'` 可以定位。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `520facd0` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 正向验收

`tests/source/testdata/let.ts` 在函数体里声明 `let`，并在 `if` 块里用同名 `let` 遮蔽外层：

```typescript
function outer(): number {
  let x: number = 1;
  if (true) {
    let x: number = 2;
    console.log(x);
  }
  return x;
}

function flags(): boolean {
  let on: boolean = true;
  return on;
}
```

```text
$ ./bin/tynel run tests/source/testdata/let.ts
2
1
true
```

内层 `x` 打印 `2`，`return x` 读到外层 `1`；`on` 说明 `let` 也可以持有 `boolean`。

### NIR 里的 mutable

```text
$ ./bin/tynel dump nir tests/source/testdata/let.ts
```

`outer` 的两条 `let` 都带 `"mutable": true`：

```json
{ "op": "let", "name": "x", "mutable": true,
  "value": {"op": "number", "bits": "0x3ff0000000000000"} }
```

块内 `const` 和模块级 `const` 不写 `mutable`，读回为 `false`。

### 产物与回放一致

```text
$ ./bin/tynel build tests/source/testdata/let.ts -o out/let
已生成 out/let（2430018 字节）
  源码     tests/source/testdata/let.ts（a701590ac6ff）
  输入     9b462dd79d82
  编译器   520facd01fa1
  构建记录 out/let.build.json

$ ./bin/tynel dump nir tests/source/testdata/let.ts > out/let.nir.json
$ ./bin/tynel build-ir out/let.nir.json -o out/let-replay
已生成 out/let-replay（2430018 字节）
  输入     9b462dd79d82
  编译器   520facd01fa1
  构建记录 out/let-replay.build.json

$ shasum -a 256 out/let out/let-replay
4859f025cd1498c3ac469cd3bdabd4ef8b309624aeae6ebb08ef93e4b2621e4d  out/let
4859f025cd1498c3ac469cd3bdabd4ef8b309624aeae6ebb08ef93e4b2621e4d  out/let-replay
```

## 负向验收

| 输入 | 结果 | 退出 |
| --- | --- | --- |
| 未初始化的 `let x: number;` | `cap.unsupported-syntax`：局部值必须有初始值 | 3 |
| `let x: number = x;` | `TS2448` / `TS2454`：声明前使用 | 1 |
| 块外读取块内 `let` | `TS2304`：找不到名称 | 1 |
| 模块级 `let` | `cap.module-let` | 3 |
| 局部 `var` | `cap.unsupported-syntax` | 3 |
| `let x: string` | `cap.bad-type` | 3 |

具体输出：

```text
$ ./bin/tynel check tests/source/testdata/errors/let-uninitialized.ts
…/errors/let-uninitialized.ts:3:7: 能力限制 cap.unsupported-syntax: 局部值 "x" 必须有且只有一个初始值
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/let-self.ts
…/errors/let-self.ts:3:19: error TS2448: Block-scoped variable 'x' used before its declaration.
…/errors/let-self.ts:3:19: error TS2454: Variable 'x' is used before being assigned.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/let-scope.ts
…/errors/let-scope.ts:6:10: error TS2304: Cannot find name 'x'.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/module-let.ts
…/errors/module-let.ts:2:5: 能力限制 cap.module-let: 模块级 let "x" 不在本阶段子集内；模块体只接受 const
$ echo $?
3
```

`let-var` 与 `let-string` 分别报 `cap.unsupported-syntax` 和 `cap.bad-type`，与 `tests/source/testdata/cases.json` 的期望一致。

## 事实与 NIR 检查

- `compiler/frontend` 的声明种类增加 `let`、`module-let`、`var`，每条声明带 `mutable`；`facts.bad-mutable` 拒绝种类与可变性不一致的事实。
- `compiler/nir` 的 `let` 增加 `mutable` 字段；`TestMutableSurvivesEncoding` 确认 `true` 会写进 JSON 并在回读后保留，`TestDecodeRejectsInvalidStructure` 拒绝在 `print` 上带 `mutable`。
- 降低器的作用域保存 `mutable`，`OpLet` 从声明带入；局部 `const` 为 `false`，局部 `let` 为 `true`。
- 模块级 `let` 由能力检查在降低之前拒绝，不会进入 NIR。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 327 个测试（171 个顶层 + 156 个子测试） |
| `tests/source` | 69 个用例（7 个测试函数，101 个子测试） |
| `cmd/tynel` | 25 个测试函数，6 个子测试 |
| `tests/native` | 8 个用例 |
| `./bridge/go/check` | 10 项全过 |
| `./bridge/tsgo/check` | 67 项全过 |
| `go vet ./...` | 无输出 |

## 阶段能力边界

- 支持块内 `let` 带初始值声明，类型限 `number` / `boolean`，可读取、可遮蔽。
- 事实与 NIR 记录可变性；赋值还没有实现，`x = ...` 仍被拒绝。
- 模块级 `let`、`var`、未初始化的 `let` 都拒绝。
- 没有 `while`、`for`、`break` / `continue`。

## 未覆盖

- 没有测试跨函数捕获局部 `let`；闭包不在子集内。
- 没有测试 `let` 在循环里的重复初始化；循环属于 04.03。
- 只在 `darwin/arm64` 上验证。
