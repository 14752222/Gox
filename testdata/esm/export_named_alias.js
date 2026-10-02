// 验证: 无 from 的本地命名导出 `export { a as b }`。
// value 是本地绑定, 对外名字是 alias; 本文件内 value 仍可用。
const value = 7;
export { value as alias };

value;
