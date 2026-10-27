// 块外读取局部值：x 只在 if 的 then 块里存在。
if (true) {
  const x = 1;
}
console.log(x);
