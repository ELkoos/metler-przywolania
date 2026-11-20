package pl.metler.przywolania

import android.app.Activity
import android.graphics.Color
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.Gravity
import android.view.WindowManager
import android.widget.LinearLayout

/** Ekran alertu brygadzisty: wezwany pracownik nie odpowiada od ponad 60 s. */
class AlertActivity : Activity() {
    private lateinit var root: LinearLayout
    private val listener: () -> Unit = { render() }
    private val tick = Handler(Looper.getMainLooper())
    private val refresh = object : Runnable { override fun run() { render(); tick.postDelayed(this, 1000) } }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (Build.VERSION.SDK_INT >= 27) { setShowWhenLocked(true); setTurnScreenOn(true) }
        else @Suppress("DEPRECATION") window.addFlags(WindowManager.LayoutParams.FLAG_SHOW_WHEN_LOCKED or WindowManager.LayoutParams.FLAG_TURN_SCREEN_ON)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
            setBackgroundColor(C.RED)
            setPadding(dp(24), dp(24), dp(24), dp(24))
        }
        setContentView(root)
    }

    override fun onStart() { super.onStart(); AppState.listen(listener); tick.post(refresh) }
    override fun onStop() { tick.removeCallbacks(refresh); AppState.unlisten(listener); super.onStop() }

    private fun render() {
        val list = AppState.alerts
        if (list.isEmpty()) { finish(); return }
        val now = System.currentTimeMillis() + AppState.serverTimeOffset
        root.removeAllViews()
        root.addView(text("BRAK ODPOWIEDZI", 26f, true, Color.WHITE).apply { gravity = Gravity.CENTER; setPadding(0, 0, 0, dp(16)) })
        for (a in list) {
            val secs = ((now - a.at) / 1000).coerceAtLeast(0)
            root.addView(text(a.station, 40f, true, Color.WHITE).apply { gravity = Gravity.CENTER })
            root.addView(text("${a.worker} nie odpowiada od ${secs / 60}:${"%02d".format(secs % 60)}", 20f, false, Color.WHITE)
                .apply { gravity = Gravity.CENTER; setPadding(0, dp(4), 0, dp(20)) })
        }
        root.addView(bigButton("OK, WIEM", Color.WHITE, C.RED) {
            LinkService.instance?.ackAlerts()
            finish()
        }.apply { textSize = 28f; minHeight = dp(110) }, lp(top = dp(12)))
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() { /* alert zamyka "OK, WIEM" albo odpowiedź pracownika */ }
}
