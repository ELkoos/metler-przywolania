package pl.metler.przywolania

import android.util.Base64
import okhttp3.OkHttpClient
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.X509TrustManager

/**
 * Połączenie z serwerem. Gdy podany jest pin ("sha256/<base64 SHA-256 klucza publicznego>"),
 * aplikacja ufa wyłącznie certyfikatowi z tym kluczem (certyfikat własny serwera, bez urzędu certyfikacji
 * i bez instalowania czegokolwiek na urządzeniu). Bez pinu: zwykła weryfikacja systemowa.
 */
object Tls {
    fun client(pin: String): OkHttpClient {
        val b = OkHttpClient.Builder()
            .pingInterval(20, TimeUnit.SECONDS)
            .connectTimeout(8, TimeUnit.SECONDS)
            .readTimeout(0, TimeUnit.MILLISECONDS)
        val want = pin.trim().removePrefix("sha256/")
        if (want.isNotEmpty()) {
            val tm = PinnedTrustManager(want)
            val ctx = SSLContext.getInstance("TLS")
            ctx.init(null, arrayOf(tm), SecureRandom())
            b.sslSocketFactory(ctx.socketFactory, tm)
            // tożsamość serwera potwierdza przypięty klucz, nie nazwa w certyfikacie
            b.hostnameVerifier { _, _ -> true }
        }
        return b.build()
    }

    fun pinOf(cert: X509Certificate): String =
        Base64.encodeToString(MessageDigest.getInstance("SHA-256").digest(cert.publicKey.encoded), Base64.NO_WRAP)

    private class PinnedTrustManager(private val pin: String) : X509TrustManager {
        override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {
            val leaf = chain?.firstOrNull() ?: throw CertificateException("Brak certyfikatu serwera")
            leaf.checkValidity()
            if (pinOf(leaf) != pin) throw CertificateException("Certyfikat serwera nie pasuje do przypiętego")
        }
        override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) =
            throw CertificateException("Nieobsługiwane")
        override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
    }
}
