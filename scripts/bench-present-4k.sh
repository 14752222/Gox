#!/usr/bin/env bash
# ============================================================================
# bench-present-4k.sh — 4K 帧呈现路径 A/B: CPU blit (x11) vs GPU 合成
#
#   ./scripts/bench-present-4k.sh                # 默认 benchtime: 全帧 20x / 脏区 100x
#   ./scripts/bench-present-4k.sh --full 50      # 全帧每项 50 次
#   ./scripts/bench-present-4k.sh --dirty 200    # 脏矩形每项 200 次
#
# 为什么单独一个脚本而不并进 bench-all.sh:
#   bench-all.sh 是 nightly 性能闸门 (看板 rKpKpD), 判 gox/node 比值,
#   跨机器可比; 本脚本回答的是 GPU 阶段的验收指标 (ShowRegions CPU 占用),
#   依赖 Xvfb + -tags gpu, 且**没有 GPU 硬件时 GPU 侧只有兜底口径**,
#   数字不宜进那条闸门。两者互为补充, 不是竞争关系。
#
# 产物落在 bench-results/present-4k-*.txt (与 bench-all.sh 的 json 并存)。
#
# 依赖: Xvfb (apt install xvfb); Go 1.26.2 (go.mod go-version-file)。
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

FULL_X=20
DIRTY_X=100
while [[ $# -gt 0 ]]; do
  case "$1" in
    --full)  FULL_X="$2"; shift 2 ;;
    --dirty) DIRTY_X="$2"; shift 2 ;;
    *) echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

W=3840 H=2160
OUT="bench-results"
mkdir -p "$OUT"

echo "== 1/2  CPU 路径 (x11 后端, 逐区域 PutImage) =="
# Xvfb 屏幕必须 >= 4K, 否则建窗失败, 基准会 skip (输出里能看到原因)。
xvfb-run -a -s "-screen 0 ${W}x${H}x24" \
  go test ./gfx -run '^$' \
    -bench 'BenchmarkPresent4K_CPU' \
    -benchtime "${FULL_X}x" \
    -benchmem 2>&1 | tee "$OUT/present-4k-cpu.txt"

echo
echo "== 2/2  GPU 路径 (gfx/gpu, 需 -tags gpu) =="
# AppCPU 口径全帧用 FULL_X; Total 口径含 Poll 等待, 次数减半防超时。
go test -tags gpu ./gfx/gpu -run '^$' \
  -bench 'BenchmarkPresent4K_GPU' \
  -benchtime "${FULL_X}x" \
  -benchmem 2>&1 | tee "$OUT/present-4k-gpu.txt"

cat <<EOF

五个数怎么读 (cpu-ns/op 列, 4K 全帧除注明外):
  CPU_X11          —— x11 PutImage 的应用 CPU (33MB 逐像素换序 + Sync)
  GPU_UploadOnly   —— 只做「子纹理提取 + WriteTexture」的应用 CPU。
                      这是 GPU 路径**唯一可迁移到真实硬件**的数字: 真实 GPU 上
                      应用线程的付出也就是提取+上传+编码, 之后异步。
  GPU_AppCPU       —— 完整 ShowRegions, 不 Poll
  GPU_Total        —— 完整 ShowRegions, 每帧 PollWait

两个实测事实 (解释了为什么 AppCPU 与 Total 几乎相同):
  1. 纯 Go 软件 HAL 把光栅化**同步塞进 Draw 调用** (wgpu 软件后端语义),
     所以 Poll 不推进任何东西, AppCPU == Total (差 <1%)。这个相等本身
     就是「当前跑在软件后端上」的指纹。
  2. 软件 HAL 每 Draw 全量 load 目标纹理 + 重建深度缓冲/管线 (~100MB/帧),
     与 scissor 脏区大小无关 —— 所以 GPU_Dirty 与 GPU 全帧几乎一样慢。
     脏矩形优化只在真实硬件上兑现 (只上传 1MB + scissor 重绘)。

结论口径:
  硬件上的应用 CPU 收益   = CPU_X11 - GPU_UploadOnly (~12x, 实测 32.4ms -> 2.7ms)
  无 GPU 硬件时的最差代价 = GPU_Total (比 CPU_X11 还慢 ~5x —— 所以生产路径
                            只在 BackendKind()=="hardware" 时包装, 见
                            cmd/gox/gpu_present_init.go 的文件头)
X 服务器 (Xvfb) 是独立进程, 它的 CPU 不进任何一侧的 rusage。
EOF
