// 实参求值顺序：先 log(1) 再 log(2)，最后才是 add。
function log(n: number): number {
  console.log(n);
  return n;
}

function add(a: number, b: number): number {
  return a + b;
}

console.log(add(log(1), log(2)));
