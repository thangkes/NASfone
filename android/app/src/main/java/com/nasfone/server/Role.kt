package com.nasfone.server

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.util.TypedValue
import android.view.ViewGroup.LayoutParams.MATCH_PARENT
import android.view.ViewGroup.LayoutParams.WRAP_CONTENT
import android.view.WindowInsets
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import java.io.File

/**
 * What this phone does: share its storage (server) or use NAS servers
 * elsewhere (client). Chosen on first launch, changeable in settings.
 */
object Role {
    const val SERVER = "server"
    const val CLIENT = "client"

    private fun prefs(ctx: Context) = ctx.applicationContext.getSharedPreferences("nasfone", Context.MODE_PRIVATE)

    /** "server", "client" or "" (not chosen yet). Installs from before the choice existed were servers. */
    fun get(ctx: Context): String {
        prefs(ctx).getString("role", null)?.let { return it }
        val sp = prefs(ctx)
        if (sp.contains("hostname") || sp.contains("autoStart") || File(ctx.filesDir, "tsnet").exists()) {
            set(ctx, SERVER)
            return SERVER
        }
        return ""
    }

    fun set(ctx: Context, role: String) {
        prefs(ctx).edit().putString("role", role).commit()
    }

    /** Opens the screen for the current role (or the chooser) and closes the caller. */
    fun launch(from: Activity, role: String = get(from)) {
        val cls = if (role == CLIENT) com.nasfone.client.MainActivity::class.java else MainActivity::class.java
        from.startActivity(Intent(from, cls).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK))
        from.finish()
    }

    /** The first-launch chooser. */
    fun chooser(a: Activity, onPick: (String) -> Unit): ScrollView {
        fun dp(v: Int) = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v.toFloat(), a.resources.displayMetrics).toInt()
        fun text(s: String, size: Float, bold: Boolean = false) = TextView(a).apply {
            text = s; textSize = size
            if (bold) typeface = Typeface.DEFAULT_BOLD
        }
        val col = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL; setPadding(dp(20), dp(24), dp(20), dp(24)) }
        col.addView(text("NASfone", 26f, bold = true))
        col.addView(text(L("Máy này dùng để làm gì?", "What is this phone for?"), 18f).apply { setPadding(0, dp(6), 0, dp(18)) })
        fun card(icon: String, title: String, desc: String, color: String, role: String) {
            val c = LinearLayout(a).apply {
                orientation = LinearLayout.VERTICAL
                setPadding(dp(18), dp(16), dp(18), dp(16))
                background = GradientDrawable().apply {
                    cornerRadius = dp(16).toFloat()
                    setColor(Color.parseColor(color))
                }
                isClickable = true
                setOnClickListener { onPick(role) }
                layoutParams = LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { bottomMargin = dp(14) }
            }
            c.addView(text("$icon  $title", 19f, bold = true).apply { setTextColor(Color.WHITE) })
            c.addView(text(desc, 14f).apply { setTextColor(Color.WHITE); setPadding(0, dp(6), 0, 0) })
            col.addView(c)
        }
        card("📦", L("Làm server", "Be the server"),
            L("Máy này chứa file. Các máy khác truy cập qua web, app Windows hoặc app NASfone trên điện thoại khác.",
                "This phone stores the files. Other devices reach them through the web, the Windows app or NASfone on another phone."),
            "#2F6FED", SERVER)
        card("📲", L("Làm client", "Be a client"),
            L("Dùng NAS ở máy khác: duyệt, tải lên/tải về, hiện trong app Tệp và tự sao lưu ảnh.",
                "Use a NAS elsewhere: browse, upload/download, show it in the Files app and back up photos."),
            "#7C4DFF", CLIENT)
        col.addView(text(L("Có thể đổi lại trong phần Cài đặt.", "You can change this later in Settings."), 13f))
        return ScrollView(a).apply {
            addView(col)
            setOnApplyWindowInsetsListener { v, insets ->
                val bars = insets.getInsets(WindowInsets.Type.systemBars())
                v.setPadding(bars.left, bars.top, bars.right, bars.bottom)
                insets
            }
        }
    }
}
