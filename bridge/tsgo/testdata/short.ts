// 短路：false && 右侧不执行，true || 右侧不执行。
function mark(n: number, value: boolean): boolean {
  console.log(n);
  return value;
}

console.log(mark(1, false) && mark(2, true));
console.log(mark(3, true) && mark(4, true));
console.log(mark(5, true) || mark(6, false));
console.log(mark(7, false) || mark(8, true));
