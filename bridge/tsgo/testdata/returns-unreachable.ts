// return 之后的语句不可达：能力检查仍然看它，生成阶段不输出。
function early(): number {
  return 1;
  console.log(2);
}

console.log(early());
