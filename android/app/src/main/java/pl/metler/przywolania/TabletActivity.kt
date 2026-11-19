package pl.metler.przywolania

import android.app.Activity
import android.app.AlertDialog
import android.graphics.Color
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.Gravity
import android.view.WindowManager
import android.widget.GridLayout
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import org.json.JSONObject

/** Tablet na stanowisku: zalogowani pracownicy jako przyciski. */
class TabletActivity : Activity() {
    private lateinit var prefs: Prefs
    private lateinit var status: TextView
    private lateinit var grid: GridLayout
    private lateinit var empty: TextView
    private val ticker = Handler(Looper.getMainLooper())
    private val listener: () -> Unit = { render() }
    private val tick = object : Runnable {
        override fun run() {
            // odświeżanie licznika czasu tylko, gdy ktoś czeka na potwierdzenie
            if (AppState.tabletWorkers.any { it.status == "wezwany" || it.status == "brak_odpowiedzi" }) render()
            ticker.postDelayed(this, 1000)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = Prefs(this)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        val root = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        val (bar, st) = header(prefs.station) { openSettingsProtected() }
        status = st
        root.addView(bar)
        val hint = text("Dotknij osobę, aby wezwać ją do stanowiska. Dotknij ponownie, aby anulować.", 14f, color = C.MUTED)
        hint.setPadding(dp(16), dp(10), dp(16), 0)
        root.addView(hint)
        empty = text("Nikt nie jest zalogowany na terminalu.", 18f, color = C.MUTED).apply {
            gravity = Gravity.CENTER; setPadding(dp(16), dp(48), dp(16), dp(16))
        }
        root.addView(empty)
        grid = GridLayout(this).apply { setPadding(dp(10), dp(10), dp(10), dp(10)) }
        root.addView(ScrollView(this).apply { addView(grid) }, LinearLayout.LayoutParams(-1, 0, 1f))
        setContentView(root)
    }

    override fun onStart() {
        super.onStart()
        LinkService.start(this)
        AppState.listen(listener)
        render()
        ticker.post(tick)
    }

    override fun onStop() {
        AppState.unlisten(listener)
        ticker.removeCallbacks(tick)
        super.onStop()
    }

    private fun columns(): Int {
        val wDp = resources.displayMetrics.widthPixels / resources.displayMetrics.density
        return (wDp / 220).toInt().coerceIn(2, 6)
    }

    private fun mmss(ms: Long): String {
        val s = (ms / 1000).coerceAtLeast(0)
        return "%d:%02d".format(s / 60, s % 60)
    }

    private fun render() {
        val t = AppState.takeToast()
        if (t.isNotEmpty()) Toast.makeText(this, t, Toast.LENGTH_LONG).show()
        status.text = if (AppState.connected) "Połączono z serwerem" else "⚠ " + AppState.connectError.ifEmpty { "Łączenie…" }
        val list = AppState.tabletWorkers
        empty.gone(list.isNotEmpty() || !AppState.connected)
        val cols = columns()
        grid.removeAllViews()
        grid.columnCount = cols
        val now = System.currentTimeMillis() + AppState.serverTimeOffset
        val cell = (resources.displayMetrics.widthPixels - dp(20)) / cols - dp(12)
        for (w in list) {
            val mine = w.station == prefs.station
            val (bg, line2) = when (w.status) {
                "wezwany" -> C.YELLOW_BG to (if (mine) "Wezwany · ${mmss(now - w.since)}" else "Wezwany: ${w.station}")
                "brak_odpowiedzi" -> C.RED_BG to (if (mine) "Brak odpowiedzi · ${mmss(now - w.since)}" else "Brak odp.: ${w.station}")
                "idzie" -> C.BLUE_BG to (if (mine) "Idzie do Ciebie ✓" else "Idzie do: ${w.station}")
                "brak_polaczenia" -> C.GREY_BG to "Terminal bez połączenia"
                else -> C.GREEN_BG to "Dostępny"
            }
            val box = LinearLayout(this).apply {
                orientation = LinearLayout.VERTICAL
                gravity = Gravity.CENTER
                background = rounded(bg, dp(12).toFloat(), if (mine && w.status != "dostepny") dp(3) else dp(1),
                    if (mine && w.status != "dostepny") C.ORANGE else Color.parseColor("#CED4DA"))
                setPadding(dp(8), dp(12), dp(8), dp(12))
                minimumHeight = dp(110)
                isClickable = true
                setOnClickListener { onWorker(w) }
            }
            box.addView(text(w.name, 20f, true).apply { gravity = Gravity.CENTER })
            box.addView(text(line2, 15f, color = C.TEXT).apply { gravity = Gravity.CENTER; setPadding(0, dp(6), 0, 0) })
            if (!w.online && w.status != "brak_polaczenia") {
                box.addView(text("terminal bez połączenia", 12f, color = C.MUTED).apply { gravity = Gravity.CENTER })
            }
            val params = GridLayout.LayoutParams().apply {
                width = cell; height = GridLayout.LayoutParams.WRAP_CONTENT
                setMargins(dp(6), dp(6), dp(6), dp(6))
            }
            grid.addView(box, params)
        }
    }

    private fun onWorker(w: WorkerState) {
        val mine = w.station == prefs.station
        if (mine && (w.status == "wezwany" || w.status == "brak_odpowiedzi")) {
            AlertDialog.Builder(this)
                .setTitle(w.name)
                .setPositiveButton("Wezwij ponownie") { _, _ -> call(w) }
                .setNegativeButton("Anuluj wezwanie") { _, _ ->
                    LinkService.send(JSONObject().put("t", "cancel").put("worker", w.id))
                }
                .setNeutralButton("Zamknij", null)
                .show()
            return
        }
        call(w)
    }

    private fun call(w: WorkerState) {
        if (!LinkService.send(JSONObject().put("t", "call").put("worker", w.id))) {
            Toast.makeText(this, "Brak połączenia z serwerem", Toast.LENGTH_LONG).show()
        }
    }
}
