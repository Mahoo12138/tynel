// 三元式的结果用于 const、条件、参数和 return。
const a: number = true ? 1 : 2;
console.log(a);

function choose(flag: boolean): number {
  return flag ? 10 : 20;
}

console.log(choose(true));
console.log(choose(false));

function add(x: number, y: number): number {
  return x + y;
}

console.log(add(true ? 1 : 2, false ? 3 : 4));
