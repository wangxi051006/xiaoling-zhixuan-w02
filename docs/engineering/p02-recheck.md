# P02 本轮本地验收与修复记录

日期：2026-11-18，Asia/Shanghai。结论：**P02 本地验收通过**。本轮重新执行既有实现测试、新增缺陷红绿验证及独立副本回放；历史 PASS 和已有代码不作为本轮通过证据。此结论限定商品库存域，不签发 G1，不启动 P05，不派发其他包，不代表预发或上线验收。

## 基线与施工边界

- 开发根 `D:/校零/校零智选-开发`；原型 `D:/校零/校零智选` 只读。没有初始化 Git、提交或推送。Git 仅作为无仓库文件差异和补丁应用工具。
- P00 复核补丁实测 SHA-256：`1831037543955abf19a543416112bc75971652cb7fd304b78f7d943fd21c8250`；与指定摘要及 P00 清单一致。
- P01 复核增量实测 SHA-256：`19a4f8bdf8109f4047689d6eab87c068cc93c13b82f8a6d900baa1ddd6c00263`；P01 清单六个交付文件全部匹配。
- 既有 P02 补丁实测 SHA-256：`758f29a4445649c82eae1d835614d4b4c3ec18fd6f39437eddc800413356d29d`。保留全部旧补丁，没有重复应用。
- P00 清单与开发树仅有 P01 已记录的 `backend/go.mod`、`backend/go.sum`、`backend/internal/platform/migrate_pg_test.go` 三处既存差异；实际摘要与 P01 交接一致。没有覆盖共享依赖或把 P01–P04 叠加树当作纯 P00。
- 本轮起始独立副本：`D:/校零/.p02-recheck-20261118/start`；123 个文件的起始清单：同目录 `start-manifest.json`，SHA-256 `978178d0d0af04b356f8d87d716dbe0c07ff45e8eb69fd7509fb41a37385c65b`。排除 node_modules、dist、.git、.check。基线核对证据为 `baseline-check.json`。
- 本轮变更仅为 `backend/internal/catalog/{http.go,inventory.go,inventory_pg_test.go,recheck_test.go,recheck_pg_test.go,recheck_http_pg_test.go,recheck_identity_pg_test.go,recheck_inventory_pg_test.go}`、新增 `backend/migrations/020_002_reservation_digest.sql` 和本报告。其余起始文件逐字节不变；原有 SQL 迁移摘要未变。
- 已读取指定计划、架构、工程交接、OpenAPI/枚举/操作表、冻结端口及 catalog 全部代码测试；身份适配测试另只读参考 identity 和 010。核心 P02 SQL 不依赖 010/030/040/050 私有表；编译引用 identity HTTP 类型与数据库依赖分开核验。

## 缺陷、红灯与最小修复

证据根记作 `E = D:/校零/.p02-recheck-20261118`。下表每个红灯退出 1 且为预期业务断言失败；对应绿灯退出 0。没有先写生产修复再倒补缺陷用例。

| 缺陷 | 本轮确认的原因与修复 | 红灯 / 绿灯日志 | 关联 |
| --- | --- | --- | --- |
| P02-R01 促销自然变化的旧报价仍被接受 | Reserve 原来只检查版本、商品和规格。现在锁住库存后按 Store.Clock（缺省系统时钟）重新 Price，对比完整行快照及金额汇总；自然开始/结束变化也拒绝。Price 仍使用显式 at | red-reserve-validation / green-reserve-audit | TRADE-01 |
| P02-R02 变化的价格快照及异 SKU 同数量重放被接受 | 原来只按物理总数判断重放。新增 020_002 为预占保存完整报价的 SHA-256；按 SKU 排序归一化，同参允许行顺序变化，异参拒绝。新预占同时核验当前价格/规格/促销/版本快照 | red-reserve-validation / green-reserve-audit | TRADE-01/03 |
| P02-R03 后台写入没有审计事件 | 商品创建、普通更新、促销更新和库存调整均在原 HTTP 事务中追加服务端 actor/station/object/request_id 审计；调整含原因。幂等重放不重复审计；审计失败撤销业务与幂等记录 | red-audit-atomicity / green-reserve-audit | TRADE-03、SEC-01/03 |
| P02-R04 输入形状不符合冻结契约 | Go 默认 JSON 解码接受 optional:null、不同大小写字段和重复成员。解码前按实际请求类型递归验证精确成员名、重复成员及 null，仅 promotion 允许 null；保留原业务验证 | red-http-shape-infrastructure / green-http-shape-infrastructure | SEC-03 |
| P02-R05 公共路由缺数据库时返回 500 | 注册公共路由检查 Pool；后台检查 Pool/Authorize，缺失返回 503/NOT_READY，保持错误包络，无假成功 | red-http-shape-infrastructure / green-http-shape-infrastructure | SEC-03 |
| P02-R06 游标上限错用 200 | 对照冻结 Cursor 最大 128，补充游标专用上限 | red-cursor-limit / green-cursor-limit | SEC-03 |

日志名称均补 `.log`。`initial-unit.log`、`initial-integration.log` 记录修复前已有用例实际通过；新增负例随后捕获上述缺陷，未因已有测试绿灯直接验收。真实身份新夹具首次因带参数多语句不支持而失败（identity-adapter.log），改为分开执行夹具 SQL 后通过（identity-adapter-corrected-fixture.log）；这次夹具失败不计为产品缺陷红灯。

020_002 不回填猜测的历史请求。已有预占 request_digest 为 NULL 时，Reserve 明确返回 ErrStockState，不能宣称精确成功重放；Consume/Release/Restock/RestockCanceled 保持正常。升级测试实际创建旧版预占后应用新迁移，并重复取货验证不重复扣减。新增摘要只保存商品报价，不含身份或地址。

## 验收矩阵与证据索引

以下 PASS 均限定本轮 P02 本地执行。测试名省略 `Test` 前缀；完整结果见 `replay-integration.log` 及最终交付回放日志。未覆盖的系统能力另列 NOT RUN/BLOCKED，不能从本表推导整单通过。

| 编号 / 验收项 | 状态 | 实际证据与责任边界 | 关联 |
| --- | --- | --- | --- |
| Q01 单件/三件装映射同一物理库存、换算 | PASS | SharedInventoryRace；SamePhysicalAggregationAndConcurrentLastUnit；三件装占 3，单件占 1 | TRADE-01 |
| Q02 整数分/基点、单件舍入后乘数量 | PASS | PricingSelectsOneTierAndRoundsBeforeMultiplication：110 分按 1500 基点折扣，单件 94、两件 188；最大金额半价无溢出 | TRADE-01 |
| Q03 最高满足阶梯且不叠加 | PASS | 同一用例乱序阶梯 1/2/4，选择最高满足数量门槛 | TRADE-01 |
| Q04 开始包含、结束排除、传入时钟和自然到期 | PASS | 定价纳秒边界；PromotionClockBoundariesAndVersion；ReserveRejectsNaturallyExpiredPromotion，未使用 sleep | TRADE-01 |
| Q05 非法数量、重复/不可售 SKU、异常折扣、金额溢出 | PASS | PricingRejectsInvalidAmountsAndUnavailableSKUs；PriceRejectsDuplicateInactiveAndAggregateOverflow；PricingRejectsAbnormalDiscounts | TRADE-01、SEC-03 |
| Q06 冻结价格/规格/促销/版本快照完整 | PASS | 定价断言物理 ID、商品 ID、规格、数量、基点、促销、版本、单价和行总额；快照篡改九种负例 | TRADE-01 |
| Q07 改价、促销变更、上下架递增版本 | PASS | ProductChangeInvalidatesPriorQuote；PromotionClockBoundariesAndVersion；HTTPContract 普通配置重复旧版本 409 | TRADE-01 |
| Q08 旧报价状态和时间变化均拒绝 | PASS | 改价、下架、促销修改、自然开始/结束和金额/快照变化负例；只验证版本的旧行为已修复 | TRADE-01 |
| Q09 P02 与 P05 的报价责任区分 | PASS（边界核验） | P02 不建立 quote_id/有效期、身份绑定或订单；未来 P05 提交必须在同一事务使用共享时钟重新报价、校验报价有效期和原窗口、比较冻结快照并编排所有端口 | TRADE-01/03 |
| I01 真实 PG 争抢最后库存 | PASS | 两个已开始事务读取报价后到达屏障，再同时 Reserve；单件最后 1 单位和单件/三件装共享 3 单位均恰一成功 | TRADE-01 |
| I02 实存共享不超卖 | PASS | 竞争后断言 on_hand/reserved/流水；无 SQLite/mock | TRADE-01 |
| I03 预占非负、不超过实存、可售量一致 | PASS | 原始 SQL 约束负例；后台可售量为 on_hand-reserved；LowStock 读取真实计数 | TRADE-01/03 |
| I04 同请求多销售规格物理汇总 | PASS | 单件 1+三件装 1 需要 4，库存 3 拒绝；库存 4 只建一条物理预占及流水 | TRADE-01 |
| I05 多物理库存稳定锁顺序、反向输入 | PASS | ReservationsLockMultipleItemsInOneOrder：两个已开始事务、反向输入、库存各预占 2、流水 4，无死锁；额外十轮 | TRADE-01/03 |
| I06 中途失败所有库存/预占/流水回滚 | PASS | MultiItemMidOperationFailureRollsBackEverything：五操作在第二个物理项插入流水故障，断言 SQLSTATE P0001 及全部业务快照恢复 | TRADE-03 |
| I07 数据库独立拒绝非法状态 | PASS | reserved=-1、reserved>on_hand、负实存、无效版本/预占数量/终态数量/摘要长度，实际均得到 23514 | TRADE-01/03、SEC-03 |
| L01 Reserve 只增 reserved | PASS | 生命周期逐步断言 3/0 → 3/3，流水 1 | TRADE-03 |
| L02 Consume 同减实存和预占 | PASS | 3/3 → 0/0，重复取货计数不变 | TRADE-03 |
| L03 未交接 Release 只减预占 | PASS | LifecycleDetailsAndExactReplay、CallerPendingReservation：3/3 → 3/0 | TRADE-03 |
| L04 取货/取消竞争只能一个成立 | PASS | PickupAndCancellationExcludeEachOther：事务内屏障；一个成功、一个 ErrStockState；两种合法结果计数断言，重复十轮 | TRADE-03 |
| L05 分拣取消待归架保留预占、归架扣损耗 | PASS（库存侧） | 预占一直保留到 RestockCanceled，损坏 1 后 2/0；空损坏明细为 3/0。P02 无分拣订单状态，P05 必须阻止待归架阶段调用 Release | TRADE-03 |
| L06 Restock 前序及上限 | PASS | 取货前退货拒绝；数量超限、负/零明细、重复物理项、未知物理项拒绝；只回验收可售数 | TRADE-03、SEC-03 |
| L07 RestockCanceled 前序及明细 | PASS | 同类明细负例；在 RESERVED 状态释放整个预占，仅损耗减实存；不从已取货状态归架 | TRADE-03 |
| L08 归架/损耗/退货/取货/释放重试不重复计数 | PASS | StockRollbackAndReplay；LifecycleDetailsAndExactReplay；EmptyInspectionMeansZeroDamageOrZeroSellable；所有正向命令重复并核对流水数 | TRADE-03 |
| L09 同业务键异参不能成功重放 | PASS | Reserve 的相同物理总数不同 SKU、各快照变化拒绝；归架/退货改变明细拒绝；后台幂等异参 409 | TRADE-01/03 |
| L10 库存流水不可变 | PASS | 真实执行 UPDATE、DELETE、TRUNCATE，均 SQLSTATE 42501；失败后流水数不变 | TRADE-03、SEC-03 |
| L11 下架不出新报价，已有预占继续履约 | PASS | StockRollbackAndReplay、LifecycleDetailsAndExactReplay：下架后新 Price 拒绝，已有预占取货/释放/归架/退货仍可完成 | TRADE-01/03 |
| H01 七条冻结路由 | PASS | HTTPContract 覆盖两个公共读、后台列表/创建/更新/促销/调整；无分类维护路由 | SEC-01/03 |
| H02 请求体及字段白名单 | PASS | 未知字段、客户端 reserved、category、null、大小写错名、重复字段、嵌套字段验证；类型错误拒绝 | SEC-03 |
| H03 分页、空列表、金额/数量/版本/原因/幂等键 | PASS | 默认 20、最大 100、游标续页无重复遗漏；非法 limit/query/cursor、超长/缺失 key、非整数 delta、零 delta、非法原因拒绝 | SEC-03 |
| H04 普通配置版本控制 | PASS | 商品/促销递增版本；旧版本再次更新 409/INVALID_STATE | TRADE-01、SEC-03 |
| H05 命令同参/异参/失败后重试 | PASS | 创建/调整同键返回原逻辑结果，异参 409；审计和幂等结果故障后同键可重试成功 | TRADE-03、SEC-03 |
| H06 后台仅接受 delta/expected_version/reason | PASS | reserved 字段 400；Adjust 使用站点和服务端 actor；减少不能侵占已有预占 | TRADE-03、SEC-01 |
| H07 库存增加、版本、流水、审计事务一致 | PASS | AdjustmentRespectsReservationAndRecordsActor；HTTPWritesAuditAndRollsBackOnAuditFailure；IdempotencyCompletionFailureRollsBackAuditAndStock | TRADE-03、SEC-03 |
| H08 公开目录仅公开在售字段 | PASS | 只含 id/name/description/skus；无 inventory/version/on_sale/promotion/内部字段；下架 404 | SEC-03 |
| H09 低库存使用真实库存 | PASS | RealPaginationLimitsAndLowStock：3/0 时正常，reserved=3 后 LowStock=true；冻结响应没有 low_stock 字段，不自行扩字段 | TRADE-01 |
| H10 无冻结外的分类维护接口 | PASS | 固定创建分类 uncategorized；category 更新 400、分类路由 404；只保留冻结目录查询过滤 | SEC-03 |
| A01 服务端身份角色和站点 | PASS | WithRealIdentityHTTPRoleSiteOriginAndCSRF：注入既有 Authenticated，角色/站点来自真实 PG 会话；伪造 X-Role、X-Station-ID、Forwarded 等不授权 | SEC-01 |
| A02 客户、骑手、财务、安全管理员、匿名不能写 | PASS | 四类写路由 × 禁止角色/匿名矩阵；无/错误入口会话 401，已认证无角色 403 | SEC-01 |
| A03 跨站点对象拒绝 | PASS | 另一站点运营修改商品/促销/库存均 404；后台列表为空；没有越站审计写入 | SEC-01 |
| A04 写 Origin/CSRF | PASS | 缺失/null/错误 Origin、缺失/错误 CSRF 均 403；错误入口 Host 即使伪造 forwarded 仍 404；正确组合成功 | SEC-01/03 |
| A05 冻结近期 MFA 策略 | PASS（策略核验） | OpenAPI 七条 P02 x-recent-mfa 均 false；原授权替身断言 false，真实员工会话以 MFA 签发，16 分钟后普通配置写仍允许。未擅自改为 true | SEC-01 |
| A06 授权替身与真实适配区分 | PASS | 原 HTTPContract 仍只是契约/授权钩子测试；新增独立 000+010+020 真实 Session/Authenticated 测试，停用账号后 401 | SEC-01/03 |
| A07 实际三域、反代、应用启动 | NOT RUN | 域内 httptest 不能证明实际入口。由总控/P07/P08 后续验证，当前不修改 identity/bootstrap/启动接线 | SEC-01/03 |
| T01 六个冻结端口编译契合 | PASS | CatalogPort 编译断言，Price/Reserve/Consume/Release/Restock/RestockCanceled 实际执行 | TRADE-01/03 |
| T02 使用调用者 pgx.Tx | PASS | CallerTransactionVisibilityAndRollback 中创建/调整未提交商品，Price/Reserve/Consume/Restock 可见；ReleaseAndCanceledRestockReadPendingCallerReservation 验另两个端口 | TRADE-03 |
| T03 无自开/自提交交易事务 | PASS | 交易端口只读写参数 tx；HTTP 边界才由平台创建事务；独立连接看不到调用者未提交数据，回滚后无残留 | TRADE-03 |
| T04 库存、预占、流水一致 | PASS | 同事务回滚、第二物理项故障、重试唯一流水及总数核对 | TRADE-03 |
| T05 后台审计/幂等一致 | PASS | 审计 INSERT 故障和幂等完成 UPDATE 故障实际回滚库存/商品/流水/审计/key；重试成功 | TRADE-03、SEC-03 |
| T06 错误后调用者整体回滚 | PASS | Store 注释及本报告明确；测试使用调用者事务回滚，五端口中途故障不内部提交部分结果；禁止捕获错误后继续提交 | TRADE-03 |
| T07 不依赖 P03/P04/P05 私有表 | PASS | 核心夹具只加载 000/020，检查 accounts/sessions/delivery_slots/wallets/orders 均不存在；并发与回滚测试在此运行 | TRADE-03 |
| E01 HTTP 状态/错误枚举/包络 | PASS | ErrorsMatchFrozenContractAndRedactDiagnostics 直接读取冻结 ErrorCode；400/404/409/500，真实身份 401/403/404，缺设施 503；无自创枚举 | SEC-01/03 |
| E02 request_id、列表与空态 | PASS | 平台赋非空 request_id，错误仅 code/message/request_id；列表 items 数组、next_cursor 字符串或 null，空态 [] | SEC-03 |
| E03 缺授权适配器/数据库不可用 | PASS | MissingInfrastructureIsUnavailable：公共和后台缺 Pool/Authorize 返回 503/NOT_READY；无假成功 | SEC-03 |
| E04 数据库/SQL/内部诊断不外泄 | PASS | 任意内部诊断映射通用 INTERNAL_ERROR；故障注入 HTTP 500 不返回底层 SQL 诊断 | SEC-03 |
| E05 日志及交付隐私 | PASS | 真实身份动态 Cookie/CSRF 不在日志；业务标记不入请求日志；最终补丁/报告/验收日志扫描由交付清单记录。测试连接仅环境传递，不在交付物输出 | SEC-03 |
| E06 报价端口错误与未来 HTTP 映射 | PASS（边界核验） | Price/Reserve 是 Go 端口，返回 ErrInvalid/ErrUnavailable/ErrPriceChanged/ErrStockState 等；P02 七条 HTTP 不接订单提交。P05 须按冻结 API 映射订单报价冲突，不声明订单 HTTP 已通过 | TRADE-01 |
| M01 000+020 空 schema 独立重放 | PASS | 每个核心集成测试新随机 schema，只加载 000_001/020_001/020_002；无跨包私有表 | TRADE-01/03 |
| M02 重复执行不改历史和业务数据 | PASS | MigrationsStandaloneRepeatAndUniqueVersions：对 schema_migrations 全行（含 digest/applied_at）、商品、SKU、库存作前后快照；再执行两次完全相等 | SEC-03 |
| M03 全库版本唯一/原迁移摘要未变 | PASS | 嵌入全库迁移按前七位版本检查唯一；起始/最终摘要核验禁止改已有迁移 | SEC-03 |
| M04 核心测试不依赖 010/030/040 | PASS | 核心夹具迁移过滤及私有表不存在断言；真实 PG 并发/约束测试均用核心夹具 | TRADE-01/03 |
| M05 身份 HTTP 单独夹具 | PASS | stockFixtureWithIdentity 显式 true 才加 010；身份联调不作为核心独立性证明 | SEC-01 |
| M06 新迁移升级兼容与已存预占 | PASS | MigrationUpgradePreservesLegacyFulfillment：旧版预占数量/流水/既有迁移历史不变，NULL 摘要不伪装重放，仍可重复取货且只扣一次 | TRADE-03 |

## 必需命令、结果和证据

开发后端目录 `B = D:/校零/校零智选-开发/backend`；开发根 `R = D:/校零/校零智选-开发`。所有 Go 命令显式使用隔离 TEST_DATABASE_URL（缺库负例除外）；不使用应用/部署/生产连接。缓存沿用工作区既有缓存，不修改 go.mod/go.sum。

| 工作目录 | 实际命令 | 退出码 / 结果 | E 下证据 |
| --- | --- | --- | --- |
| B | go test -count=1 ./internal/catalog/... | 0；单测通过 | catalog-unit-final.log/json |
| B | go test -count=1 -tags=integration ./internal/catalog/... | 0；真实 PG 通过 | catalog-integration-final.log/json |
| B | go test -count=1 ./... | 0；全包单测通过，无测试包原样记录 | all-unit-final.log/json |
| B | go vet ./... | 0；无诊断 | vet-final.log/json |
| R | node contracts/generate.mjs --check | 0；60 operations、8 enums verified | contract-check-final.log/json |
| B，移除 TEST_DATABASE_URL | go test -count=1 -tags=integration -run '^TestSharedInventoryRace$' ./internal/catalog/... | 1；预期明确失败 isolated TEST_DATABASE_URL required，没有跳过或借库 | missing-test-database.log/json |
| B | go test -count=10 -tags=integration -run 'TestSharedInventoryRace\|TestPickupAndCancellationExcludeEachOther\|TestReservationsLockMultipleItemsInOneOrder\|TestCatalogSamePhysicalAggregationAndConcurrentLastUnit' ./internal/catalog/... | 0；四组屏障/聚合用例额外十轮 | concurrency-barriers-repeat.log/json |
| E/replay | git -c core.autocrlf=false apply --check E/code-increment.patch，随后实际 apply | 0 / 0；全新起始副本，全部文件摘要等于开发树 | code-package-result.json |
| E/replay/backend | go build ./... | 0 | replay-build.log/json |
| E/replay/backend | go test -count=1 ./internal/catalog/... | 0 | replay-unit.log/json |
| E/replay/backend | go test -count=1 -tags=integration -v ./internal/catalog/... | 0；34 顶层、39 子例 PASS，0 FAIL、0 SKIP | replay-integration.log/json |
| B | go run E/database-proof.go | 0；18.6，专用回环实例，P02 schema 残留 0 | database-cleanup-proof.log/json |

精确命令、工作目录、退出码另见每条 JSON。红绿命令记录同样保存 JSON。末轮补丁加入本报告后，在另一个全新起始副本 `E/delivery-replay` 再执行 apply --check、实际应用、逐文件摘要核对、全模块编译、本包单测及真实 PG 集成测试；最终交付清单收录该轮独立结果，不以只检查代码补丁替代最终交付验证。

本轮仅使用已有本机 PostgreSQL 二进制，新建专用数据目录 `E/pg-data`，专用回环端口 55492，与旧 G0/P01 实例无关。initdb 退出 0，pg_ctl 启动日志确认 ready。仅停止本轮数据目录对应实例，停止和停止后 status 的实际退出码及日志纳入交付清单。没有对其他 PostgreSQL 进程执行终止；数据库、缓存和连接参数不进入补丁。

## 交易与安全编号结论、开放项

| 编号 | 本轮结论 | 未覆盖及责任 |
| --- | --- | --- |
| TRADE-01 | PASS（库存侧） | P02 规格、价格与自然时间重校、版本、最后库存通过。P05 报价有效期/所有者、完整订单提交重报价及跨端口编排 NOT RUN；P05 在 G1 后实施，本轮不启动 |
| TRADE-03 | PASS（库存侧） | 五库存操作中途回滚、取货取消竞争、分拣待归架、损耗及精确重放通过。收款/事件故障引发库存+时段+结算+履约整单回滚 NOT RUN，归 P05/G2 |
| SEC-01 | PASS（本地 P02） | 真实身份适配角色×站点×对象、CSRF/Origin、入口与停用通过。实际三端/三域/反代及跨系统角色矩阵 NOT RUN，归总控/P07/P08 |
| SEC-03 | PASS（本地 P02） | 白名单/输入限制/公开字段、追加流水、审计一致性、诊断与日志脱敏通过。独立审计副本、反代日志、预发 CSP 与非实现者独立安全复核 NOT RUN，归 P07/P08 |
| P00 遗留 race | BLOCKED（继承，本轮 NOT RUN） | 当前仍未找到 gcc；由 P07 在具备 C 工具链的 CI 执行，不冒充通过 |
| 依赖安全复扫及历史 picomatch | NOT RUN | P07/P08 执行完整依赖/制品复扫；本轮未更新依赖，也未声明旧告警关闭 |
| 真实三端、启动接线、预发、上线 | NOT RUN | 总控处理共享启动接线，P06/P07/P08 后续联调；域处理器通过不等于应用已接线，也不把启动未接线判为 P02 域失败 |
| G1 与独立发布签字 | NOT RUN | 总控/P08 后续门禁；本轮不签发 |

在本包要求范围内没有剩余 FAIL/BLOCKED/NOT RUN 必需项。上表系统开放项保留原门禁，不用本地结果替代。此次不需要共享变更提案；共享 Clock/端口/依赖/身份均未修改，新增时钟为 catalog Store 自身可选字段。未来接线时让 P05 与 catalog 使用同一 Clock；订单分拣/取消资格及各域锁顺序仍由 P05 负责。

已使用 using-superpowers、systematic-debugging、test-driven-development、verification-before-completion 和 requesting-code-review 工作流程。一次仅限 P02 增量的只读代码复核未发现 Critical/Important 问题；没有改文件或派发其他包。这是代码复核，不是 P08 独立安全签字。

## 增量交付

最终补丁为 `D:/校零/校零智选-P02-复核-20261118.patch`，只相对本轮起始快照生成，不含 P00/P01/P03/P04 历史改动。SHA-256、字节数、逐文件起始/最终摘要、命令日志摘要、补丁实际回放结果、写域与未变化文件证明，见 `D:/校零/校零智选-P02-复核-20261118-清单.json`，避免报告自身摘要循环。

保留 `D:/校零/校零智选-P02.patch` 及其他既有补丁；补丁不带 node_modules、缓存、数据库、凭据或环境文件。交付前对补丁、报告及验收日志执行隐私模式扫描，动态手机号、Cookie、会话材料只存在合成测试内存或可销毁数据库，不输出给报告。最终回放使用全新起始副本而非开发树，未初始化 Git。
