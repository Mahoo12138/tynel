// 局部 let 的类型也必须在 number 或 boolean 之内。
function f(): string {
  let x: string = "a";
  return x;
}

console.log(f());
