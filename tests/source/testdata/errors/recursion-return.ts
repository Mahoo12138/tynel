// 递归函数的返回值类型不符：TypeScript 拒绝。
function down(n: number): number {
  if (n === 0) {
    return true;
  }
  return down(n - 1);
}

console.log(down(3));
