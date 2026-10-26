// 调用写在声明之前：函数声明会提升。
console.log(add(40, 2));

function add(a: number, b: number): number {
  return a + b;
}
