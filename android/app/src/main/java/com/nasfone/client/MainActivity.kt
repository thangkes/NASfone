package com.nasfone.client

import android.Manifest
import android.app.Activity
import android.app.AlertDialog
import android.content.ClipboardManager
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.DocumentsContract
import android.provider.Settings
import android.view.View
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import com.google.zxing.integration.android.IntentIntegrator
import com.nasfone.core.mobileclient.Mobileclient
import com.nasfone.server.Role
import com.nasfone.server.Updater
import org.json.JSONObject
import java.util.UUID
import kotlin.concurrent.thread

private const val REQ_CAMERA = 7
private const val REQ_MEDIA = 8

/** Home: the paired servers, adding a server, backup and app settings. */
class MainActivity : Activity() {
    private lateinit var list: LinearLayout
    private lateinit var updateBtn: Button
    private var pendingBackupId: String? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        Lang.init(this)
        val (root, col) = page()
        col.addView(text(L("Máy chủ NASfone đã ghép", "Paired NASfone servers"), 20f, bold = true))
        col.addView(text(L("Duyệt, tải về, tải lên và sao lưu ảnh lên NAS. Mỗi server có khoá riêng nằm trong chip bảo mật của máy.",
            "Browse, download, upload and back up photos to your NAS. Each server has its own key kept in this phone's secure hardware."), 13f))
        updateBtn = button("") { startUpdate() }.apply { visibility = View.GONE }.also { col.addView(it) }
        list = vbox().also { col.addView(it) }

        val row = hbox()
        row.addView(button(L("📷 Quét mã ghép đôi", "📷 Scan pairing code")) { scan() }, weighted())
        row.addView(button(L("📋 Dán lời mời", "📋 Paste invite")) { paste() }, weighted())
        col.addView(row)
        col.addView(text(L("Lấy mã ghép đôi: trên server (app Android hoặc cửa sổ server Windows) bấm \"Ghép thiết bị mới\".",
            "Get a pairing code: on the server (Android app or Windows server window) tap \"Pair a new device\"."), 12f))

        section(col, L("Cài đặt", "Settings"))
        col.addView(button(L("Kiểm tra cập nhật", "Check for updates") + " (v${Updater.currentVersion(this)})") { checkUpdate() })
        col.addView(button(L("Ngôn ngữ: ", "Language: ") + when (prefs().getString("lang", "")) {
            "vi" -> "Tiếng Việt"; "en" -> "English"; else -> L("theo máy", "follow the phone")
        }) { chooseLanguage() })
        col.addView(button(L("Mở trong app Tệp của Android", "Open in the Android Files app")) { openFilesApp() })
        col.addView(button(L("Chuyển máy này sang làm server", "Switch this phone to server")) { switchToServer() })
        setContentView(root)
        handleIntent(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleIntent(intent)
    }

    override fun onResume() {
        super.onResume()
        render()
        Updater.maybeCheck(this) { _, _ -> runOnUiThread { renderUpdate() } }
        renderUpdate()
    }

    private fun prefs() = getSharedPreferences("nasfone", MODE_PRIVATE)

    /** nasfone://pair?i=… links (from the web page or another app). */
    private fun handleIntent(i: Intent?) {
        val data = i?.data ?: return
        if (data.scheme.equals("nasfone", true)) {
            i.data = null
            confirmPair(data.toString())
        }
    }

    // ------------------------------------------------------------ server list

    private fun render() {
        list.removeAllViews()
        val servers = Servers.list(this)
        if (servers.isEmpty()) {
            list.addView(text(L("Chưa ghép server nào.", "No server paired yet."), 14f).apply { setPadding(0, dp(12), 0, dp(12)) })
            return
        }
        for (s in servers) list.addView(serverCard(s))
    }

    private fun serverCard(s: Server): View {
        val c = card()
        val title = hbox()
        title.addView(text(s.host, 16f, bold = true), weighted())
        title.addView(text(if (s.canWrite) "ADMIN" else "USER", 12f, bold = true, color = if (s.canWrite) ADMIN else USER))
        c.addView(title)
        val status = text(if (s.revoked) L("⛔ Đã bị thu hồi trên server", "⛔ Revoked on the server") else L("Đang kiểm tra…", "Checking…"), 13f)
        c.addView(status)
        if (!s.revoked) {
            thread {
                val msg = try {
                    when (Servers.connect(this, s.id).optString("via")) {
                        "lan" -> L("● Kết nối qua LAN (nhanh nhất)", "● Connected over the LAN (fastest)")
                        "tailnet" -> L("● Kết nối qua tailnet", "● Connected through the tailnet")
                        else -> L("● Kết nối qua Internet (Funnel)", "● Connected over the internet (Funnel)")
                    }
                } catch (e: Exception) {
                    if (Servers.isRevoked(e)) runOnUiThread { render() }
                    "⚠ " + friendly(e)
                }
                runOnUiThread { status.text = msg }
            }
        }
        val row = hbox()
        if (!s.revoked) row.addView(button(L("Mở", "Open")) {
            startActivity(Intent(this, BrowseActivity::class.java).putExtra("server", s.id))
        }, weighted())
        row.addView(button(L("Xoá", "Remove")) { removeServer(s) }, weighted())
        c.addView(row)

        if (!s.revoked && s.canWrite) {
            c.addView(CheckBox(this).apply {
                text = L("Tự sao lưu ảnh & video mới", "Back up new photos & videos")
                isChecked = s.backup
                setOnCheckedChangeListener { b, on -> if (on) enableBackup(s.id, b as CheckBox) else setBackup(s.id, false) }
            })
            if (s.backup) {
                c.addView(CheckBox(this).apply {
                    text = L("Chỉ khi có Wi-Fi", "Wi-Fi only")
                    isChecked = s.backupWifiOnly
                    setOnCheckedChangeListener { _, on ->
                        Servers.update(this@MainActivity, s.id) { it.backupWifiOnly = on }
                        Backup.schedule(this@MainActivity)
                    }
                })
                c.addView(text(Backup.statusText(this, s.id), 12f))
                c.addView(button(L("Sao lưu cả ảnh & video cũ", "Also back up older photos & videos")) {
                    AlertDialog.Builder(this)
                        .setMessage(L("Tải lên toàn bộ ảnh và video đang có trên máy? Việc này có thể mất nhiều thời gian và dữ liệu.",
                            "Upload every photo and video on this phone? This can take a long time and use a lot of data."))
                        .setPositiveButton(L("Bắt đầu", "Start")) { _, _ ->
                            Servers.update(this, s.id) { it.backupSince = 0 }
                            Backup.runNow(this)
                        }
                        .setNegativeButton(L("Huỷ", "Cancel"), null).show()
                })
            }
        }
        return c
    }

    private fun removeServer(s: Server) {
        AlertDialog.Builder(this)
            .setTitle(L("Xoá server này khỏi máy?", "Remove this server from the phone?"))
            .setMessage(L("Khoá ghép đôi của máy này sẽ bị xoá; muốn dùng lại phải ghép đôi lại. Trên server, bạn có thể thu hồi thiết bị này.",
                "This phone's pairing key is deleted; pair again to use it. On the server you can also revoke this device."))
            .setPositiveButton(L("Xoá", "Remove")) { _, _ ->
                Servers.remove(this, s.id)
                Backup.schedule(this)
                NasProvider.notifyRoots(this)
                render()
            }
            .setNegativeButton(L("Huỷ", "Cancel"), null).show()
    }

    // ------------------------------------------------------------ pairing

    private fun scan() {
        if (checkSelfPermission(Manifest.permission.CAMERA) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(Manifest.permission.CAMERA), REQ_CAMERA)
            return
        }
        IntentIntegrator(this)
            .setDesiredBarcodeFormats(IntentIntegrator.QR_CODE)
            .setPrompt(L("Quét mã ghép đôi trên server", "Scan the pairing code on the server"))
            .setOrientationLocked(false)
            .setBeepEnabled(false)
            .initiateScan()
    }

    private fun paste() {
        val clip = (getSystemService(CLIPBOARD_SERVICE) as ClipboardManager).primaryClip
        val fromClip = clip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(this)?.toString().orEmpty()
        val et = EditText(this).apply {
            hint = "nasfone1:…"
            setText(if (Mobileclient.qrText(fromClip) == "invite") fromClip else "")
            minLines = 3
        }
        AlertDialog.Builder(this)
            .setTitle(L("Dán lời mời ghép đôi", "Paste the pairing invite"))
            .setView(et)
            .setPositiveButton(L("Tiếp tục", "Continue")) { _, _ -> confirmPair(et.text.toString()) }
            .setNegativeButton(L("Huỷ", "Cancel"), null).show()
    }

    @Deprecated("Activity result API needs AndroidX; this app uses the platform Activity")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        val res = IntentIntegrator.parseActivityResult(requestCode, resultCode, data)
        if (res == null) {
            super.onActivityResult(requestCode, resultCode, data)
            return
        }
        val t = res.contents ?: return
        if (Mobileclient.qrText(t) != "invite") {
            toast(L("Đây không phải mã ghép đôi NASfone. (Mã QR đăng nhập LAN dành cho app server.)",
                "This is not a NASfone pairing code. (LAN sign-in codes are for the server app.)"))
            return
        }
        confirmPair(t)
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        when (requestCode) {
            REQ_CAMERA -> if (grantResults.firstOrNull() == PackageManager.PERMISSION_GRANTED) scan()
                else toast(L("Cần quyền camera để quét mã", "Camera permission is needed to scan"))
            REQ_MEDIA -> pendingBackupId?.let { id ->
                pendingBackupId = null
                if (Backup.hasMediaPermission(this)) setBackup(id, true)
                else { toast(L("Cần quyền đọc ảnh & video để sao lưu", "Photo & video access is needed for backup")); render() }
            }
        }
    }

    /** Pairing always asks first: any app or page can hand us an invite. */
    private fun confirmPair(invite: String) {
        val info = try { JSONObject(Mobileclient.inviteInfo(invite.trim())) } catch (e: Exception) {
            toast(L("Lời mời không hợp lệ: ", "Invalid invite: ") + e.message)
            return
        }
        val admin = info.optString("role") == "admin"
        AlertDialog.Builder(this)
            .setTitle(L("Ghép đôi với server này?", "Pair with this server?"))
            .setMessage(L("Server: ", "Server: ") + info.optString("host") + L("\nQuyền: ", "\nAccess: ") +
                (if (admin) L("ADMIN — toàn quyền", "ADMIN — full access") else L("USER — chỉ xem & tải về", "USER — view & download only")) +
                L("\nMã nhận dạng server: ", "\nServer key: ") + info.optString("fp") +
                L("\n\nChỉ đồng ý nếu bạn vừa tạo lời mời này trên server của mình.", "\n\nOnly accept if you just created this invite on your own server."))
            .setPositiveButton(L("Ghép đôi", "Pair")) { _, _ -> doPair(invite.trim()) }
            .setNegativeButton(L("Huỷ", "Cancel"), null).show()
    }

    private fun doPair(invite: String) {
        toast(L("Đang ghép đôi…", "Pairing…"))
        thread {
            val alias = "nasfone-" + UUID.randomUUID()
            try {
                val key = KeystoreKey.create(alias)
                val cfgJson = Mobileclient.pair(invite, Servers.deviceName(), key)
                val cfg = JSONObject(cfgJson)
                val host = Uri.parse(cfg.optString("url")).host ?: cfg.optString("url")
                Servers.put(this, Server(id = cfg.getString("deviceId"), host = host, config = cfgJson, keyAlias = alias,
                    role = cfg.optString("role", "user")))
                NasProvider.notifyRoots(this)
                toast(L("Đã ghép đôi với $host", "Paired with $host"))
            } catch (e: Exception) {
                KeystoreKey.delete(alias)
                toast(L("Ghép đôi thất bại: ", "Pairing failed: ") + friendly(e))
            }
            runOnUiThread { render() }
        }
    }

    // ------------------------------------------------------------ backup

    private fun enableBackup(id: String, box: CheckBox) {
        if (!Backup.hasMediaPermission(this)) {
            pendingBackupId = id
            box.isChecked = false
            requestPermissions(Backup.mediaPermissions(), REQ_MEDIA)
            return
        }
        setBackup(id, true)
    }

    private fun setBackup(id: String, on: Boolean) {
        Servers.update(this, id) {
            it.backup = on
            if (on && it.backupSince == 0L) it.backupSince = System.currentTimeMillis() / 1000 // new media from now on
        }
        Backup.schedule(this)
        if (on) Backup.runNow(this)
        render()
    }

    // ------------------------------------------------------------ settings

    private fun openFilesApp() {
        val intents = listOf(
            Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(DocumentsContract.buildRootsUri(NasProvider.AUTHORITY), "vnd.android.document/root")
            },
            Intent(Intent.ACTION_OPEN_DOCUMENT).setType("*/*").addCategory(Intent.CATEGORY_OPENABLE),
        )
        for (i in intents) {
            try {
                startActivity(i)
                return
            } catch (_: Exception) {
            }
        }
        toast(L("Mở app Tệp, chọn \"NASfone\" trong danh sách bên trái.", "Open the Files app and pick \"NASfone\" on the left."))
    }

    private fun switchToServer() {
        AlertDialog.Builder(this)
            .setTitle(L("Chuyển sang làm server?", "Switch to server?"))
            .setMessage(L("Máy này sẽ chia sẻ bộ nhớ của nó làm NAS. Các server đã ghép vẫn được giữ, có thể chuyển về sau; tự sao lưu ảnh tạm dừng.",
                "This phone will share its storage as a NAS. Paired servers are kept, you can switch back later; photo backup pauses."))
            .setPositiveButton(L("Chuyển", "Switch")) { _, _ ->
                Role.set(this, Role.SERVER)
                Backup.schedule(this)
                Role.launch(this, Role.SERVER)
            }
            .setNegativeButton(L("Huỷ", "Cancel"), null).show()
    }

    private fun chooseLanguage() {
        val values = arrayOf("", "vi", "en")
        val labels = arrayOf(L("Theo máy", "Follow the phone"), "Tiếng Việt", "English")
        AlertDialog.Builder(this).setItems(labels) { _, i ->
            prefs().edit().putString("lang", values[i]).apply()
            Lang.init(this)
            recreate()
        }.show()
    }

    private fun renderUpdate() {
        val upd = Updater.available
        updateBtn.visibility = if (upd != null) View.VISIBLE else View.GONE
        if (upd != null) {
            updateBtn.isEnabled = !Updater.busy
            updateBtn.text = if (Updater.busy) L("Đang tải bản cập nhật…", "Downloading the update…")
            else L("⬆ Cập nhật lên v${upd.optString("version")}", "⬆ Update to v${upd.optString("version")}")
        }
    }

    private fun checkUpdate() {
        toast(L("Đang kiểm tra…", "Checking…"))
        Updater.maybeCheck(this, force = true) { rel, err ->
            runOnUiThread {
                when {
                    err != null -> toast(L("Không kiểm tra được: ", "Could not check: ") + err.message)
                    rel == null -> toast(L("Đang dùng bản mới nhất.", "You have the latest version."))
                    else -> renderUpdate()
                }
            }
        }
    }

    private fun startUpdate() {
        if (!packageManager.canRequestPackageInstalls()) {
            toast(L("Hãy cho phép NASfone Client cài ứng dụng, rồi bấm Cập nhật lần nữa.", "Allow NASfone Client to install apps, then tap Update again."))
            startActivity(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:$packageName")))
            return
        }
        Updater.install(this) { msg -> toast(L("Cập nhật lỗi: ", "Update failed: ") + msg) }
        renderUpdate()
    }
}
