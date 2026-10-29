# 05.01 验收记录：项目根与路径解析

本文件记录 05.01 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 05.01`；用 `git log --grep 'Task: 05.01'` 可以定位。

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

`compiler/project/testdata/project` 包含：

- `入口 文件.ts`：中文和空格。
- `子目录/工具 一.ts`：子目录、中文、空格。
- `src/同名.ts` 与 `other/同名.ts`：不同目录下的同名文件，内容不同。
- `src/` 用于测试错误大小写 `SRC/`。

## 正向验收

### 不同工作目录，同一项目根

```text
$ cd /Users/mahoo/Projects/tynel
$ ./bin/tynel build --root compiler/project/testdata/project "入口 文件.ts" -o /tmp/root-a
已生成 /tmp/root-a（2429874 字节）
  源码     compiler/project/testdata/project/入口 文件.ts（b64ea1f50955）
  输入     8c62528a22a9
  编译器   7a75b4acf10b
  构建记录 /tmp/root-a.build.json

$ cd /tmp
$ /Users/mahoo/Projects/tynel/bin/tynel build \
    --root /Users/mahoo/Projects/tynel/compiler/project/testdata/project \
    "入口 文件.ts" -o /tmp/root-b
已生成 /tmp/root-b（2429874 字节）
  源码     compiler/project/testdata/project/入口 文件.ts（b64ea1f50955）
  输入     8c62528a22a9
  编译器   7a75b4acf10b
  构建记录 /tmp/root-b.build.json

$ shasum -a 256 /tmp/root-a /tmp/root-b
4680fed9176f66384ca3e55f86b888b96c2dcf733012ef3c625f0e022731d2fb  /tmp/root-a
4680fed9176f66384ca3e55f86b888b96c2dcf733012ef3c625f0e022731d2fb  /tmp/root-b
```

两个工作目录、同一个项目根，产物逐字节相同。入口的相对路径按项目根解释，不按工作目录解释。

### 中文、空格与同名文件

```text
$ ./bin/tynel run --root compiler/project/testdata/project "入口 文件.ts"
1
```

`compiler/project` 的测试还确认：

- `入口 文件.ts` 可以导入 `./子目录/工具 一.ts`。
- `src/同名.ts` 与 `other/同名.ts` 的 `Path` 和 `SHA256` 都不同，不会被合并。
- 根内绝对入口与相对入口得到同一个 `Identity()`。

### 文件身份

`File.Identity()` 是 `相对路径@sha256`。换工作目录、换绝对路径写法，身份不变；绝对路径只用于启动前端宿主和显示诊断。

## 负向验收

| 输入 | 错误码 | 退出 |
| --- | --- | --- |
| 入口 `缺失.ts` | `project.missing` | 1 |
| 入口 `../outside.ts` | `project.outside-root` | 1 |
| 入口 `SRC/同名.ts`（磁盘上是 `src/`） | `project.case-conflict` | 1 |
| 导入 `工具.ts`（裸模块） | `project.bare-module` | 1 |
| 导入 `/tmp/工具.ts`（绝对路径） | `project.absolute-path` | 1 |
| 符号链接指向根外 | `project.outside-root` | 1 |
| 项目根不存在 | `project.root-missing` | 4 |

具体输出：

```text
$ ./bin/tynel check --root compiler/project/testdata/project 缺失.ts
tynel: 源码错误 (project.missing): 入口 缺失.ts 不存在
$ echo $?
1

$ ./bin/tynel check --root compiler/project/testdata/project ../outside.ts
tynel: 源码错误 (project.outside-root): 入口 "../outside.ts" 越出项目根 …/testdata/project
$ echo $?
1

$ ./bin/tynel check --root compiler/project/testdata/project SRC/同名.ts
tynel: 源码错误 (project.case-conflict): 入口 SRC/同名.ts 的大小写与磁盘不一致：磁盘上是 src
$ echo $?
1

$ ./bin/tynel check --root /tmp/definitely-missing-tynel-root 入口.ts
tynel: 环境缺失 (project.root-missing): 项目根 /tmp/definitely-missing-tynel-root 不存在或不可访问
$ echo $?
4
```

越界符号链接：

```text
$ ./bin/tynel check --root "$tmp" link.ts
tynel: 源码错误 (project.outside-root): 入口 link.ts 的符号链接指向项目根外：…/outside.ts
$ echo $?
1
```

裸模块与绝对导入没有进入源码语法，因此由 `compiler/project` 的 `TestResolveRejects` 直接调用 `ResolveImport` 验证，分别得到 `project.bare-module` 与 `project.absolute-path`。失败后没有补后缀、换目录或搜索同名文件。

## 解析流程与边界

许可根是规范化后的绝对目录；入口和导入都先拼成项目内相对路径，`path.Clean` 后必须仍在根内，且以 `.ts` 结尾。逐段精确匹配大小写，最后一段允许符号链接但目标必须在根内。流程和两个实际相对路径的例子见[项目根与路径解析](../reference/project-paths.md)。

本阶段只把入口接到解析器；`import` 语法和依赖图留给 05.02。解析器接口已经提供 `ResolveImport`，但还没有被前端调用。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 14 个包通过，根模块 404 个测试（188 个顶层 + 216 个子测试） |
| `tests/source` | 105 个用例（7 个测试函数，153 个子测试） |
| `cmd/tynel` | 27 个测试函数，6 个子测试 |
| `tests/native` | 12 个用例 |
| `./bridge/go/check` | 13 项全过 |
| `./bridge/tsgo/check` | 83 项全过 |
| `go vet ./...` | 无输出 |

## 未覆盖

- 没有多文件载入；`import` 仍由能力检查拒绝。
- 没有读 `tsconfig.json`，不扩展 `paths` / `baseUrl`。
- 没有解析裸模块或 `node_modules`。
- 只在 `darwin/arm64` 上验证。
