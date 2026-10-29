// Collatz 步数：循环版与递归版，输出应当一致。
function isEven(n: number): boolean {
  if (n === 0) {
    return true;
  }
  if (n === 1) {
    return false;
  }
  return isEven(n - 2);
}

function stepsIter(n: number): number {
  let current: number = n;
  let count: number = 0;
  while (current !== 1) {
    if (isEven(current)) {
      current = current / 2;
    } else {
      current = current * 3 + 1;
    }
    count = count + 1;
  }
  return count;
}

function stepsRec(n: number): number {
  if (n === 1) {
    return 0;
  }
  if (isEven(n)) {
    return 1 + stepsRec(n / 2);
  }
  return 1 + stepsRec(n * 3 + 1);
}

console.log(stepsIter(27));
console.log(stepsRec(27));
console.log(stepsIter(6));
console.log(stepsRec(6));
console.log(stepsIter(1));
