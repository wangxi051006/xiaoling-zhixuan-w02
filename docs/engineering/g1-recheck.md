# G1 本地集成核验

执行跨越 2026-11-18/19（Asia/Shanghai）；最终交付日期 2026-11-19。本轮起始副本与证据在 `D:/校零/.g1-p05-20261118-a1`。本报告须配合同名外部清单的最终回归、补丁重放和清理结果判定；未封存前不作为放行签发。

用户明确要求“尚未完成 G1，先核验再继续 P05”。因此本轮先单独封存 G1，停止其数据库后才开始 P05；原提示词禁止自动启动 P05 的约束由这次明确授权更新。没有 Git 授权，未初始化、提交、推送或重用历史补丁。

## 基线与矩阵

P04 清单 SHA-256 `8abda12856c55715e6ca4f8a3da25bce8349454386280f6f6b899dab4828b68c`、P04 复核补丁 `97483b1b48747c9e61f5662338287be60f95db10ba79d65ee32e66f43d67b711` 与文件实际摘要相符；P00–P03 复核补丁亦匹配。147 个文件的集合/内容全部符合 P04.finalFiles，起始清单摘要 `82ba63f44b53f73eb39b37139395007c56142085ebbea639cc6ea774abb70ca6`。固定排除 node_modules、.git、dist、.check。已知 go.mod/go.sum/platform历史测试保留现有字节。

| 检查项 | 冻结依据 | 本轮证据与边界 |
| --- | --- | --- |
| G1-BASE | 优化提示词第二节 | baseline.json、start-manifest.json，完整147文件匹配；独立副本保存 |
| G1-MIG | 00-master/10-contracts，000–040组合 | TestG1CombinedMigrationPrefixUpgradeAndReplay：6份磁盘/嵌入SQL逐字一致，合法含站点/审计/outbox数据的000前缀升级，完整历史name/digest/顺序；身份、库存/预占、窗口/预占、钱包/流水、收款/批次/复点/退款全表全行（含时间）两次重放不变。TestMigrationAtomicAndDigestChecked验证中途DDL全回滚及历史摘要保护。各领域裁剪夹具独立性在全包回归分别保留 |
| G1-PORT | contract/ports.go，10-contracts | g1_ports_test.go编译期四端口实现；各包CallerTransaction/rollback测试在本轮完整集成执行。没有用伪订单服务冒充整单事务 |
| G1-AUTH | P01/P04 HTTP策略 | TestG1RealApplicationSessionsAndMissingRefunds真实Issue/Lookup，同Clock；错误入口404、无会话401、角色403、停用401，伪造角色/站点/forwarded无效，四敏感写无CSRF拒绝，恰15分钟MFA拒绝，读取不加MFA。域内真实身份矩阵仍全量执行 |
| G1-WIRE | P01–P04路由/构造器 | NewApplication统一注册33条冻结路由；共享真实Auth.Authenticated；普通健康/就绪、本地真实会话业务读取通过。退款两写鉴权后明确503。供应商为nil时OTP/员工OIDC明确不可用，没有测试登录/默认运营 |
| G1-CONFIG | P00/P04，10-contracts | COD/BALANCE默认为false，WECHAT永不可启用，未知开关拒绝；三入口必须是显式、不同、规范HTTPS来源。TestG1MissingApplicationDependenciesFailClosed。TestG1RuntimeDomainCapabilitiesAndDDLIsolation以独立NOLOGIN非超级角色实际演练最小读写/审计追加与DDL/历史/账本写拒绝；迁移仍独立api migrate，服务不自动DDL。P04真实CLI受限导入角色本轮重跑。生产完整权限部署模板归P07 |
| G1-REGRESS | G1第七节、08-acceptance | build、unit、串行真实PG、标准并行真实PG、vet及integration vet、契约生成检查；最终清单记录退出码和代码摘要。8个关键竞争顶层测试各10轮；准确PID锁等待/屏障沿用原用例，无sleep抽签 |
| G1-DELIVERY | G1第八节 | 独立起始副本的最终增量补丁，apply --check/实际应用、完整逐文件摘要核对以及重放构建/集成/契约检查由外部清单记录 |
| G1-CLEANUP | G1第六节 | 测试schema/角色/连接残留查询以及只停止本轮55495/指定数据目录，pg_ctl status/端口/所属PID核验以cleanup.json为准；未验证关闭则不能判PASS |

## 共享修复与红灯

生产改动限 bootstrap/application.go、bootstrap/config.go、cmd/api/main.go：入口从仅探针改为真实注册P01–P04；供应商对象通过可选依赖明确传入，缺登录服务只阻断对应登录功能。缺数据库/时钟/来源拒绝构建。日志、request_id、响应脱敏、体积限制沿用platform。

- g1-red-routes-compiled：真实GET catalog预期200实际404，确认路由未接线；随后组装回归转绿。
- g1-red-config：COD_ENABLED=yes被旧配置接受；改为严格显式true/false后转绿。
- g1-red-routes：第一次测试有未使用import，属于测试编译错误，不充当产品红灯。
- g1-green-assembly：缺收款条目的制单期望404，实际409 INVALID_STATE；复核冻结前序语义后修正夹具期望，不改产品规则。green-combined以及后续完整回归通过，原失败日志保留。
- 本轮初始pg_ctl成功启动后，PowerShell重定向流被数据库子进程保持，外层命令尚未返回；用独立metadata查询核验真实127.0.0.1:55495、PostgreSQL18.6、UTC、postgres/g1_p05_fixture后继续，不以“started”替代连接证明。最早两次目标测试早于metadata证明，最终必须在端点核验后重跑。
- 早期命令记录没有逐次源码摘要，属于证据脚本缺口；runner修复后最终回归与交付验证每次保存源码清单/摘要。旧日志保留作为中间诊断，不伪造缺失摘要。
- P04报告的PG XX000原因未确证；本轮串行和标准并行回归均实际执行，不把历史偶发问题删去或断言已经证明为环境原因。

## 路由与依赖交接

P01 IdentityHTTP.Register负责13条auth/me/addresses/admin-riders，依赖EntryPolicy、Sessions(pool/Clock)、Directory(pool)、OTP与StaffOIDC供应商。P02 catalog.HTTP.Register负责7条公开商品/管理商品/库存，管理授权接真实IdentityHTTP；P03 schedule.HTTP.Register负责5条窗口/批次，注入同Clock与Store；P04 settlement.HTTP.Register负责8条支付方式/钱包/COD/退款，注入同Clock、显式PaymentPolicy、真实Authorize。P04近期MFA四写为制单、复点、批准、兑付；两条退款写缺RefundCommands时503，其他独立路由可用。完整路径/角色/请求模式以contracts/operations.json前33项为机器清单，本轮契约check确认未变。

真实OTP/IdP供应商、反代/管理入口VPN、公网域名与完整部署权限尚未核验，归P07/P08，不影响受限本地真实会话集成证明；不可把本地探针/会话测试写成公网可用。

## P05开工交接

- 只写fulfillment、050新增迁移、test/integration；不得直接更新身份、库存、容量、钱包私有表。订单先写入，再调需要引用ID的端口，050补跨模块FK及资格约束，不更改历史SQL。
- 新单幂等/活跃COD守卫或现有订单 → 窗口 → 库存ID升序 → 结算/钱包 → 配送任务/收入。P01地址/骑手锁与账号停用路径需逐条核对；真实整单共tx、故障回滚和竞态归P05/G2。
- ASAP提交以原quote.slotID调用ReserveSlot重校，SelectSlot不接受ASAP携带指定ID；不能重新择窗。释放仅限订单持锁后判断“未分拣且结束前取消”，已分拣/送达/退回不释放。
- 金额整数分，minimum=0结算合法，导入条目须>0。PAYMENT_METHOD_DISABLED/INSUFFICIENT_BALANCE按409分别映射，不能静默换COD。历史退款不受新单开关影响。
- 真实RefundCommands必须同一调用者tx先定位且锁订单/校验站点和DELIVERED，再调用SettlementPort；未交接走取消，未送达退回验收同事务FinalizeReturnSettlement，不能让P04适配夹具进入生产。
- P01 Directory只暴露AssertRider而没有上线状态写端口；P05 rider availability与P01 rider_profiles.available的边界须先给精确共享提案，不能为实现方便直接写P01表。
- P05任务状态/归属/收入、SLOT-03租约与补偿、TRADE/PAY整链均NOT RUN。本报告不签G2–G5，不代表三端、真实资金或上线GO。SEC-04制品/依赖扫描与独立安全复核按08-acceptance归P07/P08；race缺gcc是继承限制，保持NOT RUN/需CI补验，不冒充PASS。

最终状态、命令集合、复核结论、补丁SHA及清理见 `D:/校零/校零智选-G1-复核-20261119-a1-清单.json`。
