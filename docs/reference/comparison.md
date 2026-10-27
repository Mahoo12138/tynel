# 比较语义

比较运算产生 `boolean`，输入按运算符分两组：

| 运算符 | 左 | 右 | 结果 |
| --- | --- | --- | --- |
| `<` `<=` `>` `>=` | `number` | `number` | `boolean` |
| `===` `!==` | `number` | `number` | `boolean` |
| `===` `!==` | `boolean` | `boolean` | `boolean` |

`==` 与 `!=` 不在子集内，能力检查按 `cap.bad-operator` 拒绝。混合类型比较由 TypeScript 先拒绝（TS2367）；能力检查与降低仍会核对两侧类型，防止伪造的事实绕过前端。

## 求值顺序

左右操作数按源码顺序求值，与四则运算相同。`mark(1) < mark(2)` 先打印 `1`，再打印 `2`，最后打印比较结果。降低保留表达式树的结构，bridge 把它变成 Go 的二元表达式，Go 的求值顺序是左到右。

## 浮点关系表

`number` 是 binary64，比较遵循 IEEE 754。下面这些结果不是实现约定，而是 IEEE 754 与 JavaScript 共同规定的：

| 左 | 右 | `<` | `<=` | `>` | `>=` | `===` | `!==` |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `1` | `2` | true | true | false | false | false | true |
| `2` | `2` | false | true | false | true | true | false |
| `NaN` | `2` | false | false | false | false | false | true |
| `-0` | `0` | false | true | false | true | true | false |
| `+Inf` | `1e308` | false | false | true | true | false | true |
| `-Inf` | `0` | true | true | false | false | false | true |
| `+Inf` | `+Inf` | false | true | false | true | true | false |

三条容易记错的规则：

- **NaN 与任何值的有序比较都是 false**，包括它自己。`NaN === NaN` 是 false，`NaN !== NaN` 是 true。
- **负零等于正零**。`-0 === 0` 是 true，`-0 < 0` 是 false。位模式不同不影响比较。
- **无穷参与有序比较时按数值大小**，`+Inf` 大于任何有限值，`-Inf` 小于任何有限值。

## 为什么不能改写成逻辑补集

一个看起来更省事的实现是把 `a < b` 写成 `!(a >= b)`。它对普通数值成立，对 NaN 不成立：

```text
NaN < 1   → false
!(NaN >= 1) → !false → true
```

两者在 NaN 上给出相反的结果。测试 `compare-specials` 专门覆盖这一组：如果实现改写了逻辑补集，`NaN < 1` 会输出 `true`，用例立刻失败。bridge 因此把每个比较运算符直接映射成一条 Go 比较指令，不做任何改写：

| NIR | Go IR |
| --- | --- |
| `<` | `OLT` |
| `<=` | `OLE` |
| `>` | `OGT` |
| `>=` | `OGE` |
| `===` | `OEQ` |
| `!==` | `ONE` |

Go 的浮点比较本身就是 IEEE 754，NaN、负零和无穷的行为与 JavaScript 一致，不需要额外的运行时函数。`boolean` 的 `===` / `!==` 走同样的 `OEQ` / `ONE`，Go 的 `bool` 相等语义就是两个值相同。

## 不做什么

- 不做隐式类型转换。`1 === true` 不会变成 `1 === 1`，TypeScript 直接拒绝。
- 不做宽松相等。`1 == "1"` 之类的写法不在子集内。
- 不比较字符串、对象和数组。它们还没有原生表示。
- 不把比较结果当数字用。`true + 1` 在 TypeScript 里就是错误，NIR 也不会把 `bool` 送进算术。
