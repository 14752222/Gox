package com.gox

import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.database.MatrixCursor
import android.net.Uri
import android.os.ParcelFileDescriptor
import android.provider.OpenableColumns
import java.io.File
import java.io.FileNotFoundException

/**
 * 极简文件提供器 (framework 自带 ContentProvider, 零 androidx 依赖)。
 *
 * ## 为什么需要它
 *
 * minSdk 24 之后 Android **禁止** file:// 跨应用共享 (FileUriExposedException):
 * 相机 EXTRA_OUTPUT、分享 EXTRA_STREAM、看图 ACTION_VIEW 都必须给 content://。
 * androidx 的 FileProvider 不可用 (壳工程零依赖), 所以自写一个最小实现 ——
 * 只服务 cacheDir/gox/ 下的文件, 只读。
 *
 *   content://<applicationId>.files/gox/<文件名>   →  File(cacheDir, "gox/<文件名>")
 *
 * authorities 必须与应用 id 绑定 (AndroidManifest: `${applicationId}.files`),
 * 构造 URI 的地方是 GoxNativeHostImpl.shareUri。
 */
class GoxFileProvider : ContentProvider() {

    override fun onCreate(): Boolean = true

    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor {
        if (mode != "r") {
            throw FileNotFoundException("GoxFileProvider 只读 (mode=$mode)")
        }
        val name = checkName(uri)
        val dir = File(context!!.cacheDir, "gox")
        val f = File(dir, name)
        if (!f.exists()) {
            throw FileNotFoundException("没有这个文件: $name")
        }
        return ParcelFileDescriptor.open(f, ParcelFileDescriptor.MODE_READ_ONLY)
    }

    /** 只允许纯文件名: 挡住路径穿越 (.. 与 /)。 */
    private fun checkName(uri: Uri): String {
        val segs = uri.pathSegments
        if (segs.size != 2 || segs[0] != "gox") {
            throw FileNotFoundException("URI 形态必须是 gox/<文件名>: $uri")
        }
        val name = segs[1]
        if (name.isEmpty() || name.contains('/') || name.contains('\\') || name.contains("..")) {
            throw FileNotFoundException("非法文件名: $name")
        }
        return name
    }

    override fun getType(uri: Uri): String {
        val name = checkName(uri)
        val ext = name.substringAfterLast('.', "").lowercase()
        return when (ext) {
            "jpg", "jpeg" -> "image/jpeg"
            "png" -> "image/png"
            "gif" -> "image/gif"
            "webp" -> "image/webp"
            "mp4" -> "video/mp4"
            "txt" -> "text/plain"
            else -> "application/octet-stream"
        }
    }

    /** 查询文件名与大小 (部分应用打开前会查 OpenableColumns)。 */
    override fun query(
        uri: Uri,
        projection: Array<out String>?,
        selection: String?,
        selectionArgs: Array<out String>?,
        sortOrder: String?,
    ): Cursor? {
        val name = checkName(uri)
        val f = File(File(context!!.cacheDir, "gox"), name)
        val cols = projection ?: arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE)
        val row = MatrixCursor(cols)
        row.addRow(cols.map { col ->
            when (col) {
                OpenableColumns.DISPLAY_NAME -> name
                OpenableColumns.SIZE -> if (f.exists()) f.length() else 0L
                else -> null
            }
        })
        return row
    }

    // 其余写操作不支持 (只读提供器)。
    override fun insert(uri: Uri, values: ContentValues?): Uri? = null
    override fun update(
        uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?,
    ): Int = 0
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int = 0
}
