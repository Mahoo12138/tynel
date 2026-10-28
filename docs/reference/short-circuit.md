# 短路求值

`&&` 与 `||` 先求值左侧，再决定要不要碰右侧。左侧只求值一次；右侧被跳过时，它的调用、打印和临时值都不会发生。

```typescript
function mark(n: number, value: boolean): boolean {
  console.log(n);
  return value;
}

console.log(mark(1, false) && mark(2, true));
console.log(mark(3, true) || mark(4, false));
```

输出是：

```text
1
false
3
true
```

`2` 和 `4` 没有出现：`false && ...` 直接得到 `false`，`true || ...` 直接得到 `true`，右侧的 `mark` 没有被调用。

## 结果绑定

短路表达式的结果是一个 `boolean`，可以继续用在常量、条件、实参和返回值里：

```typescript
const a: boolean = mark(1, true) && mark(2, false);
if (mark(3, true) || mark(4, false)) {
  console.log(5);
}
console.log(both(mark(6, true), mark(7, true)));
```

NIR 不为 `&&` 造一个表达式节点，而是用一个 `short` 语句把结果绑定到临时值：

```json
{ "op": "short", "name": "_tmp_1", "operator": "&&",
  "left": {"op": "call", "function": "mark", "args": [...]},
  "right": [],
  "value": {"op": "call", "function": "mark", "args": [...]} }
```

之后表达式里用 `{"op":"local","name":"_tmp_1"}` 读取结果。`short` 先执行 `left` 并赋给 `name`；`&&` 在 `name` 为真时执行 `right` 列表，再把 `value` 赋给 `name`；`||` 在 `name` 为假时做同样的事。

## 右侧的准备工作

右侧如果本身是嵌套短路，它的准备工作不能提到外层。`a && (b && c)` 的 NIR 是：

```text
short t1 && a:
  right: [short t2 && b: value c]
  value: local t2
```

`t2` 的 `short` 只在 `a` 为真时执行；`t1` 的 `right` 列表就是它的位置。右侧块里的局部值只在这个块里可见，不会泄漏到外层。

## 实参顺序

带短路的实参会把求值顺序拉长，降低器在需要时把前面的实参物化成临时值。`pair(mark(4, true), mark(5, true) && mark(6, false))` 的输出是 `4`、`5`、`6`：`mark(4)` 先算并保存，再进入右侧的短路；如果直接把它留在调用参数里，它会被推迟到 `5`、`6` 之后。

```text
pair(mark(4, true), mark(5, true) && mark(6, false))
→ 4
→ 5
→ 6
→ false
```

## 类型规则

`&&` 与 `||` 的两个操作数都必须是 `boolean`，结果也是 `boolean`。TypeScript 允许 `true && 1`（结果类型是 `1`），本阶段不把数字真假转换带进原生后端，按 `cap.bad-type` 拒绝。未选中的右侧同样过能力检查：`false && (1 % 2 === 0)` 仍报 `cap.bad-operator`。

## 相关实现

- NIR 的 `short` 形状与右侧准备块见 [NIR 格式](nir.md)。
- 降低器如何把右侧列表留在条件内部见[从源码到 NIR](lowering.md)。
- bridge 如何生成条件赋值见 [bridge 说明](bridge.md)。
