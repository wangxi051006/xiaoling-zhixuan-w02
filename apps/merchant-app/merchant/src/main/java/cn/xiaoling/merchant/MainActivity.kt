package cn.xiaoling.merchant

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
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
 * 商家端原生容器：页面仍在 web/src/merchant，但打印、扫码、提醒、常亮由原生提供。
 * 与旧 TWA 壳的关键差别：window.print() 换成真正的 ESC/POS 小票打印，扫码不再依赖浏览器。
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
            mediaPlaybackRequiresUserGesture = false
            userAgentString = userAgentString + " XiaolingMerchant/" + BuildConfig.VERSION_NAME
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

        if (savedInstanceState == null) web.loadUrl(BuildConfig.START_URL) else web.restoreState(savedInstanceState)
    }

    private fun requestRuntimePermissions() {
        val wanted = mutableListOf(Manifest.permission.CAMERA)
        if (Build.VERSION.SDK_INT >= 31) {
            wanted.add(Manifest.permission.BLUETOOTH_CONNECT)
            wanted.add(Manifest.permission.BLUETOOTH_SCAN)
        }
        if (Build.VERSION.SDK_INT >= 33) wanted.add(Manifest.permission.POST_NOTIFICATIONS)
        val missing = wanted.filter { checkSelfPermission(it) != PackageManager.PERMISSION_GRANTED }
        if (missing.isNotEmpty()) requestPermissions(missing.toTypedArray(), REQ_BASE)
    }

    fun setKeepAwake(on: Boolean) = runOnUiThread {
        if (on) window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        else window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
    }

    fun onScanResult(cbId: String, code: String) = bridge.deliverScan(cbId, code)

    fun onScanCancelled(cbId: String) = bridge.deliverScan(cbId, "")

    override fun onResume() {
        super.onResume()
        web.onResume()
        web.evaluateJavascript("window.__nativeResume&&window.__nativeResume()", null)
        PrintQueue.flush(this)
    }

    override fun onPause() {
        web.onPause()
        super.onPause()
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        web.saveState(outState)
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == REQ_SCAN) bridge.handleScanResult(resultCode, data)
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
        const val REQ_BASE = 2001
        const val REQ_SCAN = 2002
    }
}
