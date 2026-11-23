package cn.xiaoling.rider

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.view.WindowManager
import android.webkit.GeolocationPermissions
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient

/**
 * 原生容器：承载 web/src/rider 页面，并把原生能力通过 window.Native 暴露给页面。
 * 与旧的 TWA 壳相比，这里是真正的 App 进程：可以起前台服务、收推送、发通知、保持唤醒。
 */
class MainActivity : Activity() {

    private lateinit var web: WebView
    private lateinit var bridge: Bridge

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        web = WebView(this)
        web.setBackgroundColor(Color.WHITE)
        web.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            databaseEnabled = true
            cacheMode = WebSettings.LOAD_DEFAULT
            mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
            // 新单提示音必须在无用户手势时也能播放
            mediaPlaybackRequiresUserGesture = false
            userAgentString = userAgentString + " XiaolingRider/" + BuildConfig.VERSION_NAME
        }

        bridge = Bridge(this, web)
        web.addJavascriptInterface(bridge, "Native")

        web.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView?, request: WebResourceRequest?): Boolean {
                val url = request?.url ?: return false
                if (url.host == BuildConfig.START_HOST) return false
                runCatching { startActivity(Intent(Intent.ACTION_VIEW, url)) }
                return true
            }
        }

        web.webChromeClient = object : WebChromeClient() {
            override fun onGeolocationPermissionsShowPrompt(
                origin: String?,
                callback: GeolocationPermissions.Callback?
            ) {
                callback?.invoke(origin, true, false)
            }
        }

        setContentView(web)
        Notifier.ensure(this)
        requestRuntimePermissions()

        if (savedInstanceState == null) {
            web.loadUrl(BuildConfig.START_URL)
        } else {
            web.restoreState(savedInstanceState)
        }
    }

    private fun requestRuntimePermissions() {
        val wanted = mutableListOf(
            Manifest.permission.ACCESS_FINE_LOCATION,
            Manifest.permission.ACCESS_COARSE_LOCATION
        )
        if (Build.VERSION.SDK_INT >= 33) wanted.add(Manifest.permission.POST_NOTIFICATIONS)
        val missing = wanted.filter {
            checkSelfPermission(it) != PackageManager.PERMISSION_GRANTED
        }
        if (missing.isNotEmpty()) {
            requestPermissions(missing.toTypedArray(), REQ_BASE)
        }
    }

    fun onScanResult(cbId: String, code: String) = bridge.deliverScan(cbId, code)

    fun startLocationService() {
        val i = Intent(this, LocationService::class.java)
        if (Build.VERSION.SDK_INT >= 26) startForegroundService(i) else startService(i)
    }

    fun stopLocationService() {
        stopService(Intent(this, LocationService::class.java))
    }

    fun setKeepAwake(on: Boolean) = runOnUiThread {
        if (on) window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        else window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
    }

    override fun onResume() {
        super.onResume()
        web.onResume()
        web.evaluateJavascript("window.__nativeResume&&window.__nativeResume()", null)
    }

    override fun onPause() {
        web.onPause()
        super.onPause()
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        web.saveState(outState)
    }

    override fun onDestroy() {
        web.destroy()
        super.onDestroy()
    }

    @Deprecated("Container keeps history inside the web view")
    override fun onBackPressed() {
        if (web.canGoBack()) web.goBack() else super.onBackPressed()
    }

    companion object {
        const val REQ_BASE = 1001
        const val REQ_BACKGROUND = 1002
    }
}
