# 03.08 验收记录：选择实验与第三篇

本文件记录 03.08 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 03.08`；完成标签 `mvp/03-choices` 在提交后创建。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `02ba7de9` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 文章示例

`docs/notes/03-choices.md` 使用 `examples/choices.ts`。从源码经主 CLI 构建：

```text
$ ./bin/tynel run examples/choices.ts
-1
0
1
1
false
3
true
```

`pick(-5)`、`pick(0)`、`pick(7)` 分别得到 `-1`、`0`、`1`；`mark(1, false) && mark(2, true)` 只打印 `1`，结果 `false`；`mark(3, true) || mark(4, false)` 只打印 `3`，结果 `true`。

### 保存 NIR 再回放

```text
$ ./bin/tynel build examples/choices.ts -o out/choices
已生成 out/choices（2430018 字节）
  源码     examples/choices.ts（756327498b36）
  输入     5bf3ba493346
  编译器   02ba7de95dd3
  构建记录 out/choices.build.json

$ ./bin/tynel dump nir examples/choices.ts > out/choices.nir.json
$ ./bin/tynel build-ir out/choices.nir.json -o out/choices-replay
已生成 out/choices-replay（2430018 字节）
  输入     5bf3ba493346
  编译器   02ba7de95dd3
  构建记录 out/choices-replay.build.json

$ shasum -a 256 out/choices out/choices-replay
3974006316ff1a74ed90544f5d08febf27ef866ef9ece716c5e190c77802ca39  out/choices
3974006316ff1a74ed90544f5d08febf27ef866ef9ece716c5e190c77802ca39  out/choices-replay
```

两条路径的产物字节相同；`out/choices.nir.json` 里的 `pick` 包含一条 `if` 和一条 `select`，`main` 包含五条 `print`，最后一条 `print` 的值是 `short` 绑定的结果。

### 文章里的错误

```text
$ ./bin/tynel check tests/source/testdata/errors/missing-return.ts
…/tests/source/testdata/errors/missing-return.ts:2:31: error TS2366: Function lacks ending return statement and return type does not include 'undefined'.
$ echo $?
1
```

文章没有展示局部赋值、`let`、`while`、`for` 或递归，因为这些仍不在子集内；支持表把它们标为计划。

## 分支、短路与三元式复跑

文章引用的三个组合程序也重新跑过：

```text
$ ./bin/tynel run tests/source/testdata/combine.ts
1
true
4
5
6
false
7
9
true

$ ./bin/tynel run tests/source/testdata/combine-ternary.ts
1
2
20
3
4
50
2
5
70

$ ./bin/tynel run tests/source/testdata/combine-order.ts
1
2
30
3
4
5
50
7
8
9
10
11
```

Node 对照全部通过，三个用例在 `tests/source/reference.json` 里标为 `shared`。

## 操作支持表

新增[操作支持表](../reference/support.md)，按值、声明与调用、表达式、语句、运行与构建五组列出支持、拒绝和计划状态。文章与支持表对同一件事的说法一致：

- 支持：`number`、`boolean`、`void`、顶层/块内 `const`、直接调用、四则运算、一元 `!`、比较、短路、三元式、`if`、`return`、`console.log`。
- 拒绝：局部 `let`、`==` / `!=`、字符串、对象、数组、联合类型、闭包、模块级 `if`。
- 计划：`while`、`for`、`break` / `continue`、递归、多文件、`windows/amd64`。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 315 个测试（168 个顶层 + 147 个子测试） |
| `tests/source` | 62 个用例（7 个测试函数，93 个子测试） |
| `cmd/tynel` | 25 个测试函数，6 个子测试 |
| `tests/native` | 8 个用例 |
| `./bridge/go/check` | 10 项全过 |
| `./bridge/tsgo/check` | 66 项全过 |
| `go vet ./...` | 无输出 |

## 文档检查

- 第三篇正文只保留 `choices.ts` 的七行输出，完整组合表留在 03.07 验收记录，不把子任务逐项写成章节清单。
- 正文没有出现未支持的局部赋值、循环或递归示例；提到它们时明确标为“还不能做到”。
- 文章命令按顺序实际执行，构建命令先于运行产物，哈希与实际输出一致。
- 相关链接：操作支持表、短路、三元式、返回路径、比较语义、分支、本记录。

## 未覆盖

- 只在 `darwin/arm64` 上验证。
- 文章没有覆盖所有错误码，只选了缺返回路径这一条可复现的负向例子。
- 没有把第三篇翻译成其他语言，也没有生成终端截图；文中的终端块是实际输出的摘录。
