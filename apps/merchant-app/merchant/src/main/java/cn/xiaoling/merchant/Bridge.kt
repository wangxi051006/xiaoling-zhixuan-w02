package cn.xiaoling.merchant

import android.content.Intent
import android.webkit.JavascriptInterface
import android.webkit.WebView
import com.journeyapps.barcodescanner.ScanOptions
import org.json.JSONObject

/**
 * window.Native.* 的商家端实现：打印、扫码、提醒、常亮、离线重放。
 * 所有方法都返回可判定的结果，页面拿不到原生能力时自动退回浏览器行为。
 */
class Bridge(private val act: MainActivity, private val web: WebView) {

    private var pendingScanId: String = ""

    @JavascriptInterface
    fun version(): String = BuildConfig.VERSION_NAME

    @JavascriptInterface
    fun platform(): String = "android"

    @JavascriptInterface
    fun setAuth(csrf: String) = Api.setCsrf(act, csrf)

    @JavascriptInterface
    fun alert(json: String) {
        val obj = runCatching { JSONObject(json) }.getOrNull()
        val title = obj?.optString("title", "新订单") ?: "新订单"
        val body = obj?.optString("body", "有新的订单待处理") ?: "有新的订单待处理"
        Notifier.alert(act, title, body, json)
    }

    @JavascriptInterface
    fun stopAlert() = Notifier.stop(act)

    /** 返回 true 表示已提交到打印机或打印队列。 */
    @JavascriptInterface
    fun printReceipt(text: String): Boolean {
        val ok = EscPosPrinter.print(act, text)
        if (!ok) PrintQueue.enqueue(act, text)
        return ok
    }

    @JavascriptInterface
    fun printerName(): String = EscPosPrinter.printerName(act) ?: ""

    @JavascriptInterface
    fun scan(cbId: String) {
        pendingScanId = cbId
        val options = ScanOptions()
            .setDesiredBarcodeFormats(ScanOptions.ALL_CODE_TYPES)
            .setPrompt("对准取餐码或订单二维码")
            .setBeepEnabled(false)
            .setOrientationLocked(false)
        act.startActivityForResult(options.createScanIntent(act), MainActivity.REQ_SCAN)
    }

    @JavascriptInterface
    fun keepAwake(on: Boolean) = act.setKeepAwake(on)

    @JavascriptInterface
    fun enqueue(method: String, path: String, body: String, idemKey: String) {
        UploadQueue.enqueue(act, method, path, body, idemKey)
    }

    fun deliverScan(cbId: String, code: String) {
        val js = "window.__nativeScan&&window.__nativeScan(" +
            JSONObject.quote(cbId) + "," + JSONObject.quote(code) + ")"
        web.post { web.evaluateJavascript(js, null) }
    }

    fun handleScanResult(resultCode: Int, data: Intent?) {
        val result = com.journeyapps.barcodescanner.ScanIntentResult.parseActivityResult(resultCode, data)
        if (result.contents.isNullOrEmpty()) deliverScan(pendingScanId, "") else deliverScan(pendingScanId, result.contents)
        pendingScanId = ""
    }
}
