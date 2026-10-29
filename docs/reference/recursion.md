# 直接递归与调用层

递归调用和普通调用走同一条路径：降低器先把所有函数名登记好，再逐个生成函数体；bridge 也先建全部签名，再填函数体。区别只在调用目标可能指向自己或后面才声明的函数。

## 每一层有自己的参数和局部值

```typescript
function sum(n: number): number {
  let local: number = n;
  if (n === 0) {
    return local;
  }
  return local + sum(n - 1);
}
```

`sum(4)` 的调用层可以想成：

| 层 | `n` | `local` | 返回 |
| --- | --- | --- | --- |
| 1 | 4 | 4 | `4 + sum(3)` |
| 2 | 3 | 3 | `3 + sum(2)` |
| 3 | 2 | 2 | `2 + sum(1)` |
| 4 | 1 | 1 | `1 + sum(0)` |
| 5 | 0 | 0 | `0` |

每一层的 `n` 和 `local` 是这次调用的参数与局部变量，不是共享的全局槽位。Go 的函数调用会为每一层建立自己的栈帧，NIR 里同名的 `param n` 和 `local local` 在不同调用层指向不同的运行值。返回时从第 5 层往上加，得到 `10`。

`recursion-local.ts` 就是这条验收：如果实现把所有调用层塞进同一个局部变量，结果会变成 `0` 或其他错误值。

## 相互递归要先登记名字

```typescript
function isEven(n: number): boolean {
  if (n === 0) {
    return true;
  }
  return isOdd(n - 1);
}

function isOdd(n: number): boolean {
  if (n === 0) {
    return false;
  }
  return isEven(n - 1);
}
```

`isEven` 调用 `isOdd` 时，`isOdd` 的声明还在后面。降低器在生成任何函数体之前，先把全部函数声明 ID 映射到 NIR 函数名；NIR 验证器也先登记全部函数签名，再检查函数体。bridge 同样先为每个函数建 `ir.Func` 和签名，最后才 typecheck 函数体。三层都做了同一件事，所以前向调用和相互递归不需要特殊语法。

## 编译器不执行递归

无终止递归的程序仍然可以编译：

```typescript
function down(n: number): number {
  return down(n - 1);
}
```

编译器只生成调用和返回，不会在编译期展开或求值。`TestUnboundedRecursionBuildsWithoutRunning` 只构建这份 NIR，不运行产物；如果编译器试图在编译期执行递归，构建会超时失败。运行时的栈溢出或无限递归不在编译期证明范围内，测试用独立进程超时兜底。

## 错误边界

- 递归实参类型不符：TypeScript 报 `TS2345`。
- 未知递归目标：`TS2304`。
- 递归返回值类型不符：`TS2322`。
- 手写 NIR 的递归调用目标或实参类型不对：`nir.unknown-function` / `nir.arg-type`。

## 相关材料

- 验收记录见[04.05 验收记录](../validation/04-05-recursion.md)。
- 函数调用与签名预登记见[从源码到 NIR](lowering.md)和 [bridge 说明](bridge.md)。
