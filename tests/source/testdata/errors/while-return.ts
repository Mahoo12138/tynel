// 只依赖循环体 return：循环可能零次执行，返回路径不完整。
function f(n: number): number {
  while (n > 0) {
    return n;
  }
}

console.log(f(1));
