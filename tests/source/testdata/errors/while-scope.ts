// 循环体里的 let 在循环外不可见。
function f(): number {
  while (false) {
    let x: number = 1;
  }
  return x;
}

console.log(f());
