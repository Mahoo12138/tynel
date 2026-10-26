// 表达式语句丢弃结果：求值照做，值不打印。
function add(a: number, b: number): number {
  return a + b;
}

const answer: number = add(40, 2);

answer;
add(1, 2);

console.log(answer);
