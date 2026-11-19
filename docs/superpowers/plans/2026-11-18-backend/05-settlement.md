# P04 · 货到付款、余额账本与对账实施

> G0 后独立执行。写域 `backend/internal/settlement/{http.go,cod.go,wallet.go,refund.go,reconcile.go,settlement_test.go,settlement_pg_test.go}`、`backend/cmd/balance-import/main.go` 与 `040_001_settlement.sql`。

[总计划](./00-master.md) · [契约](./10-contracts.md) · [验收](./08-acceptance.md)

## 执行步骤

- [ ] 建渠道测试：`COD` 以站点开关允许，`BALANCE` 生产默认关闭，`WECHAT` 永远禁用并显示待接入；直接调用 `CreateSettlement` 也兜底拒禁用方式，历史退款路径不读取新余额开关。金额从 `OrderMoney` 服务端快照取，不接受骑手/顾客提交权威金额。
- [ ] 建 COD 真实 PG 用例：创建应收未收费；同订单两个收款命令只产生一笔；撤销未收为 `VOID`；财务按骑手/日期制单，同一笔收款最多属于一个批次；另一财务账号复点，少缴进入 `DISCREPANCY` 并保留差额，重复复点不造流水。`cod.go` 记录交接金额、骑手、时间、对账批次和复点人；后续差异处理追加更正证据，不覆盖原报告。订单与骑手归属由 P05 校验，单事务收款后才完成订单。
- [ ] 建余额测试：两笔订单争抢同一钱包最后一份余额，只允许一笔；扣款后事务回滚金额和流水一同消失；退款重放只追加一次，余额不负；关闭开关仍可退。在 `refund.go` 提供 `FinalizeReturnSettlement`，运营验收未履约退回后同事务退原余额，未收 COD 撤销应收；与其他退款共享订单唯一保护。`wallet.go` 加锁更新余额与不可变流水，`reconcile.go` 校验期初＋导入/扣退净流水＝期末及每订单的原账户。
- [ ] 写余额受控导入 CLI：先对合成批次验证干跑不写入、重复批次不生效、重复外部业务引用拒绝、无审批编号/校验摘要/范围异常拒绝。上线仅由批准的受限执行人按已审批次运行，导入清单哈希与总额入账；批准人和执行人不同，过程线下复核并锁定输入版本。普通 API 无充值、发放、提现、转账权限；未确定余额来源不启用生产开关。
- [ ] 建人工全额退款状态：运营发起 → 不同财务账号审批。全额余额退原钱包且审批事务内完成；COD 审批仅进入 `REFUND_PENDING`，财务线下实际退款后填写唯一凭证引用方可记 `REFUNDED`，未收 COD 不退款。COD 已收金额仍参与原收款上缴，不自动从骑手应缴抵扣。并发审批/兑付与重复请求必须唯一；人工日对账贯通应收、已收、批次、差异、线下退款和余额流水。
- [ ] 回归管理/财务权限与测试账本，用假订单 ID 的模块事务 fixture 将包独立测过；G2 再证明联合订单写入与 P05 事务回滚。提交脱敏对账样本，不保存任何真实账单。

**命令：** 在 `backend/` 执行 `go test ./internal/settlement/... ./cmd/balance-import/...`、`go test -tags=integration ./internal/settlement/...`。**证据：** `PAY-01/02/03/04/05`；真实余额另行放行。
