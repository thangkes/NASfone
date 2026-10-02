package com.nasfone.server

import android.content.Context
import android.os.Environment
import org.json.JSONObject
import java.io.File
import java.util.zip.ZipEntry
import java.util.zip.ZipInputStream
import java.util.zip.ZipOutputStream

/**
 * Sao lưu / khôi phục toàn bộ cấu hình server: trạng thái Tailscale (tsnet/), khoá định
 * danh + thiết bị đã ghép (pair/), phiên đăng nhập (auth.json) và cài đặt (Prefs).
 *
 * Dùng khi phải gỡ app rồi cài lại (ví dụ đổi khoá ký APK): Android xoá dữ liệu app khi gỡ,
 * nhưng file sao lưu nằm ở thư mục Download nên vẫn còn. File chứa khoá bí mật → nằm NGOÀI
 * thư mục chia sẻ của NAS và bị xoá ngay sau khi khôi phục.
 */
object ConfigBackup {
    private val PARTS = listOf("tsnet", "pair", "auth.json")
    private const val PREFS_ENTRY = "prefs.json"

    fun file(): File = File(
        Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS),
        "NASfone-config-backup.zip"
    )

    /** App mới cài (chưa có trạng thái) và có sẵn file sao lưu → nên hỏi khôi phục. */
    fun shouldOfferRestore(ctx: Context): Boolean {
        val tsnet = File(ctx.filesDir, "tsnet")
        val fresh = !tsnet.exists() || (tsnet.listFiles()?.isEmpty() ?: true)
        return fresh && file().isFile
    }

    /** Ghi file sao lưu. Chỉ gọi khi server đã dừng để trạng thái không đổi giữa chừng. */
    fun export(ctx: Context): File {
        val out = file()
        out.parentFile?.mkdirs()
        val tmp = File(out.path + ".part")
        ZipOutputStream(tmp.outputStream().buffered()).use { zip ->
            for (name in PARTS) {
                val f = File(ctx.filesDir, name)
                if (f.exists()) addTree(zip, ctx.filesDir, f)
            }
            val prefs = JSONObject()
            for ((k, v) in ctx.getSharedPreferences("nasfone", Context.MODE_PRIVATE).all) {
                if (k.startsWith("update")) continue // update-check bookkeeping, not configuration
                if (v is String || v is Boolean || v is Int || v is Long) prefs.put(k, v)
            }
            zip.putNextEntry(ZipEntry(PREFS_ENTRY))
            zip.write(prefs.toString().toByteArray())
            zip.closeEntry()
        }
        if (!tmp.renameTo(out)) {
            out.delete()
            if (!tmp.renameTo(out)) throw IllegalStateException("cannot write ${out.path}")
        }
        return out
    }

    private fun addTree(zip: ZipOutputStream, base: File, f: File) {
        if (f.isDirectory) {
            f.listFiles()?.forEach { addTree(zip, base, it) }
            return
        }
        zip.putNextEntry(ZipEntry(f.relativeTo(base).invariantSeparatorsPath))
        f.inputStream().use { it.copyTo(zip) }
        zip.closeEntry()
    }

    /** Khôi phục rồi xoá file sao lưu. Chỉ gọi khi server đang dừng. */
    fun restore(ctx: Context) {
        val src = file()
        val base = ctx.filesDir.canonicalFile
        for (name in PARTS) File(base, name).deleteRecursively()
        var prefsJson: String? = null
        ZipInputStream(src.inputStream().buffered()).use { zip ->
            while (true) {
                val e = zip.nextEntry ?: break
                if (e.isDirectory) continue
                if (e.name == PREFS_ENTRY) {
                    prefsJson = zip.readBytes().toString(Charsets.UTF_8)
                    continue
                }
                val dest = File(base, e.name).canonicalFile
                // Chỉ nhận file thuộc các phần đã biết, không cho đường dẫn thoát ra ngoài.
                val top = e.name.substringBefore('/')
                if (top !in PARTS || !dest.path.startsWith(base.path + File.separator)) continue
                dest.parentFile?.mkdirs()
                dest.outputStream().use { zip.copyTo(it) }
            }
        }
        prefsJson?.let { js ->
            val o = JSONObject(js)
            val ed = ctx.getSharedPreferences("nasfone", Context.MODE_PRIVATE).edit()
            for (k in o.keys()) {
                when (val v = o.get(k)) {
                    is Boolean -> ed.putBoolean(k, v)
                    is Int -> ed.putInt(k, v)
                    is Long -> ed.putLong(k, v)
                    is String -> ed.putString(k, v)
                }
            }
            ed.commit()
        }
        src.delete()
    }
}
