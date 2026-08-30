package com.presync.app

import android.annotation.SuppressLint
import android.content.Intent
import android.graphics.Color
import android.net.Uri
import android.os.Bundle
import android.view.Gravity
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import java.net.HttpURLConnection
import java.net.URL

class MainActivity : AppCompatActivity() {

    private lateinit var webView: WebView
    private lateinit var safBridge: SafBridge
    private var trustedOrigin: String? = null

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val pairingUrl = intent?.getStringExtra("pairing_url")
        val serverUrl = getServerUrl()

        if (pairingUrl != null && !isValidPairingUrl(pairingUrl)) {
            startActivity(Intent(this, PairingActivity::class.java))
            finish()
            return
        }

        if (pairingUrl == null && serverUrl == null) {
            startActivity(Intent(this, PairingActivity::class.java))
            finish()
            return
        }

        if (pairingUrl != null) {
            setupWebView(pairingUrl)
            return
        }

        showConnectingScreen()
        Thread {
            val reachable = checkServerHealth(serverUrl!!)
            runOnUiThread {
                if (!reachable) {
                    showRetryScreen(serverUrl)
                    return@runOnUiThread
                }
                setupWebView(serverUrl)
            }
        }.start()
    }

    // A transient outage (server restarting, WiFi blip) must never wipe the
    // pairing data or the SAF folder grant — only the user can do that.
    private fun showRetryScreen(serverUrl: String) {
        val container = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(32), dp(32), dp(32), dp(32))
            setBackgroundColor(Color.parseColor("#1A1918"))
            gravity = Gravity.CENTER
        }

        container.addView(TextView(this).apply {
            text = "Cannot reach the server"
            setTextColor(Color.parseColor("#F2F0EE"))
            textSize = 18f
            gravity = Gravity.CENTER
        })
        container.addView(TextView(this).apply {
            text = "Check that the server is running and you are on the same network."
            setTextColor(Color.parseColor("#7A756F"))
            textSize = 14f
            gravity = Gravity.CENTER
            setPadding(0, dp(8), 0, 0)
        })

        val retryBtn = Button(this).apply {
            text = "Retry"
            setTextColor(Color.parseColor("#1A1918"))
            setBackgroundColor(Color.parseColor("#D97757"))
            setPadding(dp(24), dp(10), dp(24), dp(10))
            setOnClickListener {
                showConnectingScreen()
                Thread {
                    val reachable = checkServerHealth(serverUrl)
                    runOnUiThread {
                        if (reachable) {
                            setupWebView(serverUrl)
                        } else {
                            showRetryScreen(serverUrl)
                        }
                    }
                }.start()
            }
        }
        container.addView(retryBtn, LinearLayout.LayoutParams(
            LinearLayout.LayoutParams.WRAP_CONTENT,
            LinearLayout.LayoutParams.WRAP_CONTENT
        ).apply { topMargin = dp(24) })

        val forgetBtn = Button(this).apply {
            text = "Forget this server"
            setTextColor(Color.parseColor("#F2F0EE"))
            setBackgroundColor(Color.parseColor("#2A2928"))
            setPadding(dp(24), dp(10), dp(24), dp(10))
            setOnClickListener {
                clearServerData()
            }
        }
        container.addView(forgetBtn, LinearLayout.LayoutParams(
            LinearLayout.LayoutParams.WRAP_CONTENT,
            LinearLayout.LayoutParams.WRAP_CONTENT
        ).apply { topMargin = dp(8) })

        setContentView(container)
    }

    private fun dp(value: Int): Int {
        return (value * resources.displayMetrics.density).toInt()
    }

    private fun showConnectingScreen() {
        setContentView(TextView(this).apply {
            text = "Connecting to server…"
            setTextColor(Color.parseColor("#F2F0EE"))
            textSize = 16f
            gravity = Gravity.CENTER
            setBackgroundColor(Color.parseColor("#1A1918"))
        })
    }

    private fun checkServerHealth(serverUrl: String): Boolean {
        return try {
            val url = URL("$serverUrl/health")
            val conn = url.openConnection() as HttpURLConnection
            conn.connectTimeout = 3000
            conn.readTimeout = 3000
            conn.requestMethod = "GET"
            val code = conn.responseCode
            conn.disconnect()
            code == 200
        } catch (e: Exception) {
            false
        }
    }

    @SuppressLint("SetJavaScriptEnabled")
    private fun setupWebView(url: String) {
        webView = WebView(this).also { setContentView(it) }

        trustedOrigin = originOf(url)
        safBridge = SafBridge(this, webView, trustedOrigin)

        webView.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            allowFileAccess = false
            mixedContentMode = WebSettings.MIXED_CONTENT_ALWAYS_ALLOW
        }

        webView.addJavascriptInterface(safBridge, "AndroidBridge")

        webView.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView, request: android.webkit.WebResourceRequest): Boolean {
                val origin = trustedOrigin
                if (origin != null && !request.url.toString().startsWith(origin)) {
                    return true
                }
                return false
            }

            override fun onReceivedError(
                view: WebView,
                request: android.webkit.WebResourceRequest,
                error: android.webkit.WebResourceError
            ) {
                if (request.isForMainFrame) {
                    // Transient load error — do NOT wipe pairing data. Show the
                    // retry screen instead.
                    val url = getServerUrl()
                    if (url != null) {
                        showRetryScreen(url)
                    }
                }
            }
        }

        webView.webChromeClient = WebChromeClient()
        webView.loadUrl(url)
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        if (::webView.isInitialized && webView.canGoBack()) {
            webView.goBack()
        } else {
            super.onBackPressed()
        }
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == SafBridge.PICK_DESTINATION_REQUEST) {
            val uri: Uri? = data?.data
            safBridge.handleDestinationPicked(uri)
        }
    }

    private fun getServerUrl(): String? {
        val prefs = getSharedPreferences("presync_auth", MODE_PRIVATE)
        return prefs.getString("server_url", null)
    }

    fun saveServerUrl(url: String) {
        getSharedPreferences("presync_auth", MODE_PRIVATE)
            .edit()
            .putString("server_url", url)
            .apply()
    }

    private fun clearServerData() {
        getSharedPreferences("presync_auth", MODE_PRIVATE)
            .edit()
            .clear()
            .apply()
    }

    private fun originOf(url: String): String {
        return try {
            val u = Uri.parse(url)
            "${u.scheme}://${u.host}" + if (u.port > 0) ":${u.port}" else ""
        } catch (e: Exception) {
            url
        }
    }

    private fun isValidPairingUrl(url: String): Boolean {
        return try {
            val u = Uri.parse(url)
            (u.scheme == "http" || u.scheme == "https") &&
                !u.host.isNullOrEmpty() &&
                !u.getQueryParameter("token").isNullOrEmpty()
        } catch (e: Exception) {
            false
        }
    }
}
