# 类型别名与纯类型导入

05.03 让类型别名进入编译器，但只让它停在类型空间。`type Amount = number` 给 `number` 起了一个名字，不会多出一个运行时存储类型，也不会产生初始化动作。跨文件时只用 `import type`，导入边是类型边，不进入运行依赖。

## 一份共享声明

`compiler/graph/testdata/alias/types.ts`：

```typescript
export type Amount = number;
```

`main.ts`：

```typescript
import type { Amount } from "./types.ts";

const value: Amount = 41;
console.log(value + 1);
```

`Amount` 和 `number` 在检查器里是同一个基础类型。`value` 的存储仍然是 `number`，NIR 里仍然是 64 位位模式；别名没有自己的标签、宽度或初始化函数。`import type` 只把名字带进类型空间，降低阶段直接跳过，运行输出只有 `42`。

## 类型空间与值空间

- `Amount` 只能出现在类型注解、返回类型、参数类型里。
- `Amount` 不能当值使用；`const x = Amount` 由 TypeScript 报 `TS2693`。
- `import type { Amount }` 不能调用；`Amount(1)` 同样报 `TS2693`。
- 值导入是另一回事：`import { a } from "./a.ts"` 会产生运行依赖，本轮报 `cap.import-value`，留到 05.04。

别名不会出现在事实的 `Declarations` 里，它单独放在 `Aliases`：名字、位置和类型类别。这样能力检查可以拒绝接口和联合，又不会把别名误当成一个可赋值的局部变量。

## 允许的类别

| 写法 | 结果 |
| --- | --- |
| `type Amount = number` | 允许 |
| `type Flag = boolean` | 允许 |
| `type Amount = number \| boolean` | `cap.bad-type`，联合拒绝 |
| `type Amount = any` | `cap.any` |
| `interface Amount { value: number }` | `cap.interface` |
| `type A = B; type B = A` | TypeScript `TS2456`，循环别名拒绝 |
| `Amount` 当值 | TypeScript `TS2693` |
| `Amount(1)`（纯类型导入） | TypeScript `TS2693` |

别名链只允许最终落到 `number` 或 `boolean`。`type A = B` 且 `B = number` 可以通过；链里出现接口、联合或 `any` 就拒绝。

## 修改别名后的错误

把 `types.ts` 改成：

```typescript
export type Amount = string;
```

入口不变，`check` 报：

```text
main.ts:3:7: error TS2322: Type 'number' is not assignable to type 'string'.
```

这条错误来自使用方，位置在 `main.ts` 第 3 行第 7 列；它证明别名身份是跨文件共享的，而不是每个文件各自复制一份 `number`。改回 `number` 后输出恢复 `42`。

## 检查器配置

前端程序现在打开 `allowImportingTsExtensions` 和 `noEmit`：前者让显式 `.ts` 导入合法，后者保证检查器不写输出。程序级诊断（没有源码位置、偏移为 `-1`）不再硬塞进入口快照，避免把全局选项错误映射到第 0 行。

## 本轮边界

- 只支持非递归、无类型参数、最终为 `number` 或 `boolean` 的别名。
- 不支持接口、联合、字面量联合、对象类型、数组、泛型别名。
- 不支持值导入和跨模块函数/常量绑定；这些留到 05.04。
- 别名不产生运行时值，也不改变数值宽度或初始化顺序。

验收记录见[05.03 验收记录](../validation/05-03-aliases.md)。
