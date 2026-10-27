# 返回路径

有值函数必须在**每一条**路径上都返回。只看到一条 `return` 不代表路径完整：如果它藏在 `if` 的一臂里，另一臂仍然会走到函数末尾。

```typescript
function pick(flag: boolean): number {
  if (flag) {
    return 1;
  }
}
```

`pick(true)` 返回 `1`；`pick(false)` 直接走到函数末尾，没有值可返回。TypeScript 先报 TS2366（`Function lacks ending return statement`），本阶段不会把它补成 `0` 或 `undefined`。

## 判定规则

一条语句是否「结束函数」按结构判断：

| 语句 | 是否结束 |
| --- | --- |
| `return` | 是 |
| `if` | 只有 `then` 与 `else` 都存在，且两边都结束时才是 |
| `let`、`print`、`discard`、表达式语句 | 否 |

一个语句块只要遇到一条结束语句，就认为整块结束；它后面的语句不可达，但不影响「块已经结束」的结论。

由此得到三种合法写法：

```typescript
// 两臂都返回，函数体末尾不需要再补 return。
function sign(n: number): number {
  if (n < 0) {
    return -1;
  } else {
    return 1;
  }
}

// 单臂提前返回，后面补一个 return。
function pick(flag: boolean): number {
  if (flag) {
    return 1;
  }
  return 2;
}

// 嵌套分支，两层 if 都覆盖两臂。
function grade(n: number): number {
  if (n < 0) {
    if (n < -10) {
      return 0;
    } else {
      return 1;
    }
  } else {
    return 2;
  }
}
```

`void` 函数不要求返回值，正常走到末尾就是结束。降低会给它补一条空 `return`，这条 `return` 只表示函数结束，不产生值。

## 错误位置

缺返回路径时，诊断指向第一条没有结束的路径上的 `if`，而不是整个函数：

```text
…/missing-return.ts:2:31: error TS2366: Function lacks ending return statement and return type does not include 'undefined'.
```

TypeScript 已经拦住了源码。如果一份手写 NIR 绕过前端，NIR 验证器按同样的规则报 `nir.missing-return`，位置来自 `if` 节点；降低阶段也会先做一次分析，报 `lower.missing-return`。

## 不可达语句

`return` 之后的语句不可达。本阶段的规则是：

- **不跳过能力检查**。`return 1; console.log(1 % 2);` 仍然报 `cap.bad-operator`，因为未实现的能力不会因为今天执行不到就消失。
- **NIR 保留它们**。验证器照样检查类型和作用域，`dump nir` 里也能看到。
- **生成阶段丢弃它们**。bridge 在一个块遇到结束语句后停止生成后续语句，Go 编译器因此不会看到不可达代码，也不会报 `unreachable code`。

这样既没有用 Go 的零值填补缺口，也没有把不可达代码悄悄从检查里拿掉。

## 与 Go 的关系

Go 的类型检查器自己会做返回路径分析：两臂都 `return` 的 `if` 之后可以不再写 `return`。bridge 直接构造 `ir.IfStmt` 与 `ir.ReturnStmt`，让 Go 的 `typecheck` 和 `walk` 沿用它们原有的控制流规则，不在 bridge 里另写一套代码生成。

## 相关实现

- NIR 的返回路径判定在 `compiler/nir/returns.go`，验证器与降低器共用。
- 源码到 NIR 的 `if` / `return` 对应关系见[从源码到 NIR](lowering.md)。
- bridge 如何丢弃不可达语句见 [bridge 说明](bridge.md)。
