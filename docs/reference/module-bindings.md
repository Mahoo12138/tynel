# 跨模块名称绑定

05.04 把「导入」从类型边推进到名称绑定：一个模块导出的函数和顶层 `const`，可以被另一个模块按名字引用。绑定不是把名字拼成字符串，而是用 TypeScript 检查器给出的符号证据，落到「模块路径 + 模块内声明 ID」这个完整身份上。

## 同名函数

`compiler/graph/testdata/modules`：

```typescript
// a.ts
export function helper(x: number): number {
  return x + 1;
}
export const base = 40;

// b.ts
export function helper(x: number): number {
  return x + 2;
}

// main.ts
import { helper as h, base } from "./a.ts";
import { helper as h2 } from "./b.ts";

console.log(h(base));
console.log(h2(base));
```

两个模块都叫 `helper`，但它们是两个不同的声明：

| 本地名 | 导入名 | 目标模块 | 目标声明 |
| --- | --- | --- | --- |
| `h` | `helper` | `a.ts` | `a.ts#helper` |
| `h2` | `helper` | `b.ts` | `b.ts#helper` |
| `base` | `base` | `a.ts` | `a.ts#base` |

`dump modules` 输出：

```json
{
  "entry": "…/main.ts",
  "modules": [
    {"path": "…/a.ts", "exports": ["base", "helper"], "imports": {}},
    {"path": "…/b.ts", "exports": ["helper"], "imports": {}},
    {"path": "…/main.ts", "exports": null, "imports": {
      "base": "…/a.ts#base",
      "h": "…/a.ts#helper",
      "h2": "…/b.ts#helper"
    }}
  ]
}
```

`h` 和 `h2` 不会因为都叫 `helper` 而混在一起。导入重命名只改变本地绑定名，目标身份仍是导出模块和导出名。

## 完整身份

- 模块身份是规范化的绝对路径，来自依赖图的 `project.File.Abs`。
- 模块内身份是声明 ID；它只在同一个模块内唯一。
- 跨模块引用是 `(Module, ID)`，报告里写成 `模块#导出名` 只是为了可读。
- 前端事实额外记录 `CalleeModule` / `CalleeName`，Go 侧符号表再用 `Imports` 核对；两边都同意才建立绑定。

不用「把模块名和函数名拼起来再查表」替代检查器符号：同名函数、重命名导入和未来可能的同名导出都靠符号证据区分。

## 导出验证

`modules.Build` 在进入降低前检查：

| 情况 | 错误码 |
| --- | --- |
| 导入的模块不在依赖图里 | `module.missing-module` |
| 目标模块没有这个导出 | `module.missing-export` |
| 同一模块重复导出同名声明 | `module.duplicate-export` |
| 值导入指向纯类型导出 | `module.type-import` |
| 函数读取本模块顶层 `const` | `module.captured-variable` |

最后一条是刻意的：顶层 `const` 在模块初始化阶段写入，函数读取它需要闭包或隐式模块环境；本轮不实现，直接拒绝。顶层值引用只允许出现在模块初始化表达式和导入绑定里。

## 当前边界

- 只支持命名导出、命名导入和导入重命名；默认导入、命名空间导入仍不在子集内。
- `dump modules` 只建立符号表；跨模块函数调用和顶层值初始化的 NIR 合并留到 05.05 / 05.06。
- 值导入在 `check` / `build` 的能力检查里仍会被拒绝；`dump modules` 可以先查看绑定是否正确。

验收记录见[05.04 验收记录](../validation/05-04-bindings.md)。
