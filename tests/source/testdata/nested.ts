// 嵌套调用与四则运算。
function add(a: number, b: number): number {
  return a + b;
}

function mul(a: number, b: number): number {
  return a * b;
}

console.log(add(mul(2, 3), add(1, 4)));
