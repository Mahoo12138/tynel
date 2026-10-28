// 局部 var：本阶段只支持 const 与 let。
function f(): number {
  var x: number = 1;
  return x;
}

console.log(f());
