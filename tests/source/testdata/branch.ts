// 两条路径：条件为真走 then，为假走 else；两边同名的局部值互不影响。
function mark(n: number): number {
  console.log(n);
  return n;
}

function pick(flag: boolean): void {
  if (flag) {
    const x = mark(1);
    console.log(x);
  } else {
    const x = mark(2);
    console.log(x);
  }
}

pick(true);
pick(false);
