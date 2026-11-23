# 校零智选

本仓库已整理为**三端本地运行的最小集**：商家端、骑手端在 Android Studio 运行，用户端可装 `apps/user-app` 的 APK 或在微信开发者工具里跑小程序，三端共用一套 Go 后端与 PostgreSQL。原型、验收证据、历史预览和构建中间产物均已移除。

| 模块 | 源码目录 | 运行方式 |
| --- | --- | --- |
| 01 商家端 | [apps/merchant-app](apps/merchant-app/README.md) | Android Studio |
| 02 骑手端 | [apps/rider-app](apps/rider-app) | Android Studio |
| 03 用户端 | [apps/user-miniprogram](apps/user-miniprogram) | 微信开发者工具 |
| 04 用户端 APK | [apps/user-app](apps/user-app/README.md) | Android Studio / `tools\build-apk.cmd` |
| 前端页面 | `web/`（`src/merchant`、`src/rider`、`src/user`、`src/shared`） | Vite |
| 后端 | [backend](backend/后端.md) | Go + PostgreSQL |
| 接口契约 | [contracts](contracts/README.md) | `node contracts/generate.mjs` |
| 本地联调脚本 | [tools/dev-stack](tools/README.md) | `tools\dev-stack.cmd` |

`web/admin.html` 是运营后台页面，仍在 `web/` 内，不在本次保留目标中。`deploy/`（Caddyfile、`env.example`、静态拆分脚本）保留为正式环境部署模板。

## 本地联调端口约定

`tools\dev-stack.cmd start` 一条命令拉起 PostgreSQL、Go API 和两个 Vite。端口与 APK、小程序里写死的 origin 一一对应，任一端改端口都要同步改客户端，否则后端按入口 origin 拒绝并返回 404：

| 服务 | 地址 | 说明 |
| --- | --- | --- |
| Go API | `http://127.0.0.1:8080` | 四个入口 origin 由 dev-stack 注入（user 5194；rider/merchant/admin 5173） |
| Vite | `http://127.0.0.1:5173` | 商家端、骑手端、运营端页面；商家/骑手 APK 的 origin 端口即此值 |
| Vite | `http://127.0.0.1:5194` | 用户端页面；`apps/user-miniprogram/storefront.js` 指向它 |

状态与日志在 `.dev-stack/`（`api.pid`、`web.pid`、`user-web.pid` 与对应的 `*.log`）。端口被别的进程占用时脚本只提示不接管，加 `-Force` 才会 kill。

## 商家端 / 用户端 / 骑手端（Android）

1. 一条命令打包：仓库根目录 `tools\build-apk.cmd`（或 `powershell -ExecutionPolicy Bypass -File tools/build-apk.ps1 -Target user`）。也可以逐个在 Android Studio 打开 `apps/merchant-app`、`apps/user-app` 或 `apps/rider-app`；也可以双击 `apps/merchant-app/本地打开-AndroidStudio.bat` 启动，它会先注入 `MERCHANT_ORIGIN` / `RIDER_ORIGIN` 再拉起 Studio。
2. 环境要求：JDK 17、Gradle 8.11.1、Android SDK Platform 36。`sdk.dir` 已写入各工程的 `local.properties`。
3. 构建 debug 包：

   ```powershell
   gradle -p apps/merchant-app :merchant:assembleDebug
   gradle -p apps/rider-app :rider:assembleDebug
   gradle -p apps/user-app :user:assembleDebug
   ```

   已生成的包：`apps/merchant-app/merchant/build/outputs/apk/debug/merchant-debug.apk`、`apps/rider-app/rider/build/outputs/apk/debug/rider-debug.apk`。中间产物已清理，首次构建会重新生成。
4. APK 内的 origin 为 `http://merchant.localhost:5173/merchant.html` 与 `http://rider.localhost:5173/rider.html`。装到模拟器或真机后执行 `adb reverse tcp:5173 tcp:5173`，再从桌面图标进入。

## 用户端（微信开发者工具）

1. 微信开发者工具直接导入 `apps/user-miniprogram`。AppID 已写在 `project.config.json`，`project.private.config.json` 已关闭域名校验。
2. `storefront.js` 的 `storefrontUrl` 为 `http://user.localhost:5194/user.html`；`tools\dev-stack.cmd start` 已经在 5194 起好用户端 Vite，若改 `-UserPort` 需同步改这个文件。

3. 小程序用 `web-view` 打开该地址，接口请求经 Vite 的 `/api/v1/` 代理转发到 `http://127.0.0.1:8080`，后端保持运行即可登录。

## 后端与数据库

`tools\dev-stack.cmd start` 会一起拉起本机 PostgreSQL 服务（`pgsql-local`，库 `campus`）、Go API 与 Vite，并注入 `APP_ENV=development`、`ALLOW_TEST_AUTH=true` 和四个 `*.localhost` origin（user 5194；rider/merchant/admin 5173）。三端本地测试账号密码都是 `123456`，只在回环数据库加 `*.localhost` 入口下可用。参数和日志位置见 [tools/README.md](tools/README.md)。

## 校验

| 位置 | 命令 |
| --- | --- |
| `web/` | `npm run typecheck`、`npm test` |
| `backend/` | `go test -mod=readonly ./...` |
| 项目根目录 | `node contracts/generate.mjs --check` |

## 本次精简说明

- 已删除：原型与演示（`demo/`、`styles/`、`index.html`、`校零智选-手机演示.html`）、验收证据（`evidence/`、`docs/`、`apps/*/evidence`）、历史预览（`.dsh-resume/` 下除活动后端外的目录）、构建缓存与中间产物（`.dsh-verify`、`build/`、`web/dist`、`web/dist-merchant`）、CI 与发布流程（`.github/`、`deploy/ci`）。
- 其中纳入 git 的文件仍可用 `git checkout -- <路径>` 取回；`DESIGN.md`、`design-tokens.css` 未纳入 git，已保留。
