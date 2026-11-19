# P07 · 服务器部署与运行实施

> G0 后建模板，G2/G3 后演练，G5 后才允许发布。只写 `deploy/`、`.github/workflows/`、`docs/runbooks/`。不购买资源、不上传真密钥、不直接修改线上。

[总计划](./00-master.md) · [架构](../../specs/2026-11-18-backend-design.md) · [验收](./08-acceptance.md)

## 执行步骤

- [ ] 建 `deploy/compose.dev.yaml` 与 `deploy/{Caddyfile,systemd/campus-api.service,env.example}`。Go 单主机非 root 只监听回环，三个域名各自静态目录和受控 API，管理端入口在 VPN/网关；默认 Host 拒绝，健康、诊断、数据库、运维端口不公网。限制 body、超时和日志暴露。PostgreSQL 用同地域私网 TLS/独立账户。验证未配置时 `prod` 启动失败，且无 `demo/`、mock、余额默认启用或微信支付入口。
- [ ] 在 `.github/workflows/ci.yml` 建可重现构建：锁定依赖、Go test/vet/race、真实 PG 容器、OpenAPI diff、前端构建与 E2E、secret 涉及项、`govulncheck`、依赖审计与 SBOM。发布产物含版本、SHA-256、迁移清单，不含敏感配置；审批保护预发/生产入口。P00 的共享测试命令一项不省略。
- [ ] 在 `docs/runbooks/deploy.md` 记录资源准备、域名与证书、员工 IdP/OTP 的可信配置、站点和角色初始化、迁移角色、密钥轮换、上线时额外需要的运营/安全核验。环境变量/密钥来源必列；承载位置和流量入口须在发布前确认。提供 Caddy 配置核验命令 `caddy validate --config deploy/Caddyfile --adapter caddyfile`（使用非生产参数），CI 的 Compose 测试用唯一项目名运行 `docker compose -p campus-ci -f deploy/compose.dev.yaml config`，不能复用其他窗口数据库。真正的试点使用独立预发实例验真实 OTP/IdP，生产不能选用测试提供者。
- [ ] 在 `docs/runbooks/incident.md` 演练 Go 进程/数据库/队列故障、预约逾期、COD 差异、余额对账失败、登录供应商不可用；验证告警到指定人。给出暂停新下单/放单、防止继续结算差错、保留存量退款/上缴/归架的开关和复原流程。审计日志副本独立且不含个人信息明文；运维查看仅限指定角色。
- [ ] 在 `docs/runbooks/recovery.md` 验证 PostgreSQL PITR 恢复、应用及配置重建、库存/时段/结算/现金与余额对账，并量化 RPO/RTO。使用向前兼容迁移与回切上一构建，验证快照恢复期间停止写入、核对后再启，不在业务跑动中强行反迁移。下单/放单可停，存量事件持久保留并补跑。
- [ ] G4 提交命令、版本、构建摘要、拓扑与私网核验、实际预发告警/恢复证据。G5 批准后才使用生产配置依次上线、检查最小闭环、核对首日结算；本计划不授权实际操作生产。

**证据：** `SEC-04`、`OPS-01/02/03/04`。若未拿到实际资源，模板 CI 仍可验证，但 G4/G5 不能签发。
