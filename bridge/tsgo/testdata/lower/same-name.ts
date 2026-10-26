// 两个函数各有一个 a：降低时不能把它们当成同一个参数。
function first(a: number): number {
  return a + 1;
}

function second(a: number): number {
  return a * 2;
}

console.log(first(1) + second(3));
