// void 调用没有值，不能当作参数传出去。
function announce(n: number): void {
  console.log(n);
}

console.log(announce(1));
