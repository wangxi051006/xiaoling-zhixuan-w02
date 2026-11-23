# 校零智选骑手端（rider-app）

Trusted Web Activity 壳，加载骑手端网页（`web/rider.html`）。本目录只放 Android 壳、图标与联调脚本；界面、接口和测试都在仓库的 `web/` 与 `backend/`。

## 界面结构（2026-11-22 改版）

底部三段：**订单 / 消息 / 我的**。

- 订单：顶部站点与容量 → **任务大厅 / 待取货 / 配送中** 三个分段 → 楼宇 / 付款方式 / 排序筛选 + **批量抢单 / 批量取货 / 批量送达** → 楼宇或任务号搜索 → 任务卡片 / 空态插画 + 刷新列表 → 右下角刷新悬浮球。
- 消息：站内消息、单条已读、全部已读。
- 我的：**今日单量 / 今日收入**、**工作状态**（上线/下线，状态来自服务器）、**钱包**（收入明细 + 日期切换）、**订单**（历史配送）、**排行榜**（同站点骑手当日单量榜）。
- 登录：本地测试登录 + 员工登录 / MFA（不提供自助注册）。

## 两张参考图的映射

| 参考图 | 采用 | 未采用 / 原因 |
| --- | --- | --- |
| 订单页 | 任务大厅 / 待取货 / 配送中、批量操作、筛选、搜索、卡片、空态插画、底部导航 | 「新任务」分段（没有独立的新任务来源）、「中转站 / 商户」筛选（`LobbyTask` 契约没有这两个字段）、「扫码接单」（没有扫码能力，该位置做成刷新） |
| 我的页 | 今日单量 / 今日收入、工作状态、钱包、订单、排行榜 | 箱子 / 保障 / 评价 / 帮助 / 反馈 / 设置按要求去掉；提现（平台结算流程里没有骑手提现接口） |

## 相关文件

| 位置 | 作用 |
| --- | --- |
| `web/src/rider/entry.ts` | 整页（Vue `h()` 渲染），保留轮询、接单/取货/送达/退回、批量、消息已读、收入查询 |
| `web/src/rider/app.css` | 移动端样式（蓝色顶栏、卡片、分段、底部导航、空态插画、安全区） |
| `web/rider.html` | `viewport-fit=cover`、`theme-color` |
| `rider/src/main/res/values/colors.xml` | 状态栏 / 导航栏 / 启动背景色 |
| `backend/internal/fulfillment/query.go`、`order_limits.go` | 新增两个只读接口 |
| `tools/ui-check.mjs` | 界面回归（固定数据 + 截图） |
| `tools/dev-emulator.ps1` | 模拟器/真机联调：起 API + 端口反向 |

## 新增接口

- `GET /rider/leaderboard`（可选 `date`）→ `{ local_date, entries: [{ rank, rider_id, display_name, orders, me }] }`，同站点按当日 `rider_earnings` 的送达单量排名，最多 100 人，不返回他人收入。
- `GET /rider/availability` → `{ available }`，此前只有 `PUT`，界面只能显示「状态待确认」。

批量取货没有批量命令，界面按顺序逐单调用 `POST /rider/tasks/{id}/pickup`；批量接单/送达走 `/rider/task-batches/claim|deliver`。

数据口径：今日单量 = 当日 `rider_earnings` 条数；今日收入 = 当日 `rider_earnings.amount_fen` 合计（含货到付款提成）。

## 验证

```bash
cd web && npx tsc --noEmit && npm run contract && npm run build
cd ../backend && go build ./... && go vet ./... && go test ./internal/fulfillment/
cd ../web && BROWSER_EXECUTABLE="C:/Program Files/Google/Chrome/Application/chrome.exe" \
  node --test tests/entries.e2e.mjs tests/identity.e2e.mjs tests/p06-roles.e2e.mjs tests/p06-recovery.e2e.mjs
cd ../apps/rider-app && node tools/ui-check.mjs
```

最近一次结果：typecheck / contract（90 operations）/ build 通过；Go build、vet、包测试通过；四个 e2e 共 44 项全过；`ui-check` 18 项全过、无控制台错误、390/360/1440 无横向溢出。

## 模拟器 / 真机联调

`rider.localhost` 在 Android 上解析到设备自己的回环地址，主机上的开发服务器不在设备里，直接装包会得到 `ERR_CONNECTION_REFUSED`。接上设备后执行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File apps/rider-app/tools/dev-emulator.ps1
#   -WebPort 5174 -ApiPort 8080 -SkipApi
```

脚本会检查设备、按当前代码编译并启动本地 API（`RIDER_ORIGIN=http://rider.localhost:<WebPort>`，其余端 `http://*.localhost:5173`），并设置 `adb reverse tcp:<WebPort> tcp:<WebPort>`。APK 的 `RIDER_ORIGIN` 必须与设备实际访问的来源完全一致（含端口）。

也可以改用模拟器别名 `10.0.2.2`：`RIDER_ORIGIN=http://10.0.2.2:5174 ALLOW_LOCAL_ORIGIN=1 gradle -p apps/rider-app :rider:assembleDebug`，同时把后端 `RIDER_ORIGIN` 改成 `http://10.0.2.2:5174`。真机继续用 `adb reverse`；正式包用已部署的 HTTPS 域名。

## 截图

`evidence/ui-redesign/`：`01`–`15` 为固定数据（有任务、有排行）下的界面，由 `tools/ui-check.mjs` 生成，可随时重跑覆盖：

| 文件 | 内容 |
| --- | --- |
| `01-orders-hall.png` | 任务大厅（3 个可接任务、容量、批量抢单） |
| `02-orders-batch-selected.png` | 勾选两单后的批量状态 |
| `03-pickup.png` | 待取货分段 |
| `04-pickup-detail.png` | 当前任务卡（确认取货） |
| `05-delivering-cod-detail.png` | 配送中 · 货到付款详情（收款勾选 + 应收金额） |
| `06-delivering.png` | 配送中分段 |
| `07-me.png` | 我的（今日单量 / 今日收入 / 工作状态 / 钱包 / 订单 / 排行榜） |
| `08-work-status.png` | 工作状态展开面板 |
| `09-ranking.png` | 排行榜（我在第 3 名） |
| `10-wallet.png` | 钱包（收入明细 + 日期） |
| `11-history.png` | 我的订单（历史配送） |
| `12-messages.png` | 消息 |
| `13-orders-360.png` / `14-me-360.png` | 360 宽 |
| `15-orders-desktop.png` | 1440 宽（页面保持 460 宽移动布局居中） |

真实接口与模拟器上的效果用 `tools/dev-emulator.ps1` 起本地环境后自行核对：空数据时任务大厅显示插画空态、我的显示 `0 / ¥0` 与服务器确认的工作状态。
