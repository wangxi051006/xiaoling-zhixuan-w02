package cn.xiaoling.rider

import android.Manifest
import android.annotation.SuppressLint
import android.app.Service
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ServiceInfo
import android.location.Location
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import com.google.android.gms.location.FusedLocationProviderClient
import com.google.android.gms.location.LocationCallback
import com.google.android.gms.location.LocationRequest
import com.google.android.gms.location.LocationResult
import com.google.android.gms.location.LocationServices
import com.google.android.gms.location.Priority

/**
 * 配送中的前台服务：定位上报 + 常驻通知。
 * 没有它，App 切后台就会被系统回收，新单与轨迹都会断。
 */
class LocationService : Service() {

    private lateinit var fused: FusedLocationProviderClient
    private lateinit var api: Api
    private val buffer = mutableListOf<Location>()
    private val handler = Handler(Looper.getMainLooper())

    private val flushTask = object : Runnable {
        override fun run() {
            if (buffer.isNotEmpty()) {
                val batch = buffer.toList()
                buffer.clear()
                // 后台线程发送：主 looper 上直接发请求会抛 NetworkOnMainThreadException
                api.reportLocationsAsync(batch)
            }
            handler.postDelayed(this, FLUSH_MS)
        }
    }

    override fun onCreate() {
        super.onCreate()
        api = Api(applicationContext, BuildConfig.START_URL)
        fused = LocationServices.getFusedLocationProviderClient(this)
        startForegroundCompat()
        startUpdates()
        handler.postDelayed(flushTask, FLUSH_MS)
    }

    private fun startForegroundCompat() {
        val n = Notifier.ongoing(this, "正在配送 · 位置上报中")
        if (Build.VERSION.SDK_INT >= 29) {
            startForeground(Notifier.ID_ONGOING, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_LOCATION)
        } else {
            startForeground(Notifier.ID_ONGOING, n)
        }
    }

    @SuppressLint("MissingPermission")
    private fun startUpdates() {
        if (checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) != PackageManager.PERMISSION_GRANTED) {
            stopSelf()
            return
        }
        val request = LocationRequest.Builder(Priority.PRIORITY_HIGH_ACCURACY, INTERVAL_MS)
            .setMinUpdateDistanceMeters(MIN_DISTANCE_M.toFloat())
            .setWaitForAccurateLocation(false)
            .build()
        runCatching {
            fused.requestLocationUpdates(request, object : LocationCallback() {
                override fun onLocationResult(result: LocationResult) {
                    buffer.addAll(result.locations)
                }
            }, Looper.getMainLooper())
        }.onFailure { stopSelf() }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        intent?.getStringExtra("token")?.let { Api.setCsrf(applicationContext, it) }
        return START_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        handler.removeCallbacks(flushTask)
        runCatching { fused.removeLocationUpdates { } }
        super.onDestroy()
    }

    companion object {
        const val INTERVAL_MS = 15_000L
        const val MIN_DISTANCE_M = 50
        const val FLUSH_MS = 30_000L
    }
}
