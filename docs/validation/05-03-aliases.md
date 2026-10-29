# 05.03 验收记录：类型别名与纯类型导入

本文件记录 05.03 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 05.03`；用 `git log --grep 'Task: 05.03'` 可以定位。

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

### 共享类型别名

`compiler/graph/testdata/alias/types.ts`：

```typescript
export type Amount = number;
```

`main.ts`：

```typescript
import type { Amount } from "./types.ts";

const value: Amount = 41;
console.log(value + 1);
```

```text
$ ./bin/tynel run --root compiler/graph/testdata/alias main.ts
42

$ ./bin/tynel check --root compiler/graph/testdata/alias main.ts
ok: …/alias/main.ts（1 个函数，NIR 已验证）
```

`import type` 没有产生运行时输出，降低后的入口只包含 `value` 的 `let` 和打印。

### 本地别名与别名链

```typescript
type Amount = number;
type Flag = boolean;
const value: Amount = 41;
const ok: Flag = true;
console.log(value + 1);
console.log(ok);
```

输出：

```text
42
true
```

`type Base = number; type Amount = Base;` 也输出 `42`，别名链最终仍归到 `number`。

```text
$ ./bin/tynel run --root compiler/graph/testdata/alias chain.ts
42
```

### 修改别名后使用方报错

把 `types.ts` 改为 `export type Amount = string;`，入口不变：

```text
$ ./bin/tynel check --root compiler/graph/testdata/alias main.ts
…/alias/main.ts:3:7: error TS2322: Type 'number' is not assignable to type 'string'.
$ echo $?
1
```

错误定位到使用方第 3 行第 7 列，说明别名身份跨文件共享；改回 `number` 后恢复输出 `42`。

## 负向验收

| 输入 | 结果 | 退出 |
| --- | --- | --- |
| `type Amount = number \| boolean` | `cap.bad-type`，联合拒绝 | 3 |
| `interface Amount { value: number }` | `cap.interface` | 3 |
| `type Amount = any` | `cap.any` | 3 |
| `const x: Amount = Amount` | `TS2693`，类型名当值 | 1 |
| `Amount(1)`（`import type`） | `TS2693`，纯类型导入被调用 | 1 |
| `type A = B; type B = A` | `TS2456`，循环别名 | 1 |
| `import { a } from "./a.ts"` | `cap.import-value` | 3 |

具体输出：

```text
$ ./bin/tynel check --root compiler/graph/testdata/alias union.ts
…/alias/union.ts:1:1: 能力限制 cap.bad-type: 类型别名 "Amount" 是联合类型，本阶段不支持

$ ./bin/tynel check --root compiler/graph/testdata/alias interface.ts
…/alias/interface.ts:1:1: 能力限制 cap.interface: 接口 "Amount" 不在本阶段子集内…

$ ./bin/tynel check --root compiler/graph/testdata/alias any.ts
…/alias/any.ts:1:1: 能力限制 cap.any: 类型别名 "Amount" 是 any 或 unknown

$ ./bin/tynel check --root compiler/graph/testdata/alias value.ts
…/alias/value.ts:2:19: error TS2693: 'Amount' only refers to a type, but is being used as a value here.

$ ./bin/tynel check --root compiler/graph/testdata/alias called.ts
…/alias/called.ts:3:13: error TS2693: 'Amount' only refers to a type, but is being used as a value here.

$ ./bin/tynel check --root compiler/graph/testdata/alias cycle.ts
…/alias/cycle.ts:1:6: error TS2456: Type alias 'A' circularly references itself.
```

值导入用菱形夹具验证：

```text
$ ./bin/tynel check --root compiler/graph/testdata/diamond main.ts
…/diamond/main.ts:2:1: 能力限制 cap.import-value: 值导入不在本阶段子集内；只支持 import type
$ echo $?
3
```

## 检查器配置

- `allowImportingTsExtensions` 让显式 `.ts` 导入合法。
- `noEmit` 避免检查器写输出。
- 程序级诊断没有源码位置时不再硬塞进入口快照，避免偏移 `-1` 变成 `source.bad-offset`。

## 事实与降低

- 别名放在 `Aliases`，不进入 `Declarations`，所以不会被当成可赋值的局部值。
- `import type` 在事实里是 `ImportDeclaration` 且 `typeOnly` 为 `true`；降低阶段直接跳过。
- 值导入在能力检查阶段报 `cap.import-value`，不进入降低。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 15 个包通过，根模块 419 个测试（199 个顶层 + 220 个子测试） |
| `tests/source` | 105 个用例（7 个测试函数，153 个子测试） |
| `cmd/tynel` | 27 个测试函数，6 个子测试 |
| `tests/native` | 12 个用例 |
| `./bridge/go/check` | 13 项全过 |
| `./bridge/tsgo/check` | 89 项全过 |
| `go vet ./...` | 无输出 |

## 未覆盖

- 不支持泛型别名、接口、联合、对象类型、数组、字面量联合。
- 不支持值导入和跨模块函数/常量绑定；这些留到 05.04。
- 只在 `darwin/arm64` 上验证。
