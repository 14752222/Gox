// testdata/ts/broken.ts —— 故意写错的样例: 验证转译错误定位到 .ts 行号。
// 注意: 本文件不会被其它夹具 import (否则会拖垮整组用例)。

const broken: = 1;
