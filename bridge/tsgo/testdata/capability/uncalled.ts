// 这个函数没有被调用，但检查不靠可达性：里面的 for 同样要拒绝。
function neverCalled(n: number): number {
  for (let i: number = 0; i < n; i = i + 1) {
    console.log(i);
  }
  return n;
}
