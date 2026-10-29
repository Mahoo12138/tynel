// 组合：循环内提前返回，保留已经写入的值。
function early(limit: number): number {
  let total: number = 0;
  let i: number = 0;
  while (i < limit) {
    total = total + i;
    if (i === 2) {
      return total;
    }
    i = i + 1;
  }
  return total;
}

console.log(early(5));
console.log(early(1));
