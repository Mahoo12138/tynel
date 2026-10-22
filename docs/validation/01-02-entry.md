# 01.02 工程入口与测试骨架验证记录

本记录对应子节点 01.02「建立工程入口、诊断和测试骨架」。被测代码是包含本文件的提交，其正文带 `Task: 01.02`；用 `git log --grep 'Task: 01.02'` 可以定位。

## 环境

| 项目 | 实际值 |
| --- | --- |
| 机型 | Mac mini，Mac16,10，Apple M4，24 GB |
| 系统 | macOS 26.6.2，arm64 |
| 构建与测试用的 Go | `.work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go`，`go version go1.27.3 darwin/arm64` |
| 执行时间 | 2026-10-22 16:07–16:30（CST） |

## 交付物

| 路径 | 内容 |
| --- | --- |
| `go.mod` | 根模块 `github.com/Mahoo12138/tynel`，`go 1.27` |
| `cmd/tynel` | `version`、`help`、`toolchain`、`toolchain which` 与分发逻辑 |
| `compiler/diagnostic` | 四类失败、稳定代码、统一格式与退出状态映射 |
| `internal/toolchain` | 锁文件与准备报告读取、`Runner` 接口、真实进程实现 |
| `tools/build` | 用隔离 Go 构建 `bin/tynel`，注入提交号 |
| `tools/test` | 测试总入口，统计实际跑过用例的包数 |
| `docs/reference/cli.md` | 命令参考、退出状态、CLI 与外部工具职责 |

## 正向验收

### 构建与版本

```text
$ ./tools/build
已生成 /Users/mahoo/Projects/tynel/bin/tynel（commit 5206108）

$ ./bin/tynel version
tynel dev (commit 5206108)
  目标平台    darwin/arm64
  工具链      go1.27.3（提交 2aae1000ea1d，BSD-3-Clause）
  锁文件      tools/toolchain/go.lock.json
  准备状态    已准备（go1.27.3-sdk-darwin-arm64）
```

版本里的 Go 版本、源码提交和许可证来自锁文件，准备状态来自准备报告，程序没有内置第二份常量。

### 帮助

`./bin/tynel help` 输出命令表和退出状态表，退出 0；`help toolchain` 输出该命令的详细说明，退出 0。

### 工具链检查

```text
$ ./bin/tynel toolchain
锁定工具链 go1.27.3（提交 2aae1000ea1d，BSD-3-Clause）
目标平台     darwin/arm64
SDK 目录     …/.work/toolchain/go1.27.3-sdk-darwin-arm64
  go       …/bin/go
  compile  …/pkg/tool/darwin_arm64/compile
  link     …/pkg/tool/darwin_arm64/link
  asm      …/pkg/tool/darwin_arm64/asm
源码目录     …/.work/toolchain/go1.27.3-src

$ ./bin/tynel toolchain which go
/Users/mahoo/Projects/tynel/.work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go
```

### 测试入口

```text
$ ./tools/test
使用 go version go1.27.3 darwin/arm64
ok  	github.com/Mahoo12138/tynel/cmd/tynel	0.416s
ok  	github.com/Mahoo12138/tynel/compiler/diagnostic	1.205s
ok  	github.com/Mahoo12138/tynel/internal/toolchain	0.876s
?   	github.com/Mahoo12138/tynel/tools/toolchain	[no test files]
测试通过：3 个包
```

测试入口用 `-count=1` 禁用缓存，只统计报告 `ok` 的包；没有任何包报告通过时按失败处理，避免「零断言也算过」。本次共 30 个测试函数通过，`go vet ./...` 无输出。

## 负向验收

命令行实际退出状态：

| 场景 | 命令 | 输出要点 | 退出状态 |
| --- | --- | --- | --- |
| 无参数 | `tynel` | 用法写到 stderr | 2 |
| 未知命令 | `tynel build` | `用法错误 (cli.unknown-command)：未知命令 "build"` | 2 |
| 缺参数 | `tynel toolchain which` | `用法错误 (cli.missing-argument)` | 2 |
| 未知工具名 | `tynel toolchain which nope` | `用法错误 (cli.unknown-tool)` | 2 |
| 工具不在磁盘 | 临时项目，报告指向不存在的路径 | `环境缺失 (tool.missing)：准备报告里的工具不可用：go、compile、link、asm` | 4 |
| 未准备工具链 | 临时项目，删除准备报告 | `环境缺失 (toolchain.not-prepared)` | 4 |
| 项目外运行 | 在 `/tmp` 执行 `tynel toolchain` | 找不到锁文件，按环境缺失 | 4 |

同一份二进制在 `/tmp` 执行 `tynel version` 仍然退出 0，把目标平台标为未知，不猜造身份。

### 内部进程失败不冒充源码错误

外部进程以非零状态退出时，诊断类别必须是内部故障，并保留该进程的 stderr。对应测试：

```text
=== RUN   TestExecRunKeepsNonZeroExitAsData
--- PASS: TestExecRunKeepsNonZeroExitAsData (0.01s)
=== RUN   TestFailureIsInternalNotSource
--- PASS: TestFailureIsInternalNotSource (0.00s)
```

第一个用例让 `/bin/sh` 输出 `boom` 并以 7 退出，断言退出状态作为数据返回而不是调用失败；第二个断言该结果映射为内部故障（退出 5）而不是源码错误（退出 1），且原因保留 stderr。

## 未覆盖

- `build`、`check`、`run`、`dump facts`、`dump nir`、`build-ir` 都未实现，调用 `build` 得到的是「未知命令」。
- 没有任何 TypeScript 前端、NIR、Go bridge 或代码生成，本记录不支持「编译器能编译程序」的说法。
- `Runner.Run` 目前只被测试使用；真正的工具编排在 01.06 接入。
- 发行版身份（把锁文件身份固化进二进制）留给 06.x，当前版本在运行时读取仓库内的锁文件与准备报告。
