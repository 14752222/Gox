// gx/media 图片处理演示: load → resize → crop → encode → write。
// 输入/输出路径由运行方注入 (__IN__ / __OUT__ 占位符)。
import { loadImage, resize, crop, encodeImage, writeImage, freeImage } from "gx/media";

const checks = [];
const ok = (name, cond) => checks.push(name + "=" + (cond ? "true" : "false"));

const img = loadImage("__IN__");
ok("load 4x4", img.width === 4 && img.height === 4);
ok("format png", img.format === "png");

const small = resize(img, 2, 3);
ok("resize 2x3", small.width === 2 && small.height === 3);

const part = crop(img, 1, 1, 2, 2);
ok("crop 2x2", part.width === 2 && part.height === 2);

const pngBytes = encodeImage(part, "png");
// PNG 魔数: 137 80 78 71
ok("encode png magic", pngBytes.length > 8 && pngBytes[0] === 137 && pngBytes[1] === 80);

const jpgLow = encodeImage(img, "jpeg", 20);
const jpgHigh = encodeImage(img, "jpeg", 95);
ok("jpeg quality 影响体积", jpgLow.length < jpgHigh.length);

writeImage(part, "__OUT__", 90);
const round = loadImage("__OUT__");
ok("write→load 回读 2x2", round.width === 2 && round.height === 2);
ok("回读 format jpeg", round.format === "jpeg");

freeImage(img);
let freed = false;
try { resize(img, 1, 1); } catch (e) { freed = true; }
ok("freeImage 后报错", freed);

globalThis.__imgDemoSummary = checks.join(" | ");
