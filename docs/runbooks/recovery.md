# P07 PITR、重建与性能验证手册

日期：2026-11-19，Asia/Shanghai。OPS-03/04 正式演练 **NOT RUN**，G4 **BLOCKED**。此文只给规程，不代表备份/WAL/恢复目标或负载资源已可用。先取得隔离目标授权；生产库、生产资金和公网切流不在本任务授权内。

## 1. 演练合同与证据

发布负责人、DBA、运营、财务、安全在演练记录填：授权号/窗口/隔离实例ID/网络ACL、允许的数据集及脱敏审批、候选与制品SHA、PG/OS/扩展版本、数据规模、恢复点UTC、源/目标timeline、全备ID/摘要、WAL范围与独立审计副本。**必须确认源与目标不同**，恢复操作只落批准目标。目标禁止真实短信/IdP/现金业务出站，复用测试供应商也须单独授权。

默认目标 RPO≤15分钟、RTO≤2小时；其他口径须有事前批准依据。使用获准脱敏生产等价数据，记录表行数/库大小/订单、库存、钱包和事件规模。小空库或本机合成副本只能称预演。已授权脱敏数据未提供、备份/WAL未提供、没有全停写窗口或接收人均为BLOCKED，不自行下载生产库。

RPO与RTO测量不从“恢复命令执行完”推算：

| 字段 | 测量口径 |
|---|---|
| T0 | 演练故障/写入截止开始，外部独立时钟记录；RTO从此开始 |
| T_last_ack | 源在故障前最后一笔确认提交的合成业务事务时间与订单/请求/业务键；独立确认日志保存 |
| T_recovered_ack | 恢复后可证实存在的最新业务提交时间与其业务键；不能用主机时间或最近一条WAL文件mtime替代 |
| 实际RPO | `max(0,T_last_ack-T_recovered_ack)`；列恢复点后被确认提交但丢失的业务键及金额，不隐去允许窗口内丢失 |
| T_ready | 恢复库、应用、配置、身份及核对通过，并获准恢复写入；记录审批时间 |
| 实际RTO | `T_ready-T0`，包含下载、恢复、重建、对账和批准时间；若未达到可写条件RTO仍在计时，不能PASS |

## 2. 备份/WAL准备

DBA先确认全量备份可读取/校验、连续WAL可重放、归档失败与归档延迟告警到人，保留期覆盖恢复点和演练证据窗口。归档必须写独立受限存储；备份凭据不等于app凭据。RPO15分钟需要实际WAL归档时延和低流量边界验证；仅设置archive_timeout不能证明达标。

自管PG的示例配置应由DBA按PG版本、存储、密钥批准，不直接粘贴到生产：`wal_level=replica`、`archive_mode=on`、实际可监控且幂等的归档命令；可用 `archive_timeout=300s` 作为低流量归档上限起点，但必须监测上传/重试额外时延。`archive_command` 成功才算归档；禁用仅返回0的伪归档命令。托管PG使用实际提供商的备份/PITR作业ID与审计输出，不混用下面自管文件步骤。

批准自管隔离目标上的备份操作示例（PGSERVICEFILE/PGPASSFILE受限提供认证，输出目录先核为本轮新目录）：

```sh
pg_basebackup --dbname='service=campus-backup' --pgdata='<new-backup-dir>' \
  --format=plain --wal-method=stream --checkpoint=fast --manifest-checksums=SHA256
pg_verifybackup '<new-backup-dir>'
psql 'service=campus-dba-check' -X -v ON_ERROR_STOP=1 -c \
 'SELECT archived_count,last_archived_wal,last_archived_time,failed_count,last_failed_wal,last_failed_time FROM pg_stat_archiver;'
```

流式附带的WAL保证备份完成所需范围，不能替代备份之后连续归档。从独立存储随机取回并校验一组WAL及备份；记录无法读取/缺段立即BLOCKED。不在证据打印完整存储签名URL、密码或访问令牌。备份清单验证通过仍需实际PITR。

## 3. 全停写、隔离恢复与应用重建

1. 运营保存未完订单/现金交接/待退款/待归架清单；财务保存批次、未复点及退款凭据受限引用。先按incident关ORDERS_ENABLED/DISPATCH_ENABLED并重启，确认新事务被拒。
2. 由指挥人批准全停写：网关隔离API写流量，停止campus-api（worker随之停），停止balance-import、迁移、脚本及任何其他写入者；DBA确认没有业务写连接/活动写事务。仅关闭业务开关不能全停写。记录最后确认事务、WAL位置/归档状态和T0，不手工改业务表。保留事故源原盘/快照与日志，不覆盖。
3. DBA再次核源实例ID、目标实例ID/新空目录/目标端口与ACL。目标不能承接任何原流量或真实供应商调用。使用同一PG主版本及已确认兼容小版本/扩展，核二进制摘要与密钥版本；不能拿不兼容主版本直接读数据目录。
4. 从已核验备份恢复到新目标，安装归档取回命令与受限凭据；自管PG设置下面恢复项，创建 `recovery.signal`，由PG服务账户启动目标。取回命令只从已批准WAL前缀读取并核校验，不执行备份内容中的任意脚本。

```text
# 仅获准的新隔离目标；占位符须由DBA解析
restore_command = '<approved-wal-fetch-command> %f %p'
recovery_target_time = '<approved-RFC3339-UTC-point>'
recovery_target_timeline = '<approved-source-timeline-number>'
recovery_target_action = 'pause'
hot_standby = on
```

自管目标的目录准备顺序：DBA把备份复制/解包到新的空 `<approved-target-pgdata>`，核属主为PG服务用户、模式0700；将恢复配置追加到目标专属配置而非源目录，再由该服务用户执行 `touch <approved-target-pgdata>/recovery.signal`，用 `pg_ctl -D <approved-target-pgdata> -l <restricted-target-log> start` 启动。路径、端口、socket目录和listen_addresses必须在资产单核定，不能与源实例共目录/端口；不得对未核路径使用递归删除。托管目标按批准提供商API创建新实例并保存作业ID。

5. 记录PG恢复日志中的实际目标、回放终点及timeline；未到指定目标、WAL断档、校验失败均FAIL，保持隔离。暂停态只读核 `pg_is_in_recovery()`、`pg_last_wal_replay_lsn()`、`pg_last_xact_replay_timestamp()` 及业务键；这些技术时间仅辅助RPO，不代替确认提交对照。确定恢复点无误后，DBA在目标受限连接执行 `SELECT pg_wal_replay_resume();`（在到达目标的pause状态恢复回放将结束恢复），随后查询 `SELECT pg_is_in_recovery();` 必须为false并核新timeline。若目标不正确则关目标实例，按批准的新恢复点重新准备，不能直接允许应用写入。托管PG使用其明确的完成恢复接口。保留前后日志，仍保持应用关闭，执行一致性核对。
6. 新主机按deploy重建非root用户、固定版本目录、CA、api.env、Caddy及网关策略；秘密从独立密钥管理恢复。**不执行默认迁移或升级**，先将schema_migrations与恢复点所用构建逐项核对；确需前向迁移另经审批和预演。旧构建不能读取恢复结构则保持停机交总控处理。
7. 用campus_reconcile只读角色执行下列SQL，运营/财务独立复核结果；允许证据目录受限。不能用app角色临时增加SELECT敏感表权限。

```sh
# cwd: 固定候选根；PGSERVICEFILE/PGPASSFILE由受限会话提供
psql 'service=campus-reconcile' -X -v ON_ERROR_STOP=1 \
  -v station_id='<approved-station-id>' -v local_date='<Shanghai-YYYY-MM-DD>' \
  -f docs/runbooks/reconciliation.sql > '<restricted-run-dir>/reconciliation-before.txt'
```

8. SQL的库存reserved、时段used、钱包金额/订单账本、批次金额、COD收款、配送收入、重复活跃骑手任务错误计数应为0；逐个非零说明工单及责任人。实存库存用获准基线+流水delta+实物盘点，不能假设数据库初值为0。COD按应收创建日、收款日、批次local_date、退款兑付日各自上海日期核；这些总数不是同一订单集合，不能直接相减称资金差异。非零批次差额、未上缴、待退款为待办清单，财务核真实交接凭据，不能自动清零。
9. 恢复点之后已确认提交但未恢复的订单/现金/库存动作由运营和财务逐笔隔离登记，重建或补偿方案交总控/对应域审批；不能换幂等键盲重放，更不能直接SQL补金额库存。保留独立审计副本与业务日志交叉核，提交不明必须核服务端结果。
10. 先在阻断所有外部流量和真实供应商出站的情况下启动API，维持两开关false；启动即运行worker，没有只读/worker-disable配置，必须事前批准其对恢复库的写入。观察≥30秒租约恢复及多轮扫描，用第二份只读快照比较outbox：到期工作补跑一次，未来available_at任务可留，finished事件不人工复位；相同event_id由relay去重。核重复唤醒/收入/扣退为0，并核外部告警送达。终态不再改写。
11. P08/运营/财务/DBA确认所有差异处置、审计、身份与存量合成闭环。记录T_ready与实际RPO/RTO；恢复点/规模不符或超限为FAIL。仅经发布负责人批准，才在本次**隔离演练目标**恢复获准写流量、开相应开关并重启；本任务不切生产。保存恢复后核对和15分钟观察，清理仅本轮目标进程/库/网络故障，保留受限证据。

业务停新单时存量退款、上缴和归架应继续；全停写窗口无法执行这些命令是预期，要记录暂停/恢复时间而非宣称仍可写。恢复过程中发现数据不一致、崩溃或确定产品缺陷，通知总控重开受影响G2/G3/G4，旧首错保留。

## 4. OPS-04 15分钟负载合同

须在冻结候选、同一获准预发拓扑实测；没有负载工具脚本、事务测时或资源即NOT RUN/BLOCKED。记录应用/PG规格与版本、网络时延、TLS、数据规模（SKU/实存/窗口容量/订单历史/钱包）、冷暖缓存、固定随机种子、工具版本、时钟源及压测脚本SHA。合成客户50个和骑手20个均独立会话，正式OTP/IdP预先合法登录，不把测试签发器放制品。

持续900秒，50并发用户按批准mix执行列表/报价/新单/查询/合法取消，20骑手每10秒轮询任务（独立10秒节拍、抖动范围和退避规则留档）；预计骑手轮询约1800次，偏差解释、实际统计为准。下单负载需覆盖同账号多笔进行中的货到付款订单，并独立统计成功下单与业务冲突。另用同步屏障覆盖最后库存/最后容量/双抢/双扣，不以随机负载声称证明全部竞态。

每个端点记录请求量、2xx/4xx/5xx/网络失败、P50/P95/P99、成功下单数、业务冲突数及持续时间；总5xx比例=所有HTTP5xx/所有HTTP响应，另列网络失败/超时比例，不隐藏无响应。列表P95≤200ms，下单本地事务P95≤500ms，HTTP5xx<0.5%。客户端端到端P95可另报但不能冒充事务P95；当前http_request无duration/路由、无事务计时，P00/P05需提供可审的低敏聚合计时或批准测试仪表，未补齐则事务指标BLOCKED。

负载前后运行reconciliation.sql并核业务唯一键、零超卖/超预约/双扣/重复收入；保留库存基线、容量、现金/余额及outbox变化。性能目标全部达标且不变量0差异才能PASS；低并发、短于15分钟、本机规模、无真实预发或缺指标仅记预演，正式OPS-04 NOT RUN/BLOCKED。不得虚构样本数、P95或吞吐结果。

## 5. 交P08与G5的输入

| 责任方 | 必交输入/裁定边界 |
|---|---|
| 总控/P00 | 固定接线候选、迁移与制品SHA、完整runtime/审计只读授权、受控事务计时、共享契约与CI缺口修复；重开门禁裁定 |
| P01 | 真实测试OTP/短信与Staff注入、可信配置键、员工近期MFA、站点/角色初始化授权脚本、可信代理来源限频方案 |
| P04/P05 | COD差额处置/只读导出契约、历史退款/上缴/归架及outbox补跑证据；余额独立来源授权；不变量/前向兼容证明 |
| P06/总控 | 三HTML正式接线、同候选三端G3矩阵、非零COD差额浏览器分支、P06 Node首错独立诊断 |
| P07/SRE/DBA | 资产/授权/私网TLS/最小权限、独立审计、真实到人告警、固定版升级回切、PITR及RPO/RTO、15分钟负载原始与聚合证据；P05 PG诊断OPEN记录 |
| P08独立复核员 | 非实现者安全复核SEC01–04及功能/恢复矩阵，逐项PASS/FAIL/NOT RUN/BLOCKED，确认首错未覆盖 |
| 运营/财务/安全/发布 | 分别签COD渠道单：现金交接/拒付退回/值班/差额、收款上缴退款、独立审计与权限、发布配置与门禁；余额另单签真实来源与账本/导入责任 |

缺任何必须实测或四方签名，G5均NO-GO；COD签发不自动打开余额。签名只由实际责任人提供，P07不代签。即使G5通过，仍需单独生产执行授权。

机制参考：[PostgreSQL 18 PITR](https://www.postgresql.org/docs/18/continuous-archiving.html)、[恢复目标](https://www.postgresql.org/docs/18/runtime-config-wal.html#RUNTIME-CONFIG-WAL-RECOVERY-TARGET)、[pg_verifybackup](https://www.postgresql.org/docs/18/app-pgverifybackup.html)。执行时按实际PG版本核对，不据此假设提供商能力已开通。
