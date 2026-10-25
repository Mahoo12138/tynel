// 前向调用：add 在声明之前就被调用，函数声明会提升。
const answer: number = add(40, 2);

function add(a: number, b: number): number {
  return a + b;
}

// 同名参数：这里的 a 和 add 的 a 是两个不同的声明。
function scale(a: number): number {
  return a * 2;
}

const doubled: number = scale(answer);
