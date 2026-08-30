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
    fun writeFile(path: String, base64Data: String, callback: String) {
        if (!isTrustedPage()) {
            invokeCallback(callback, false, "Untrusted page")
            return
        }
        try {
            val treeUri = getDestinationUri() ?: run {
                invokeCallback(callback, false, "No destination folder selected")
                return
            }

            val documentFile = DocumentFile.fromTreeUri(activity, treeUri)
                ?: run {
                    invokeCallback(callback, false, "Cannot access destination folder")
                    return
                }

            val parts = path.split("/")
            var currentDir = documentFile

            for (i in 0 until parts.size - 1) {
                val dirName = parts[i]
                currentDir = currentDir.findFile(dirName)
                    ?: currentDir.createDirectory(dirName)
                    ?: run {
                        invokeCallback(callback, false, "Failed to create directory: $dirName")
                        return
                    }
            }

            val fileName = parts.last()
            val data = Base64.getDecoder().decode(base64Data)

            val existingFile = currentDir.findFile(fileName)
            if (existingFile != null && existingFile.isFile) {
                existingFile.delete()
            }

            val targetFile = currentDir.createFile("application/octet-stream", fileName)
                ?: run {
                    invokeCallback(callback, false, "Failed to create file: $fileName")
                    return
                }

            activity.contentResolver.openOutputStream(targetFile.uri)?.use { out ->
                out.write(data)
            } ?: run {
                invokeCallback(callback, false, "Failed to write file: $fileName")
                return
            }

            invokeCallback(callback, true, "OK")
        } catch (e: Exception) {
            invokeCallback(callback, false, e.message ?: "Unknown error")
        }
    }

    @JavascriptInterface
    fun listDestination(@Suppress("UNUSED_PARAMETER") profileName: String, callback: String) {
        if (!isTrustedPage()) {
            invokeCallback(callback, false, "Untrusted page")
            return
        }
        try {
            val treeUri = getDestinationUri()
            if (treeUri == null) {
                invokeCallback(callback, false, "No destination folder selected")
                return
            }

            val documentFile = DocumentFile.fromTreeUri(activity, treeUri)
            if (documentFile == null || !documentFile.exists()) {
                invokeCallback(callback, false, "Destination folder not accessible")
                return
            }

            val files = JSONArray()
            for (file in documentFile.listFiles()) {
                if (file.isFile) {
                    files.put(file.name ?: "unknown")
                }
            }

            val result = JSONObject().apply {
                put("files", files)
            }
            invokeCallback(callback, true, result.toString())
        } catch (e: Exception) {
            invokeCallback(callback, false, e.message ?: "Unknown error")
        }
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
            activity.contentResolver.takePersistableUriPermission(
                uri,
                Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION
            )
            prefs.edit().putString("destination_uri", uri.toString()).apply()
            invokeCallbackOnWebView("onDestinationPicked", true, uri.toString())
        } else {
            invokeCallbackOnWebView("onDestinationPicked", false, "Cancelled")
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
