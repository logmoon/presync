package com.presync.app

import android.Manifest
import android.annotation.SuppressLint
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Bundle
import android.util.Log
import android.util.Size
import android.view.Gravity
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.FrameLayout
import androidx.appcompat.app.AppCompatActivity
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.Preview
import androidx.camera.core.resolutionselector.ResolutionSelector
import androidx.camera.core.resolutionselector.ResolutionStrategy
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import com.google.mlkit.vision.barcode.BarcodeScannerOptions
import com.google.mlkit.vision.barcode.BarcodeScanning
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.common.InputImage
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

class PairingActivity : AppCompatActivity() {

    private lateinit var cameraExecutor: ExecutorService
    private val pairingLaunched = AtomicBoolean(false)
    private val barcodeScanner by lazy {
        BarcodeScanning.getClient(
            BarcodeScannerOptions.Builder()
                .setBarcodeFormats(Barcode.FORMAT_QR_CODE)
                .build()
        )
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        cameraExecutor = Executors.newSingleThreadExecutor()
        showEntryScreen()
    }

    private fun showEntryScreen() {

        val container = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(24), dp(24), dp(24), dp(24))
            setBackgroundColor(0xFF1A1918.toInt())
            gravity = Gravity.CENTER_HORIZONTAL
        }

        val title = TextView(this).apply {
            text = "Pair with Server"
            textSize = 24f
            setTextColor(0xFFF2F0EE.toInt())
            gravity = Gravity.CENTER
            setPadding(0, 0, 0, dp(16))
        }
        container.addView(title)

        val subtitle = TextView(this).apply {
            text = "Enter the server address from the QR code"
            textSize = 14f
            setTextColor(0xFFB5B0AB.toInt())
            gravity = Gravity.CENTER
            setPadding(0, 0, 0, dp(24))
        }
        container.addView(subtitle)

        val urlInput = EditText(this).apply {
            hint = "http://192.168.1.x:7700/?token=..."
            setTextColor(0xFFF2F0EE.toInt())
            setHintTextColor(0xFF7A756F.toInt())
            setBackgroundColor(0xFF2A2928.toInt())
            setPadding(dp(12), dp(10), dp(12), dp(10))
            textSize = 14f
            isSingleLine = true
        }
        container.addView(urlInput)

        val connectBtn = Button(this).apply {
            text = "Connect"
            setTextColor(0xFF1A1918.toInt())
            setBackgroundColor(0xFFD97757.toInt())
            setPadding(dp(16), dp(10), dp(16), dp(10))
            textSize = 14f
            setOnClickListener {
                val url = urlInput.text.toString().trim()
                if (isValidPairingUrl(url)) {
                    proceedWithUrl(url)
                } else {
                    urlInput.error = "Enter a valid http://server:port/?token=... URL"
                }
            }
        }
        container.addView(connectBtn, LinearLayout.LayoutParams(
            LinearLayout.LayoutParams.MATCH_PARENT,
            LinearLayout.LayoutParams.WRAP_CONTENT
        ).apply { topMargin = dp(12) })

        val divider = TextView(this).apply {
            text = "or"
            textSize = 14f
            setTextColor(0xFF7A756F.toInt())
            gravity = Gravity.CENTER
            setPadding(0, dp(24), 0, dp(24))
        }
        container.addView(divider)

        val scanBtn = Button(this).apply {
            text = "Scan QR Code"
            setTextColor(0xFFF2F0EE.toInt())
            setBackgroundColor(0xFF2A2928.toInt())
            setPadding(dp(16), dp(10), dp(16), dp(10))
            textSize = 14f
            setOnClickListener {
                if (ContextCompat.checkSelfPermission(this@PairingActivity, Manifest.permission.CAMERA)
                    == PackageManager.PERMISSION_GRANTED) {
                    showScanScreen()
                } else {
                    ActivityCompat.requestPermissions(
                        this@PairingActivity,
                        arrayOf(Manifest.permission.CAMERA),
                        CAMERA_REQUEST
                    )
                }
            }
        }
        container.addView(scanBtn, LinearLayout.LayoutParams(
            LinearLayout.LayoutParams.MATCH_PARENT,
            LinearLayout.LayoutParams.WRAP_CONTENT
        ))

        setContentView(container)
    }

    @SuppressLint("UnsafeOptInUsageError")
    private fun showScanScreen() {

        val root = FrameLayout(this).apply {
            setBackgroundColor(0xFF1A1918.toInt())
        }

        val previewView = PreviewView(this).apply {
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.MATCH_PARENT
            )
        }
        root.addView(previewView)

        val overlay = TextView(this).apply {
            text = "Point camera at QR code"
            textSize = 14f
            setTextColor(0xFFF2F0EE.toInt())
            gravity = Gravity.CENTER
            setBackgroundColor(0x801A1918.toInt())
            setPadding(dp(16), dp(8), dp(16), dp(8))
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.WRAP_CONTENT
            ).apply {
                gravity = Gravity.BOTTOM or Gravity.CENTER_HORIZONTAL
                bottomMargin = dp(48)
            }
        }
        root.addView(overlay)

        val backBtn = Button(this).apply {
            text = "Back"
            setTextColor(0xFFF2F0EE.toInt())
            setBackgroundColor(0x802A2928.toInt())
            setPadding(dp(12), dp(6), dp(12), dp(6))
            textSize = 12f
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT,
                FrameLayout.LayoutParams.WRAP_CONTENT
            ).apply {
                gravity = Gravity.TOP or Gravity.START
                marginStart = dp(16)
                topMargin = dp(16)
            }
            setOnClickListener { showEntryScreen() }
        }
        root.addView(backBtn)

        setContentView(root)

        val cameraProviderFuture = ProcessCameraProvider.getInstance(this)
        cameraProviderFuture.addListener({
            val cameraProvider = cameraProviderFuture.get()

            val preview = Preview.Builder().build().also {
                it.setSurfaceProvider(previewView.surfaceProvider)
            }

            val imageAnalysis = ImageAnalysis.Builder()
                .setResolutionSelector(
                    ResolutionSelector.Builder()
                        .setResolutionStrategy(ResolutionStrategy(Size(1280, 720), ResolutionStrategy.FALLBACK_RULE_CLOSEST_HIGHER_THEN_LOWER))
                        .build()
                )
                .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                .build()
                .also { analysis ->
                    analysis.setAnalyzer(cameraExecutor) { imageProxy ->
                        val mediaImage = imageProxy.image
                        if (mediaImage != null) {
                            val image = InputImage.fromMediaImage(
                                mediaImage,
                                imageProxy.imageInfo.rotationDegrees
                            )
                            barcodeScanner.process(image)
                                .addOnSuccessListener { barcodes ->
                                    for (barcode in barcodes) {
                                        val raw = barcode.rawValue ?: continue
                                        if (isValidPairingUrl(raw)) {
                                            if (pairingLaunched.compareAndSet(false, true)) {
                                                runOnUiThread { proceedWithUrl(raw) }
                                            }
                                            return@addOnSuccessListener
                                        }
                                    }
                                }
                                .addOnCompleteListener {
                                    imageProxy.close()
                                }
                        } else {
                            imageProxy.close()
                        }
                    }
                }

            try {
                cameraProvider.unbindAll()
                cameraProvider.bindToLifecycle(
                    this,
                    CameraSelector.DEFAULT_BACK_CAMERA,
                    preview,
                    imageAnalysis
                )
            } catch (e: Exception) {
                Log.e("PairingActivity", "Camera bind failed", e)
                runOnUiThread { showEntryScreen() }
            }
        }, ContextCompat.getMainExecutor(this))
    }

    private fun proceedWithUrl(url: String) {
        val intent = Intent(this, MainActivity::class.java).apply {
            putExtra("pairing_url", url)
            flags = Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_NEW_TASK
        }
        startActivity(intent)
        finish()
    }

    private fun isValidPairingUrl(url: String): Boolean {
        return try {
            val u = android.net.Uri.parse(url)
            (u.scheme == "http" || u.scheme == "https") &&
                !u.host.isNullOrEmpty() &&
                !u.getQueryParameter("token").isNullOrEmpty()
        } catch (e: Exception) {
            false
        }
    }

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray
    ) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode == CAMERA_REQUEST) {
            if (grantResults.isNotEmpty() && grantResults[0] == PackageManager.PERMISSION_GRANTED) {
                showScanScreen()
            }
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        cameraExecutor.shutdown()
    }

    private fun dp(value: Int): Int {
        return (value * resources.displayMetrics.density).toInt()
    }

    companion object {
        private const val CAMERA_REQUEST = 1001
    }
}
