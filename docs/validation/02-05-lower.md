# 02.05 降低数值函数：验收记录

记录 02.05 的实际运行结果。被测代码是正文带 `Task: 02.05` 的提交；本文件不预填不存在的哈希。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核，24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 隔离工具链 | go1.27.3 darwin/arm64 |
| 前端源码 | `microsoft/typescript-go`，`typescript/v7.0.2`，提交 `2bd066d8` |

## 正向验收

### add.ts 与手写 NIR 行为一致

```text
$ consumer -nir bridge/tsgo/testdata/add.ts > /tmp/add.nir.json
$ bin/tynel build-ir /tmp/add.nir.json -o /tmp/add-ts
已生成 /tmp/add-ts（2429842 字节）
$ /tmp/add-ts
42

$ bin/tynel build-ir examples/ir/add.nir.json -o /tmp/add-hand
已生成 /tmp/add-hand（2429842 字节）
$ /tmp/add-hand
42
```

两份 NIR 的形状不同——手写版直接把调用放进 `print`，源码生成的版本先用 `let` 绑定 `answer` 再打印——但运行结果相同，编译出的二进制也都是 2,429,842 字节。

### 求值顺序

`lower/order.ts` 里 `add(log(1), log(2))` 的两个实参都带输出：

```text
$ consumer -nir bridge/tsgo/testdata/lower/order.ts > order.nir.json
$ bin/tynel build-ir order.nir.json -o order && ./order
1
2
3
```

先 `log(1)`、再 `log(2)`、最后 `add`，与源码顺序一致。

### 前向调用与同名参数

| 夹具 | 内容 | 输出 |
| --- | --- | --- |
| `lower/forward.ts` | `console.log(add(40, 2))` 写在 `add` 声明之前 | `42` |
| `lower/same-name.ts` | `first(a)` 与 `second(a)` 各有一个 `a`，`first(1) + second(3)` | `8` |

### 一元负号保留负零

```text
$ consumer -nir bridge/tsgo/testdata/lower/negzero.ts | grep bits
              "bits": "0x8000000000000000",
$ ./negzero
-0
```

降低把 `-0` 折成符号位为 1 的位模式，不依赖运行时取负。

## 负向验收

`compiler/lower` 的用例全部通过，覆盖伪造事实的四类问题：

| 场景 | 做法 | 错误码 |
| --- | --- | --- |
| 类型不符 | 二元运算的操作数标成 `string` | `lower.bad-type` |
| 操作数数量不符 | 二元运算只有一个操作数 | `lower.bad-arity` |
| 实参个数不符 | `add(40)` | `lower.bad-arity` |
| 作用域错误 | 函数内引用模块常量 | `lower.module-const-in-function` |
| 作用域错误 | 引用不存在的声明 ID | `lower.unknown-name` |
| 入口冲突 | 用户函数叫 `main` | `lower.entry-conflict` |
| 缺返回值 | `number` 函数体为空 | `lower.missing-return` |
| 一元运算符越界 | `-a`（参数而不是字面量） | `lower.bad-unary` |
| 深度超限 | 150 层嵌套 | `facts.body-too-deep` |

每条都同时断言：`Lower` 返回 `nil` 模块，类别是内部故障（退出 5）。

## 端到端检查

`./bridge/tsgo/check` 的 27 项全部通过，新增 6 项：

```text
PASS  add 运行输出符合预期
PASS  order 运行输出符合预期
PASS  negzero 运行输出符合预期
PASS  forward 运行输出符合预期
PASS  same-name 运行输出符合预期
PASS  手写 NIR 与源码生成的 NIR 输出一致
```

前五项各自走完「源码 → NIR → 原生程序 → 运行」，最后一项比较手写 NIR 与源码生成 NIR 的运行结果。

## 用例统计

| 范围 | 用例数 |
| --- | --- |
| `compiler/lower` | 12 个 `=== RUN`（3 个测试函数，含 8 个负向子用例） |
| 宿主 `bridge/tsgo/consumer` | 39 个 `=== RUN`（含 6 个降低夹具） |
| `./bridge/tsgo/check` | 27 项 |

`./tools/test` 覆盖 `compiler/lower`，10 个包通过。

## 过程中发现的问题

1. **NIR 表达不了模块级 const**。`const answer = add(40, 2)` 的值要在 `console.log(answer)` 里用，而 NIR 原来只有 `return` 与 `print`。要么内联（调用会执行两次），要么加局部值。最终给 NIR 加了 `let` 语句与 `local` 表达式，bridge 用 `fn.NewLocal` + 赋值语句落地。
2. **模块级语句在前向声明之前就被转换了**。`console.log(add(40, 2))` 写在 `add` 声明之前，事实里的 `callee` 变成 0。模块语句和函数体一样，必须等所有声明登记完再转；用占位保住源码顺序。
3. **能力检查还在比较旧的一元运算符名**。适配层把运算符换成了符号文本（`-`），能力检查仍按 `MinusToken` 比较，导致 `-0` 被误判为不支持。
4. **事实里的类型摘要需要被降低阶段核对**。第一版降低不看类型，伪造一份「二元运算的操作数是 string」的事实也能生成 NIR。补了 `numberOperand` 检查，类型不符在降低阶段就报 `lower.bad-type`。

## 与计划的偏差

- **`console.log` 的降低提前到了 02.05**。02.06 的交付里包含「单个 number 参数的 console 输出」，但 02.05 的验收要求 `add.ts` 与手写 NIR 行为一致——不打印就没有行为可比。现在降低把 `console.log(x)` 映射成 NIR 的 `print`；02.06 仍负责 void 函数、无值 `return`、丢弃结果的表达式语句和输出格式。
- **一元负号只支持字面量**。NIR 没有取负指令，`-x` 需要一条新操作；本阶段按「保留负零」的验收只折字面量，其余情况在能力与降低两层都拒绝。
- **函数内不能引用模块级常量**。NIR 没有全局变量，把模块常量降到 `main` 的局部值之后，其他函数看不到它。能力检查报 `cap.module-const-in-function`，降低报 `lower.module-const-in-function`，两层一致。
