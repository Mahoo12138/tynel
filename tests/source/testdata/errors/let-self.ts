// 初始化表达式读取自身：TypeScript 在声明完成前拒绝这个名字。
function f(): number {
  let x: number = x;
  return x;
}

console.log(f());
