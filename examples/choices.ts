// 选择：比较、分支、短路和三元式挑出一个值。
function pick(n: number): number {
  if (n < 0) {
    return -1;
  }
  return n === 0 ? 0 : 1;
}

function mark(n: number, value: boolean): boolean {
  console.log(n);
  return value;
}

console.log(pick(-5));
console.log(pick(0));
console.log(pick(7));
console.log(mark(1, false) && mark(2, true));
console.log(mark(3, true) || mark(4, false));
