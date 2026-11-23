package cn.xiaoling.merchant

import android.bluetooth.BluetoothAdapter
import android.bluetooth.BluetoothManager
import android.content.Context
import java.io.OutputStream
import java.util.UUID

/**
 * ESC/POS 小票打印：经典蓝牙 SPP（UUID 00001101-...）。
 * 中文字符用 GBK 发送（热敏机国标字库），打印完切纸。
 */
object EscPosPrinter {

    private val SPP: UUID = UUID.fromString("00001101-0000-1000-8000-00805F9B34FB")
    private const val PREFS = "xiaoling_merchant"
    private const val KEY_ADDR = "printer_address"

    fun printerName(ctx: Context): String? {
        val adapter = adapter(ctx) ?: return null
        val addr = prefs(ctx).getString(KEY_ADDR, null)
        return runCatching {
            if (addr != null) adapter.getRemoteDevice(addr).name
            else adapter.bondedDevices.firstOrNull { looksLikePrinter(it.name) }?.name
        }.getOrNull()
    }

    fun print(ctx: Context, text: String): Boolean {
        val adapter = adapter(ctx) ?: return false
        val device = runCatching {
            val addr = prefs(ctx).getString(KEY_ADDR, null)
            if (addr != null) adapter.getRemoteDevice(addr)
            else adapter.bondedDevices.firstOrNull { looksLikePrinter(it.name) }
        }.getOrNull() ?: return false
        return runCatching {
            adapter.getRemoteDevice(device.address)
                .createRfcommSocketToServiceRecord(SPP)
                .use { socket ->
                    socket.connect()
                    write(socket.outputStream, text)
                }
            prefs(ctx).edit().putString(KEY_ADDR, device.address).apply()
            true
        }.getOrDefault(false)
    }

    private fun write(out: OutputStream, text: String) {
        out.write(byteArrayOf(0x1B, 0x40))                 // ESC @ 初始化
        out.write(byteArrayOf(0x1B, 0x61, 0x01))           // 居中
        out.write(byteArrayOf(0x1D, 0x21, 0x11))           // 双倍宽高
        val lines = text.split("\n")
        out.write(lines.firstOrNull().orEmpty().toByteArray(charset("GBK")))
        out.write(byteArrayOf(0x0A))
        out.write(byteArrayOf(0x1D, 0x21, 0x00, 0x1B, 0x61, 0x00))
        if (lines.size > 1) {
            out.write(lines.drop(1).joinToString("\n").toByteArray(charset("GBK")))
            out.write(byteArrayOf(0x0A))
        }
        out.write(byteArrayOf(0x0A, 0x0A))
        out.write(byteArrayOf(0x1D, 0x56, 0x41, 0x10))     // GS V A 切纸
        out.flush()
    }

    private fun adapter(ctx: Context): BluetoothAdapter? =
        runCatching {
            (ctx.getSystemService(Context.BLUETOOTH_SERVICE) as BluetoothManager).adapter
        }.getOrNull()

    private fun prefs(ctx: Context) = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    private fun looksLikePrinter(name: String?): Boolean {
        val n = name?.lowercase() ?: return false
        return listOf("printer", "pos", "esc", "58", "80mm", "gp-", "xp-", "sp-", "打印机").any { n.contains(it) }
    }
}
