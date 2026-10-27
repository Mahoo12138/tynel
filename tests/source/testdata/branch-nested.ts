// 嵌套分支：外层决定是否进入内层，内层再分两条。
function choose(a: boolean, b: boolean): void {
  if (a) {
    if (b) {
      console.log(1);
    } else {
      console.log(2);
    }
  } else {
    console.log(3);
  }
}

choose(true, true);
choose(true, false);
choose(false, true);
