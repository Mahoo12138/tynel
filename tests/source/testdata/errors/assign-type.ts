// 赋值类型不一致：let 的类型不能改变。
function f(): number {
  let x: number = 1;
  x = true;
  return x;
}

console.log(f());
