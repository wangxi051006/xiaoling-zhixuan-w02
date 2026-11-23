package cn.xiaoling.rider

import android.webkit.JavascriptInterface
import android.webkit.WebView
import org.json.JSONObject

/**
 * 页面侧调用：window.Native.xxx(...)
 * 方法体在 WebView 的 JS 桥线程执行，涉及 UI 的动作都转回主线程。
 */
class Bridge(private val act: MainActivity, private val web: WebView) {

    @JavascriptInterface
    fun version(): String = BuildConfig.VERSION_NAME

    @JavascriptInterface
    fun platform(): String = "android"

    /** 页面拿到 /me 后把 csrf_token 交给原生，离线重放与定位上报都要它。 */
    @JavascriptInterface
    fun setAuth(csrf: String) = Api.setCsrf(act, csrf)

    /** 新单提醒：通知 + 铃声 + 振动。payload 为订单摘要 JSON。 */
    @JavascriptInterface
    fun alert(json: String) {
        val obj = runCatching { JSONObject(json) }.getOrNull()
        val title = obj?.optString("title", "新订单") ?: "新订单"
        val body = obj?.optString("body", "点击查看订单") ?: "点击查看订单"
        Notifier.alert(act, title, body, json)
    }

    @JavascriptInterface
    fun stopAlert() = Notifier.stop(act)

    /** 上线后开始上报位置；鉴权走 WebView 的 Cookie。 */
    @JavascriptInterface
    fun startTracking() = act.startLocationService()

    @JavascriptInterface
    fun stopTracking() = act.stopLocationService()

    @JavascriptInterface
    fun keepAwake(on: Boolean) = act.setKeepAwake(on)

    /** 弱网兜底：写操作交给 WorkManager，联网后自动重放。 */
    @JavascriptInterface
    fun enqueue(method: String, path: String, body: String, idemKey: String) {
        UploadQueue.enqueue(act, method, path, body, idemKey)
    }

    fun deliverScan(cbId: String, code: String) {
        val js = "window.__nativeScan&&window.__nativeScan(" +
            JSONObject.quote(cbId) + "," + JSONObject.quote(code) + ")"
        web.post { web.evaluateJavascript(js, null) }
    }
}
