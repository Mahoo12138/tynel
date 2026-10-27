# 03.04 验收记录：提前返回与完整返回路径

本文件记录 03.04 的实际运行结果。被测代码是包含本文件的提交，其正文带 `Task: 03.04`；用 `git log --grep 'Task: 03.04'` 可以定位。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核（4 性能核 + 6 能效核），24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 架构 | arm64 |
| 工具链 | 锁定的 go1.27.3（`.work/toolchain`），前端为 typescript/v7.0.2 |
| 修改版编译器 | `.work/bridge/compile`，sha256 前缀 `942e1691` |
| 对照运行时 | Node v22.22.2，`node --experimental-strip-types <file>` |

## 正向验收

### 两臂都返回

```text
$ ./bin/tynel run tests/source/testdata/returns-both.ts
-1
1
```

`sign` 的函数体只有一条 `if`，两臂都 `return`，末尾没有再补 `return`。

### 单臂提前返回后补 return

```text
$ ./bin/tynel run tests/source/testdata/returns-early.ts
1
2
```

`pick(true)` 在 `if` 里提前返回 `1`；`pick(false)` 走到后面的 `return 2`。

### 嵌套分支

```text
$ ./bin/tynel run tests/source/testdata/returns-nested.ts
0
1
2
```

两层 `if` 都覆盖两臂，外层 else 也返回。

### void 正常结束

```text
$ ./bin/tynel run tests/source/testdata/returns-void.ts
1
```

`show(false)` 不进入 `if`，直接走到函数末尾；降低补一条空 `return`，不产生输出。

### 不可达语句

```text
$ ./bin/tynel run tests/source/testdata/returns-unreachable.ts
1
```

`return 1` 后面的 `console.log(2)` 保留在 NIR 里，bridge 在生成时停住，运行时不会打印 `2`。

### 产物与回放一致

```text
$ ./bin/tynel build tests/source/testdata/returns-both.ts -o out/returns
已生成 out/returns（2429906 字节）
  源码     tests/source/testdata/returns-both.ts（903a5d2f0fe2）
  输入     0793e05f9bc2
  编译器   942e1691f4cb

$ ./bin/tynel dump nir tests/source/testdata/returns-both.ts > out/returns.nir.json
$ ./bin/tynel build-ir out/returns.nir.json -o out/returns-replay
已生成 out/returns-replay（2429906 字节）
  输入     0793e05f9bc2
  编译器   942e1691f4cb

$ shasum -a 256 out/returns out/returns-replay
5dfe41fdf3983d05a8daa244128343a8edf0a44bc0129579961da418c46f992f  out/returns
5dfe41fdf3983d05a8daa244128343a8edf0a44bc0129579961da418c46f992f  out/returns-replay
```

### NIR 形状

```json
{ "name": "sign", "params": [{"name": "n", "type": "number"}],
  "result": "number",
  "body": [
    { "op": "if",
      "cond": {"op": "compare", "operator": "<",
               "left": {"op": "param", "name": "n"},
               "right": {"op": "number", "bits": "0x0000000000000000"}},
      "then": [{"op": "return", "value": {"op": "number", "bits": "0xbff0000000000000"}}],
      "else": [{"op": "return", "value": {"op": "number", "bits": "0x3ff0000000000000"}}] }
  ] }
```

函数体没有末尾的 `return`：`if` 两臂都结束，已经覆盖所有路径。

### bridge 直接构造返回路径

```text
$ ./bridge/go/check
PASS 加法输出 42
PASS 空入口可执行且无输出
PASS boolean 输出 true
PASS if 分支按条件输出
PASS 完整返回路径与不可达语句
PASS 坏 NIR 在编译期被拒绝
PASS 普通构建不受影响
PASS 缺少支持函数被拒绝
```

`bridge/go/testdata/returns.nir.json` 里 `sign` 两臂都返回，`early` 在 `return` 后还有一条打印；两次调用输出 `1`、`2`，`early()` 只输出 `1`。

## 负向验收

| 输入 | 结果 | 退出 |
| --- | --- | --- |
| 缺返回路径 | TS2366 | 1 |
| 嵌套分支缺返回路径 | TS2366 | 1 |
| 返回值类型不符 | TS2322 | 1 |
| void 函数带返回值 | TS2322 | 1 |
| 不可达代码里的取模 | `cap.bad-operator` | 3 |
| NIR 缺返回路径 | `nir.missing-return`，位置指向 if | 1 |
| NIR 返回类型不符 | `nir.result-type` | 1 |
| NIR void 函数带返回值 | `nir.result-type` | 1 |

具体输出：

```text
$ ./bin/tynel check tests/source/testdata/errors/missing-return.ts
…/errors/missing-return.ts:2:31: error TS2366: Function lacks ending return statement and return type does not include 'undefined'.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/missing-return-nested.ts
…/errors/missing-return-nested.ts:2:40: error TS2366: Function lacks ending return statement and return type does not include 'undefined'.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/return-type.ts
…/errors/return-type.ts:3:3: error TS2322: Type 'boolean' is not assignable to type 'number'.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/void-return.ts
…/errors/void-return.ts:3:3: error TS2322: Type 'number' is not assignable to type 'void'.
$ echo $?
1

$ ./bin/tynel check tests/source/testdata/errors/unreachable-unsupported.ts
…/errors/unreachable-unsupported.ts:4:15: 能力限制 cap.bad-operator: 运算符 "%" 不在本阶段支持的 + - * / 与比较之内
$ echo $?
3
```

NIR 层的三条：

```text
$ ./bin/tynel build-ir /tmp/missing-return.nir.json -o out/missing-return
tynel: 源码错误 (nir.missing-return): 函数 "pick" 返回 number，但有路径没有 return
  位置: /tmp/missing-return.ts:2:3
$ echo $?
1

$ ./bin/tynel build-ir /tmp/return-type.nir.json -o out/return-type
tynel: 源码错误 (nir.result-type): 函数 "f" 声明返回 number，实际返回 bool
$ echo $?
1

$ ./bin/tynel build-ir /tmp/void-return.nir.json -o out/void-return
tynel: 源码错误 (nir.result-type): void 函数 "main" 的 return 不能带返回值
$ echo $?
1
```

失败的构建没有创建对应产物。

## 不可达语句的规则

- 能力检查不跳过：`return 1; console.log(1 % 2);` 仍报 `cap.bad-operator`。
- NIR 保留：`dump nir` 里能看到 `return` 后面的 `print`，验证器照样检查类型和作用域。
- bridge 丢弃：一个块遇到结束语句后停止生成，Go 编译器看不到不可达代码。
- 不用 Go 零值填补缺口：缺路径在 TypeScript、降低和 NIR 验证三处分别被拒绝。

## 改动范围

- `compiler/nir`：新增 `returns.go`，`BlockReturns` / `FirstMissingReturn` 做结构化路径分析；验证器用它们替换「最后一条语句必须是 return」。
- `compiler/lower`：降低后检查完整路径，缺口位置来自 NIR 的 `if`；void 函数在路径已覆盖时不再补 `return`。
- `bridge/go`：`stmts` 在结束语句后停止生成，避免 Go 的 unreachable code。
- `tests/source`：新增 5 个返回路径程序与 5 个错误用例，用例总数 35 → 45。
- `docs/reference/returns.md`：用 `pick(false)` 走到末尾的例子解释为什么「有 return」仍可能不完整。

## 累计测试

| 入口 | 结果 |
| --- | --- |
| `./tools/test` | 13 个包通过，根模块 273 个测试（158 个顶层 + 115 个子测试） |
| `tests/source` | 45 个用例（7 个测试函数，67 个子测试） |
| `tests/native` | 8 个用例 |
| `./bridge/go/check` | 8 项全过 |
| `./bridge/tsgo/check` | 57 项全过 |
| `go vet ./...` | 无输出 |

Node 对照全部通过，5 个返回路径程序都标记为 `shared`，输出逐字节相同。

## 阶段能力边界

- 有值函数支持两臂都返回、提前返回后补 `return`、嵌套分支和 void 正常结束。
- 缺路径、返回类型不符、void 带返回值都被拒绝；不使用 Go 零值。
- 不可达代码允许存在，但能力检查与 NIR 验证都照常进行，生成阶段丢弃。
- `while`、`for`、`&&`、`||` 与三元式仍未实现。

## 未覆盖

- 没有循环，因此没有「循环之后是否返回」的分析。
- 没有 `switch` 与 `try`。
- 不可达代码没有单独诊断；TypeScript 默认也允许它。
- 只在 `darwin/arm64` 上验证。
