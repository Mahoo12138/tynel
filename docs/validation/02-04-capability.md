# 02.04 原生能力白名单：验收记录

记录 02.04 的实际运行结果。被测代码是正文带 `Task: 02.04` 的提交；本文件不预填不存在的哈希。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核，24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 隔离工具链 | go1.27.3 darwin/arm64 |
| 前端源码 | `microsoft/typescript-go`，`typescript/v7.0.2`，提交 `2bd066d8` |

## 正向验收

### 支持子集通过

`bridge/tsgo/testdata/capability/ok.ts` 覆盖本阶段全部写法：number 函数、参数、const、四则运算、直接调用、`console.log(number)`。

```text
$ consumer -capability bridge/tsgo/testdata/capability/ok.ts
[]
$ echo $?
0
```

### 受限输出核对符号来源

`console.log` 只有在内置 `console` 上才被接受。单元测试 `TestConsoleCall` 构造了三种情况：

| 输入 | 结果 |
| --- | --- |
| `console` 解析到文件外（`target = 0`） | 通过 |
| `console` 解析到本文件的声明 | `cap.shadowed-console` |
| 实参是 `string` 或个数不是 1 | `cap.bad-console-call` |

### 未调用函数同样检查

`capability/uncalled.ts` 里的 `neverCalled` 没有任何调用点，里面的 `if` 照样被报出来。检查不按可达性跳过任何函数。

## 负向验收

`./bridge/tsgo/check` 的 21 项全部通过，其中能力相关 10 项：

```text
PASS  支持子集通过能力检查（退出 0）
PASS  any.ts 按退出 3 拒绝（cap.any）
PASS  assert.ts 按退出 3 拒绝（cap.type-escape）
PASS  object.ts 按退出 3 拒绝（cap.unsupported-syntax）
PASS  eval.ts 按退出 3 拒绝（cap.forbidden-global）
PASS  dynamic-function.ts 按退出 3 拒绝（cap.unsupported-syntax）
PASS  library.ts 按退出 3 拒绝（cap.unknown-callee）
PASS  uncalled.ts 按退出 3 拒绝（cap.unsupported-syntax）
PASS  源码错误先于能力检查（退出 1）
```

| 夹具 | 写法 | 错误码 |
| --- | --- | --- |
| `any.ts` | `function identity(value: any): any` | `cap.any` |
| `assert.ts` | `(1 as any) as number` | `cap.type-escape` |
| `object.ts` | `const pair = { left: 1, right: 2 }` | `cap.unsupported-syntax` |
| `eval.ts` | `eval("1")` | `cap.forbidden-global` |
| `dynamic-function.ts` | `new Function("return 1")` | `cap.unsupported-syntax` |
| `library.ts` | `Math.max(1, 2)` | `cap.unknown-callee` |
| `uncalled.ts` | 未调用函数里的 `if` | `cap.unsupported-syntax` |

单元测试还覆盖了夹具造不出来的情况：函数值（`cap.unsupported-syntax`）、`%` 运算符（`cap.bad-operator`）、实参个数不符（`cap.arg-count`）、`console` 被遮蔽（`cap.shadowed-console`）、事实不自洽时拒绝继续检查（`facts.duplicate-id`）。

### 分层：源码错误先于能力检查

```text
$ consumer -capability bridge/tsgo/testdata/bad.ts
[ … "code": 2322 … ]
$ echo $?
1
```

类型错误存在时不会给出能力结论——类型都没定下来，「有没有原生表示」无从判断。

## 用例统计

| 范围 | 用例数 |
| --- | --- |
| `compiler/capability` | 11 个 `=== RUN`（10 个测试函数） |
| 宿主 `bridge/tsgo/consumer` | 28 个 `=== RUN`（含 8 个能力夹具子用例） |
| `./bridge/tsgo/check` | 21 项 |

`./tools/test` 覆盖 `compiler/capability`，9 个包通过。

## 过程中发现的问题

1. **`Kind.String()` 带前缀**。stringer 生成的是 `KindBlock`、`KindReturnStatement`，而能力检查按 `Block`、`ReturnStatement` 匹配，结果所有节点都被当成「未知语法」。适配层统一去掉 `Kind` 前缀。
2. **运算符 token 不能调 `Text()`**。对 `*ast.BinaryOperatorToken` 调 `Text()` 会 panic（`Unhandled case in Node.Text: *ast.Token`）。改成按 `Kind` 映射成符号文本，未识别的保留种类名交给能力检查拒绝。
3. **前向调用在前一趟里丢了**。为了不把库符号（`Math`、`console`）错认成本文件声明，引用改成「只查已登记符号」。但声明与函数体在同一趟里处理，`add(40, 2)` 出现在 `add` 声明之前时查不到，`callee` 变成 0。改成两趟：先登记全部声明，再遍历函数体。
4. **属性访问多收了一个子节点**。`console.log` 的 `PropertyAccessExpression` 通用递归会把属性名 `log` 也当成 Identifier 收进来，看起来像两个子节点。改为只收对象那一侧。

## 与计划的偏差

- **事实在 02.04 增加了语法骨架**。02.03 的事实只有声明、引用、调用和表达式类型，没有语句结构；而「遍历所有载入函数体」必须有函数体的形状。骨架作为 `body` 字段挂在声明上，02.05 的降低阶段直接用它。
- **`console.log` 在能力层接受，实际生成留到 02.06**。能力检查回答的是「能不能进后端」，02.06 才实现输出。中间的 02.05 只降低数值函数，遇到 `console.log` 会按未实现处理——这是阶段划分的缝隙，不是能力判断的错。
