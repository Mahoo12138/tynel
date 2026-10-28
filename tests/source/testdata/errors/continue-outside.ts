// 循环外的 continue：TypeScript 拒绝。
function f(): number {
  continue;
  return 1;
}

console.log(f());
