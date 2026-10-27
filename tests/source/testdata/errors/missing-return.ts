// 缺返回路径：条件为假时直接走到函数末尾。
function pick(flag: boolean): number {
  if (flag) {
    return 1;
  }
}

console.log(pick(true));
