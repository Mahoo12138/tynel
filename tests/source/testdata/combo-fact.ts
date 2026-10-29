// 组合：迭代阶乘与递归阶乘给出同一个参考值。
function factIter(n: number): number {
  let result: number = 1;
  let i: number = 2;
  while (i <= n) {
    result = result * i;
    i = i + 1;
  }
  return result;
}

function factRec(n: number): number {
  if (n <= 1) {
    return 1;
  }
  return n * factRec(n - 1);
}

console.log(factIter(6));
console.log(factRec(6));
console.log(factIter(7));
console.log(factRec(7));
