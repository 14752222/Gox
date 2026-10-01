#!/usr/bin/env python
"""iOS 验收取证: 读 `xcrun simctl io booted screenshot` 的 PNG 做**客观**断言。

这是 `app/android/tools/screencap.py` 的 iOS 等价物 —— 折叠屏验收里那条
"长 feed 不跳动 / 折痕上没内容落下"**必须**用区域哈希断言, 靠肉眼看截图
是最容易放过回归的方式 (资料陷阱 3)。

与安卓版的两处差异 (都是平台给的, 不是取舍):
  - 源不同: 安卓是 `adb exec-out screencap` 的**裸帧** (三行解析出 RGBA),
    iOS 是 `simctl io booted screenshot` 的 **PNG** ⇒ 这里自带一个只认
    "8 位 RGBA/灰度 + filter 0..4" 的迷你 PNG 解码器 (zlib 是标准库,
    不用 PIL)。
  - 设备选择: 安卓靠 `adb -s <serial>`; iOS 靠 `xcrun simctl` 的 `booted`
    (或显式传 udid, 见 UDID 环境变量)。折叠屏验收要开**两台**模拟器
    (外屏/内屏) 时, 用 `xcrun simctl list devices booted` 拿 udid 再传进来。

用法 (xcrun 不在 PATH 时用 XCRUN= 指路):
  python screencap.py save  <out.png>                     # 直接存原始 PNG
  python screencap.py crop  <out.png> x0 y0 x1 y1 [scale] # 裁剪 (+整数放大) → PNG
  python screencap.py hash  x0 y0 x1 y1                   # 区域像素哈希 (前后比对)
  python screencap.py map   x0 y0 x1 y1 [cols]            # ASCII 色块图 (客观看颜色)
  python screencap.py size                                # 只打印分辨率

环境变量:
  XCRUN   xcrun 可执行文件路径 (默认 "xcrun", 走 PATH)
  UDID    模拟器 udid (默认 "booted")
"""
import hashlib
import os
import pathlib
import struct
import subprocess
import sys
import zlib


def _xcrun():
    return os.environ.get("XCRUN", "xcrun")


def _udid():
    return os.environ.get("UDID", "booted")


def capture():
    """跑 simctl 截屏, 返回 (w, h, rgba_bytes)。"""
    out = subprocess.run(
        [_xcrun(), "simctl", "io", _udid(), "screenshot", "--type=png", "-"],
        capture_output=True,
    )
    b = out.stdout
    if not b.startswith(b"\x89PNG\r\n\x1a\n"):
        msg = out.stderr.decode("utf-8", "replace").strip()
        raise SystemExit(
            "screenshot 没有输出 PNG (模拟器没启动? 先 `xcrun simctl list devices booted`)\n"
            + (msg or "(无 stderr)")
        )
    return decode_png(b)


# ── 迷你 PNG 解码 (只认截图会给的形状: 8 位, color type 0/2/4/6, filter 0..4) ──


def _paeth(a, b, c):
    p = a + b - c
    pa, pb, pc = abs(p - a), abs(p - b), abs(p - c)
    if pa <= pb and pa <= pc:
        return a
    if pb <= pc:
        return b
    return c


def decode_png(b):
    off = 8
    w = h = bitdepth = colortype = None
    idat = bytearray()
    while off + 8 <= len(b):
        (length,) = struct.unpack_from(">I", b, off)
        tag = b[off + 4:off + 8]
        data = b[off + 8:off + 8 + length]
        off += 12 + length
        if tag == b"IHDR":
            w, h, bitdepth, colortype, comp, filt, interlace = struct.unpack(">IIBBBBB", data)
            if bitdepth != 8 or interlace != 0:
                raise SystemExit(f"只支持 8 位非隔行 PNG (本图 depth={bitdepth} interlace={interlace})")
            if colortype not in (0, 2, 4, 6):
                raise SystemExit(f"只支持灰度/真彩/带 alpha 的 PNG (本图 colorType={colortype})")
        elif tag == b"IDAT":
            idat += data
        elif tag == b"IEND":
            break
    if w is None:
        raise SystemExit("PNG 里没有 IHDR")

    ch = {0: 1, 2: 3, 4: 2, 6: 4}[colortype]     # 每像素通道数
    stride = w * ch
    raw = zlib.decompress(bytes(idat))

    # 反 filter: 每行是 1 字节 filter + stride 字节数据
    prev = bytearray(stride)
    lines = []
    pos = 0
    for _ in range(h):
        f = raw[pos]
        pos += 1
        cur = bytearray(raw[pos:pos + stride])
        pos += stride
        if f == 1:      # Sub
            for i in range(ch, stride):
                cur[i] = (cur[i] + cur[i - ch]) & 0xFF
        elif f == 2:    # Up
            for i in range(stride):
                cur[i] = (cur[i] + prev[i]) & 0xFF
        elif f == 3:    # Average
            for i in range(stride):
                a = cur[i - ch] if i >= ch else 0
                cur[i] = (cur[i] + ((a + prev[i]) >> 1)) & 0xFF
        elif f == 4:    # Paeth
            for i in range(stride):
                a = cur[i - ch] if i >= ch else 0
                c = prev[i - ch] if i >= ch else 0
                cur[i] = (cur[i] + _paeth(a, prev[i], c)) & 0xFF
        elif f != 0:
            raise SystemExit(f"不认识的 PNG filter: {f}")
        lines.append(cur)
        prev = cur

    # 统一成 RGBA
    rgba = bytearray(w * h * 4)
    for y, line in enumerate(lines):
        base = y * w * 4
        if colortype == 6:
            rgba[base:base + w * 4] = line
        elif colortype == 2:
            for x in range(w):
                s, d = x * 3, base + x * 4
                rgba[d] = line[s]; rgba[d + 1] = line[s + 1]
                rgba[d + 2] = line[s + 2]; rgba[d + 3] = 255
        elif colortype == 0:
            for x in range(w):
                d = base + x * 4
                v = line[x]
                rgba[d] = v; rgba[d + 1] = v; rgba[d + 2] = v; rgba[d + 3] = 255
        else:  # colortype == 4 (灰度 + alpha)
            for x in range(w):
                s, d = x * 2, base + x * 4
                v = line[s]
                rgba[d] = v; rgba[d + 1] = v; rgba[d + 2] = v; rgba[d + 3] = line[s + 1]
    return w, h, bytes(rgba)


# ── 与安卓版逐字同源的三个取证动作 ──


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
    if len(sys.argv) < 2:
        raise SystemExit(__doc__)
    mode = sys.argv[1]
    w, h, pix = capture()
    if mode == "save":
        # 已经是 PNG, 直接落盘 (省一次重新编码); 同时打印分辨率
        out = subprocess.run(
            [_xcrun(), "simctl", "io", _udid(), "screenshot", sys.argv[2]],
            capture_output=True,
        )
        if out.returncode != 0:
            raise SystemExit(out.stderr.decode("utf-8", "replace"))
    elif mode == "size":
        pass
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
