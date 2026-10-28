// 给参数赋值：TypeScript 允许，本阶段拒绝。
function f(x: number): number {
  x = 2;
  return x;
}

console.log(f(1));
