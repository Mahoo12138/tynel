// 被拒绝的写法：取模不在本阶段子集内。
// 保留这份源码作为失败记录，不参与构建。
function isEven(n: number): boolean {
  return n % 2 === 0;
}

console.log(isEven(4));
