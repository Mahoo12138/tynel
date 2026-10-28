// 三元式只求值选中的分支。
function pick(n: number, value: number): number {
  console.log(n);
  return value;
}

console.log(true ? pick(1, 10) : pick(2, 20));
console.log(false ? pick(3, 10) : pick(4, 20));
