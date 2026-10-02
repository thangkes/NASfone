package com.nasfone.server

import android.content.Context
import android.os.Environment
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.security.MessageDigest
import java.util.zip.ZipEntry
import java.util.zip.ZipInputStream
import java.util.zip.ZipOutputStream

/**
 * Sao lưu / khôi phục toàn bộ cấu hình server: trạng thái Tailscale (tsnet/), khoá định
 * danh + thiết bị đã ghép (pair/), phiên đăng nhập (auth.json) và cài đặt (Prefs).
 *
 * Android xoá dữ liệu app khi gỡ, nhưng file sao lưu nằm ở thư mục Download nên vẫn còn:
 * cài lại app là được hỏi khôi phục. Server tự sao lưu định kỳ khi cấu hình thay đổi
 * (autoBackup), nên lúc nào cũng có bản mới nhất. File chứa khoá bí mật → nằm NGOÀI thư mục
 * chia sẻ của NAS.
 */
object ConfigBackup {
    private val PARTS = listOf("tsnet", "pair", "auth.json")
    private const val PREFS_ENTRY = "prefs.json"
    private var lastSig: String? = null

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

    /** Ghi file sao lưu ngay. */
    @Synchronized
    fun export(ctx: Context): File {
        val snap = snapshot(ctx) ?: throw IllegalStateException("configuration is being written, try again")
        write(snap)
        lastSig = signature(snap)
        return file()
    }

    /**
     * Gọi định kỳ từ service (luồng nền): chỉ ghi khi cấu hình đổi so với lần trước, hoặc
     * file sao lưu đã mất. Bỏ qua lượt này nếu đang có file ghi dở.
     */
    @Synchronized
    fun autoBackup(ctx: Context) {
        if (!File(ctx.filesDir, "tsnet/tailscaled.state").isFile) return // chưa có gì đáng giữ
        val snap = snapshot(ctx) ?: return
        val sig = signature(snap)
        if (sig == lastSig && file().isFile) return
        try {
            write(snap)
            lastSig = sig
        } catch (e: Exception) {
            Core.log(L("Tự sao lưu cấu hình lỗi: $e", "Automatic configuration backup failed: $e"))
        }
    }

    /** Đọc toàn bộ cấu hình vào bộ nhớ (tên → nội dung). null nếu file JSON đang ghi dở. */
    private fun snapshot(ctx: Context): Map<String, ByteArray>? {
        val out = sortedMapOf<String, ByteArray>()
        for (name in PARTS) {
            val f = File(ctx.filesDir, name)
            if (f.exists()) collect(ctx.filesDir, f, out)
        }
        for ((name, bytes) in out) {
            if (name.endsWith(".json") && !validJson(bytes)) return null
        }
        val prefs = JSONObject()
        for ((k, v) in ctx.getSharedPreferences("nasfone", Context.MODE_PRIVATE).all.toSortedMap()) {
            if (k.startsWith("update")) continue // update-check bookkeeping, not configuration
            if (v is String || v is Boolean || v is Int || v is Long) prefs.put(k, v)
        }
        out[PREFS_ENTRY] = prefs.toString().toByteArray()
        return out
    }

    private fun collect(base: File, f: File, out: MutableMap<String, ByteArray>) {
        if (f.isDirectory) {
            f.listFiles()?.forEach { collect(base, it, out) }
            return
        }
        // Nhật ký của Tailscale đổi liên tục và không cần để khôi phục.
        if (f.name.startsWith("tailscaled.log") && f.name.endsWith(".txt")) return
        out[f.relativeTo(base).invariantSeparatorsPath] = f.readBytes()
    }

    private fun validJson(b: ByteArray): Boolean {
        val s = b.toString(Charsets.UTF_8).trim()
        return try {
            if (s.startsWith("[")) JSONArray(s) else JSONObject(s)
            true
        } catch (e: Exception) {
            false
        }
    }

    private fun signature(snap: Map<String, ByteArray>): String {
        val md = MessageDigest.getInstance("SHA-256")
        for ((name, bytes) in snap) {
            md.update(name.toByteArray()); md.update(0); md.update(bytes); md.update(0)
        }
        return md.digest().joinToString("") { "%02x".format(it) }
    }

    private fun write(snap: Map<String, ByteArray>) {
        val out = file()
        out.parentFile?.mkdirs()
        val tmp = File(out.path + ".part")
        ZipOutputStream(tmp.outputStream().buffered()).use { zip ->
            for ((name, bytes) in snap) {
                zip.putNextEntry(ZipEntry(name))
                zip.write(bytes)
                zip.closeEntry()
            }
        }
        if (!tmp.renameTo(out)) {
            out.delete()
            if (!tmp.renameTo(out)) throw IllegalStateException("cannot write ${out.path}")
        }
    }

    /** Khôi phục từ file sao lưu. Chỉ gọi khi server đang dừng. File được giữ lại (tự sao lưu ghi đè sau). */
    @Synchronized
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
        lastSig = null
    }
}
