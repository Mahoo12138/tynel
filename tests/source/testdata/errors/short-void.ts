// void 调用没有值，不能当短路的操作数。
function announce(): void {
  console.log(1);
}

true && announce();
