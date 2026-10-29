# 设计记录：状态、循环与调用

04.07 只整理已经实现并验证过的边界。这里的每条规则都能在测试或验收记录里找到对应；没有测试过的设想放在“未实现与未知”一节，不当作能力。

## 为什么这样切边界

状态先切成“声明身份 + 可变标记”，而不是完整的变量系统：`let` 默认不可变，只有显式写 `let x: T = ...` 的局部值可以被 `set`。这样验证器只查一张作用域表，bridge 只生成普通 Go 局部变量，不需要闭包、提升或运行时环境。

循环只保留 `while`，不引入 `for` 的初始化、条件和后置三段式。`while` 的条件准备和条件本身分开，因为短路、三元式和带副作用的调用都可能在条件里产生准备语句；把它们放进 `condPre`，每轮重新执行，语义最直接。

跳转只保留无标签的 `break` / `continue`。每个 `while` 有一个正整数 id，跳转带 `target`；验证器用循环栈检查目标是不是当前最内层。没有标签，就不需要处理“跳到某一层”的解析，也不会出现跨函数或跳进已结束作用域的目标。

递归不另造调用机制。降低器先登记全部函数名，NIR 验证器先登记全部签名，bridge 先建全部 `ir.Func`，自调用和前向调用都走同一条 `call`。编译器不展开递归，也不证明终止；运行时的栈深度和无限递归留给测试超时与原生运行时。

返回路径只做结构判断。`while` 可能零次执行，所以循环体里的 `return` 不算覆盖所有路径；这样不需要“确定赋值”或可达性分析，也不会把只在循环里返回的函数误判为完整。

## NIR 节选

下面是一个带循环、提前退出和赋值的函数节选：

```json
{
  "name": "countDown",
  "params": [{"name": "n", "type": "number"}],
  "result": "number",
  "body": [
    {"op": "let", "name": "total", "mutable": true,
     "value": {"op": "number", "bits": "0x0000000000000000"}},
    {"op": "while", "id": 1,
     "cond": {"op": "compare", "operator": ">",
       "left": {"op": "param", "name": "n"},
       "right": {"op": "number", "bits": "0x0000000000000000"}},
     "body": [
       {"op": "if",
        "cond": {"op": "compare", "operator": "===",
          "left": {"op": "param", "name": "n"},
          "right": {"op": "number", "bits": "0x4000000000000000"}},
        "then": [{"op": "break", "target": 1}]},
       {"op": "set", "name": "total", "value": {"op": "binary", "operator": "+",
         "left": {"op": "local", "name": "total"},
         "right": {"op": "param", "name": "n"}}},
       {"op": "set", "name": "n", "value": {"op": "binary", "operator": "-",
         "left": {"op": "param", "name": "n"},
         "right": {"op": "number", "bits": "0x3ff0000000000000"}}}
     ]},
    {"op": "return", "value": {"op": "local", "name": "total"}}
  ]
}
```

`while` 的 `id` 是循环身份，`break` 的 `target` 必须等于它；`set` 先算右侧再写回；`return` 在循环之后，覆盖零次执行的情况。

## 规则与证据

| 规则 | 证据 | 状态 |
| --- | --- | --- |
| 只有可变的 `let` 能被赋值 | `assign-const`（TS2588）、NIR `set 目标不可变`、[04.02 验收](../validation/04-02-assignment.md) | 已验证 |
| 赋值先算右值再写回 | `assign-order`（2、2、3、5）、[04.02 验收](../validation/04-02-assignment.md) | 已验证 |
| 赋值类型必须一致 | `assign-type`（TS2322）、NIR `set 类型不符` | 已验证 |
| `while` 条件必须是 `boolean` | `while-number`（`cap.bad-type`）、NIR `while 条件不是 bool` | 已验证 |
| `while` 每轮重新检查条件，可零次执行 | `combo-sum`（0、1、55）、`while-cond`、`TestLoopTerminates` | 已验证 |
| 循环体局部值不泄漏 | `while-scope`（TS2304）、NIR `while 体局部值泄漏` | 已验证 |
| 循环体里的 `return` 不算完整返回 | `while-return`（TS2366）、NIR `while 体 return 不算完整返回` | 已验证 |
| `break` / `continue` 只作用于最近循环 | `break`（3）、`continue`（12）、`TestVerifyJumps` | 已验证 |
| 跳转目标必须等于当前循环 id | NIR `break 目标不是当前循环`、`循环 id 重复` | 已验证 |
| 带标签跳转被拒绝 | `labeled-break` / `labeled-continue`（`cap.labeled-jump`） | 已验证 |
| `return` 退出整个函数 | `jump-return`（2） | 已验证 |
| 直接递归与相互递归可用 | `recursion-fact`（5040）、`recursion-mutual`、`TestVerifyMutualRecursion` | 已验证 |
| 每层调用有自己的参数和局部值 | `recursion-local`（10）、`TestRecursionTerminates` | 已验证 |
| 编译器不在编译期执行递归 | `TestUnboundedRecursionBuildsWithoutRunning` | 已验证 |
| 组合不改变求值顺序 | `combo-fact`、`combo-branch`、`combo-early`、`combo-mixed`、`TestCombinationTerminates` | 已验证 |

## 失败案例

| 输入 | 错误码 | 证据 |
| --- | --- | --- |
| `for` 循环 | `cap.unsupported-syntax` | `control-flow` |
| 循环外 `break` / `continue` | `TS1107` | `break-outside`、`continue-outside` |
| 带标签的 `break` / `continue` | `cap.labeled-jump` | `labeled-break`、`labeled-continue` |
| 非 boolean 的 `while` 条件 | `cap.bad-type` | `while-number` |
| 给 `const` 赋值 | `TS2588` | `assign-const` |
| 赋值类型不一致 | `TS2322` | `assign-type`、`combo-bad-assign` |
| 只有循环体 `return` | `TS2366` | `while-return`、`combo-bad-return` |
| NIR 跳转目标不是当前循环 | `nir.loop-target` | `TestVerifyRejectsSemanticErrors` |
| NIR 循环 id 重复 | `nir.duplicate-loop` | `TestVerifyRejectsSemanticErrors` |
| NIR 写入不可变局部值 | `nir.immutable-local` | `TestVerifyRejectsSemanticErrors` |
| 递归实参类型不符 | `TS2345` / `nir.arg-type` | `recursion-signature` |
| 未知递归目标 | `TS2304` / `nir.unknown-function` | `recursion-unknown` |

这些错误没有被组合测试放宽：`combo-bad-return.ts` 和 `combo-bad-assign.ts` 仍然由 TypeScript 先拒绝，NIR 层则用更小的坏模块单独覆盖循环目标、返回路径和赋值目标。

## 未实现与未知

- 未实现：`for`、`do`、`switch`、`try`、`throw`、带标签跳转、闭包、`var`、复合赋值、自增自减。
- 未知：很深的递归、栈溢出、尾调用优化、运行时无限递归的检测、组合程序的性能。测试只覆盖小范围值，并用独立进程超时兜底，不证明终止。
- 未验证：`darwin/arm64` 之外的平台。

## 与参考实现和测试的对应

`tests/source` 的每个程序都与 Node `--experimental-strip-types` 的输出对照，`reference.json` 里标为 `shared`。NIR 层由 `compiler/nir/verify_test.go` 的 `TestVerifyWhile`、`TestVerifyJumps`、`TestVerifyMutualRecursion` 和 `TestVerifyRejectsSemanticErrors` 覆盖；原生层由 `tests/native/native_test.go` 的循环、递归、组合超时测试覆盖。验收记录按节点分别记录在 `docs/validation/04-0*.md`。

相关材料：[NIR 格式](../reference/nir.md)、[从源码到 NIR](../reference/lowering.md)、[循环跳转](../reference/jumps.md)、[直接递归与调用层](../reference/recursion.md)、[状态与控制组合](../reference/combinations.md)。
