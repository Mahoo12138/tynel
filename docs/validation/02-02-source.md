# 02.02 源码快照与错误位置：验收记录

记录 02.02 的实际运行结果。被测代码是正文带 `Task: 02.02` 的提交；本文件不预填不存在的哈希。

## 环境

| 项目 | 值 |
| --- | --- |
| 机型 | Mac mini，机型标识 Mac16,10 |
| 芯片 | Apple M4，10 核，24 GB 内存 |
| 系统 | macOS 26.6.2，构建号 25G83 |
| 隔离工具链 | go1.27.3 darwin/arm64 |
| 前端源码 | `microsoft/typescript-go`，`typescript/v7.0.2`，提交 `2bd066d8` |

## 正向验收

### 位置换算

同一份源码 `// 中文注释\nconst answer: number = "forty-two";\n`：

| 输入 | 结果 |
| --- | --- |
| `Position(22)` | `2:7`（`answer` 的字节偏移 22，字符列 7） |
| `Position(0)` | `1:1` |
| `Position(Size)` | 文件末尾位置，不报错 |
| 连续 200 次 `Position(22)` | 结果完全相同 |

emoji 用例 `const s = "😀"; const x = 1;`：`x` 的字节偏移是 24，列是 22。emoji 占 4 个字节但只让列前进 1，说明列按字符计数。

### 端到端：中文前缀之后的错误

```text
$ cd bridge/tsgo/consumer && go run . ../testdata/bad.ts
[
  {
    "file": "/Users/mahoo/Projects/tynel/bridge/tsgo/testdata/bad.ts",
    "line": 2,
    "column": 7,
    "endLine": 2,
    "endColumn": 13,
    "start": 109,
    "length": 6,
    "severity": "error",
    "code": 2322,
    "message": "Type 'string' is not assignable to type 'number'."
  }
]
$ echo $?
1
```

`start` 是字节偏移，`line`/`column` 是换算结果。同一段代码去掉中文注释后 `start` 变成 6，列仍是 7。

### 快照不跟随磁盘

`TestSnapshotDoesNotFollowLaterDiskChanges`（`compiler/source`）与 `TestFileChangeAfterReadDoesNotAffectResult`（宿主）分别验证：读成快照之后改写文件，快照内容与检查结果都不变。宿主这一条走完整链路——快照、会话、映射——仍然得到空诊断。

### 映射形状

```text
mapped[0].String() == "bad.ts:2:7: Type 'string' is not assignable to type 'number'. (TS2322)"
```

字节区间原样保留在 `Location.Bytes`，行列在 `Location.Start/End`。声明库里的诊断只保留路径，行列留空。

## 负向验收

`./bridge/tsgo/check` 的 11 个用例全部通过：

```text
使用 go version go1.27.3 darwin/arm64
  PASS  前端源码身份与适配包副本一致
  PASS  外部消费者测试通过
  PASS  干净输入没有诊断（退出 0）
  PASS  类型错误以 2322 报告（退出 1）
  PASS  中文注释之后的位置仍是 2:7
  PASS  截断源码报语法错误（退出 1）
  PASS  缺文件按环境缺失退出 4
  PASS  外部导入 internal 包被拒绝
  PASS  源码身份不符按退出 8 拒绝
  PASS  声明库缺失按退出 8 拒绝
  PASS  声明库不完整按退出 8 拒绝
```

| 场景 | 做法 | 结果 |
| --- | --- | --- |
| 超限字节 | 17 字节输入、`MaxBytes: 16` | `source.too-large`，源码错误 |
| 超限行数 | 6 行输入、`MaxLines: 4` | `source.too-many-lines`，源码错误 |
| 坏编码 | 内容里放 `\xff\xfe` | `source.encoding`，消息指出字节 16 |
| 无效偏移 | `-1`、`Size+1`、多字节字符中间 | `source.bad-offset`，内部故障 |
| 无效行号 | `LineText(9)` | `source.bad-line`，内部故障 |
| 半段字符 | `Slice` 切在字符中间 | `source.bad-offset` |
| 截断源码 | `function add(a: number` | 语法诊断，退出 1，不崩溃 |
| 缺文件 | 路径不存在 | `source.io`，环境缺失，退出 4 |
| 未知诊断类别 | `Category: "fatal"` | `source.bad-severity` |
| 越界诊断区间 | `Start: Size+3` | `source.bad-offset` |

截断源码这一条不是快照层的判断——快照只保证字节合法，语法是否完整由前端说。它验证的是「截断的输入走到前端再映射回来，得到一条正常的语法错误，而不是崩溃或空诊断」。

## 用例统计

| 范围 | 用例数 |
| --- | --- |
| `compiler/source` | 21 个 `=== RUN`（18 个测试函数，含子用例） |
| 宿主 `bridge/tsgo/consumer` | 14 个 `=== RUN`（10 个测试函数，含子用例） |
| `./bridge/tsgo/check` | 11 项 |

`./tools/test` 覆盖 `compiler/source`，6 个包通过。

## 过程中发现的问题

1. **字节偏移的算术比想象中容易错**。emoji 用例最初把 `const x` 的偏移写成 15，实际是 18——漏算了字符串的收尾引号和分号。测试先失败才发现，说明这类断言值得手算一遍再写死。
2. **BOM 必须与前端一致**。带 BOM 的文件如果不去掉，前端看到的内容比快照少 3 个字节，所有位置都会偏。现在在建立快照时去掉并记录 `HadBOM`。
3. **外来诊断不能硬套位置**。声明库诊断没有本快照的位置，套上去会显示 `1:1`，比不显示更误导。改为保留路径、行列留空。

## 与计划的偏差

- **映射使用自己的 `FrontendDiagnostic`，不直接依赖适配层类型**。`compiler/source` 属于根模块，如果它导入 `tyneladapter`，根模块就必须带一条指向 `.work` 的 `replace`，那么没有准备前端源码的人连 `go test ./...` 都跑不起来。转换放在宿主里，代价是宿主多写一个几行的 `convert`。
- **没有把行列换算做进适配层**。适配层只给字节偏移，换算留在 Tynel 侧：上游的位置语义可能变，而「我们怎么展示位置」不该跟着上游走。
