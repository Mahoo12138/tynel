// 实参从左到右求值：先输出 1，再输出 2，最后输出和。
function log(n: number): number {
  console.log(n);
  return n;
}

function add(a: number, b: number): number {
  return a + b;
}

console.log(add(log(1), log(2)));
