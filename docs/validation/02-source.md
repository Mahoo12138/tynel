# 节点 02 累计验证记录

本记录对应子节点 02.09「完成真实源码实验与第二篇」，同时是节点 02 的阶段验收。被测代码是包含本文件的提交，其正文带 `Task: 02.09`；用 `git log --grep 'Task: 02.09'` 可以定位。

## 环境

| 项目 | 实际值 |
| --- | --- |
| 机型 | Mac mini，Mac16,10，Apple M4，10 核，24 GB |
| 系统 | macOS 26.6.2，arm64 |
| Go SDK | `.work/toolchain/go1.27.3-sdk-darwin-arm64`，go1.27.3 |
| 前端 | typescript/v7.0.2（提交 `2bd066d8`） |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `1ac6e266` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |
| 执行时间 | 2026-10-27 09:33–09:45（CST） |

## 从代码准备到产物

```text
$ ./tools/toolchain/prep --quiet
/Users/mahoo/Projects/tynel/.work/toolchain/toolchain.json

$ ./tools/build
已生成 …/bin/tynel（commit bd7029f）

$ ./bin/tynel version
tynel dev (commit bd7029f)
  目标平台    darwin/arm64
  工具链      go1.27.3（提交 2aae1000ea1d，BSD-3-Clause）
  锁文件      tools/toolchain/go.lock.json
  准备状态    已准备（go1.27.3-sdk-darwin-arm64）
```

## 正向验收

### 一条命令从 TypeScript 得到 42

```text
$ ./bin/tynel build examples/add.ts -o out/add
已生成 out/add（2429986 字节）
  源码     examples/add.ts（12269d671d8c）
  输入     4b599cc831a0
  编译器   1ac6e26642e2
  构建记录 out/add.build.json

$ ./out/add
42
```

构建记录里的三项哈希与产物对得上：

```text
source.sha256  12269d671d8c1358…
input.sha256   4b599cc831a03c34…
output.sha256  1217c68216f64e5b…
```

### 改输入得到 43

```text
$ cp examples/add.ts out/add-41.ts
$ perl -pi -e 's/add\(40, 2\)/add(41, 2)/' out/add-41.ts
$ ./bin/tynel build out/add-41.ts -o out/add-41
已生成 out/add-41（2429986 字节）
  源码     out/add-41.ts（59ef982cfc76）
  输入     e8528bf6fd1f
  编译器   1ac6e26642e2
$ ./out/add-41
43
```

### run 编译并运行本次产物

```text
$ ./bin/tynel run examples/add.ts
42
```

### 存下 NIR 回放，产物逐字节相同

```text
$ ./bin/tynel dump nir examples/add.ts > out/add.nir.json
$ ./bin/tynel build-ir out/add.nir.json -o out/add-replay
已生成 out/add-replay（2429986 字节）
  输入     4b599cc831a0
  编译器   1ac6e26642e2
$ ./out/add-replay
42
$ shasum -a 256 out/add out/add-replay
1217c68216f64e5bc771538f457fd548da969dc23d31df1c63bada45c695f542  out/add
1217c68216f64e5bc771538f457fd548da969dc23d31df1c63bada45c695f542  out/add-replay
```

回放用的输入哈希 `4b599cc8…` 与 `build` 记录里的 `input.sha256` 相同，说明两次编译的是同一份 NIR。手写 NIR 不再是使用前提，但后端的固定入口仍然可用。

### check 的机器可读结果

```text
$ ./bin/tynel check examples/add.ts --json
{
  "schemaVersion": 1,
  "file": "/…/examples/add.ts",
  "sourceSha256": "12269d671d8c13584dfb4f93f95871c15c4a36eff17e049f27543027cc94a7c6",
  "sourceBytes": 138,
  "diagnostics": [],
  "capability": [],
  "summary": {
    "functions": 2,
    "verified": true
  }
}
```

`verified` 为真表示 NIR 通过了独立验证器，不只是「生成过」。

## 负向验收

| 输入 | 结果 | 退出 |
| --- | --- | --- |
| `errors/type.ts` | `2:7: error TS2322` | 1 |
| `errors/modulo.ts` | `2:13: 能力限制 cap.bad-operator` | 3 |
| `errors/control.ts` | `3:3: 能力限制 cap.unsupported-syntax` | 3 |
| `errors/any.ts` | `1:1` 与 `2:19: 能力限制 cap.any` | 3 |
| `errors/missing.ts` | `环境缺失 (source.io)` | 4 |

### 失败不覆盖旧产物

```text
$ ./bin/tynel build examples/add.ts -o out/keep
（成功，记录里 output 1217c68216f6、input 4b599cc831a0）

$ ./bin/tynel build-ir bridge/go/testdata/bad-call.nir.json -o out/keep
tynel: 源码错误 (nir.unknown-function): 调用了不存在的函数 "missing"
$ echo $?
1

$ ./out/keep
42
$ 记录里的 output 1217c68216f6、input 4b599cc831a0
```

产物和构建记录都没有变化。旧产物还在，但记录里的哈希没变，说明它仍属于上一次输入，不能当作本次结果。

## 本节点修正：源码位置指到记号，而不是前导空白

02.09 用使用者视角复查源码位置时发现：能力诊断指向上一行末尾。`errors/control.ts` 的 `if` 在第 3 行第 3 列，诊断却报 `2:26`——函数签名末尾 `{` 之后的位置。原因是适配层直接取了上游的 `node.Pos()`，它返回「完整起点」，包含节点前面的空白和注释；函数声明的范围也因此把头顶的注释算进去。

修正方式是用 `scanner.GetTokenPosOfNode` 取第一个有效记号的位置，事实里所有 `start` 与 `length` 都改从这里算。修正前后：

| 观察 | 修正前 | 修正后 |
| --- | --- | --- |
| `control.ts` 的 `if` 诊断 | `2:26` | `3:3` |
| `facts.ts` 的 `function scale` | `start 175`、`length 127`（含注释） | `start 249`、`length 53` |
| `examples/add.ts` 的 `print` 位置 | `4:2`（`}` 后的换行） | `6:1` |

NIR 的位置字段跟着变化，`tests/source` 的黄金输出只比较 stdout，不受影响；`docs/reference/facts.md` 里的示例偏移已按新结果更新。`./tools/toolchain/prep --frontend-only` 会把适配包重新复制进前端源码树，宿主在下次运行时按源码哈希自动重建。

## 累计测试

```text
$ ./tools/test
使用 go version go1.27.3 darwin/arm64
ok  	github.com/Mahoo12138/tynel/cmd/tynel
ok  	github.com/Mahoo12138/tynel/compiler/capability
ok  	github.com/Mahoo12138/tynel/compiler/diagnostic
ok  	github.com/Mahoo12138/tynel/compiler/frontend
ok  	github.com/Mahoo12138/tynel/compiler/lower
ok  	github.com/Mahoo12138/tynel/compiler/nir
ok  	github.com/Mahoo12138/tynel/compiler/pipeline
ok  	github.com/Mahoo12138/tynel/compiler/source
ok  	github.com/Mahoo12138/tynel/internal/build
ok  	github.com/Mahoo12138/tynel/internal/host
ok  	github.com/Mahoo12138/tynel/internal/toolchain
ok  	github.com/Mahoo12138/tynel/tests/native
ok  	github.com/Mahoo12138/tynel/tests/source
测试通过：13 个包
```

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 203 个测试（140 个顶层 + 63 个子测试） |
| `tests/source` | 29 个用例（6 个测试函数，23 个子测试） |
| `tests/native` | 8 个用例 |
| `./bridge/tsgo/check` | 47 项全过 |
| `go vet ./...` | 无输出 |

## 阶段能力边界

节点 02 交付的是「真实源码到原生程序」的完整链路，语言子集仍然很小：

- 类型只有 `number` 和 `void`；`boolean`、`string`、对象和数组都没有。
- 语句只有 `return`、表达式语句和语句块；没有 `if`、`while`、局部 `let` 赋值。
- 运算只有 `+ - * /` 与一元 `+ -`；比较、逻辑运算、三元式都不在子集内。
- 一个文件就是一个程序，`import` 与模块解析不支持。
- 输出只有单参数 `console.log(<number>)`，无穷写作 `+Inf` / `-Inf`，与 Node 的 `Infinity` 不同。
- 修改版编译器只存在于 `.work/bridge/compile`，发行打包在 06.x。

## 未覆盖

- 只在 `darwin/arm64` 上验证，Windows 目标仍是 `deferredTargets`。
- 产物字节级可重复性只在同一台机器、同一份工具链上比较过；跨机器重建没有断言。
- 诊断的结束位置（`EndLine`/`EndColumn`）没有在文章里展示，也没有专门的跨行断言。
- 模块级表达式里的调用没有进 `calls` 事实，降低按语法骨架里的 `target` 解析；这一点已在事实参考里写明范围。
