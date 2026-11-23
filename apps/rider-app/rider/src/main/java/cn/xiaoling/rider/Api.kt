package cn.xiaoling.rider

import android.content.Context
import android.location.Location
import android.webkit.CookieManager
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONArray
import org.json.JSONObject
import okhttp3.Dns
import java.net.InetAddress
import java.util.concurrent.TimeUnit

/**
 * 原生侧的 HTTP 客户端：定位上报与离线重放用它。
 * 鉴权复用 WebView 的 Cookie（含 HttpOnly），CSRF 由页面通过 Native.setAuth 注入。
 * 页面本身的读写仍然走浏览器 fetch，这里不重复实现业务。
 */
class Api(private val ctx: Context, private val baseUrl: String) {

    // 浏览器按规范把 *.localhost 解析到 127.0.0.1，OkHttp 走系统 DNS 解析不到；
    // adb reverse 映射的正是设备回环端口，所以本地联调域名在这里钉到回环地址。
    private val localDns = object : Dns {
        override fun lookup(hostname: String): List<InetAddress> =
            if (hostname.endsWith(".localhost")) listOf(InetAddress.getByName("127.0.0.1"))
            else Dns.SYSTEM.lookup(hostname)
    }

    private val client = OkHttpClient.Builder()
        .dns(localDns)
        .connectTimeout(10, TimeUnit.SECONDS)
        .writeTimeout(20, TimeUnit.SECONDS)
        .readTimeout(20, TimeUnit.SECONDS)
        .retryOnConnectionFailure(true)
        .build()

    private val prefs = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    private fun origin(): String {
        val u = runCatching { java.net.URI(baseUrl) }.getOrNull() ?: return baseUrl
        val port = if (u.port > 0) ":" + u.port else ""
        return u.scheme + "://" + u.host + port
    }

    private fun cookie(): String =
        runCatching { CookieManager.getInstance().getCookie(origin() + "/") ?: "" }.getOrDefault("")

    // OkHttp 在主线程会被系统直接拒绝（NetworkOnMainThreadException），
    // 前台服务的 flush 跑在主 looper 上，所以上报统一丢到单线程执行器。
    private val io = java.util.concurrent.Executors.newSingleThreadExecutor()

    fun reportLocationsAsync(points: List<Location>) {
        if (points.isEmpty()) return
        io.execute { reportLocations(points) }
    }

    fun reportLocations(points: List<Location>): Boolean {
        if (points.isEmpty()) return true
        val arr = JSONArray()
        points.forEach { p ->
            arr.put(
                JSONObject()
                    .put("lat", p.latitude)
                    .put("lng", p.longitude)
                    .put("accuracy", p.accuracy.toDouble())
                    .put("speed", p.speed.toDouble())
                    .put("bearing", p.bearing.toDouble())
                    .put("recorded_at", p.time)
            )
        }
        val body = JSONObject().put("points", arr).toString()
        return send("POST", "/api/v1/rider/locations", body, "loc-" + points.first().time)
    }

    fun send(method: String, path: String, body: String, idemKey: String): Boolean {
        val url = if (path.startsWith("http")) path else origin() + path
        val verb = method.uppercase()
        val rb = body.toRequestBody(JSON)
        val builder = Request.Builder()
            .url(url)
            .header("Accept", "application/json")
            .header("Origin", origin())
            .header("Idempotency-Key", idemKey)
        val c = cookie()
        if (c.isNotEmpty()) builder.header("Cookie", c)
        val csrf = prefs.getString(KEY_CSRF, null)
        if (csrf != null && verb != "GET" && verb != "HEAD") builder.header("X-CSRF-Token", csrf)
        val request = when (verb) {
            "POST" -> builder.post(rb).build()
            "PUT" -> builder.put(rb).build()
            "PATCH" -> builder.patch(rb).build()
            "DELETE" -> builder.delete().build()
            else -> builder.get().build()
        }
        // 原生请求在真机上排障只能看 logcat：打出目标地址与结果码（不含 Cookie/CSRF）。
        return runCatching {
            client.newCall(request).execute().use { resp ->
                android.util.Log.i("XiaolingApi", "${verb} ${url} -> ${resp.code}")
                resp.isSuccessful || resp.code in 400..499
            }
        }.getOrElse {
            android.util.Log.w("XiaolingApi", "${verb} ${url} failed: ${it.javaClass.simpleName}: ${it.message}")
            false
        }
    }

    companion object {
        const val PREFS = "xiaoling_rider"
        const val KEY_CSRF = "csrf"
        private val JSON = "application/json; charset=utf-8".toMediaType()

        fun setCsrf(ctx: Context, token: String) {
            ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
                .edit().putString(KEY_CSRF, token).apply()
        }
    }
}
