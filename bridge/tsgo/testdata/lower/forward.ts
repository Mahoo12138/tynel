// 调用写在声明之前，降低阶段必须仍然指向后面那个函数。
console.log(add(40, 2));

function add(a: number, b: number): number {
  return a + b;
}
