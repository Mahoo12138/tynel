// 单臂提前返回，后面再补一个 return。
function pick(flag: boolean): number {
  if (flag) {
    return 1;
  }
  return 2;
}

console.log(pick(true));
console.log(pick(false));
