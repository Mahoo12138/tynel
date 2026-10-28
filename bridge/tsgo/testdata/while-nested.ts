// 嵌套循环：内层结束后外层继续，循环体里的 let 每轮重新初始化。
function mark(n: number): number {
  console.log(n);
  return n;
}

function f(): number {
  let total: number = 0;
  let i: number = 0;
  while (i < 2) {
    let j: number = 0;
    while (j < 2) {
      total = total + mark(1);
      j = j + 1;
    }
    i = i + 1;
  }
  return total;
}

console.log(f());
