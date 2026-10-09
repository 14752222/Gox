#!/bin/sh
# fullab 共享锁的双向自检 —— 看板 rsx0nn 的验收要求:
#   「陈旧锁能识别 + 新锁不误回收」双向证据 + 可复现脚本。
#
# 运行: sh scripts/fullab-lock-selftest.sh
# 退出码 0 = 全部通过。

set -u
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
. "$SCRIPT_DIR/fullab-lock.sh"

TMPROOT="${TMPDIR:-/tmp}/fullab-lock-selftest.$$"
mkdir -p "$TMPROOT"
trap 'rm -rf "$TMPROOT"' EXIT

PASS=0
FAIL=0
CURRENT=

ok()   { PASS=$((PASS + 1)); printf '  \033[32mPASS\033[0m %s\n' "$1"; }
bad()  { FAIL=$((FAIL + 1)); printf '  \033[31mFAIL\033[0m %s\n' "$1"; }
check(){ if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (期望 [$3] 实得 [$2])"; fi; }
head2(){ printf '\n\033[1m%s\033[0m\n' "$1"; CURRENT="$1"; }

# ---------------------------------------------------------------- 辅助
# 造一个「已死」的 pid
dead_pid() {
	sleep 0.1 &
	p=$!
	wait "$p" 2>/dev/null
	echo "$p"
}

# 用 1 秒心跳模拟一个活着的持有者; $1 = 锁目录, $2 = 持有者 pid
spawn_holder() {
	d="$1"
	# 注意: 后台任务必须重定向 stdout。本函数是用 hbp="$(spawn_holder ...)" 调用的,
	# 命令替换会一直等到 stdout 写端全部关闭; 若后台心跳继承了这个管道, 调用将永不返回。
	( while :; do
		date +%s > "$d/heartbeat" 2>/dev/null || exit 0
		sleep 1 || exit 0
	done ) >/dev/null 2>&1 &
	echo $!
}

# ---------------------------------------------------------------- 0. 证伪旧根因
head2 "0. 对照实验: 旧根因描述站不住 (rsx0nn 已证伪, 此处复现留档)"
D="$TMPROOT/depth"; mkdir -p "$D/a" "$D/b"
touch -d "30 minutes ago" "$D/a"
find "$D/a" -maxdepth 0 -mmin +12 > "$D/r0" 2>/dev/null
find "$D/a" -maxdepth 1 -mmin +12 > "$D/r1" 2>/dev/null
find "$D/a"               -mmin +12 > "$D/rn" 2>/dev/null
check "-maxdepth 0 命中空目录" "$(cat "$D/r0")" "$D/a"
check "-maxdepth 1 命中空目录 (故不是病灶)" "$(cat "$D/r1")" "$D/a"
check "不给 maxdepth 也命中" "$(cat "$D/rn")" "$D/a"
mkdir -p "$D/fresh"
check "新锁不误命中" "$(find "$D/fresh" -maxdepth 1 -mmin +12 2>/dev/null)" ""
echo "  ⇒ find 起始点自身总是参与匹配, -maxdepth 0/1/不给在空目录上等价。"
echo "  ⇒ 「-maxdepth 1 只看目录内条目 ⇒ 空目录永远匹配不上」是错的。"

# ---------------------------------------------------------------- 1. 旧判据为何失灵
head2 "1. 旧判据 (目录 mtime) 为何失灵 —— mtime 可被无关操作扰动"
D="$TMPROOT/mtime"; mkdir -p "$D"
touch -d "1 hour ago" "$D"
before="$(find "$D" -maxdepth 1 -mmin +12 2>/dev/null)"
echo 'pid=999999' > "$D/owner.info"          # 新建目录项 -> 刷新目录 mtime
after="$(find "$D" -maxdepth 1 -mmin +12 2>/dev/null)"
check "陈旧目录判据命中" "$before" "$D"
check "写入 owner.info 后判据失灵 (僵锁伪装成活锁)" "$after" ""
echo "  ⇒ 目录 mtime 只在目录项增删时变化, 是个可被无关操作刷新的信号。"

# ---------------------------------------------------------------- 2. 陈旧锁能识别
head2 "2. 正向: 陈旧僵锁必须能被识别并回收"
D="$TMPROOT/stale"; mkdir -p "$D"
dp="$(dead_pid)"
printf 'pid=%s\nhost=%s\n' "$dp" "$(uname -n)" > "$D/owner.info"
echo 0 > "$D/heartbeat"
touch -d "1 hour ago" "$D/heartbeat"
FULLAB_QUIET=1
if fullab_lock_acquire "$D" 900 5; then
	ok "僵锁被回收 (持有者已死 + 心跳停止)"
	if [ -f "$D/owner.info" ] && [ "$(sed -n 's/^pid=//p' "$D/owner.info")" = "$$" ]; then
		ok "回收后 owner.info 已改写为本进程"
	else
		bad "回收后 owner.info 未改写"
	fi
	fullab_lock_release
	check "释放后锁目录被清除" "$([ -d "$D" ] && echo yes || echo no)" "no"
else
	bad "僵锁未被回收 (会永久自旋)"
fi

# ---------------------------------------------------------------- 3. 新锁不误回收
head2 "3. 反向: 新锁 / 活锁绝不许误回收"
D="$TMPROOT/live"; mkdir -p "$D"
sleep 30 &
holder=$!
printf 'pid=%s\nhost=%s\n' "$holder" "$(uname -n)" > "$D/owner.info"
hbp="$(spawn_holder "$D")"
sleep 2   # 让心跳跑一会儿
cp "$D/owner.info" "$D/owner.info.bak"
if fullab_lock_acquire "$D" 3 4; then
	bad "活锁被误回收了 (⇒ 两个 fullab 会并发跑, 结果互相污染)"
	fullab_lock_release
else
	ok "活锁未被误回收 (心跳新鲜 + 进程存活)"
fi
check "owner.info 未被改写" "$(cat "$D/owner.info")" "$(cat "$D/owner.info.bak")"
kill "$hbp" 2>/dev/null; kill "$holder" 2>/dev/null; wait 2>/dev/null

# ---------------------------------------------------------------- 4. 慢锁不误回收
head2 "4. 反向: 心跳已过期但持有者还活着 ⇒ 只算「慢」, 绝不许回收"
D="$TMPROOT/slow"; mkdir -p "$D"
sleep 30 &
holder=$!
printf 'pid=%s\nhost=%s\n' "$holder" "$(uname -n)" > "$D/owner.info"
# 刻意**不**起心跳刷新器, 并直接把心跳时间戳推到一小时前:
# 这样第一道闸 (心跳新鲜度) 会被突破, 判定完全落在第二道闸 (进程存活) 上。
echo 0 > "$D/heartbeat"
touch -d "1 hour ago" "$D/heartbeat"
if fullab_lock_acquire "$D" 3 4; then
	bad "慢锁被误回收 (心跳旧但人还活着) ⇒ 两个 fullab 会并发跑"
	fullab_lock_release
else
	ok "慢锁未被回收 (进程存活 ⇒ 只是慢, 不是僵)"
fi
kill "$holder" 2>/dev/null; wait 2>/dev/null
# 持有者一死, 同一把锁就该能被回收了 —— 证明上面「不回收」是判据生效, 不是卡死
if fullab_lock_acquire "$D" 3 4; then
	ok "同一个 D 持有者死后立即转为可回收"
	fullab_lock_release
else
	bad "持有者死后仍回收不掉 (⇒ 会永久自旋)"
fi

# ---------------------------------------------------------------- 5. 进程死了心跳却新鲜
head2 "5. 反向: 持有者刚死、心跳尚新鲜 ⇒ 不回收 (避免与退出中的持有者抢跑)"
D="$TMPROOT/justdied"; mkdir -p "$D"
dp="$(dead_pid)"
printf 'pid=%s\nhost=%s\n' "$dp" "$(uname -n)" > "$D/owner.info"
date +%s > "$D/heartbeat"
if fullab_lock_acquire "$D" 900 3; then
	bad "刚退出的持有者被抢跑"
	fullab_lock_release
else
	ok "心跳新鲜 ⇒ 即使进程已死也先等 (等心跳自然过期)"
fi

# ---------------------------------------------------------------- 6. 遗留垃圾目录
head2 "6. 没有 owner.info 的遗留垃圾目录 ⇒ 可回收"
D="$TMPROOT/orphan"; mkdir -p "$D"
touch -d "1 hour ago" "$D"
if fullab_lock_acquire "$D" 900 5; then
	ok "无人登记的遗留目录被回收"
	fullab_lock_release
else
	bad "遗留目录没被回收"
fi

# ---------------------------------------------------------------- 7. 互斥
head2 "7. 并发互斥: 同一时刻只有一个持有者"
D="$TMPROOT/mutex"
if fullab_lock_acquire "$D" 900 5; then
	ok "主进程拿到锁"
	( . "$SCRIPT_DIR/fullab-lock.sh"; FULLAB_QUIET=1
	  if fullab_lock_acquire "$D" 900 3; then
	      echo yes > "$TMPROOT/second"
	      fullab_lock_release
	  else
	      echo no > "$TMPROOT/second"
	  fi )
	check "第二个争用者拿不到锁" "$(cat "$TMPROOT/second")" "no"
	fullab_lock_release
	check "释放后可再次获取" "$(FULLAB_QUIET=1 fullab_lock_acquire "$D" 900 3 && echo yes || echo no)" "yes"
	fullab_lock_release
else
	bad "主进程没拿到锁"
fi

# ---------------------------------------------------------------- 8. 心跳与目录 mtime 解耦
head2 "8. 心跳写入不刷新目录 mtime (旧判据踩的坑, 本实现已绕开)"
D="$TMPROOT/decouple"
# 这里刻意不 mkdir —— 目录要由 acquire 自己建, 否则会被判成「已被持有」
if fullab_lock_acquire "$D" 900 5; then
	touch -d "1 hour ago" "$D"        # 把目录 mtime 推到一小时前
	before="$(_fullab_mtime "$D")"
	sleep 1
	FULLAB_HB_PID=; _fullab_now > "$D/heartbeat"
	after="$(_fullab_mtime "$D")"
	check "写心跳不刷新目录 mtime" "$after" "$before"
	check "心跳文件本身是新的" "$([ "$(_fullab_now)" -ge "$(_fullab_mtime "$D/heartbeat")" ] && echo yes || echo no)" "yes"
	fullab_lock_release
else
	bad "拿不到锁, 无法测解耦"
fi

# ---------------------------------------------------------------- 汇总
printf '\n\033[1m==== 汇总 ====\033[0m\n'
printf 'PASS %d   FAIL %d\n' "$PASS" "$FAIL"
if [ "$FAIL" -gt 0 ]; then
	printf '\033[31m自检未通过\033[0m\n'
	exit 1
fi
printf '\033[32m自检全部通过 —— 双向证据齐备\033[0m\n'
exit 0
