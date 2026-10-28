// 带标签的 continue：TypeScript 允许，本阶段拒绝。
function f(): number {
  let i: number = 0;
  outer: while (i < 3) {
    i = i + 1;
    continue outer;
  }
  return i;
}

console.log(f());
