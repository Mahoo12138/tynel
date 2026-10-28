// 数字条件：TypeScript 允许，本阶段要求 boolean。
function f(): number {
  while (1) {
    return 1;
  }
  return 0;
}

console.log(f());
