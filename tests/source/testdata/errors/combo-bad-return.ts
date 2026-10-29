// 组合破坏返回路径：只有循环体里的 return，循环可能零次执行。
function early(limit: number): number {
  let total: number = 0;
  let i: number = 0;
  while (i < limit) {
    total = total + i;
    return total;
  }
}

console.log(early(5));
