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
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import java.net.HttpURLConnection
import java.net.URL

class MainActivity : AppCompatActivity() {

    private lateinit var webView: WebView
    private lateinit var safBridge: SafBridge

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val pairingUrl = intent?.getStringExtra("pairing_url")
        val serverUrl = getServerUrl()

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
                    clearServerData()
                    startActivity(Intent(this@MainActivity, PairingActivity::class.java).apply {
                        flags = Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_NEW_TASK
                    })
                    finish()
                    return@runOnUiThread
                }
                setupWebView(serverUrl)
            }
        }.start()
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

        safBridge = SafBridge(this, webView)

        webView.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            allowFileAccess = true
            mixedContentMode = WebSettings.MIXED_CONTENT_ALWAYS_ALLOW
        }

        webView.addJavascriptInterface(safBridge, "AndroidBridge")

        webView.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView, request: android.webkit.WebResourceRequest): Boolean {
                return false
            }

            override fun onReceivedError(
                view: WebView,
                request: android.webkit.WebResourceRequest,
                error: android.webkit.WebResourceError
            ) {
                if (request.isForMainFrame) {
                    clearServerData()
                    startActivity(Intent(this@MainActivity, PairingActivity::class.java).apply {
                        flags = Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_NEW_TASK
                    })
                    finish()
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
}
