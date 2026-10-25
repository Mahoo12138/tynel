# 02.03 声明身份与事实：验收记录

记录 02.03 的实际运行结果。被测代码是正文带 `Task: 02.03` 的提交；本文件不预填不存在的哈希。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核，24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 隔离工具链 | go1.27.3 darwin/arm64 |
| 前端源码 | `microsoft/typescript-go`，`typescript/v7.0.2`，提交 `2bd066d8` |

## 正向验收

### 同名参数拿到不同 ID

夹具 `bridge/tsgo/testdata/facts.ts` 里 `add(a, b)` 与 `scale(a)` 各有一个 `a`：

```text
$ consumer -facts bridge/tsgo/testdata/facts.ts
1 const answer 82
2 function add 111       参数 [3, 4]
3 parameter a 126
4 parameter b 136
5 function scale 175     参数 [6]
6 parameter a 264
7 const doubled 309
```

两处引用分别指向各自的声明：

```text
{"name": "a", "start": 166, "length": 2, "target": 3}
{"name": "a", "start": 293, "length": 2, "target": 6}
```

`TestFactsDistinguishSameNamedParameters` 断言两个 ID 不同，且每个 ID 恰好被引用一次。

### 前向调用解析正确

`add(40, 2)` 出现在 `add` 的声明之前（调用在字节 99，声明在 111）。事实里的 `callee` 是 2，指向后面那个声明：

```text
{"start": 99, "length": 11, "callee": 2,
 "arguments": [{"kind":"number"},{"kind":"number"}],
 "result": {"kind":"number"}}
```

`TestFactsResolveForwardCall` 断言被调用者是函数 `add`、声明位置在调用之后、实参个数与形参一致。

### 会话关闭后事实仍可读

宿主先 `session.Close()` 再 `frontend.Decode(raw)`，解码与校验都在句柄清空之后完成，数据完整。`TestFactsRemainReadableAfterSessionClose` 还确认了 `Session` 标识存在、声明与引用非空、`Validate` 通过。

### 类型摘要

`const answer = add(40, 2)` 的类型是 `number`，尽管检查器对字面量给出的是字面量类型。摘要来自类型标志位，不来自 `"42"` 这样的字符串。

## 负向验收

`compiler/frontend` 的 18 条不一致事实用例全部按预期错误码拒绝：

| 场景 | 做法 | 错误码 |
| --- | --- | --- |
| 重复 ID | 把参数 ID 改成函数的 ID | `facts.duplicate-id` |
| 引用指向不存在的声明 | `target = 99` | `facts.unknown-target` |
| 调用指向不存在的声明 | `callee = 99` | `facts.unknown-target` |
| 调用指向常量 | `callee = 4` | `facts.call-target` |
| 实参个数不符 | 只留一个实参 | `facts.arg-count` |
| 实参类型不符 | 第一个实参改成 `string` | `facts.arg-type` |
| 返回类型摘要不符 | 调用结果改成 `void` | `facts.result-type` |
| 函数缺返回类型 | `result = null` | `facts.bad-type` |
| 参数缺类型 | `type = null` | `facts.bad-type` |
| 参数重复归属 | 函数参数列表写 `[2, 2]` | `facts.shared-parameter` |
| 参数没有归属 | 函数参数列表漏掉一个 | `facts.orphan-parameter` |
| 参数 ID 指向函数 | 参数列表写 `[1]` | `facts.bad-parameter` |
| 声明种类不认识 | `kind = "variable"` | `facts.bad-decl-kind` |
| 类型类别带空格 | `kind = "number "` | `facts.bad-type` |
| 类型类别大小写不符 | `kind = "Number"` | `facts.bad-type` |
| 位置为负 | `start = -1` | `facts.bad-position` |
| 缺会话标识 | `session = ""` | `facts.no-session` |
| schema 版本不符 | `schemaVersion = 2` | `facts.schema` |

另外两条：

- **未知字段**：在合法 JSON 里加 `"upstreamSymbol"` 后 `Decode` 报 `facts.decode`。适配层与本包的字段一旦漂移就会被发现，不会静默丢一半事实。
- **会话混用**：两次检查同一份文件，`Combine` 报 `facts.session-mismatch`。`TestFactsFromDifferentSessionsCannotCombine` 用两个真实会话验证，两次的 `session` 标识确实不同。

全部错误都属于内部故障（退出 5），不是源码错误。

### 纯数据检查

`TestFactsHoldOnlyPlainData` 用反射遍历 `Facts` 的类型图：只允许基本类型、本包结构体和它们的切片，指针只能指向本包结构体。加入 `*ast.Node` 这类上游字段会让它直接失败。

## 用例统计

| 范围 | 用例数 |
| --- | --- |
| `compiler/frontend` | 23 个 `=== RUN`（5 个测试函数，含 18 个子用例） |
| 宿主 `bridge/tsgo/consumer` | 18 个 `=== RUN`（14 个测试函数） |
| `./bridge/tsgo/check` | 12 项 |

`./tools/test` 覆盖 `compiler/frontend`，8 个包通过。

## 过程中发现的问题

1. **`fn.Parameters` 是字段，不是方法**。`FunctionDeclaration` 结构体里 `Parameters` 是 `*ParameterList`，`Body` 是 `*NodeBody`；要拿参数节点列表和函数体，得用 `*ast.Node` 上的同名方法。写成 `fn.Parameters()` 编译不过。
2. **声明顺序最初是「参数先于函数」**。因为先追加参数再追加函数，读起来像参数没有归属。改成先算 ID、再按「函数、参数、参数」写入，输出顺序与源码一致。
3. **类型注解里的 `number` 会被当成引用**。第一版遍历整个函数节点，`a: number` 的 `number` 也进了引用表，指向一个不存在的声明。改成只走函数体与初始化表达式，不进类型节点。

## 与计划的偏差

- **`dump facts` 目前是宿主的 `-facts` 模式，不是 `tynel dump facts`**。根模块不能导入 `tyneladapter`（它在另一个模块的源码树里），主 CLI 要到 02.07 才接入前端。现在先把它做成外部宿主的一个开关，命令行参考里仍把 `dump facts` 标为未实现。
- **事实通过 JSON 传递，而不是共享结构体**。适配层在另一个模块里，无法导入 `compiler/frontend`；两边各写一份结构体、靠严格解码对齐，比在根模块加一条指向 `.work` 的 `replace` 更省事，也让「事实可以被写出、被别的进程读」这件事在 02.03 就成立。
