# 原生能力白名单

本页说明能力检查负责什么、当前支持哪些写法，以及每个错误码的含义。

## TypeScript 接受，Tynel 还不支持

```typescript
function identity(value: any): any {
  return value;
}
```

这段代码没有任何类型错误，`tsc` 会痛快地接受它。Tynel 不行：`any` 在机器码里没有对应表示——它能装数字、字符串、对象，而原生后端只有 `float64` 的槽位。要支持它就得先有一套装箱表示，那是另一个阶段的事。

所以编译器要回答两个不同的问题：

| 问题 | 谁回答 | 失败时退出 |
| --- | --- | --- |
| 这段代码本身有没有错？ | 类型检查（前端） | 1，源码错误 |
| 这段代码能不能变成机器码？ | 能力检查（本包） | 3，能力限制 |

把两者混成一个「编译失败」会让使用者分不清该改代码还是该等实现。上面那段 `identity` 的诊断长这样：

```json
{
  "line": 1,
  "column": 1,
  "category": "能力限制",
  "code": "cap.any",
  "message": "函数 \"identity\" 的返回类型是 any 或 unknown；本阶段只支持 number 和 void"
}
```

## 检查什么时候跑，跑哪些地方

顺序是固定的：先做源码检查，源码有错就直接报错退出 1——类型都没定下来，「这个类型有没有原生表示」无从谈起。源码干净之后才进入能力检查。

能力检查遍历事实里的**每一个**函数体和常量初始化表达式，包括没有被调用的函数。它不做可达性分析：

```typescript
function neverCalled(n: number): number {
  while (n < 3) {
    console.log(n);
  }
  return n;
}
```

这个函数没人调用，`while` 照样被拒绝。`return` 之后的不可达语句也一样：它们不会执行，但能力检查仍然遍历。理由是「今天不可达」和「以后也不会被调用」是两件事，而按可达性跳过检查，等到有人接上调用时才会在更靠后的阶段炸掉——那时离原因已经很远了。

## 当前支持什么

| 类别 | 支持 | 说明 |
| --- | --- | --- |
| 声明 | `function`、参数、顶层 `const`、函数体内 `const` 与已初始化的 `let` | 参数、常量与局部值限 `number`、`boolean`，返回值限 `number`、`boolean` 或 `void`；模块级 `let` 与 `var` 拒绝 |
| 运算 | `+` `-` `*` `/`，一元 `-` `+`、`!`，比较 `<` `<=` `>` `>=` `===` `!==`，短路 `&&` `\|\|`，三元式 `? :` | 有序比较只接受 `number`；严格相等要求两侧同类型；`&&` / `\|\|` 两侧都必须是 `boolean`；三元式的条件必须是 `boolean`，两臂同类型 |
| 调用 | 本文件内声明的函数，实参个数与形参一致 | 不支持函数值、方法调用 |
| 语句 | `return`、表达式语句、语句块、`if` / `else` / `else if`、`while`、`break` / `continue`、分支内 `const` 与已初始化的 `let`、独立的简单赋值 | 表达式语句的结果被丢弃；`if` / `while` 的条件必须是 `boolean`；`break` / `continue` 只能作用于最近循环 |
| 输出 | `console.log(<number 或 boolean>)` | 必须是内置 `console`，见下 |

`boolean` 现在是完整的标量：可以作为常量、参数、返回值和输出。`!` 只对 `boolean` 取反；TypeScript 允许 `!1`，但本阶段不把数字的真假转换带进原生后端，`!1` 按 `cap.bad-type` 拒绝。

比较分两类：`<` `<=` `>` `>=` 只接受两个 `number`；`===` `!==` 要求两侧同为 `number` 或同为 `boolean`。`==` 与 `!=` 是合法 TypeScript，但会做隐式类型转换，本阶段按 `cap.bad-operator` 拒绝。`true < false` 在 TypeScript 里能通过，能力检查按 `cap.bad-type` 拒绝——布尔值没有原生顺序。混合类型比较（`1 === true`）由 TypeScript 直接拒绝。

`&&` 与 `||` 的两个操作数都必须是 `boolean`。TypeScript 允许 `true && 1`，结果类型是 `1`；本阶段按 `cap.bad-type` 拒绝，不做数字真假转换。右侧即使被短路跳过也要检查：`false && (1 % 2 === 0)` 仍报 `cap.bad-operator`。

三元式的条件必须是 `boolean`，两条分支必须同为 `number` 或同为 `boolean`。TypeScript 允许 `true ? 1 : false` 得到 `number | false`；本阶段按 `cap.bad-type` 拒绝，不把联合类型带进原生后端。未选中的分支同样遍历：`false ? (1 % 2) : 3` 仍报 `cap.bad-operator`。

分支支持 `if`、`else`、`else if` 和嵌套分支。条件必须是 `boolean`；`if (1)` 在 TypeScript 里合法，能力检查按 `cap.bad-type` 拒绝。函数体内可以声明 `const` 和带初始值的 `let`，作用域是所在的块；未初始化的 `let` 按 `cap.unsupported-syntax` 拒绝，模块级 `let` 按 `cap.module-let` 拒绝，`var` 按 `cap.unsupported-syntax` 拒绝。能力检查会遍历未选中的分支，`if (false) { ... }` 里的未实现写法同样拒绝。

简单赋值只支持 `x = value;` 这种独立语句，目标必须是可变的 `let`，右值与目标同类型。给 `const`、参数或模块常量赋值报 `cap.assign-immutable`；赋值出现在表达式里报 `cap.assignment-value`；复合赋值报 `cap.compound-assignment`；`++` / `--` 报 `cap.inc-dec`。

`while` 的条件必须是 `boolean`，循环体在函数体内，可以包含 `if`、赋值和嵌套 `while`。条件每轮重新求值，循环可能零次执行；模块级 `while` 报 `cap.unsupported-syntax`。`break` / `continue` 只作用于最近循环，带标签的写法报 `cap.labeled-jump`，循环外的跳转由 TypeScript 先报 `TS1107`。`for` 与 `do` 仍不在子集内。

## void 与丢弃结果

`void` 只描述「没有值」，所以它只能出现在两个位置：作为函数的返回类型，以及作为一条表达式语句的完整表达式。

```typescript
function announce(n: number): void {
  console.log(n);
}

announce(1);        // 可以：调用语句，结果丢弃
console.log(announce(1));  // 拒绝：void 没有值可以传
const x = announce(1);     // 拒绝：void 没有值可以绑
```

`announce(1)` 作为参数、操作数、返回值或常量初始值时，报 `cap.void-value`。TypeScript 的类型检查能拦住其中一部分，但 `console.log` 的形参是 `any[]`，把 void 传进去它不会吭声——这条必须由能力检查补上。

表达式语句的规则相反：**结果被丢弃，不打印**。`answer;` 与 `add(1, 2);` 都是合法写法，求值照做，值不进输出。只有 `console.log(x)` 会写一行。

## 错误码

| 代码 | 触发条件 |
| --- | --- |
| `cap.any` | 参数、返回值或常量声明为 `any` / `unknown` |
| `cap.type-escape` | `as`、`<T>x`、后缀 `!`（非空断言）、`satisfies` 这类绕过检查的写法 |
| `cap.unsupported-syntax` | 对象、数组、字符串、`for` / `do`、未初始化的 `let`、`var`、`new`、嵌套函数等本阶段不支持的语法 |
| `cap.unknown-callee` | 调用目标不是本文件声明的函数，也不是 `console.log` |
| `cap.forbidden-global` | 调用 `eval` |
| `cap.shadowed-console` | `console` 被本文件的声明遮蔽 |
| `cap.bad-console-call` | `console.log` 的参数不是恰好一个 `number` 或 `boolean` |
| `cap.bad-operator` | 运算符不在算术、一元 `- +`、`!` 与比较之内，包括宽松 `==` / `!=` |
| `cap.module-let` | 模块级 `let`；模块体只接受 `const` |
| `cap.assign-immutable` | 给 `const`、参数或模块常量赋值 |
| `cap.assign-target` | 赋值目标不是直接名字 |
| `cap.assignment-value` | 赋值出现在表达式里，例如 `const y = (x = 1)` |
| `cap.compound-assignment` | 复合赋值 `+=`、`-=` 等 |
| `cap.inc-dec` | 自增自减 `++` / `--` |
| `cap.labeled-jump` | 带标签的 `break` / `continue` 或带标签语句 |
| `cap.loop-target` | `break` / `continue` 不在循环里 |
| `cap.unknown-name` | 引用了本文件之外的名称 |
| `cap.arg-count` | 实参个数与函数声明的形参个数不一致 |
| `cap.void-value` | 把 void 调用当作值使用：实参、操作数、返回值、常量初始值 |
| `cap.bad-type` | 声明的类型不在本阶段范围内，例如 `string` 参数；或 `!` 的操作数不是 `boolean`、`if` 的条件不是 `boolean`、`&&` / `\|\|` 的操作数不是 `boolean` |

错误消息里带着具体的写法名（`对象字面量`、`控制流 IfStatement`），因为「不支持」这三个字本身不提供任何线索。

## 为什么 `console.log` 要核对来源

名字叫 `console.log` 不代表它就是内置的那个。如果文件里写了

```typescript
const console = { log: (x: number) => x };
```

那么 `console.log(42)` 调用的是用户的函数，不是输出。能力检查因此不看名字，而是看 `console` 这个标识符解析到了哪里：解析到本文件里的声明就是遮蔽，按 `cap.shadowed-console` 拒绝。

这条检查与输出是否已实现无关：能力检查的职责是判断「这段程序能不能进后端」。输出本身在 02.06 已经可用，格式见[输出格式](output.md)。

## 检查不做什么

- 不重新做类型检查。类型错误由前端报，能力检查信任已经通过检查的事实。
- 不做常量折叠、不做可达性分析、不判断某个分支会不会执行。
- 不生成任何代码。它只回答「能不能」，怎么生成是降低阶段的事。

能力检查之前会先校验事实本身是否自洽（见[声明身份与事实](facts.md)）：ID 重复或引用悬空时直接报内部故障，而不是基于坏数据给出能力结论。
