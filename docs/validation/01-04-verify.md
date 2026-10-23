# 01.04 NIR 验证器验证记录

本记录对应子节点 01.04「独立验证函数、引用和类型」。被测代码是包含本文件的提交，其正文带 `Task: 01.04`；用 `git log --grep 'Task: 01.04'` 可以定位。

## 环境

| 项目 | 实际值 |
| --- | --- |
| 机型 | Mac mini，Mac16,10，Apple M4，24 GB |
| 系统 | macOS 26.6.2，arm64 |
| 测试用的 Go | `.work/toolchain/go1.27.3-sdk-darwin-arm64/bin/go`，go1.27.3 darwin/arm64 |
| 执行时间 | 2026-10-23 11:19–11:45（CST） |

## 交付物

| 路径 | 内容 |
| --- | --- |
| `compiler/nir/verify.go` | `Verify`、`VerifyWithLimits`、函数表、表达式类型推导、带位置的诊断 |
| `compiler/nir/verify_test.go` | 9 个测试：有效输入、16 条语义错误、位置、循环、共享节点、内存超深 |
| `compiler/nir/codec.go` | 表达式遍历增加重复引用检测（`nir.cycle`），保证验证器不会被循环结构卡住 |

验证器只依赖 `compiler/nir` 与 `compiler/diagnostic`，不导入 tsgo、Go 内部包或 `internal/toolchain`。

## 正向验收

```text
=== RUN   TestVerifyExampleModule
--- PASS: TestVerifyExampleModule (0.00s)
=== RUN   TestVerifyNestedCall
--- PASS: TestVerifyNestedCall (0.00s)
=== RUN   TestVerifyEmptyVoidEntry
--- PASS: TestVerifyEmptyVoidEntry (0.00s)
```

- `examples/ir/add.nir.json` 通过验证：`add` 两个 `number` 参数返回 `number`，`main` 调用它并打印。
- 嵌套调用 `double(double(1))` 通过：内层调用类型为 `number`，满足外层实参要求。
- 空 `void` 入口通过，`void` 函数里的裸 `return` 也通过。

### 位置可追踪

```text
=== RUN   TestVerifyErrorCarriesPosition
--- PASS: TestVerifyErrorCarriesPosition (0.00s)
```

用例把 `param("missing")` 的位置设为 `examples/add.ts:7:9`，断言返回的诊断类别为源码错误，且 `File`、`Line`、`Column` 与输入节点一致。位置来自 NIR 节点自己的 `pos`，不是重新推算的。

## 负向验收

全部 16 条语义错误都按表驱动执行，断言返回错误且错误码匹配：

| 输入 | 错误码 |
| --- | --- |
| `entry` 指向不存在的函数 | `nir.entry-missing` |
| 入口带参数 | `nir.entry-signature` |
| 入口返回 `number` | `nir.entry-signature` |
| 两个函数同名 | `nir.duplicate-name` |
| 同一函数两个参数同名 | `nir.duplicate-name` |
| 引用不存在的参数 | `nir.unknown-param` |
| 调用不存在的函数 | `nir.unknown-function` |
| `add` 只给一个实参 | `nir.arg-count` |
| 把 `void` 调用当实参 | `nir.arg-type` |
| `number` 函数返回 `void` 调用 | `nir.result-type` |
| `void` 函数 `return` 带值 | `nir.result-type` |
| `number` 函数只 `print` 不 `return` | `nir.missing-return` |
| `number` 函数空函数体 | `nir.missing-return` |
| `print` 一个 `void` 调用 | `nir.print-type` |
| 运算符的操作数是 `void` 调用 | `nir.operand-type` |
| 内存里构造未知 `op` | `nir.bad-op` |

最后一条特意绕过 JSON：模块直接在内存里构造，没有经过解码器，仍然被拒绝。这说明验证器不依赖解码器兜底。

### 循环与共享节点

```text
=== RUN   TestVerifyRejectsCycleWithoutHanging
--- PASS: TestVerifyRejectsCycleWithoutHanging (0.00s)
=== RUN   TestVerifyRejectsSharedNode
--- PASS: TestVerifyRejectsSharedNode (0.00s)
```

循环用例把 `binary` 的 `Left` 和 `Right` 都指回自身，在一个 goroutine 里调用 `Verify`，2 秒内必须返回；超时即判定遍历不终止。共享用例让 `Left` 与 `Right` 指向同一个节点，同样以 `nir.cycle` 拒绝。

拒绝共享节点是有意的：表达式按树处理，写回 JSON 时同一节点会被复制成两份，之后两边的修改互不影响，容易产生「看起来改了一处」的错觉。

### 内存里超预算

```text
=== RUN   TestVerifyRejectsOverDepthInMemory
--- PASS: TestVerifyRejectsOverDepthInMemory (0.00s)
```

70 层嵌套在内存中构造，验证器按默认预算 64 层拒绝。预算不只写在解码器里。

## 测试汇总

```text
$ ./tools/test
使用 go version go1.27.3 darwin/arm64
ok  	github.com/Mahoo12138/tynel/cmd/tynel	…
ok  	github.com/Mahoo12138/tynel/compiler/diagnostic	…
ok  	github.com/Mahoo12138/tynel/compiler/nir	…
ok  	github.com/Mahoo12138/tynel/internal/toolchain	…
?   	github.com/Mahoo12138/tynel/tools/toolchain	[no test files]
测试通过：4 个包
```

全部 50 个测试函数通过，其中 `compiler/nir` 20 个（01.03 的 11 个加本次 9 个）；`go vet ./...` 无输出。

## 未覆盖

- 没有分支和循环，因此「所有值返回路径」只检查函数体最后一条语句。真正的多路径返回检查在 03.04。
- 类型只有 `number` 和 `void`；没有布尔、字符串、对象，也没有类型别名。
- 没有可达性分析：函数可以定义但从不被调用，不报错。
- 没有命令行入口，验证器目前只由测试调用；`tynel check` 与 `build-ir` 分别在后续节点接入。
- 验证器不证明程序终止，也不检查运行时行为，这些不属于它的职责。
