# 05.04 验收记录：跨模块名称绑定

本文件记录 05.04 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 05.04`；用 `git log --grep 'Task: 05.04'` 可以定位。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `7a75b4ac` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 正向验收

夹具在 `compiler/graph/testdata/modules`：`a.ts` 和 `b.ts` 都导出 `helper`，`a.ts` 还导出顶层 `base = 40`；`main.ts` 用 `h`、`h2` 和 `base` 导入。

```text
$ ./bin/tynel dump modules --root compiler/graph/testdata/modules main.ts
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

- 两个 `helper` 分别落到 `a.ts#helper` 和 `b.ts#helper`，没有因为同名合并。
- 导入重命名 `h` / `h2` 只改变本地名，目标身份仍是导出模块和导出名。
- 顶层 `base` 绑定到 `a.ts#base`；初始化顺序留到 05.05。

### 事实里的跨模块目标

`consumer -facts main.ts a.ts b.ts` 输出：

```json
"imports": [
  {"local": "h", "imported": "helper", "module": "…/a.ts"},
  {"local": "base", "imported": "base", "module": "…/a.ts"},
  {"local": "h2", "imported": "helper", "module": "…/b.ts"}
],
"calls": [
  {"calleeName": "helper", "calleeModule": "…/a.ts"},
  {"calleeName": "helper", "calleeModule": "…/b.ts"}
]
```

`CalleeModule` / `CalleeName` 是检查器给出的符号证据；Go 侧再用导入表核对，不靠字符串拼接猜目标。

## 负向验收

### 缺导出

```text
$ ./bin/tynel dump modules --root compiler/graph/testdata/modules missing.ts
tynel: 源码错误 (module.missing-export): 模块 …/a.ts 没有导出 "nope"（被 …/missing.ts 导入）
$ echo $?
1
```

### 函数捕获模块级 const

```text
$ ./bin/tynel dump modules --root compiler/graph/testdata/modules capture-main.ts
tynel: 源码错误 (module.captured-variable): 函数 "add" 读取模块级变量 "offset"；本阶段不实现闭包或隐式模块环境
$ echo $?
1
```

### 重复导出与伪造模块引用

`compiler/modules` 的单元测试直接构造事实：

- `TestBuildRejectsDuplicateExport`：同一模块两个同名导出，得到 `module.duplicate-export`。
- `TestBuildRejectsMissingModule`：导入 `forged.ts`，得到 `module.missing-module`。

实际夹具 `dup.ts` 的两个同名函数会先被 TypeScript 判为重复实现，前端事实在解码前就失败；模块表的重复导出检查用于合成事实和未来更细的导出形状。

## 身份表示

- `Declaration.Module` 是模块绝对路径，`Declaration.ID` 只在模块内唯一。
- `Facts.Imports` 记录本地名、导入名、解析后的模块和 type-only 标记。
- `Facts.Exports` 记录导出名和模块内声明 ID。
- 跨模块引用是 `(Module, ID)`；报告里的 `模块#名字` 只是可读形式。

## 当前边界

- `dump modules` 建立符号表；跨模块函数调用和顶层值初始化的 NIR 合并留到 05.05 / 05.06。
- 值导入在 `check` / `build` 里仍报 `cap.import-value`；本轮不执行跨模块程序。
- 默认导入、命名空间导入不在子集内。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 16 个包通过，根模块 424 个测试（204 个顶层 + 220 个子测试） |
| `tests/source` | 105 个用例（7 个测试函数，153 个子测试） |
| `cmd/tynel` | 27 个测试函数，6 个子测试 |
| `tests/native` | 12 个用例 |
| `./bridge/go/check` | 13 项全过 |
| `./bridge/tsgo/check` | 91 项全过 |
| `go vet ./...` | 无输出 |

## 未覆盖

- 跨模块调用的 NIR 合并和模块初始化顺序。
- 默认导入、命名空间导入、循环值依赖。
- 只在 `darwin/arm64` 上验证。
