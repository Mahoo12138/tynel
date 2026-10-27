// 嵌套分支：两层 if 都覆盖两臂。
function grade(n: number): number {
  if (n < 0) {
    if (n < -10) {
      return 0;
    } else {
      return 1;
    }
  } else {
    return 2;
  }
}

console.log(grade(-20));
console.log(grade(-5));
console.log(grade(5));
