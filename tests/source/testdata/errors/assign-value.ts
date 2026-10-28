// 赋值作为表达式：结果被使用，本阶段不支持。
function f(): number {
  let x: number = 1;
  const y: number = (x = 2);
  return y;
}

console.log(f());
