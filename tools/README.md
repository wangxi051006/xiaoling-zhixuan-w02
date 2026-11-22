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
| Vite | `127.0.0.1:5174` | 端口必须与 Android 端 `RIDER_ORIGIN` 的端口一致 |
| Go API | `127.0.0.1:8080` | 可用 `-ApiPort` 改 |
| PostgreSQL | `127.0.0.1:5432`，库 `campus` | 服务名 `pgsql-local`，可用 `-PgService`/`-DbName` 改 |
| 入口 | `http://rider.localhost:5174/rider.html` | 本地测试登录 `123456` / `123456` |

后端环境变量由脚本统一注入：`APP_ENV=development`、`ALLOW_TEST_AUTH=true`、三个 `*.localhost` origin、
`COD/ORDERS/DISPATCH/BALANCE_ENABLED=true`。测试登录只在 loopback 数据库 + `<entry>.localhost` origin 下可用。

## 常用参数

| 参数 | 作用 |
| --- | --- |
| `-ApiPort 8082` | 换后端端口（`DEV_API_ORIGIN` 会跟着变） |
| `-WebPort 5175` | 换 Vite 端口（需同步改 APK 的 `RIDER_ORIGIN`） |
| `-DbName campus_devstack` | 用独立数据库，避免和其他会话/手动实验的数据互相污染 |
| `-Force` | 端口被别的进程占用时接管（会 kill 对方进程，谨慎） |
| `-SkipMigrate` | 跳过迁移（数据库已迁移过时可用） |
| `-WithEmulator` | 没有连接中的设备时自动冷启动 AVD 并做 `adb reverse` |

## 状态与日志

- 状态目录：仓库根 `.dev-stack/`（已加入 `.gitignore`）——`api.pid`、`web.pid`、`campus-api.exe`、`api.out.log`、`web.out.log`
- 脚本按 PID 文件停服务；遇到不是自己启动的进程只提示，不动手（除非 `-Force`）
- 后端编译缓存：只改了 `*.go` / `*.sql` 才会重新编译

## Android 端

APK 里的 `RIDER_ORIGIN` 必须和这里的 Vite 地址一致（`http://rider.localhost:<WebPort>/rider.html`），
构建命令见 `apps/README.md`。模拟器每次重启后都要重做 `adb reverse`；`start` 会顺手补上。
