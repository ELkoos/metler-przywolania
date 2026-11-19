package pl.metler.przywolania

import android.app.Activity
import android.content.Intent
import android.os.Bundle

class StartActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val prefs = Prefs(this)
        if (!prefs.configured) {
            startActivity(Intent(this, SettingsActivity::class.java))
        } else {
            LinkService.start(this)
            val target = if (prefs.mode == Prefs.MODE_TABLET) TabletActivity::class.java else TerminalActivity::class.java
            startActivity(Intent(this, target))
        }
        finish()
    }
}
