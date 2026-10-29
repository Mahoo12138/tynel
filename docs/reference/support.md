# 操作支持表

这张表对应节点 03 完成时的编译器。`支持` 表示有源码夹具和验证记录；`拒绝` 表示能力检查或 TypeScript 会明确报错；`计划` 表示尚未实现，不能靠推测当成可用。

## 值

| 操作 | 状态 | 说明 |
| --- | --- | --- |
| `number` | 支持 | 双精度浮点，字面量在 NIR 里是 64 位位模式 |
| `boolean` | 支持 | `true` / `false`，输出拼写与 TypeScript 一致 |
| `void` | 支持 | 只用于函数结果和 `return` |
| `string` | 拒绝 | `cap.bad-type` 或 `cap.unsupported-syntax` |
| 对象、数组、元组 | 拒绝 | `cap.unsupported-syntax` |
| `any`、`unknown` | 拒绝 | `cap.any` |
| 联合类型 | 拒绝 | 三元式两臂不同、声明为联合都会报 `cap.bad-type` |

## 声明与调用

| 操作 | 状态 | 说明 |
| --- | --- | --- |
| 顶层函数声明 | 支持 | 直接调用，按函数名绑定 |
| 参数 | 支持 | `number` 或 `boolean` |
| 顶层 `const` | 支持 | 初始化表达式按源码顺序求值 |
| 块内 `const` | 支持 | 只在当前块可见，同名遮蔽外层 |
| 块内 `let` | 支持 | 必须带初始值；可读、可遮蔽，赋值留给 04.02 |
| 模块级 `let` | 拒绝 | `cap.module-let`，模块体只接受 `const` |
| 局部 `var` | 拒绝 | `cap.unsupported-syntax` |
| 直接函数调用 | 支持 | 实参个数与类型必须匹配 |
| 递归 | 支持 | 直接递归与相互递归，调用层各自持有参数和局部值 |
| 闭包、嵌套函数 | 拒绝 | `cap.unsupported-syntax` |

## 表达式

| 操作 | 状态 | 说明 |
| --- | --- | --- |
| 数字、布尔字面量 | 支持 | 数字不做十进制重解析 |
| 标识符 | 支持 | 必须指向本文件里的声明 |
| `+` `-` `*` `/` | 支持 | 两个 `number` |
| 一元 `-` `+` | 支持 | 只接受数字字面量，编译期折成位模式 |
| `!` | 支持 | 操作数必须是 `boolean` |
| `<` `<=` `>` `>=` | 支持 | 两个 `number` |
| `===` `!==` | 支持 | 两侧同为 `number` 或同为 `boolean` |
| `==` `!=` | 拒绝 | `cap.bad-operator`，不做隐式转换 |
| `&&` `\|\|` | 支持 | 两侧 `boolean`，短路求值，结果绑定到临时值 |
| `? :` | 支持 | 条件 `boolean`，两臂同为 `number` 或同为 `boolean` |
| 字符串拼接、模板字符串 | 拒绝 | 字符串不在子集内 |
| 赋值表达式 | 拒绝 | `cap.assignment-value`，赋值只能作为独立语句 |

## 语句

| 操作 | 状态 | 说明 |
| --- | --- | --- |
| `return` | 支持 | 有值函数的每条路径都要结束 |
| `if` / `else if` / `else` | 支持 | 条件 `boolean`，分支各自一个作用域 |
| 语句块 | 支持 | 只在 `if` 或函数体里出现 |
| `const` 声明 | 支持 | 一次声明一个值 |
| `let` 声明 | 支持 | 必须带初始值；块内可遮蔽 |
| 简单赋值 `x = value;` | 支持 | 目标必须是可变的 `let`，右值先求值再写入 |
| 复合赋值、自增自减 | 拒绝 | `cap.compound-assignment` / `cap.inc-dec` |
| 表达式语句 | 支持 | 调用保留副作用；裸值求值后丢弃 |
| `console.log(x)` | 支持 | 一个参数，`number` 或 `boolean` |
| `while` | 支持 | 条件每轮重新检查，循环可能零次执行 |
| `break`、`continue` | 支持 | 无标签，只作用于最近循环；带标签写法拒绝 |
| `for` | 计划 | 节点 04 之后 |
| `switch`、`try`、`throw` | 拒绝 | `cap.unsupported-syntax` |
| 顶层 `if` / 顶层语句块 | 拒绝 | 模块体只接受声明、`const` 和表达式语句 |

## 运行与构建

| 操作 | 状态 | 说明 |
| --- | --- | --- |
| `tynel check` | 支持 | 类型检查 + 能力检查 |
| `tynel build` | 支持 | 生成原生程序与构建记录 |
| `tynel run` | 支持 | 构建后运行本次产物 |
| `tynel dump facts` | 支持 | 查看前端事实 |
| `tynel dump nir` | 支持 | 导出通过验证的 NIR |
| `tynel build-ir` | 支持 | 从固定 NIR 回放 |
| 多文件、`import` | 计划 | 节点 05 |
| `windows/amd64` | 计划 | 首版只验证 `darwin/arm64` |

## 相关材料

- 能力检查的错误码见[原生能力白名单](capability.md)。
- 短路与三元式的语义见[短路求值](short-circuit.md)与[三元式](ternary.md)。
- 完整验收结果见[03.07 验收记录](../validation/03-07-combine.md)。
