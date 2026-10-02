package com.nasfone.client

import android.app.Activity
import android.app.AlertDialog
import android.content.ContentValues
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.os.ParcelFileDescriptor
import android.provider.DocumentsContract
import android.provider.MediaStore
import android.provider.OpenableColumns
import android.view.Gravity
import android.view.View
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import com.nasfone.core.mobileclient.Mobileclient
import com.nasfone.core.mobileclient.Progress
import org.json.JSONArray
import org.json.JSONObject
import kotlin.concurrent.thread

private const val REQ_UPLOAD = 21

/** Browse one server: folders, open/share/download files, and (admin) upload/manage. */
class BrowseActivity : Activity() {
    private lateinit var serverId: String
    private lateinit var server: Server
    private var path = "/"
    private lateinit var title: TextView
    private lateinit var status: TextView
    private lateinit var listBox: LinearLayout
    private lateinit var adminRow: LinearLayout

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        Lang.init(this)
        serverId = intent.getStringExtra("server") ?: return finish()
        server = Servers.get(this, serverId) ?: return finish()
        path = savedInstanceState?.getString("path") ?: "/"

        val (root, col) = page()
        title = text("", 18f, bold = true).also { col.addView(it) }
        status = text("", 12f).also { col.addView(it) }
        adminRow = hbox()
        adminRow.addView(button(L("⬆ Tải lên", "⬆ Upload")) { pickUpload() }, weighted())
        adminRow.addView(button(L("＋ Thư mục", "＋ Folder")) { newFolder() }, weighted())
        col.addView(adminRow)
        listBox = vbox().also { col.addView(it) }
        setContentView(root)
        load()
    }

    override fun onSaveInstanceState(out: Bundle) {
        super.onSaveInstanceState(out)
        out.putString("path", path)
    }

    @Deprecated("Back handling without AndroidX")
    override fun onBackPressed() {
        if (path != "/") {
            path = parentOf(path)
            load()
        } else {
            @Suppress("DEPRECATION")
            super.onBackPressed()
        }
    }

    private fun parentOf(p: String) = p.trimEnd('/').substringBeforeLast('/').ifEmpty { "/" }
    private fun join(dir: String, name: String) = (if (dir.endsWith("/")) dir else "$dir/") + name

    private fun load() {
        title.text = server.host + if (path == "/") "" else "  ›  " + path.trimStart('/').replace("/", " › ")
        status.text = L("Đang tải…", "Loading…")
        listBox.removeAllViews()
        thread {
            try {
                val info = Servers.connect(this, serverId)
                server = Servers.get(this, serverId) ?: server
                val sess = Servers.session(this, serverId)
                val entries = JSONArray(sess.list(path))
                val quota = try { JSONObject(sess.quota()) } catch (_: Exception) { null }
                runOnUiThread { render(entries, info, quota) }
            } catch (e: Exception) {
                runOnUiThread {
                    status.text = "⚠ " + friendly(e)
                    listBox.addView(button(L("Thử lại", "Retry")) { load() })
                }
            }
        }
    }

    private fun render(entries: JSONArray, info: JSONObject, quota: JSONObject?) {
        val via = when (info.optString("via")) {
            "lan" -> "LAN"; "tailnet" -> "tailnet"; else -> "Internet"
        }
        val free = quota?.optLong("avail", -1L)?.takeIf { it >= 0 }?.let { " • " + L("trống ", "free ") + humanSize(it) } ?: ""
        status.text = L("Qua ", "Via ") + via + free + " • " + (if (server.canWrite) "ADMIN" else L("USER (chỉ xem)", "USER (view only)"))
        adminRow.visibility = if (server.canWrite) View.VISIBLE else View.GONE
        listBox.removeAllViews()
        if (path != "/") listBox.addView(row("⬆", "..", "") { path = parentOf(path); load() })
        if (entries.length() == 0) listBox.addView(text(L("Thư mục trống.", "This folder is empty."), 14f).apply { setPadding(0, dp(12), 0, 0) })
        for (i in 0 until entries.length()) {
            val e = entries.getJSONObject(i)
            val name = e.getString("name")
            val p = e.getString("path")
            if (e.getBoolean("dir")) {
                listBox.addView(row("📁", name, shortDate(e.optLong("mtime")), onLong = { menu(e) }) { path = p; load() })
            } else {
                val sub = humanSize(e.optLong("size")) + "  •  " + shortDate(e.optLong("mtime"))
                listBox.addView(row(iconFor(e.optString("mime")), name, sub, onLong = { menu(e) }) { open(e) })
            }
        }
    }

    private fun iconFor(mime: String) = when {
        mime.startsWith("image/") -> "🖼"
        mime.startsWith("video/") -> "🎞"
        mime.startsWith("audio/") -> "🎵"
        mime == "application/pdf" -> "📕"
        mime.contains("zip") || mime.contains("compressed") -> "🗜"
        else -> "📄"
    }

    private fun row(icon: String, name: String, sub: String, onLong: (() -> Unit)? = null, onClick: () -> Unit): View {
        val r = hbox().apply {
            setPadding(0, dp(10), 0, dp(10))
            isClickable = true
            setOnClickListener { onClick() }
            onLong?.let { f -> setOnLongClickListener { f(); true } }
        }
        r.addView(text(icon, 22f).apply { setPadding(0, 0, dp(12), 0); gravity = Gravity.CENTER })
        val c = vbox()
        c.addView(text(name, 15f))
        if (sub.isNotEmpty()) c.addView(text(sub, 12f))
        r.addView(c, weighted())
        return r
    }

    // ------------------------------------------------------------ file actions

    private fun docUri(p: String) = DocumentsContract.buildDocumentUri(NasProvider.AUTHORITY, NasProvider.docId(serverId, p))

    private fun open(e: JSONObject) {
        val uri = docUri(e.getString("path"))
        try {
            startActivity(Intent.createChooser(Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(uri, e.optString("mime", "*/*"))
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            }, e.getString("name")))
        } catch (_: Exception) {
            toast(L("Không có app nào mở được loại file này.", "No app can open this kind of file."))
        }
    }

    private fun share(e: JSONObject) {
        startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).apply {
            type = e.optString("mime", "*/*")
            putExtra(Intent.EXTRA_STREAM, docUri(e.getString("path")))
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }, e.getString("name")))
    }

    private fun menu(e: JSONObject) {
        val dir = e.getBoolean("dir")
        val items = mutableListOf<Pair<String, () -> Unit>>()
        if (!dir) {
            items += L("Mở", "Open") to { open(e) }
            items += L("Chia sẻ", "Share") to { share(e) }
            items += L("Tải về máy (Download)", "Save to Downloads") to { saveToDownloads(e) }
        }
        if (server.canWrite) {
            items += L("Đổi tên", "Rename") to { rename(e) }
            items += L("Xoá", "Delete") to { delete(e) }
        }
        if (items.isEmpty()) return
        AlertDialog.Builder(this).setTitle(e.getString("name"))
            .setItems(items.map { it.first }.toTypedArray()) { _, i -> items[i].second() }.show()
    }

    private fun progress(label: String) = object : Progress {
        override fun onProgress(done: Long, total: Long) {
            val pct = if (total > 0) " ${done * 100 / total}%" else " " + humanSize(done)
            runOnUiThread { status.text = label + pct }
        }
    }

    private fun saveToDownloads(e: JSONObject) {
        val name = e.getString("name")
        thread {
            try {
                val values = ContentValues().apply {
                    put(MediaStore.Downloads.DISPLAY_NAME, name)
                    put(MediaStore.Downloads.MIME_TYPE, e.optString("mime", "application/octet-stream"))
                    put(MediaStore.Downloads.RELATIVE_PATH, "Download/NASfone")
                    put(MediaStore.Downloads.IS_PENDING, 1)
                }
                val uri = contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values) ?: throw IllegalStateException("MediaStore")
                try {
                    val pfd = contentResolver.openFileDescriptor(uri, "w") ?: throw IllegalStateException("open")
                    Servers.session(this, serverId).download(e.getString("path"), pfd.detachFd().toLong(), progress(L("Đang tải về", "Downloading")))
                    contentResolver.update(uri, ContentValues().apply { put(MediaStore.Downloads.IS_PENDING, 0) }, null, null)
                } catch (x: Exception) {
                    contentResolver.delete(uri, null, null)
                    throw x
                }
                toast(L("Đã lưu vào Download/NASfone/$name", "Saved to Download/NASfone/$name"))
                runOnUiThread { load() }
            } catch (x: Exception) {
                toast(L("Tải về lỗi: ", "Download failed: ") + friendly(x))
                runOnUiThread { load() }
            }
        }
    }

    private fun rename(e: JSONObject) {
        val et = EditText(this).apply { setText(e.getString("name")); setSelection(0, text.lastIndexOf('.').takeIf { it > 0 } ?: text.length) }
        AlertDialog.Builder(this).setTitle(L("Đổi tên", "Rename")).setView(et)
            .setPositiveButton("OK") { _, _ ->
                val n = et.text.toString().trim()
                if (n.isEmpty() || n.contains('/')) return@setPositiveButton
                background(L("Đã đổi tên", "Renamed")) { Servers.session(this, serverId).move(e.getString("path"), join(path, n)) }
            }
            .setNegativeButton(L("Huỷ", "Cancel"), null).show()
    }

    private fun delete(e: JSONObject) {
        AlertDialog.Builder(this)
            .setTitle(L("Xoá \"${e.getString("name")}\"?", "Delete \"${e.getString("name")}\"?"))
            .setMessage(if (e.getBoolean("dir")) L("Toàn bộ nội dung trong thư mục cũng bị xoá.", "Everything inside the folder is deleted too.") else null)
            .setPositiveButton(L("Xoá", "Delete")) { _, _ ->
                background(L("Đã xoá", "Deleted")) { Servers.session(this, serverId).delete(e.getString("path")) }
            }
            .setNegativeButton(L("Huỷ", "Cancel"), null).show()
    }

    private fun newFolder() {
        val et = EditText(this).apply { hint = L("Tên thư mục", "Folder name") }
        AlertDialog.Builder(this).setTitle(L("Thư mục mới", "New folder")).setView(et)
            .setPositiveButton("OK") { _, _ ->
                val n = et.text.toString().trim()
                if (n.isEmpty() || n.contains('/')) return@setPositiveButton
                background(L("Đã tạo thư mục", "Folder created")) { Servers.session(this, serverId).mkdir(join(path, n)) }
            }
            .setNegativeButton(L("Huỷ", "Cancel"), null).show()
    }

    private fun background(done: String, f: () -> Unit) {
        thread {
            try {
                f()
                toast(done)
            } catch (e: Exception) {
                toast(friendly(e))
            }
            NasProvider.notifyChildren(this, serverId, path)
            runOnUiThread { load() }
        }
    }

    // ------------------------------------------------------------ upload

    private fun pickUpload() {
        @Suppress("DEPRECATION")
        startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
            addCategory(Intent.CATEGORY_OPENABLE)
            type = "*/*"
            putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
        }, REQ_UPLOAD)
    }

    @Deprecated("Activity result API needs AndroidX; this app uses the platform Activity")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode != REQ_UPLOAD || resultCode != RESULT_OK || data == null) return
        val uris = mutableListOf<Uri>()
        data.clipData?.let { c -> for (i in 0 until c.itemCount) uris += c.getItemAt(i).uri } ?: data.data?.let { uris += it }
        if (uris.isNotEmpty()) uploadAll(uris, path)
    }

    private fun nameOf(uri: Uri): Pair<String, Long> {
        contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c ->
            if (c.moveToFirst()) {
                val n = c.getString(0) ?: "file"
                val s = if (c.isNull(1)) -1L else c.getLong(1)
                return n to s
            }
        }
        return (uri.lastPathSegment ?: "file") to -1L
    }

    /** Uploads one by one; on a name clash asks overwrite / keep both / skip (optionally for all). */
    private fun uploadAll(uris: List<Uri>, dir: String) {
        thread {
            var sticky: String? = null // mode chosen with "apply to all"
            var ok = 0
            var skipped = 0
            val sess = Servers.session(this, serverId)
            for ((i, uri) in uris.withIndex()) {
                val (name, size) = nameOf(uri)
                val target = join(dir, name)
                val label = L("Tải lên", "Uploading") + " ${i + 1}/${uris.size}: $name"
                fun send(mode: String): String {
                    val pfd: ParcelFileDescriptor = contentResolver.openFileDescriptor(uri, "r") ?: throw IllegalStateException("open")
                    return sess.upload(target, pfd.detachFd().toLong(), size, mode, progress(label))
                }
                try {
                    val result = try {
                        send(sticky ?: Mobileclient.ModeFail)
                    } catch (e: Exception) {
                        if (e.message?.startsWith("exists:") != true) throw e
                        val mode = askConflict(name).also { (m, all) -> if (all) sticky = m }.first
                        if (mode == Mobileclient.ModeSkip) "" else send(mode)
                    }
                    if (result.isEmpty()) skipped++ else ok++
                } catch (e: Exception) {
                    toast("$name: " + friendly(e))
                }
            }
            toast(L("Đã tải lên $ok file", "Uploaded $ok file(s)") + if (skipped > 0) L(", bỏ qua $skipped", ", skipped $skipped") else "")
            NasProvider.notifyChildren(this, serverId, dir)
            runOnUiThread { load() }
        }
    }

    /** Blocks the upload thread until the user answers. Returns (mode, applyToAll). */
    private fun askConflict(name: String): Pair<String, Boolean> {
        val lock = Object()
        var answer: Pair<String, Boolean>? = null
        runOnUiThread {
            val all = CheckBox(this).apply { text = L("Áp dụng cho các file trùng tiếp theo", "Apply to the next clashes too") }
            val box = vbox().apply { setPadding(dp(20), dp(8), dp(20), 0); addView(all) }
            fun pick(m: String) = synchronized(lock) { answer = m to all.isChecked; lock.notifyAll() }
            AlertDialog.Builder(this)
                .setTitle(L("\"$name\" đã có trên server", "\"$name\" already exists on the server"))
                .setView(box)
                .setCancelable(false)
                .setPositiveButton(L("Ghi đè", "Overwrite")) { _, _ -> pick(Mobileclient.ModeOverwrite) }
                .setNeutralButton(L("Giữ cả hai", "Keep both")) { _, _ -> pick(Mobileclient.ModeKeepBoth) }
                .setNegativeButton(L("Bỏ qua", "Skip")) { _, _ -> pick(Mobileclient.ModeSkip) }
                .show()
        }
        synchronized(lock) {
            while (answer == null) lock.wait()
        }
        return answer!!
    }
}
