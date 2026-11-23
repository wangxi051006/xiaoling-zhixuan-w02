package cn.xiaoling.merchant

import android.content.Context

/**
 * 打印失败兜底：小票文本落 SharedPreferences，下次回到前台或再次打印时重试。
 * 高峰期打印机断电/走纸，不能因此丢单。
 */
object PrintQueue {

    private const val PREFS = "xiaoling_merchant"
    private const val KEY = "print_backlog"
    private const val SEP = "\u0001"

    fun enqueue(ctx: Context, text: String) {
        val prefs = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val current = prefs.getString(KEY, "").orEmpty()
        val next = if (current.isEmpty()) text else current + SEP + text
        prefs.edit().putString(KEY, next).apply()
    }

    fun size(ctx: Context): Int =
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY, "").orEmpty()
            .split(SEP).count { it.isNotBlank() }

    fun flush(ctx: Context) {
        val prefs = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val raw = prefs.getString(KEY, "").orEmpty()
        if (raw.isBlank()) return
        val items = raw.split(SEP).filter { it.isNotBlank() }
        val remaining = mutableListOf<String>()
        for (item in items) {
            if (!EscPosPrinter.print(ctx, item)) remaining.add(item)
        }
        prefs.edit().putString(KEY, remaining.joinToString(SEP)).apply()
    }
}
