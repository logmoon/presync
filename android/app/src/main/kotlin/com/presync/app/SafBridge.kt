package com.presync.app

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.view.WindowManager
import android.webkit.JavascriptInterface
import android.webkit.WebView
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.documentfile.provider.DocumentFile
import org.json.JSONArray
import org.json.JSONObject
import java.security.MessageDigest
import java.util.Base64

class SafBridge(
    private val context: Context,
    private val webView: WebView,
    private val trustedOrigin: String?,
    private val activity: Activity? = null,
    private val onAutoSyncFinished: ((Boolean, String) -> Unit)? = null
) {

    private val prefs by lazy {
        context.getSharedPreferences("presync_auth", Context.MODE_PRIVATE)
    }

    @Volatile
    private var pendingPushProfile: String? = null

    // The bridge runs on the WebView's JavaBridge thread, where WebView methods
    // must never be called. MainActivity keeps this in sync from the UI thread.
    @Volatile
    private var currentUrl: String? = null

    fun updateCurrentUrl(url: String?) {
        currentUrl = url
    }

    // Only pages loaded from the paired server's origin may use the bridge.
    // Navigation is additionally blocked cross-origin in MainActivity, so this
    // is defense in depth against a malicious or redirected page.
    private fun isTrustedPage(): Boolean {
        val origin = trustedOrigin ?: return false
        val url = currentUrl ?: return false
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

    // Keeps the display awake while a sync runs so WebView timers and network
    // requests are not suspended by the screen timeout.
    @JavascriptInterface
    fun setSyncActive(active: Boolean) {
        if (!isTrustedPage()) return
        // A headless auto-sync has no Activity; the service holds a wake lock
        // for the duration instead.
        val act = activity ?: return
        AutoSyncService.setManualSyncActive(act, active)
        act.runOnUiThread {
            if (active) {
                act.window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            } else {
                act.window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            }
        }
    }

    // --- Destination file access (pull) ---
    //
    // All paths arrive from the trusted web UI as "<profile>/<relative path>"
    // and are resolved strictly inside the user-picked destination tree.
    // Segments are validated so a crafted path can never walk out of it.

    @JavascriptInterface
    fun buildManifest(profileName: String, callback: String) {
        if (!isTrustedPage()) {
            invokeCallback(callback, false, "Untrusted page")
            return
        }
        if (!isSafeSegment(profileName)) {
            invokeCallback(callback, false, "Invalid profile name")
            return
        }
        try {
            val root = getDestinationRoot()
            if (root == null) {
                invokeCallback(callback, true, "{\"files\":[]}")
                return
            }

            val profileDir = root.findFile(profileName)
            val files = JSONArray()
            if (profileDir != null && profileDir.isDirectory) {
                collectManifest(profileDir, "", files)
            }

            val result = JSONObject().apply { put("files", files) }
            invokeCallback(callback, true, result.toString())
        } catch (e: Exception) {
            invokeCallback(callback, false, e.message ?: "Unknown error")
        }
    }

    @JavascriptInterface
    fun hashFile(path: String, callback: String) {
        if (!isTrustedPage()) {
            invokeCallback(callback, false, "Untrusted page")
            return
        }
        try {
            val document = resolvePath(path) ?: run {
                invokeCallback(callback, false, "File not found: $path")
                return
            }
            if (!document.isFile) {
                invokeCallback(callback, false, "Not a file: $path")
                return
            }

            val result = JSONObject().apply { put("hash", hashDocument(document)) }
            invokeCallback(callback, true, result.toString())
        } catch (e: Exception) {
            invokeCallback(callback, false, e.message ?: "Unknown error")
        }
    }

    @JavascriptInterface
    fun tempFileSize(path: String, callback: String) {
        if (!isTrustedPage()) {
            invokeCallback(callback, false, "Untrusted page")
            return
        }
        try {
            val temp = resolvePath("$path.presync-tmp")
            val size = if (temp != null && temp.isFile) temp.length() else 0L
            invokeCallback(callback, true, size.toString())
        } catch (e: Exception) {
            invokeCallback(callback, false, e.message ?: "Unknown error")
        }
    }

    @JavascriptInterface
    fun writeChunk(path: String, base64Data: String, callback: String) {
        if (!isTrustedPage()) {
            invokeCallback(callback, false, "Untrusted page")
            return
        }
        try {
            val tempPath = "$path.presync-tmp"
            val (parent, name) = resolveParent(tempPath, createDirs = true) ?: run {
                invokeCallback(callback, false, "Cannot resolve path: $tempPath")
                return
            }

            var temp = parent.findFile(name)
            if (temp == null) {
                temp = parent.createFile("application/octet-stream", name) ?: run {
                    invokeCallback(callback, false, "Failed to create temp file: $name")
                    return
                }
            }
            if (temp.name != name) {
                // Some providers rewrite display names; appending to the wrong
                // document would corrupt the transfer.
                temp.delete()
                invokeCallback(callback, false, "Provider renamed temp file to: ${temp.name}")
                return
            }

            val bytes = Base64.getDecoder().decode(base64Data)
            context.contentResolver.openOutputStream(temp.uri, "wa")?.use { out ->
                out.write(bytes)
            } ?: run {
                invokeCallback(callback, false, "Failed to open temp file for append")
                return
            }

            invokeCallback(callback, true, "OK")
        } catch (e: Exception) {
            invokeCallback(callback, false, e.message ?: "Unknown error")
        }
    }

    @JavascriptInterface
    fun finishFile(path: String, callback: String) {
        if (!isTrustedPage()) {
            invokeCallback(callback, false, "Untrusted page")
            return
        }
        try {
            val tempPath = "$path.presync-tmp"
            var temp = resolvePath(tempPath)
            if (temp == null) {
                // Zero-byte files never produce a writeChunk call. Materialize
                // the empty staging file so hashing and rename work normally.
                val (parent, name) = resolveParent(tempPath, createDirs = true) ?: run {
                    invokeCallback(callback, false, "Cannot resolve path: $tempPath")
                    return
                }
                temp = parent.createFile("application/octet-stream", name) ?: run {
                    invokeCallback(callback, false, "Failed to create empty temp file")
                    return
                }
                if (temp.name != name) {
                    temp.delete()
                    invokeCallback(callback, false, "Provider renamed temp file to: ${temp.name}")
                    return
                }
            }
            if (!temp.isFile) {
                invokeCallback(callback, false, "Temp path is not a file")
                return
            }

            val result = JSONObject().apply {
                put("hash", hashDocument(temp))
                put("size", temp.length())
            }
            invokeCallback(callback, true, result.toString())
        } catch (e: Exception) {
            invokeCallback(callback, false, e.message ?: "Unknown error")
        }
    }

    @JavascriptInterface
    fun renameFile(path: String, callback: String) {
        if (!isTrustedPage()) {
            invokeCallback(callback, false, "Untrusted page")
            return
        }
        try {
            val (parent, finalName) = resolveParent(path, createDirs = false) ?: run {
                invokeCallback(callback, false, "Cannot resolve path: $path")
                return
            }

            val temp = parent.findFile("$finalName.presync-tmp") ?: run {
                invokeCallback(callback, false, "Temp file not found for $path")
                return
            }

            val existing = parent.findFile(finalName)
            if (existing != null && existing.exists()) {
                if (!existing.delete()) {
                    invokeCallback(callback, false, "Failed to replace existing file: $finalName")
                    return
                }
            }

            if (!temp.renameTo(finalName)) {
                invokeCallback(callback, false, "Failed to rename temp file to: $finalName")
                return
            }

            invokeCallback(callback, true, "OK")
        } catch (e: Exception) {
            invokeCallback(callback, false, e.message ?: "Unknown error")
        }
    }

    @JavascriptInterface
    fun abortFile(path: String, callback: String) {
        if (!isTrustedPage()) {
            invokeCallback(callback, false, "Untrusted page")
            return
        }
        try {
            val temp = resolvePath("$path.presync-tmp")
            if (temp != null && temp.exists()) {
                temp.delete()
            }
            invokeCallback(callback, true, "OK")
        } catch (e: Exception) {
            invokeCallback(callback, false, e.message ?: "Unknown error")
        }
    }

    @JavascriptInterface
    fun deleteFile(path: String, callback: String) {
        if (!isTrustedPage()) {
            invokeCallback(callback, false, "Untrusted page")
            return
        }
        try {
            val document = resolvePath(path)
            if (document == null || !document.exists()) {
                invokeCallback(callback, true, "OK")
                return
            }
            if (!document.delete()) {
                invokeCallback(callback, false, "Failed to delete: $path")
                return
            }
            invokeCallback(callback, true, "OK")
        } catch (e: Exception) {
            invokeCallback(callback, false, e.message ?: "Unknown error")
        }
    }

    private fun collectManifest(dir: DocumentFile, prefix: String, files: JSONArray) {
        for (child in dir.listFiles()) {
            val name = child.name ?: continue
            if (name.isEmpty() || name == "." || name == "..") continue
            if (name.endsWith(".presync-tmp")) continue

            val relative = if (prefix.isEmpty()) name else "$prefix/$name"
            if (child.isDirectory) {
                collectManifest(child, relative, files)
            } else if (child.isFile) {
                files.put(JSONObject().apply {
                    put("path", relative)
                    put("size", child.length())
                    put("mod_time", child.lastModified() / 1000)
                })
            }
        }
    }

    private fun hashDocument(document: DocumentFile): String {
        val digest = MessageDigest.getInstance("SHA-256")
        context.contentResolver.openInputStream(document.uri)?.use { input ->
            val buffer = ByteArray(64 * 1024)
            while (true) {
                val read = input.read(buffer)
                if (read < 0) break
                if (read > 0) digest.update(buffer, 0, read)
            }
        } ?: throw IllegalStateException("Cannot read file: ${document.name}")

        return digest.digest().joinToString("") { "%02x".format(it) }
    }

    private fun getDestinationRoot(): DocumentFile? {
        val uri = getDestinationUri() ?: return null
        return DocumentFile.fromTreeUri(context, uri)
    }

    private fun resolvePath(relativePath: String): DocumentFile? {
        return resolvePathUnder(getDestinationRoot(), relativePath)
    }

    private fun resolvePathUnder(root: DocumentFile?, relativePath: String): DocumentFile? {
        val (parent, name) = resolveParentUnder(root, relativePath, createDirs = false) ?: return null
        return parent.findFile(name)
    }

    private fun resolveParent(relativePath: String, createDirs: Boolean): Pair<DocumentFile, String>? {
        return resolveParentUnder(getDestinationRoot(), relativePath, createDirs)
    }

    private fun resolveParentUnder(
        root: DocumentFile?,
        relativePath: String,
        createDirs: Boolean
    ): Pair<DocumentFile, String>? {
        val segments = validateSegments(relativePath) ?: return null
        var dir = root ?: return null

        for (segment in segments.dropLast(1)) {
            var next = dir.findFile(segment)
            if (next == null) {
                if (!createDirs) return null
                next = dir.createDirectory(segment) ?: return null
            }
            if (!next.isDirectory) return null
            dir = next
        }

        return dir to segments.last()
    }

    private fun validateSegments(path: String): List<String>? {
        if (path.isBlank() || path.startsWith("/") || path.contains("\\")) return null
        val segments = path.split("/")
        if (segments.any { it.isEmpty() || it == "." || it == ".." }) return null
        return segments
    }

    private fun isSafeSegment(value: String): Boolean {
        return value.isNotEmpty() && value != "." && value != ".." &&
            !value.contains("/") && !value.contains("\\")
    }

    // --- Push sources (phone -> server) ---

    @JavascriptInterface
    fun getPushSourceDisplayPath(profileName: String): String {
        if (!isTrustedPage()) return ""
        if (!isSafeSegment(profileName)) return ""
        return try {
            val root = getPushSourceRoot(profileName) ?: return ""
            root.name ?: root.uri.lastPathSegment ?: ""
        } catch (e: Exception) {
            ""
        }
    }

    @JavascriptInterface
    fun pickPushSourceFolder(profileName: String) {
        if (!isTrustedPage()) return
        if (!isSafeSegment(profileName)) return
        pendingPushProfile = profileName
        val act = activity ?: return
        act.runOnUiThread {
            val intent = Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).apply {
                flags = Intent.FLAG_GRANT_READ_URI_PERMISSION or
                        Intent.FLAG_GRANT_WRITE_URI_PERMISSION or
                        Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION
            }
            act.startActivityForResult(intent, PICK_PUSH_SOURCE_REQUEST)
        }
    }

    fun handlePushSourcePicked(uri: Uri?) {
        val profileName = pendingPushProfile
        pendingPushProfile = null
        if (profileName == null || !isSafeSegment(profileName)) {
            invokeCallbackOnWebView("onPushSourcePicked", false, "No profile selected")
            return
        }

        if (uri == null) {
            invokeCallbackOnWebView("onPushSourcePicked", false, "Cancelled")
            return
        }

        try {
            context.contentResolver.takePersistableUriPermission(
                uri,
                Intent.FLAG_GRANT_READ_URI_PERMISSION
            )
        } catch (e: Exception) {
            // Provider does not support persistable grants — valid for this session.
        }
        prefs.edit().putString(pushSourceKey(profileName), uri.toString()).apply()
        invokeCallbackOnWebView("onPushSourcePicked", true, uri.toString())
    }

    @JavascriptInterface
    fun pushBuildManifest(profileName: String, callback: String) {
        if (!isTrustedPage()) {
            invokeCallback(callback, false, "Untrusted page")
            return
        }
        if (!isSafeSegment(profileName)) {
            invokeCallback(callback, false, "Invalid profile name")
            return
        }
        try {
            val root = getPushSourceRoot(profileName) ?: run {
                invokeCallback(callback, false, "No upload folder selected")
                return
            }
            val files = JSONArray()
            collectManifest(root, "", files)
            val result = JSONObject().apply { put("files", files) }
            invokeCallback(callback, true, result.toString())
        } catch (e: Exception) {
            invokeCallback(callback, false, e.message ?: "Unknown error")
        }
    }

    @JavascriptInterface
    fun pushHashFile(profileName: String, relativePath: String, callback: String) {
        if (!isTrustedPage()) {
            invokeCallback(callback, false, "Untrusted page")
            return
        }
        if (!isSafeSegment(profileName)) {
            invokeCallback(callback, false, "Invalid profile name")
            return
        }
        try {
            val document = resolvePathUnder(getPushSourceRoot(profileName), relativePath) ?: run {
                invokeCallback(callback, false, "File not found: $relativePath")
                return
            }
            if (!document.isFile) {
                invokeCallback(callback, false, "Not a file: $relativePath")
                return
            }
            val result = JSONObject().apply { put("hash", hashDocument(document)) }
            invokeCallback(callback, true, result.toString())
        } catch (e: Exception) {
            invokeCallback(callback, false, e.message ?: "Unknown error")
        }
    }

    @JavascriptInterface
    fun pushReadChunk(profileName: String, relativePath: String, offset: Double, length: Int, callback: String) {
        if (!isTrustedPage()) {
            invokeCallback(callback, false, "Untrusted page")
            return
        }
        if (!isSafeSegment(profileName)) {
            invokeCallback(callback, false, "Invalid profile name")
            return
        }
        val start = offset.toLong()
        if (start < 0 || length <= 0 || length > 1024 * 1024) {
            invokeCallback(callback, false, "Invalid chunk request")
            return
        }

        try {
            val document = resolvePathUnder(getPushSourceRoot(profileName), relativePath) ?: run {
                invokeCallback(callback, false, "File not found: $relativePath")
                return
            }
            val input = context.contentResolver.openInputStream(document.uri) ?: run {
                invokeCallback(callback, false, "Cannot read file: $relativePath")
                return
            }

            input.use { stream ->
                var remaining = start
                // Local SAF streams back onto a file descriptor, so skip is a
                // seek. Fall back to reading only if the provider refuses.
                while (remaining > 0) {
                    val skipped = stream.skip(remaining)
                    if (skipped <= 0) break
                    remaining -= skipped
                }
                if (remaining > 0) {
                    val discard = ByteArray(64 * 1024)
                    while (remaining > 0) {
                        val read = stream.read(discard, 0, minOf(remaining, discard.size.toLong()).toInt())
                        if (read < 0) break
                        remaining -= read
                    }
                }
                if (remaining > 0) {
                    invokeCallback(callback, false, "Offset beyond end of file")
                    return
                }

                val buffer = ByteArray(length)
                var total = 0
                while (total < length) {
                    val read = stream.read(buffer, total, length - total)
                    if (read < 0) break
                    total += read
                }

                val data = Base64.getEncoder().encodeToString(buffer.copyOf(total))
                val result = JSONObject().apply {
                    put("data", data)
                    put("eof", total < length)
                }
                invokeCallback(callback, true, result.toString())
            }
        } catch (e: Exception) {
            invokeCallback(callback, false, e.message ?: "Unknown error")
        }
    }

    private fun pushSourceKey(profileName: String): String = "push_source_uri_$profileName"

    private fun getPushSourceRoot(profileName: String): DocumentFile? {
        if (!isSafeSegment(profileName)) return null
        val uriString = prefs.getString(pushSourceKey(profileName), null) ?: return null
        return try {
            DocumentFile.fromTreeUri(context, Uri.parse(uriString))
        } catch (e: Exception) {
            null
        }
    }

    private fun releasePushGrants() {
        for ((key, value) in prefs.all) {
            if (!key.startsWith("push_source_uri_") || value !is String) continue
            try {
                context.contentResolver.releasePersistableUriPermission(
                    Uri.parse(value),
                    Intent.FLAG_GRANT_READ_URI_PERMISSION
                )
            } catch (e: Exception) {
                // Nothing to release — ignore
            }
        }
    }

    @JavascriptInterface
    fun getDestinationDisplayPath(): String {
        if (!isTrustedPage()) return ""
        val uriString = prefs.getString("destination_uri", null) ?: return ""
        return try {
            val uri = Uri.parse(uriString)
            val docFile = DocumentFile.fromTreeUri(context, uri)
            docFile?.name ?: uri.lastPathSegment ?: ""
        } catch (e: Exception) {
            ""
        }
    }

    @JavascriptInterface
    fun clearServerData() {
        if (!isTrustedPage()) return
        AutoSyncService.setEnabled(context, false)
        releaseDestinationGrant()
        releasePushGrants()
        prefs.edit().clear().apply()
        val act = activity ?: return
        act.runOnUiThread {
            val intent = Intent(act, PairingActivity::class.java).apply {
                flags = Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_NEW_TASK
            }
            act.startActivity(intent)
            act.finish()
        }
    }

    @JavascriptInterface
    fun pickDestinationFolder() {
        if (!isTrustedPage()) return
        val act = activity ?: return
        act.runOnUiThread {
            val intent = Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).apply {
                flags = Intent.FLAG_GRANT_READ_URI_PERMISSION or
                        Intent.FLAG_GRANT_WRITE_URI_PERMISSION or
                        Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION
            }
            act.startActivityForResult(intent, PICK_DESTINATION_REQUEST)
        }
    }

    fun handleDestinationPicked(uri: Uri?) {
        if (uri != null) {
            try {
                context.contentResolver.takePersistableUriPermission(
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
            context.contentResolver.releasePersistableUriPermission(
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
        webView.post {
            webView.evaluateJavascript(script, null)
        }
    }

    private fun invokeCallbackOnWebView(functionName: String, success: Boolean, data: String) {
        val json = JSONObject().apply {
            put("success", success)
            put("data", data)
        }
        val script = "javascript:$functionName($json)"
        webView.post {
            webView.evaluateJavascript(script, null)
        }
    }

    // --- Auto-sync ---

    @JavascriptInterface
    fun getAutoSyncEnabled(): Boolean {
        if (!isTrustedPage()) return false
        return AutoSyncService.isEnabled(context)
    }

    @JavascriptInterface
    fun setAutoSyncEnabled(enabled: Boolean) {
        if (!isTrustedPage()) return
        AutoSyncService.setEnabled(context, enabled)
        if (enabled) {
            activity?.let { requestNotificationPermission(it) }
        }
    }

    @JavascriptInterface
    fun isAutoSyncRunning(): Boolean {
        if (!isTrustedPage()) return false
        return AutoSyncService.isSyncing()
    }

    @JavascriptInterface
    fun autoSyncFinished(ok: Boolean, message: String) {
        if (!isTrustedPage()) return
        onAutoSyncFinished?.invoke(ok, message)
    }

    private fun requestNotificationPermission(act: Activity) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return
        if (ContextCompat.checkSelfPermission(act, Manifest.permission.POST_NOTIFICATIONS) ==
            PackageManager.PERMISSION_GRANTED
        ) {
            return
        }
        ActivityCompat.requestPermissions(
            act,
            arrayOf(Manifest.permission.POST_NOTIFICATIONS),
            REQUEST_NOTIFICATIONS
        )
    }

    companion object {
        const val PICK_DESTINATION_REQUEST = 1001
        const val PICK_PUSH_SOURCE_REQUEST = 1002
        const val REQUEST_NOTIFICATIONS = 1003
    }
}
