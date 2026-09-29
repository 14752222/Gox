// gx/update: 桌面应用自动更新 (update 包的 JS 门面, v1.1)。
//
//	import { checkForUpdate, downloadAndInstall, currentVersion, cleanupBackup } from "gx/update";
//
//	const rel = await checkForUpdate("https://my.host/app-manifest.json", {
//	    currentVersion: "0.7.0",   // 缺省时从工作目录 gox.json 的 version 读, 再退 "0.0.0"
//	    preRelease: false,         // true 时 pre 通道也参与选版本
//	});
//	if (rel) console.log(`发现新版本 ${rel.version}: ${rel.notes}`);
//
//	const res = await downloadAndInstall("https://my.host/app-manifest.json", {
//	    onProgress: (done, total) => console.log(`${done}/${total}`),
//	    preRelease: false,
//	});
//	if (res) console.log(`已就位 ${res.version}, 重启生效`);
//	cleanupBackup();  // 启动时调一次: 清理上次更新残留的 .bak
//
// 语义 (与 update 包一一对齐, 设计文档 docs/auto-update.md):
//   - checkForUpdate 返回 Promise<release | null>: null 表示已是最新; 网络/清单
//     错误 reject。release = {version, channel, notes, files: {"goos/goarch": {url, sha256, size}}}。
//   - downloadAndInstall 返回 Promise<{version, path} | null>: null 表示已是最新
//     (不会下载); 成功时新二进制已就位, **当前进程仍跑旧映像, 重启生效**。
//   - onProgress 从下载 goroutine 经事件循环派发 (0ms 定时器桥, 与 http 模块
//     同构), 每 64ms 至多一次 —— JS 侧不会在单帧里被进度回调打爆。
//   - 本模块只做"换名就位", 不做进程重启: 重启是宿主/用户的事 (桌面端
//     退出后由启动器/快捷方式拉起新版本)。
package stdlib

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/14752222/Gox/config"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
	"github.com/14752222/Gox/update"
)

// updateProgressInterval 限制 onProgress 桥接到 JS 的频率 (64ms ≈ 单帧 @60fps)。
const updateProgressInterval = 64 * time.Millisecond

// setupUpdate 注册 gx/update 模块 (由 Setup 调用, env 参数保持签名一致)。
func setupUpdate(env *runtime.Environment) {
	object.RegisterBuiltinModule("gx/update", func() map[string]object.Value {
		return map[string]object.Value{
			"currentVersion":     object.NewBuiltin("currentVersion", jsUpdateCurrentVersion),
			"checkForUpdate":     object.NewBuiltin("checkForUpdate", jsCheckForUpdate),
			"downloadAndInstall": object.NewBuiltin("downloadAndInstall", jsDownloadAndInstall),
			"cleanupBackup":      object.NewBuiltin("cleanupBackup", jsUpdateCleanupBackup),
		}
	})
}

// resolveAppVersion 决定"当前版本"的解析顺序:
//  1. 调用方显式给 (options.currentVersion) —— 最可信;
//  2. 工作目录 gox.json 的 version —— gox create 出来的工程就在这一份;
//  3. "0.0.0" —— 没有任何版本信息时视为"总是要更新"。
func resolveAppVersion(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if cfg, err := config.Load("."); err == nil && cfg.Version != "" {
		return cfg.Version
	}
	return "0.0.0"
}

// parseUpdateOptions 从 options 对象取 preRelease / currentVersion / onProgress。
// opts 为 null/undefined 时用全默认值。
func parseUpdateOptions(op string, opts object.Value) (pre bool, curVer string, onProgress object.Value, err object.Value) {
	if opts == nil || opts == object.NullSingleton || opts == object.UndefinedSingleton {
		return false, "", nil, nil
	}
	obj, ok := opts.(*object.Object)
	if !ok {
		return false, "", nil, object.NewTypeError("%s: options 必须是对象, 收到 %s", op, object.TypeOf(opts))
	}
	if v, found := obj.GetProperty("preRelease"); found {
		if b, isBool := v.(*object.Boolean); isBool {
			pre = b.Value
		}
	}
	if v, found := obj.GetProperty("currentVersion"); found {
		if s, isStr := v.(*object.String); isStr {
			curVer = s.Value
		}
	}
	if v, found := obj.GetProperty("onProgress"); found && object.IsCallable(v) {
		onProgress = v
	}
	return pre, curVer, onProgress, nil
}

// jsUpdateCurrentVersion 实现 currentVersion() —— 同步返回解析出的当前版本号。
func jsUpdateCurrentVersion(args ...object.Value) object.Value {
	if len(args) > 0 {
		if s, ok := args[0].(*object.String); ok {
			return object.NewString(resolveAppVersion(s.Value))
		}
		return object.NewTypeError("currentVersion: 参数必须是字符串 (显式版本号), 收到 %s", object.TypeOf(args[0]))
	}
	return object.NewString(resolveAppVersion(""))
}

// jsCheckForUpdate 实现 checkForUpdate(manifestURL, options?) —— Promise 版。
func jsCheckForUpdate(args ...object.Value) object.Value {
	if len(args) < 1 {
		return object.NewTypeError("checkForUpdate: 缺少 manifestURL 参数")
	}
	urlStr, ok := args[0].(*object.String)
	if !ok {
		return object.NewTypeError("checkForUpdate: manifestURL 必须是字符串, 收到 %s", object.TypeOf(args[0]))
	}
	pre, curVer, _, optsErr := parseUpdateOptions("checkForUpdate", argOrNil(args, 1))
	if optsErr != nil {
		return optsErr
	}

	result := object.NewPromise()
	token := object.GlobalScheduler().AddPendingTask()
	go func() {
		defer object.GlobalScheduler().FinishPendingTask(token)
		rel, err := update.Check(urlStr.Value, resolveAppVersion(curVer), update.CheckOptions{PreRelease: pre})
		dispatchToLoop(func() {
			if err != nil {
				result.Reject(object.NewError(err.Error()))
				return
			}
			if rel == nil {
				result.Resolve(object.NullSingleton)
				return
			}
			result.Resolve(releaseToObject(rel))
			if cbErr := object.TakeCallbackError(); cbErr != nil {
				reportUncaught(cbErr)
			}
		})
	}()
	return result
}

// jsDownloadAndInstall 实现 downloadAndInstall(manifestURL, options?)。
func jsDownloadAndInstall(args ...object.Value) object.Value {
	if len(args) < 1 {
		return object.NewTypeError("downloadAndInstall: 缺少 manifestURL 参数")
	}
	urlStr, ok := args[0].(*object.String)
	if !ok {
		return object.NewTypeError("downloadAndInstall: manifestURL 必须是字符串, 收到 %s", object.TypeOf(args[0]))
	}
	pre, curVer, onProgress, optsErr := parseUpdateOptions("downloadAndInstall", argOrNil(args, 1))
	if optsErr != nil {
		return optsErr
	}

	result := object.NewPromise()
	if !updateBusy.TryLock() {
		result.Reject(object.NewError("downloadAndInstall: 已有一个更新任务在进行"))
		return result
	}
	token := object.GlobalScheduler().AddPendingTask()
	go func() {
		defer func() {
			updateBusy.Unlock()
			object.GlobalScheduler().FinishPendingTask(token)
		}()
		var lastEmit time.Time
		var progress update.ProgressFunc
		if onProgress != nil {
			progress = func(done, total int64) {
				// 下载 goroutine 里只做节流, 不碰 VM; 真正的回调经
				// dispatchToLoop 投回主线程 (onProgress 可能是 JS 闭包)。
				now := time.Now()
				if now.Sub(lastEmit) < updateProgressInterval && done != total {
					return
				}
				lastEmit = now
				cb := onProgress
				dispatchToLoop(func() {
					object.CallFunction(cb, nil, object.NewNumber(float64(done)), object.NewNumber(float64(total)))
					if cbErr := object.TakeCallbackError(); cbErr != nil {
						reportUncaught(cbErr)
					}
				})
			}
		}

		rel, applied, err := update.SelfUpdate("", urlStr.Value, resolveAppVersion(curVer),
			update.CheckOptions{PreRelease: pre}, progress)
		dispatchToLoop(func() {
			if err != nil {
				result.Reject(object.NewError(err.Error()))
				return
			}
			if rel == nil || !applied {
				result.Resolve(object.NullSingleton) // 已是最新
				return
			}
			out := object.NewObject()
			out.SetProperty("version", object.NewString(rel.Version))
			if exe, err := os.Executable(); err == nil {
				out.SetProperty("path", object.NewString(exe))
			}
			result.Resolve(out)
			if cbErr := object.TakeCallbackError(); cbErr != nil {
				reportUncaught(cbErr)
			}
		})
	}()
	return result
}

// jsUpdateCleanupBackup 实现 cleanupBackup() —— 同步清理当前可执行文件的
// .bak 残留 (幂等); 启动时调一次即可。
func jsUpdateCleanupBackup(args ...object.Value) object.Value {
	exe, err := os.Executable()
	if err != nil {
		return object.NewError(fmt.Sprintf("cleanupBackup: 定位当前可执行文件失败: %v", err))
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := update.CleanupBackup(exe); err != nil {
		return object.NewError(err.Error())
	}
	return object.UndefinedSingleton
}

// releaseToObject 把 update.Release 转成 JS 对象。
func releaseToObject(rel *update.Release) object.Value {
	out := object.NewObject()
	out.SetProperty("version", object.NewString(rel.Version))
	out.SetProperty("channel", object.NewString(rel.Channel))
	out.SetProperty("notes", object.NewString(rel.Notes))
	files := object.NewObject()
	for k, f := range rel.Files {
		fe := object.NewObject()
		fe.SetProperty("url", object.NewString(f.URL))
		fe.SetProperty("sha256", object.NewString(f.SHA256))
		fe.SetProperty("size", object.NewNumber(float64(f.Size)))
		files.SetProperty(k, fe)
	}
	out.SetProperty("files", files)
	return out
}

// argOrNil 取第 i 个参数, 越界时返回 undefined (供 parseUpdateOptions 判空)。
func argOrNil(args []object.Value, i int) object.Value {
	if i < len(args) {
		return args[i]
	}
	return object.UndefinedSingleton
}

// updateBusy 保证同一进程内不并发执行两次"下载安装" (JS 里用户可能连点
// 两次按钮); 忙碌时第二次调用直接 reject, 不排队。
var updateBusy sync.Mutex
