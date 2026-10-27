# 03.03 验收记录：if 分支与词法作用域

本文件记录 03.03 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 03.03`；用 `git log --grep 'Task: 03.03'` 可以定位。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `22be7a0f` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 正向验收

### 两条路径与同名局部值

```text
$ ./bin/tynel run tests/source/testdata/branch.ts
1
1
2
2
```

`pick(true)` 走 then：`mark(1)` 打印 `1`，`x` 绑定 `1`，再打印 `1`。`pick(false)` 走 else：打印 `2` 两次。两边各有一个 `x`，是两个独立声明；未选中的分支不调用 `mark`。

### else if 链

```text
$ ./bin/tynel run tests/source/testdata/branch-chain.ts
0
1
2
```

`label(-1)` 走第一个分支，`label(0)` 走 else if，`label(5)` 走最后的 else。

### 嵌套分支

```text
$ ./bin/tynel run tests/source/testdata/branch-nested.ts
1
2
3
```

外层 `a` 为真时进入内层，内层再由 `b` 选择；`a` 为假时直接走外层 else。

### 单条语句的分支

```text
$ ./bin/tynel run tests/source/testdata/branch-single.ts
0
1
2
```

`if (n < 0) console.log(0);` 没有花括号，降低时包成只有一个元素的语句块；`else if` 与 `else` 同理。

### 产物与回放一致

```text
$ ./bin/tynel build tests/source/testdata/branch.ts -o out/branch
已生成 out/branch（2429970 字节）
  源码     tests/source/testdata/branch.ts（8a7a658cb4b2）
  输入     434fbd029020
  编译器   22be7a0f4fbb

$ ./bin/tynel dump nir tests/source/testdata/branch.ts > out/branch.nir.json
$ ./bin/tynel build-ir out/branch.nir.json -o out/branch-replay
已生成 out/branch-replay（2429970 字节）
  输入     434fbd029020
  编译器   22be7a0f4fbb

$ shasum -a 256 out/branch out/branch-replay
9e775754a43e5d505135e483a822d5115570c163d4d686ee28cf96a9eb5fdeb7  out/branch
9e775754a43e5d505135e483a822d5115570c163d4d686ee28cf96a9eb5fdeb7  out/branch-replay
```

### NIR 形状

```json
{ "op": "if",
  "cond": {"op": "param", "name": "flag"},
  "then": [
    {"op": "let", "name": "x", "value": {"op": "call", "function": "mark",
      "args": [{"op": "number", "bits": "0x3ff0000000000000"}]}},
    {"op": "print", "value": {"op": "local", "name": "x"}}
  ],
  "else": [
    {"op": "let", "name": "x", "value": {"op": "call", "function": "mark",
      "args": [{"op": "number", "bits": "0x4000000000000000"}]}},
    {"op": "print", "value": {"op": "local", "name": "x"}}
  ] }
```

两条分支都完整保留，条件只出现一次。分支里的 `const` 是 NIR 的 `let`，两个 `x` 在不同的语句块里。

### bridge 直接构造 if

```text
$ ./bridge/go/check
PASS 加法输出 42
PASS 空入口可执行且无输出
PASS boolean 输出 true
PASS if 分支按条件输出
PASS 坏 NIR 在编译期被拒绝
PASS 普通构建不受影响
PASS 缺少支持函数被拒绝
```

`bridge/go/testdata/branch.nir.json` 是手写的分支 NIR：`pick(true)` 与 `pick(false)` 分别输出 `1` 和 `2`。

## 负向验收

| 输入 | 结果 | 退出 |
| --- | --- | --- |
| `if (1)` | `cap.bad-type`：条件不是 boolean | 3 |
| 模块级 `if` | `cap.unsupported-syntax` | 3 |
| 块外读取分支局部值 | TS2304 | 1 |
| else 分支读取 then 的局部值 | TS2304 | 1 |
| `if (false)` 里写 `1 % 2` | `cap.bad-operator` | 3 |
| 分支里写 `let x` | `cap.unsupported-syntax` | 3 |
| 同一块重复 `const x` | TS2451 | 1 |
| NIR 的 if 条件为 number | `nir.condition-type` | 1 |
| NIR 在块外引用分支局部值 | `nir.unknown-local` | 1 |
| NIR 同一块重复 let | `nir.duplicate-name` | 1 |

具体输出：

```text
$ ./bin/tynel check tests/source/testdata/errors/number-condition.ts
…/errors/number-condition.ts:3:7: 能力限制 cap.bad-type: if 的条件是 number，需要 boolean
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/branch-scope.ts
…/errors/branch-scope.ts:5:13: error TS2304: Cannot find name 'x'.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/cross-branch.ts
…/errors/cross-branch.ts:5:15: error TS2304: Cannot find name 'x'.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/unselected-unsupported.ts
…/errors/unselected-unsupported.ts:4:17: 能力限制 cap.bad-operator: 运算符 "%" 不在本阶段支持的 + - * / 与比较之内
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/local-let.ts
…/errors/local-let.ts:3:7: 能力限制 cap.unsupported-syntax: 局部 let/var 不在本阶段子集内，只支持 const
…/errors/local-let.ts:4:15: 能力限制 cap.unknown-name: 名称 "x" 不是本文件里的参数或常量
$ echo $?
3

$ ./bin/tynel check tests/source/testdata/errors/duplicate-local.ts
…/errors/duplicate-local.ts:3:9: error TS2451: Cannot redeclare block-scoped variable 'x'.
…/errors/duplicate-local.ts:4:9: error TS2451: Cannot redeclare block-scoped variable 'x'.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/module-if.ts
…/errors/module-if.ts:2:1: 能力限制 cap.unsupported-syntax: 模块级 if 不在本阶段子集内
$ echo $?
3
```

NIR 层的三条：

```text
$ ./bin/tynel build-ir /tmp/if-number.nir.json -o out/if-number
tynel: 源码错误 (nir.condition-type): if 的条件必须是 bool，实际为 number
$ echo $?
1

$ ./bin/tynel build-ir /tmp/if-scope.nir.json -o out/if-scope
tynel: 源码错误 (nir.unknown-local): 引用了尚未定义的局部值 "x"
$ echo $?
1

$ ./bin/tynel build-ir /tmp/if-duplicate.nir.json -o out/if-duplicate
tynel: 源码错误 (nir.duplicate-name): 函数 "main" 里 "x" 重复定义
$ echo $?
1
```

失败的构建没有创建对应产物。

## 编译期检查与运行时执行

`if` 在编译期只被遍历，不被裁剪。能力检查和降低都会进入两条分支：`if (false) { 1 % 2 }` 照样报 `cap.bad-operator`，NIR 里 then 与 else 都完整保存。运行时的选择由 Go 的 `if` 完成，未选中的分支不求值、不打印。

双路径图与 `mark` 的对应关系见[分支的两条路径](../reference/branches.md)。

## 改动范围

- `compiler/frontend`：新增 `local` 声明种类，局部 `const` 的声明身份进入事实。
- `bridge/tsgo`：函数体内的 `const` 先登记再转骨架；`VariableDeclaration` 只保留初始化表达式；`IfStatement` 的条件与两条分支进入骨架。
- `compiler/nir`：新增 `if` 语句、语句块递归检查、块作用域、`if` 条件类型与语句树去重。
- `compiler/capability`：放行 `if` / `else` / 分支 `const`；条件必须是 boolean；局部 `let`、独立语句块与未选分支里的越界写法继续拒绝。
- `compiler/lower`：`if` 降成 NIR `if`，分支各开子作用域，局部 `const` 降成 `let`。
- `bridge/go`：`if` 转 `ir.NewIfStmt`；局部变量按块作用域分配，同名时改 Go 符号名。
- `tests/source`：新增 4 个分支程序与 7 个错误用例，用例总数 24 → 35。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 255 个测试（155 个顶层 + 100 个子测试） |
| `tests/source` | 35 个用例（7 个测试函数，52 个子测试） |
| `tests/native` | 8 个用例 |
| `./bridge/go/check` | 7 项全过 |
| `./bridge/tsgo/check` | 52 项全过 |
| `go vet ./...` | 无输出 |

Node 对照全部通过，`branch`、`branch-chain`、`branch-nested`、`branch-single` 标记为 `shared`，输出逐字节相同。

## 阶段能力边界

- 支持 `if`、`else`、`else if`、嵌套分支和分支内 `const`。
- 条件必须是 `boolean`；`if (1)` 按能力限制拒绝。
- 局部 `let` 还没有赋值语义，按能力限制拒绝；独立语句块也不支持。
- 有值函数仍要求函数体最后一条语句是 `return`；两臂都返回的完整路径分析在 03.04。
- `while`、`for`、`&&`、`||` 与三元式仍未实现。

## 未覆盖

- 没有提前返回与完整返回路径分析；`if` 两臂都 `return` 的函数仍需要在末尾补一条 `return`。
- 没有 `let` 与赋值语句。
- 没有独立语句块；块必须挂在 `if` 上。
- 只在 `darwin/arm64` 上验证。
