// void 函数正常结束：条件为假时直接走到函数末尾。
function show(flag: boolean): void {
  if (flag) {
    console.log(1);
  }
}

show(true);
show(false);
