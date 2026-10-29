// 不同调用层的局部值互不覆盖。
function sum(n: number): number {
  let local: number = n;
  if (n === 0) {
    return local;
  }
  return local + sum(n - 1);
}

console.log(sum(4));
