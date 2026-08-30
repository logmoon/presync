package com.presync.app

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.webkit.JavascriptInterface
import android.webkit.WebView
import androidx.documentfile.provider.DocumentFile
import org.json.JSONArray
import org.json.JSONObject
import java.security.MessageDigest
import java.util.Base64

class SafBridge(
    private val activity: Activity,
    private val webView: WebView,
    private val trustedOrigin: String?
) {

    private val prefs by lazy {
        activity.getSharedPreferences("presync_auth", Context.MODE_PRIVATE)
    }

    // Only pages loaded from the paired server's origin may use the bridge.
    // Navigation is additionally blocked cross-origin in MainActivity, so this
    // is defense in depth against a malicious or redirected page.
    private fun isTrustedPage(): Boolean {
        val origin = trustedOrigin ?: return false
        val url = webView.url ?: return false
        return url.startsWith(origin)
    }

    @JavascriptInterface
    fun saveServerUrl(url: String) {
        if (!isTrustedPage()) return
        (activity as? MainActivity)?.saveServerUrl(url)
    }

    @JavascriptInterface
    fun saveToken(token: String) {
        if (!isTrustedPage()) return
        prefs.edit().putString("token", token).apply()
    }

    @JavascriptInterface
    fun getToken(): String {
        if (!isTrustedPage()) return ""
        return prefs.getString("token", "") ?: ""
    }

    @JavascriptInterface
    fun saveDeviceId(deviceId: String) {
        if (!isTrustedPage()) return
        prefs.edit().putString("device_id", deviceId).apply()
    }

    @JavascriptInterface
    fun getDeviceId(): String {
        if (!isTrustedPage()) return ""
        return prefs.getString("device_id", "") ?: ""
    }

    @JavascriptInterface
    fun getDestinationDisplayPath(): String {
        if (!isTrustedPage()) return ""
        val uriString = prefs.getString("destination_uri", null) ?: return ""
        return try {
            val uri = Uri.parse(uriString)
            val docFile = DocumentFile.fromTreeUri(activity, uri)
            docFile?.name ?: uri.lastPathSegment ?: ""
        } catch (e: Exception) {
            ""
        }
    }

    @JavascriptInterface
    fun clearServerData() {
        if (!isTrustedPage()) return
        releaseDestinationGrant()
        prefs.edit().clear().apply()
        activity.runOnUiThread {
            val intent = Intent(activity, PairingActivity::class.java).apply {
                flags = Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_NEW_TASK
            }
            activity.startActivity(intent)
            activity.finish()
        }
    }

    @JavascriptInterface
    fun pickDestinationFolder() {
        if (!isTrustedPage()) return
        activity.runOnUiThread {
            val intent = Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).apply {
                flags = Intent.FLAG_GRANT_READ_URI_PERMISSION or
                        Intent.FLAG_GRANT_WRITE_URI_PERMISSION or
                        Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION
            }
            activity.startActivityForResult(intent, PICK_DESTINATION_REQUEST)
        }
    }

    fun handleDestinationPicked(uri: Uri?) {
        if (uri != null) {
            try {
                activity.contentResolver.takePersistableUriPermission(
                    uri,
                    Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION
                )
            } catch (e: Exception) {
                // Provider does not support persistable grants — the grant is
                // still valid for this session; do not crash the app.
            }
            prefs.edit().putString("destination_uri", uri.toString()).apply()
            invokeCallbackOnWebView("onDestinationPicked", true, uri.toString())
        } else {
            invokeCallbackOnWebView("onDestinationPicked", false, "Cancelled")
        }
    }

    private fun releaseDestinationGrant() {
        val uriString = prefs.getString("destination_uri", null) ?: return
        try {
            activity.contentResolver.releasePersistableUriPermission(
                Uri.parse(uriString),
                Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION
            )
        } catch (e: Exception) {
            // Nothing to release — ignore
        }
    }

    private fun getDestinationUri(): Uri? {
        val uriString = prefs.getString("destination_uri", null) ?: return null
        return Uri.parse(uriString)
    }

    private fun invokeCallback(callback: String, success: Boolean, data: String) {
        val json = JSONObject().apply {
            put("success", success)
            put("data", data)
        }
        val script = "javascript:$callback($json)"
        activity.runOnUiThread {
            webView.evaluateJavascript(script, null)
        }
    }

    private fun invokeCallbackOnWebView(functionName: String, success: Boolean, data: String) {
        val json = JSONObject().apply {
            put("success", success)
            put("data", data)
        }
        val script = "javascript:$functionName($json)"
        webView.evaluateJavascript(script, null)
    }

    companion object {
        const val PICK_DESTINATION_REQUEST = 1001
    }
}
