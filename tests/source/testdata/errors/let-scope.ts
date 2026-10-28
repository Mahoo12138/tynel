// 越界作用域：块外读不到块里的 let。
function f(): number {
  if (true) {
    let x: number = 1;
  }
  return x;
}

console.log(f());
