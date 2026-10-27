// 比较：有序比较与严格相等都产生 boolean。
console.log(1 < 2);
console.log(2 <= 2);
console.log(3 > 2);
console.log(2 >= 3);
console.log(1 === 1);
console.log(1 !== 1);

function same(a: boolean, b: boolean): boolean {
  return a === b;
}

console.log(same(true, true));
console.log(same(true, false));
