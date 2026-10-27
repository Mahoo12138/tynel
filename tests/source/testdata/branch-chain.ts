// else if 链：三个区间各走一条路径。
function label(n: number): void {
  if (n < 0) {
    console.log(0);
  } else if (n === 0) {
    console.log(1);
  } else {
    console.log(2);
  }
}

label(-1);
label(0);
label(5);
