# P03 本轮复核记录

日期：2026-11-18（Asia/Shanghai）。本包必需验证已通过；最终补丁回放、摘要核对和测试数据库关闭结果以 `D:/校零/校零智选-P03-复核-20261118-清单.json` 为最终交付判据。本记录不签发 G1、预发或上线放行。

## 基线、写域与方法

项目：`D:/校零/校零智选-开发`；原型只读。未初始化 Git、提交或推送，未重放历史补丁。起始副本为 `D:/校零/.p03-recheck-20261118/start`，共 130 文件。起始清单 `start-manifest.json` 的 SHA-256 为 `11427bdfd9531f90eda5eacb2440503eaf7d6de52f0f11a91b0bea8695f1f08b`。以下证据文件均位于该 `.p03-recheck-20261118` 目录。

`baseline-check.json` 核验四份历史补丁及 P00/P01/P02 最新逐文件清单，全部相符：P00 `1831037543955abf19a543416112bc75971652cb7fd304b78f7d943fd21c8250`；P01 `19a4f8bdf8109f4047689d6eab87c068cc93c13b82f8a6d900baa1ddd6c00263`；P02 `697f68f7f526dc554b5a1a81ee321d33df142fab8d411b949b93d26f6c33466f`；原 P03 `79b16ffc65d57227b350b65e357b5328c2512a10eccbb25470780ea7178cda79`。已知共享差异 go.mod、go.sum、platform/migrate_pg_test.go 保持原样；不是本轮改动。

仅修改 schedule 内源码/测试和本文；没有新增迁移，已有迁移完全未改。增量为 12 文件，其中修改 5 个既有文件，125 个起始文件保持不变。生产修改限 http.go、slots.go、reservation.go。使用固定时钟、真实 PG、事务屏障和实际 SQL 故障；未用 SQLite 或 mock 证明一致性。

## 缺陷与红绿证据

| 缺陷 | 原问题与修复 | 失败证据 → 通过证据 |
| --- | --- | --- |
| P03-R01 | 配置/批次缺审计；在同事务记录服务端 actor、station、对象和 request_id，同键重放不重复审计 | red-audit-version-ports-assertions → green-audit-version-ports-assertions |
| P03-R02 | JSON 非精确白名单；拒缺项、null、重复、大小写变体、未知字段及错误嵌套/类型 | red-shape-window-error → green-shape-window-error |
| P03-R03 | 非整 15 分钟营业尾段被静默截断；改为拒绝不完整区间和不可表示年份/跨年 | red-shape-window-error → green-shape-window-error |
| P03-R04 | SLOT_FULL/SLOT_EXPIRED 缺 HTTP 冲突映射；补 409 | red-shape-window-error → green-shape-window-error |
| P03-R05 | 最大安全版本边界不一致；解析允许 9007199254740991，版本耗尽/不匹配返回 409，不产生不可安全表示的新版本 | red-audit-version-ports-assertions → green-audit-version-ports-assertions |
| P03-R06 | nil 调用者事务可 panic、零时刻可进入端口；显式拒绝基础设施或输入错误 | red-audit-version-ports-assertions → green-audit-version-ports-assertions |

上述红灯命令退出 1 且为目标断言失败，绿灯退出 0。保留全部中间日志：red-audit-version-ports 首次因测试误用不存在的 Valid 方法编译失败；expanded-http 首次有未用 import；green-audit-version-ports 首次旧重叠夹具不满足完整区间。修正测试后重跑，不将这些编译/夹具错误当作产品红灯。恢复收尾时数据库未在指定端口监听，exact-pid-repeat、retry、final 三份日志为连接失败；纠正本轮实例启动参数后 exact-pid-repeat-verified 退出 0。直接以管理员运行 postgres 被数据库自身拒绝，随后使用 pg_ctl 的既有受限启动机制；未绕过该限制。

## 本轮验收矩阵

PASS 仅指本地包范围。测试名称省略 Test 前缀。

| 范围 | 状态 | 证据和边界 |
| --- | --- | --- |
| 1 上海日期/UTC、月末年末闰日、23:30 跨午夜、连续 15 分钟、提前量 0/120、日期非法 | PASS | RecheckCalendarLeadAndBookingEdges、RecheckWindowIntervalMustBeComplete；原固定时钟测试保留 |
| 1 预约范围默认 3 天及配置 1/3/7；上限前/等于/后 | PASS | RecheckHTTPAndPortBookingRangeAgree；冻结“最多 N 天”解释为上海 now 加 N 日的含端点滚动上限，非第 N 日末；默认现代上海为 72 小时。纳秒单测与 PG 微秒边界均覆盖 |
| 2 SCHEDULED 指定窗且 now<dispatch_at，ASAP 最早可用窗且距结束至少 10 分钟 | PASS | RecheckSelectionAndPrecision；恰好 dispatch/end、10 分钟/差 1ns、满窗跳后窗、全满/关闭、非法模式/ID；选档无写入 |
| 2 提交使用原窗口不换窗，报价有效期/订单资格 | PASS / NOT RUN | RecheckOriginalSlotRevalidationAndConfigVersion 验容量端原窗；报价与订单资格归 P05 |
| 3 最后名额、同单同槽重放、同单异槽/异模式、并发不同槽不泄漏 | PASS | RecheckLastSeatTransactionBarrier、RecheckConcurrentSameOrderReplay、RecheckSameOrderDifferentSlotsConcurrent；事务已开始，真实行锁屏障，观察确切两个 contender PID 的 pg_blocking_pids 后放行，无 sleep 赌并发 |
| 3 预占/释放竞争容量调整/关窗，锁协议/反向批次 | PASS | RecheckReserveReleaseAgainstAdmin 四组合；RecheckActualLockProtocol、RecheckBatchesReverseInputAndConcurrentGeneration。预占/释放先锁窗口再写预占；批次先站点 NO KEY UPDATE，再有序探测窗口 |
| 3 数据库约束 | PASS | RecheckMigrationHistoryAndDatabaseConstraints 实际 SQL 拒负 used、超容量、非法窗口/dispatch、重复站点起点、重复订单、非法 mode/release |
| 4 释放一次可再售，重复释放、错误前序、结束边界、关闭/过期同参重放 | PASS | RecheckReplayLifecycleAfterClosureAndExpiry：结束前释放，恰好结束拒绝新释放，重复已释放可重放；已释放不可复活；原预占关窗/过期后重放不扣第二次 |
| 4 分拣后取消、送达/退回不得释放及定时唤醒 | NOT RUN | SchedulePort 无订单履约状态；P05 持订单锁判资格。SLOT-03 唤醒/租约/迟到告警未实现、未派发 |
| 5 批次幂等同键同参/异参、失败重试、不同键同窗、重叠/跨午夜/并发 | PASS | RecheckHTTPBatchConcurrentKeys、RecheckBatchFailureRollbackAndRetry、RecheckBatchesReverseInputAndConcurrentGeneration；中途/幂等完成故障后窗口、审计、key 全回滚 |
| 5 expected_version、capacity>=used、禁止改 used/窗口、关闭保留历史、配置版本与更新时间 | PASS | RecheckOriginalSlotRevalidationAndConfigVersion、RecheckVersionUpperBound、RecheckHTTPPaginationAndInputLimits；计数变化不改配置版本/更新时间，配置变更才递增 |
| 5 审计与业务事务一致 | PASS | RecheckHTTPAuditAtomicity，审计触发失败使配置/批次/key 全回滚；同键重放不造审计 |
| 6 五条冻结 HTTP 权限及真实身份适配 | PASS | RecheckHTTPWithRealIdentity：真实 Sessions.Issue/Lookup + IdentityHTTP.Authenticated；客户接口仅 CUSTOMER，后台仅 OPERATOR，其他角色/匿名拒绝；服务端角色/站点、Host、伪造 header、Origin/CSRF、停用账号、跨站 GET/PUT404 与列表[] |
| 6 近期 MFA | PASS | 五条冻结路由 x-recent-mfa=false；16 分钟后普通运营配置仍允许，未擅自增加近期 MFA。真实三域/代理/启动不在夹具证明范围 |
| 7 输入边界、分页与公开字段、三字段错误包络、诊断脱敏 | PASS | RecheckStrictJSON、RecheckHTTPPaginationAndInputLimits、RecheckErrorMapping；95 窗口默认20/最大100/分页17无漏重，客户5字段后台9字段，空items=[]/next_cursor=null，超体413，ID/key/cursor按冻结限制；request_id 非空 |
| 7 Pool/Clock/Authorize、nil tx 缺失 | PASS | RecheckMissingHTTPInfrastructure、RecheckInvalidPortTimeAndInfrastructure；五路由503；端口拒绝，不开替代事务、不假成功 |
| 8 调用者事务、未提交可见、外部不可见、回滚与故障 | PASS | RecheckCallerTransactionVisibilityAndRollback；RecheckPortFaultsRollback 在预占写后、计数写后实际触发 P0001；回滚后重试成功；调用方必须回滚整个事务 |
| 8 库存/资金/履约整单同事务 | NOT RUN | TRADE-03 整单证明归 P05/G2；本包不调用其他 W1 私有表 |
| 9 000+030 独立空 schema、带数据重复迁移、历史摘要/时间稳定、全库版本唯一 | PASS | RecheckMigrationHistoryAndDatabaseConstraints；独立夹具无 accounts/inventory/wallets/orders。真实身份夹具单独 000+010+030，不替代核心独立性 |
| 9 新迁移存量升级 | NOT RUN（不适用） | 本轮未新增迁移；原 SQL 摘要完全保持 |

验收 ID：SLOT-01/02 为 PASS_LOCAL；SLOT-03 为 NOT RUN（P05）；TRADE-03 为 PASS_LOCAL_CAPACITY_ONLY，整单 NOT RUN；SEC-01/03 为 PASS_LOCAL_IDENTITY_ADAPTER_ONLY，真实域/预发/独立安全复核 NOT RUN。

## 实际命令与证据

Go 1.27.4 windows/amd64，Node 26.13.1；独立 PostgreSQL 18.6，回环 127.0.0.1:55493，时区 UTC。只用显式 TEST_DATABASE_URL，清除应用/迁移连接变量，连接凭据不写交付；只停止本轮数据目录的实例。

| 工作目录 | 命令 | 退出码 | 日志 |
| --- | --- | --- | --- |
| 项目/backend | go test -count=1 ./internal/schedule/... | 0 | schedule-unit-final.log |
| 项目/backend | go test -count=1 -tags=integration -v ./internal/schedule/... | 0 | schedule-integration-final.log；31 顶层、28 子用例 PASS，0 FAIL/SKIP |
| 项目/backend | go test -count=1 ./... | 0 | all-unit-final.log |
| 项目/backend | go vet ./... | 0 | vet-final.log |
| 项目根 | node contracts/generate.mjs --check | 0 | contract-check-final.log；60 operations、8 enums |
| 项目/backend | 不提供 TEST_DATABASE_URL 的集成负例 | 1（预期） | missing-test-database.log，isolated TEST_DATABASE_URL required |
| 项目/backend | 三组精确 PID 并发场景 -count=10 | 0 | exact-pid-repeat-verified.log；完整命令见同名 json |
| 项目/backend | 五组屏障并发场景 -count=10 | 0 | concurrency-barriers-repeat.log；在精确 PID 测试改进前运行 |

所有完整命令、工作目录与退出码同时保存在同名 json。只读代码复核未发现 Critical/Important；唯一 Minor 为全局锁等待统计可能受其他连接干扰，已改为确切 PID 并经十轮真实 PG 复测；这不是独立安全签字。

## 交付与未覆盖项

最终补丁 `D:/校零/校零智选-P03-复核-20261118.patch` 相对本轮 start；最终清单记录补丁 SHA-256、逐文件起始/最终摘要、原迁移不变、全部命令和证据摘要。全新 `delivery-replay` 从 start 建立，先 git apply --check 再实际应用（均不初始化仓库），所有源文件逐字节核对后执行 go build ./...、本包单测及真实 PG 集成；结果见 final-package-result.json 和 delivery-{build,unit,integration}.{json,log}。早先 code-replay 仅是中间回放，不作为最终文件一致性证据。

最终数据库残留 schema 检查与关闭见 database-cleanup-proof、database-stop-result；未验证最终清单前，不以本文推断清理成功。

P05/G2：订单资格、取消/分拣/送达/退回策略、SLOT-03、报价提交和整单回滚；总控/P06/P07/P08：启动接线、真实三端三域、反代、预发、独立安全复核。继承 race 缺 gcc 仍 BLOCKED（P07），依赖安全复扫及审计副本 NOT RUN（P07/P08）。未新增这些实现、未派发其他包、未签 G1。下一阶段仅 P04 本地复核，余额生产开关继续关闭。
