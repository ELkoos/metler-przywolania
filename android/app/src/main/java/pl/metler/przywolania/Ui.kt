package pl.metler.przywolania

import android.app.Activity
import android.app.AlertDialog
import android.content.Context
import android.content.Intent
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.text.InputType
import android.view.Gravity
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView

object C {
    val NAVY = Color.parseColor("#1F3A5F")
    val ORANGE = Color.parseColor("#E8590C")
    val GREEN = Color.parseColor("#2B8A3E")
    val GREEN_BG = Color.parseColor("#D3F9D8")
    val GREY_BG = Color.parseColor("#E9ECEF")
    val YELLOW_BG = Color.parseColor("#FFE8A3")
    val RED = Color.parseColor("#C92A2A")
    val RED_BG = Color.parseColor("#FFC9C9")
    val BLUE_BG = Color.parseColor("#D0EBFF")
    val TEXT = Color.parseColor("#1D1D1F")
    val MUTED = Color.parseColor("#6C757D")
}

fun Context.dp(v: Int): Int = (v * resources.displayMetrics.density).toInt()

fun rounded(color: Int, radius: Float, stroke: Int = 0, strokeColor: Int = 0) = GradientDrawable().apply {
    setColor(color); cornerRadius = radius
    if (stroke > 0) setStroke(stroke, strokeColor)
}

fun Context.text(s: String, size: Float = 16f, bold: Boolean = false, color: Int = C.TEXT) = TextView(this).apply {
    text = s; textSize = size; setTextColor(color)
    if (bold) setTypeface(typeface, Typeface.BOLD)
}

fun Context.bigButton(s: String, bg: Int = C.NAVY, fg: Int = Color.WHITE, onClick: () -> Unit) = Button(this).apply {
    text = s; textSize = 18f; setTextColor(fg); isAllCaps = false
    background = rounded(bg, dp(10).toFloat())
    minHeight = dp(56)
    setPadding(dp(16), dp(8), dp(16), dp(8))
    setOnClickListener { onClick() }
}

fun Context.vbox(pad: Int = 16) = LinearLayout(this).apply {
    orientation = LinearLayout.VERTICAL
    setPadding(dp(pad), dp(pad), dp(pad), dp(pad))
}

fun lp(w: Int = LinearLayout.LayoutParams.MATCH_PARENT, h: Int = LinearLayout.LayoutParams.WRAP_CONTENT, top: Int = 0) =
    LinearLayout.LayoutParams(w, h).apply { topMargin = top }

fun View.gone(g: Boolean) { visibility = if (g) View.GONE else View.VISIBLE }

/** Pyta o klucz urządzenia przed wejściem w ustawienia (żeby pracownik przypadkiem nie zmienił trybu). */
fun Activity.openSettingsProtected() {
    val prefs = Prefs(this)
    if (prefs.key.isEmpty()) { startActivity(Intent(this, SettingsActivity::class.java)); return }
    val input = EditText(this).apply {
        hint = "Klucz urządzenia"
        inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
    }
    AlertDialog.Builder(this)
        .setTitle("Ustawienia")
        .setMessage("Podaj klucz urządzenia (z panelu serwera).")
        .setView(input)
        .setPositiveButton("OK") { _, _ ->
            if (input.text.toString().trim() == prefs.key) startActivity(Intent(this, SettingsActivity::class.java))
        }
        .setNegativeButton("Anuluj", null)
        .show()
}

fun Context.header(title: String, onSettings: () -> Unit): Pair<LinearLayout, TextView> {
    val bar = LinearLayout(this).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
        setBackgroundColor(C.NAVY)
        setPadding(dp(16), dp(10), dp(8), dp(10))
    }
    val col = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
    col.addView(text(title, 20f, true, Color.WHITE))
    val status = text("", 14f, false, Color.parseColor("#CFE0F5"))
    col.addView(status)
    bar.addView(col, LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f))
    val gear = Button(this).apply {
        text = "⚙"; textSize = 22f; setTextColor(Color.WHITE); background = null
        minWidth = dp(48); minHeight = dp(48)
        setOnClickListener { onSettings() }
    }
    bar.addView(gear)
    return bar to status
}
