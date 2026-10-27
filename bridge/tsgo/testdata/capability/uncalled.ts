// 这个函数没有被调用，但检查不靠可达性：里面的循环同样要拒绝。
function neverCalled(n: number): number {
  while (n < 3) {
    console.log(n);
  }
  return n;
}
