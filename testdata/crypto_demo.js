// ru3TZK 演示: 摘要与随机数 —— gx/crypto 的最小可用集 (无窗口, 纯终端输出)。
// 运行: go run ./cmd/gox testdata/crypto_demo.js
//
// 本脚本同时是冒烟用例: 它把全局形态 (hash / crypto.*) 与模块形态
// (import ... from "gx/crypto") 各走一遍 —— 两者必须是同一份实现,
// 否则改名/补参数时只改一处就会静默漂移。
//
// 打印出的指纹可与外部工具核对 (printf gox | sha256sum), 不是"两次调用相等"
// 那种自证。
//
// 语法纪律 (与 testdata/http_demo.js 同): 运行时只认 let/const (没有 var),
// 顶层不能 await —— 异步逻辑放进 async function。
import { hash as modHash, digest as modDigest, randomUUID, getRandomValues } from "gx/crypto";

function hex(ab) {
  const view = new Uint8Array(ab);
  let s = "";
  for (let i = 0; i < view.length; i++) {
    s += view[i].toString(16).padStart(2, "0");
  }
  return s;
}

async function main() {
  console.log("== 同步 hash(data, algo?) ==");
  console.log("hash('gox')           =", hash("gox"));
  console.log("hash('gox', 'SHA-1')  =", hash("gox", "SHA-1"));
  console.log("hash('gox', 'sha512') =", hash("gox", "sha512"));
  console.log("hash('gox', 'md5')    =", hash("gox", "MD5"));

  console.log("");
  console.log("== 入参形态 (同一份字节, 同一个指纹) ==");
  console.log("string                =", hash("gox"));
  console.log("Uint8Array            =", hash(new Uint8Array([103, 111, 120])));
  console.log("ArrayBuffer           =", hash(new Uint8Array([103, 111, 120]).buffer));
  console.log("byte array (fs 形状)   =", hash([103, 111, 120]));

  console.log("");
  console.log("== crypto.subtle.digest(algo, data) -> Promise<ArrayBuffer> ==");
  const ab = await crypto.subtle.digest("SHA-256", "gox");
  console.log("byteLength            =", ab.byteLength);
  console.log("hex                   =", hex(ab));

  console.log("");
  console.log("== 模块形态 (与全局同一份实现) ==");
  console.log("modHash === hash      =", modHash === hash);
  console.log("modHash('gox')        =", modHash("gox"));
  const modAb = await modDigest("sha-256", "gox"); // 算法名大小写/连字符不敏感
  console.log("modDigest hex         =", hex(modAb));

  console.log("");
  console.log("== 随机 ==");
  console.log("crypto.randomUUID()   =", crypto.randomUUID());
  console.log("randomUUID()          =", randomUUID());
  const salt = new Uint8Array(8);
  getRandomValues(salt);
  console.log("getRandomValues(8)    =", hex(salt.buffer));

  console.log("");
  console.log("== 错误分支 (都要报出来, 不能静默出摘要) ==");
  try {
    hash("gox", "SHA-999");
  } catch (e) {
    console.log("未知算法              =", e.name + ": " + e.message);
  }
  try {
    await crypto.subtle.digest("MD5", "gox"); // WebCrypto 里没有 MD5 —— 与浏览器同口径
  } catch (e) {
    console.log("subtle 不认 MD5       =", e.name + ": " + e.message);
  }

  // 给回归测试用的接口面 (见 vm/crypto_demo_test.go): 演示脚本不参与编译,
  // 悄悄过期没人知道, 所以留一个可被断言的出口。
  globalThis.__cryptoDemoSummary = {
    sha256: hash("gox"),
    digestHex: hex(ab),
    digestBytes: ab.byteLength,
    sameImpl: modHash === hash,
    uuid: crypto.randomUUID(),
  };
}

const boot = main();
