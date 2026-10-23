// 本阶段的输入：一个只做加法的函数。02.05 会把它降低成 NIR，
// 现在只用来确认前端能检查它且不产生诊断。
function add(a: number, b: number): number {
  return a + b;
}

const answer: number = add(40, 2);
