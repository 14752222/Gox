#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# 自动发版：小版本号（补丁位）自增 —— 供夜间定时任务在每完成一个任务后调用。
#
#   v0.15.0 → v0.15.1 → v0.15.2 → …（同一晚多个任务依次完成则依次递增）
#
# 版本号规则：
#   取 Gox 仓库最新的 vMAJOR.MINOR.PATCH 标签，把 PATCH 位 +1 作为新版本；
#   MAJOR / MINOR 不动（它们仍走人工发版）。若目标 tag 已被并发任务抢先打出，
#   自动再 +1 重试，直到拿到一个不存在的 tag 为止 —— 所以同晚并发也不会撞号。
#
# 三处真相点必须同步（release.yml 的 decide 闸门会逐一核对，不一致直接红）：
#   1. 本仓 cmd/gox/main.go 的 const version
#   2. gox-npm 仓 package.json（主包 @goxjs/goxjs）
#   3. gox-npm 仓 packages/*/package.json（移动端平台子包，须同号）
#
# 动作序列（全部走 GitHub API，本地不需要克隆仓库）：
#   ① 算出新版本号（最新 v*.*.* 的补丁位 +1）
#   ② 改 main.go 并提交到本仓 main
#   ③ 改 gox-npm 的主包 + 子包版本并提交到其默认分支
#   ④ 推送 tag vX.Y.Z（指向 ② 的提交）→ 触发 release.yml：交叉编译、
#      npm 发布（OIDC）、建 GitHub Release
#
# 用法：
#   GH_TOKEN=ghp_xxx bash scripts/auto-release.sh [--note "任务说明"]
#
#   --note  仅用于 commit message 里的任务标识，可省略。
#
# 认证：GH_TOKEN 必须能写本仓与 gox-npm 仓（contents:write）。
#   CI 里若配了 RELEASE_PAT secret 则优先用它（GITHUB_TOKEN 写不了 gox-npm）。
#
# 幂等与失败语义：任一步失败即退出非零，已改的版本号文件不会自动回滚 ——
#   重跑本脚本即可：它会基于"当前最新 tag"重新算号，把遗漏的点补齐再推 tag。
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

REPO="${GITHUB_REPOSITORY:-14752222/Gox}"
NPM_REPO="${GOX_NPM_REPOSITORY:-14752222/gox-npm}"
NPM_BRANCH="${GOX_NPM_BRANCH:-main}"
RELEASE_BRANCH="${AUTO_RELEASE_BRANCH:-main}"

TOKEN="${RELEASE_PAT:-${GH_TOKEN:-${GITHUB_TOKEN:-}}}"
if [ -z "$TOKEN" ]; then
  echo "::error::缺少令牌：请设置 GH_TOKEN（需对本仓与 gox-npm 均有 contents:write）" >&2
  exit 1
fi

API="https://api.github.com"
H_AUTH="Authorization: Bearer $TOKEN"
H_ACCEPT="Accept: application/vnd.github+json"
api() { curl -sf -H "$H_AUTH" -H "$H_ACCEPT" "$@"; }

NOTE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --note) NOTE="${2:-}"; shift 2 ;;
    *) echo "未知参数：$1" >&2; exit 2 ;;
  esac
done

ORIGIN_URL="https://x-access-token:${TOKEN}@github.com/${REPO}.git"

# ── ① 解析下一个版本号：最新 v*.*.* 标签的补丁位 +1 ──────────────────────────
latest_tag() {
  # 只认 x.y.z 三段纯数字 tag（排除 nightly-* 之类），按版本序取最大
  git ls-remote --tags "$ORIGIN_URL" 'refs/tags/v*' \
    | awk -F/ '{print $NF}' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -1
}

tag_exists() {
  git ls-remote --exit-code --tags "$ORIGIN_URL" "refs/tags/$1" >/dev/null 2>&1
}

next_tag() {
  local latest major minor patch candidate
  latest="${1:-v0.0.0}"
  echo "::notice::当前最新标签：${latest}" >&2
  major="${latest#v}"; major="${major%%.*}"
  minor="$(echo "${latest#v}" | cut -d. -f2)"
  patch="$(echo "${latest#v}" | cut -d. -f3)"
  # 补丁位从当前值 +1 向上找第一个不存在的 tag（并发安全：撞号自动让位）
  while :; do
    patch=$((patch + 1))
    candidate="v${major}.${minor}.${patch}"
    if tag_exists "$candidate"; then
      echo "::warning::tag ${candidate} 已存在（被并发任务抢先？），继续 +1" >&2
      continue
    fi
    printf '%s' "$candidate"
    return 0
  done
}

# 取 raw 文件内容，带「元数据污染」防御 —— contents API 偶发无视 raw Accept 头、
#   把 JSON 元数据（含 sha/encoding/content 键）整个当响应吐回来。2026-10-29 实测：
#   这份 JSON 被 bump_content 的 JSON 分支改了 "version" 后写进 main.go，main 直接
#   构建红、当晚 tag 的 release 构建失败。防御两层：识别出元数据就重试，3 次仍
#   取不到 raw 就放弃写入 —— 宁可发版失败，也绝不把元数据写进源文件。
fetch_raw() { # $1=repo $2=path $3=branch → stdout
  local repo="$1" path="$2" branch="$3" try raw
  for try in 1 2 3; do
    raw=$(api "$API/repos/${repo}/contents/${path}?ref=${branch}" -H "Accept: application/vnd.github.raw") || true
    if printf '%s' "$raw" | head -c 2000 | grep -q '"encoding"'; then
      echo "::warning::${repo}/${path} 第 ${try} 次取到 JSON 元数据而非文件内容（API raw 头被忽略），重试" >&2
      sleep 2
      continue
    fi
    if [ -z "$raw" ]; then
      echo "::warning::${repo}/${path} 第 ${try} 次取到空内容，重试" >&2
      sleep 2
      continue
    fi
    printf '%s' "$raw"
    return 0
  done
  echo "::error::${repo}/${path} 连续 3 次取不到 raw 内容 —— 放弃写入，禁止把 JSON 元数据写进源文件" >&2
  return 1
}

# package.json / main.go 的版本号改写（stdin → stdout）。
#   按文件名分流，不做内容嗅探：内容以 { 开头不再等于 package.json ——
#   元数据 JSON 也是 { 开头（这正是 2026-10-29 事故的成因）。
bump_content() { # $1: 新版本(无v)  $2: 提示用途的路径名
  python3 -c '
import sys, json, re
ver, path = sys.argv[1], sys.argv[2]
raw = sys.stdin.read()
if path.endswith("package.json"):
    data = json.loads(raw)
    if "encoding" in data or "content" in data:
        sys.exit(f"{path} 取到的是 contents API 元数据 JSON，不是文件内容 —— 禁止写入")
    data["version"] = ver
    print(json.dumps(data, ensure_ascii=False, indent=2) + "\n")
else:
    out, n = re.subn(r"(const version = \")[^\"]+(\")", r"\g<1>" + ver + r"\g<2>", raw, count=1)
    if n != 1:
        sys.exit(f"{path} 里找不到 const version —— 八成是取回了元数据/错误内容，禁止写入")
    sys.stdout.write(out)
' "$1" "$2"
}

update_file() { # $1=repo $2=path $3=branch $4=ver $5=commit_msg
  local repo="$1" path="$2" branch="$3" ver="$4" msg="$5"
  local meta sha content payload
  meta=$(api "$API/repos/${repo}/contents/${path}?ref=${branch}")
  sha=$(printf '%s' "$meta" | jq -r '.sha')
  content=$(fetch_raw "$repo" "$path" "$branch" | bump_content "$ver" "$path")
  payload=$(jq -n --arg m "$msg" --arg s "$sha" \
    --arg c "$(printf '%s' "$content" | base64 -w0)" --arg b "$branch" \
    '{message:$m, content:$c, sha:$s, branch:$b}')
  api -X PUT "$API/repos/${repo}/contents/${path}" -d "$payload" >/dev/null
  echo "::notice::已更新 ${repo}/${path} → ${ver}" >&2
}

main() {
  local latest new_tag new_ver msg payload commit_sha
  latest=$(latest_tag || true)
  # 护栏一：拿不到最新 tag（网络超时 / ls-remote 失败）绝不能从 v0.0.0 起算，
  # 否则会误发 v0.0.1 —— 必须直接中止，重跑即可。
  if [ -z "${latest}" ]; then
    echo "::error::无法从远端解析最新 v*.*.* 标签（git ls-remote 超时或失败）。拒绝发版，请排查网络后重跑。" >&2
    exit 1
  fi
  new_tag=$(next_tag "${latest}")
  new_ver="${new_tag#v}"
  echo "::notice::发版版本：${new_ver}（补丁位自增）" >&2

  # 护栏二：新版本号必须 ≥ main.go 里声明的当前版本，否则说明 tag 解析出了问题
  # （比如远端 tag 列表不全），把版本号往回写会造成回退事故 —— 中止。
  local cur_ver
  cur_ver=$(git ls-remote "$ORIGIN_URL" "refs/heads/${RELEASE_BRANCH}" >/dev/null 2>&1 \
    && fetch_raw "$REPO" "cmd/gox/main.go" "${RELEASE_BRANCH}" \
    | grep -m1 -oP 'const version = "\K[0-9]+\.[0-9]+\.[0-9]+' || true)
  if [ -n "$cur_ver" ] && [ "$(printf '%s\n%s\n' "$new_ver" "$cur_ver" | sort -V | head -1)" != "$cur_ver" ]; then
    echo "::error::新版本 ${new_ver} 低于 main.go 当前版本 ${cur_ver} —— tag 解析异常，拒绝发版。" >&2
    exit 1
  fi

  # ── ② 本仓 main.go ──
  msg="chore(release): 自动发版 v${new_ver}${NOTE:+ — ${NOTE}}"
  main_sha=$(api "$API/repos/${REPO}/contents/cmd/gox/main.go?ref=${RELEASE_BRANCH}" | jq -r '.sha')
  main_content=$(fetch_raw "$REPO" "cmd/gox/main.go" "${RELEASE_BRANCH}" \
    | bump_content "$new_ver" "cmd/gox/main.go")
  payload=$(jq -n --arg m "$msg" --arg s "$main_sha" \
    --arg c "$(printf '%s' "$main_content" | base64 -w0)" --arg b "$RELEASE_BRANCH" \
    '{message:$m, content:$c, sha:$s, branch:$b}')
  commit_sha=$(api -X PUT "$API/repos/${REPO}/contents/cmd/gox/main.go" -d "$payload" | jq -r '.commit.sha')
  echo "::notice::main.go → ${new_ver}，commit ${commit_sha}" >&2

  # ── ③ gox-npm：主包 + 全部子包 ──
  msg="chore(release): bump version to ${new_ver}"
  update_file "$NPM_REPO" "package.json" "$NPM_BRANCH" "$new_ver" "$msg"
  for d in $(api "$API/repos/${NPM_REPO}/contents/packages?ref=${NPM_BRANCH}" | jq -r '.[].name'); do
    update_file "$NPM_REPO" "packages/${d}/package.json" "$NPM_BRANCH" "$new_ver" "$msg"
  done

  # ── ④ 推 tag → 触发 release.yml ──
  api -X POST "$API/repos/${REPO}/git/refs" \
    -d "$(jq -n --arg ref "refs/tags/${new_tag}" --arg sha "$commit_sha" '{ref:$ref, sha:$sha}')" >/dev/null
  echo "::notice::已推送标签 ${new_tag}（指向 ${commit_sha}）—— release.yml 将完成构建、npm 发布与 GitHub Release" >&2
  echo "RELEASE_VERSION=${new_ver}"
  echo "RELEASE_TAG=${new_tag}"
}

main
