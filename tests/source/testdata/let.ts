// 已初始化的 let：读取与 const 相同，块内同名变量遮蔽外层。
function outer(): number {
  let x: number = 1;
  if (true) {
    let x: number = 2;
    console.log(x);
  }
  return x;
}

function flags(): boolean {
  let on: boolean = true;
  return on;
}

console.log(outer());
console.log(flags());
