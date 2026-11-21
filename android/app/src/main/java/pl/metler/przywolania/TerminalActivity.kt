package pl.metler.przywolania

import android.app.Activity
import android.app.AlertDialog
import android.content.Intent
import android.os.Bundle
import android.text.InputType
import android.view.Gravity
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import org.json.JSONObject

/** Terminal pracownika: logowanie (wybór osoby + PIN) i stan. */
class TerminalActivity : Activity() {
    private lateinit var prefs: Prefs
    private lateinit var status: TextView
    private lateinit var body: LinearLayout
    private val listener: () -> Unit = { render() }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = Prefs(this)
        val root = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        val (bar, st) = header("Przywołania") { openSettingsProtected() }
        status = st
        root.addView(bar)
        body = vbox(16)
        root.addView(ScrollView(this).apply { addView(body) }, LinearLayout.LayoutParams(-1, 0, 1f))
        setContentView(root)
    }

    override fun onResume() { super.onResume(); render() }

    override fun onStart() {
        super.onStart()
        LinkService.start(this)
        AppState.listen(listener)
        render()
    }

    override fun onStop() {
        AppState.unlisten(listener)
        super.onStop()
    }

    private fun render() {
        val t = AppState.takeToast()
        if (t.isNotEmpty()) Toast.makeText(this, t, Toast.LENGTH_LONG).show()
        status.text = if (AppState.connected) "Połączono z serwerem" else "⚠ " + AppState.connectError.ifEmpty { "Łączenie…" }
        body.removeAllViews()
        for (p in permIssues(terminal = true)) {
            body.addView(bigButton("⚠ Włącz: ${p.label}", C.ORANGE) { p.fix(this) }, lp(top = dp(8)))
        }
        if (AppState.activeCall != null) {
            body.addView(bigButton("Wezwanie: ${AppState.activeCall?.station} – otwórz", C.ORANGE) {
                startActivity(Intent(this, CallActivity::class.java))
            }, lp(top = dp(8)))
        }
        if (AppState.loggedInName.isNotEmpty()) {
            body.addView(text("Zalogowany", 16f, color = C.MUTED), lp(top = dp(16)))
            body.addView(text(AppState.loggedInName, 28f, true))
            body.addView(text("Wezwania ze stanowisk pakowania pojawią się na tym terminalu z dźwiękiem i wibracją.", 15f, color = C.MUTED), lp(top = dp(8)))
            body.addView(bigButton("Wyloguj", C.NAVY) {
                LinkService.send(JSONObject().put("t", "logout"))
                prefs.workerId = ""; prefs.workerPin = ""
                AppState.update { loggedInName = "" }
            }, lp(top = dp(32)))
            return
        }
        if (prefs.workerId.isNotEmpty()) {
            body.addView(text("Logowanie…", 18f), lp(top = dp(16)))
            return
        }
        body.addView(text("Wybierz siebie z listy", 20f, true), lp(top = dp(8)))
        if (AppState.loginError.isNotEmpty()) body.addView(text(AppState.loginError, 15f, color = C.ORANGE), lp(top = dp(6)))
        if (AppState.workers.isEmpty()) {
            body.addView(text(if (AppState.connected) "Brak pracowników na liście. Dodaj ich w panelu serwera." else "Czekam na połączenie z serwerem…", 16f, color = C.MUTED), lp(top = dp(12)))
        }
        for (w in AppState.workers) {
            val b = bigButton(w.name, C.GREEN_BG, C.TEXT) { onPick(w) }
            b.gravity = Gravity.CENTER_VERTICAL or Gravity.START
            body.addView(b, lp(top = dp(8)))
        }
    }

    private fun onPick(w: PublicWorker) {
        if (!w.hasPin) { login(w, ""); return }
        val input = EditText(this).apply {
            // PIN albo hasło z WMS: litery, cyfry i znaki specjalne
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
            hint = "PIN / hasło"; textSize = 22f
        }
        AlertDialog.Builder(this)
            .setTitle(w.name)
            .setView(input)
            .setPositiveButton("Zaloguj") { _, _ -> login(w, input.text.toString()) }
            .setNegativeButton("Anuluj", null)
            .show()
    }

    private fun login(w: PublicWorker, pin: String) {
        prefs.workerId = w.id; prefs.workerPin = pin
        AppState.update { loginError = "" }
        if (!LinkService.send(JSONObject().put("t", "login").put("worker", w.id).put("pin", pin))) {
            Toast.makeText(this, "Brak połączenia – zaloguję po połączeniu", Toast.LENGTH_LONG).show()
        }
        render()
    }
}
