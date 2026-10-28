// continue 回到条件检查，不跳过下一轮。
function f(): number {
  let total: number = 0;
  let i: number = 0;
  while (i < 5) {
    i = i + 1;
    if (i === 3) {
      continue;
    }
    total = total + i;
  }
  return total;
}

console.log(f());
