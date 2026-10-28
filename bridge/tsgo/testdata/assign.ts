// 局部简单赋值：右值先算，再写回同一个 let。
function counter(): number {
  let x: number = 1;
  x = x + 1;
  x = x * 2;
  return x;
}

function flag(): boolean {
  let on: boolean = false;
  on = true;
  return on;
}

console.log(counter());
console.log(flag());
