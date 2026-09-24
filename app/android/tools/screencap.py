#!/usr/bin/env python
"""安卓验收取证: 读 `adb screencap` 的原始帧做**客观**断言 (纯标准库, 无 PIL)。

M1 验收 (2026-09-24, 模拟器 x86_64 / API 34) 就是用它做的:
  区域哈希证明"点击后界面真的变了", ASCII 色块图证明"颜色没有红蓝互换",
  裁剪导出的 PNG 供肉眼复核 —— 三样都不是"我看图觉得对"。

为什么不用 PIL / ImageGrab:
  - 本机没装 PIL, 为看一眼截图装依赖不值得;
  - screencap 的 raw 格式 (w,h,format[,colorspace] + RGBA_8888) 三行就能解析。

用法 (ADB 不在 PATH 时用环境变量指路:  ADB=H:/AndroidSDK/platform-tools/adb.exe):
  python screencap.py save  <out.png>                     # 整帧 → PNG
  python screencap.py crop  <out.png> x0 y0 x1 y1 [scale] # 裁剪 (+整数放大) → PNG
  python screencap.py hash  x0 y0 x1 y1                   # 区域像素哈希 (前后比对)
  python screencap.py map   x0 y0 x1 y1 [cols]            # ASCII 色块图 (客观看颜色)
"""
import hashlib
import os
import pathlib
import struct
import subprocess
import sys
import zlib


def _adb():
    exe = os.environ.get("ADB", "adb")
    if not pathlib.Path(exe).exists() and os.sep not in exe and "/" not in exe:
        return exe  # 就在 PATH 里
    return exe


def capture():
    out = subprocess.run([_adb(), "exec-out", "screencap"], capture_output=True)
    b = out.stdout
    if len(b) < 16:
        raise SystemExit("screencap 没有输出 (设备掉线? 先 adb devices 看一眼)")
    w, h, fmt = struct.unpack_from("<III", b, 0)
    off = len(b) - w * h * 4          # 头是 12 或 16 字节 (有无 colorspace), 按尾部对齐最稳
    if off not in (12, 16):
        raise SystemExit(f"帧长度不对: len={len(b)} w*h*4={w*h*4} off={off}")
    return w, h, b[off:]


def write_png(path, w, h, pix, x0=0, y0=0, x1=None, y1=None, scale=1):
    x1 = w if x1 is None else x1
    y1 = h if y1 is None else y1
    cw = x1 - x0
    raw = bytearray()
    for y in range(y0, y1):
        row = pix[(y * w + x0) * 4:(y * w + x1) * 4]
        for _ in range(scale):
            raw.append(0)                      # filter type 0 (None)
            if scale == 1:
                raw += row
            else:                              # 最近邻放大: 每个像素重复 scale 次
                srow = bytearray()
                for x in range(cw):
                    srow += row[x * 4:(x + 1) * 4] * scale
                raw += srow

    def chunk(tag, data):
        return (struct.pack(">I", len(data)) + tag + data
                + struct.pack(">I", zlib.crc32(tag + data) & 0xffffffff))

    ihdr = struct.pack(">IIBBBBB", cw * scale, (y1 - y0) * scale, 8, 6, 0, 0, 0)
    png = (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr)
           + chunk(b"IDAT", zlib.compress(bytes(raw), 6)) + chunk(b"IEND", b""))
    pathlib.Path(path).write_bytes(png)
    print(f"{path}  {cw*scale}x{(y1-y0)*scale}")


def region_hash(pix, w, x0, y0, x1, y1):
    h = hashlib.sha256()
    for y in range(y0, y1):
        h.update(pix[(y * w + x0) * 4:(y * w + x1) * 4])
    return h.hexdigest()[:16]


def color_map(pix, w, x0, y0, x1, y1, cols=72):
    """把区域压成 ASCII 色块图: 每格取块内平均色 → 一个字符。
    W 白 / K 黑 / R 红 / G 绿 / B 蓝 / Y 黄 / C 青 / M 品红 / . 灰阶 / # 深灰。"""
    rows = max(1, int((y1 - y0) / (x1 - x0) * cols * 0.5))
    cw, chh = (x1 - x0) / cols, (y1 - y0) / rows
    out = []
    for ry in range(rows):
        line = []
        for rx in range(cols):
            sx0 = int(x0 + rx * cw)
            sx1 = max(sx0 + 1, int(x0 + (rx + 1) * cw))
            sy0 = int(y0 + ry * chh)
            sy1 = max(sy0 + 1, int(y0 + (ry + 1) * chh))
            r = g = b = n = 0
            for y in range(sy0, sy1, max(1, (sy1 - sy0) // 4)):
                for x in range(sx0, sx1, max(1, (sx1 - sx0) // 4)):
                    i = (y * w + x) * 4
                    r += pix[i]; g += pix[i + 1]; b += pix[i + 2]; n += 1
            r, g, b = r // n, g // n, b // n
            mx, mn = max(r, g, b), min(r, g, b)
            if mx > 240 and mn > 240:
                c = "W"
            elif mx < 40:
                c = "K"
            elif mx - mn < 24:
                c = "W" if mx > 200 else ("." if mx > 100 else "#")
            elif r == mx and g > mn + 40 and b < 120:
                c = "Y"
            elif r == mx and b > 120:
                c = "M"
            elif r == mx:
                c = "R"
            elif g == mx and b > mn + 40:
                c = "C"
            elif g == mx:
                c = "G"
            else:
                c = "B"
            line.append(c)
        out.append("".join(line))
    print("\n".join(out))


def main():
    mode = sys.argv[1]
    w, h, pix = capture()
    if mode == "save":
        write_png(sys.argv[2], w, h, pix)
    elif mode == "crop":
        a = [int(v) for v in sys.argv[3:7]]
        scale = int(sys.argv[7]) if len(sys.argv) > 7 else 1
        write_png(sys.argv[2], w, h, pix, a[0], a[1], a[2], a[3], scale)
    elif mode == "hash":
        a = [int(v) for v in sys.argv[2:6]]
        print(region_hash(pix, w, a[0], a[1], a[2], a[3]))
    elif mode == "map":
        a = [int(v) for v in sys.argv[2:6]]
        cols = int(sys.argv[6]) if len(sys.argv) > 6 else 72
        color_map(pix, w, a[0], a[1], a[2], a[3], cols)
    else:
        raise SystemExit(__doc__)
    print(f"frame {w}x{h}")


main()
