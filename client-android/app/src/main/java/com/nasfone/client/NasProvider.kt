package com.nasfone.client

import android.content.Context
import android.database.Cursor
import android.database.MatrixCursor
import android.os.Bundle
import android.os.CancellationSignal
import android.os.Handler
import android.os.HandlerThread
import android.os.ParcelFileDescriptor
import android.provider.DocumentsContract
import android.provider.DocumentsContract.Document
import android.provider.DocumentsContract.Root
import android.provider.DocumentsProvider
import com.nasfone.core.mobileclient.Mobileclient
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.FileNotFoundException
import java.security.MessageDigest
import kotlin.concurrent.thread

/**
 * Shows every paired server as a location in the Android Files app and in
 * every app's file picker, so files can be opened from and saved to the NAS
 * directly (the Android counterpart of the Windows drive letter).
 *
 * Document IDs are "<server id>:<server path>".
 */
class NasProvider : DocumentsProvider() {
    companion object {
        const val AUTHORITY = "com.nasfone.client.documents"

        fun docId(server: String, path: String) = "$server:$path"

        fun notifyRoots(ctx: Context) {
            ctx.contentResolver.notifyChange(DocumentsContract.buildRootsUri(AUTHORITY), null)
        }

        fun notifyChildren(ctx: Context, server: String, path: String) {
            ctx.contentResolver.notifyChange(DocumentsContract.buildChildDocumentsUri(AUTHORITY, docId(server, path)), null)
        }

        private val ROOT_COLS = arrayOf(Root.COLUMN_ROOT_ID, Root.COLUMN_FLAGS, Root.COLUMN_TITLE, Root.COLUMN_SUMMARY,
            Root.COLUMN_DOCUMENT_ID, Root.COLUMN_ICON, Root.COLUMN_MIME_TYPES)
        private val DOC_COLS = arrayOf(Document.COLUMN_DOCUMENT_ID, Document.COLUMN_DISPLAY_NAME, Document.COLUMN_MIME_TYPE,
            Document.COLUMN_SIZE, Document.COLUMN_LAST_MODIFIED, Document.COLUMN_FLAGS)
    }

    private val closer by lazy { Handler(HandlerThread("nasfone-provider").apply { start() }.looper) }

    override fun onCreate(): Boolean {
        Lang.init(context!!)
        return true
    }

    private fun ctx() = context!!

    private fun split(docId: String): Pair<String, String> {
        val i = docId.indexOf(':')
        if (i <= 0) throw FileNotFoundException(docId)
        return docId.substring(0, i) to docId.substring(i + 1).ifEmpty { "/" }
    }

    private fun server(id: String): Server =
        Servers.get(ctx(), id)?.takeIf { !it.revoked } ?: throw FileNotFoundException(L("Server không còn ghép đôi", "Server no longer paired"))

    private fun join(dir: String, name: String) = (if (dir.endsWith("/")) dir else "$dir/") + name

    // ------------------------------------------------------------ queries

    override fun queryRoots(projection: Array<out String>?): Cursor {
        val c = MatrixCursor(projection ?: ROOT_COLS)
        for (s in Servers.list(ctx()).filter { !it.revoked }) {
            var flags = Root.FLAG_SUPPORTS_IS_CHILD
            if (s.canWrite) flags = flags or Root.FLAG_SUPPORTS_CREATE
            c.newRow()
                .add(Root.COLUMN_ROOT_ID, s.id)
                .add(Root.COLUMN_FLAGS, flags)
                .add(Root.COLUMN_TITLE, "NASfone")
                .add(Root.COLUMN_SUMMARY, s.host + if (s.canWrite) "" else L(" (chỉ xem)", " (view only)"))
                .add(Root.COLUMN_DOCUMENT_ID, docId(s.id, "/"))
                .add(Root.COLUMN_ICON, R.mipmap.ic_launcher)
                .add(Root.COLUMN_MIME_TYPES, "*/*")
        }
        c.setNotificationUri(ctx().contentResolver, DocumentsContract.buildRootsUri(AUTHORITY))
        return c
    }

    private fun addRow(c: MatrixCursor, sid: String, e: JSONObject, canWrite: Boolean) {
        val dir = e.optBoolean("dir")
        var flags = 0
        if (canWrite) {
            flags = flags or Document.FLAG_SUPPORTS_DELETE or Document.FLAG_SUPPORTS_RENAME
            flags = flags or if (dir) Document.FLAG_DIR_SUPPORTS_CREATE else Document.FLAG_SUPPORTS_WRITE
        }
        val path = e.getString("path")
        c.newRow()
            .add(Document.COLUMN_DOCUMENT_ID, docId(sid, path))
            .add(Document.COLUMN_DISPLAY_NAME, if (path == "/") Servers.get(ctx(), sid)?.host ?: "NASfone" else e.optString("name"))
            .add(Document.COLUMN_MIME_TYPE, if (dir) Document.MIME_TYPE_DIR else e.optString("mime", "application/octet-stream"))
            .add(Document.COLUMN_SIZE, if (dir) null else e.optLong("size"))
            .add(Document.COLUMN_LAST_MODIFIED, e.optLong("mtime").takeIf { it > 0 })
            .add(Document.COLUMN_FLAGS, flags)
    }

    override fun queryDocument(documentId: String, projection: Array<out String>?): Cursor {
        val (sid, path) = split(documentId)
        val s = server(sid)
        val c = MatrixCursor(projection ?: DOC_COLS)
        if (path == "/") {
            addRow(c, sid, JSONObject().put("path", "/").put("dir", true), s.canWrite) // no network needed
        } else {
            val e = try { JSONObject(Servers.session(ctx(), sid).stat(path)) } catch (x: Exception) { throw FileNotFoundException(friendly(x)) }
            addRow(c, sid, e, s.canWrite)
        }
        return c
    }

    /** A cursor that can carry an error message for the Files app to show. */
    private class ResultCursor(cols: Array<out String>) : MatrixCursor(cols) {
        var extrasBundle = Bundle.EMPTY
        override fun getExtras(): Bundle = extrasBundle
    }

    override fun queryChildDocuments(parentDocumentId: String, projection: Array<out String>?, sortOrder: String?): Cursor {
        val (sid, path) = split(parentDocumentId)
        val s = server(sid)
        val c = ResultCursor(projection ?: DOC_COLS)
        try {
            Servers.connect(ctx(), sid)
            val arr = JSONArray(Servers.session(ctx(), sid).list(path))
            for (i in 0 until arr.length()) addRow(c, sid, arr.getJSONObject(i), s.canWrite)
        } catch (x: Exception) {
            c.extrasBundle = Bundle().apply { putString(DocumentsContract.EXTRA_ERROR, friendly(x)) }
        }
        c.setNotificationUri(ctx().contentResolver, DocumentsContract.buildChildDocumentsUri(AUTHORITY, parentDocumentId))
        return c
    }

    override fun isChildDocument(parentDocumentId: String, documentId: String): Boolean {
        val (ps, pp) = split(parentDocumentId)
        val (ds, dp) = split(documentId)
        return ps == ds && (pp == "/" || dp == pp || dp.startsWith(pp.trimEnd('/') + "/"))
    }

    // ------------------------------------------------------------ content

    private fun cacheFile(documentId: String): File {
        val h = MessageDigest.getInstance("SHA-1").digest(documentId.toByteArray()).joinToString("") { "%02x".format(it) }
        return File(File(ctx().cacheDir, "docs").apply { mkdirs() }, h)
    }

    /** Downloads into the cache (reused while size and date match) so apps get a seekable file. */
    private fun fetch(sid: String, path: String, documentId: String, signal: CancellationSignal?): File {
        val sess = Servers.session(ctx(), sid)
        val st = JSONObject(sess.stat(path))
        val f = cacheFile(documentId)
        val mtime = st.optLong("mtime")
        if (f.isFile && f.length() == st.optLong("size") && f.lastModified() == mtime) return f
        val tmp = File(f.path + ".part")
        val pfd = ParcelFileDescriptor.open(tmp, ParcelFileDescriptor.MODE_CREATE or ParcelFileDescriptor.MODE_TRUNCATE or ParcelFileDescriptor.MODE_WRITE_ONLY)
        signal?.throwIfCanceled()
        sess.download(path, pfd.detachFd().toLong(), null)
        signal?.throwIfCanceled()
        if (!tmp.renameTo(f)) {
            f.delete()
            tmp.renameTo(f)
        }
        if (mtime > 0) f.setLastModified(mtime)
        trimCache()
        return f
    }

    /** Keeps the cache under ~1 GB, oldest first. */
    private fun trimCache() {
        val files = File(ctx().cacheDir, "docs").listFiles()?.sortedBy { it.lastModified() } ?: return
        var total = files.sumOf { it.length() }
        for (f in files) {
            if (total < 1L shl 30) break
            total -= f.length()
            f.delete()
        }
    }

    override fun openDocument(documentId: String, mode: String, signal: CancellationSignal?): ParcelFileDescriptor {
        val (sid, path) = split(documentId)
        val s = server(sid)
        val writing = mode.contains('w')
        if (!writing) {
            val f = try { fetch(sid, path, documentId, signal) } catch (x: Exception) { throw FileNotFoundException(friendly(x)) }
            return ParcelFileDescriptor.open(f, ParcelFileDescriptor.MODE_READ_ONLY)
        }
        if (!s.canWrite) throw FileNotFoundException(L("Máy này chỉ có quyền xem", "This phone has view-only access"))
        // Writes go to a local file; when the app closes it, the file is uploaded.
        val local = if (mode.contains('r') || !mode.contains('t')) {
            try { fetch(sid, path, documentId, signal) } catch (_: Exception) { cacheFile(documentId).apply { writeBytes(ByteArray(0)) } }
        } else cacheFile(documentId)
        val pfdMode = ParcelFileDescriptor.parseMode(mode)
        return ParcelFileDescriptor.open(local, pfdMode, closer) { err ->
            if (err != null) return@open
            thread {
                try {
                    val src = ParcelFileDescriptor.open(local, ParcelFileDescriptor.MODE_READ_ONLY)
                    Servers.session(ctx(), sid).upload(path, src.detachFd().toLong(), local.length(), Mobileclient.ModeOverwrite, null)
                    local.setLastModified(0) // force a fresh download next time
                    notifyChildren(ctx(), sid, path.substringBeforeLast('/').ifEmpty { "/" })
                } catch (_: Exception) {
                }
            }
        }
    }

    // ------------------------------------------------------------ changes

    override fun createDocument(parentDocumentId: String, mimeType: String, displayName: String): String {
        val (sid, dir) = split(parentDocumentId)
        if (!server(sid).canWrite) throw FileNotFoundException(L("Máy này chỉ có quyền xem", "This phone has view-only access"))
        val sess = Servers.session(ctx(), sid)
        val target = join(dir, displayName.replace('/', '_'))
        try {
            if (mimeType == Document.MIME_TYPE_DIR) {
                sess.mkdir(target)
                notifyChildren(ctx(), sid, dir)
                return docId(sid, target)
            }
            // An empty file under a free name; the app then writes into it.
            val empty = File(ctx().cacheDir, "empty").apply { writeBytes(ByteArray(0)) }
            val pfd = ParcelFileDescriptor.open(empty, ParcelFileDescriptor.MODE_READ_ONLY)
            val written = sess.upload(target, pfd.detachFd().toLong(), 0, Mobileclient.ModeKeepBoth, null)
            notifyChildren(ctx(), sid, dir)
            return docId(sid, written)
        } catch (x: Exception) {
            throw FileNotFoundException(friendly(x))
        }
    }

    override fun deleteDocument(documentId: String) {
        val (sid, path) = split(documentId)
        try {
            Servers.session(ctx(), sid).delete(path)
        } catch (x: Exception) {
            throw FileNotFoundException(friendly(x))
        }
        cacheFile(documentId).delete()
        notifyChildren(ctx(), sid, path.substringBeforeLast('/').ifEmpty { "/" })
    }

    override fun renameDocument(documentId: String, displayName: String): String {
        val (sid, path) = split(documentId)
        val dir = path.substringBeforeLast('/').ifEmpty { "/" }
        val dest = join(dir, displayName.replace('/', '_'))
        try {
            Servers.session(ctx(), sid).move(path, dest)
        } catch (x: Exception) {
            throw FileNotFoundException(friendly(x))
        }
        notifyChildren(ctx(), sid, dir)
        return docId(sid, dest)
    }
}
