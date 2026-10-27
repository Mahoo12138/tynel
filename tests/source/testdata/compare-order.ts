// 求值顺序：左右操作数按源码顺序求值，先打印 1、2，再打印比较结果。
function mark(n: number): number {
  console.log(n);
  return n;
}

console.log(mark(1) < mark(2));
