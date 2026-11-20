# 校零智选

本仓库包含 2026-11-19 的 **W02 待审版**源码。目录说明与验收证据不属于 [258 文件源码校验清单](docs/evidence/launch/W02-freeze-controller-2026-11-19/source-files.json)。

**这是源码快照，不是正式发布。** 工程状态为 `REVIEW_READY`，发布冻结仍为 `BLOCKED`。原始验收日志和本机运行文件不随源码公开。

| 模块 | 源码目录与说明 | 程序入口 |
| --- | --- | --- |
| 01 用户端 | [web/src/user](web/src/user/用户端.md) | [web/user.html](web/user.html) |
| 02 配送端（骑手端） | [web/src/rider](web/src/rider/配送端.md) | [web/rider.html](web/rider.html) |
| 03 管理端 | [web/src/admin](web/src/admin/管理端.md) | [web/admin.html](web/admin.html) |
| 04 后端 | [backend](backend/后端.md) | [backend/cmd/api/main.go](backend/cmd/api/main.go) |

- `web/src/shared/`、`web/tests/`：三端共用的 API 客户端、入口逻辑和测试。三端独立入口，共用一套 Vite 配置与依赖锁文件。
- [接口契约](contracts/README.md)包含 API 定义和类型生成；[部署文档](deploy/README.md)包含部署模板、打包与验证。
- [静态设计参考](index.html)及 `demo/`、`styles/` 是主版本内保留的原型，不是实际业务前端，不进入发布包。

## 本地开发与验证

在 `web/` 内执行 `npm ci` 后，用 `npm run dev` 启动 Vite，访问终端所示地址的 `/user.html`、`/rider.html`、`/admin.html`。构建产物 `web/dist/` 由 `npm run build` 生成。后端需要本地配置和 PostgreSQL；见[后端说明](backend/后端.md)。

### 三端本地测试登录

三端测试账号及密码都是 `123456`，从各页面的「本地测试登录」进入。三端使用不同身份和会话；该入口默认关闭，生产环境禁止启用。

准备独立的本机 PostgreSQL 数据库，`DATABASE_URL` 与 `MIGRATION_DATABASE_URL` 指向该测试库（使用回环 IP）。在 `backend/` 用 PowerShell 配置并启动：

```powershell
$env:APP_ENV = 'development'
$env:ALLOW_TEST_AUTH = 'true'
$env:HTTP_ADDR = '127.0.0.1:8081'
$env:USER_ORIGIN = 'http://user.localhost:5174'
$env:RIDER_ORIGIN = 'http://rider.localhost:5174'
$env:ADMIN_ORIGIN = 'http://admin.localhost:5174'
go run ./cmd/api migrate
go run ./cmd/api
```

另开终端在 `web/` 执行以下命令，再用 Chromium 浏览器打开 `http://user.localhost:5174/user.html`、`http://rider.localhost:5174/rider.html` 或 `http://admin.localhost:5174/admin.html`：

```powershell
$env:DEV_API_ORIGIN = 'http://127.0.0.1:8081'
npm run dev -- --port 5174 --strictPort
```

不要将该预览通过公网代理或隧道暴露，也不要连接真实数据或邮件服务。恢复普通登录时，用 `ALLOW_TEST_AUTH=false` 及各端正式 HTTPS 来源重启后端；之前的测试会话将不再有效。真实浏览器验证：在预览运行时设置 `LOCAL_TEST_WEB_PORT=5174`，执行 `npm run test:e2e`。

| 检查位置 | 命令 |
| --- | --- |
| `web/` | `npm run typecheck`、`npm test`、`npm run build` |
| `backend/` | `go test -mod=readonly ./...` |
| 项目根目录 | `node contracts/generate.mjs --check`、`node deploy/ci/gates.mjs wiring .` |

`.dsh-resume/` 只保留当前本地预览进程使用的可执行文件、静态产物、运行依赖和日志，不是第二份开发源码。停掉该预览后，可移除该目录；日常开发从上表入口进入。
