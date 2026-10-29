// 组合破坏赋值目标：循环体把 number 写成 boolean。
function sum(n: number): number {
  let total: number = 0;
  while (n > 0) {
    total = true;
    n = n - 1;
  }
  return total;
}

console.log(sum(3));
