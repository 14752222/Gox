#!/usr/bin/env bash
# 交叉编译 Gox 全平台二进制到 npm/binaries/，供 npm 包分发。
# 全仓库无 CGO（GUI 后端均为纯 syscall 实现），任意一台装了 Go 的机器
# 都能编出全部目标，无需各平台工具链。
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
OUT="$ROOT/npm/binaries"

command -v go >/dev/null 2>&1 || { echo "error: go not found in PATH" >&2; exit 1; }

rm -rf "$OUT"

# 每项为 GOOS/GOARCH/Node process.arch 三元组（Node 把 amd64 叫 x64）
targets="darwin/amd64/x64 darwin/arm64/arm64 linux/amd64/x64 linux/arm64/arm64 windows/amd64/x64"

for t in $targets; do
  IFS=/ read -r os goarch arch <<< "$t"
  ext=""
  if [ "$os" = "windows" ]; then ext=".exe"; fi
  echo "==> GOOS=$os GOARCH=$goarch"
  GOOS=$os GOARCH=$goarch CGO_ENABLED=0 \
    go build -trimpath -ldflags '-s -w' -o "$OUT/$os-$arch/gox$ext" "$ROOT"
done

# Unix 二进制需要可执行位（发布必须在 Linux/macOS 上进行，Windows 打包不保留权限位）
chmod +x "$OUT"/*/gox 2>/dev/null || true

echo
echo "build output:"
ls -lh "$OUT"/*/*
