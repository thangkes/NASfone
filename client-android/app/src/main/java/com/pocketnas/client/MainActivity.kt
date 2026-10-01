package com.pocketnas.client

import android.app.Activity
import android.os.Bundle
import android.view.Gravity
import android.widget.TextView

// Man hinh tam (khung project). Tinh nang that se them theo docs/DESIGN.md.
class MainActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(TextView(this).apply {
            text = getString(R.string.app_name) + "\n" + getString(R.string.app_desc)
            gravity = Gravity.CENTER
            textSize = 18f
        })
    }
}
