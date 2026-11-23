# tools/dev-stack

一条命令拉起本地联调所需的服务，避免每次重启电脑后手工起 PostgreSQL / 后端 / Vite。

## 用法

```powershell
# 查看状态（默认动作）
tools\dev-stack.cmd

# 启动：PostgreSQL 服务 + Go API + Vite + adb 端口映射（有设备时）
tools\dev-stack.cmd start

# 停止后端与前端（PostgreSQL 服务保持运行）
tools\dev-stack.cmd stop

# 重启
tools\dev-stack.cmd restart

# 首次在一台新机器上初始化数据库（initdb + 注册 Windows 服务 + 建库，需要管理员）
tools\dev-stack.cmd init-db
```

等价写法：`powershell -NoProfile -ExecutionPolicy Bypass -File tools\dev-stack.ps1 start`。

## 默认值

| 项 | 默认 | 说明 |
| --- | --- | --- |
| Vite（商家/骑手/运营） | `127.0.0.1:5173` | 端口必须与两个 APK 里写死的 origin 端口一致 |
| Vite（用户端） | `127.0.0.1:5194` | 必须与 `apps/user-miniprogram/storefront.js` 的 `storefrontUrl` 端口一致 |
| Go API | `127.0.0.1:8080` | 可用 `-ApiPort` 改 |
| PostgreSQL | `127.0.0.1:5432`，库 `campus` | 服务名 `pgsql-local`，可用 `-PgService`/`-DbName` 改 |
| 入口（员工端） | `http://rider.localhost:5173/rider.html`、`http://merchant.localhost:5173/merchant.html`、`http://admin.localhost:5173/admin.html` | 本地测试登录 `123456` / `123456` |
| 入口（用户端） | `http://user.localhost:5194/user.html` | 小程序 `web-view` 打开的就是这个地址 |

后端环境变量由脚本统一注入：`APP_ENV=development`、`ALLOW_TEST_AUTH=true`、四个 `*.localhost` origin（user 5194；rider/merchant/admin 5173）、
`COD/ORDERS/DISPATCH/BALANCE_ENABLED=true`。测试登录只在 loopback 数据库 + `<entry>.localhost` origin 下可用。

## 常用参数

| 参数 | 作用 |
| --- | --- |
| `-ApiPort 8082` | 换后端端口（`DEV_API_ORIGIN` 会跟着变） |
| `-WebPort 5175` | 换员工端 Vite 端口（需同步改两个 APK 的 origin 与后端 `RIDER_ORIGIN`/`MERCHANT_ORIGIN`） |
| `-UserPort 5195` | 换用户端 Vite 端口（需同步改 `storefront.js` 与 `USER_ORIGIN`） |
| `-DbName campus_devstack` | 用独立数据库，避免和其他会话/手动实验的数据互相污染 |
| `-Force` | 端口被别的进程占用时接管（会 kill 对方进程，谨慎） |
| `-SkipMigrate` | 跳过迁移（数据库已迁移过时可用） |
| `-WithEmulator` | 没有连接中的设备时自动冷启动 AVD 并做 `adb reverse` |

## 状态与日志

- 状态目录：仓库根 `.dev-stack/`（已加入 `.gitignore`）——`api.pid`、`web.pid`、`user-web.pid`、`campus-api.exe` 及对应 `*.out.log`/`*.err.log`
- 脚本按 PID 文件停服务；遇到不是自己启动的进程只提示，不动手（除非 `-Force`）
- 后端编译缓存：只改了 `*.go` / `*.sql` 才会重新编译

## Android 端

三个 APK 里的 origin 必须和这里的 Vite 地址一致（`http://rider.localhost:<WebPort>/rider.html`、`http://merchant.localhost:<WebPort>/merchant.html`、`http://user.localhost:<UserPort>/user.html`），
构建命令见 `apps/README.md`，或直接 `tools\build-apk.cmd`（`-Target all|merchant|user|rider`、`-Mode debug|release`）。模拟器每次重启后都要重做 `adb reverse`；`start` 会顺手补上。

# tools/phone-apk

手机装包一条龙：局域网下载站 + `adb reverse` + `adb install`，安装包统一放 `dist/apk/`。

```powershell
tools\phone-apk.cmd status     # 设备 / 端口映射 / 安装包清单
tools\phone-apk.cmd serve      # 起下载站，手机浏览器打开 http://<本机局域网 IP>:8000/
tools\phone-apk.cmd link       # 对已连接设备做 adb reverse tcp:5173 tcp:5173
tools\phone-apk.cmd install    # 直接 adb install -r dist/apk 下的两个包
tools\phone-apk.cmd all        # link + install + serve
```

下载站是 `tools/apk-share.mjs`（纯 Node，无依赖），每次请求按目录实时列包、算 SHA-256 前 16 位，换包不用重启；端口 `-Port`，目录 `-ApkDir`。手机与电脑需在同一 WiFi，首次安装要在手机上允许「安装未知应用」。

## 打包装机注意

`client.gradle` 的 origin 解析顺序是 `-P` → 环境变量 → `local.properties`。终端里残留的 `MERCHANT_ORIGIN` / `RIDER_ORIGIN`（例如上次用模拟器联调留下的 `10.0.2.2`、`5174`）会盖掉 `local.properties`，打出指向错误地址的包。打包前显式对齐：

```powershell
$env:MERCHANT_ORIGIN    = "http://merchant.localhost:5173/merchant.html"
$env:RIDER_ORIGIN       = "http://rider.localhost:5173/rider.html"
$env:ALLOW_LOCAL_ORIGIN = "1"
tools\build-apk.cmd -Target all
```

核对包里写死的地址：看 `apps/merchant-app/merchant/build/generated/source/buildConfig/debug/cn/xiaoling/merchant/BuildConfig.java` 与骑手端同名文件的 `START_URL`；装到设备后执行 `adb reverse tcp:5173 tcp:5173`，桌面图标才能加载页面（`*.localhost` 在手机上指向手机自己的回环，后端测试登录也硬性要求 `<entry>.localhost` 入口）。

## 无线调试（不插数据线）

```powershell
# 手机：设置 → 开发者选项 → 无线调试 → 使用配对码配对设备（拿到 IP:端口 与 6 位配对码）
tools\phone-apk.cmd pair    -WifiHost 192.168.x.x -PairPort 37xxx -PairCode 123456
tools\phone-apk.cmd connect -WifiHost 192.168.x.x -AdbPort 41xxx
tools\phone-apk.cmd link    # adb reverse tcp:5173 tcp:5173
```

`adb reverse` 走 ADB 通道，页面仍然从设备本机的 `*.localhost` 取，和插线效果完全一样；手机换网络后要重新 `connect` 并重做 `reverse`。

## 为什么不能把 origin 换成局域网 IP

页面能打开，但登录一定失败：会话 Cookie 是 `__Host-xiaoling-session`，带 `__Host-` 前缀与 `Secure`（`backend/internal/identity/session.go:178`），浏览器只允许把它写到可信来源——`*.localhost` 或 HTTPS。
手机访问 `http://192.168.x.x:5173` 属于不可信来源，`Secure` Cookie 被直接丢弃，登录返回 204 之后下一次请求仍是匿名。
要么 `adb reverse`（推荐），要么真的部署 HTTPS 域名。

