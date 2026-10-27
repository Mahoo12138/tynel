# 分支的两条路径

一个 `if` 在编译期是一条语句，在运行期只走一条路。这两件事要分开看：编译器检查并保留两条分支，运行时才按条件选择。

```typescript
function mark(n: number): number {
  console.log(n);
  return n;
}

function pick(flag: boolean): void {
  if (flag) {
    const x = mark(1);
    console.log(x);
  } else {
    const x = mark(2);
    console.log(x);
  }
}

pick(true);
pick(false);
```

## 运行时的两条路径

```text
pick(true)                          pick(false)
    │                                   │
    │  cond: flag = true                │  cond: flag = false
    ▼                                   ▼
then 分支                            else 分支
    const x = mark(1)                   const x = mark(2)
      │ 打印 1，返回 1                    │ 打印 2，返回 2
      ▼                                   ▼
    console.log(x) → 1                  console.log(x) → 2
```

两次调用的输出是：

```text
1
1
2
2
```

`mark(1)` 与 `mark(2)` 各自只执行一次，未选中的分支连调用都不会发生。两条分支里的 `x` 是两个独立的声明：then 的 `x` 是 `1`，else 的 `x` 是 `2`，它们不共享存储。

## 编译期：两条路径都检查

能力检查和降低都遍历整棵语句树，不按条件值裁剪：

- `if (false) { console.log(1 % 2); }` 仍然会报 `cap.bad-operator`。未选中的分支今天不执行，不代表以后不会被选中；跳过检查会把问题留到运行时。
- NIR 里同时保存 `then` 与 `else`，`if` 的 `cond` 只出现一次。降低不把 `if (true)` 折成 then 分支。
- 分支里的 `const` 变成 NIR 的 `let`，作用域是所在的块。`x` 在 then 里声明，else 和块外都读不到；TypeScript 会先报 TS2304。
- 有值函数的返回路径还没有做完整分析，03.03 仍要求函数体最后一条语句是 `return`。两臂都 `return` 的写法要到 03.04 才放开。

## 为什么条件只求值一次

条件在进入分支之前求值一次，结果用于选择路径。`if (mark(0) > 0)` 只打印一次 `0`；不会因为「先判断 then、再判断 else」而求值两次。NIR 的 `if` 只有一个 `cond` 字段，bridge 只构造一个条件节点，Go 的 `if` 也只求值一次。

## 相关实现

- NIR 的 `if` 形状与作用域规则见 [NIR 格式](nir.md)。
- 源码语句如何降成 `if` 与 `let` 见[从源码到 NIR](lowering.md)。
- bridge 如何构造 Go 的 `ir.IfStmt` 与分支局部变量见 [bridge 说明](bridge.md)。
