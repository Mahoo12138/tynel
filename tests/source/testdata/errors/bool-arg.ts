// 类型错误：boolean 参数收到 number。
function invert(value: boolean): boolean {
  return !value;
}

console.log(invert(1));
