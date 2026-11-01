package com.presync.app

import android.annotation.SuppressLint
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.Network
import android.net.Uri
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.PowerManager
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import org.json.JSONArray
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL
import java.util.Collections
import java.util.concurrent.ConcurrentHashMap

// Watches the paired server while the app is backgrounded and drives a sync
// through the hidden WebView when the server becomes reachable and the
// cooldown allows. The web UI remains the single implementation of sync.
class AutoSyncService : Service() {

    private val handler = Handler(Looper.getMainLooper())
    private var webView: WebView? = null
    private var wakeLock: PowerManager.WakeLock? = null
    private var networkCallback: ConnectivityManager.NetworkCallback? = null

    @Volatile
    private var destroyed = false

    private val periodicCheck = object : Runnable {
        override fun run() {
            maybeStartSync()
            handler.postDelayed(this, CHECK_INTERVAL_MS)
        }
    }

    private val syncTimeout = Runnable {
        finishAutoSync(false, "Timed out waiting for the sync")
    }

    private data class AutoSyncConfig(val cooldownMinutes: Int, val profiles: List<String>)

    override fun onCreate() {
        super.onCreate()
        startForegroundWithNotification("Watching for server")
        registerNetworkCallback()
        handler.post(periodicCheck)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_DISABLE) {
            prefs(this).edit().putBoolean(KEY_ENABLED, false).apply()
            stopSelf()
            return START_NOT_STICKY
        }

        if (!isEnabled(this)) {
            stopSelf()
            return START_NOT_STICKY
        }

        maybeStartSync()
        return START_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        destroyed = true
        handler.removeCallbacksAndMessages(null)
        networkCallback?.let { callback ->
            try {
                connectivity().unregisterNetworkCallback(callback)
            } catch (e: Exception) {
                // Already unregistered.
            }
        }
        networkCallback = null
        cleanupWebView()
        releaseWakeLock()
        syncing = false
        super.onDestroy()
    }

    // --- Triggering ---

    private fun maybeStartSync() {
        if (destroyed) return
        if (!isEnabled(this)) {
            stopSelf()
            return
        }
        if (syncing || isManualSyncActive() || webView != null) return

        val p = prefs(this)
        val serverUrl = p.getString("server_url", null)
        val token = p.getString("token", null)
        if (serverUrl.isNullOrEmpty() || token.isNullOrEmpty()) {
            updateNotification("Waiting for pairing")
            return
        }
        if (p.getString("destination_uri", null).isNullOrEmpty()) {
            updateNotification("No sync folder selected")
            return
        }

        Thread {
            val cfg = fetchAutoSyncConfig(serverUrl, token)
            handler.post { applyAutoSyncConfig(serverUrl, cfg) }
        }.start()
    }

    // Runs on the main thread, after the eligibility fetch. All trigger
    // conditions are re-checked here because the user may have toggled
    // auto-sync off or started a manual sync while the fetch was in flight.
    private fun applyAutoSyncConfig(serverUrl: String, cfg: AutoSyncConfig?) {
        if (destroyed || !isEnabled(this) || isManualSyncActive() || syncing || webView != null) return

        if (cfg == null) {
            updateNotification("Waiting for server")
            return
        }
        if (cfg.profiles.isEmpty()) {
            updateNotification("No auto-sync profiles configured")
            return
        }

        val cooldownMs = cfg.cooldownMinutes.coerceAtLeast(1) * 60_000L
        val p = prefs(this)
        val last = p.getLong(KEY_LAST_RUN, 0L)
        val now = System.currentTimeMillis()
        if (last > 0 && now - last < cooldownMs) {
            updateNotification("Watching for server")
            return
        }

        p.edit().putLong(KEY_LAST_RUN, now).apply()
        beginHiddenSync(serverUrl)
    }

    @SuppressLint("SetJavaScriptEnabled")
    private fun beginHiddenSync(serverUrl: String) {
        if (destroyed || !isEnabled(this) || isManualSyncActive() || syncing || webView != null) return
        syncing = true
        updateNotification("Syncing…")
        acquireWakeLock()

        val origin = originOf(serverUrl)
        val view = WebView(applicationContext)
        view.settings.javaScriptEnabled = true
        view.settings.domStorageEnabled = true
        view.settings.allowFileAccess = false

        val bridge = SafBridge(applicationContext, view, origin, null) { ok, message ->
            handler.post { finishAutoSync(ok, message) }
        }
        bridge.updateCurrentUrl(serverUrl)

        view.addJavascriptInterface(bridge, "AndroidBridge")
        view.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(
                view: WebView,
                request: WebResourceRequest
            ): Boolean {
                return !request.url.toString().startsWith(origin)
            }

            override fun onReceivedError(
                view: WebView,
                request: WebResourceRequest,
                error: WebResourceError
            ) {
                if (request.isForMainFrame) {
                    finishAutoSync(false, "Failed to load the server UI")
                }
            }

            override fun onRenderProcessGone(
                view: WebView,
                detail: android.webkit.RenderProcessGoneDetail
            ): Boolean {
                finishAutoSync(false, "The sync engine stopped unexpectedly")
                return true
            }
        }

        webView = view
        view.loadUrl("$serverUrl/?autosync=1")
        handler.postDelayed(syncTimeout, SYNC_TIMEOUT_MS)
    }

    private fun finishAutoSync(ok: Boolean, message: String) {
        if (!syncing) return
        cleanupWebView()
        releaseWakeLock()
        syncing = false

        val text = if (ok) {
            "Last sync: $message"
        } else {
            "Auto-sync error: $message"
        }
        updateNotification(text.take(120))
    }

    private fun cleanupWebView() {
        handler.removeCallbacks(syncTimeout)
        val view = webView ?: return
        webView = null
        try {
            view.stopLoading()
            view.removeJavascriptInterface("AndroidBridge")
            view.destroy()
        } catch (e: Exception) {
            // The WebView was already torn down.
        }
    }

    // --- Server query ---

    private fun fetchAutoSyncConfig(serverUrl: String, token: String): AutoSyncConfig? {
        return try {
            val conn = URL("$serverUrl/auto-sync").openConnection() as HttpURLConnection
            conn.connectTimeout = 5000
            conn.readTimeout = 5000
            conn.setRequestProperty("X-Presync-Token", token)
            try {
                if (conn.responseCode != HttpURLConnection.HTTP_OK) {
                    return null
                }
                val body = conn.inputStream.bufferedReader().use { it.readText() }
                val json = JSONObject(body)
                val arr = json.optJSONArray("profiles") ?: JSONArray()
                val profiles = ArrayList<String>()
                for (i in 0 until arr.length()) {
                    val name = arr.optString(i, "")
                    if (name.isNotEmpty()) profiles.add(name)
                }
                AutoSyncConfig(json.optInt("cooldown_minutes", 15), profiles)
            } finally {
                conn.disconnect()
            }
        } catch (e: Exception) {
            null
        }
    }

    // --- Connectivity / wake lock ---

    private fun connectivity(): ConnectivityManager =
        getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager

    private fun registerNetworkCallback() {
        val callback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                handler.post { maybeStartSync() }
            }
        }
        try {
            connectivity().registerDefaultNetworkCallback(callback)
            networkCallback = callback
        } catch (e: Exception) {
            // Older/broken providers: the periodic check still covers us.
        }
    }

    private fun acquireWakeLock() {
        if (wakeLock?.isHeld == true) return
        val power = getSystemService(Context.POWER_SERVICE) as PowerManager
        wakeLock = power.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "presync:auto-sync").apply {
            setReferenceCounted(false)
            acquire(SYNC_TIMEOUT_MS)
        }
    }

    private fun releaseWakeLock() {
        try {
            if (wakeLock?.isHeld == true) {
                wakeLock?.release()
            }
        } catch (e: Exception) {
            // Already released.
        }
        wakeLock = null
    }

    // --- Notification ---

    private fun startForegroundWithNotification(text: String) {
        val notification = buildNotification(text)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            startForeground(NOTIFICATION_ID, notification)
        }
    }

    private fun updateNotification(text: String) {
        startForegroundWithNotification(text)
    }

    private fun buildNotification(text: String): Notification {
        ensureChannel()

        val openApp = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        val disable = PendingIntent.getService(
            this,
            1,
            Intent(this, AutoSyncService::class.java).setAction(ACTION_DISABLE),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setContentTitle("presync auto-sync")
            .setContentText(text)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .setContentIntent(openApp)
            .addAction(0, "Turn off auto-sync", disable)
            .build()
    }

    private fun ensureChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val manager = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        if (manager.getNotificationChannel(CHANNEL_ID) == null) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "Auto-sync",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "presync background auto-sync status"
            }
            manager.createNotificationChannel(channel)
        }
    }

    private fun originOf(url: String): String {
        return try {
            val u = Uri.parse(url)
            "${u.scheme}://${u.host}" + if (u.port > 0) ":${u.port}" else ""
        } catch (e: Exception) {
            url
        }
    }

    companion object {
        private const val CHANNEL_ID = "presync_auto_sync"
        private const val NOTIFICATION_ID = 7701
        private const val PREFS = "presync_auth"
        private const val KEY_ENABLED = "auto_sync_enabled"
        private const val KEY_LAST_RUN = "last_auto_sync_at"
        private const val ACTION_DISABLE = "com.presync.app.DISABLE_AUTO_SYNC"
        private const val CHECK_INTERVAL_MS = 60_000L
        private const val SYNC_TIMEOUT_MS = 60 * 60 * 1000L

        @Volatile
        private var syncing = false

        private val manualSyncOwners = Collections.newSetFromMap(
            ConcurrentHashMap<Any, Boolean>()
        )

        fun isEnabled(context: Context): Boolean =
            prefs(context).getBoolean(KEY_ENABLED, false)

        fun isSyncing(): Boolean = syncing

        fun setManualSyncActive(owner: Any, active: Boolean) {
            if (active) {
                manualSyncOwners.add(owner)
            } else {
                manualSyncOwners.remove(owner)
            }
        }

        fun clearManualSyncOwner(owner: Any) {
            manualSyncOwners.remove(owner)
        }

        private fun isManualSyncActive(): Boolean = manualSyncOwners.isNotEmpty()

        fun setEnabled(context: Context, enabled: Boolean) {
            prefs(context).edit().putBoolean(KEY_ENABLED, enabled).apply()
            if (enabled) {
                startIfEnabled(context)
            } else {
                context.stopService(Intent(context, AutoSyncService::class.java))
            }
        }

        fun startIfEnabled(context: Context) {
            if (!isEnabled(context)) return
            ContextCompat.startForegroundService(
                context,
                Intent(context, AutoSyncService::class.java)
            )
        }

        private fun prefs(context: Context) =
            context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
    }
}
