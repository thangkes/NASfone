package com.pocketnas.server

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
import com.pocketnas.core.mobile.Mobile
import org.json.JSONArray
import org.json.JSONObject
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlin.concurrent.thread

class MainActivity : Activity() {
    private lateinit var prefs: Prefs

    private lateinit var stateTv: TextView
    private lateinit var accountBox: LinearLayout
    private lateinit var addrBox: LinearLayout
    private lateinit var toggleBtn: Button
    private lateinit var loginBtn: Button
    private lateinit var logoutBtn: Button
    private lateinit var funnelSw: Switch
    private lateinit var funnelTv: TextView
    private lateinit var funnelHelpBtn: Button
    private lateinit var hostEt: EditText
    private lateinit var controlEt: EditText
    private lateinit var portEt: EditText
    private lateinit var rootEt: EditText
    private lateinit var passEt: EditText
    private lateinit var verboseCb: CheckBox
    private lateinit var autoCb: CheckBox
    private lateinit var permTv: TextView
    private lateinit var logTv: TextView
    private lateinit var codeTv: TextView
    private lateinit var codeInfoTv: TextView
    private lateinit var codeBar: ProgressBar
    private lateinit var codeCopyBtn: Button
    private lateinit var devBox: LinearLayout

    private var code: String? = null
    private var lastDevicesJson = ""
    private val ticker = Handler(Looper.getMainLooper())
    private val tick = object : Runnable {
        override fun run() {
            // Lỗi hiển thị không được làm sập app (service vẫn chạy trong cùng tiến trình).
            try {
                renderCode()
                renderDevices()
            } catch (e: Exception) {
                Core.log("Lỗi hiển thị: $e")
            }
            ticker.postDelayed(this, 1000)
        }
    }
    private val dateFmt = SimpleDateFormat("HH:mm dd/MM", Locale.US)

    private val onChange: () -> Unit = { render() }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = Prefs(this)
        setContentView(buildUi())
        // Mở app là có mã đăng nhập: tự khởi động server nếu đang dừng.
        if (!Core.running && savedInstanceState == null) toggle()
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

        col.addView(text("PocketNAS Server", 24f, bold = true))
        stateTv = text("", 16f, bold = true).also { col.addView(it) }
        toggleBtn = button("") { toggle() }.also { col.addView(it) }

        section(col, "Mã đăng nhập (6 số, đổi mỗi phút)")
        codeTv = text("— — — —", 34f, bold = true).apply {
            typeface = Typeface.create(Typeface.MONOSPACE, Typeface.BOLD)
            gravity = Gravity.CENTER
            letterSpacing = 0.12f
        }.also { col.addView(it, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT)) }
        codeBar = ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply { max = 60 }
            .also { col.addView(it, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT)) }
        codeInfoTv = text("", 13f).apply { gravity = Gravity.CENTER }
            .also { col.addView(it, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT)) }
        codeCopyBtn = button("Sao chép mã") { code?.let { copy(it.replace(" ", "")) } }.also { col.addView(it) }

        section(col, "Thiết bị đã đăng nhập")
        devBox = vbox().also { col.addView(it) }
        col.addView(button("Thu hồi tất cả") {
            AlertDialog.Builder(this)
                .setTitle("Thu hồi tất cả thiết bị?")
                .setMessage("Mọi trình duyệt đang đăng nhập sẽ bị đăng xuất ngay.")
                .setPositiveButton("Thu hồi") { _, _ -> thread { Mobile.revokeAllDevices(); runOnUiThread { lastDevicesJson = ""; renderDevices() } } }
                .setNegativeButton("Hủy", null)
                .show()
        })

        section(col, "Tài khoản Tailscale")
        accountBox = vbox().also { col.addView(it) }
        val accBtns = hbox().also { col.addView(it) }
        loginBtn = button("Đăng nhập") { login() }.also { accBtns.addView(it, weighted()) }
        logoutBtn = button("Đăng xuất / đổi tài khoản") { logout() }.also { accBtns.addView(it, weighted()) }

        section(col, "Địa chỉ truy cập")
        addrBox = vbox().also { col.addView(it) }
        col.addView(row("Mật khẩu (giai đoạn 1)", prefs.password))

        section(col, "Funnel (truy cập công khai qua Internet)")
        funnelSw = Switch(this).apply {
            text = "Bật Funnel"
            isChecked = prefs.funnel
            setOnCheckedChangeListener { _, on ->
                prefs.funnel = on
                if (Core.running) thread { Mobile.setFunnel(on) }
            }
        }
        col.addView(funnelSw)
        funnelTv = text("", 13f).also { col.addView(it) }
        funnelHelpBtn = button("Mở trang cài đặt Tailscale để sửa") {
            Core.status.optString("funnelHelpURL").takeIf { it.isNotEmpty() }?.let {
                startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(it)))
            }
        }.also { col.addView(it) }

        section(col, "Cài đặt (áp dụng khi khởi động lại)")
        hostEt = field(col, "Tên máy trong tailnet", prefs.hostname)
        controlEt = field(col, "Máy chủ điều khiển (để trống = Tailscale; điền URL nếu dùng Headscale)", prefs.controlUrl)
        portEt = field(col, "Cổng LAN", prefs.lanPort.toString(), InputType.TYPE_CLASS_NUMBER)
        rootEt = field(col, "Thư mục lưu trữ", prefs.rootDir)
        passEt = field(col, "Mật khẩu truy cập", prefs.password)
        verboseCb = CheckBox(this).apply { text = "Ghi log chi tiết của Tailscale"; isChecked = prefs.verboseLog }.also { col.addView(it) }
        autoCb = CheckBox(this).apply {
            text = "Tự chạy khi khởi động máy"
            isChecked = prefs.autoStart
            setOnCheckedChangeListener { _, on -> prefs.autoStart = on }
        }.also { col.addView(it) }
        col.addView(button("Lưu cài đặt") { save() })

        section(col, "Quyền & chống bị tắt nền (MagicOS)")
        permTv = text("", 14f).also { col.addView(it) }
        col.addView(button("Cấp quyền truy cập tất cả file") {
            startActivity(Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION, Uri.parse("package:$packageName")))
        })
        col.addView(button("Bỏ tối ưu pin") {
            @Suppress("BatteryLife")
            startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName")))
        })
        col.addView(button("Mở cài đặt ứng dụng (Khởi chạy ứng dụng → thủ công)") {
            startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:$packageName")))
        })

        section(col, "Nhật ký")
        logTv = text("", 11f).apply { typeface = Typeface.MONOSPACE; setTextIsSelectable(true) }.also { col.addView(it) }
        col.addView(button("Sao chép nhật ký") { copy(Core.logLines().joinToString("\n")) })
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

    /** Một dòng "nhãn: giá trị" kèm nút sao chép. */
    private fun row(label: String, value: String) = hbox().apply {
        gravity = Gravity.CENTER_VERTICAL
        addView(text("$label\n$value", 14f).apply { setTextIsSelectable(true) }, weighted())
        addView(button("Sao chép") { copy(value) })
    }

    // ---------------------------------------------------------------- render

    private fun render() {
        val st = Core.status
        val backend = st.optString("backendState")
        val running = Core.running

        stateTv.text = when {
            !running && Core.startError != null -> "⛔ Lỗi: ${Core.startError}"
            !running -> "○ Đã dừng"
            backend == "Running" -> "● Đang chạy • tailnet đã kết nối"
            backend == "NeedsLogin" -> "● Đang chạy • chờ đăng nhập Tailscale"
            backend.isEmpty() -> "● Đang khởi động…"
            else -> "● Đang chạy • $backend"
        }
        stateTv.setTextColor(Color.parseColor(if (running) "#1F9D55" else "#D64545"))
        toggleBtn.text = if (running) "Dừng server" else "Khởi động server"

        accountBox.removeAllViews()
        if (running) {
            val login = st.optString("loginName")
            val tailnet = st.optString("tailnetName")
            accountBox.addView(text(
                when {
                    login.isNotEmpty() -> "Tài khoản: $login\nTailnet: $tailnet"
                    backend == "NeedsLogin" -> "Chưa đăng nhập. Bấm \"Đăng nhập\" để mở trang đăng nhập Tailscale."
                    else -> "…"
                }, 14f
            ))
            val auth = st.optString("authURL")
            if (auth.isNotEmpty()) accountBox.addView(row("Link đăng nhập (mở trên máy này hoặc máy khác)", auth))
        } else {
            accountBox.addView(text("Khởi động server để đăng nhập.", 14f))
        }
        loginBtn.isEnabled = running && backend != "Running"
        logoutBtn.isEnabled = running && backend == "Running"

        addrBox.removeAllViews()
        if (running) {
            val dns = st.optString("dnsName")
            if (dns.isNotEmpty()) addrBox.addView(row("Tailnet", "http://$dns"))
            val ips = st.optJSONArray("tailscaleIPs")
            if (ips != null && ips.length() > 0) addrBox.addView(row("Tailnet IP", "http://${ips.getString(0)}"))
            val port = st.optInt("lanPort", prefs.lanPort)
            for ((label, ip) in Core.lanAddresses()) addrBox.addView(row(label, "http://$ip:$port"))
            val funnel = st.optString("funnelURL")
            if (funnel.isNotEmpty()) addrBox.addView(row("Funnel (công khai)", funnel))
        } else {
            addrBox.addView(text("—", 14f))
        }

        funnelTv.text = when {
            st.optString("funnelError").isNotEmpty() ->
                "⚠ ${st.optString("funnelError")}\nApp tự thử lại mỗi 20 giây sau khi bạn sửa."
            st.optString("funnelURL").isNotEmpty() -> "Đang mở công khai: ${st.optString("funnelURL")}"
            prefs.funnel -> "Sẽ mở khi tailnet kết nối xong."
            else -> "Tắt — chỉ truy cập qua LAN và tailnet."
        }
        funnelHelpBtn.visibility = if (st.optString("funnelHelpURL").isNotEmpty()) View.VISIBLE else View.GONE

        val files = Environment.isExternalStorageManager()
        val battery = (getSystemService(POWER_SERVICE) as PowerManager).isIgnoringBatteryOptimizations(packageName)
        permTv.text = "${if (files) "✔" else "✘"} Truy cập tất cả file\n${if (battery) "✔" else "✘"} Bỏ tối ưu pin"

        logTv.text = Core.logLines().takeLast(80).joinToString("\n")
    }

    private fun renderCode() {
        if (!::codeTv.isInitialized) return
        val json = if (Core.running) Mobile.loginCode() else ""
        if (json.isEmpty()) {
            code = null
            codeTv.text = "— — —"
            codeBar.progress = 0
            codeInfoTv.text = "Khởi động server để có mã đăng nhập."
        } else {
            val o = JSONObject(json)
            code = o.getString("code")
            val step = o.optInt("step", 60)
            val left = ((o.getLong("expires") - System.currentTimeMillis() + 999) / 1000).toInt().coerceIn(0, step)
            codeTv.text = code
            codeBar.max = step
            codeBar.progress = left
            codeInfoTv.text = "Đổi mã sau %d:%02d • mỗi mã dùng 1 lần".format(left / 60, left % 60)
        }
        codeCopyBtn.isEnabled = code != null
    }

    private fun renderDevices() {
        if (!::devBox.isInitialized) return
        val json = if (Core.running) Mobile.devices() else "[]"
        if (json == lastDevicesJson) return
        lastDevicesJson = json
        devBox.removeAllViews()
        val arr = try { JSONArray(json) } catch (_: Exception) { JSONArray() }
        if (arr.length() == 0) {
            devBox.addView(text(if (Core.running) "Chưa có thiết bị nào." else "—", 14f))
            return
        }
        for (i in 0 until arr.length()) {
            val d = arr.getJSONObject(i)
            val id = d.getString("id")
            val name = d.getString("name")
            val seen = parseTime(d.optString("lastSeen"))
            val info = "${d.optString("via")} • ${d.optString("lastIP")} • lần cuối $seen • hết hạn ${parseTime(d.optString("expires"))}"
            devBox.addView(hbox().apply {
                gravity = Gravity.CENTER_VERTICAL
                addView(text("$name\n$info", 13f), weighted())
                addView(button("Thu hồi") {
                    thread { Mobile.revokeDevice(id); runOnUiThread { lastDevicesJson = ""; renderDevices() } }
                })
            })
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
        val port = portEt.text.toString().toIntOrNull()
        if (port == null || port !in 1024..65535) return toast("Cổng phải trong khoảng 1024–65535")
        val pass = passEt.text.toString().trim()
        if (pass.length < 6) return toast("Mật khẩu tối thiểu 6 ký tự")
        val host = hostEt.text.toString().trim().lowercase()
        if (!host.matches(Regex("[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?"))) return toast("Tên máy chỉ gồm a-z, 0-9 và dấu -")
        val control = controlEt.text.toString().trim()
        if (control.isNotEmpty() && !control.startsWith("https://")) return toast("Máy chủ điều khiển phải bắt đầu bằng https://")

        if (control != prefs.controlUrl) {
            // Trạng thái đăng nhập gắn với máy chủ điều khiển cũ; xóa để đăng nhập lại.
            if (Core.running) return toast("Dừng server trước khi đổi máy chủ điều khiển")
            prefs.stateDir.deleteRecursively()
            Core.log("Đã đổi máy chủ điều khiển, cần đăng nhập lại")
        }
        prefs.lanPort = port
        prefs.password = pass
        prefs.hostname = host
        prefs.controlUrl = control
        prefs.rootDir = rootEt.text.toString().trim()
        prefs.verboseLog = verboseCb.isChecked
        if (Core.running) toast("Đã lưu. Khởi động lại server để áp dụng.") else toast("Đã lưu")
        return true
    }

    private fun login() {
        val auth = Core.status.optString("authURL")
        if (auth.isNotEmpty()) {
            startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(auth)))
            return
        }
        thread {
            try { Mobile.login() } catch (e: Exception) { Core.log("Đăng nhập: ${e.message}") }
        }
        toast("Đang lấy link đăng nhập…")
    }

    private fun logout() {
        AlertDialog.Builder(this)
            .setTitle("Đăng xuất Tailscale?")
            .setMessage("Server sẽ rời tailnet hiện tại. Sau đó bấm \"Đăng nhập\" để dùng tài khoản khác.")
            .setPositiveButton("Đăng xuất") { _, _ ->
                thread {
                    try { Mobile.logout() } catch (e: Exception) { Core.log("Đăng xuất: ${e.message}") }
                }
            }
            .setNegativeButton("Hủy", null)
            .show()
    }

    private fun copy(s: String) {
        (getSystemService(CLIPBOARD_SERVICE) as ClipboardManager).setPrimaryClip(ClipData.newPlainText("PocketNAS", s))
        toast("Đã sao chép")
    }

    private fun toast(msg: String): Boolean {
        Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()
        return false
    }
}
