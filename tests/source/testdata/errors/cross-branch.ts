// 跨分支读取：else 分支看不到 then 分支里的 x。
if (true) {
  const x = 1;
} else {
  console.log(x);
}
