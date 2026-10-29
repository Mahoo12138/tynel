// 直接递归：小范围阶乘。
function fact(n: number): number {
  if (n <= 1) {
    return 1;
  }
  return n * fact(n - 1);
}

console.log(fact(0));
console.log(fact(1));
console.log(fact(5));
console.log(fact(7));
