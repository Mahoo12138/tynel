// 输出顺序：实参、比较和三元式按源码顺序求值。
function f(n: number, value: number): number {
  console.log(n);
  return value;
}

console.log(f(1, 10) + f(2, 20));
console.log((f(3, 1) < f(4, 2)) ? f(5, 50) : f(6, 60));
console.log(f(7, 1) + (f(8, 2) < f(9, 3) ? f(10, 10) : f(11, 20)));
