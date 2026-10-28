// 未初始化的 let：本阶段要求局部值声明时就带初始值。
function f(): number {
  let x: number;
  return 1;
}

console.log(f());
