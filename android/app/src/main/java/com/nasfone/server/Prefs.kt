package com.nasfone.server

import android.content.Context
import android.os.Environment
import java.io.File

/** Cài đặt của server. Không có gì về tài khoản Tailscale được gán cứng ở đây. */
class Prefs(private val ctx: Context) {
    private val sp = ctx.getSharedPreferences("nasfone", Context.MODE_PRIVATE)

    var hostname: String
        get() = sp.getString("hostname", "nasfone")!!
        set(v) = sp.edit().putString("hostname", v).apply()

    /** Trống = máy chủ điều khiển mặc định của Tailscale. Điền URL nếu dùng Headscale. */
    var controlUrl: String
        get() = sp.getString("controlUrl", "")!!
        set(v) = sp.edit().putString("controlUrl", v).apply()

    var rootDir: String
        get() = sp.getString("rootDir", File(Environment.getExternalStorageDirectory(), "NASfone").path)!!
        set(v) = sp.edit().putString("rootDir", v).apply()

    init {
        // Bản cũ có mật khẩu truy cập; cơ chế này đã bị bỏ, xóa giá trị còn lưu.
        if (sp.contains("password")) sp.edit().remove("password").apply()
    }

    var funnel: Boolean
        get() = sp.getBoolean("funnel", false)
        set(v) = sp.edit().putBoolean("funnel", v).apply()

    /** Kết nối LAN: trình duyệt cùng mạng mở http://IP:cổng, đăng nhập bằng mã 6 số hoặc QR. */
    var lanEnabled: Boolean
        get() = sp.getBoolean("lanEnabled", false)
        set(v) = sp.edit().putBoolean("lanEnabled", v).apply()

    var lanPort: Int
        get() = sp.getInt("lanPort", 8080)
        set(v) = sp.edit().putInt("lanPort", v).apply()

    var verboseLog: Boolean
        get() = sp.getBoolean("verboseLog", false)
        set(v) = sp.edit().putBoolean("verboseLog", v).apply()

    /** "" = follow the phone, "vi" or "en". */
    var lang: String
        get() = sp.getString("lang", "")!!
        set(v) = sp.edit().putString("lang", v).apply()

    var autoStart: Boolean
        get() = sp.getBoolean("autoStart", false)
        set(v) = sp.edit().putBoolean("autoStart", v).apply()

    /** Trạng thái đăng nhập tailnet; xóa thư mục này = quên tài khoản. */
    val stateDir: File get() = File(ctx.filesDir, "tsnet")
}
