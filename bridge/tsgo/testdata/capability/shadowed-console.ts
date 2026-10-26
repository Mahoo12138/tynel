// console 被参数遮蔽：这个名字不再是内置输出，而是用户自己的符号。
function shout(console: { log(n: number): void }, n: number): void {
  console.log(n);
}
