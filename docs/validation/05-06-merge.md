# 05.06 验收记录：合并模块生成原生程序

本文件记录 05.06 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 05.06`；用 `git log --grep 'Task: 05.06'` 可以定位。

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

### 拆分后的 sumTo

`compiler/graph/testdata/merge/math.ts` 导出 `sumTo`，`main.ts` 导入并调用：

```text
$ ./bin/tynel run --root compiler/graph/testdata/merge main.ts
55
```

`dump nir` 的函数：

```text
m1$sumTo
__init$0
__init$1
main
```

`__init$0` 是 `math.ts`，`__init$1` 是 `main.ts`；入口 `main` 依次调用它们，再返回。模块前缀避免同名函数在 NIR 里覆盖。

### 初始化顺序与跨模块 return

`compiler/graph/testdata/init/main.ts`：

```text
$ ./bin/tynel run --root compiler/graph/testdata/init main.ts
1
3
2
4
23
```

前四行是 `shared.ts`、`b.ts`、`a.ts`、`main.ts` 的顶层打印；`shared.ts` 只执行一次。最后一行 `23` 是 `a + b`，两个顶层 `const` 都按模块身份读取。

### 同名 helper

`compiler/graph/testdata/modules/main.ts`：

```text
$ ./bin/tynel run --root compiler/graph/testdata/modules main.ts
41
42
```

`a.ts` 和 `b.ts` 都导出 `helper`，`h` 和 `h2` 分别调用两个模块的函数；函数名在 NIR 里是 `m0$helper` / `m1$helper`，没有互相覆盖。

### NIR 回放不需要源码

```text
$ ./bin/tynel dump nir --root compiler/graph/testdata/merge main.ts > /tmp/merge.nir.json
$ ./bin/tynel build-ir /tmp/merge.nir.json -o /tmp/merge-replay
$ /tmp/merge-replay
55
```

`Pos` 里的文件路径只用于诊断，回放不读 TypeScript 源文件。

## 负向验收

| 情况 | 拒绝点 |
| --- | --- |
| 初始化目标不存在 | `modules.Plan` 的 `init.missing-module` / `init.uninitialized` |
| 值依赖环 | `graph.cycle`，`Plan` 再报 `init.cycle` |
| 缺失导出 | `module.missing-export` |
| 同名覆盖 | 合并时函数名加模块前缀，不会覆盖；NIR 验证器再检查重名 |
| 跨模块签名错配 | TypeScript 诊断；NIR 验证器检查实参数量与类型 |
| 错误源码引用 | `nir.Verify` 拒绝未知函数/参数 |

`nir.Verify` 是独立验证器；合并降低产出不合法 NIR 时返回 `lower.invalid-output`，不会把半份程序交给后端。

## 实现要点

- `compiler/merge` 重新分配全局声明 ID，函数名加 `mN$` 前缀，重映射 `Decl`、`Target`、`Callee` 和跨模块引用。
- `lower.LowerMerged` 把每个模块体降低成 `__init$N`，入口 `main` 按 `order` 调用它们。
- 顶层 `const` 在初始化阶段按表达式内联读取；函数读取模块级 `const` 仍由 `module.captured-variable` 拒绝。
- `build` / `run` 对多文件使用合并 NIR；单文件仍走同一条合并路径（只有一个模块）。
- `dump nir` 对多文件输出合并后的 NIR，供 `build-ir` 回放。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 16 个包通过，根模块 430 个测试（210 个顶层 + 220 个子测试） |
| `tests/source` | 105 个用例（7 个测试函数，153 个子测试） |
| `cmd/tynel` | 27 个测试函数，6 个子测试 |
| `tests/native` | 12 个用例 |
| `./bridge/go/check` | 13 项全过 |
| `./bridge/tsgo/check` | 96 项全过 |
| `go vet ./...` | 无输出 |

## 未覆盖

- 默认导入、命名空间导入、函数捕获模块级 `const`。
- 任意顶层初始化表达式的完整存储模型；当前按表达式内联。
- 只在 `darwin/arm64` 上验证。
