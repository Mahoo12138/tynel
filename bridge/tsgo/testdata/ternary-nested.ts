// 嵌套三元式与分支内部的准备：只有选中的分支求值。
function mark(n: number, value: boolean): boolean {
  console.log(n);
  return value;
}

console.log(mark(1, false) ? 10 : mark(2, true) ? 20 : 30);
console.log(mark(3, true) ? (mark(4, true) ? 40 : 50) : mark(5, true) ? 60 : 70);
console.log(true ? (mark(6, false) || mark(7, true)) : mark(8, true));
