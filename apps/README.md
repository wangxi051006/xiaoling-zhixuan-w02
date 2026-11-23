# 校零智选三端发布

| 客户端 | 工程 | 服务入口 |
| --- | --- | --- |
| 用户小程序 | `apps/user-miniprogram` | 用户商城 HTTPS 域名 |
| 校零智选商家 | `apps/merchant-app` | 独立商家 HTTPS 域名，包名 `cn.xiaoling.merchant` |
| 校零智选骑手 | `apps/rider-app` | 独立骑手 HTTPS 域名，包名 `cn.xiaoling.rider` |
| 校零智选（用户端） | `apps/user-app` | 用户商城 HTTPS 域名，包名 `cn.xiaoling.user` |

运营、财务和账号管理继续使用管理网页。三个客户端共用 Go API 和 PostgreSQL。

## 微信小程序

首版使用微信 `web-view` 打开现有商城，沿用商城的注册登录、商品、购物袋、下单、订单和已启用的支付方式。在微信公众平台配置用户商城的业务域名，并在用户域名上线商城和同源 API。检查小程序账号主体具备 `web-view` 能力，并确认服务器已配置正式登录与支付方式。

在微信开发者工具直接导入 `apps/user-miniprogram` 即可编译源码。需要在此工程调试商城时，将 `storefront.js` 中的 `storefrontUrl` 设置为已配置的小程序业务域名对应的 HTTPS 地址。地址留空时，小程序会显示未配置提示。

打包时，在仓库根目录设置 `WX_APP_ID` 和 `USER_ORIGIN`，执行 `node apps/user-miniprogram/build.mjs`。将生成的 `apps/user-miniprogram/dist` 导入微信开发者工具。使用正式 AppID、业务域名和 HTTPS 证书验收登录、下单及订单查询，再上传体验版和提审。当前默认构建目录已存在时，可以指定一个新的输出目录并导入该目录。

## Android

三个包各自启动对应域名的 Trusted Web Activity（用户端 `apps/user-app`、商家端 `apps/merchant-app`、骑手端 `apps/rider-app`）。支持该能力的浏览器依据 Digital Asset Links 验证关联，员工登录和 MFA 继续走正式 IdP 的浏览器流程。在实际派发的手机上验证默认浏览器、登录回跳及从桌面再次打开的行为。

用户端在 `web` 运行 `npm run build`，用 `deploy/scripts/split-static.mjs` 把 `dist` 拆到 `static/user`（TWA 入口 `<用户域名>/user.html`）。商家端运行 `npm run build:merchant`，将 `dist-merchant` 的内容部署至 Caddy 的 `static/merchant`。骑手使用现有 `static/rider` 构建。配置 `MERCHANT_ORIGIN`、`MERCHANT_HOST` 和覆盖商家域名的证书，并把商家域名加 `/api/v1/auth/staff/callback` 加入 IdP 回调。在两个对应站点各自提供 `/.well-known/assetlinks.json`，可用 `node apps/merchant-app/assetlinks.mjs merchant <SHA-256指纹>` 、`node apps/rider-app/assetlinks.mjs rider <SHA-256指纹>` 或 `node apps/user-app/assetlinks.mjs user <SHA-256指纹>` 生成（`apps/user-app/assetlinks.mjs` 同时支持 user/merchant/rider 三个角色）。指纹使用手机上实际安装包的签名证书。

三个工程可以在 Android Studio 分别打开（或双击各目录的 `本地打开-AndroidStudio.bat`），也可以直接用仓库根目录的 `tools\build-apk.cmd` 一条命令打三端 debug 包。使用 JDK 17、Gradle 8.11.1 和 Android SDK Platform 36，设置 `ANDROID_HOME`，并为各工程设置对应的 `MERCHANT_ORIGIN`、`USER_ORIGIN` 或 `RIDER_ORIGIN`。在仓库根目录分别执行下列命令构建调试包。

```powershell
gradle -p apps/merchant-app :merchant:assembleDebug
gradle -p apps/user-app :user:assembleDebug
gradle -p apps/rider-app :rider:assembleDebug
```

调试包分别输出到 `apps/merchant-app/merchant/build/outputs/apk/debug/`、`apps/user-app/user/build/outputs/apk/debug/` 和 `apps/rider-app/rider/build/outputs/apk/debug/`。发布时使用各自的正式签名构建并验证三个发行包。

## 开通商家

应用迁移 `120_001_merchant_ownership.sql`。在受控数据库操作中创建店铺记录 `merchants`，为已核对 IdP 身份的员工账号授予 `MERCHANT`，并创建 `merchant_staff` 店铺归属。一名商家账号绑定一家店铺。按确认的店铺标注商品的 `merchant_id`。未归属的商品仍可按旧流程销售，未归属的订单由平台后台处理。新订单记录下单时的店铺，跨店商品需分开下单。

商家端可以接单、完成备货，并维护本店商品：`POST /merchant/products` 新增商品（`merchant_id` 固定为调用者自己的店铺）、`PATCH /merchant/products/{id}` 改名/改描述/改价/改补货点/上下架、`POST /merchant/inventory/adjustments` 调整本店商品库存（跨店一律按不存在处理）。商品与货到付款结算单供店铺查询。平台后台负责跨店商品维护、促销和打款。迁移后给后端运行账号授予新表所需的查询权限，并按既有最小授权方式允许对 `merchant_staff` 取事务行锁（PostgreSQL 要求至少一列 UPDATE 权限）。账号、店铺和商品的后台开通操作应使用单独的受控身份。
