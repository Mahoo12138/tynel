# 源码快照与位置

本页说明编译器怎样把一份 `.ts` 文件固定下来，以及诊断里的 `2:7` 和 `109` 分别是怎么算出来的。

## 一处错误的位置从哪来

`bridge/tsgo/testdata/bad.ts` 只有两行，第一行是中文注释，第二行把字符串赋给 `number`：

```typescript
// 一个类型错误：数字变量被赋了字符串。用来确认诊断带着错误码和位置。
const answer: number = "forty-two";
```

前端报的是 `start: 109, length: 6`，映射之后显示 `2:7`。三个数字来自同一份东西——我们交给前端的那串字节：

- `109` 是 `answer` 在文件里的**字节**偏移；
- `2:7` 是它的**行**和**列**；
- `6` 是它的字节长度。

如果把第一行删掉，`start` 会变成 6，而列仍然是 7。列没变是因为第一行只有一行注释，不影响第二行的第 7 个字符；`start` 变了 103，是因为那行中文占 103 个字节（36 个字符加换行）。

这也说明了为什么必须有快照：`109` 和 `2:7` 是分别算出来的，如果算 `2:7` 时重新读了一次磁盘，而文件在两次读取之间被改过，两个数字就会指向不同的内容。快照把「哪份源码」这个问题一次回答完。

## 快照是什么

`source.Read` 是唯一读磁盘的地方。它读一次，复制一份字节，之后所有查询都在这份副本上做：

```go
snapshot, err := source.Read("bad.ts", source.DefaultLimits())
pos, err := snapshot.Position(109)   // 2:7
```

几条性质：

- **不可变**。`New` 会复制调用方的切片，`Bytes` 返回的也是这份副本。调用方之后改动自己的数据，快照和已经算出的位置都不受影响。
- **BOM 会被去掉**。带 BOM 的文件在复制时去掉那三个字节，因为前端拿到的是去掉 BOM 的内容，位置必须与它一致；`HadBOM` 记录原文件有没有。
- **行起点预先算好**。建立快照时扫一遍换行，记下每行的起始字节，之后的行列换算是对这个数组做二分，不重复扫描。
- **有身份**。每个快照有一个进程内唯一的 `FileID`。它只用来区分文件，不跨进程、不写进产物——需要稳定身份的地方用路径或内容哈希。

## 两种位置

快照同时提供两种位置，它们不能互相替代：

| 形式 | 单位 | 用途 |
| --- | --- | --- |
| `Range{Start, End}` | UTF-8 字节，左闭右开 | 切源码、和前端对齐、比较两次结果是否指向同一段 |
| `Position{Line, Column}` | 1 起始的行、按字符计数的列 | 给人看，写进诊断 |

从行列反推字节是不行的：同样的「第 7 列」在不同行对应的字节偏移不同，而字节才是前端与快照之间唯一可靠的坐标。所以诊断里两个都留着，行列只用于展示。

列按字符（Unicode 码点）计数，不是字节数，也不是 UTF-16 码元数。一个 emoji 占 4 个字节，但只让列前进 1。

## 换算会拒绝什么

`Position` 只在偏移落在 `[0, Size]` 且位于字符边界上时返回结果。越界、负数、或者切在多字节字符中间，都返回 `source.bad-offset`，类别是内部故障——偏移由前端或编译器自己产生，出错说明我们算错了，不是用户输入的问题，所以不能伪装成源码错误。

同理，`Slice` 不会返回半段字符，`LineText` 不会返回不存在的行。这些检查让上层可以放心地用「拿不到就报错」的写法，而不是在每个调用点自己防一遍。

CRLF 文件按 `\n` 分行，`LineText` 会去掉行尾的 `\r`；列计数时 `\r` 仍然算一个字符，因为它确实占了位置。

## 输入预算

| 预算 | 值 | 超限时的行为 |
| --- | --- | --- |
| 字节数 | 1 MiB | `source.too-large`，源码错误 |
| 行数 | 100,000 | `source.too-many-lines`，源码错误 |

超限直接拒绝，不截断。截断之后继续编译会得到指向错误位置的行列，比直接说「文件太大」难查得多。

## 诊断映射

前端返回的诊断是一条扁平记录，`MapDiagnostics` 把它绑到快照上：

```go
mapped, err := snapshot.MapDiagnostics([]source.FrontendDiagnostic{{
    File:     snapshot.Path(),
    Code:     2322,
    Category: "error",
    Message:  "Type 'string' is not assignable to type 'number'.",
    Start:    109,
    Length:   6,
}})
// mapped[0].String() == "bad.ts:2:7: Type 'string' is not assignable to type 'number'. (TS2322)"
```

`FrontendDiagnostic` 刻意不使用适配层的类型：`compiler/source` 属于 Tynel 自己的模块，不依赖上游源码树。宿主把适配层返回的字段填进来即可，这层转换由宿主承担。

两条边界规则：

- **指向别的文件的诊断不换算**。声明库里的诊断（比如 `Cannot find global type 'Array'`）保留原路径，行列留空。硬套本快照的位置会把话说错。
- **区间非法就整体失败**。落在快照内但越界或切在字符中间的区间返回内部故障，不输出「差不多对」的位置。

## 文件系统边界

`Read` 之外没有任何地方访问磁盘：适配层拿到的是 `snapshot.Bytes()`，它在内存文件系统里组装程序，不会再去读原文件。`TestSnapshotDoesNotFollowLaterDiskChanges` 和宿主的 `TestFileChangeAfterReadDoesNotAffectResult` 都验证这一点——读完之后把文件改坏，检查结果不变。

## 错误码

| 代码 | 类别 | 触发条件 |
| --- | --- | --- |
| `source.io` | 环境缺失 | 文件不存在、无权限 |
| `source.encoding` | 源码错误 | 存在非法 UTF-8 字节，消息指出第一个出错字节 |
| `source.too-large` | 源码错误 | 超过字节预算 |
| `source.too-many-lines` | 源码错误 | 超过行数预算 |
| `source.bad-offset` | 内部故障 | 偏移越界或落在多字节字符中间 |
| `source.bad-line` | 内部故障 | 行号不在 `1..LineCount` |
| `source.bad-severity` | 内部故障 | 前端返回了未知的诊断类别 |

源码错误退出 1，环境缺失退出 4，内部故障退出 5——分类与退出码的对应只在 `compiler/diagnostic` 一处定义。
