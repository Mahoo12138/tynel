// 同一个块里重复声明：TypeScript 直接拒绝。
if (true) {
  const x = 1;
  const x = 2;
  console.log(x);
}
