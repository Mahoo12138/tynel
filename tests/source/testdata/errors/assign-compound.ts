// 复合赋值不在本阶段子集内。
function f(): number {
  let x: number = 1;
  x += 1;
  return x;
}

console.log(f());
