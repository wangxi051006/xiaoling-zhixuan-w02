# 校零智选用户端 · Android 壳（Trusted Web Activity）

这个模块**不含业务代码**：它是一个 TWA 壳，用 Chrome 内核打开部署好的用户商城网页
（`USER_ORIGIN`，默认入口 `web/user.html`），并负责桌面图标、名称、启动图与「无地址栏」校验。

包名 `cn.xiaoling.user`，应用名 `校零智选`。与 `apps/merchant-app`（`cn.xiaoling.merchant`）、
`apps/rider-app`（`cn.xiaoling.rider`）共用同一套壳逻辑，只有 origin、图标配色和签名前缀不同。

## 目录

```
apps/user-app/
├── AndroidManifest.xml          # 三端共用的壳清单（占位符由 Gradle 注入）
├── client.gradle                # 壳通用逻辑：origin 校验、peer 互斥、启动 URL、签名、版本号
├── user/build.gradle            # 只声明 clientId / clientName / originVariable / peerOriginVariables
├── tools/make_icons.py          # 生成图标与启动图（品牌色取自 web/src/user/style.css）
└── user/src/main/res/           # 图标、颜色、字符串
```

## 构建

```powershell
# 1) 先构建用户端网页（产出 web/dist，部署到用户商城域名）
cd web
npm run build          # user.html / admin.html / rider.html → web/dist
cd ..

# 2) 打调试包（本地联调 origin）
node apps/user-app/gradlew.bat :user:assembleDebug
# 或在仓库根目录：
powershell -ExecutionPolicy Bypass -File tools/build-apk.ps1 -Target user
```

产物：`apps/user-app/user/build/outputs/apk/debug/user-debug.apk`

生产构建（指向真实域名 + 正式签名）：

```powershell
$env:USER_ORIGIN                 = "https://user.example.com"
$env:USER_KEYSTORE               = "D:\keys\user-release.jks"
$env:USER_KEYSTORE_PASSWORD      = "***"
$env:USER_KEY_ALIAS              = "user"
$env:USER_KEY_PASSWORD           = "***"
.\gradlew.bat :user:assembleRelease
```

版本号可用 `-PappVersionCode=12 -PappVersionName=1.2.0` 覆盖。

## 本地联调

```powershell
# 仓库根目录起开发栈（用户端 Vite 5194 → 后端 8080）
powershell -ExecutionPolicy Bypass -File tools/dev-stack.ps1 start
adb reverse tcp:5194 tcp:5194        # 真机/模拟器把 user.localhost 指到开发机
```

`local.properties` 里已经写好：

```properties
USER_ORIGIN=http://user.localhost:5194/user.html
ALLOW_LOCAL_ORIGIN=1
```

约束（`client.gradle` 里强制）：

- 只接受 `http://10.0.2.2|127.0.0.1|*.localhost:<port>[/path]`，且必须显式 `ALLOW_LOCAL_ORIGIN=1`；
- `USER_ORIGIN` 必须与 `MERCHANT_ORIGIN`、`RIDER_ORIGIN` 不同，否则后端按 origin 分流会串号；
- 任何带 `release` 的构建任务遇到 http origin 直接失败。

## Digital Asset Links（去掉地址栏的关键）

1. 取安装包签名指纹：`keytool -list -v -keystore <keystore> -alias <alias> | findstr SHA256`
2. 生成声明：`node assetlinks.mjs user <SHA-256 指纹>`
3. 部署到 `https://<用户商城域名>/.well-known/assetlinks.json`（`Content-Type: application/json`，不能重定向）。

未通过校验时壳按 `FALLBACK_STRATEGY=customtabs` 回落成 Chrome 自定义标签页：页面仍可用，只是顶部有地址栏。
本地 http 联调永远属于这种情况。

## 图标

```powershell
python tools\make_icons.py     # 橙色「零」+ 奶油底，与网页 .logo 一致
```

## 与微信小程序的关系

`apps/user-miniprogram` 是同一套用户商城的微信入口（`web-view`）。两个渠道共用 `web/` 的用户端页面和后端 API，
APK 面向安卓桌面安装，小程序面向微信内分发。
