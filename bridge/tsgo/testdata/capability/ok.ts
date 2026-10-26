// 本阶段支持的全部写法：number 函数、const、四则运算、直接调用和受限输出。
function add(a: number, b: number): number {
  return a + b;
}

const answer: number = add(40, 2);

function main(): void {
  console.log(answer);
}
