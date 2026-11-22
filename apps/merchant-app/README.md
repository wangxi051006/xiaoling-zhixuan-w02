# 校零智选商家 · Android 壳（Trusted Web Activity）

这个模块**不含业务代码**：它是一个 TWA 壳，用 Chrome 内核打开部署好的商家端网页
（`MERCHANT_ORIGIN`），并负责桌面图标、名称、启动图、深链与「无地址栏」校验。

包名 `cn.xiaoling.merchant`，应用名 `校零智选商家`。

## 目录

```
apps/merchant-app/
├── AndroidManifest.xml          # 两个 app 共用的壳清单（占位符由 Gradle 注入）
├── client.gradle                # 壳通用逻辑：origin 校验、启动 URL、签名、版本号
├── merchant/build.gradle        # 只声明 clientId / clientName / originVariable
├── tools/make_icons.py          # 生成图标与启动图（品牌色取自 web/src/user/style.css）
└── merchant/src/main/res/       # 图标、颜色、字符串（由上面的脚本与手写 XML 组成）
```

## 前置

| 项 | 版本 | 说明 |
| --- | --- | --- |
| Gradle | 8.11.1 | 已带 `gradlew`，`distributionUrl` 指向腾讯镜像 |
| Gradle JDK | **17** | AS 自带 JBR 是 25，Gradle 8.11.1 不支持，需在 AS 里指定 JDK 17 |
| Android SDK | Platform 36 / Build-Tools 36 | `compileSdk = 36`、`targetSdk = 36` |
| minSdk | 24 | `androidbrowserhelper:2.7.4` 要求 ≥24 |

## 构建

```powershell
# 调试包（指向线上 HTTPS 域名）
$env:MERCHANT_ORIGIN = "https://merchant.example.com"
$env:RIDER_ORIGIN   = "https://rider.example.com"     # 必须与商家不同
.\gradlew.bat :merchant:assembleDebug

# 产物
merchant\build\outputs\apk\debug\merchant-debug.apk
```

版本号可覆盖：`-PappVersionCode=12 -PappVersionName=1.2.0`。

### 在 Android Studio 里同步

Android Studio 的 Gradle 进程**不会**继承终端里 `$env:` 设置的变量，所以在 IDE 里同步会直接报
`MERCHANT_ORIGIN ... (got: null)`。把 origin 写进工程根目录的 `local.properties`（已被
`.gitignore` 排除，只影响本机）：

```properties
MERCHANT_ORIGIN=https://merchant.example.com
```

然后 Build ▸ Sync Project with Gradle Files 即可。origin 的解析顺序是
`-PMERCHANT_ORIGIN=…` → 环境变量 → `local.properties`，三种方式任选其一。

只同步、不构建时允许暂时缺省：脚本用一个占位 origin 让工程配置通过；真正构建
（`assembleDebug` 等）时缺省会立即失败，避免打出指向占位地址的包。

### 本地联调（无域名）

```powershell
$env:MERCHANT_ORIGIN    = "http://merchant.localhost:5173/merchant.html"
$env:RIDER_ORIGIN       = "https://rider.example.com"
$env:ALLOW_LOCAL_ORIGIN = "1"      # 缺省时 http origin 会被直接拒绝
.\gradlew.bat :merchant:assembleDebug
```

约束（`client.gradle` 里强制）：

- 只接受 `http://10.0.2.2|127.0.0.1|*.localhost:<port>[/path]`，而且必须显式 `ALLOW_LOCAL_ORIGIN=1`；
- 任何带 `release` 的构建任务遇到 http origin 直接失败；
- 真机联调配合 `adb reverse tcp:5173 tcp:5173`，让设备上的 `*.localhost` 指向开发机。

## 正式签名

keystore 与口令**不进仓库**，通过环境变量注入；没有配置时 release 产物是未签名的。

```powershell
$env:MERCHANT_KEYSTORE            = "D:\keys\merchant-release.jks"
$env:MERCHANT_KEYSTORE_PASSWORD   = "***"
$env:MERCHANT_KEY_ALIAS           = "merchant"
$env:MERCHANT_KEY_PASSWORD        = "***"
.\gradlew.bat :merchant:assembleRelease
```

## 图标与启动图

图标是脚本生成的（橙色圆角方块 + 白色「零」，与网页 `.logo` 一致），改样式后重跑：

```powershell
python tools\make_icons.py
```

产出：`mipmap-*/ic_launcher[_round].png`（API 24-25）、`drawable-*/ic_launcher_foreground.png`
（API 26+ 自适应前景）、`drawable-nodpi/splash_logo.png`（启动图）。

## Digital Asset Links（去掉地址栏的关键）

1. 取安装包的签名指纹：

   ```powershell
   keytool -list -v -keystore <keystore> -alias <alias> | findstr SHA256
   ```

2. 生成声明文件：

   ```powershell
   node assetlinks.mjs merchant <SHA256 指纹>
   ```

3. 部署到 `https://<商家域名>/.well-known/assetlinks.json`（`Content-Type: application/json`，不能重定向）。

未通过校验时壳会按 `FALLBACK_STRATEGY=customtabs` 回落成 Chrome 自定义标签页：**页面仍可用，只是顶部有地址栏**。
本地 http 联调永远属于这种情况。

## 商家端界面

TWA 壳只负责打开商家端网页，界面代码在仓库的 `web/src/merchant/`：

- `entry.ts`：订单 / 店铺 / 我的三个底部页签，以及商品、库存、分类、财务、营业数据、售后等子页；
- `app.css`：移动端布局、浅蓝顶栏、卡片、图标块、空态插画与安全区；
- `web/merchant.html`：`viewport-fit=cover` 与 `theme-color`。

2026-11-22 的改版说明、截图和功能边界见 [evidence/ui-redesign/README.md](evidence/ui-redesign/README.md)。

## 验收清单

- [ ] 桌面图标是橙色「零」，名称「校零智选商家」
- [ ] 点击图标直接进入商家端，**顶部无地址栏**（说明 assetlinks 通过）
- [ ] 员工登录 → IdP 回调 `/api/v1/auth/staff/callback` → 回到 App 内
- [ ] 按返回键回到首页而不是直接退出；从最近任务重进，登录态还在
- [ ] 页面里的外链在 Chrome 自定义标签页打开，不跳出 App
- [ ] 未安装 Chrome 的设备：按 `FALLBACK_STRATEGY` 仍能打开页面

## 已知行为

- **启动图只在 TWA 校验通过时出现**；回落到 Custom Tab 时不会显示 `splash_logo`。
- Chrome 首次运行向导（新模拟器）会挡住 TWA，需要先选「Stay signed out」。
- 本地测试鉴权同一账号再次登录会让旧会话失效，App 里重新点「测试登录」即可。
