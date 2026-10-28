// for 仍不在本阶段子集内。
function count(n: number): number {
  for (let i: number = 0; i < n; i = i + 1) {
    console.log(i);
  }
  return n;
}

console.log(count(3));
