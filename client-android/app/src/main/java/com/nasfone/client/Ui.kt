package com.nasfone.client

import android.app.Activity
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup.LayoutParams.MATCH_PARENT
import android.view.ViewGroup.LayoutParams.WRAP_CONTENT
import android.view.WindowInsets
import android.widget.Button
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/** Small helpers for the programmatic layouts (no AndroidX views needed). */

const val ACCENT = "#7C4DFF" // the client's colour (the server app is blue)
const val ADMIN = "#B42318"
const val USER = "#2F6FED"

fun Activity.dp(v: Int) = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v.toFloat(), resources.displayMetrics).toInt()

fun Activity.text(s: String, size: Float = 15f, bold: Boolean = false, color: String? = null) = TextView(this).apply {
    text = s
    textSize = size
    if (bold) typeface = Typeface.DEFAULT_BOLD
    color?.let { setTextColor(Color.parseColor(it)) }
}

fun Activity.button(label: String, onClick: () -> Unit) = Button(this).apply {
    text = label
    isAllCaps = false
    setOnClickListener { onClick() }
}

fun Activity.vbox() = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
fun Activity.hbox() = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL; gravity = Gravity.CENTER_VERTICAL }
fun weighted() = LinearLayout.LayoutParams(0, WRAP_CONTENT, 1f)

fun Activity.section(col: LinearLayout, title: String) {
    col.addView(text(title, 14f, bold = true, color = ACCENT).apply { setPadding(0, dp(18), 0, dp(6)) })
}

/** A rounded card that holds a vertical column. */
fun Activity.card(): LinearLayout = vbox().apply {
    setPadding(dp(14), dp(12), dp(14), dp(12))
    background = GradientDrawable().apply {
        cornerRadius = dp(12).toFloat()
        setColor(Color.argb(18, 124, 77, 255))
    }
    layoutParams = LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { bottomMargin = dp(10) }
}

/** A scrolling page with system-bar padding; returns (root, column). */
fun Activity.page(): Pair<View, LinearLayout> {
    val col = vbox().apply { setPadding(dp(16), dp(12), dp(16), dp(24)) }
    val scroll = ScrollView(this).apply { addView(col) }
    scroll.setOnApplyWindowInsetsListener { v, insets ->
        val bars = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.ime())
        v.setPadding(bars.left, bars.top, bars.right, bars.bottom)
        insets
    }
    return scroll to col
}

fun Activity.toast(msg: String) {
    runOnUiThread { Toast.makeText(this, msg, Toast.LENGTH_LONG).show() }
}

fun humanSize(n: Long): String = when {
    n < 0 -> "?"
    n < 1024 -> "$n B"
    n < 1024L * 1024 -> String.format(Locale.US, "%.1f KB", n / 1024.0)
    n < 1024L * 1024 * 1024 -> String.format(Locale.US, "%.1f MB", n / 1048576.0)
    n < 1024L * 1024 * 1024 * 1024 -> String.format(Locale.US, "%.1f GB", n / 1073741824.0)
    else -> String.format(Locale.US, "%.2f TB", n / 1099511627776.0)
}

fun shortDate(ms: Long): String = if (ms <= 0) "" else SimpleDateFormat("HH:mm dd/MM/yyyy", Locale.US).format(Date(ms))

/** A friendly message for errors from the Go core. */
fun friendly(e: Throwable): String {
    val m = e.message ?: e.toString()
    return when {
        m.startsWith("revoked:") -> L("Server đã thu hồi máy này. Hãy ghép đôi lại.", "The server revoked this phone. Pair it again.")
        m.startsWith("readonly:") -> L("Máy này chỉ có quyền xem và tải về.", "This phone has view and download access only.")
        m.startsWith("notfound:") -> L("Không tìm thấy (có thể đã bị xoá).", "Not found (it may have been deleted).")
        m.contains("no address") || m.contains("timeout", true) || m.contains("dial tcp") || m.contains("no such host") ->
            L("Không kết nối được tới server. Kiểm tra mạng, hoặc server đang tắt.", "Cannot reach the server. Check the network or whether the server is running.")
        else -> m
    }
}
