#!/usr/bin/env node
// 校零智选 · 手机下载站：在局域网内把商家端 / 骑手端 APK 递给手机，并给出 adb reverse 联调提示。
// 用法：node tools/apk-share.mjs [--port 8000] [--dir dist/apk]
import { createServer } from "node:http";
import { createReadStream, existsSync, readdirSync, statSync } from "node:fs";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { networkInterfaces } from "node:os";
import { extname, join, resolve } from "node:path";

const argv = process.argv.slice(2);
const argOf = (name, fallback) => {
  const i = argv.indexOf("--" + name);
  return i >= 0 && argv[i + 1] ? argv[i + 1] : fallback;
};
const PORT = Number(argOf("port", process.env.APK_PORT || 8000));
const DIR = resolve(argOf("dir", process.env.APK_DIR || "dist/apk"));

const LABELS = {
  "merchant-debug.apk": { title: "校零智选 · 商家端", download: "校零智选商家.apk", color: "#ff8a3d", note: "店铺、商品、库存、订单、财务", rank: 0 },
  "rider-debug.apk": { title: "校零智选 · 骑手端", download: "校零智选骑手.apk", color: "#3b82f6", note: "任务大厅、取货、配送、钱包、排行", rank: 0 },
  "user-debug.apk": { title: "校零智选 · 用户端", download: "校零智选用户.apk", color: "#22c55e", note: "点单、支付、订单跟踪", rank: 1 },
};

function lanAddresses() {
  const out = [];
  for (const list of Object.values(networkInterfaces())) {
    for (const ni of list ?? []) {
      if (ni.family === "IPv4" && !ni.internal) out.push(ni.address);
    }
  }
  return out;
}

function apkList() {
  if (!existsSync(DIR)) return [];
  return readdirSync(DIR)
    .filter((f) => f.toLowerCase().endsWith(".apk"))
    .map((f) => {
      const p = join(DIR, f);
      const st = statSync(p);
      const hash = createHash("sha256").update(readFileSync(p)).digest("hex").slice(0, 16);
      return { file: f, bytes: st.size, mtime: st.mtime, hash, ...(LABELS[f] ?? { title: f, download: f, color: "#666", note: "" }) };
    })
    .sort((a, b) => (a.rank ?? 9) - (b.rank ?? 9) || a.title.localeCompare(b.title, "zh"));
}

const mb = (n) => (n / 1024 / 1024).toFixed(2) + " MB";

function page() {
  const items = apkList();
  const cards = items
    .map(
      (a) => `<a class="card" href="/dl/${encodeURIComponent(a.file)}">
      <span class="dot" style="background:${a.color}"></span>
      <span class="meta"><b>${a.title}</b><i>${a.note}</i>
      <em>${mb(a.bytes)} · ${a.mtime.toLocaleString("zh-CN", { hour12: false })} · sha256:${a.hash}</em></span>
      <span class="go">下载</span></a>`
    )
    .join("\n");
  return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="theme-color" content="#0f172a">
<title>校零智选 · 手机安装包</title>
<style>
*{box-sizing:border-box}body{margin:0;font:16px/1.55 -apple-system,"PingFang SC","Microsoft YaHei",sans-serif;
background:#0f172a;color:#e2e8f0;padding:22px 16px 40px}
h1{font-size:20px;margin:0 0 4px}p.sub{margin:0 0 18px;color:#94a3b8;font-size:13px}
.card{display:flex;align-items:center;gap:12px;background:#1e293b;border:1px solid #334155;border-radius:14px;
padding:14px;margin-bottom:12px;text-decoration:none;color:inherit}
.card:active{background:#243044}.dot{width:10px;height:34px;border-radius:6px;flex:none}
.meta{flex:1;min-width:0;display:flex;flex-direction:column;gap:2px}
.meta b{font-size:15px}.meta i{font-style:normal;color:#94a3b8;font-size:12px}
.meta em{font-style:normal;color:#64748b;font-size:11px;word-break:break-all}
.go{flex:none;background:#f8fafc;color:#0f172a;font-weight:600;font-size:13px;padding:7px 14px;border-radius:999px}
ol{padding-left:20px;color:#cbd5e1;font-size:13px;line-height:1.9}ol code{background:#1e293b;padding:1px 6px;border-radius:5px;color:#7dd3fc}
.note{margin-top:18px;background:#111c2f;border:1px dashed #334155;border-radius:12px;padding:12px 14px;font-size:12px;color:#94a3b8}
</style></head><body>
<h1>校零智选 · 手机安装包</h1>
<p class="sub">同一 WiFi 下直接下载安装；首次安装需在手机上允许「安装未知应用」。</p>
${cards}
<ol>
<li>下载 → 允许「安装未知应用」→ 安装</li>
<li>电脑接上手机：USB 调试，或无线调试（<code>adb pair</code> 然后 <code>adb connect</code>）</li>
<li>执行 <code>tools\\phone-apk.cmd link</code>（等价 <code>adb reverse tcp:5173 tcp:5173</code>），再点桌面图标</li>
<li>登录用本地测试账号 <code>123456 / 123456</code></li>
</ol>
<div class="note">包内页面地址：<code>http://merchant.localhost:5173/merchant.html</code> 与 <code>http://rider.localhost:5173/rider.html</code>。
手机上的 <code>localhost</code> 指向手机自己的回环，所以必须先 <code>adb reverse</code> 把设备 5173 端口接回电脑。<br>
会话 Cookie 带 <code>__Host-</code> 前缀和 <code>Secure</code>，只接受可信来源（<code>*.localhost</code> 或 HTTPS），
所以不能把 origin 直接换成局域网 IP —— 那样页面能开、登录一定失败。<br>
包为 Android Debug 签名（<code>cn.xiaoling.merchant</code> / <code>cn.xiaoling.rider</code>）。</div>
</body></html>`;
}

const server = createServer((req, res) => {
  const url = new URL(req.url ?? "/", "http://x");
  if (url.pathname === "/" || url.pathname === "/index.html") {
    const body = page();
    res.writeHead(200, { "content-type": "text/html; charset=utf-8", "cache-control": "no-store" });
    res.end(body);
    return;
  }
  if (url.pathname === "/healthz") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ ok: true, apks: apkList().map((a) => a.file) }));
    return;
  }
  if (url.pathname.startsWith("/dl/")) {
    const name = decodeURIComponent(url.pathname.slice(4));
    const file = apkList().find((a) => a.file === name);
    if (!file) {
      res.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
      res.end("没有这个安装包");
      return;
    }
    const p = join(DIR, file.file);
    res.writeHead(200, {
      "content-type": "application/vnd.android.package-archive",
      "content-length": statSync(p).size,
      "content-disposition": `attachment; filename="${file.file}"; filename*=UTF-8''${encodeURIComponent(file.download)}`,
    });
    if (req.method === "HEAD") return res.end();
    createReadStream(p).pipe(res);
    return;
  }
  res.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
  res.end("404");
});

server.listen(PORT, "0.0.0.0", () => {
  console.log(`[apk-share] 目录 ${DIR}`);
  console.log(`[apk-share] 本机  http://127.0.0.1:${PORT}/`);
  for (const ip of lanAddresses()) console.log(`[apk-share] 手机  http://${ip}:${PORT}/`);
  for (const a of apkList()) console.log(`[apk-share] ${a.file}  ${mb(a.bytes)}  sha256:${a.hash}`);
});
