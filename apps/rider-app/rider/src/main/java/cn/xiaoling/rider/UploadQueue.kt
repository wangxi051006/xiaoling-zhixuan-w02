package cn.xiaoling.rider

import android.content.Context
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.Worker
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import java.util.concurrent.TimeUnit

/**
 * 弱网兜底：确认送达、取货这类写操作先入队，联网后自动重放。
 * idemKey 由页面生成（与服务端幂等键一致），重复入队会被 KEEP 策略吞掉。
 */
object UploadQueue {

    private const val PREFIX = "upload:"

    fun enqueue(ctx: Context, method: String, path: String, body: String, idemKey: String) {
        val data = workDataOf(
            "method" to method,
            "path" to path,
            "body" to body,
            "key" to idemKey
        )
        val request = OneTimeWorkRequestBuilder<UploadWorker>()
            .setInputData(data)
            .setConstraints(
                Constraints.Builder()
                    .setRequiredNetworkType(NetworkType.CONNECTED)
                    .build()
            )
            .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 10, TimeUnit.SECONDS)
            .build()
        WorkManager.getInstance(ctx)
            .enqueueUniqueWork(PREFIX + idemKey, ExistingWorkPolicy.KEEP, request)
    }

    fun pending(ctx: Context): Int = 0
}

class UploadWorker(ctx: Context, params: WorkerParameters) : Worker(ctx, params) {

    override fun doWork(): Result {
        val method = inputData.getString("method") ?: return Result.failure()
        val path = inputData.getString("path") ?: return Result.failure()
        val body = inputData.getString("body") ?: "{}"
        val key = inputData.getString("key") ?: return Result.failure()
        val api = Api(applicationContext, BuildConfig.START_URL)
        val ok = api.send(method, path, body, key)
        return when {
            ok -> Result.success()
            runAttemptCount >= 6 -> Result.failure()
            else -> Result.retry()
        }
    }
}
