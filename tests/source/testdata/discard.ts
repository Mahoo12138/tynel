// 表达式语句丢弃结果：只有 console.log 会输出。
function add(a: number, b: number): number {
  return a + b;
}

const answer: number = add(40, 2);

answer;
add(1, 2);

console.log(answer);
