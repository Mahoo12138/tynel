// 未知递归目标：TypeScript 拒绝。
function fact(n: number): number {
  if (n <= 1) {
    return 1;
  }
  return missing(n - 1);
}

console.log(fact(5));
