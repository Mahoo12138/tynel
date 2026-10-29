# 05.02 验收记录：依赖图与源码快照

本文件记录 05.02 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 05.02`；用 `git log --grep 'Task: 05.02'` 可以定位。

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

## 夹具

- `compiler/graph/testdata/linear`：`main.ts → a.ts → b.ts`。
- `compiler/graph/testdata/diamond`：`main.ts` 分别导入 `a.ts`、`b.ts`，两者都导入 `shared.ts`；`a.ts` 还 `import type` 了 `types.ts`。
- `compiler/graph/testdata/cycle`：`main.ts → a.ts → b.ts → a.ts`。

## 正向验收

### 线性依赖

```text
$ ./bin/tynel dump graph --root compiler/graph/testdata/linear main.ts
…
"files": 3 个，3 条边中的 2 条
"hash": "2417bbf198bc…"
```

入口、`a.ts`、`b.ts` 全部载入，3 个文件、2 条边；`b.ts` 只出现一次。

### 菱形依赖与类型边

```text
$ ./bin/tynel dump graph --root compiler/graph/testdata/diamond main.ts
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

5 个文件、5 条边；`shared.ts` 在两条路径上都出现，但只保留一份快照。`a.ts → types.ts` 的 `typeOnly` 为 `true`，其余为 `false`。

### 图哈希进入构建记录

```text
$ ./bin/tynel dump graph examples/add.ts | python3 -c 'import json,sys; print(json.load(sys.stdin)["hash"])'
17b0c1024dc6ca133d8087aa5b45dbf33aca33adf3ca0fa64c5ed2febd779c55

$ ./bin/tynel build examples/add.ts -o /tmp/add-graph
$ python3 -c 'import json; print(json.load(open("/tmp/add-graph.build.json"))["graphHash"])'
17b0c1024dc6ca133d8087aa5b45dbf33aca33adf3ca0fa64c5ed2febd779c55
```

检查和构建都在分析前调用同一个 `loadGraph`；构建记录保存同一版图哈希，便于复核。

## 负向验收

### 环

```text
$ ./bin/tynel dump graph --root compiler/graph/testdata/cycle main.ts
tynel: 源码错误 (graph.cycle): 依赖环：a.ts -> b.ts -> a.ts
$ echo $?
1
```

### 缺失目标

```text
$ ./bin/tynel dump graph --root /tmp/tynel-missing-graph main.ts
tynel: 源码错误 (project.missing): 导入 nope.ts 不存在
$ echo $?
1
```

### tsgo 与项目解析不一致

`compiler/graph` 的 `TestLoadRejectsResolutionMismatch` 给出一条 `Resolved` 指向根外的导入，得到 `graph.resolution-mismatch`，退出 1。

### 不用正则代替语法树

导入发现走 `tyneladapter.Session.Imports`：tsgo AST 给出 `ImportDeclaration`、`ModuleSpecifier` 和 type-only 标记，`bridge/tsgo/check` 用 `-imports` 验证类型导入为 `true`、值导入为 `false`。源码里的 `import` 字样不会直接被字符串匹配。

## 本轮边界

- 只支持显式相对 `.ts` 导入；裸模块、目录导入、后缀推断仍拒绝。
- 依赖环本轮一律拒绝，包括只用于类型的环。
- 图已固定全部快照，但跨模块名称绑定和值导入尚未进入降低；`import` 在检查阶段仍会被能力检查拒绝，05.03/04 接上类型别名和名称绑定。
- 没有读 `tsconfig.json`。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 15 个包通过，根模块 411 个测试（195 个顶层 + 216 个子测试） |
| `tests/source` | 105 个用例（7 个测试函数，153 个子测试） |
| `cmd/tynel` | 27 个测试函数，6 个子测试 |
| `tests/native` | 12 个用例 |
| `./bridge/go/check` | 13 项全过 |
| `./bridge/tsgo/check` | 86 项全过 |
| `go vet ./...` | 无输出 |

## 未覆盖

- 没有跨模块名称绑定、类型别名导入和运行值导入。
- 没有读 `tsconfig.json`、裸模块或 `node_modules`。
- 只在 `darwin/arm64` 上验证。
