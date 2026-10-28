// 短路结果用于 const、条件和参数。
function mark(n: number, value: boolean): boolean {
  console.log(n);
  return value;
}

const a: boolean = mark(1, true) && mark(2, false);
console.log(a);

function check(): void {
  if (mark(3, true) || mark(4, false)) {
    console.log(5);
  }
}

check();

function both(x: boolean, y: boolean): boolean {
  return x && y;
}

console.log(both(mark(6, true), mark(7, true)));
