// 组合：循环累加，零次、一次、多次。
function sumTo(n: number): number {
  let total: number = 0;
  let i: number = 1;
  while (i <= n) {
    total = total + i;
    i = i + 1;
  }
  return total;
}

console.log(sumTo(0));
console.log(sumTo(1));
console.log(sumTo(10));
