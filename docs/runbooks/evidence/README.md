# P07 证据填写与交接

此目录只放可复制的**空模板**，不包含正式演练结果。模板字段 null/空字符串不表示0、通过或资源存在；复制到本轮独立受限证据目录，再填写实际操作。源文件不要原地改成结果。正式G3/G4须填同一候选、预发环境ID、授权号与负责人姓名；本机结果层级明确写LOCAL，不签发正式门禁。

使用 [run-record.template.json](run-record.template.json) 逐命令/人工操作记录，使用 [g4-exercise.template.json](g4-exercise.template.json) 汇总正式演练。原始日志留受限存储，普通索引只放脱敏版本与SHA-256。不能保存DSN、密码、OTP、Cookie、CSRF值、令牌、签名URL、真实电话/地址或现金/退款凭据原文；秘密只用版本ID及受限引用。工具版本和漏洞库时间必须来自该次实际输出。

| 状态 | 含义 |
|---|---|
| PASS | 该层级已实际执行、必测项全部命中且达标，有可复核证据 |
| FAIL | 已执行未达标，首错永久保留；复测另用run_id并链接first_failure_run_id |
| NOT RUN | 尚未执行；不把命令写在文档当实际运行 |
| BLOCKED | 缺授权、资源、前置门禁或必需证据，列条件和下一责任人 |
| OPEN / UNCONFIRMED | 用于诊断状态/根因，不用于把已执行FAIL改成未执行 |
| NO-GO | 未签发渠道发布/生产余额；必须由四方实际签名才另行裁定 |

构建/语法命令可以test_counts=null，并写validation_kind=CONFIG/BUILD；真实测试必须检查匹配数>0、required_skipped=0、全部必测命中。保留退出码及pass/fail/skip，不用退出0替代必测覆盖。扫描记录工具、规则/漏洞库版本与时间、严重性计数及处置复测。Compose/Caddy语法通过只签配置解析，不签请求结果/网络隔离。

每个告警演练保留：故障注入授权、源信号时刻、relay入队ID/持久游标、主通道消息ID、实际接收人、送达/确认时刻、升级与备用通道结果、恢复通知、待补跑/去重核对。`order_operational_alert`日志时间不是接收人送达时间。

PITR记录T0、T_last_ack、T_recovered_ack、T_ready与每个业务键/恢复点/WAL/timeline，实际RPO/RTO计算值必须来自时间证据。负载保存脚本和种子摘要、900秒实际区间、50用户/20骑手独立会话、10秒轮询实际次数、每类请求/错误/分位数、不变量前后快照与事务测时来源。严禁把空模板填默认目标数当实测数。

首错档案单独保留两条：P05-PG-CRASH（p05-frozen-races-ten-02，FAIL、OPEN、UNCONFIRMED）与P06-NODE-CRASH（首次回放0xC0000005，FAIL、OPEN、UNCONFIRMED，当次PG未崩）。复测通过只追加证据，不能删除首错或宣称根因关闭。出现本轮新首错同样追加。

正式移交索引按四栏分列：P07模板与CI、本轮G3支持、G4的SEC-04/OPS-01–04、P08/G5输入。CI本地核验、实际远端CI运行、实际仓库环境保护分开；已写workflow不等于远端运行或保护启用。当前未授权预发：G3/G4 BLOCKED、每个正式演练NOT RUN；COD及余额G5 NO-GO。
