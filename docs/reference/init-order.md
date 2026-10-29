# 模块初始化顺序

05.05 规定模块什么时候执行。顺序不是把依赖图随便排一遍：它从入口出发，按源码里的导入顺序深度优先，先完成值依赖，再执行当前模块。同一个模块只执行一次；纯类型导入不触发执行。

## 源码顺序与打印顺序

夹具 `compiler/graph/testdata/init`：

```typescript
// main.ts
import { b } from "./b.ts";
import { a } from "./a.ts";

console.log(4);
console.log(a + b);
```

```typescript
// b.ts
import { shared } from "./shared.ts";
console.log(3);
export const b = shared + 2;
```

```typescript
// a.ts
import { shared } from "./shared.ts";
console.log(2);
export const a = shared + 1;
```

```typescript
// shared.ts
console.log(1);
export const shared = 10;
```

源码里 `main.ts` 先导入 `b`、再导入 `a`；`b` 和 `a` 都依赖 `shared`。计划输出：

```json
{
  "order": ["shared.ts", "b.ts", "a.ts", "main.ts"],
  "prints": [
    {"module": "shared.ts", "value": "1"},
    {"module": "b.ts", "value": "3"},
    {"module": "a.ts", "value": "2"},
    {"module": "main.ts", "value": "4"}
  ]
}
```

对应打印顺序：

```text
1
3
2
4
```

`shared.ts` 只打印一次，即使它从两条路径到达。`b` 在 `a` 前，是因为 `main.ts` 先写了 `b` 的导入；把两行导入对调，打印顺序也会变成 `1 2 3 4`。这说明顺序来自源码导入顺序加依赖，而不是模块名字典序。

## 类型依赖不执行

`type-main.ts` 只 `import type { T } from "./types.ts"`；`types.ts` 里有 `console.log(9)`，但计划：

```json
{
  "order": ["type-main.ts"],
  "typeEdges": [{"from": "type-main.ts", "to": "types.ts", "typeOnly": true}],
  "prints": [{"module": "type-main.ts", "value": "5"}]
}
```

`types.ts` 不进入 `order`，`9` 不会打印。它仍然参与检查：类型声明必须存在，别名必须能归类；只是不执行模块体。

## 顶层 const 的存储与读取

- 每个需要执行的模块有一条初始化记录，按 `order` 执行一次。
- 顶层 `const` 在所属模块初始化时求值并写入该模块的存储；读取只允许发生在初始化之后。
- 导入的顶层 `const` 通过 `(Module, ID)` 找到目标存储，不复制一份值。
- 函数读取模块级 `const` 仍然拒绝（`module.captured-variable`）；本轮不实现闭包或隐式环境。
- 类型别名没有存储，不产生初始化动作。

05.05 输出的是顺序和规则；真正把这些初始化语句合并进一个 NIR 入口由 05.06 完成。`prints` 只识别模块体里可直接求值的 `console.log(数字字面量)`，用于核对顺序，不是执行器。

## 拒绝条件

| 情况 | 错误码 |
| --- | --- |
| 值依赖成环 | `init.cycle` |
| 初始化时读取未执行模块的值 | `init.uninitialized` |
| 把 `import type` 绑定当值使用 | `init.type-value-mix` |
| 计划里出现不存在的模块 | `init.missing-module` |

依赖图本身已经拒绝所有环；初始化计划再按值边检查一次，避免把类型环和值环混为一谈。

## 本轮边界

- 只排顺序、核对规则，不执行原生程序；NIR 合并留给 05.06。
- 只识别字面量 `console.log` 用于展示；任意初始化表达式仍由后续降低执行。
- 函数捕获模块级值、默认导入、命名空间导入仍拒绝。

验收记录见[05.05 验收记录](../validation/05-05-init.md)。
