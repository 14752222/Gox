#!/usr/bin/env bash
# macOS 签名 + 公证 —— codesign → notarytool → stapler 三步，一次跑完。
#
# 为什么必须三步：Gatekeeper 只认「Developer ID 签名 + 公证票据」两件事同时成立。
#   只有签名 → 用户看到“无法验证开发者”；只有公证不可能（公证的输入必须先签名）；
#   少 stapler 只是不能离线验证，所以本脚本把它降级成「能 staple 就 staple」。
#
# 一个必须写明的现实约束（也是设计上唯一的妥协点）：
#   **扁平 Mach-O 二进制无法 staple** —— 票据要落在 bundle 的 Contents/ 里，
#   单文件没有地方放，硬 staple 会得到一句没有任何信息量的 `Error 73`。
#   所以本脚本按产物形态分流：.app / .dmg / .pkg 正常 staple，扁平二进制只
#   codesign + notarize（Gatekeeper 首次运行时联网验票，功能上不缺，只是不能离线）。
#   本仓库当前发布的 darwin 产物就是扁平的 gox 二进制，因此走的是后者。
#   将来 `gox build macos` 出的 .app 接进流水线时，同一个脚本直接就能 staple。
#
# 环境变量：
#   MACOS_CERT_P12          Developer ID Application 的 p12 路径
#   MACOS_CERT_P12_BASE64   同上，p12 的 base64（CI 走这条）
#   MACOS_CERT_PASSWORD     p12 导出密码（无密码可不设）
#   MACOS_SIGNING_IDENTITY  可选；不给就从 keychain 里取第一个 Developer ID Application
#   APPLE_API_KEY           App Store Connect API 密钥的 Key ID（与 TestFlight 同一套账号）
#   APPLE_API_ISSUER        Issuer ID
#   APPLE_API_KEY_FILE      .p8 私钥路径
#   APPLE_API_KEY_CONTENT   .p8 的 base64 或 PEM 原文（CI 走这条）
#   API 三件套齐备才做公证；缺任一 → 只 codesign，并在日志里写明“未公证”。
#
# base64 形态的凭证在**脚本内**落盘成 600 的临时文件、退出即删：secret 变文件的
#   动作集中在这一处，YAML 里就不用每个作业各写一遍解码与清理。
#
# 用法：
#   MACOS_CERT_P12=/tmp/gox.p12 bash scripts/sign-macos.sh dist/gox-darwin-arm64 [...]
set -euo pipefail

if [ "$#" -lt 1 ]; then
  echo "usage: MACOS_CERT_P12=<cert.p12> bash scripts/sign-macos.sh <产物> [...]" >&2
  exit 2
fi

for tool in codesign security xcrun; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "error: 缺少 $tool —— 本脚本只能在 macOS 上跑（CI 里对应 runs-on: macos-latest）" >&2
    exit 1
  }
done

MAT_DIR=""
materialize() {
  # $1 = 变量名前缀（MACOS_CERT_P12 / APPLE_API_KEY_FILE），$2 = 落盘文件名
  local path_var="$1" b64_var="$2" name="$3" path="${!1:-}" b64="${!2:-}"
  if [ -n "$path" ] && [ -f "$path" ]; then printf '%s' "$path"; return 0; fi
  [ -n "$b64" ] || { printf ''; return 0; }
  if [ -z "$MAT_DIR" ]; then
    MAT_DIR="$(mktemp -d)"; chmod 700 "$MAT_DIR"
  fi
  # .p8 允许直接贴 PEM 原文（复制时少一步编码，也少一次出错机会）
  case "$b64" in
    *"BEGIN PRIVATE KEY"*|*"BEGIN EC PRIVATE KEY"*|*"BEGIN PRIVATE"*)
      printf '%s\n' "$b64" > "$MAT_DIR/$name" ;;
    *) printf '%s' "$b64" | base64 -d > "$MAT_DIR/$name" ;;
  esac
  chmod 600 "$MAT_DIR/$name"
  printf '%s' "$MAT_DIR/$name"
}

CERT="$(materialize MACOS_CERT_P12 MACOS_CERT_P12_BASE64 cert.p12)"
KEYFILE="$(materialize APPLE_API_KEY_FILE APPLE_API_KEY_CONTENT AuthKey.p8)"
if [ -z "$CERT" ]; then
  echo "error: 需要 MACOS_CERT_P12（路径）或 MACOS_CERT_P12_BASE64（base64），当前都没给" >&2
  exit 1
fi
PW="${MACOS_CERT_PASSWORD:-}"

# ── 临时 keychain ─────────────────────────────────────────────────────────────
# 为什么不用 login.keychain：runner 是一次性的，往里灌证书既污染环境也留后患；
# 独立 keychain 用完即删，且 `security default-keychain` 保证 codesign 只看见
# 我们要它看见的那张证书（避免 runner 上恰好有别的身份导致签错）。
KEYCHAIN="${MACOS_KEYCHAIN:-${RUNNER_TEMP:-/tmp}/gox-sign.keychain-db}"
rm -f "$KEYCHAIN"
cleanup() {
  security delete-keychain "$KEYCHAIN" >/dev/null 2>&1 || true
  [ -n "$MAT_DIR" ] && rm -rf "$MAT_DIR"
  return 0
}
trap cleanup EXIT

security create-keychain -p "$PW" "$KEYCHAIN"
security default-keychain -s "$KEYCHAIN"
security unlock-keychain -p "$PW" "$KEYCHAIN"
# -lut 21600：解锁状态保持 6 小时，避免长作业跑到一半 keychain 自己锁上
security set-keychain-settings -lut 21600 "$KEYCHAIN"
security import "$CERT" -k "$KEYCHAIN" -P "$PW" -T /usr/bin/codesign -T /usr/bin/security
# 分区列表不设会弹 GUI 授权框（CI 里没人点，表现为 codesign 静默挂住/失败）
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$PW" "$KEYCHAIN"

IDENTITY="${MACOS_SIGNING_IDENTITY:-}"
if [ -z "$IDENTITY" ]; then
  IDENTITY=$(security find-identity -v -p codesigning "$KEYCHAIN" \
    | grep -m1 'Developer ID Application' | sed -E 's/.*"([^"]+)".*/\1/')
fi
if [ -z "$IDENTITY" ]; then
  echo "error: keychain 里没有 Developer ID Application 身份；证据：" >&2
  security find-identity -v -p codesigning "$KEYCHAIN" >&2 || true
  exit 1
fi
echo "==> 签名身份: $IDENTITY"

# ── 公证凭证（缺则只签名）────────────────────────────────────────────────────
NOTARIZE=0
if [ -n "${APPLE_API_KEY:-}" ] && [ -n "${APPLE_API_ISSUER:-}" ] && [ -n "$KEYFILE" ]; then
  export APPLE_API_KEY_FILE="$KEYFILE"
  NOTARIZE=1
else
  echo "::notice::APPLE_API_KEY / APPLE_API_ISSUER / APPLE_API_KEY_CONTENT 未配齐 —— 只做 codesign，跳过公证（Gatekeeper 仍会拦）"
fi

notarize_one() {
  # $1 = 待公证产物
  local f="$1" dir base zip out sub_id
  dir="$(cd "$(dirname "$f")" && pwd)"
  base="$(basename "$f")"
  zip="$dir/$base.notarize.zip"
  rm -f "$zip"
  if [ -d "$f" ]; then
    # bundle 用 ditto：zip 会丢掉扩展属性与符号链接，公证端扫出来的签名就是坏的
    ditto -c -k --keepParent "$f" "$zip"
  else
    # 扁平二进制用 zip 就够（签名本身在 Mach-O 里），但要在产物所在目录里打包，
    # 否则 zip 会把绝对路径一起打进去，Apple 端报“找不到可执行文件”
    ( cd "$dir" && zip -q "$zip" "$base" )
  fi

  echo "==> notarytool submit $zip"
  if ! out=$(xcrun notarytool submit "$zip" \
        --key "$APPLE_API_KEY_FILE" --key-id "$APPLE_API_KEY" \
        --issuer "$APPLE_API_ISSUER" --wait 2>&1); then
    echo "$out" >&2
    sub_id=$(printf '%s\n' "$out" | awk '/^[[:space:]]*id:/{print $2; exit}')
    if [ -n "$sub_id" ]; then
      echo "==> notarytool log $sub_id" >&2
      xcrun notarytool log "$sub_id" --key "$APPLE_API_KEY_FILE" \
        --key-id "$APPLE_API_KEY" --issuer "$APPLE_API_ISSUER" 2>&1 | tail -40 >&2 || true
    fi
    echo "error: 公证未通过（$f）—— 上面的 log 是 Apple 给的逐条原因" >&2
    rm -f "$zip"
    return 1
  fi
  echo "$out"
  printf '%s\n' "$out" | grep -q 'status: Accepted' || {
    echo "error: 公证返回了非 Accepted 状态（$f）" >&2
    rm -f "$zip"; return 1
  }
  rm -f "$zip"

  # 扁平二进制没有地方放票据（staple 会报无信息量的 Error 73），只有 bundle 类能 staple
  case "$f" in
    *.app|*.dmg|*.pkg|*.xip)
      echo "==> stapler staple $f"
      xcrun stapler staple "$f" || { echo "error: staple 失败" >&2; return 1; }
      xcrun stapler validate "$f" || true
      ;;
    *)
      echo "::notice::$f 是扁平 Mach-O —— 无法 staple（票据要落在 bundle 目录里，硬 staple 只会得到 Error 73）；已公证，Gatekeeper 首次运行时联网验票"
      ;;
  esac
}

verify_one() {
  # $1 = 产物, $2 = 是否做过公证
  # spctl 是 Gatekeeper 自己的判据（比 codesign --verify 更贴近用户看到的结果），
  # 所以它是本脚本的验收出口 —— 但它依赖 Apple 侧票据，未公证时必然 rejected。
  local f="$1" notarized="$2" i out
  local args=(-a -vv -t exec "$f")
  if [ -d "$f" ]; then args=(-a -vv "$f"); fi

  echo "==> spctl ${args[*]}"
  if [ "$notarized" != 1 ]; then
    # 只签名未公证：spctl 判 rejected 是**预期内**的，别把它算成失败
    spctl "${args[@]}" 2>&1 || echo "::notice::$f 未公证，Gatekeeper 判 rejected 属预期（用户需右键打开或 xattr -d com.apple.quarantine）"
    return 0
  fi
  for i in 1 2 3; do
    if out=$(spctl "${args[@]}" 2>&1) && printf '%s\n' "$out" | grep -q 'accepted'; then
      echo "$out"
      return 0
    fi
    echo "$out"
    # 票据从 Apple 侧传播到全网有几十秒延迟，公证刚 Accepted 就查几乎必然踩到
    if [ "$i" -lt 3 ]; then echo "==> spctl 未接受，15s 后重试（$i/3）"; sleep 15; fi
  done
  echo "error: spctl 三次都未接受 $f（公证已 Accepted，通常只是票据传播慢；也可能是产物类型与 -t exec 不匹配）" >&2
  return 1
}

status=0
for f in "$@"; do
  [ -e "$f" ] || { echo "error: 产物不存在: $f" >&2; status=1; continue; }
  echo "──────── $f ────────"
  # --options runtime（硬运行时）是公证的硬要求，缺了 Apple 直接判 Invalid；
  # --timestamp 让签名在证书过期后仍然有效。
  codesign --force --timestamp --options runtime --sign "$IDENTITY" "$f" || { status=1; continue; }
  codesign --verify --strict --verbose=2 "$f" || { echo "error: codesign 自验签失败: $f" >&2; status=1; continue; }

  n=0
  if [ "$NOTARIZE" = 1 ]; then
    if notarize_one "$f"; then n=1; else status=1; continue; fi
  fi
  verify_one "$f" "$n" || status=1
done

[ "$status" = 0 ] || exit 1
echo "==> macOS 签名/公证完成"
