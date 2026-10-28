// 内层 break 只结束内层循环，外层继续。
function f(): number {
  let total: number = 0;
  let i: number = 0;
  while (i < 3) {
    let j: number = 0;
    while (j < 3) {
      if (j === 1) {
        break;
      }
      total = total + 1;
      j = j + 1;
    }
    i = i + 1;
  }
  return total;
}

console.log(f());
