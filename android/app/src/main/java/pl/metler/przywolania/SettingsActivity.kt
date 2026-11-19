package pl.metler.przywolania

import android.app.Activity
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.PowerManager
import android.provider.Settings
import android.text.InputType
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.RadioButton
import android.widget.RadioGroup
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast

class SettingsActivity : Activity() {
    private lateinit var prefs: Prefs
    private lateinit var permBox: LinearLayout
    private lateinit var group: RadioGroup

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = Prefs(this)
        val root = vbox(20)
        root.addView(text("Ustawienia urządzenia", 22f, true))
        root.addView(text("Wersja ${packageManager.getPackageInfo(packageName, 0).versionName} · ID urządzenia ${prefs.deviceId}", 13f, color = C.MUTED))

        root.addView(text("Tryb pracy", 16f, true), lp(top = dp(20)))
        group = RadioGroup(this)
        val rTablet = RadioButton(this).apply { text = "Tablet na stanowisku pakowania (wzywa)"; id = ID_TABLET; textSize = 16f }
        val rTerm = RadioButton(this).apply { text = "Terminal pracownika (otrzymuje wezwania)"; id = ID_TERMINAL; textSize = 16f }
        group.addView(rTablet); group.addView(rTerm)
        group.check(if (prefs.mode == Prefs.MODE_TABLET) ID_TABLET else if (prefs.mode == Prefs.MODE_TERMINAL) ID_TERMINAL else -1)
        root.addView(group)

        fun field(label: String, value: String, hint: String, type: Int = InputType.TYPE_CLASS_TEXT): EditText {
            root.addView(text(label, 16f, true), lp(top = dp(16)))
            val e = EditText(this).apply { setText(value); this.hint = hint; inputType = type; textSize = 18f; isSingleLine = true }
            root.addView(e)
            return e
        }
        val server = field("Adres serwera", prefs.server, "np. 192.168.1.20:8080", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI)
        val key = field("Klucz urządzenia", prefs.key, "z panelu serwera (/admin)")
        val stationLabel = text("Nazwa stołu (jak w EasyStorage, np. KONTR_12)", 16f, true)
        val station = EditText(this).apply { setText(prefs.station); hint = "np. KONTR_12"; textSize = 18f; isSingleLine = true
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_CAP_CHARACTERS }
        root.addView(stationLabel, lp(top = dp(16))); root.addView(station)
        fun refreshStation() { val t = group.checkedRadioButtonId == ID_TABLET; stationLabel.gone(!t); station.gone(!t) }
        group.setOnCheckedChangeListener { _, _ -> refreshStation(); refreshPerms() }
        refreshStation()

        permBox = vbox(0)
        root.addView(permBox, lp(top = dp(16)))

        root.addView(bigButton("Zapisz i uruchom", C.ORANGE) {
            val mode = when (group.checkedRadioButtonId) { ID_TABLET -> Prefs.MODE_TABLET; ID_TERMINAL -> Prefs.MODE_TERMINAL; else -> "" }
            if (mode.isEmpty() || server.text.isBlank() || key.text.isBlank() || (mode == Prefs.MODE_TABLET && station.text.isBlank())) {
                Toast.makeText(this, "Uzupełnij wszystkie pola", Toast.LENGTH_LONG).show()
                return@bigButton
            }
            val modeChanged = mode != prefs.mode
            if (prefs.mode == Prefs.MODE_TERMINAL && prefs.workerId.isNotEmpty() && (modeChanged || server.text.toString().trim() != prefs.server)) {
                LinkService.send(org.json.JSONObject().put("t", "logout"))
            }
            prefs.mode = mode
            prefs.server = server.text.toString().trim()
            prefs.key = key.text.toString().trim()
            prefs.station = station.text.toString().trim().uppercase().replace(" ", "_")
            if (modeChanged) { prefs.workerId = ""; prefs.workerPin = "" }
            LinkService.stop(this)
            AppState.update { connected = false; loggedInName = ""; tabletWorkers = emptyList(); workers = emptyList(); activeCall = null }
            startActivity(Intent(this, StartActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK))
            finish()
        }, lp(top = dp(24)))

        setContentView(ScrollView(this).apply { addView(root) })
    }

    override fun onResume() { super.onResume(); refreshPerms() }

    private fun refreshPerms() {
        permBox.removeAllViews()
        permBox.addView(text("Uprawnienia", 16f, true))
        fun row(label: String, ok: Boolean, action: () -> Unit) {
            val t = TextView(this).apply {
                text = (if (ok) "✓ " else "✗ ") + label + if (ok) "" else "  – dotknij, aby włączyć"
                textSize = 16f; setTextColor(if (ok) C.GREEN else C.ORANGE)
                setPadding(0, dp(8), 0, dp(8))
                if (!ok) setOnClickListener { action() }
            }
            permBox.addView(t)
        }
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        if (Build.VERSION.SDK_INT >= 33) {
            row("Powiadomienia", nm.areNotificationsEnabled()) {
                requestPermissions(arrayOf(android.Manifest.permission.POST_NOTIFICATIONS), 1)
            }
        }
        val pm = getSystemService(Context.POWER_SERVICE) as PowerManager
        row("Praca w tle bez ograniczeń baterii", pm.isIgnoringBatteryOptimizations(packageName)) {
            startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName")))
        }
        val terminal = group.checkedRadioButtonId == ID_TERMINAL
        if (terminal) {
            row("Wyświetlanie nad innymi aplikacjami (ekran wezwania nad EasyStorage)", Settings.canDrawOverlays(this)) {
                startActivity(Intent(Settings.ACTION_MANAGE_OVERLAY_PERMISSION, Uri.parse("package:$packageName")))
            }
            if (Build.VERSION.SDK_INT >= 34) {
                row("Powiadomienia pełnoekranowe", nm.canUseFullScreenIntent()) {
                    startActivity(Intent(Settings.ACTION_MANAGE_APP_USE_FULL_SCREEN_INTENT, Uri.parse("package:$packageName")))
                }
            }
        }
    }

    companion object {
        private val ID_TABLET = android.view.View.generateViewId()
        private val ID_TERMINAL = android.view.View.generateViewId()
    }
}
