package com.nasfone.server

import android.Manifest
import android.app.Activity
import android.app.AlertDialog
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.graphics.Typeface
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.provider.Settings
import android.text.InputType
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup.LayoutParams.MATCH_PARENT
import android.view.ViewGroup.LayoutParams.WRAP_CONTENT
import android.view.WindowInsets
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.Switch
import android.widget.TextView
import android.widget.Toast
import com.google.zxing.integration.android.IntentIntegrator
import com.nasfone.core.mobile.Mobile
import org.json.JSONArray
import org.json.JSONObject
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlin.concurrent.thread

private const val REQ_CAMERA = 7

class MainActivity : Activity() {
    private lateinit var prefs: Prefs

    private lateinit var stateTv: TextView
    private lateinit var accountBox: LinearLayout
    private lateinit var addrBox: LinearLayout
    private lateinit var toggleBtn: Button
    private lateinit var updateBtn: Button
    private lateinit var loginBtn: Button
    private lateinit var logoutBtn: Button
    private lateinit var funnelSw: Switch
    private lateinit var funnelTv: TextView
    private lateinit var funnelHelpBtn: Button
    private lateinit var hostEt: EditText
    private lateinit var controlEt: EditText
    private lateinit var rootEt: EditText
    private lateinit var verboseCb: CheckBox
    private lateinit var lanSw: Switch
    private lateinit var lanTv: TextView
    private lateinit var lanPortEt: EditText
    private lateinit var lanScanBtn: Button
    private lateinit var autoCb: CheckBox
    private lateinit var permTv: TextView
    private lateinit var logTv: TextView
    private lateinit var adminCodeTv: TextView
    private lateinit var userCodeTv: TextView
    private lateinit var adminCopyBtn: Button
    private lateinit var userCopyBtn: Button
    private lateinit var codeInfoTv: TextView
    private lateinit var codeBar: ProgressBar
    private lateinit var devBox: LinearLayout

    private var adminCode: String? = null
    private var userCode: String? = null
    private var lastDevicesJson = ""
    private lateinit var pairedBox: LinearLayout
    private var lastPairedJson = ""
    private val ticker = Handler(Looper.getMainLooper())
    private val tick = object : Runnable {
        override fun run() {
            // Lỗi hiển thị không được làm sập app (service vẫn chạy trong cùng tiến trình).
            try {
                renderCode()
                renderDevices()
                renderPaired()
            } catch (e: Exception) {
                Core.log(L("Lỗi hiển thị: $e", "Display error: $e"))
            }
            ticker.postDelayed(this, 1000)
        }
    }
    private val dateFmt = SimpleDateFormat("HH:mm dd/MM", Locale.US)

    private val onChange: () -> Unit = { render() }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = Prefs(this)
        Lang.init(this)
        setContentView(buildUi())
        // Mở app là có mã đăng nhập: tự khởi động server nếu đang dừng.
        if (!Core.running && savedInstanceState == null) {
            // Vừa cài lại app và còn file sao lưu cấu hình: hỏi khôi phục trước khi chạy server.
            if (ConfigBackup.shouldOfferRestore(this)) offerRestore() else toggle()
        }
        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
        }
    }

    override fun onResume() {
        super.onResume()
        Core.addListener(onChange)
        render()
        ticker.post(tick)
    }

    override fun onPause() {
        Core.removeListener(onChange)
        ticker.removeCallbacks(tick)
        super.onPause()
    }

    // ---------------------------------------------------------------- UI

    private fun dp(v: Int) = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v.toFloat(), resources.displayMetrics).toInt()

    private fun buildUi(): View {
        val col = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(16), dp(12), dp(16), dp(24))
        }
        val scroll = ScrollView(this).apply { addView(col) }
        scroll.setOnApplyWindowInsetsListener { v, insets ->
            val bars = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.ime())
            v.setPadding(bars.left, bars.top, bars.right, bars.bottom)
            insets
        }

        // Tên app đã có trên thanh tiêu đề của hệ thống; không lặp lại ở đây.
        stateTv = text("", 16f, bold = true).also { col.addView(it) }
        toggleBtn = button("") { toggle() }.also { col.addView(it) }
        updateBtn = button("") { startUpdate() }.apply { visibility = View.GONE }.also { col.addView(it) }

        section(col, L("Mã đăng nhập web (6 số, đổi mỗi phút)", "Web sign-in codes (6 digits, change every minute)"))
        // Hai mã luôn khác nhau; màu và nhãn tách biệt để không đưa nhầm mã Admin.
        val (aTv, aBtn) = codeBlock(col, L("ADMIN — toàn quyền (tải lên, ghi đè, xóa)", "ADMIN — full access (upload, overwrite, delete)"), "#B42318") { adminCode }
        adminCodeTv = aTv; adminCopyBtn = aBtn
        val (uTv, uBtn) = codeBlock(col, L("USER — chỉ xem & tải về", "USER — view & download only"), "#2F6FED") { userCode }
        userCodeTv = uTv; userCopyBtn = uBtn
        codeBar = ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply { max = 60 }
            .also { col.addView(it, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT)) }
        codeInfoTv = text("", 13f).apply { gravity = Gravity.CENTER }
            .also { col.addView(it, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT)) }
        // Trình duyệt trong mạng LAN hiện mã QR; quét để cho vào (chỉ xem & tải về).
        lanScanBtn = button(L("📷 Quét QR đăng nhập LAN", "📷 Scan LAN sign-in QR")) { scanLanQr() }
            .apply { visibility = View.GONE }.also { col.addView(it) }

        section(col, L("Ứng dụng đã ghép (Windows / Android)", "Paired apps (Windows / Android)"))
        pairedBox = vbox().also { col.addView(it) }
        col.addView(button(L("＋ Ghép thiết bị mới", "＋ Pair new device")) { newPairing() })

        section(col, L("Phiên trình duyệt (đăng nhập bằng mã 6 số)", "Browser sessions (signed in with a 6-digit code)"))
        devBox = vbox().also { col.addView(it) }
        col.addView(button(L("Thu hồi tất cả", "Revoke all")) {
            AlertDialog.Builder(this)
                .setTitle(L("Thu hồi tất cả thiết bị?", "Revoke all devices?"))
                .setMessage(L("Mọi trình duyệt đang đăng nhập sẽ bị đăng xuất ngay.", "Every signed-in browser is signed out immediately."))
                .setPositiveButton(L("Thu hồi", "Revoke")) { _, _ -> thread { Mobile.revokeAllDevices(); runOnUiThread { lastDevicesJson = ""; renderDevices() } } }
                .setNegativeButton(L("Hủy", "Cancel"), null)
                .show()
        })

        section(col, L("Tài khoản Tailscale", "Tailscale account"))
        accountBox = vbox().also { col.addView(it) }
        val accBtns = hbox().also { col.addView(it) }
        loginBtn = button(L("Đăng nhập", "Sign in")) { login() }.also { accBtns.addView(it, weighted()) }
        logoutBtn = button(L("Đăng xuất / đổi tài khoản", "Sign out / switch account")) { logout() }.also { accBtns.addView(it, weighted()) }

        section(col, L("Địa chỉ truy cập", "Addresses"))
        addrBox = vbox().also { col.addView(it) }

        section(col, L("Funnel (truy cập công khai qua Internet)", "Funnel (public access over the internet)"))
        funnelSw = Switch(this).apply {
            text = L("Bật Funnel", "Enable Funnel")
            isChecked = prefs.funnel
            setOnCheckedChangeListener { _, on ->
                prefs.funnel = on
                if (Core.running) thread { Mobile.setFunnel(on) }
            }
        }
        col.addView(funnelSw)
        funnelTv = text("", 13f).also { col.addView(it) }
        funnelHelpBtn = button(L("Mở trang cài đặt Tailscale để sửa", "Open the Tailscale settings page to fix this")) {
            Core.status.optString("funnelHelpURL").takeIf { it.isNotEmpty() }?.let {
                startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(it)))
            }
        }.also { col.addView(it) }

        section(col, L("Kết nối LAN (cùng mạng Wi-Fi / hotspot, không cần internet)", "LAN access (same Wi-Fi / hotspot, no internet needed)"))
        lanPortEt = field(col, L("Cổng LAN", "LAN port"), prefs.lanPort.toString(), InputType.TYPE_CLASS_NUMBER)
        lanSw = Switch(this).apply {
            text = L("Bật kết nối LAN", "Enable LAN access")
            isChecked = prefs.lanEnabled
            setOnCheckedChangeListener { sw, on -> setLan(sw as Switch, on) }
        }.also { col.addView(it) }
        lanTv = text(L("Trình duyệt cùng mạng mở địa chỉ LAN ở trên, đăng nhập bằng mã 6 số hoặc quét QR (chỉ xem). Đổi cổng: tắt rồi bật lại.",
            "Browsers on the same network open the LAN address above and sign in with a 6-digit code or a QR scan (view only). To change the port, switch off and on."), 13f)
            .also { col.addView(it) }

        section(col, L("Cài đặt (áp dụng khi khởi động lại)", "Settings (applied on restart)"))
        hostEt = field(col, L("Tên máy trong tailnet", "Machine name in the tailnet"), prefs.hostname)
        controlEt = field(col, L("Máy chủ điều khiển (để trống = Tailscale; điền URL nếu dùng Headscale)", "Control server (empty = Tailscale; enter a URL for Headscale)"), prefs.controlUrl)
        rootEt = field(col, L("Thư mục lưu trữ", "Storage folder"), prefs.rootDir)
        verboseCb = CheckBox(this).apply { text = L("Ghi log chi tiết của Tailscale", "Verbose Tailscale logs"); isChecked = prefs.verboseLog }.also { col.addView(it) }
        autoCb = CheckBox(this).apply {
            text = L("Tự chạy khi khởi động máy", "Start when the phone boots")
            isChecked = prefs.autoStart
            setOnCheckedChangeListener { _, on -> prefs.autoStart = on }
        }.also { col.addView(it) }
        col.addView(button(L("Lưu cài đặt", "Save settings")) { save() })
        col.addView(button(L("Kiểm tra cập nhật", "Check for updates") + " (v${Updater.currentVersion(this)})") { checkUpdate() })
        col.addView(button(L("Sao lưu cấu hình (tự động, hoặc bấm để làm ngay)", "Configuration backup (automatic, tap to run now)")) { backupConfig() })
        col.addView(button(L("Ngôn ngữ: ", "Language: ") + when (prefs.lang) {
            "vi" -> "Tiếng Việt"; "en" -> "English"; else -> L("theo máy", "follow the phone")
        }) { chooseLanguage() })

        section(col, L("Quyền & chống bị tắt nền (MagicOS)", "Permissions & background survival"))
        permTv = text("", 14f).also { col.addView(it) }
        col.addView(button(L("Cấp quyền truy cập tất cả file", "Grant all-files access")) {
            startActivity(Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION, Uri.parse("package:$packageName")))
        })
        col.addView(button(L("Bỏ tối ưu pin", "Disable battery optimization")) {
            @Suppress("BatteryLife")
            startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName")))
        })
        col.addView(button(L("Mở cài đặt ứng dụng (Khởi chạy ứng dụng → thủ công)", "Open app settings (App launch → manage manually)")) {
            startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:$packageName")))
        })

        section(col, L("Nhật ký", "Log"))
        logTv = text("", 11f).apply { typeface = Typeface.MONOSPACE; setTextIsSelectable(true) }.also { col.addView(it) }
        col.addView(button(L("Sao chép nhật ký", "Copy log")) { copy(Core.logLines().joinToString("\n")) })
        return scroll
    }

    private fun text(s: String, size: Float, bold: Boolean = false) = TextView(this).apply {
        text = s
        textSize = size
        if (bold) setTypeface(typeface, Typeface.BOLD)
        setPadding(0, dp(4), 0, dp(4))
    }

    private fun button(label: String, onClick: () -> Unit) = Button(this).apply {
        text = label
        isAllCaps = false
        setOnClickListener { onClick() }
    }

    private fun vbox() = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
    private fun hbox() = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
    private fun weighted() = LinearLayout.LayoutParams(0, WRAP_CONTENT, 1f)

    private fun section(col: LinearLayout, title: String) {
        col.addView(text(title, 13f, bold = true).apply {
            setPadding(0, dp(18), 0, dp(2))
            setTextColor(Color.parseColor("#2F6FED"))
        })
    }

    private fun field(col: LinearLayout, label: String, value: String, type: Int = InputType.TYPE_CLASS_TEXT): EditText {
        col.addView(text(label, 12f).apply { setPadding(0, dp(8), 0, 0) })
        return EditText(this).apply {
            setText(value)
            inputType = type
            setSingleLine()
        }.also { col.addView(it, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT)) }
    }

    /** Nhãn quyền + mã lớn + nút sao chép cho một loại mã đăng nhập. */
    private fun codeBlock(col: LinearLayout, label: String, color: String, current: () -> String?): Pair<TextView, Button> {
        col.addView(text(label, 13f, bold = true).apply {
            setTextColor(Color.parseColor(color))
            setPadding(0, dp(10), 0, 0)
        })
        val row = hbox().apply { gravity = Gravity.CENTER_VERTICAL }.also { col.addView(it) }
        val tv = text("— — —", 30f, bold = true).apply {
            typeface = Typeface.create(Typeface.MONOSPACE, Typeface.BOLD)
            letterSpacing = 0.12f
            setTextColor(Color.parseColor(color))
        }.also { row.addView(it, weighted()) }
        val btn = button(L("Sao chép", "Copy")) { current()?.let { copy(it.replace(" ", "")) } }.also { row.addView(it) }
        return tv to btn
    }

    /** Một dòng "nhãn: giá trị" kèm nút sao chép. */
    private fun row(label: String, value: String) = hbox().apply {
        gravity = Gravity.CENTER_VERTICAL
        addView(text("$label\n$value", 14f).apply { setTextIsSelectable(true) }, weighted())
        addView(button(L("Sao chép", "Copy")) { copy(value) })
    }

    // ---------------------------------------------------------------- render

    // Last rendered content per section: status updates arrive every ~2 s
    // (traffic counters), so sections are rebuilt only when what they show changed.
    private var accountKey = ""
    private var addrKey = ""
    private var logKey = ""

    private fun render() {
        val st = Core.status
        val backend = st.optString("backendState")
        val running = Core.running

        stateTv.text = when {
            !running && Core.startError != null -> L("⛔ Lỗi: ${Core.startError}", "⛔ Error: ${Core.startError}")
            !running -> L("○ Đã dừng", "○ Stopped")
            backend == "Running" -> L("● Đang chạy • tailnet đã kết nối", "● Running • tailnet connected")
            backend == "NeedsLogin" -> L("● Đang chạy • chờ đăng nhập Tailscale", "● Running • waiting for Tailscale sign-in")
            backend.isEmpty() -> L("● Đang khởi động…", "● Starting…")
            else -> L("● Đang chạy • $backend", "● Running • $backend")
        }
        stateTv.setTextColor(Color.parseColor(if (running) "#1F9D55" else "#D64545"))
        toggleBtn.text = if (running) L("Dừng server", "Stop server") else L("Khởi động server", "Start server")
        lanScanBtn.visibility = if (running && (st.optJSONArray("lanURLs")?.length() ?: 0) > 0) View.VISIBLE else View.GONE
        val upd = Updater.available
        updateBtn.visibility = if (upd != null) View.VISIBLE else View.GONE
        if (upd != null) {
            updateBtn.isEnabled = !Updater.busy
            updateBtn.text = if (Updater.busy) L("Đang tải bản cập nhật…", "Downloading the update…")
            else L("⬆ Cập nhật lên v${upd.optString("version")}", "⬆ Update to v${upd.optString("version")}")
        }

        val newAccountKey = listOf(running, backend, st.optString("loginName"), st.optString("tailnetName"), st.optString("authURL")).joinToString("|")
        if (newAccountKey != accountKey) {
            accountKey = newAccountKey
            renderAccount(st, backend, running)
        }
        loginBtn.isEnabled = running && backend != "Running"
        logoutBtn.isEnabled = running && backend == "Running"

        val newAddrKey = listOf(running, st.optString("dnsName"), st.optJSONArray("tailscaleIPs")?.toString(), st.optString("funnelURL"),
            st.optJSONArray("lanURLs")?.toString(), st.optString("lanError")).joinToString("|")
        if (newAddrKey != addrKey) {
            addrKey = newAddrKey
            renderAddresses(st, running)
        }
        funnelTv.text = when {
            st.optString("funnelError").isNotEmpty() ->
                "⚠ ${st.optString("funnelError")}\n" + L("App tự thử lại mỗi 20 giây sau khi bạn sửa.", "The app retries every 20 seconds after you fix it.")
            st.optString("funnelURL").isNotEmpty() -> L("Đang mở công khai: ", "Public at: ") + st.optString("funnelURL")
            prefs.funnel -> L("Sẽ mở khi tailnet kết nối xong.", "Opens once the tailnet is connected.")
            else -> L("Tắt — chỉ truy cập qua LAN và tailnet.", "Off — reachable only through the tailnet.")
        }
        funnelHelpBtn.visibility = if (st.optString("funnelHelpURL").isNotEmpty()) View.VISIBLE else View.GONE

        val files = Environment.isExternalStorageManager()
        val battery = (getSystemService(POWER_SERVICE) as PowerManager).isIgnoringBatteryOptimizations(packageName)
        permTv.text = "${if (files) "✔" else "✘"} " + L("Truy cập tất cả file", "All files access") +
            "\n${if (battery) "✔" else "✘"} " + L("Bỏ tối ưu pin", "Battery optimization disabled")

        val logText = Core.logLines().takeLast(80).joinToString("\n")
        if (logText != logKey) {
            logKey = logText
            logTv.text = logText
        }
    }

    private fun renderAccount(st: JSONObject, backend: String, running: Boolean) {
        accountBox.removeAllViews()
        if (running) {
            val login = st.optString("loginName")
            val tailnet = st.optString("tailnetName")
            accountBox.addView(text(
                when {
                    login.isNotEmpty() -> L("Tài khoản: $login\nTailnet: $tailnet", "Account: $login\nTailnet: $tailnet")
                    backend == "NeedsLogin" -> L("Chưa đăng nhập. Bấm \"Đăng nhập\" để mở trang đăng nhập Tailscale.", "Not signed in. Tap \"Sign in\" to open the Tailscale sign-in page.")
                    else -> "…"
                }, 14f
            ))
            val auth = st.optString("authURL")
            if (auth.isNotEmpty()) accountBox.addView(row(L("Link đăng nhập (mở trên máy này hoặc máy khác)", "Sign-in link (open on this or another device)"), auth))
        } else {
            accountBox.addView(text(L("Khởi động server để đăng nhập.", "Start the server to sign in."), 14f))
        }
    }

    private fun renderAddresses(st: JSONObject, running: Boolean) {
        addrBox.removeAllViews()
        if (running) {
            val dns = st.optString("dnsName")
            if (dns.isNotEmpty()) addrBox.addView(row("Tailnet", "http://$dns"))
            val ips = st.optJSONArray("tailscaleIPs")
            if (ips != null && ips.length() > 0) addrBox.addView(row("Tailnet IP", "http://${ips.getString(0)}"))
            val funnel = st.optString("funnelURL")
            if (funnel.isNotEmpty()) addrBox.addView(row(L("Funnel (công khai)", "Funnel (public)"), funnel))
            val lan = st.optJSONArray("lanURLs")
            for (i in 0 until (lan?.length() ?: 0)) addrBox.addView(row(L("LAN (cùng mạng)", "LAN (same network)"), lan!!.getString(i)))
            val lanErr = st.optString("lanError")
            if (lanErr.isNotEmpty()) addrBox.addView(text("⚠ LAN: $lanErr", 13f))
        } else {
            addrBox.addView(text("—", 14f))
        }
    }

    private fun renderCode() {
        if (!::adminCodeTv.isInitialized) return
        val json = if (Core.running) Mobile.loginCode() else ""
        if (json.isEmpty()) {
            adminCode = null
            userCode = null
            adminCodeTv.text = "— — —"
            userCodeTv.text = "— — —"
            codeBar.progress = 0
            codeInfoTv.text = L("Khởi động server để có mã đăng nhập.", "Start the server to get sign-in codes.")
        } else {
            val o = JSONObject(json)
            adminCode = o.getString("admin")
            userCode = o.getString("user")
            val step = o.optInt("step", 60)
            val left = ((o.getLong("expires") - System.currentTimeMillis() + 999) / 1000).toInt().coerceIn(0, step)
            adminCodeTv.text = adminCode
            userCodeTv.text = userCode
            codeBar.max = step
            codeBar.progress = left
            codeInfoTv.text = L("Đổi mã sau %d:%02d • mỗi mã dùng 1 lần", "New codes in %d:%02d • each code works once").format(left / 60, left % 60)
        }
        adminCopyBtn.isEnabled = adminCode != null
        userCopyBtn.isEnabled = userCode != null
    }

    private fun renderDevices() {
        if (!::devBox.isInitialized) return
        val json = if (Core.running) Mobile.devices() else "[]"
        if (json == lastDevicesJson) return
        lastDevicesJson = json
        devBox.removeAllViews()
        val arr = try { JSONArray(json) } catch (_: Exception) { JSONArray() }
        if (arr.length() == 0) {
            devBox.addView(text(if (Core.running) L("Chưa có thiết bị nào.", "No devices yet.") else "—", 14f))
            return
        }
        for (i in 0 until arr.length()) {
            val d = arr.getJSONObject(i)
            val id = d.getString("id")
            val name = d.getString("name")
            val seen = parseTime(d.optString("lastSeen"))
            val role = if (d.optString("role") == "admin") "ADMIN" else L("USER (chỉ xem)", "USER (view only)")
            val info = "$role • ${d.optString("via")} • ${d.optString("lastIP")} • " + L("lần cuối", "last seen") + " $seen • " +
                L("hết hạn", "expires") + " ${parseTime(d.optString("expires"))}"
            devBox.addView(hbox().apply {
                gravity = Gravity.CENTER_VERTICAL
                addView(text("$name\n$info", 13f), weighted())
                addView(button(L("Thu hồi", "Revoke")) {
                    thread { Mobile.revokeDevice(id); runOnUiThread { lastDevicesJson = ""; renderDevices() } }
                })
            })
        }
    }

    /** Danh sách app đã ghép đôi bằng cặp khóa, kèm đổi quyền và thu hồi. */
    private fun renderPaired() {
        if (!::pairedBox.isInitialized) return
        val json = if (Core.running) Mobile.pairedDevices() else "[]"
        if (json == lastPairedJson) return
        lastPairedJson = json
        pairedBox.removeAllViews()
        val arr = try { JSONArray(json) } catch (_: Exception) { JSONArray() }
        if (arr.length() == 0) {
            pairedBox.addView(text(if (Core.running) L("Chưa ghép ứng dụng nào.", "No apps paired yet.") else "—", 14f))
            return
        }
        for (i in 0 until arr.length()) {
            val d = arr.getJSONObject(i)
            val id = d.getString("id")
            val name = d.getString("name")
            val admin = d.optString("role") == "admin"
            val fp = d.optString("fp").take(16).uppercase().chunked(4).joinToString("-")
            val head = text((if (admin) "● ADMIN  " else "● USER  ") + name, 14f, bold = true).apply {
                setTextColor(Color.parseColor(if (admin) "#B42318" else "#2F6FED"))
            }
            val info = text(
                "${platformLabel(d.optString("platform"))} • " + L("khóa", "key") + " $fp\n" +
                    L("Lần cuối", "Last seen") + " ${parseTime(d.optString("lastSeen"))} " + L("qua", "via") + " ${d.optString("via")} • " +
                    L("ghép lúc", "paired") + " ${parseTime(d.optString("created"))}",
                12f
            )
            val actions = hbox()
            actions.addView(button(if (admin) L("Hạ xuống User", "Demote to User") else L("Nâng lên Admin", "Promote to Admin")) {
                val to = if (admin) "user" else "admin"
                AlertDialog.Builder(this)
                    .setTitle(L("Đổi quyền \"$name\"?", "Change access for \"$name\"?"))
                    .setMessage(if (admin) L("Thiết bị chỉ còn quyền xem và tải về.", "The device will only be able to view and download.") else L("Thiết bị sẽ có toàn quyền: tải lên, ghi đè, xóa.", "The device gets full access: upload, overwrite, delete."))
                    .setPositiveButton(L("Đổi", "Change")) { _, _ -> thread { Mobile.setPairedRole(id, to); runOnUiThread { lastPairedJson = ""; renderPaired() } } }
                    .setNegativeButton(L("Hủy", "Cancel"), null)
                    .show()
            }, weighted())
            actions.addView(button(L("Thu hồi", "Revoke")) {
                AlertDialog.Builder(this)
                    .setTitle(L("Thu hồi \"$name\"?", "Revoke \"$name\"?"))
                    .setMessage(L("Thiết bị bị ngắt ngay và không kết nối lại được. Muốn dùng lại phải ghép đôi lại.", "The device is disconnected now and cannot reconnect. Pair it again to use it."))
                    .setPositiveButton(L("Thu hồi", "Revoke")) { _, _ -> thread { Mobile.revokePaired(id); runOnUiThread { lastPairedJson = ""; renderPaired() } } }
                    .setNegativeButton(L("Hủy", "Cancel"), null)
                    .show()
            }, weighted())
            pairedBox.addView(vbox().apply {
                setPadding(0, dp(6), 0, dp(6))
                addView(head)
                addView(info)
                addView(actions)
            })
        }
    }

    private fun platformLabel(p: String) = when (p) {
        "windows" -> "Windows"
        "android" -> "Android"
        "ios" -> "iOS"
        "macos" -> "macOS"
        else -> p
    }

    /** Chọn quyền rồi hiện lời mời ghép đôi dạng QR + nút sao chép. */
    private fun newPairing() {
        if (!Core.running) { toast(L("Khởi động server trước", "Start the server first")); return }
        AlertDialog.Builder(this)
            .setTitle(L("Ghép thiết bị mới — chọn quyền", "Pair a new device — choose access"))
            .setItems(arrayOf(L("USER — chỉ xem & tải về", "USER — view & download only"), L("ADMIN — toàn quyền (tải lên, ghi đè, xóa)", "ADMIN — full access (upload, overwrite, delete)"))) { _, which ->
                showInvite(if (which == 1) "admin" else "user")
            }
            .setNegativeButton(L("Hủy", "Cancel"), null)
            .show()
    }

    private fun showInvite(role: String) {
        thread {
            val inv = try {
                JSONObject(Mobile.newPairInvite(role))
            } catch (e: Exception) {
                runOnUiThread { toast(e.message ?: L("Không tạo được lời mời", "Could not create an invite")) }
                return@thread
            }
            val invite = inv.getString("invite")
            val png = try { Mobile.qrpng(invite, 8) } catch (_: Exception) { null }
            runOnUiThread {
                val box = vbox().apply { setPadding(dp(20), dp(8), dp(20), 0); gravity = Gravity.CENTER_HORIZONTAL }
                if (png != null) {
                    val bmp = android.graphics.BitmapFactory.decodeByteArray(png, 0, png.size)
                    box.addView(android.widget.ImageView(this).apply {
                        setImageDrawable(android.graphics.drawable.BitmapDrawable(resources, bmp).apply {
                            isFilterBitmap = false // keep QR modules sharp when scaled
                        })
                        setBackgroundColor(Color.WHITE)
                    }, LinearLayout.LayoutParams(dp(260), dp(260)))
                }
                val roleText = if (role == "admin") L("ADMIN — toàn quyền", "ADMIN — full access") else L("USER — chỉ xem & tải về", "USER — view & download only")
                val expires = inv.getLong("expires")
                val info = text("", 13f).apply { gravity = Gravity.CENTER }
                box.addView(info)
                val dialog = AlertDialog.Builder(this)
                    .setTitle(L("Lời mời ghép đôi", "Pairing invite"))
                    .setView(box)
                    .setPositiveButton(L("Sao chép lời mời", "Copy invite")) { _, _ -> copy(invite) }
                    .setNegativeButton(L("Đóng", "Close"), null)
                    .show()
                val h = Handler(Looper.getMainLooper())
                val tickInfo = object : Runnable {
                    override fun run() {
                        val left = ((expires - System.currentTimeMillis()) / 1000).coerceAtLeast(0)
                        info.text = L("Quyền", "Access") + ": $roleText\n" + L("Vân tay server", "Server fingerprint") + ": ${inv.optString("fp")}\n" +
                            (if (left > 0) L("Dùng 1 lần • hết hạn sau %d:%02d", "Single use • expires in %d:%02d").format(left / 60, left % 60) else L("Đã hết hạn — tạo lời mời mới", "Expired — create a new invite")) +
                            L("\n\nQuét bằng app NASfone trên điện thoại, hoặc sao chép rồi dán vào app trên máy tính.", "\n\nScan with the NASfone app on a phone, or copy and paste it into the app on a computer.")
                        if (left > 0 && dialog.isShowing) h.postDelayed(this, 1000)
                    }
                }
                tickInfo.run()
            }
        }
    }

    private fun parseTime(iso: String): String = try {
        // Go ghi thời gian dạng RFC3339 có phần nano giây; chỉ cần đến giây.
        val base = iso.substring(0, 19)
        val tz = iso.substring(iso.indexOfAny(charArrayOf('Z', '+', '-'), 19))
        val fmt = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ssXXX", Locale.US)
        dateFmt.format(fmt.parse(base + tz) ?: Date())
    } catch (_: Exception) {
        "?"
    }

    // ---------------------------------------------------------------- actions

    private fun toggle() {
        if (Core.running) {
            stopService(Intent(this, NasService::class.java))
        } else {
            if (!save()) return
            startForegroundService(Intent(this, NasService::class.java))
        }
        render()
    }

    private fun save(): Boolean {
        val host = hostEt.text.toString().trim().lowercase()
        if (!host.matches(Regex("[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?"))) return toast(L("Tên máy chỉ gồm a-z, 0-9 và dấu -", "The machine name may only use a-z, 0-9 and -"))
        val control = controlEt.text.toString().trim()
        if (control.isNotEmpty() && !control.startsWith("https://")) return toast(L("Máy chủ điều khiển phải bắt đầu bằng https://", "The control server must start with https://"))

        if (control != prefs.controlUrl) {
            // Trạng thái đăng nhập gắn với máy chủ điều khiển cũ; xóa để đăng nhập lại.
            if (Core.running) return toast(L("Dừng server trước khi đổi máy chủ điều khiển", "Stop the server before changing the control server"))
            prefs.stateDir.deleteRecursively()
            Core.log(L("Đã đổi máy chủ điều khiển, cần đăng nhập lại", "Control server changed; sign in again"))
        }
        prefs.hostname = host
        prefs.controlUrl = control
        prefs.rootDir = rootEt.text.toString().trim()
        prefs.verboseLog = verboseCb.isChecked
        if (Core.running) toast(L("Đã lưu. Khởi động lại server để áp dụng.", "Saved. Restart the server to apply.")) else toast(L("Đã lưu", "Saved"))
        return true
    }

    private fun login() {
        val auth = Core.status.optString("authURL")
        if (auth.isNotEmpty()) {
            startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(auth)))
            return
        }
        thread {
            try { Mobile.login() } catch (e: Exception) { Core.log(L("Đăng nhập: ${e.message}", "Sign in: ${e.message}")) }
        }
        toast(L("Đang lấy link đăng nhập…", "Getting the sign-in link…"))
    }

    private fun logout() {
        AlertDialog.Builder(this)
            .setTitle(L("Đăng xuất Tailscale?", "Sign out of Tailscale?"))
            .setMessage(L("Server sẽ rời tailnet hiện tại. Sau đó bấm \"Đăng nhập\" để dùng tài khoản khác.", "The server leaves the current tailnet. Then tap \"Sign in\" to use another account."))
            .setPositiveButton(L("Đăng xuất", "Sign out")) { _, _ ->
                thread {
                    try { Mobile.logout() } catch (e: Exception) { Core.log(L("Đăng xuất: ${e.message}", "Sign out: ${e.message}")) }
                }
            }
            .setNegativeButton(L("Hủy", "Cancel"), null)
            .show()
    }

    /** Dừng hẳn server (chờ lõi Go tắt xong), chạy work trên luồng nền, rồi bật lại nếu cần. */
    private fun withServerStopped(restart: Boolean, work: () -> Unit) {
        thread(name = "nasfone-config") {
            Mobile.stop() // chặn tới khi tắt xong; lệnh dừng của service sau đó không còn gì để làm
            runOnUiThread { if (Core.running) stopService(Intent(this, NasService::class.java)) }
            try {
                work()
            } finally {
                runOnUiThread {
                    if (restart && !Core.running) startForegroundService(Intent(this, NasService::class.java))
                    render()
                }
            }
        }
    }

    private fun backupConfig() {
        AlertDialog.Builder(this)
            .setTitle(L("Sao lưu cấu hình?", "Back up configuration?"))
            .setMessage(L(
                "Tài khoản Tailscale, thiết bị đã ghép và cài đặt được lưu vào:\n${ConfigBackup.file().path}\n\nApp đã TỰ sao lưu vài phút một lần khi cấu hình thay đổi; nút này sao lưu ngay lập tức. Gỡ rồi cài lại app thì mở app là được hỏi khôi phục.\n\nFile này chứa khoá bí mật: đừng chia sẻ, đừng xoá nếu muốn giữ cấu hình.",
                "The Tailscale account, paired devices and settings are saved to:\n${ConfigBackup.file().path}\n\nThe app already backs this up AUTOMATICALLY every few minutes when the configuration changes; this button does it right now. After a reinstall, open the app and it offers to restore.\n\nThe file holds secret keys: do not share it, and keep it if you want to keep your setup."
            ))
            .setPositiveButton(L("Sao lưu ngay", "Back up now")) { _, _ ->
                thread(name = "nasfone-backup") {
                    val msg = try {
                        val f = ConfigBackup.export(this)
                        L("Đã sao lưu: ${f.path}", "Backed up: ${f.path}")
                    } catch (e: Exception) {
                        L("Sao lưu lỗi: $e", "Backup failed: $e")
                    }
                    Core.log(msg)
                    runOnUiThread { toast(msg) }
                }
            }
            .setNegativeButton(L("Huỷ", "Cancel"), null)
            .show()
    }

    private fun offerRestore() {
        AlertDialog.Builder(this)
            .setTitle(L("Khôi phục cấu hình?", "Restore configuration?"))
            .setMessage(L(
                "Tìm thấy bản sao lưu cấu hình NASfone:\n${ConfigBackup.file().path}\n\nKhôi phục để giữ tài khoản Tailscale, địa chỉ truy cập, thiết bị đã ghép và cài đặt như trước khi cài lại.",
                "Found a NASfone configuration backup:\n${ConfigBackup.file().path}\n\nRestore it to keep the Tailscale account, address, paired devices and settings from before the reinstall."
            ))
            .setCancelable(false)
            .setPositiveButton(L("Khôi phục", "Restore")) { _, _ ->
                withServerStopped(restart = true) {
                    val msg = try {
                        ConfigBackup.restore(this)
                        L("Đã khôi phục cấu hình", "Configuration restored")
                    } catch (e: Exception) {
                        L("Khôi phục lỗi: $e", "Restore failed: $e")
                    }
                    Core.log(msg)
                    runOnUiThread {
                        toast(msg)
                        recreate() // đọc lại cài đặt vừa khôi phục vào các ô nhập
                    }
                }
            }
            .setNegativeButton(L("Bắt đầu mới", "Start fresh")) { _, _ -> toggle() }
            .show()
    }

    // ---------------------------------------------------------------- LAN QR sign-in

    /** Nút gạt LAN: mở/đóng cổng ngay, không cần khởi động lại server. */
    private fun setLan(sw: Switch, on: Boolean) {
        val port = lanPortEt.text.toString().trim().toIntOrNull()
        if (on && (port == null || port !in 1024..65535)) {
            toast(L("Cổng LAN phải từ 1024 đến 65535", "The LAN port must be between 1024 and 65535"))
            sw.setOnCheckedChangeListener(null); sw.isChecked = false
            sw.setOnCheckedChangeListener { s, v -> setLan(s as Switch, v) }
            return
        }
        prefs.lanEnabled = on
        if (port != null) prefs.lanPort = port
        if (!Core.running) return // áp dụng khi server khởi động
        thread {
            val err = try { Mobile.setLan(if (on) port!!.toLong() else 0L); null } catch (e: Exception) { e.message ?: e.toString() }
            if (err != null) runOnUiThread {
                toast(L("Không mở được cổng LAN: ", "Could not open the LAN port: ") + err)
                prefs.lanEnabled = false
                sw.setOnCheckedChangeListener(null); sw.isChecked = false
                sw.setOnCheckedChangeListener { s, v -> setLan(s as Switch, v) }
            }
        }
    }

    private fun scanLanQr() {
        // Xin quyền camera trước, ngay trong app (máy quét chỉ dùng quyền đã có).
        if (checkSelfPermission(Manifest.permission.CAMERA) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(Manifest.permission.CAMERA), REQ_CAMERA)
            return
        }
        IntentIntegrator(this)
            .setDesiredBarcodeFormats(IntentIntegrator.QR_CODE)
            .setPrompt(L("Quét mã QR trên trang đăng nhập NASfone", "Scan the QR code on the NASfone sign-in page"))
            .setOrientationLocked(false)
            .setBeepEnabled(false)
            .initiateScan()
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode != REQ_CAMERA) return
        if (grantResults.firstOrNull() == PackageManager.PERMISSION_GRANTED) scanLanQr()
        else toast(L("Cần quyền camera để quét mã QR", "Camera permission is needed to scan the QR code"))
    }

    @Deprecated("Activity result API needs AndroidX; this app uses the platform Activity")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        val res = IntentIntegrator.parseActivityResult(requestCode, resultCode, data)
        if (res == null) {
            super.onActivityResult(requestCode, resultCode, data)
            return
        }
        val text = res.contents ?: return // cancelled
        if (!Mobile.isLanQR(text)) {
            toast(L("Đây không phải mã đăng nhập LAN của NASfone", "This is not a NASfone LAN sign-in code"))
            return
        }
        thread {
            val info = try { JSONObject(Mobile.lanTicketInfo(text)) } catch (e: Exception) {
                runOnUiThread { toast(e.message ?: e.toString()) }
                return@thread
            }
            runOnUiThread { confirmLan(text, info) }
        }
    }

    private fun confirmLan(text: String, info: JSONObject) {
        AlertDialog.Builder(this)
            .setTitle(L("Cho phép trình duyệt này?", "Allow this browser?"))
            .setMessage(L(
                "Máy: ${info.optString("ip")}\n${info.optString("agent")}\n\nQuyền: chỉ xem và tải về.\nPhiên tự kết thúc sau 1 giờ không dùng, khi IP LAN của điện thoại đổi, hoặc khi server khởi động lại.",
                "Computer: ${info.optString("ip")}\n${info.optString("agent")}\n\nAccess: view and download only.\nThe session ends after 1 hour without use, when the phone's local IP changes, or when the server restarts."
            ))
            .setPositiveButton(L("Cho phép", "Allow")) { _, _ ->
                thread {
                    val msg = try {
                        Mobile.approveLan(text)
                        L("Đã cho phép, trình duyệt sẽ tự vào.", "Allowed; the browser signs in by itself.")
                    } catch (e: Exception) {
                        e.message ?: e.toString()
                    }
                    runOnUiThread { toast(msg); lastDevicesJson = ""; renderDevices() }
                }
            }
            .setNegativeButton(L("Không", "No"), null)
            .show()
    }

    private fun checkUpdate() {
        toast(L("Đang kiểm tra…", "Checking…"))
        Updater.maybeCheck(this, force = true) { rel, err ->
            runOnUiThread {
                when {
                    err != null -> toast(L("Không kiểm tra được: ", "Could not check: ") + err.message)
                    rel == null -> toast(L("Đang dùng bản mới nhất.", "You have the latest version."))
                    else -> render()
                }
            }
        }
    }

    private fun startUpdate() {
        // Android yêu cầu cho phép "cài ứng dụng không rõ nguồn" cho chính NASfone một lần.
        if (!packageManager.canRequestPackageInstalls()) {
            toast(L("Hãy cho phép NASfone cài ứng dụng, rồi bấm Cập nhật lần nữa.", "Allow NASfone to install apps, then tap Update again."))
            startActivity(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:$packageName")))
            return
        }
        Updater.install(this) { msg -> runOnUiThread { toast(L("Cập nhật lỗi: ", "Update failed: ") + msg) } }
    }

    private fun chooseLanguage() {
        val codes = arrayOf("", "en", "vi")
        AlertDialog.Builder(this)
            .setTitle(L("Ngôn ngữ", "Language"))
            .setItems(arrayOf(L("Theo ngôn ngữ của máy", "Follow the phone"), "English", "Tiếng Việt")) { _, which ->
                prefs.lang = codes[which]
                Lang.init(this)
                recreate() // rebuild the screen in the new language
            }
            .show()
    }

    private fun copy(s: String) {
        (getSystemService(CLIPBOARD_SERVICE) as ClipboardManager).setPrimaryClip(ClipData.newPlainText("NASfone", s))
        toast(L("Đã sao chép", "Copied"))
    }

    private fun toast(msg: String): Boolean {
        Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()
        return false
    }
}
