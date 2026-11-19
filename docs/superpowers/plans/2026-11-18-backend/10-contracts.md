# 冻结契约 · G0 基准

[总计划](./00-master.md) · [架构](../../specs/2026-11-18-backend-design.md)。本页是 P00 生成机器契约的基准；只由总控合并变更。本页不代表接口已经上线。

## 1. 通用

- API 根 `/api/v1`；JSON UTF-8，货币只接受/返回 `*_fen` 整数，币种 `CNY`，ID 不透明字符串，时刻为 RFC 3339，站点解读 `Asia/Shanghai`。生产只存在一个站点，站点上下文从服务端绑定，不读客户端声称的角色。接口未列出的角色默认拒绝。用户越权读对象返回 `404`，没有登录 `401`，缺动作权限 `403`，状态/并发冲突 `409`，格式错误 `400`，限频 `429`。
- 错误 `{ "code": "SLOT_FULL", "message": "该时段已满", "request_id": "..." }`。业务状态冲突码固定：`QUOTE_CHANGED`、`SLOT_FULL`、`SLOT_EXPIRED`、`OUT_OF_STOCK`、`PAYMENT_METHOD_DISABLED`、`INSUFFICIENT_BALANCE`、`COD_ACTIVE_LIMIT`、`IDEMPOTENCY_CONFLICT`、`TASK_ALREADY_CLAIMED`、`INVALID_STATE`。列表 `items/next_cursor`，默认 20 最大 100；浏览器只重试可安全读、或同幂等键写入。
- 所有带副作用的交易命令接 `Idempotency-Key`，作用域 `(actor_id, command, key)`，参数摘要排除请求 ID；同参返回第一次逻辑结果，异参 `IDEMPOTENCY_CONFLICT`。记录与业务同事务，不缓存回滚；另用主键、唯一约束和前序状态保护兜底。OTP 请求独立限流与一次性消费。普通配置更新用版本号/条件更新，重复 `409`。
- 客户 `CUSTOMER`、骑手 `RIDER`、运营 `OPERATOR`、财务制单或复点 `FINANCE`、仅账号/角色管理 `SECURITY_ADMIN`；具多角色的账号不得同时在**同一**敏感业务担任两位操作者。后台对每项写操作校验站点、对象归属、前序与原因，特权动作要求近期 MFA。审批和线下执行经不同字段/事件留痕。

## 2. API 分组（P00 写 OpenAPI）

| 所有者 | 方法及路径（均相对根） | 约定 |
| --- | --- | --- |
| P01 | `POST /auth/otp/request`、`POST /auth/otp/verify`、`GET /auth/staff/start`、`GET /auth/staff/callback`、`POST /auth/logout`、`GET /me` | OTP 用一次性 challenge；员工的 issuer、nonce/state/PKCE 和受信回跳固定。`/me` 返回角色可见字段和 CSRF 请求值。 |
| P01 | `GET/POST /me/addresses`、`PATCH/DELETE /me/addresses/{id}`；`GET/PATCH /admin/riders/{id}`、`GET /admin/riders` | 地址可软删除，订单快照不变；骑手管理仅运营权限，绑定权限交安全管理员。 |
| P02 | `GET /catalog/products`、`GET /catalog/products/{id}`；`GET/POST /admin/products`、`PATCH /admin/products/{id}`、`PUT /admin/products/{id}/promotion`、`POST /admin/inventory/adjustments` | 后台管理 SKU、阈值、在售；调整只传 delta、预期版本、原因。 |
| P03 | `GET /delivery/slots`；`GET/PUT /admin/slots/{id}`、`GET /admin/slots`、`POST /admin/slots/batches` | 返回 `starts_at/ends_at/dispatch_at/accepting`；前台不需要知道内部容量计数。生成批次明确传本地营业区间及容量，重放不得重复生成相同窗口。 |
| P04 | `GET /payment-methods`、`GET /me/wallet`；`GET /admin/cod/receipts`、`POST /admin/cod/batches`、`POST /admin/cod/batches/{id}/verify`；`GET /admin/refunds`、`POST /admin/refunds/{id}/approve`、`POST /admin/refunds/{id}/record-cod-payout` | `methods[{code,enabled,label}]`；`WECHAT` 禁用。`/me/wallet` 只看本人；无前台余额发放、充值、提现或秘密后门。退款 HTTP 编排需调用 P05 的订单校验事务；P04 不直接改订单。 |
| P05 | `POST /checkout/quote`、`POST/GET /orders`、`GET /orders/{id}`、`POST /orders/{id}/cancel`、`POST /orders/{id}/urge` | 提交体不含金额；报价 `{lines:[{sku_id,qty}],address_id,delivery_mode,slot_id?,payment_method}`，响应 `{quote_id,valid_until,slot_id,lines,total_fen,payment_method}`，下单仅 `{quote_id,note?}` 外加幂等键；报价绑定本人，快照与占位不是一回事。 |
| P05 | `GET /admin/orders`、`GET /admin/orders/{id}`、`POST /admin/orders/{id}/{prepare,dispatch,reclaim,cancel,restock,return-inspect,refund}`、`GET /admin/dashboard` | `prepare` 标记已备妥并记分拣结果；`dispatch` 创建真实任务；回收/取消/退回有原因。未交接退款走取消策略；已送达可全退并保留履约状态。 |
| P05 | `PUT /rider/availability`、`GET /rider/tasks`、`GET /rider/tasks/{id}`、`POST /rider/tasks/{id}/{claim,pickup,deliver,return}`、`GET /rider/history`、`GET /rider/earnings`、`GET /rider/messages`、`POST /rider/messages/read` | `deliver` 对 COD 接收 `cod_received=true` 作为骑手作业确认，金额取订单，不由前端填；拒付走 `return`。本人历史和消息不泄露其他订单联系方式。 |

大括号含逗号为简写，OpenAPI 逐一路径展开。`payment-methods` 由配置和账号类型决定，不是唯一强制入口；按请求重新校验。`SCHEDULED` 支付方式创建后不可换；换方式重报价下单。禁用渠道不影响历史退款、上缴和交接。

## 3. Go 与 PostgreSQL 边界

P00 在 `backend/internal/contract/ports.go` 固化以下端口族（共享 DTO 放 `types.go`，时钟 `Clock.Now() time.Time`）。领域 HTTP 可以自行解析与映射；交易写方法接受调用者的 `pgx.Tx`，不得自开事务或提交。在 G0 把以下签名的 DTO 字段与 SQL ID/错误映射落实为 Go 编译契约。

| 端口 | 事务方法 / 入参与责任 |
| --- | --- |
| `IdentityPort` | `ResolveAddress(ctx, tx, actorID, addressID) (AddressSnapshot,error)`；`AssertRider(ctx,tx,actorID,stationID) error`。环境权限由 actor/scope 中间件校验，资产读取校验归属和服务楼栋。 |
| `CatalogPort` | `Price(ctx,tx,[]OrderLineInput,at) (PricedLines,error)`；`Reserve(ctx,tx,orderID,PricedLines)`、`Consume(ctx,tx,orderID)`、`Release(ctx,tx,orderID)`、`Restock(ctx,tx,orderID,[]RestockLine)` 返回 `error`。库存同物理项合计，变更自带审计上下文。 |
| `SchedulePort` | `SelectSlot(ctx,tx,stationID,mode,requestedSlotID,now) (SlotSnapshot,error)`；`ReserveSlot(ctx,tx,slotID,orderID,mode,now)`、`ReleaseSlot(ctx,tx,orderID,now)` 返回 `error`；后者仅由订单按资格调用。提交重校必须是原报价窗口，不自动选其他窗口。 |
| `SettlementPort` | `CreateSettlement(ctx,tx,OrderMoney)`；`RecordCODCollection(ctx,tx,orderID,riderID)`；`CancelSettlement(ctx,tx,orderID)`；`FinalizeReturnSettlement(ctx,tx,orderID)`；`RequestRefund(ctx,tx,RefundRequest)`；`ApproveRefund(ctx,tx,RefundApproval)`；`RecordCODPayout(ctx,tx,CODPayout)`，均返回 `error`。`CancelSettlement` 用于未交接整单取消，`FinalizeReturnSettlement` 用于运营验收未送达退回；两者按资格退原余额或撤销未收 COD，并共享订单退款唯一键。人工退款前由调用方验证订单状态。 |

共享根表归 P00：`stations`、`idempotency_keys`、`audit_events`、`outbox_jobs`。P01 `accounts/sessions/addresses/role_bindings`；P02 `products/skus/inventory/stock_reservations/inventory_movements`；P03 `delivery_slots/slot_reservations`；P04 `settlements/cod_receipts/cod_remittance_batches/wallets/wallet_entries/refunds`；P05 `quotes/orders/order_items/order_events/delivery_tasks/rider_earnings/notifications`，P05 统一补跨包 FK。P04 的 HTTP 退款批准/兑付由总控接到 P05 校验的命令，财务对账批次可独立执行。

锁顺序：幂等记录/本人活跃 COD 守卫（新单）或现有订单 → 窗口 → 库存 ID 升序 → 结算/钱包 → 配送任务与收入；任何命令只获取需要的子集，不逆序。骑手上线/停用与抢单依赖同一校验/约束；任务唯一约束与唯一收入业务键是最后保护。限并发后台用短租约，超时重新领取，处理时重读订单，禁止用过期任务覆盖终态。

## 4. 终态与展示

`SCHEDULED/PREPARING/READY_FOR_CLAIM/AWAITING_PICKUP/IN_DELIVERY/DELIVERED/CANCELED/RETURNING/RETURNED` 是履约枚举；`UNSORTED/SORTED/RESTOCK_PENDING/RESTOCKED` 用作备货/归架标志。预约触发未备货时 `PREPARING` 不等于已经备妥。`COD_UNCOLLECTED/COD_COLLECTED/BALANCE_PAID/VOID/REFUND_PENDING/REFUNDED` 是资金枚举；COD 上缴 `NOT_REMITTED/IN_BATCH/VERIFIED/DISCREPANCY` 独立，不由资金标签推导。用户看到实际收款、退款处理和送达状态，财务可追收款及上缴差额。没有收款却取消/退回只进入 `VOID`，不是 `REFUNDED`。
