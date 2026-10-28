// 赋值的求值顺序：右侧的打印先发生，然后才写回。
function mark(n: number): number {
  console.log(n);
  return n;
}

function f(): number {
  let x: number = 1;
  x = mark(2);
  console.log(x);
  x = x + mark(3);
  return x;
}

console.log(f());
