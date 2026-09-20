#!/usr/bin/env bash
# 交叉编译 Gox 全平台二进制，供 npm 包分发。
# 全仓库无 CGO（GUI 后端均为纯 syscall 实现），任意一台装了 Go 的机器
# 都能编出全部目标，无需各平台工具链 —— 所以 CI 上也不需要 matrix。
#
# 产物落在 dist/npm/binaries/（dist/ 已在 .gitignore 中，不进仓库）。
# 加 --into-package 时会再复制一份到 npm/binaries/ —— 那是 npm publish 要求
# 的包内位置，所以它只要在打包那一刻存在就够：
#
#     bash scripts/build-npm.sh --into-package && (cd npm && npm publish)
#
# 发布完请删掉它（工作流里有 always() 的清理步骤），别让它常驻。
#
# 为什么不直接常驻在 npm/binaries/：npm 在包目录里找不到 .npmignore 时会回退
# 去读仓库根的 .gitignore，被它忽略的目录即使写在 package.json 的 files 里也
# 进不了包（详见 npm/.npmignore）。所以 .gitignore 里绝不能出现 npm/ 下的路径。
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
# 切到仓库根再用**相对路径**构建：Windows 的 Git Bash 里 $ROOT 是 /f/desktop/go
# 这种 MSYS 路径，直接喂给原生 go.exe 会被当成 import path（malformed import path）。
cd "$ROOT"
OUT="dist/npm/binaries"

command -v go >/dev/null 2>&1 || { echo "error: go not found in PATH" >&2; exit 1; }

rm -rf "$OUT"

# 每项为 GOOS/GOARCH/Node process.arch 三元组（Node 把 amd64 叫 x64）。
# 目录名必须与 npm/bin/gox.js 里 `osName + '-' + process.arch` 的拼法逐字符对应。
targets="darwin/amd64/x64 darwin/arm64/arm64 linux/amd64/x64 linux/arm64/arm64 windows/amd64/x64"

for t in $targets; do
  IFS=/ read -r os goarch arch <<< "$t"
  ext=""
  if [ "$os" = "windows" ]; then ext=".exe"; fi
  echo "==> GOOS=$os GOARCH=$goarch"
  GOOS=$os GOARCH=$goarch CGO_ENABLED=0 \
    go build -trimpath -ldflags '-s -w' -o "$OUT/$os-$arch/gox$ext" .
done

# Unix 二进制需要可执行位（Windows 上打包不保留权限位，所以只在类 Unix 上 chmod）。
chmod +x "$OUT"/*/gox 2>/dev/null || true

# bin/gox.js 是 npm 的 bin 入口，同样要有执行位；在 Windows 上提交时
# git 记录的是 644，所以打包机上补一次。
chmod +x npm/bin/gox.js 2>/dev/null || true

if [ "${1:-}" = "--into-package" ]; then
  rm -rf npm/binaries
  cp -r "$OUT" npm/binaries
  echo "已复制到 npm/binaries/（打包用，发布后请删除）"
fi

echo
echo "build output:"
ls -lh "$OUT"/*/*
