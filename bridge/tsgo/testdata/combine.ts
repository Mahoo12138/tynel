// 优先级与括号：&& 先于 ||，括号改变组合。
function t(n: number, value: boolean): boolean {
  console.log(n);
  return value;
}

console.log(t(1, true) || t(2, false) && t(3, true));
console.log((t(4, false) || t(5, true)) && t(6, false));
console.log(t(7, false) && t(8, true) || t(9, true));
