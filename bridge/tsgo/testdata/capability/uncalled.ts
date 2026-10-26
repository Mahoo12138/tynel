// 这个函数没有被调用，但检查不靠可达性：里面的控制流同样要拒绝。
function neverCalled(): number {
  if (true) {
    return 1;
  }
  return 0;
}
