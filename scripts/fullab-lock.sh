#!/bin/sh
# fullab 共享锁 —— 用「心跳新鲜度 + 持有者进程存活」双判据取代旧的「目录 mtime 陈旧」单判据。
#
# 背景: 全量 test262 A/B (fullab) 需要一个跨会话的共享锁做串行化。旧实现用
#     find "$LOCK" -maxdepth 1 -mmin +12
# 判陈旧。看板 rsx0nn 已用实验证伪「-maxdepth 1 只看目录内条目」这一说法 ——
# find 的**起始点自身总是参与匹配**(与 -maxdepth 无关), 空目录下 -maxdepth 0/1/不给
# 三者行为等价且都能命中。真正的问题不在 -maxdepth, 而在**判据本身选错了**:
#
#   1. 目录 mtime 只在「目录项增删/重命名」时变化, 追加写文件不动它。
#      于是: mkdir 后 echo > owner.info 会把目录 mtime 刷到当下; 反过来
#      touch 一下目录就能让僵锁永远判不出陈旧。mtime 是**可被无关操作扰动**的信号。
#   2. 更要命的是, mtime 完全不含「持有者还活着吗」这个信息:
#      - 活锁跑满阈值会被**误回收** ⇒ 两个 fullab 并发跑, 结果互相污染;
#      - 僵锁被任何 touch 续命后就再也**回收不掉** ⇒ 后来者永久自旋。
#
# 本实现的判据:
#   * 心跳文件 $LOCK/heartbeat 由持有者后台线程定期写 (用 `>` 截断写已存在的文件,
#     不会再动目录 mtime, 彻底与目录 mtime 解耦);
#   * 陈旧 = 心跳年龄超过阈值 **且** 持有者进程已死 (同机比对 pid, 跨机只能看心跳);
#   * 两个条件缺一不可 —— 只陈旧但进程活着 ⇒ 只是慢, 等; 进程死了但心跳新鲜 ⇒ 刚退出, 等。
#
# 用法 (source, 不要直接执行):
#   . scripts/fullab-lock.sh
#   fullab_lock_acquire /f/tmp/fullab.lockdir || { echo "拿不到锁" >&2; exit 1; }
#   trap fullab_lock_release EXIT INT TERM
#   ... 干活 ...
#
# 可调环境变量 (在 acquire 前设置):
#   FULLAB_STALE_SECS   心跳超过多少秒算陈旧 (默认 900)
#   FULLAB_WAIT_SECS    最多自旋多久放弃          (默认 3600)
#   FULLAB_POLL_SECS    自旋间隔                  (默认 5)
#   FULLAB_HB_INTERVAL  心跳写入间隔              (默认 30)
#
# 双向证据见 scripts/fullab-lock-selftest.sh。

_fullab_now() { date +%s; }

# 取文件/目录 mtime (epoch 秒)。GNU coreutils 与 BSD stat 参数不同, 两边都兜。
_fullab_mtime() {
	stat -c %Y "$1" 2>/dev/null && return 0
	stat -f %m "$1" 2>/dev/null && return 0
	return 1
}

_fullab_pid_alive() {
	[ -n "$1" ] || return 1
	case "$1" in
	'' | *[!0-9]*) return 1 ;;
	esac
	kill -0 "$1" 2>/dev/null
}

# 持有者还活着的证据时间戳。优先级: heartbeat > owner.info > 目录自身。
# (没有 heartbeat 说明持有者是旧版脚本, 退化到 owner.info / 目录 mtime 的老判据。)
_fullab_liveness_ts() {
	d="$FULLAB_LOCK_DIR"
	if [ -f "$d/heartbeat" ]; then
		_fullab_mtime "$d/heartbeat" && return 0
	fi
	if [ -f "$d/owner.info" ]; then
		_fullab_mtime "$d/owner.info" && return 0
	fi
	_fullab_mtime "$d"
}

_fullab_owner_pid() {
	sed -n 's/^pid=//p' "$FULLAB_LOCK_DIR/owner.info" 2>/dev/null | head -1
}

_fullab_owner_host() {
	sed -n 's/^host=//p' "$FULLAB_LOCK_DIR/owner.info" 2>/dev/null | head -1
}

# 判定现有锁是否为可安全回收的僵锁。命中时把年龄写进 FULLAB_STALE_AGE 供日志用。
# 返回 0 = 是僵锁, 1 = 不是 (还在被正常持有)。
_fullab_lock_is_stale() {
	[ -d "$FULLAB_LOCK_DIR" ] || return 0

	ts="$(_fullab_liveness_ts)" || return 1
	[ -n "$ts" ] || return 1

	FULLAB_STALE_AGE=$(( $(_fullab_now) - ts ))
	[ "$FULLAB_STALE_AGE" -gt "$FULLAB_STALE_SECS" ] || return 1

	# 心跳(或退化判据)陈旧了, 还得确认持有者进程确实不在了。
	pid="$(_fullab_owner_pid)"
	host="$(_fullab_owner_host)"

	if [ -z "$pid" ]; then
		# 连 owner.info 都没有 —— 没人登记过, 视为遗留垃圾
		return 0
	fi
	if [ -n "$host" ] && [ "$host" != "$(uname -n)" ]; then
		# 别的机器上的 pid 无从判断存活 (pid 空间不同) —— 只能信心跳
		return 0
	fi
	if _fullab_pid_alive "$pid"; then
		# 进程还在 ⇒ 它只是慢, 不是僵。绝不能回收, 否则两个 fullab 并发跑。
		return 1
	fi
	return 0
}

_fullab_write_owner_info() {
	mkdir -p "$FULLAB_LOCK_DIR" 2>/dev/null
	printf 'pid=%s\nhost=%s\nstarted=%s\nhb=%s\nstale=%s\n' \
		"$$" "$(uname -n)" "$(_fullab_now)" \
		"$FULLAB_HB_INTERVAL" "$FULLAB_STALE_SECS" \
		> "$FULLAB_LOCK_DIR/owner.info"
}

# 后台心跳。用 `>` 截断写**已存在**的文件 —— 只改文件 mtime, 不动目录项,
# 因此不会刷新目录 mtime (这正是旧判据踩的坑)。
_fullab_start_heartbeat() {
	# 后台任务必须重定向 stdout —— 否则调用方若处在命令替换 $(...) 或管道里,
	# 会一直等到这个后台进程关闭写端才返回 (脚本里踩过一次, 表现为整个流程挂死)。
	( while :; do
		_fullab_now > "$FULLAB_LOCK_DIR/heartbeat" 2>/dev/null || exit 0
		sleep "$FULLAB_HB_INTERVAL" 2>/dev/null || exit 0
	done ) >/dev/null 2>&1 &
	FULLAB_HB_PID=$!
	_fullab_now > "$FULLAB_LOCK_DIR/heartbeat" 2>/dev/null
}

# fullab_lock_acquire <lockdir> [stale_secs] [wait_secs]
# 返回 0 = 拿到锁 (并设置 trap 用的 FULLAB_LOCK_HELD=1), 1 = 超时放弃。
fullab_lock_acquire() {
	FULLAB_LOCK_DIR="${1:-/f/tmp/fullab.lockdir}"
	FULLAB_STALE_SECS="${2:-${FULLAB_STALE_SECS:-900}}"
	FULLAB_WAIT_SECS="${3:-${FULLAB_WAIT_SECS:-3600}}"
	FULLAB_POLL_SECS="${FULLAB_POLL_SECS:-5}"
	FULLAB_HB_INTERVAL="${FULLAB_HB_INTERVAL:-30}"
	FULLAB_STALE_AGE=0

	deadline=$(( $(_fullab_now) + FULLAB_WAIT_SECS ))
	while :; do
		if mkdir "$FULLAB_LOCK_DIR" 2>/dev/null; then
			_fullab_write_owner_info
			_fullab_start_heartbeat
			FULLAB_LOCK_HELD=1
			echo "[fullab-lock] 已获取 $FULLAB_LOCK_DIR (pid $$, 心跳 ${FULLAB_HB_INTERVAL}s, 陈旧阈值 ${FULLAB_STALE_SECS}s)" >&2
			return 0
		fi

		if _fullab_lock_is_stale; then
			echo "[fullab-lock] 回收僵锁 $FULLAB_LOCK_DIR (持有者已死, 心跳停止 ${FULLAB_STALE_AGE}s)" >&2
			rm -rf "$FULLAB_LOCK_DIR"
			continue
		fi

		if [ "$(_fullab_now)" -ge "$deadline" ]; then
			echo "[fullab-lock] 等待 $FULLAB_LOCK_DIR 超时 (${FULLAB_WAIT_SECS}s), 放弃" >&2
			return 1
		fi

		# 关键: 每次轮询都打一行, 免得又出现「空转 9 分钟不知道在等什么」
		if [ "${FULLAB_QUIET:-0}" != 1 ]; then
			pid="$(_fullab_owner_pid)"
			host="$(_fullab_owner_host)"
			echo "[fullab-lock] $FULLAB_LOCK_DIR 被 ${host:-?}:${pid:-?} 持有, 等 ${FULLAB_POLL_SECS}s..." >&2
		fi
		sleep "$FULLAB_POLL_SECS"
	done
}

fullab_lock_release() {
	[ "${FULLAB_LOCK_HELD:-0}" = 1 ] || return 0
	if [ -n "${FULLAB_HB_PID:-}" ]; then
		kill "$FULLAB_HB_PID" 2>/dev/null
		wait "$FULLAB_HB_PID" 2>/dev/null
		FULLAB_HB_PID=
	fi
	rm -rf "$FULLAB_LOCK_DIR" 2>/dev/null
	FULLAB_LOCK_HELD=0
}

# 只查询不获取: 打印当前持锁者, 没锁返回 1。
fullab_lock_status() {
	d="${1:-/f/tmp/fullab.lockdir}"
	[ -d "$d" ] || return 1
	cat "$d/owner.info" 2>/dev/null
	return 0
}
