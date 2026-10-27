// 不可达的代码同样要过能力检查：这里的取模不会被生成，但仍要拒绝。
function early(): number {
  return 1;
  console.log(1 % 2);
}

console.log(early());
