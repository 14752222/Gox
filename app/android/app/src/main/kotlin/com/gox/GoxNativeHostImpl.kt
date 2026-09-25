package com.gox

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.Uri
import android.os.BatteryManager
import android.os.Build
import android.os.Environment
import android.os.Looper
import android.os.PowerManager
import android.os.VibrationEffect
import android.os.Vibrator
import android.provider.MediaStore
import android.provider.Settings
import android.location.Location
import android.location.LocationListener
import android.location.LocationManager
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.FileOutputStream
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * NativeHost 六模块实现 (device / app / geo / media / permission + viewport 上报)。
 *
 * ## 与 Go 契约的对应关系 (app/NATIVE-HOST.md 有完整对照表)
 *
 * 同步方法 (device.* / app.exit / app.orientation / permission.get /
 * location.unwatch) 直接在 [nativeCall] 里返回结果 JSON 或错误信封 —— 内核的
 * deviceHostOverlay / jsGetBrightness / jsOpenSystemSettings /
 * refreshPermissionsFromHost / jsSetOrientation 只认同步返回值。
 *
 * 异步方法 (app.share / permission.request / camera / gallery / media /
 * location.get / location.watch) 返回 [GoxNativeHost.PENDING], 把 __id 留住,
 * 在系统回调里经 [GoxRuntime.nativeResolveNative] 回填 (任意线程, Go 侧
 * gfx.Post 投回 GUI 线程)。流式定位 watch 对同一个 id 反复回填。
 *
 * ## 降级口径 (绝不返回假数据)
 *
 *   - 权限没给 → permission-denied;
 *   - 用户取消 → cancelled;
 *   - 平台给不出要求的坐标系 (gcj02) → platform-error, 不做火星坐标换算
 *     (gfx/native_geo.go 文件头: 换错比不换更糟);
 *   - 功能确实做不到 (如 Android 上取不到的量) → unavailable / unsupported;
 *   - 参数不合法 → invalid-arg。
 *
 * ## 线程纪律
 *
 * nativeCall 跑在 Go 的脚本线程上。碰 UI / 系统回调的操作一律
 * `activity.runOnUiThread { … }` post 到主线程, 本调用立即返回 PENDING。
 * Android 主线程从不同步等待脚本线程, 所以这个 post 不会死锁。
 */
class GoxNativeHostImpl(private val activity: Activity) : GoxNativeHost {

    // ===== 常量 =====

    private companion object {
        const val REQ_CAMERA = 7001
        const val REQ_GALLERY = 7002
        const val REQ_VIDEO = 7003
        const val REQ_PERM = 8001

        /** 一次定位的宿主侧兜底超时 (内核只在脚本传了 timeout 时才设定时器)。 */
        const val LOC_TIMEOUT_MS = 30_000L

        val PERMISSION_KINDS = listOf(
            "location", "camera", "gallery", "microphone",
            "notification", "contacts", "calendar", "bluetooth", "storage",
        )
    }

    // ===== 待决调用状态 (只在主线程与脚本线程短暂互碰, 用锁保护) =====

    private val lock = Object()

    /** 权限申请: REQ_PERM 对应的 (id, kinds)。 */
    private var pendingPermId: String? = null
    private var pendingPermKinds: List<String> = emptyList()

    /** 定位监听: __id → LocationListener (location.watch / location.get 共用表)。 */
    private val locationListeners = HashMap<String, LocationListener>()

    /** 一次定位的兜底超时任务: __id → 已解除标记。 */
    private val locTimers = HashMap<String, Boolean>()

    // ===== 契约入口 =====

    override fun nativeCapabilities(): String = GoxNativeHost.CAPABILITY_LIST.joinToString(",")

    override fun nativeCall(method: String, argsJson: String): String {
        val args = try {
            if (argsJson.isBlank()) JSONObject() else JSONObject(argsJson)
        } catch (e: Exception) {
            return err(GoxNativeHost.ERR_INVALID_ARG, "参数不是合法 JSON: ${e.message}")
        }
        val id = args.optString("__id", "")
        return try {
            when (method) {
                // ── gx/device ──
                "device.info" -> deviceInfoJson().toString()
                "device.vibrate" -> vibrate(args)
                "device.brightness" -> brightness(args)
                "device.keepScreenOn" -> keepScreenOn(args)
                "device.openSettings" -> openSettings(args)
                // ── gx/app ──
                "app.share" -> share(args, id)
                "app.exit" -> exitApp()
                "app.orientation" -> setOrientation(args)
                // ── gx/permission ──
                "permission.get" -> permissionGet()
                "permission.request" -> permissionRequest(args, id)
                // ── gx/media ──
                "camera.takePhoto" -> takePhoto(args, id)
                "gallery.pick" -> pickImages(args, id)
                "gallery.pickVideo" -> pickVideo(id)
                "media.save" -> saveMedia(args, id)
                "media.preview" -> preview(args, id)
                // ── gx/geo ──
                "location.get" -> getLocation(args, id)
                "location.watch" -> watchLocation(args, id)
                "location.unwatch" -> unwatchLocation(id)
                // ── 未知方法: 显式 unsupported, 绝不返回假数据 ──
                else -> err(GoxNativeHost.ERR_UNSUPPORTED, "Android 宿主未实现方法 $method")
            }
        } catch (e: Exception) {
            err(GoxNativeHost.ERR_PLATFORM, "$method: ${e.javaClass.simpleName}: ${e.message}")
        }
    }

    // ── 回填助手 (异步方法用) ──

    private fun resolve(id: String, result: JSONObject) {
        if (id.isEmpty()) return
        GoxRuntime.nativeResolveNative(id, result.toString(), "", "")
    }

    private fun resolveString(id: String, value: String) {
        if (id.isEmpty()) return
        GoxRuntime.nativeResolveNative(id, JSONObject.quote(value), "", "")
    }

    private fun fail(id: String, code: String, msg: String) {
        if (id.isEmpty()) return
        GoxRuntime.nativeResolveNative(id, "", code, msg)
    }

    // ===== gx/device =====

    /**
     * device.info → 完整设备信息对象 (字段名与 gfx.DeviceInfo 的 JSON 逐字一致,
     * 见 gfx/native_device.go deviceInfoToJS / applyDeviceOverlay)。
     */
    private fun deviceInfoJson(): JSONObject {
        val dm = activity.resources.displayMetrics
        val conf = activity.resources.configuration
        val locale = Locale.getDefault()
        val tz = java.util.TimeZone.getDefault()
        val abis = Build.SUPPORTED_ABIS
        val abi = if (abis.isNotEmpty()) archName(abis[0]) else ""
        val pm = activity.packageManager
        val pkg = activity.packageName
        val versionName = try {
            @Suppress("DEPRECATION")
            pm.getPackageInfo(pkg, 0).versionName ?: ""
        } catch (e: Exception) {
            ""
        }
        val versionCode = try {
            @Suppress("DEPRECATION")
            pm.getPackageInfo(pkg, 0).let { if (Build.VERSION.SDK_INT >= 28) it.longVersionCode.toString() else it.versionCode.toString() }
        } catch (e: Exception) {
            ""
        }
        val appName = try {
            @Suppress("DEPRECATION")
            pm.getApplicationLabel(pm.getApplicationInfo(pkg, 0)).toString()
        } catch (e: Exception) {
            "Gox"
        }
        val o = JSONObject()
        o.put("platform", "android")
        o.put("os", "Android ${Build.VERSION.RELEASE}")
        o.put("osVersion", Build.VERSION.RELEASE ?: "")
        o.put("arch", abi)
        o.put("model", Build.MODEL ?: "")
        o.put("brand", Build.BRAND ?: "")
        o.put("manufacturer", Build.MANUFACTURER ?: "")
        o.put("deviceId", Settings.Secure.getString(activity.contentResolver, Settings.Secure.ANDROID_ID) ?: "")
        o.put("locale", locale.toString()) // "zh_CN" 形态; 语言的 language/country 是拆开的两份
        o.put("language", locale.language)
        o.put("region", locale.country)
        o.put("timezone", tz.id)
        o.put("tzOffset", tz.getOffset(System.currentTimeMillis()) / 60000)
        o.put("sdkVersion", Build.VERSION.SDK_INT)
        o.put("screenWidth", dm.widthPixels)
        o.put("screenHeight", dm.heightPixels)
        o.put("pixelRatio", dm.density.toDouble())
        o.put("isEmulator", isEmulator())
        o.put("isTablet", conf.smallestScreenWidthDp >= 600)
        o.put("appName", appName)
        o.put("appVersion", versionName)
        o.put("appBuild", versionCode)
        return o
    }

    /** arch 归一到内核词汇表 ("arm64"/"arm"/"amd64"/"386", 见 gfx.DeviceInfo.Arch)。 */
    private fun archName(abi: String): String = when {
        abi.startsWith("arm64") -> "arm64"
        abi.startsWith("armeabi") -> "arm"
        abi.startsWith("x86_64") -> "amd64"
        abi.startsWith("x86") -> "386"
        else -> abi
    }

    private fun isEmulator(): Boolean {
        val f = Build.FINGERPRINT ?: return false
        val model = Build.MODEL ?: ""
        return f.contains("generic", ignoreCase = true) ||
            f.contains("emulator", ignoreCase = true) ||
            model.contains("Emulator", ignoreCase = true) ||
            model.contains("Android SDK built for", ignoreCase = true)
    }

    /** device.vibrate {duration}. 软调用: 没有震动器就静默 (内核 vibrate 不看结果)。 */
    private fun vibrate(args: JSONObject): String {
        val ms = if (args.has("duration") && !args.isNull("duration")) args.optDouble("duration", 15.0) else 15.0
        @Suppress("DEPRECATION")
        val vibrator = activity.getSystemService(Context.VIBRATOR_SERVICE) as? Vibrator
            ?: return err(GoxNativeHost.ERR_UNAVAILABLE, "设备没有震动器")
        if (!vibrator.hasVibrator()) {
            return err(GoxNativeHost.ERR_UNAVAILABLE, "设备没有震动器")
        }
        if (Build.VERSION.SDK_INT >= 26) {
            vibrator.vibrate(VibrationEffect.createOneShot(ms.toLong(), VibrationEffect.DEFAULT_AMPLITUDE))
        } else {
            @Suppress("DEPRECATION")
            vibrator.vibrate(ms.toLong())
        }
        return "true"
    }

    /**
     * device.brightness: 带 {value} 是设置 (0..1, 内核已夹紧), 不带是读取。
     * 读不出 (-1) 返回 -1 —— 不猜。设置走主线程 post, 当场返回目标值。
     */
    private fun brightness(args: JSONObject): String {
        if (args.has("value") && !args.isNull("value")) {
            val v = args.optDouble("value", -1.0).coerceIn(0.0, 1.0)
            activity.runOnUiThread {
                val w = activity.window ?: return@runOnUiThread
                val lp = w.attributes
                lp.screenBrightness = v.toFloat()
                w.attributes = lp
            }
            return JSONObject().put("value", v).toString()
        }
        val cur = activity.window?.attributes?.screenBrightness ?: -1f
        return JSONObject().put("value", if (cur < 0) -1.0 else cur.toDouble()).toString()
    }

    /** device.keepScreenOn {on}. FLAG_KEEP_SCREEN_ON 必须在主线程改。 */
    private fun keepScreenOn(args: JSONObject): String {
        val on = if (args.has("on") && !args.isNull("on")) args.optBoolean("on", true) else true
        activity.runOnUiThread {
            val w = activity.window ?: return@runOnUiThread
            if (on) w.addFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            else w.clearFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        }
        return "true"
    }

    /**
     * device.openSettings {kind}. kind 白名单与 gfx.SettingKinds() 逐项对应
     * (native_device.go settingKindList: app/wifi/bluetooth/location/notification/
     * display/sound/battery/date/privacy/storage)。
     */
    private fun openSettings(args: JSONObject): String {
        val kind = args.optString("kind", "app").ifEmpty { "app" }
        val action = when (kind) {
            "app" -> Settings.ACTION_APPLICATION_DETAILS_SETTINGS
            "wifi" -> Settings.ACTION_WIFI_SETTINGS
            "bluetooth" -> Settings.ACTION_BLUETOOTH_SETTINGS
            "location" -> Settings.ACTION_LOCATION_SOURCE_SETTINGS
            "notification" -> if (Build.VERSION.SDK_INT >= 26) Settings.ACTION_APP_NOTIFICATION_SETTINGS
            else Settings.ACTION_APPLICATION_DETAILS_SETTINGS
            "display" -> Settings.ACTION_DISPLAY_SETTINGS
            "sound" -> Settings.ACTION_SOUND_SETTINGS
            "battery" -> Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS
            "date" -> Settings.ACTION_DATE_SETTINGS
            "privacy" -> Settings.ACTION_PRIVACY_SETTINGS
            "storage" -> Settings.ACTION_INTERNAL_STORAGE_SETTINGS
            else -> return err(GoxNativeHost.ERR_UNSUPPORTED, "Android 未实现设置页 $kind")
        }
        val intent = Intent(action)
        if (action == Settings.ACTION_APPLICATION_DETAILS_SETTINGS) {
            intent.data = Uri.fromParts("package", activity.packageName, null)
        }
        intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        return try {
            activity.startActivity(intent)
            "true"
        } catch (e: Exception) {
            err(GoxNativeHost.ERR_PLATFORM, "打开设置页失败: ${e.message}")
        }
    }

    // ===== gx/app =====

    /**
     * app.share {text/url/imagePath/files} → 系统分享面板。无回调结果可等
     * (ACTION_SEND chooser 不回传), 拉起成功即 resolve {}。
     */
    private fun share(args: JSONObject, id: String): String {
        val text = args.optString("text", "")
        val url = args.optString("url", "")
        val imagePath = args.optString("imagePath", "")
        val files = args.optJSONArray("files")
        if (text.isEmpty() && url.isEmpty() && imagePath.isEmpty() && files == null) {
            return err(GoxNativeHost.ERR_INVALID_ARG, "share: 至少要给 text / url / imagePath / files 之一")
        }
        activity.runOnUiThread {
            try {
                val body = StringBuilder()
                if (text.isNotEmpty()) body.append(text)
                if (url.isNotEmpty()) body.append(if (body.isEmpty()) url else " $url")
                val send = Intent()
                var streamUri: Uri? = null
                val streamUris = ArrayList<Uri>()
                if (imagePath.isNotEmpty()) {
                    shareUri(imagePath)?.let { streamUri = it }
                }
                if (files != null) {
                    for (i in 0 until files.length()) {
                        val f = files.optJSONObject(i) ?: continue
                        val p = f.optString("uri", "").ifEmpty { f.optString("path", "") }
                        if (p.isNotEmpty()) shareUri(p)?.let { streamUris.add(it) }
                    }
                }
                if (streamUris.size > 1) {
                    send.action = Intent.ACTION_SEND_MULTIPLE
                    send.putParcelableArrayListExtra(Intent.EXTRA_STREAM, streamUris)
                    send.type = "image/*"
                } else {
                    send.action = Intent.ACTION_SEND
                    if (streamUri != null) {
                        send.putExtra(Intent.EXTRA_STREAM, streamUri)
                        send.type = "image/*"
                    } else {
                        send.type = "text/plain"
                    }
                }
                if (body.isNotEmpty()) send.putExtra(Intent.EXTRA_TEXT, body.toString())
                activity.startActivity(Intent.createChooser(send, "分享"))
                resolve(id, JSONObject())
            } catch (e: Exception) {
                fail(id, GoxNativeHost.ERR_PLATFORM, "拉起分享面板失败: ${e.message}")
            }
        }
        return GoxNativeHost.PENDING
    }

    /** app.exit: iOS 上系统不允许自杀, Android finishAffinity 是诚实的实现。 */
    private fun exitApp(): String {
        activity.runOnUiThread { activity.finishAffinity() }
        return "true"
    }

    /** app.orientation {mode}: "portrait"|"landscape"|"auto" (内核已归一)。 */
    private fun setOrientation(args: JSONObject): String {
        val mode = args.optString("mode", "")
        val orientation = when (mode) {
            "portrait" -> android.content.pm.ActivityInfo.SCREEN_ORIENTATION_PORTRAIT
            "landscape" -> android.content.pm.ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE
            "auto" -> android.content.pm.ActivityInfo.SCREEN_ORIENTATION_FULL_SENSOR
            else -> return err(GoxNativeHost.ERR_INVALID_ARG, "setOrientation: 不认识的模式 $mode")
        }
        activity.runOnUiThread { activity.requestedOrientation = orientation }
        return "true"
    }

    // ===== gx/permission =====

    /**
     * permission.get → {location, camera, gallery, microphone, notification,
     * contacts, calendar, bluetooth, storage} (kind 列表与
     * gfx/native_permission.go permissionKindList 一致)。
     *
     * Android 没有 not-determined 与 denied 的稳定区分 (shouldShowRequestPermission-
     * Rationale 在"从未问过"与"别再问了"两种情况下都返回 false), 诚实起见非授权
     * 一律报 denied —— 不猜。
     */
    private fun permissionGet(): String {
        val o = JSONObject()
        for (kind in PERMISSION_KINDS) {
            o.put(kind, permissionState(kind))
        }
        return o.toString()
    }

    /** 单个权限的当前状态 (同步读, 任意线程可调)。 */
    fun permissionState(kind: String): String = when (kind) {
        "location" -> checkPerm(Manifest.permission.ACCESS_FINE_LOCATION)
        "camera" -> checkPerm(Manifest.permission.CAMERA)
        "gallery" -> galleryPermState()
        "microphone" -> checkPerm(Manifest.permission.RECORD_AUDIO)
        "notification" -> {
            val nm = activity.getSystemService(Context.NOTIFICATION_SERVICE) as? android.app.NotificationManager
            if (nm != null && nm.areNotificationsEnabled()) "granted" else "denied"
        }
        "contacts" -> checkPerm(Manifest.permission.READ_CONTACTS)
        "calendar" -> checkPerm(Manifest.permission.READ_CALENDAR)
        "bluetooth" -> if (Build.VERSION.SDK_INT >= 31) checkPerm(Manifest.permission.BLUETOOTH_CONNECT) else "granted"
        "storage" -> if (Build.VERSION.SDK_INT >= 33) checkPerm(Manifest.permission.READ_MEDIA_IMAGES)
        else checkPerm(Manifest.permission.READ_EXTERNAL_STORAGE)
        else -> "unknown"
    }

    private fun checkPerm(perm: String): String =
        if (activity.checkSelfPermission(perm) == PackageManager.PERMISSION_GRANTED) "granted" else "denied"

    /**
     * 相册: Android 14 的"仅选中的照片" → limited (与 iOS 的 limited 同词)。
     */
    private fun galleryPermState(): String {
        if (Build.VERSION.SDK_INT >= 34) {
            val full = activity.checkSelfPermission(Manifest.permission.READ_MEDIA_IMAGES)
            val partial = activity.checkSelfPermission(Manifest.permission.READ_MEDIA_VISUAL_USER_SELECTED)
            return when {
                full == PackageManager.PERMISSION_GRANTED -> "granted"
                partial == PackageManager.PERMISSION_GRANTED -> "limited"
                else -> "denied"
            }
        }
        return if (Build.VERSION.SDK_INT >= 33) checkPerm(Manifest.permission.READ_MEDIA_IMAGES)
        else checkPerm(Manifest.permission.READ_EXTERNAL_STORAGE)
    }

    /**
     * permission.request {kind} 或 {kinds:[…]} → 批量申请, **串行申请一次回填**:
     * Activity.requestPermissions 一次收一组权限, 结果按 kinds 逐项映射。
     * resolve 形状 {kind: state} (内核 extractPermissionState 认这个形状)。
     */
    private fun permissionRequest(args: JSONObject, id: String): String {
        val kinds = LinkedHashSet<String>()
        val arr = args.optJSONArray("kinds")
        if (arr != null) {
            for (i in 0 until arr.length()) {
                normalizeKind(arr.optString(i, ""))?.let { kinds.add(it) }
            }
        } else {
            normalizeKind(args.optString("kind", ""))?.let { kinds.add(it) }
        }
        if (kinds.isEmpty()) {
            return err(GoxNativeHost.ERR_INVALID_ARG, "permission.request: 没有可识别的权限种类")
        }

        // 已经授过/系统管的直接算结果, 其余进申请列表。
        val needRequest = ArrayList<Pair<String, String>>() // kind → manifest 权限
        val states = LinkedHashMap<String, String>()
        for (kind in kinds) {
            val state = permissionState(kind)
            if (state == "granted" || state == "limited") {
                states[kind] = state
                continue
            }
            val perm = manifestPermOf(kind)
            if (perm == null) {
                // 该 kind 在此系统版本不需要运行时申请 (如 <31 的蓝牙) → 视当前状态。
                states[kind] = state
            } else {
                needRequest.add(kind to perm)
            }
        }
        if (needRequest.isEmpty()) {
            resolveString(id, JSONObject(states).toString())
            return GoxNativeHost.PENDING
        }

        synchronized(lock) {
            pendingPermId = id
            pendingPermKinds = kinds.toList()
        }
        val perms = needRequest.map { it.second }.toTypedArray()
        activity.runOnUiThread {
            try {
                activity.requestPermissions(perms, REQ_PERM)
            } catch (e: Exception) {
                synchronized(lock) { pendingPermId = null }
                fail(id, GoxNativeHost.ERR_PLATFORM, "申请权限失败: ${e.message}")
            }
        }
        return GoxNativeHost.PENDING
    }

    /** kind → 归一 (白名单见 gfx/native_permission.go)。 */
    private fun normalizeKind(k: String): String? = when (k.lowercase(Locale.US).trim()) {
        "location", "gps" -> "location"
        "camera" -> "camera"
        "gallery", "photos", "photo", "images", "image", "album", "media" -> "gallery"
        "microphone", "mic", "record" -> "microphone"
        "notification", "notifications" -> "notification"
        "contacts" -> "contacts"
        "calendar" -> "calendar"
        "bluetooth" -> "bluetooth"
        "storage" -> "storage"
        else -> null
    }

    /** kind → 要申请的 manifest 权限 (null = 这个版本不需要运行时申请)。 */
    private fun manifestPermOf(kind: String): String? = when (kind) {
        "location" -> Manifest.permission.ACCESS_FINE_LOCATION
        "camera" -> Manifest.permission.CAMERA
        "gallery" -> when {
            Build.VERSION.SDK_INT >= 33 -> Manifest.permission.READ_MEDIA_IMAGES
            else -> Manifest.permission.READ_EXTERNAL_STORAGE
        }
        "microphone" -> Manifest.permission.RECORD_AUDIO
        "notification" -> if (Build.VERSION.SDK_INT >= 33) Manifest.permission.POST_NOTIFICATIONS else null
        "contacts" -> Manifest.permission.READ_CONTACTS
        "calendar" -> Manifest.permission.READ_CALENDAR
        "bluetooth" -> if (Build.VERSION.SDK_INT >= 31) Manifest.permission.BLUETOOTH_CONNECT else null
        "storage" -> if (Build.VERSION.SDK_INT >= 33) Manifest.permission.READ_MEDIA_IMAGES
        else Manifest.permission.READ_EXTERNAL_STORAGE
        else -> null
    }

    /** MainActivity.onRequestPermissionsResult 转发进来 (主线程)。 */
    fun onPermissionResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        if (requestCode != REQ_PERM) return
        val id: String
        val kinds: List<String>
        synchronized(lock) {
            id = pendingPermId ?: ""
            kinds = pendingPermKinds
            pendingPermId = null
            pendingPermKinds = emptyList()
        }
        if (id.isEmpty()) return
        val granted = HashMap<String, String>()
        permissions.forEachIndexed { i, perm ->
            val state = if (i < grantResults.size && grantResults[i] == PackageManager.PERMISSION_GRANTED) "granted" else "denied"
            manifestKindOf(perm)?.let { granted[it] = state }
        }
        val out = JSONObject()
        for (kind in kinds) {
            val state = granted[kind] ?: permissionState(kind)
            out.put(kind, state)
            GoxRuntime.nativeReportPermission(kind, state)
        }
        resolveString(id, out.toString())
    }

    /** manifest 权限 → kind (与 manifestPermOf 互逆)。 */
    private fun manifestKindOf(perm: String): String? = when (perm) {
        Manifest.permission.ACCESS_FINE_LOCATION,
        Manifest.permission.ACCESS_COARSE_LOCATION -> "location"
        Manifest.permission.CAMERA -> "camera"
        Manifest.permission.READ_MEDIA_IMAGES,
        Manifest.permission.READ_MEDIA_VISUAL_USER_SELECTED,
        Manifest.permission.READ_EXTERNAL_STORAGE -> "gallery"
        Manifest.permission.RECORD_AUDIO -> "microphone"
        Manifest.permission.POST_NOTIFICATIONS -> "notification"
        Manifest.permission.READ_CONTACTS -> "contacts"
        Manifest.permission.READ_CALENDAR -> "calendar"
        Manifest.permission.BLUETOOTH_CONNECT -> "bluetooth"
        else -> null
    }

    /** 启动时 / 从设置页回来时全量补报 (gx/native_permission.go ReportPermission 的两个典型时机)。 */
    fun reportAllPermissions() {
        for (kind in PERMISSION_KINDS) {
            GoxRuntime.nativeReportPermission(kind, permissionState(kind))
        }
    }

    // ===== gx/media =====

    /** camera.takePhoto {camera, quality, saveToGallery, format, maxWidth, maxHeight}. */
    private fun takePhoto(args: JSONObject, id: String): String {
        if (activity.checkSelfPermission(Manifest.permission.CAMERA) != PackageManager.PERMISSION_GRANTED) {
            // 相机调用必须先经 permission.request —— 不替脚本偷偷申请。
            fail(id, GoxNativeHost.ERR_PERMISSION_DENIED, "相机权限未授权 (先调 authorize(\"camera\"))")
            return GoxNativeHost.PENDING
        }
        activity.runOnUiThread {
            try {
                val intent = Intent(MediaStore.ACTION_IMAGE_CAPTURE)
                if (intent.resolveActivity(activity.packageManager) == null) {
                    fail(id, GoxNativeHost.ERR_UNAVAILABLE, "设备没有相机应用")
                    return@runOnUiThread
                }
                var outUri: Uri? = null
                if (Build.VERSION.SDK_INT >= 29) {
                    // API 29+: 插一条 MediaStore 记录当输出 (不需要写存储权限)。
                    val values = android.content.ContentValues().apply {
                        put(MediaStore.Images.Media.DISPLAY_NAME, "gox-cam-${System.currentTimeMillis()}.jpg")
                        put(MediaStore.Images.Media.MIME_TYPE, "image/jpeg")
                        put(MediaStore.Images.Media.RELATIVE_PATH, Environment.DIRECTORY_PICTURES + "/Gox")
                    }
                    outUri = activity.contentResolver.insert(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, values)
                    intent.putExtra(MediaStore.EXTRA_OUTPUT, outUri)
                }
                cameraOutUri = outUri
                synchronized(lock) { pendingMediaId[REQ_CAMERA] = id }
                activity.startActivityForResult(intent, REQ_CAMERA)
            } catch (e: Exception) {
                synchronized(lock) { pendingMediaId.remove(REQ_CAMERA) }
                fail(id, GoxNativeHost.ERR_PLATFORM, "拉起相机失败: ${e.message}")
            }
        }
        return GoxNativeHost.PENDING
    }

    /** 相机 EXTRA_OUTPUT 的 MediaStore uri (API 29+ 路径; 主线程读写)。 */
    private var cameraOutUri: Uri? = null

    /** onActivityResult 的 id 登记 (requestCode → __id)。 */
    private val pendingMediaId = HashMap<Int, String>()

    /** MainActivity.onActivityResult 转发进来 (主线程)。 */
    fun onMediaResult(requestCode: Int, resultCode: Int, data: Intent?) {
        val id = synchronized(lock) { pendingMediaId.remove(requestCode) } ?: return
        if (resultCode != Activity.RESULT_OK) {
            // 用户在系统 UI 里取消/返回 → cancelled (不是失败, 也不是假成功)。
            fail(id, GoxNativeHost.ERR_CANCELLED, "用户取消了操作")
            return
        }
        when (requestCode) {
            REQ_CAMERA -> {
                val out = cameraOutUri
                cameraOutUri = null
                if (out != null) {
                    val mime = activity.contentResolver.getType(out) ?: "image/jpeg"
                    resolve(id, mediaJson(uri = out.toString(), mime = mime))
                    return
                }
                // API <29 的兜底: 系统回传缩略图 Bitmap, 落到缓存目录再给路径。
                val bmp = data?.extras?.get("data") as? android.graphics.Bitmap
                if (bmp == null) {
                    fail(id, GoxNativeHost.ERR_PLATFORM, "相机没有返回图像")
                    return
                }
                val f = newCacheFile("jpg")
                FileOutputStream(f).use { bmp.compress(android.graphics.Bitmap.CompressFormat.JPEG, 90, it) }
                resolve(id, mediaJson(path = f.absolutePath, mime = "image/jpeg", size = f.length()))
            }
            REQ_GALLERY, REQ_VIDEO -> {
                val list = JSONArray()
                val clip = data?.clipData
                if (clip != null) {
                    for (i in 0 until clip.itemCount) {
                        val u = clip.getItemAt(i).uri ?: continue
                        list.put(mediaJson(uri = u.toString(), mime = activity.contentResolver.getType(u) ?: ""))
                    }
                } else {
                    val u = data?.data
                    if (u != null) {
                        list.put(mediaJson(uri = u.toString(), mime = activity.contentResolver.getType(u) ?: ""))
                    }
                }
                if (list.length() == 0) {
                    fail(id, GoxNativeHost.ERR_PLATFORM, "选择器没有返回内容")
                    return
                }
                resolve(id, list.toString())
            }
        }
    }

    /** 组一份 MediaFile 结果 JSON (字段与 gfx/native_media.go MediaFile 对齐)。 */
    private fun mediaJson(path: String = "", uri: String = "", mime: String = "", size: Long = -1): JSONObject {
        val o = JSONObject()
        o.put("path", path)
        o.put("uri", uri.ifEmpty { path })
        o.put("name", (uri.ifEmpty { path }).substringAfterLast('/').substringAfterLast('\\'))
        o.put("mimeType", mime)
        o.put("size", size)
        o.put("width", 0)
        o.put("height", 0)
        o.put("duration", -1)
        o.put("createdAt", System.currentTimeMillis())
        return o
    }

    /** gallery.pick {count, source, compressed, quality, allowVideo}. */
    private fun pickImages(args: JSONObject, id: String): String {
        val count = if (args.has("count") && !args.isNull("count")) args.optInt("count", 1).coerceIn(1, 9) else 1
        activity.runOnUiThread {
            try {
                val intent: Intent
                if (count > 1) {
                    intent = Intent(Intent.ACTION_GET_CONTENT)
                    intent.type = "image/*"
                    intent.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
                } else {
                    intent = Intent(Intent.ACTION_PICK, MediaStore.Images.Media.EXTERNAL_CONTENT_URI)
                }
                intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                pendingMediaId[REQ_GALLERY] = id
                activity.startActivityForResult(Intent.createChooser(intent, "选择图片"), REQ_GALLERY)
            } catch (e: Exception) {
                synchronized(lock) { pendingMediaId.remove(REQ_GALLERY) }
                fail(id, GoxNativeHost.ERR_PLATFORM, "拉起相册失败: ${e.message}")
            }
        }
        return GoxNativeHost.PENDING
    }

    /** gallery.pickVideo {source, maxDuration, compressed, quality}. */
    private fun pickVideo(id: String): String {
        activity.runOnUiThread {
            try {
                val intent = Intent(Intent.ACTION_PICK, MediaStore.Video.Media.EXTERNAL_CONTENT_URI)
                intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                pendingMediaId[REQ_VIDEO] = id
                activity.startActivityForResult(intent, REQ_VIDEO)
            } catch (e: Exception) {
                synchronized(lock) { pendingMediaId.remove(REQ_VIDEO) }
                fail(id, GoxNativeHost.ERR_PLATFORM, "拉起视频选择失败: ${e.message}")
            }
        }
        return GoxNativeHost.PENDING
    }

    /**
     * media.save {album, file:{path/uri}} → 写入系统相册。
     * API 29+ 走 MediaStore (不需要权限); 更早版本写公共 Pictures 目录并扫描。
     */
    private fun saveMedia(args: JSONObject, id: String): String {
        val file = args.optJSONObject("file")
        val srcPath = file?.optString("path", "").orEmpty()
        val srcUri = file?.optString("uri", "").orEmpty()
        if (srcPath.isEmpty() && srcUri.isEmpty()) {
            return err(GoxNativeHost.ERR_INVALID_ARG, "media.save: 需要含 path/uri 的 file")
        }
        activity.runOnUiThread {
            try {
                val name = "gox-${System.currentTimeMillis()}.jpg"
                val saved: JSONObject
                if (Build.VERSION.SDK_INT >= 29) {
                    val values = android.content.ContentValues().apply {
                        put(MediaStore.Images.Media.DISPLAY_NAME, name)
                        put(MediaStore.Images.Media.MIME_TYPE, guessMime(srcPath, srcUri))
                        put(MediaStore.Images.Media.RELATIVE_PATH, Environment.DIRECTORY_PICTURES + "/Gox")
                    }
                    val outUri = activity.contentResolver.insert(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, values)
                        ?: throw IllegalStateException("MediaStore 插入失败")
                    copyInto(srcPath, srcUri, activity.contentResolver.openOutputStream(outUri))
                    saved = mediaJson(uri = outUri.toString(), mime = guessMime(srcPath, srcUri))
                } else {
                    val dir = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_PICTURES)
                    val goxDir = File(dir, "Gox")
                    if (!goxDir.exists()) goxDir.mkdirs()
                    val f = File(goxDir, name)
                    copyInto(srcPath, srcUri, FileOutputStream(f))
                    // 刷新媒体库 (API <29; listener 不需要, 系统自己收尾)。
                    android.media.MediaScannerConnection.scanFile(activity, arrayOf(f.absolutePath), null, null)
                    saved = mediaJson(path = f.absolutePath, mime = guessMime(srcPath, srcUri), size = f.length())
                }
                resolve(id, saved)
            } catch (e: Exception) {
                fail(id, GoxNativeHost.ERR_PLATFORM, "保存到相册失败: ${e.message}")
            }
        }
        return GoxNativeHost.PENDING
    }

    /** 把来源 (本地路径或 content uri) 拷进输出流。 */
    private fun copyInto(srcPath: String, srcUri: String, out: java.io.OutputStream?) {
        if (out == null) throw IllegalStateException("输出流打不开")
        val input = when {
            srcUri.isNotEmpty() -> activity.contentResolver.openInputStream(Uri.parse(srcUri))
            srcPath.isNotEmpty() -> java.io.FileInputStream(File(srcPath))
            else -> null
        } ?: throw IllegalStateException("来源打不开 ($srcPath$srcUri)")
        input.use { ins -> out.use { ins.copyTo(it) } }
    }

    private fun guessMime(path: String, uri: String): String = when {
        path.endsWith(".png") || uri.endsWith(".png") -> "image/png"
        else -> "image/jpeg"
    }

    /**
     * media.preview {files:[…], index} → 系统看图。Android 没有多图预览的系统
     * 面板, 预览 index 指定的那一张 (诚实实现, 不假装能翻页)。
     */
    private fun preview(args: JSONObject, id: String): String {
        val files = args.optJSONArray("files")
        if (files == null || files.length() == 0) {
            return err(GoxNativeHost.ERR_INVALID_ARG, "media.preview: 文件列表为空")
        }
        val idx = if (args.has("index") && !args.isNull("index")) args.optInt("index", 0).coerceIn(0, files.length() - 1) else 0
        val f = files.optJSONObject(idx)
        val src = f?.optString("uri", "").orEmpty().ifEmpty { f?.optString("path", "").orEmpty() }
        if (src.isEmpty()) {
            return err(GoxNativeHost.ERR_INVALID_ARG, "media.preview: 文件缺少 path/uri")
        }
        activity.runOnUiThread {
            try {
                val uri = shareUri(src)
                if (uri == null) {
                    fail(id, GoxNativeHost.ERR_UNAVAILABLE, "文件不存在或无法访问: $src")
                    return@runOnUiThread
                }
                val intent = Intent(Intent.ACTION_VIEW)
                intent.setDataAndType(uri, activity.contentResolver.getType(uri) ?: "image/*")
                intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                activity.startActivity(intent)
                resolve(id, JSONObject())
            } catch (e: Exception) {
                fail(id, GoxNativeHost.ERR_PLATFORM, "预览失败: ${e.message}")
            }
        }
        return GoxNativeHost.PENDING
    }

    /**
     * 把本地路径 (或 content uri) 变成可共享的 content uri:
     * 本地文件先拷进 cacheDir/gox/, 再经 [GoxFileProvider] 提供 (API 24+ 禁止
     * file:// 跨应用共享, FileUriExposedException)。
     */
    private fun shareUri(src: String): Uri? {
        if (src.startsWith("content://")) return Uri.parse(src)
        val f = File(src)
        if (!f.exists()) return null
        val cached = newCacheFile(f.extension.ifEmpty { "bin" })
        f.copyTo(cached, overwrite = true)
        return Uri.parse("content://${activity.packageName}.files/gox/${cached.name}")
    }

    private fun newCacheFile(ext: String): File {
        val dir = File(activity.cacheDir, "gox")
        if (!dir.exists()) dir.mkdirs()
        val ts = SimpleDateFormat("yyyyMMdd-HHmmss-SSS", Locale.US).format(Date())
        return File(dir, "$ts.$ext")
    }

    // ===== gx/geo =====

    /**
     * location.get {type, highAccuracy, timeout, maximumAge} → Promise<Location>。
     *
     * 坐标系口径 (gfx/native_geo.go 文件头): Android 系统定位给 wgs84;
     * 脚本要 gcj02 时明确报 platform-error —— 内核不做、宿主也不敢做火星换算。
     */
    private fun getLocation(args: JSONObject, id: String): String {
        val type = args.optString("type", "wgs84")
        if (type == "gcj02") {
            return err(GoxNativeHost.ERR_PLATFORM, "location.get: Android 无法提供 gcj02 坐标 (只支持 wgs84)")
        }
        if (activity.checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) != PackageManager.PERMISSION_GRANTED &&
            activity.checkSelfPermission(Manifest.permission.ACCESS_COARSE_LOCATION) != PackageManager.PERMISSION_GRANTED
        ) {
            return err(GoxNativeHost.ERR_PERMISSION_DENIED, "定位权限未授权 (先调 authorize(\"location\"))")
        }
        val lm = activity.getSystemService(Context.LOCATION_SERVICE) as? LocationManager
            ?: return err(GoxNativeHost.ERR_UNAVAILABLE, "设备没有定位服务")
        if (!lm.isProviderEnabled(LocationManager.GPS_PROVIDER) &&
            !lm.isProviderEnabled(LocationManager.NETWORK_PROVIDER)
        ) {
            return err(GoxNativeHost.ERR_UNAVAILABLE, "定位服务未开启 (飞行模式或系统开关关闭)")
        }
        val provider = if (args.optBoolean("highAccuracy", false)) LocationManager.GPS_PROVIDER
        else pickBestProvider(lm)
        val listener = LocationListener { loc ->
            if (finishLoc(id)) return@LocationListener
            removeListener(id)?.let { lm.removeUpdates(it) }
            resolve(id, locationJson(loc, provider))
        }
        synchronized(lock) { locationListeners[id] = listener; locTimers[id] = false }
        try {
            lm.requestLocationUpdates(provider, 0L, 0f, listener, Looper.getMainLooper())
        } catch (e: Exception) {
            removeListener(id)
            synchronized(lock) { locTimers.remove(id) }
            return err(GoxNativeHost.ERR_PLATFORM, "定位请求失败: ${e.message}")
        }
        // 宿主侧兜底超时: 脚本没传 timeout 也不能让 Promise 悬 30s 以上。
        android.os.Handler(Looper.getMainLooper()).postDelayed({
            val expired = synchronized(lock) { locTimers[id] == false }
            if (expired) {
                synchronized(lock) { locTimers[id] = true }
                removeListener(id)?.let { lm.removeUpdates(it) }
                fail(id, GoxNativeHost.ERR_TIMEOUT, "定位超时 (${LOC_TIMEOUT_MS / 1000}s 内没有可用位置)")
            }
        }, LOC_TIMEOUT_MS)
        return GoxNativeHost.PENDING
    }

    /** 找一个可用的 provider (GPS 优先, 否则 network/passive)。 */
    private fun pickBestProvider(lm: LocationManager): String {
        if (lm.isProviderEnabled(LocationManager.NETWORK_PROVIDER)) return LocationManager.NETWORK_PROVIDER
        return LocationManager.PASSIVE_PROVIDER
    }

    /** 从监听表摘除并返回 (没有 → null)。 */
    private fun removeListener(id: String): LocationListener? =
        synchronized(lock) { locationListeners.remove(id) }

    /** 标记一次定位已结束 (防超时与结果双发); 返回 true 表示已结束过。 */
    private fun finishLoc(id: String): Boolean {
        synchronized(lock) {
            val done = locTimers[id] == true
            locTimers[id] = true
            return done
        }
    }

    /** Location → 结果 JSON (字段与 gfx/native_geo.go locationFromJS 对齐)。 */
    private fun locationJson(loc: Location, provider: String): JSONObject {
        val o = JSONObject()
        o.put("latitude", loc.latitude)
        o.put("longitude", loc.longitude)
        o.put("altitude", loc.altitude)
        o.put("accuracy", loc.accuracy.toDouble())
        o.put("altitudeAccuracy", 0)
        o.put("speed", loc.speed.toDouble())
        o.put("heading", loc.bearing.toDouble())
        o.put("timestamp", loc.time)
        o.put("provider", mapProvider(provider))
        o.put("mocked", if (Build.VERSION.SDK_INT >= 31) loc.isMock else loc.isFromMockProvider)
        o.put("type", "wgs84")
        return o
    }

    private fun mapProvider(p: String): String = when (p) {
        LocationManager.GPS_PROVIDER -> "gps"
        LocationManager.NETWORK_PROVIDER -> "network"
        LocationManager.PASSIVE_PROVIDER -> "passive"
        else -> "unknown"
    }

    /**
     * location.watch → 流式: 同一个 __id 反复回填, 直到 location.unwatch 或致命
     * 错误 (内核 nativeErrFatal 的语义)。错误码 fatal 才停流: 一次定位失败投
     * platform-error 给 sink, 监听继续。
     */
    private fun watchLocation(args: JSONObject, id: String): String {
        val type = args.optString("type", "wgs84")
        if (type == "gcj02") {
            return err(GoxNativeHost.ERR_PLATFORM, "location.watch: Android 无法提供 gcj02 坐标 (只支持 wgs84)")
        }
        if (activity.checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) != PackageManager.PERMISSION_GRANTED &&
            activity.checkSelfPermission(Manifest.permission.ACCESS_COARSE_LOCATION) != PackageManager.PERMISSION_GRANTED
        ) {
            return err(GoxNativeHost.ERR_PERMISSION_DENIED, "定位权限未授权 (先调 authorize(\"location\"))")
        }
        val lm = activity.getSystemService(Context.LOCATION_SERVICE) as? LocationManager
            ?: return err(GoxNativeHost.ERR_UNAVAILABLE, "设备没有定位服务")
        val provider = if (args.optBoolean("highAccuracy", false)) LocationManager.GPS_PROVIDER
        else pickBestProvider(lm)
        val listener = LocationListener { loc ->
            // 流式推送: 不摘除条目, 每次更新都 resolve 同一个 id。
            resolve(id, locationJson(loc, provider))
        }
        synchronized(lock) { locationListeners[id] = listener }
        return try {
            lm.requestLocationUpdates(provider, 1000L, 1f, listener, Looper.getMainLooper())
            GoxNativeHost.PENDING
        } catch (e: Exception) {
            synchronized(lock) { locationListeners.remove(id) }
            err(GoxNativeHost.ERR_PLATFORM, "定位监听失败: ${e.message}")
        }
    }

    /** location.unwatch {__id} → 停掉对应监听 (内核 stopNativeStream 尽力而为)。 */
    private fun unwatchLocation(id: String): String {
        if (id.isNotEmpty()) {
            val lm = activity.getSystemService(Context.LOCATION_SERVICE) as? LocationManager
            val listener = synchronized(lock) { locationListeners.remove(id) }
            if (lm != null && listener != null) {
                try {
                    lm.removeUpdates(listener)
                } catch (e: Exception) {
                    // 停流尽力而为: 失败也不报错 (内核同口径)。
                }
            }
        }
        return "true"
    }

    /** 页面销毁时清理全部监听 (MainActivity.onDestroy 调)。 */
    fun stopAllWatches() {
        val lm = activity.getSystemService(Context.LOCATION_SERVICE) as? LocationManager
        val listeners: List<LocationListener>
        synchronized(lock) {
            listeners = locationListeners.values.toList()
            locationListeners.clear()
            locTimers.clear()
        }
        if (lm != null) {
            for (l in listeners) {
                try {
                    lm.removeUpdates(l)
                } catch (e: Exception) {
                }
            }
        }
    }

    // ===== 上报 (电池 / 网络): 由 MainActivity 注册与触发 =====

    private fun isPowerSave(): Boolean {
        val pm = activity.getSystemService(Context.POWER_SERVICE) as? PowerManager ?: return false
        return pm.isPowerSaveMode
    }

    /**
     * 从 ACTION_BATTERY_CHANGED 的 intent 组装并上报电池 (含充电状态/温度 ——
     * 这些只在广播里有, BatteryManager 的 property 拿不到)。
     */
    fun reportBatteryFrom(intent: Intent) {
        val o = JSONObject()
        o.put("supported", true)
        val level = intent.getIntExtra(BatteryManager.EXTRA_LEVEL, -1)
        val scale = intent.getIntExtra(BatteryManager.EXTRA_SCALE, -1)
        o.put("level", if (level >= 0 && scale > 0) level.toDouble() / scale else -1.0)
        val status = intent.getIntExtra(BatteryManager.EXTRA_STATUS, -1)
        val charging = status == BatteryManager.BATTERY_STATUS_CHARGING ||
            status == BatteryManager.BATTERY_STATUS_FULL
        o.put("charging", charging)
        o.put("chargingType", when (intent.getIntExtra(BatteryManager.EXTRA_PLUGGED, 0)) {
            BatteryManager.BATTERY_PLUGGED_AC -> "ac"
            BatteryManager.BATTERY_PLUGGED_USB -> "usb"
            BatteryManager.BATTERY_PLUGGED_WIRELESS -> "wireless"
            else -> if (charging) "unknown" else "none"
        })
        o.put("temperature", intent.getIntExtra(BatteryManager.EXTRA_TEMPERATURE, -1) / 10.0)
        o.put("lowPowerMode", isPowerSave())
        GoxRuntime.nativeReportBattery(o.toString())
    }

    /** 网络上报 (ConnectivityManager 回调线程安全: nativeReportNetwork 任意线程可调)。 */
    fun reportNetwork(connected: Boolean, caps: NetworkCapabilities?) {
        val o = JSONObject()
        o.put("connected", connected)
        var type = "none"
        var metered = true
        if (caps != null) {
            type = when {
                caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> "wifi"
                caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> "cellular"
                caps.hasTransport(NetworkCapabilities.TRANSPORT_BLUETOOTH) -> "bluetooth"
                caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN) -> "vpn"
                caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> "ethernet"
                else -> "other"
            }
            metered = !caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED)
        }
        o.put("type", type)
        o.put("metered", metered)
        o.put("strength", -1)
        GoxRuntime.nativeReportNetwork(o.toString())
    }

    /** 注册网络回调 (MainActivity.onCreate 调一次)。 */
    fun registerNetworkCallback() {
        val cm = activity.getSystemService(Context.CONNECTIVITY_SERVICE) as? ConnectivityManager ?: return
        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .build()
        val callback = object : android.net.ConnectivityManager.NetworkCallback() {
            private var lastCaps: NetworkCapabilities? = null
            private var available = false

            override fun onAvailable(network: Network) {
                available = true
                reportNetwork(true, lastCaps)
            }

            override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) {
                lastCaps = caps
                if (available) reportNetwork(true, caps)
            }

            override fun onLost(network: Network) {
                available = false
                reportNetwork(false, null)
            }
        }
        try {
            cm.registerNetworkCallback(request, callback)
        } catch (e: Exception) {
            // 网络上报失败不影响其它能力 (缺省值是"未连接", 不是错误)。
        }
    }
}
