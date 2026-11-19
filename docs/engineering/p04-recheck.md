# P04 收款与账本本地复核

执行日期：2026-11-18（Asia/Shanghai）。项目：`D:/校零/校零智选-开发`。验收依据：`D:/校零/校零智选-P04-验收提示词-20261118.md`、冻结后端设计、00-master / 05-settlement / 10-contracts / 08-acceptance、OpenAPI / enums / operations 和 SettlementPort；没有从实现倒推金融规则。

**结论：P04 本地验收通过，范围是本包端口、八条 HTTP、真实身份适配和受限角色实际 CLI。** 交付补丁独立应用、完整文件摘要、重放构建/测试与资源关闭证据在同名外部清单中，须与本报告配套核验。本轮不签 G1，不启动 P05，不代表 COD 或余额上线 GO；生产余额保持关闭。

全部账户、订单 ID、批准信息、现金和资金均为合成数据。没有接入真实资金、供应商或公网，没有初始化 Git、提交、推送或重复应用历史补丁。

## 基线和写域

起始独立副本 `D:/校零/.p04-recheck-20261118/start` 含 137 个源文件/文档；逐文件清单 `start-manifest.json` 的 SHA-256 为 `63397d8e4f171f6dceb1654eedec0ffba291188af90bef6412f843c4d87f527c`。`baseline-check.json` 对最新交接清单核对，无未解释差异。

| 交付 | 本轮实际 SHA-256（与指定值相同） |
| --- | --- |
| P00 复核补丁 | `1831037543955abf19a543416112bc75971652cb7fd304b78f7d943fd21c8250` |
| P01 复核补丁 | `19a4f8bdf8109f4047689d6eab87c068cc93c13b82f8a6d900baa1ddd6c00263` |
| P02 复核补丁 | `697f68f7f526dc554b5a1a81ee321d33df142fab8d411b949b93d26f6c33466f` |
| P03 复核补丁 | `c41a380ae18cbdcfaebb01dc002d18c64a686226a824e2b3c4c5a88712577a8f` |
| P03 复核清单 | `7d8bb792e551bd8fd75cbe125be53b13746263e0a058a1c6e95c5ae0ce679638` |
| 原 P04 补丁 | `b5608e92f22f69e7c6fb3dcea28d4a5405d6d793198d2b6099463c3f2d3ce1dd` |

开发树已含 P01–P04 与前三包复核成果。P00 已知共享差异 `backend/go.mod`、`go.sum`、`internal/platform/migrate_pg_test.go` 保持起始字节；不恢复成纯 P00。只读原型、其他包、contract、platform、bootstrap、contracts、web、启动接线和既有迁移均未改。最终逐文件比较拒绝越界变动。没有新增迁移；`040_001_settlement.sql` 保持 `1bcf9fe6aafb154a99925bfc597cce67a3eb2e7fb5bd8b812793d5ae9a5458ea`。

产品修复仅在：

- `backend/internal/settlement/cod.go`：七端口的共同结算锁和创建入口防 nil tx；同单创建的冲突覆盖全部唯一索引，随后锁读并比较完整原始 OrderMoney，仍拒异客户/站点/方式/金额。
- `settlement/json.go`、`import.go`、`http.go`：精确字段 JSON，拒大小写别名、重复/未知成员、缺字段、null、错误嵌套/类型和非法 UTF-8。导入保留原始清单字节 SHA 校验。
- `settlement/import.go`：站点范围事务 advisory lock + 普通站点读取，替代要求共享 stations UPDATE 权限的锁读；钱包仍按账户升序锁定，全批事务不变。
- `settlement/http.go`：必需注入 Clock，缺失/零时钟 503；默认收款日由注入时钟转上海业务日；游标保持最多 128 字符可往返；幂等键按 128 Unicode 字符而非字节计长；禁用渠道、余额不足映射冻结 409 错误码。
- `settlement/reconcile.go`：制单须为合法已收状态，拒绝只有收款行却仍未收的孤立数据。
- `backend/cmd/balance-import/main.go`：比较配置 DSN 的用户/端点，防仅改连接选项、loopback 拼写或同集群数据库名绕过应用/迁移连接复用检查。

其他改动是上述两目录内的测试，以及本报告。既有两处手工插入收款的正例夹具补足 COD_COLLECTED 状态，没有改变金融规则来迎合旧测试。

## 本包验收矩阵

状态含义：PASS 为本轮实际通过；FAIL 为最终未解决失败；BLOCKED 为环境/依赖无法执行；NOT RUN 为本轮边界之外或未实际演练。中间红灯另表保留，不等同最终 FAIL。

以下测试名均可在 `backend/internal/settlement/` 查到。主要完整运行证据是 `D:/校零/.p04-recheck-20261118/final-complete-integration.log`；命令、cwd、退出码另有同名 JSON。

| 验收项 | 状态 | 实际证据及结论 |
| --- | --- | --- |
| 渠道开关、WECHAT 待接入、直接端口禁用 | PASS | PaymentPolicyDefaultsToDisabled / UsesIndependentSwitches、CODCollectionReplayAndCancellation、RecheckMoneySnapshotAndBounds。默认 BALANCE/WECHAT 不可用，直接 CreateSettlement 拒禁用方式，余额不足无 COD 回退。 |
| 服务端金额/账户快照与整数边界 | PASS | RecheckMoneySnapshotAndBounds：负数、超 `9007199254740991`、bigint 外输入拒绝；最大安全整数保真；原客户/站点/方式/金额异参拒绝。冻结金额 minimum=0，零金额结算按契约允许；导入条目须 >0。 |
| COD 创建、收款、撤销与重放（PAY-01 本包） | PASS | CODCollectionReplayAndCancellation、RecheckCODCollectionBatchVerifyBarriers、RecheckPortFaultsRollback。创建只应收；全额取原快照，同骑手重放一次，异骑手拒；未收撤销 VOID 无退款，已收不能走未收撤销。端口无“少付金额”参数，不接受客户端金额覆盖。 |
| COD 归日和批次 | PASS | CODBatchMembershipAndIndependentVerification、RecheckBatchRejectsUncollectedReceipt、RecheckCODShanghaiCalendarBoundaries。站点/骑手/业务日限制，一收款一批；非法未收行拒绝；重复/并发制单不重用成员。 |
| 原始复点及职责分离 | PASS | 同人制单复点拒绝；不同财务全缴 VERIFIED、短缴 DISCREPANCY；原复点报告追加一次且不可改。同键同参第一次逻辑结果，异参冲突；不同键重复复点不覆盖原报告。冻结 CODVerifyRequest 没有 expected_version，测试拒此额外字段，未自造版本参数。 |
| 钱包最后余额及同单扣款（PAY-03 本包） | PASS | RecheckLastBalanceAndSameOrderBarriers。真实事务屏障下两单争最后余额仅一单扣成，同单并发仅一笔 DEBIT，余额非负；完整结果/流水数均断言。 |
| 历史取消/退回/人工退款互斥 | PASS | WalletAtomicDebitAndHistoricalReturn、ManualBalanceRefundCannotAlsoReverseOrder、RecheckRefundCrossCommandBarrier。关闭新单余额开关后历史合法退原钱包；取消、FinalizeReturnSettlement、人工退款共享订单唯一保护，跨命令最多一退；已收 COD 不冒充完成线下退款。 |
| 人工全额退款与独立审批 | PASS | ManualCODRefundRequiresIndependentApprovalAndPayout、ManualBalanceRefundCannotAlsoReverseOrder、RecheckApprovalPayoutBarriers。原金额/钱包、前序与身份约束；余额审批与退款同事务；运营发起者不能自批。 |
| COD 审批/真实兑付记账区分 | PASS | 审批只到 REFUND_PENDING，记录唯一凭证后才 REFUNDED；RecheckPayoutReferenceContention 验不同退款争同凭证仅一笔成功；顺序/并发兑付不重复；未收款不产生现金退款。这里只验合成凭证记账。 |
| 七端口沿用调用者 tx | PASS | RecheckCallerTransactionAllSevenMethods：Create/Collect/Cancel/Return/Request/Approve/Payout 各自未提交内部可见、外部不可见，caller rollback 全部消失；不自行提交/另开交易。RecheckNilCallerTransaction 验所有入口不 panic。错误后调用者必须回滚整个 tx。 |
| 钱包/收款/退款故障原子性 | PASS | RecheckPortFaultsRollback：真实 PG AFTER trigger P0001 在钱包更新、流水、收款、审批、REFUNDED 状态和兑付写后注错，回滚再同参重试。 |
| HTTP 业务、审计、幂等完成原子性 | PASS | RecheckHTTPWriteFaultsAndReplay：四写路由 × 业务中途/审计/幂等完成故障；全业务表 fingerprint 与原样一致，失败不留 key；重试一次，重放同逻辑 JSON，异参409。 |
| 受限角色实际 CLI（PAY-04 本包） | PASS | cmd/balance-import/TestRecheckActualCLIRestrictedRole 实际构建 exe 并 exec。干跑四表0写；apply=300分/2流水/1批次，重放不增；合法异内容 total400/200+200、缺批准、授权挪用、坏 SHA、总额不符、重复条目/引用、错站点、负数/越界、>1MiB 拒绝；第二条流水故障全批回滚，再试=600分/4流水/2批次。 |
| 导入并发与加法溢出 | PASS | RecheckImportAgainstDebitAndReturn：多钱包导入与扣款/退回真屏障，无账本漂移；RecheckConcurrentImportReplayAndOverflow：同批准并发只一批，钱包加法溢出干跑和 apply 都拒绝。 |
| CLI 专用权限与连接隔离 | PASS | 实际 LOGIN 导入角色无 superuser/createdb/createrole/replication/bypass/membership；仅 schema USAGE、4表SELECT、3表INSERT、wallet balance列UPDATE、流水sequence USAGE；无站点/结算/退款/审计/COD写权。复用应用/迁移配置及选项别名的单测拒绝。不能以此推断任意生产角色已安全。 |
| 钱包与 COD 对账 | PASS | WalletReconciliationDetectsDriftAndMissingDebit、CODDayReconciliationSeparatesRefundFromRemittance、导入竞争后的 ReconcileWallets；下面给出可算回金额。数据库直接SQL拒错账户/金额/方式/重复退款引用。 |
| 记录不可变与迁移独立性 | PASS | RecheckMigrationsAndDirectConstraints：000+040 独立空 schema；带存量钱包/流水/应收/收款/批次/成员/复点/退款重复迁移两次，全行JSON（含history摘要/时间）不变；全库版本唯一，无010/020/030/050表依赖；8个不可变表 UPDATE/DELETE/TRUNCATE 实际拒绝。 |
| 八 HTTP 真实身份/MFA（SEC-01/03 本包） | PASS | RecheckEightRoutesRealIdentityAndMFA，真实 Sessions.Issue/Lookup + IdentityHTTP.Authenticated；000+010+040 仅用于身份联调，未替代核心独立性证明。细项见下表。 |
| HTTP 精确输入/分页/金额/错误 | PASS | RecheckHTTPExactJSON、HTTPPaginationAndFrozenInputs、HTTPAmountAndCommandBounds、CursorContract、PaymentErrorMapping。两列表95行，以17分页不漏不重；默认20、上限100，空 items=[]/next_cursor=null；ID/游标/幂等键128字符往返，129拒；理由64/65；32KiB限制；浮点/指数/string及safe/bigint越界拒绝。 |
| HTTP 依赖不完整 | PASS | RecheckHTTPMissingDependenciesAllRoutes / MissingClock：缺 Pool/Clock/Authorize 八路503，零时钟503；缺 RefundCommands 退款写503，无默认授权/系统时钟/绕过Store成功。 |
| 退款 HTTP 同事务适配夹具 | PASS | recheckRefundCommands 明确为 P04 测试夹具，按会话站点查退款并调用 Store 同一 tx；failAfter 证明包裹回滚。没有实现或验证订单资格、订单锁、库存恢复。 |
| 新迁移带存量升级 | NOT RUN | 本轮没有新迁移，升级场景不适用；已有迁移摘要不变及重复执行已验证。 |

不可变表验证范围：wallet_entries、balance_import_batches、cod_receipts、cod_refund_payouts、cod_batches、cod_batch_receipts、cod_batch_verifications、audit_events。权限治理的生产级独立安全签字不在此 PASS 内。

## 八条 HTTP 的真实鉴权与协议

均以 `/api/v1` 为前缀。真实身份测试遍历 CUSTOMER、RIDER、OPERATOR、FINANCE、SECURITY_ADMIN，普通读取不加近期 MFA；四写路由验证服务端会话 MFA 为新近、15分钟前1微秒、恰15分钟和16分钟（后两者拒绝），以及无MFA防御。错误三字段 `code/message/request_id`，request_id 非空；返回与日志不包含内部诊断/认证材料。

| 路由 | 冻结角色 | 近期 MFA | 本轮 |
| --- | --- | --- | --- |
| GET /payment-methods | CUSTOMER | false | PASS |
| GET /me/wallet | CUSTOMER，本人站点钱包 | false | PASS |
| GET /admin/cod/receipts | FINANCE | false | PASS |
| POST /admin/cod/batches | FINANCE | true | PASS |
| POST /admin/cod/batches/{id}/verify | FINANCE | true | PASS |
| GET /admin/refunds | OPERATOR 或 FINANCE | false | PASS |
| POST /admin/refunds/{id}/approve | FINANCE | true | PASS（退款业务为限定夹具） |
| POST /admin/refunds/{id}/record-cod-payout | FINANCE | true | PASS（退款业务为限定夹具） |

真实会话还覆盖：入口 Host、错误域/入口404、停用/到期401、跨站列表空/对象404、钱包只本人、伪造角色/站点/forwarded header无效，四写 Origin/CSRF 缺失或恶意值403。日志检查 Cookie/token/CSRF/电话/房间/认证查询等不泄露。SQL内部错误仅对外 INTERNAL_ERROR，不输出原 SQL。金额禁用/不足/非法前序/幂等冲突分别为冻结409枚举；P05报价/下单的端口错误映射仍须由P05验收。

P01现有SQL本来禁止员工session无MFA。测试先证明该约束拒绝，再仅在一次性对抗schema移除 `sessions_check` 构造NULL，证明真实授权适配器也403；跨站对抗仅在一次性schema移除单站singleton索引。生产迁移、identity代码和约束没有修改。真实身份授权证据与使用授权替身的协议/故障测试分别存在，未互相冒充。

## 并发、时钟与锁

核心并发不是sleep抽签：先启动各真实交易，gate事务持锁，统一start，逐个竞争连接的确切PID进入 `pg_blocking_pids` 等待后才释放；HTTP同键竞争使用独立 application_name，观测两连接都在Lock等待后释放。所有响应、成功/失败类别、余额、行数和唯一结果均断言。

修复后以下8个顶层场景连续10轮通过，日志 `green-concurrency-ten-rounds.log`，exit 0：最后余额/同单、取消退回人工退款、COD收款/制单/复点、审批/兑付、不同退款同凭证、四HTTP同键、导入vs扣退、同导入批次重放。此前十轮红灯捕获9轮同单创建23505，完整保留；未把偶发错误当作历史遗留。

实际锁路径：

- 新结算 INSERT/唯一约束 → 钱包行；同单重放 settlement FOR UPDATE → 比较原快照。存量取消/退回/退款 settlement行 → 原钱包行。P05必须先持订单锁再调端口，本包没有订单表，未证明这部分调用约定已经接线。
- COD收款 settlement行 → 收款INSERT → 状态；制单先receipt ID升序锁收款 → batch/member；复点batch行锁 → append原始报告。
- 退款先settlement行 → 退款前序/原钱包，兑付再凭证唯一约束。导入站点scope事务advisory锁 → actor升序钱包锁 → 整批流水/余额；未要求stations UPDATE权限。

数据库为本轮新建 PostgreSQL 18.6、loopback独立55494、UTC，连接通过 TEST_DATABASE_URL 注入且移除应用/迁移/导入环境。上海归日实测年末2026-12-31、次年2027-01-01、闰日2028-02-29，UTC16:00起止及前后1微秒；收款/批次/对账同口径。HTTP默认业务日使用固定注入Clock，不使用SQL now。时间样本是合成边界，无未来业务数据上线含义。

## 可逐项算回的对账样本

钱包样本：期初500 + 导入0 − 两单200×2 + 取消退原钱包200 = 期末300分，1账户，正常 mismatch为0。主动改为301并造一笔缺DEBIT的余额结算，实际检出 BalanceMismatches=1、OrderMismatches=1。实际CLI另一个独立样本从0导入300，同批准重放仍300；中途故障仍300；同参重试最终600，4条IMPORT、2批准批次。多钱包导入竞争后逐账户无漂移。

COD合成业务日2026-11-19：应收1700（900+200+500+100），撤销100，实收1100（900+200），未入批200，入批应缴900，待复点0，实缴800，差额100，另记线下退款900。已收后来REFUNDED的900仍进入原骑手应缴，没有用退款冲抵。上海次日边界的999不进当日应收；跨站结果全0。应收按created_at归日，实收按collected_at，批次按local_date，线下兑付按recorded_at；不能把这些不同日期来源当同一订单集净额。

流水不重复/账户金额错配既通过唯一/外键等直接SQL负例拒绝，也通过对账异常检查。包内对账方法不代表新增财务HTTP报表；未新增充值、发放、提现、转账、微信支付或回调入口。

## 缺陷红灯与复测

证据目录均为 `D:/校零/.p04-recheck-20261118/`，每项log另配实际command/cwd/exitCode的JSON。

| 原缺陷/发现 | 真实目标红灯（exit 1） | 最小修复后的绿灯（exit 0） |
| --- | --- | --- |
| 七端口nil panic、宽松导入JSON、128ID游标变长、业务错误500 | red-nil-json-cursor-errors | green-nil-json-cursor-errors；最终unit/integration |
| HTTP大小写/重复/null/缺字段被接受并留key；未收孤立receipt能制单 | red-http-shape-batch-state | green-http-shape-batch-state；四路由完整输入负例 |
| 缺Clock仍200 | red-clock-assertion | green-initial-rechecks；最终八路依赖矩阵 |
| 真受限角色dryrun成功但apply失败：站点锁读需UPDATE权限 | red-restricted-cli | green-restricted-cli；最终actual CLI含合法异内容拒绝 |
| 改application_name绕过DSN字面相等检查 | red-cli-credential-alias | green-cli-credential-alias；最终CLI单测 |
| 同单并发在复合唯一索引发生23505 | concurrency-ten-rounds | green-concurrency-ten-rounds 10/10，各场景及最终完整集成 |

开发过程非产品目标红灯也保留：red-missing-clock因runner误清TEST_DATABASE_URL，只能证明缺库，改runner后red-clock-assertion才是目标红灯；real-identity-mfa首次受P01 sessions_check拒绝，后改为先证SQL约束再一次性对抗夹具；expanded-http有unused import编译失败，expanded-http-compiled有JSON字节顺序比较错误，改语义JSON后expanded-http-semantic-json通过。这些没有冒充产品缺陷。

本包增量由只读复核代理独立审查，未发现Critical资金/越权缺陷；提出CLI异内容用例不足，已补合法400分清单及失败后账本不变断言。复核代理完整集成曾在fixture迁移040遭PG XX000 `trying to store an on-disk heap tuple into wrong type of slot`（exit1）；其后该用例独立3/3通过，尚不能归因为产品代码。该代理两次命令只有聊天工具输出，没有另存原始日志，外部清单按代理报告来源记录，不伪造为主验收日志。主验收随后避开其他DDL压测完整串行重跑，以及原要求命令全套重跑均exit0，保留该环境失败事实和复核记录。此代码复核不代替P08独立安全签字。

## 实际命令和交付校验

工作目录 B=`D:/校零/校零智选-开发/backend`，R=`D:/校零/校零智选-开发`。下表均为本轮实际运行；源码后续只补充迁移存量批次测试，再以final-complete-integration验证全部集成通过。

| cwd | 实际命令 | exit | 证据标签 |
| --- | --- | --- | --- |
| B | go test -count=1 ./internal/settlement/... ./cmd/balance-import/... | 0 | final-local-unit |
| B | go test -count=1 -tags=integration ./internal/settlement/... ./cmd/balance-import/... | 0 | final-required-integration |
| B | go test -p=1 -count=1 -tags=integration -v ./internal/settlement/... ./cmd/balance-import/... | 0 | final-local-integration |
| B | go test -count=1 -tags=integration -v ./internal/settlement/... ./cmd/balance-import/... | 0 | final-complete-integration |
| B | go test -count=1 ./... | 0 | final-all-unit |
| B | go vet ./... | 0 | final-vet |
| B | go vet -tags=integration ./... | 0 | final-integration-vet |
| R | node contracts/generate.mjs --check | 0 | contract-check-final，60 operations / 8 enums |
| B | go test -count=10 -tags=integration -v -run ^TestRecheck(LastBalance\|RefundCross\|CODCollectionBatch\|ApprovalPayout\|PayoutReference\|HTTPConcurrent\|ImportAgainst\|ConcurrentImport) ./internal/settlement | 0 | green-concurrency-ten-rounds |
| B | go test -count=1 -tags=integration -run ^TestCODCollectionReplayAndCancellation$ ./internal/settlement（清除TEST_DATABASE_URL） | 1（预期） | missing-test-database；明确FAIL，无SKIP假PASS |
| B | go run D:/校零/.p04-recheck-20261118/database-proof.go | 0 | pre-delivery-database-proof：PG18.6/UTC/55494，无残留schema/导入role/其他session |

导入真实CLI执行发生在集成测试中：实际 `go build -o <一次性exe> .` 后以 `--manifest --sha256 --approval --approver --executor` 分别干跑及 `--apply`；专用 BALANCE_IMPORT_DATABASE_URL 指向一次性受限角色。详细测试输出只显示场景和成功/拒绝，不输出DSN、路径、批准材料或账户清单。所有实际命令/退出码的机器清单和日志摘要见外部交付清单；PG原始诊断文件和数据库数据目录不混进产品补丁。

交付为相对本轮start快照的增量：`D:/校零/校零智选-P04-复核-20261118.patch`、`D:/校零/校零智选-P04-复核-20261118-清单.json` 和本报告。清单包含每个变化文件的起始/最终SHA-256、最终完整源文件清单、补丁SHA-256、边界核对、实际 `git apply --check` / apply记录，以及全新 `delivery-replay` 的 build、单测、真实PG集成/实际CLI命令结果。最终补丁验证和关闭状态以清单 `applicationVerification` / `cleanup` 对应证据为准，避免报告或补丁自包含摘要循环。没有初始化Git。

资源关闭判据：只针对本轮data目录pg_ctl stop；复核schema和导入角色为0，其他session为0；停止后pg_ctl status=3、55494监听=0、本轮data路径PG进程=0。相关实际输出写入cleanup记录并由外部清单摘要固定，不关闭其他包资源。

## 尚未覆盖的阶段门禁

| 编号/责任阶段 | 状态 | 精确边界/后续责任 |
| --- | --- | --- |
| PAY-01，P05/G2 | NOT RUN | 真实即时/预约整单，订单/当前骑手归属、取货/履约前序、拒付/少付、收款+送达+收入同事务；本轮只验P04端口全额快照及回滚。 |
| PAY-02，财务/运营/P08 | NOT RUN | 现金实物复点、真实兑付凭据及核实到账、追加差异更正流程；冻结无更正写API，不自造入口，原复点报告不得覆盖。 |
| PAY-03 / TRADE-03，P05/G2 | NOT RUN | 退回验收资格、订单锁、库存恢复、时段释放与资金整单原子性；不替P03/P05判断资格。 |
| PAY-04，财务/运营/P08 | NOT RUN | 真实资金来源、批准人/执行人认证与签字、批准清单版本锁定、独立SHA来源/复核。CLI字符串参数不是外部身份认证。 |
| PAY-05，P05/P06 | NOT RUN | 真实直接报价/提交的禁用映射、三端微信展示。P04渠道列表和端口禁用PASS，不扩张为整条PAY-05通过。 |
| SEC-01/03，P07/P08/部署 | NOT RUN | 真实三端、三域反代、应用启动/预发接线、CSP与生产权限核验、独立安全复核。httptest不等于部署验证。 |
| Clock接线，总控/P05启动集成 | NOT RUN | 本包HTTP新增必需Clock，后续组装须注入已有shared Clock；本轮未改启动接线，缺配置明确503。真实RefundCommands缺口同样保留503。 |
| 专用数据库权限，部署/运营/P08 | NOT RUN | sameDatabaseRole只比较已配置用户名和重叠端点（含loopback/fallback），不是数据库角色认证，不保证识别任意DNS别名；本轮权限证明仅限一次性角色。生产必须独立核实专用受限凭据及授权。 |
| Go race工具链，P07/P08 | BLOCKED | 继承环境缺gcc，本轮未运行race；真实PG并发测试和go vet不能替代race。 |
| 依赖/secret/SBOM/制品复扫，P07/P08 | NOT RUN | 继续对应阶段，不以本轮代码复核代替；没有新增依赖。 |
| G1、预发、上线及余额独立签发 | NOT RUN | 本轮不签发，余额实账NO-GO（保持关闭），由财务/运营/安全/发布完成后续共有及余额门禁。 |

最终本包必需项无未解决FAIL/BLOCKED；上述跨阶段NOT RUN与继承race限制始终保留，不能随本地PASS自动放行。
