#!/usr/bin/env bash
# Windows Authenticode 签名 —— 给交叉编译出来的 .exe 盖一个可验的戳。
#
# 为什么用 osslsigncode 而不是 signtool / 新加一个 Go 依赖：
#   * 本仓库的 windows 产物是在 **ubuntu runner 上交叉编译**的（build-npm.sh），
#     签名要赶在 npm pack 之前完成，中间再插一个 windows runner 来回传产物，
#     等于为一个盖章动作买一次完整的作业调度；
#   * osslsigncode 是纯 C + OpenSSL，签 PE 与宿主平台无关（electron-builder 就是
#     这么在 Linux 上签 Windows 包的），`apt-get install -y osslsigncode` 一步到位；
#   * 走 golang.org/x/sys/windows 自实现 Authenticode 要自己拼 PKCS#7/SpcIndirectData
#     并新增依赖 —— 与「发版流水线少一个活动部件」的方向相反。
#
# 时间戳（--ts）不是可选项：没有 RFC3161 时间戳的签名在证书过期后即失效，
#   有时间戳则证书过期后签名仍可信 —— 这是「发版产物」和「临时产物」的分界。
#
# 环境变量：
#   GOX_WIN_PFX            必填其一，.pfx/.p12 路径
#   GOX_WIN_PFX_BASE64     必填其一，pfx 的 base64（CI 走这条；脚本内落盘 600，用完即删）
#   GOX_WIN_PFX_PASSWORD   证书密码（无密码可不设）
#   GOX_SIGN_NAME          可选，签名里的描述，默认 Gox
#   GOX_SIGN_URL           可选，签名里的信息 URL，默认项目主页
#   GOX_TIMESTAMP_URL      可选，时间戳服务，默认 DigiCert
#
# 用法：
#   GOX_WIN_PFX=/tmp/gox.pfx bash scripts/sign-windows.sh in.exe out.exe
#   GOX_WIN_PFX=/tmp/gox.pfx bash scripts/sign-windows.sh in.exe        # 就地签名
set -euo pipefail

IN="${1:-}"
OUT="${2:-}"

if [ -z "$IN" ]; then
  echo "usage: GOX_WIN_PFX=<file.pfx> bash scripts/sign-windows.sh <in.exe> [out.exe]" >&2
  exit 2
fi
if [ ! -f "$IN" ]; then
  echo "error: 输入文件不存在: $IN" >&2
  exit 1
fi
# 凭证要么给路径（GOX_WIN_PFX，本地用法），要么给 base64（GOX_WIN_PFX_BASE64，
# CI 用法）。base64 那条路在**脚本内**落盘成 600 的临时文件再删 —— 让 secret
# 只在这一处变成文件，YAML 里就不必各作业各写一遍解码与清理（漏一处就是明文长驻）。
MAT_DIR=""
cleanup() { [ -n "$MAT_DIR" ] && rm -rf "$MAT_DIR"; return 0; }
trap cleanup EXIT

MAT_DIR="$(mktemp -d)"
chmod 700 "$MAT_DIR"
PASS="${GOX_WIN_PFX_PASSWORD:-}"

PFX="${GOX_WIN_PFX:-}"
if [ -z "$PFX" ] && [ -n "${GOX_WIN_PFX_BASE64:-}" ]; then
  printf '%s' "$GOX_WIN_PFX_BASE64" | base64 -d > "$MAT_DIR/gox.pfx"
  chmod 600 "$MAT_DIR/gox.pfx"
  PFX="$MAT_DIR/gox.pfx"
fi
if [ -z "$PFX" ] || [ ! -f "$PFX" ]; then
  echo "error: 需要 GOX_WIN_PFX（路径）或 GOX_WIN_PFX_BASE64（base64），当前都没给" >&2
  exit 1
fi

# 验签要用到的 CA：自签证书（gox cert windows 产的就是这种）过不了系统信任链，
# 直接 `osslsigncode verify` 会报 self-signed certificate 而失败 —— 那样的话
# 「配了自签证书反而发不出包」，正是看板上说的那种越配越坏。把签发者自己的证书
# 从 pfx 里取出来喂给 -CAfile 即可正常验；正规 CA 证书有它也不影响。
CAFILE=""
if command -v openssl >/dev/null 2>&1; then
  # -legacy：OpenSSL 3 默认拒绝 pfx 里的旧算法（RC2/40bit），自签工具常产这种
  if openssl pkcs12 -legacy -in "$PFX" -clcerts -nokeys -passin "pass:$PASS" \
       -out "$MAT_DIR/signer.pem" 2>/dev/null \
     || openssl pkcs12 -in "$PFX" -clcerts -nokeys -passin "pass:$PASS" \
       -out "$MAT_DIR/signer.pem" 2>/dev/null; then
    CAFILE="$MAT_DIR/signer.pem"
  else
    echo "::notice::没能从 pfx 里取出签发者证书，验签将只走系统信任链（自签证书会判失败）"
  fi
fi

if ! command -v osslsigncode >/dev/null 2>&1; then
  echo "error: 找不到 osslsigncode" >&2
  echo "  Ubuntu/Debian: sudo apt-get install -y osslsigncode" >&2
  echo "  macOS:         brew install osslsigncode" >&2
  exit 1
fi

# 1.x 与 2.x 的调用姿势不同（2.x 才有 `sign` 子命令、时间戳开关叫 -ts），
# 镜像换了大版本不该让这条流水线静默签出个错包，所以按版本号分流。
VER_LINE="$(osslsigncode --version 2>&1 | head -1 || true)"
echo "==> $VER_LINE"
if printf '%s' "$VER_LINE" | grep -qE 'osslsigncode[^0-9]*([2-9]|[1-9][0-9])\.'; then
  SUBCMD="sign"; TSFLAG="-ts"
else
  SUBCMD=""; TSFLAG="-t"
fi

NAME="${GOX_SIGN_NAME:-Gox}"
URL="${GOX_SIGN_URL:-https://github.com/14752222/Gox}"
TSA="${GOX_TIMESTAMP_URL:-http://timestamp.digicert.com}"
PASS="${GOX_WIN_PFX_PASSWORD:-}"

# 就地签名时先写临时文件再 mv：osslsigncode 边读边写会截断输入
TMP_OUT=""
if [ -z "$OUT" ]; then
  OUT="$IN"
  TMP_OUT="$IN.signed.$$"
fi
sha256() {
  # Linux 叫 sha256sum、macOS 叫 shasum；这脚本两头都可能跑
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}';
  else shasum -a 256 "$1" | awk '{print $1}'; fi
}

echo "==> 签名前 sha256: $(sha256 "$IN")"
args=(-h sha256 -pkcs12 "$PFX" -pass "$PASS" -n "$NAME" -i "$URL" "$TSFLAG" "$TSA"
      -in "$IN" -out "${TMP_OUT:-$OUT}")
if [ -n "$SUBCMD" ]; then
  args=("$SUBCMD" "${args[@]}")
fi
echo "==> osslsigncode ${args[0]} ... -in $IN -out ${TMP_OUT:-$OUT}"
if ! osslsigncode "${args[@]}"; then
  echo "error: osslsigncode 签名失败（常见原因：pfx 与密码不匹配、时间戳服务不可达）" >&2
  rm -f "$TMP_OUT"
  exit 1
fi
if [ -n "$TMP_OUT" ]; then
  mv "$TMP_OUT" "$OUT"
fi

echo "==> 签名后 sha256: $(sha256 "$OUT")"
# 验签留证据：签名动作本身成功不代表产物可验 —— 而发版流水线里没装 Windows。
# 这条是本机能给的最强证据，Windows 上的权威判据是 `signtool verify /pa <file>`。
echo "==> osslsigncode verify（Linux 侧自验；Windows 上另用 signtool verify /pa）"
vargs=(verify -in "$OUT")
if [ -n "$CAFILE" ]; then vargs+=(-CAfile "$CAFILE"); fi
if ! osslsigncode "${vargs[@]}"; then
  echo "error: 签名后自验签失败 —— 产物不可信，别发出去" >&2
  exit 1
fi
echo "==> 已签名: $OUT"
