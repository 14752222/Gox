#!/usr/bin/env bash
# 桌面产物签名凭证探测 —— 「缺凭证就跳过」的唯一真源。
#
# 为什么单独一个脚本：release.yml 里有两处 macOS 签名（sign-macos 作业给 npm 包、
# universal 作业给 Release Assets）和一处 Windows 签名，三处都要同样的判空 +
# 同样的跳过说明。散在 YAML 里写三遍，改一处漏两处是迟早的事 —— 而漏掉的那一处
# 恰好就是「凭空多出一个没闸门的 secrets 引用」，是最难在 review 里看出来的坏味道。
#
# 判空为什么在 YAML 侧做（`secrets.X != ''`）而不是把值传进来：GitHub Actions 里
# 未配置的 secret 求值为空串，只有表达式能区分「没配」和「配了空串」；把值传进
# 脚本只会让凭证多一次出现在进程环境里的机会。所以这里只收布尔结论。
#
# 输出：`enabled=true|false` 写进 $GITHUB_OUTPUT（本地跑时只打印，不写文件），
#   调用方拿它做 `if: steps.<id>.outputs.enabled == 'true'` 闸门。
#
# 用法：
#   HAS_WIN_PFX=true  bash scripts/check-signing-creds.sh windows
#   HAS_MACOS_CERT=true HAS_APPLE_API=false bash scripts/check-signing-creds.sh macos
set -euo pipefail

KIND="${1:-}"
emit() {
  # 本地（无 GITHUB_OUTPUT）只回显，CI 里同时进 step outputs
  echo "$1"
  if [ -n "${GITHUB_OUTPUT:-}" ]; then
    echo "$1" >> "$GITHUB_OUTPUT"
  fi
}

case "$KIND" in
  windows)
    if [ "${HAS_WIN_PFX:-false}" != 'true' ]; then
      echo "::notice::未配置 WINDOWS_PFX_BASE64 —— 跳过 Authenticode 签名，windows 产物保持未签名（首次运行会提示“未知发布者”）；不影响发布。要启用：把 base64(pfx) 存进 WINDOWS_PFX_BASE64、证书密码存进 WINDOWS_PFX_PASSWORD（自签证书可用 gox cert windows 生成，但自签无法消除 SmartScreen，只保证完整性）"
      emit "enabled=false"
      exit 0
    fi
    echo "::notice::检测到 WINDOWS_PFX_BASE64，将对 windows 产物做 Authenticode 签名"
    emit "enabled=true"
    ;;
  macos)
    if [ "${HAS_MACOS_CERT:-false}" != 'true' ]; then
      echo "::notice::未配置 MACOS_CERT_P12_BASE64 —— 跳过 macOS 签名与公证，darwin 产物保持未签名（用户需右键打开或 xattr -d com.apple.quarantine）；不影响发布。要启用：base64(Developer ID Application 的 p12) 存进 MACOS_CERT_P12_BASE64、导出密码存进 MACOS_CERT_PASSWORD、公证用的 App Store Connect API 密钥三件套存进 APPLE_API_KEY / APPLE_API_ISSUER / APPLE_API_KEY_CONTENT"
      emit "enabled=false"
      exit 0
    fi
    if [ "${HAS_APPLE_API:-false}" != 'true' ]; then
      echo "::notice::APPLE_API_KEY / APPLE_API_ISSUER / APPLE_API_KEY_CONTENT 缺一项 —— 只做 codesign、跳过公证：产物有完整性签名但 Gatekeeper 仍会拦（缺公证票据）。要完整兑付请把三项配齐（与 TestFlight 上传同一套 App Store Connect API 凭证）"
      emit "enabled=true"
      exit 0
    fi
    echo "::notice::检测到 macOS 签名证书与 App Store Connect API 密钥，将走 codesign → notarytool → stapler 完整链路"
    emit "enabled=true"
    ;;
  *)
    echo "usage: HAS_WIN_PFX=... bash scripts/check-signing-creds.sh windows" >&2
    echo "       HAS_MACOS_CERT=... HAS_APPLE_API=... bash scripts/check-signing-creds.sh macos" >&2
    exit 2
    ;;
esac
