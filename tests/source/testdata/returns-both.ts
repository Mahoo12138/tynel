// 两臂都返回：函数体末尾不需要再补 return。
function sign(n: number): number {
  if (n < 0) {
    return -1;
  } else {
    return 1;
  }
}

console.log(sign(-5));
console.log(sign(5));
