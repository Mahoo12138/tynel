# 模块图与依赖快照

05.02 在检查和降低之前先做一件事：从入口出发，用 tsgo 的语法树找出导入，把每个目标文件读成一份快照，并记下这条边是只用于类型还是会产生运行值。图建好之后才进入检查；图里出现缺失目标或环时，整次构建直接失败。

## 一张图

以 `compiler/graph/testdata/diamond` 为例：

```text
main.ts
├── value: ./a.ts ──────> a.ts
│                         ├── value: ./shared.ts ──> shared.ts
│                         └── type:  ./types.ts ───> types.ts
└── value: ./b.ts ──────> b.ts
                          └── value: ./shared.ts ──> shared.ts
```

`value` 箭头会参与运行：目标文件里的函数和常量在程序运行时需要存在。`type` 箭头只用于检查：它让类型检查器看到声明，但不会生成运行时值。两种边都要载入快照，因为检查器需要类型信息；区别在于后续降低时只有值边会变成运行依赖。

`shared.ts` 在两条路径上都出现，但图里只保留一个文件和一个快照。菱形依赖不会复制文件，也不会因为到达顺序不同而得到两份内容。

## 解析日志

`tynel dump graph` 输出图的稳定形状：

```json
{
  "entry": "main.ts",
  "files": [
    {"path": "main.ts", "sha256": "ae5c8429…"},
    {"path": "a.ts", "sha256": "d0399273…"},
    {"path": "shared.ts", "sha256": "e85f9652…"},
    {"path": "types.ts", "sha256": "1fac0388…"},
    {"path": "b.ts", "sha256": "61c96df3…"}
  ],
  "edges": [
    {"from": "main.ts", "to": "a.ts", "specifier": "./a.ts", "typeOnly": false, "start": 45, "length": 9},
    {"from": "a.ts", "to": "shared.ts", "specifier": "./shared.ts", "typeOnly": false, "start": 86, "length": 14},
    {"from": "a.ts", "to": "types.ts", "specifier": "./types.ts", "typeOnly": true, "start": 129, "length": 13},
    {"from": "main.ts", "to": "b.ts", "specifier": "./b.ts", "typeOnly": false, "start": 73, "length": 9},
    {"from": "b.ts", "to": "shared.ts", "specifier": "./shared.ts", "typeOnly": false, "start": 41, "length": 14}
  ],
  "hash": "e580ced98df33889c729b749777e5e507a86b5c2ea455ce91027c94a92d8850b"
}
```

`start` 和 `length` 是导入路径在源文件里的 UTF-8 字节区间，用来把错误定位回依赖文件。`hash` 只由文件路径和内容哈希组成，排序后计算；同一组文件换个发现顺序，哈希不变。

## 解析流程

1. 入口由 `project` 按项目根解析，得到第一份快照。
2. 宿主用 `-imports` 让 tsgo 读入口语法树，返回 specifier、位置和 type-only 标记。
3. 每条导入交给 `project.ResolveImport`：只接受 `./` 或 `../` 开头的 `.ts`，大小写精确，符号链接不能越界。
4. 目标文件读入快照，记录边，再递归处理目标文件的导入。
5. 已经完成的文件不重复载入；正在访问的文件再次出现就是环，直接报 `graph.cycle`。
6. 全部文件固定后计算图哈希。检查、降低和构建记录都引用这一版快照。

失败不返回半个图：缺失目标报 `project.missing`，环报 `graph.cycle`，tsgo 与项目解析不一致报 `graph.resolution-mismatch`。

## 检查器文件访问

前端宿主新增 `Imports` 查询，只读语法树，不按字符串搜索 `import`。`import`、`import type`、`import { type X }` 和副作用导入都由 tsgo 的 AST 给出；`typeOnly` 由导入子句和命名说明符共同判断。依赖文件全部进入程序后，`Resolved` 使用 tsgo 的模块解析结果，Go 侧再与 `project` 的解析结果对照。

## 本轮边界

- 只支持显式相对 `.ts` 导入；裸模块、目录导入、后缀推断仍拒绝。
- 依赖环本轮一律拒绝，包括只用于类型的环。
- 图已经固定全部快照，但跨模块的名称绑定和值导入要到 05.04 才接入降低；`dump graph` 可以单独查看图，`check` 和 `build` 现在会先载入并校验图。
- 没有读 `tsconfig.json`，不扩展 `paths` / `baseUrl`。

验收记录见[05.02 验收记录](../validation/05-02-graph.md)。
