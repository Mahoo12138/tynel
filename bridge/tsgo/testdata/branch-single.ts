// 单条语句的分支：if / else if / else 都不带花括号。
function sign(n: number): void {
  if (n < 0) console.log(0);
  else if (n === 0) console.log(1);
  else console.log(2);
}

sign(-1);
sign(0);
sign(5);
