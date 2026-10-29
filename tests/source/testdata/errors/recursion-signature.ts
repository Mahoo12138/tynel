// 递归调用的实参类型不符：TypeScript 拒绝。
function fact(n: number): number {
  if (n <= 1) {
    return 1;
  }
  return fact(true);
}

console.log(fact(5));
