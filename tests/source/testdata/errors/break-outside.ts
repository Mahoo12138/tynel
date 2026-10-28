// 循环外的 break：TypeScript 拒绝。
function f(): number {
  break;
  return 1;
}

console.log(f());
