// 组合：分支里的赋值只走选中的那条路径。
function pick(flag: boolean): number {
  let x: number = 1;
  if (flag) {
    x = 2;
  } else {
    x = 3;
  }
  return x;
}

console.log(pick(true));
console.log(pick(false));
