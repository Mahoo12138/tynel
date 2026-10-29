// 组合：循环、赋值、短路与三元式在同一个表达式里。
function score(n: number, bonus: boolean): number {
  let total: number = 0;
  let i: number = 0;
  while (i < n) {
    total = total + (bonus && i === 1 ? 10 : i);
    i = i + 1;
  }
  return total;
}

console.log(score(4, true));
console.log(score(4, false));
