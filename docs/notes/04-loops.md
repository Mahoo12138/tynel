# 用一个小工具试状态与循环

状态、循环和递归都通过验收之后，我想找一个不刻意、又能把这些东西串起来的小工具。最后选了 Collatz 步数：给一个正整数，偶数就除以二，奇数就乘三加一，数到一为止。它有一个循环、一个累加变量、一个递归版本，还有一个“偶数还是奇数”的判断，正好把这一章的东西都用上。

源码放在 `examples/collatz.ts`：

```typescript
// Collatz 步数：循环版与递归版，输出应当一致。
function isEven(n: number): boolean {
  if (n === 0) {
    return true;
  }
  if (n === 1) {
    return false;
  }
  return isEven(n - 2);
}

function stepsIter(n: number): number {
  let current: number = n;
  let count: number = 0;
  while (current !== 1) {
    if (isEven(current)) {
      current = current / 2;
    } else {
      current = current * 3 + 1;
    }
    count = count + 1;
  }
  return count;
}

function stepsRec(n: number): number {
  if (n === 1) {
    return 0;
  }
  if (isEven(n)) {
    return 1 + stepsRec(n / 2);
  }
  return 1 + stepsRec(n * 3 + 1);
}

console.log(stepsIter(27));
console.log(stepsRec(27));
console.log(stepsIter(6));
console.log(stepsRec(6));
console.log(stepsIter(1));
```

`stepsIter` 用 `while` 和两个可变的 `let`，`stepsRec` 用提前返回和自调用，`isEven` 用递归做减法。三者的边界都很窄，正好适合看组合后的行为。

## 第一次尝试：取模被拒绝

我最初写的是最常见的形式：

```typescript
function isEven(n: number): boolean {
  return n % 2 === 0;
}
```

`./bin/tynel check` 没有进入降低阶段，直接报：

```text
$ ./bin/tynel check examples/collatz-mod.ts
…/examples/collatz-mod.ts:4:10: 能力限制 cap.bad-operator: 运算符 "%" 不在本阶段支持的 + - * / 与比较之内
$ echo $?
3
```

这不是运行时兜底，也不是“先编译再想办法”。`%` 不在能力白名单里，检查阶段就拒绝，退出码是 `3`。我一开始觉得有点可惜，后来发现这个拒绝反而逼出了后面那个更有意思的错误。

## 第二次尝试：能编译，但语义是错的

不能用 `%`，我换了一个看起来等价的办法：

```typescript
function isEven(n: number): boolean {
  return n / 2 * 2 === n;
}
```

它编译通过了。`./bin/tynel run examples/collatz.ts` 和 Node 的输出也一致，都是 `1075`、`1075`、`1076`、`1076`、`0`。如果只看“编译器输出和 Node 一样”，这次实验已经通过了。

但 Collatz 的步数是已知的：`27` 要 `111` 步，`6` 要 `8` 步。`1075` 不是“另一种实现”，它是错的。原因很简单：对任何有限的整数，`n / 2 * 2` 都等于 `n`，这个判断永远为真，于是 `stepsIter` 一直在做除以二，永远不会走到奇数分支。

编译器没有错。它忠实地编译了我写的程序，也忠实地复现了我的错误。这件事让我确认了一件事：源码回归只能证明“和参考实现一致”，不能证明“程序符合意图”。所以这一章除了对照 Node，还得把已知的 Collatz 步数写进预期。

## 第三次：用减法递归判断奇偶

最后改成：

```typescript
function isEven(n: number): boolean {
  if (n === 0) {
    return true;
  }
  if (n === 1) {
    return false;
  }
  return isEven(n - 2);
}
```

这个版本只用减法、比较和递归，全部在子集里。运行结果：

```text
$ ./bin/tynel run examples/collatz.ts
111
111
8
8
0
```

Node 的输出逐行相同。`stepsIter(27)` 与 `stepsRec(27)` 都是 `111`，`stepsIter(6)` 与 `stepsRec(6)` 都是 `8`，`stepsIter(1)` 是 `0`。循环版和递归版在三个输入上一致，说明循环的回边、递归的调用层和提前返回都没有互相干扰。

## NIR 里看到了什么

`stepsIter` 的核心是一个带 id 的 `while`：

```json
{ "op": "while", "id": 1,
  "cond": {"op": "compare", "operator": "!==",
    "left": {"op": "local", "name": "current"},
    "right": {"op": "number", "bits": "0x3ff0000000000000"}},
  "body": [
    {"op": "if", "cond": {"op": "call", "function": "isEven", "args": [...]},
     "then": [{"op": "set", "name": "current", "value": {"op": "binary", "operator": "/", ...}}],
     "else": [{"op": "set", "name": "current", "value": {"op": "binary", "operator": "+", ...}}]},
    {"op": "set", "name": "count", "value": {"op": "binary", "operator": "+", ...}}
  ]}
```

循环 id 是 `1`，这一版没有 `break`，但 `stepsIter` 的提前退出仍然由条件本身完成。`stepsRec` 里则是 `if` 加 `return`，没有循环 id。把 NIR 存下来单独回放，产物和源码构建逐字节相同：

```text
$ ./bin/tynel build examples/collatz.ts -o out/collatz
已生成 out/collatz（2430002 字节）
  源码     examples/collatz.ts（0c5ee6b7249b）
  输入     84d479911726
  编译器   7a75b4acf10b
  构建记录 out/collatz.build.json

$ ./bin/tynel dump nir examples/collatz.ts > out/collatz.nir.json
$ ./bin/tynel build-ir out/collatz.nir.json -o out/collatz-replay
已生成 out/collatz-replay（2430002 字节）
  输入     84d479911726
  编译器   7a75b4acf10b
  构建记录 out/collatz-replay.build.json

$ shasum -a 256 out/collatz out/collatz-replay
8177c79116c9cbbaa0f5aaa14cddd24a0413d14aaefe624be6f4f0e984b951b9  out/collatz
8177c79116c9cbbaa0f5aaa14cddd24a0413d14aaefe624be6f4f0e984b951b9  out/collatz-replay
```

## 这次实验的边界

- `%` 被 `cap.bad-operator` 拒绝，没有运行时兜底；`for` 仍然被 `cap.unsupported-syntax` 拒绝，这一章没有绕过它。
- 输入是写死的常量，没有命令行参数或标准输入；它是一段实验源码，不是产品命令。
- `isEven` 用减法递归，只适合非负整数；负数不在这个工具的输入范围里。
- 递归深度只到 `27` 这一档，没有测栈溢出、尾调用或性能。
- 只在 `darwin/arm64` 上跑过，Node 对照也是本机的 v22.22.2。

这次取舍让我更愿意把“编译通过”和“结果正确”分开看。前者是编译器的事，后者还得靠已知参考值、循环版与递归版互相对照，以及一个被明确拒绝的失败案例。相关规则见[状态、循环与调用](../design/state-and-loops.md)，验收记录见[04.07 验收记录](../validation/04-loops.md)。
