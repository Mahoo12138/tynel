// 多层选择：三元式与短路组合，只有选中的路径执行。
function mark(n: number, value: boolean): boolean {
  console.log(n);
  return value;
}

console.log(mark(1, false) ? 10 : mark(2, true) ? 20 : 30);
console.log(mark(3, true) ? (mark(4, false) ? 40 : 50) : 60);
console.log(true ? (false ? 1 : 2) : (true ? 3 : 4));
console.log((mark(5, true) || mark(6, false)) ? 70 : 80);
