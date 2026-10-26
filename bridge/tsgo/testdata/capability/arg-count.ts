// 实参个数与声明不符，TypeScript 自己就会拒绝。
function add(a: number, b: number): number {
  return a + b;
}

console.log(add(1));
