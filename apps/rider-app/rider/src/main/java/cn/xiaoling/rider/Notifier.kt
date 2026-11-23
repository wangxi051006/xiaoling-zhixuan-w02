package cn.xiaoling.rider

import android.app.Activity
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.media.AudioAttributes
import android.media.Ringtone
import android.media.RingtoneManager
import android.os.Build
import android.os.VibrationEffect
import android.os.Vibrator
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat

/**
 * 新单提醒必须穿透锁屏与静音：高优渠道 + 全屏 Intent + STREAM_ALARM 铃声 + 振动。
 */
object Notifier {

    const val CH_ORDER = "new_order"
    const val CH_ONGOING = "ongoing"
    const val ID_ONGOING = 1001
    const val ID_ORDER = 1002

    private var ringtone: Ringtone? = null

    fun ensure(ctx: Context) {
        if (Build.VERSION.SDK_INT < 26) return
        val nm = ctx.getSystemService(NotificationManager::class.java) ?: return
        nm.createNotificationChannel(
            NotificationChannel(CH_ORDER, "新订单", NotificationManager.IMPORTANCE_HIGH).apply {
                setSound(null, null)
                enableVibration(true)
                vibrationPattern = longArrayOf(0, 600, 400, 600)
                setBypassDnd(true)
                lockscreenVisibility = Notification.VISIBILITY_PUBLIC
            }
        )
        nm.createNotificationChannel(
            NotificationChannel(CH_ONGOING, "配送状态", NotificationManager.IMPORTANCE_LOW)
        )
    }

    fun ongoing(ctx: Context, text: String): Notification {
        ensure(ctx)
        return NotificationCompat.Builder(ctx, CH_ONGOING)
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setContentTitle("校零骑手")
            .setContentText(text)
            .setOngoing(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .build()
    }

    fun alert(act: Activity, title: String, body: String, payload: String) {
        ensure(act)
        val open = Intent(act, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP
            putExtra("order", payload)
        }
        val pi = PendingIntent.getActivity(
            act, 0, open,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        val n = NotificationCompat.Builder(act, CH_ORDER)
            .setSmallIcon(android.R.drawable.stat_notify_more)
            .setContentTitle(title)
            .setContentText(body)
            .setPriority(NotificationCompat.PRIORITY_MAX)
            .setCategory(NotificationCompat.CATEGORY_CALL)
            .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
            .setFullScreenIntent(pi, true)
            .setContentIntent(pi)
            .setAutoCancel(true)
            .build()
        runCatching { NotificationManagerCompat.from(act).notify(ID_ORDER, n) }
        startRing(act)
        vibrate(act)
    }

    private fun startRing(ctx: Context) {
        val uri = RingtoneManager.getDefaultUri(RingtoneManager.TYPE_ALARM)
            ?: RingtoneManager.getDefaultUri(RingtoneManager.TYPE_NOTIFICATION)
        val r = RingtoneManager.getRingtone(ctx.applicationContext, uri) ?: return
        if (Build.VERSION.SDK_INT >= 21) {
            r.audioAttributes = AudioAttributes.Builder()
                .setUsage(AudioAttributes.USAGE_ALARM)
                .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
                .build()
        }
        if (Build.VERSION.SDK_INT >= 28) r.isLooping = true
        r.play()
        ringtone = r
    }

    private fun vibrate(ctx: Context) {
        val v = ctx.getSystemService(Vibrator::class.java) ?: return
        if (Build.VERSION.SDK_INT >= 26) {
            v.vibrate(VibrationEffect.createWaveform(longArrayOf(0, 600, 400, 600), -1))
        } else {
            @Suppress("DEPRECATION")
            v.vibrate(longArrayOf(0, 600, 400, 600), -1)
        }
    }

    fun stop(ctx: Context) {
        ringtone?.stop()
        ringtone = null
        val v = ctx.getSystemService(Vibrator::class.java)
        v?.cancel()
        runCatching { NotificationManagerCompat.from(ctx).cancel(ID_ORDER) }
    }
}
