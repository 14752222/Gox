// 标签块: break 到块末尾
let reached = false
block: {
  console.log('in block')
  if (true) break block
  reached = true
  console.log('should not print')
}
console.log('after block, reached =', reached)

// 未命名的 break 仍作用于最内层循环
let arr = []
for (let i = 0; i < 3; i++) {
  for (let j = 0; j < 3; j++) {
    if (j === 2) break
    arr.push(i * 10 + j)
  }
}
console.log('unnamed break:', arr.join(','))
