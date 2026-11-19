# P02 · 商品、定价与库存实施

> G0 后独立执行。写域 `backend/internal/catalog/`、`backend/migrations/020_001_catalog.sql`。不修改订单、公共端口或页面。

[总计划](./00-master.md) · [契约](./10-contracts.md) · [验收](./08-acceptance.md)

## 执行步骤

- [ ] 写 `pricing_test.go` 涵盖 1/3 件装、促销时间区间、最高满足阶梯、非叠加、边界金额/舍入与非法数量。在 `pricing.go` 实现整数分与基点；多个销售规格映射同一物理库存项，三件装消耗三单位。报价返回 SKU/促销/价格版本快照，管理员改价和上架变更要递增版本。
- [ ] 写 `inventory_pg_test.go`：两事务抢最后一份只成功一次；锁顺序按物理库存 ID；预占量不能变负或超过实存；三件装与单件共享那一份实存。在 `inventory.go` 与 020 迁移建立行锁/条件更新、非负约束、按订单/物理 SKU 唯一预占、不可变流水。所有交易端口使用外部传入 `pgx.Tx`。
- [ ] 落地 `Reserve/Consume/Release/Restock`：预占仅增 reserved；取货同时减 on_hand/reserved；未交接释放仅减 reserved；退货仅在验收时加 on_hand 且按明细上限校验。前序与唯一键阻止重复取、退或释放。实测事务故意回滚后，可售量和流水均未发生变化；重试返回正确幂等结果。
- [ ] 在 `products.go`、`http.go` 做用户目录与后台商品/促销/增减库存接口；后台调整记 delta、原因、操作者和版本，不从前端接受 `reserved`。测试下架不出新报价、旧单仍可正常履约；低库存预警由真实库存计算；后台减少库存不可侵占预占。
- [ ] 提交 P02 测试证据与端口编译结果；运行只含 G0+020 的空库回归，再进入 G1 全库迁移。

**命令：** 在 `backend/` 执行 `go test ./internal/catalog/...`、`go test -tags=integration ./internal/catalog/...`。**成功证据：** `TRADE-01/03`，真实 PG 最后一份和回滚守恒。
