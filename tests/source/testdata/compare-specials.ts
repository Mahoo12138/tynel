// 特殊值：NaN 与任何值都不相等，负零等于正零。
const nan: number = 0 / 0;
console.log(nan < 1);
console.log(nan <= 1);
console.log(nan > 1);
console.log(nan >= 1);
console.log(nan === nan);
console.log(nan !== nan);

const zero: number = 0;
const negZero: number = -0;
console.log(negZero === zero);
console.log(negZero < zero);

console.log(1 / 0 > 1e308);
console.log(-1 / 0 < 0);
