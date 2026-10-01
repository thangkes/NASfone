package com.nasfone.server

import android.content.Context
import java.util.Locale

/** UI language: the in-app choice, else the phone's language (Vietnamese or English). */
object Lang {
    @Volatile
    var vi: Boolean = Locale.getDefault().language == "vi"
        private set

    fun init(ctx: Context) {
        vi = when (Prefs(ctx).lang) {
            "vi" -> true
            "en" -> false
            else -> Locale.getDefault().language == "vi"
        }
    }
}

/** Picks the Vietnamese or English text for the current UI language. */
fun L(vi: String, en: String): String = if (Lang.vi) vi else en
