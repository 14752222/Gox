#!/usr/bin/env bash
# 构建 Gox wasm Playground 探针产物（rzTQml T14）。
#
#   bash scripts/build-playground.sh
#
# 产物落在 playground/：
#   gox.wasm      —— cmd/goxwasm 编出的引擎（-s -w strip）
#   wasm_exec.js  —— 从 GOROOT 拷贝，版本与当前 Go 严格匹配（不能跨版本复用）
#
# 本地起服务验证：
#   python3 -m http.server -d playground 8080
# 无浏览器时可用 Node 宿主做执行验证：
#   node cmd/goxwasm/run_node.js playground/gox.wasm
set -euo pipefail
cd "$(dirname "$0")/.."

OUT=playground
mkdir -p "$OUT"

echo ">>> GOOS=js GOARCH=wasm go build ./cmd/goxwasm"
GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o "$OUT/gox.wasm" ./cmd/goxwasm

cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$OUT/wasm_exec.js"

ls -l "$OUT/gox.wasm" "$OUT/wasm_exec.js"
echo ">>> 完成。起服务: python3 -m http.server -d $OUT 8080"
