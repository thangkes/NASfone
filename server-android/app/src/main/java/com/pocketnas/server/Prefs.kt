package com.pocketnas.server

import android.content.Context
import android.os.Environment
import java.io.File
import java.security.SecureRandom

/** Cài đặt của server. Không có gì về tài khoản Tailscale được gán cứng ở đây. */
class Prefs(private val ctx: Context) {
    private val sp = ctx.getSharedPreferences("pocketnas", Context.MODE_PRIVATE)

    var hostname: String
        get() = sp.getString("hostname", "pocketnas")!!
        set(v) = sp.edit().putString("hostname", v).apply()

    /** Trống = máy chủ điều khiển mặc định của Tailscale. Điền URL nếu dùng Headscale. */
    var controlUrl: String
        get() = sp.getString("controlUrl", "")!!
        set(v) = sp.edit().putString("controlUrl", v).apply()

    var rootDir: String
        get() = sp.getString("rootDir", File(Environment.getExternalStorageDirectory(), "PocketNAS").path)!!
        set(v) = sp.edit().putString("rootDir", v).apply()

    /** Mật khẩu tạm của giai đoạn 1 (sẽ thay bằng ghép đôi cặp khóa). Tự sinh lần đầu. */
    var password: String
        get() = sp.getString("password", null) ?: randomPassword().also { password = it }
        set(v) = sp.edit().putString("password", v).apply()

    var funnel: Boolean
        get() = sp.getBoolean("funnel", false)
        set(v) = sp.edit().putBoolean("funnel", v).apply()

    var verboseLog: Boolean
        get() = sp.getBoolean("verboseLog", false)
        set(v) = sp.edit().putBoolean("verboseLog", v).apply()

    var autoStart: Boolean
        get() = sp.getBoolean("autoStart", false)
        set(v) = sp.edit().putBoolean("autoStart", v).apply()

    /** Trạng thái đăng nhập tailnet; xóa thư mục này = quên tài khoản. */
    val stateDir: File get() = File(ctx.filesDir, "tsnet")

    private fun randomPassword(): String {
        val alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
        val rnd = SecureRandom()
        return (1..10).map { alphabet[rnd.nextInt(alphabet.length)] }.joinToString("")
    }
}
