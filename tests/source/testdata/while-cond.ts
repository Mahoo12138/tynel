// 条件函数的打印次数与循环次数一致。
function mark(n: number, value: boolean): boolean {
  console.log(n);
  return value;
}

function count(): number {
  let i: number = 0;
  while (mark(i, i < 3)) {
    i = i + 1;
  }
  return i;
}

console.log(count());
