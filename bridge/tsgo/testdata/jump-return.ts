// return 退出整个函数，不只是当前循环。
function f(): number {
  let i: number = 0;
  while (i < 10) {
    if (i === 2) {
      return i;
    }
    i = i + 1;
  }
  return -1;
}

console.log(f());
