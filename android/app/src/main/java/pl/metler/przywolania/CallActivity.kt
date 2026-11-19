package pl.metler.przywolania

import android.app.Activity
import android.graphics.Color
import android.os.Build
import android.os.Bundle
import android.view.Gravity
import android.view.WindowManager
import android.widget.LinearLayout

/** Ekran wezwania: duży napis ze stanowiskiem i przycisk "IDĘ". */
class CallActivity : Activity() {
    private lateinit var root: LinearLayout
    private val listener: () -> Unit = { render() }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (Build.VERSION.SDK_INT >= 27) { setShowWhenLocked(true); setTurnScreenOn(true) }
        else @Suppress("DEPRECATION") window.addFlags(WindowManager.LayoutParams.FLAG_SHOW_WHEN_LOCKED or WindowManager.LayoutParams.FLAG_TURN_SCREEN_ON)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
            setBackgroundColor(C.ORANGE)
            setPadding(dp(24), dp(24), dp(24), dp(24))
        }
        setContentView(root)
    }

    override fun onStart() { super.onStart(); AppState.listen(listener); render() }
    override fun onStop() { AppState.unlisten(listener); super.onStop() }

    private fun render() {
        val c = AppState.activeCall
        if (c == null) { finish(); return }
        root.removeAllViews()
        root.addView(text("PODEJDŹ DO", 28f, true, Color.WHITE).apply { gravity = Gravity.CENTER })
        root.addView(text(c.station, 44f, true, Color.WHITE).apply { gravity = Gravity.CENTER; setPadding(0, dp(12), 0, dp(40)) })
        root.addView(bigButton("IDĘ", Color.WHITE, C.ORANGE) {
            LinkService.instance?.ack() ?: LinkService.start(this, LinkService.ACTION_ACK)
            finish()
        }.apply { textSize = 32f; minHeight = dp(120) }, lp())
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() { /* wezwanie zamyka tylko "IDĘ" albo anulowanie ze stanowiska */ }
}
