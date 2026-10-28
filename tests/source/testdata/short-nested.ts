// 嵌套短路与实参顺序：左侧先求值，右侧只在需要时求值。
function mark(n: number, value: boolean): boolean {
  console.log(n);
  return value;
}

function pair(a: boolean, b: boolean): boolean {
  return a && b;
}

console.log(mark(1, true) && (mark(2, true) && mark(3, false)));
console.log(pair(mark(4, true), mark(5, true) && mark(6, false)));
console.log(pair(mark(7, true) && mark(8, false), mark(9, true)));
