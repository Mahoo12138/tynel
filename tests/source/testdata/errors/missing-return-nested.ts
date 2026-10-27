// 嵌套分支缺返回路径：内层 if 没有 else。
function pick(a: boolean, b: boolean): number {
  if (a) {
    if (b) {
      return 1;
    }
  } else {
    return 2;
  }
}

console.log(pick(true, true));
