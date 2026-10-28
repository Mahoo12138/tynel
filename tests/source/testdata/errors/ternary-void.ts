// 两条分支都是 void，没有可绑定的结果。
function announce(): void {
  console.log(1);
}

true ? announce() : announce();
