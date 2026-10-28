# 三元式

条件表达式只求值选中的一条分支：

```typescript
function pick(n: number, value: number): number {
  console.log(n);
  return value;
}

console.log(true ? pick(1, 10) : pick(2, 20));
console.log(false ? pick(3, 10) : pick(4, 20));
```

输出是：

```text
1
10
4
20
```

`2` 和 `3` 没有出现：条件为真时只执行 `then`，为假时只执行 `else`。条件和两条分支各求值一次，没有被选中的分支连准备语句都不执行。

## 结果绑定

三元式的结果可以继续用在常量、条件、实参和返回值里：

```typescript
const a: number = true ? 1 : 2;
function choose(flag: boolean): number {
  return flag ? 10 : 20;
}
console.log(add(true ? 1 : 2, false ? 3 : 4));
```

NIR 用 `select` 语句把结果绑定到临时值：

```json
{ "op": "select", "name": "_tmp_1", "type": "number",
  "cond": {"op": "bool", "value": true},
  "then": [],
  "thenValue": {"op": "call", "function": "pick", "args": [...]},
  "else": [],
  "elseValue": {"op": "call", "function": "pick", "args": [...]} }
```

`select` 先求值条件；条件为真时执行 `then` 列表，在同一个子作用域里求值 `thenValue`，再赋给 `name`；为假时走 `else`。`name` 在语句之后可见，类型由 `type` 声明，必须是 `number` 或 `bool`。

## 分支内部的准备

分支如果本身是短路或嵌套三元式，它的准备语句留在自己的 `then` / `else` 列表里：

```typescript
console.log(mark(3, true) ? (mark(4, true) ? 40 : 50) : mark(5, true) ? 60 : 70);
```

外层只执行 `mark(3)` 与内层三元式，`mark(5)` 不会被调用。`select` 的 `then` / `else` 只接受 `let`、`short` 与 `select`，里面的局部值只对对应的 `thenValue` / `elseValue` 可见。

## 类型规则

- 条件必须是 `boolean`。
- 两条分支必须是同一个标量类型：都为 `number` 或都为 `boolean`。
- TypeScript 允许 `true ? 1 : false` 得到联合类型 `number | false`；本阶段不把联合类型带进原生后端，按 `cap.bad-type` 拒绝。
- 未选中的分支同样过能力检查：`false ? (1 % 2) : 3` 仍报 `cap.bad-operator`。

## 与短路的区别

`&&` / `||` 的结果是布尔值，操作数也必须是布尔值；三元式的两条分支可以是 `number`。两者都用临时值绑定结果，但 `short` 只绑定 `bool`，`select` 的 `type` 由分支决定。

## 相关实现

- NIR 的 `select` 形状见 [NIR 格式](nir.md)。
- 降低器如何把分支准备留在分支内部见[从源码到 NIR](lowering.md)。
- bridge 如何生成条件赋值见 [bridge 说明](bridge.md)。
