// new Function 与 eval 同类：代码在运行时才产生。
function make(): void {
  new Function("return 1");
}
