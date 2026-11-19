# 校零智选后端实施总计划

> **执行要求：** 逐包实施、测试先行。总控按本页放行批次，执行者只改本包写域。正式开发从 P00 开始；本文不表示代码已实现或系统已通过验收。

**目标：** 单校单站上线即时和预约配送，订单支持货到付款与可控余额；微信只显示「待接入」。

**架构：** Go 单体 + PostgreSQL，Caddy 统一入口，三端 Vue/TS/Vite 静态构建。余额生产默认关闭，Rust 不随首发部署。

**入口：** [HTML 预览](./preview.html) · [架构设计](../../specs/2026-11-18-backend-design.md) · [拓扑图](../../specs/2026-11-18-backend-topology.svg) · [冻结契约](./10-contracts.md) · [验收与安全审计](./08-acceptance.md)

## 1. 施工顺序与文件归属

以下为**将创建的项目相对路径**。目前只有原型，不是 Git 仓库；不在此次文档交付中初始化 Git。正式开工先由总控取得仓库授权，创建经审核的基线（排除 `.check/node_modules/`、临时产物、凭据和证据原件）。同一份物理目录不要开多个写入窗口：用独立 worktree/分支；若暂不使用 Git，只能用独立副本加人工移交补丁，由总控串行应用。各窗口用独立测试 DB/Compose project/端口，不共享生产配置或环境变量。

| 包 | 阶段 / 依赖 | 唯一写入范围与成果 | 门禁 |
| --- | --- | --- | --- |
| [P00 工程与契约](./01-foundation.md) | W0；无 | `backend/cmd/`、`backend/internal/{bootstrap,contract,platform}/`、`backend/go.*`、`backend/migrations/000_*`、`contracts/`、`web/` 的共享脚手架和锁文件 | G0：冻结契约、脚手架能跑、基线确定后才能派工 |
| [P01 身份与权限](./02-platform.md) | W1；G0 | `backend/internal/identity/`、`backend/migrations/010_*` | 登录、授权、地址与脱敏；真实 OTP/IdP 未核验不得开公网 |
| [P02 商品与库存](./03-catalog-stock.md) | W1；G0 | `backend/internal/catalog/`、`backend/migrations/020_*` | 整数分定价、库存交易边界、并发守恒 |
| [P03 时段与容量](./04-slots.md) | W1；G0 | `backend/internal/schedule/`、`backend/migrations/030_*` | 即时/预约同池、容量约束、相同时钟与窗口口径 |
| [P04 收款与账本](./05-settlement.md) | W1；G0 | `backend/internal/settlement/`、`backend/cmd/balance-import/`、`backend/migrations/040_*` | COD 应收/收款/上缴；余额扣、退、导入和对账 |
| [P05 订单与履约](./06-order-delivery.md) | W2；G1 | `backend/internal/fulfillment/`、`backend/migrations/050_*`、`backend/test/integration/` | G2：三模块共用同一事务；整条履约、资金与竞态测试 |
| [P06 三端前端](./07-web.md) | W1 契约/mock；W2/G2 后联调 | `web/src/{user,rider,admin}/`、`web/tests/` | G3：三端真实 API 联调；开发 mock 不进入生产 |
| [P07 部署与运维](./09-deploy.md) | W1 搭环境；G2/G3 后跑预发 | `deploy/`、`.github/workflows/`、`docs/runbooks/` | G4：预发、回滚、告警、恢复演练 |
| [P08 独立验收](./08-acceptance.md) | G2 后验功能，G3/G4 后签发 | `docs/evidence/launch/` 的脱敏验收索引；不修生产代码 | G5：独立安全审计、运营/财务/发布签字，一个渠道一张放行单 |

**总控保留权**：共享迁移 `000_*`、API/YAML 与生成物、启动接线、三端共享构建/依赖、验收基准、合并顺序。P04 的 `backend/cmd/balance-import/` 是例外专属目录。提出跨域变更时，先记录受影响接口、测试和消费方，请总控集中修改共享文件；不要各自改副本。目录写域与授权边界不是一回事，人工集成也不能绕过规则。

**迁移与波次**：每段迁移用全库唯一版本号（如 `020_001_catalog.sql`），只新增迁移；`000` 建单站点和共享表。W1 不依赖其他 W1 私有表，`050` 补订单跨模块外键。每包先在基线与本包迁移上测，再由总控在空数据库从 000 到 050 全量重放。G1 合并 P01–P04、核验共享接口与迁移；此时才派 P05。P06 和 P07 可从 G0 使用契约/最小启动物工作，不能把 mock 或空壳视为通过。G2 后完成真实三端联调；G3/G4 后签 G5。

**推荐开窗法**：先一窗 P00。G0 后开 P01–P04、P06、P07（按资源分两批也可）；总控保留一窗处理契约与合并。G1 后新开 P05。验收员使用隔离预发和只读证据，不与实现者共用角色。单窗口多智能体用同一依赖图，未到门禁的代理不提前改代码。

## 2. 日常合并与落地流程

1. 总控确定基线提交；执行窗口在报告中写明包号、基线 ID、分支、写域、测试 DB 可用性。身份购买/供应商接入、域名和线上批准都不在代理权限内。
2. 执行者读取架构、总计划、契约、对应分包和验收编号。逐条写失败测试，运行确认失败原因；最小实现，绿灯后再做小范围重构；每批有可运行证据。交易并发用真实 PostgreSQL，不能以 SQLite/mock 替代；时钟注入，竞态用同步屏障，不靠睡眠赌命中。
3. 统一运行：后端在 `backend/` 执行 `go test ./...`、`go vet ./...`，CI 执行 `go test -race ./...`。PG 测试通过单独的 `TEST_DATABASE_URL` 在 `go test -tags=integration ./...` 运行；缺 DB 要明确失败，不能伪装跳过。前端在 `web/` 执行 `npm ci`、`npm run typecheck`、`npm test`、`npm run build`、`npm run test:e2e`。P00 提供这些脚本入口；P07 提供完整预发命令和证据索引。
4. 汇报格式：`包号 / 状态 / 分支或补丁 / 改动路径 / 执行命令及结果 / 证据编号 / 跨包提案或阻塞项`。标注未执行项与原因。合并者核对 `git diff --name-only` 对应写域、契约 diff、独立审阅、迁移与回归，再打门禁；有异常退回原包，不在多窗口抢修同一文件。
5. 上线顺序：跑空库迁移和升级演练 → G3/G4 → G5 → 小范围开放 COD → 盯实际 COD 上缴、预约和异常 → 再单独审批是否启用余额。运维开关只停止新下单/分配，已有订单继续核对、退款、回库和收尾，禁止直接手改金额/库存解困。

## 3. 可直接派发的分段提示词

总控先使用 **P00**，其他块按表放行。块内已有任务边界和交付格式，不需要另行拼接。

### P00 · 总控与脚手架

```text
你是校零智选总控，执行 P00。工作区为校零智选根。先读 docs/superpowers/plans/2026-11-18-backend/00-master.md、01-foundation.md、10-contracts.md 和链接的现有架构；原型只读。确认是否有授权 Git 基线，没有只报告并保留安全工作方式，不私自推送。用 Go+pgx 和 Vue/TS/Vite 建最小可执行骨架，冻结 OpenAPI、共享交易端口、迁移编号、测试脚本与脱敏样本；不实现域逻辑，不集成微信/部署线上。共享路径只有你负责。输出 G0 清单、基线提交/补丁、执行的命令和结果、未完成项。G0 未绿不得派 W1。
```

### P01 · 身份与地址

```text
执行校零智选 P01，读取 docs/superpowers/plans/2026-11-18-backend/{00-master,02-platform,10-contracts,08-acceptance}.md；G0 未放行则停止写入。只写 backend/internal/identity/ 和迁移 010 段。实现客户手机号 OTP、员工 OIDC/MFA 映射、会话、站点权限和地址簿，落实权限/失效/CSRF/脱敏测试。OTP 和 IdP 只用隔离测试适配器，不把假登录放进生产，不碰订单/支付或共享配置。按总计划格式报告 P01 分支、改动、测试、证据及阻塞项。
```

### P02 · 商品、定价及库存

```text
执行校零智选 P02，读 docs/superpowers/plans/2026-11-18-backend/{00-master,03-catalog-stock,10-contracts,08-acceptance}.md，等待 G0。只写 backend/internal/catalog/ 和迁移 020 段。测试先行实现规格换算、阶梯促销整数分报价、上架/版本、库存预占/消费/释放/退回/调整，函数沿用调用方传入的 pgx.Tx。重点用真实 PG 验证抢最后一份、取消和取货冲突、三件装和重试。别重算订单金额或修改公共接口；提出变更交总控。报告分支、路径、测试和证据。
```

### P03 · 预约与即时容量

```text
执行校零智选 P03，读 docs/superpowers/plans/2026-11-18-backend/{00-master,04-slots,10-contracts,08-acceptance}.md，等待 G0。只写 backend/internal/schedule/ 和迁移 030 段。实现 Asia/Shanghai 营业窗口、即时选择与预约时段、共享容量原子预占/释放，注入时钟；用真实 PG 验证边界、跨天与并发最后一席。不实现订单状态和后台唤醒，只给订单包确定的时间/容量接口。报告分支、路径、测试和证据。
```

### P04 · COD、余额与对账

```text
执行校零智选 P04，读 docs/superpowers/plans/2026-11-18-backend/{00-master,05-settlement,10-contracts,08-acceptance}.md，等待 G0。只写 backend/internal/settlement/、backend/cmd/balance-import/ 和迁移 040 段。测试先行实现 COD 应收、收款、批次上缴/复点和线下退款记录；余额用受控批次导入、整数分账本、原子扣退和唯一键。只用合成资金数据和隔离 PG；余额新单生产默认关闭，退款保持，禁止充值、提现、转账、伪造上缴、微信回调。消费者传同一 pgx.Tx，不能另开事务。报告分支、路径、测试、对账证据和运营放行项。
```

### P05 · 订单和配送联接

```text
执行校零智选 P05，读 docs/superpowers/plans/2026-11-18-backend/{00-master,06-order-delivery,10-contracts,08-acceptance}.md，G1 后开工。只写 backend/internal/fulfillment/、迁移 050 段和 backend/test/integration/。按规则实现报价、提交共用事务、预约唤醒、取消/归架、备货与放单/抢单/取送/退回、催单消息和收入；COD 已收与送达同提交，拒付不算收入，余额退回不改历史。以真实 PG 测重复、并发、错误注入及回滚，勿绕开其他包端口。报告分支、路径、执行结果、证据和遗留。
```

### P06 · 三端 UI

```text
执行校零智选 P06，读 docs/superpowers/plans/2026-11-18-backend/{00-master,07-web,10-contracts,08-acceptance}.md。G0 后只写 web/src/{user,rider,admin}/ 和 web/tests/；根 index.html、demo/、锁文件、生成契约均不可改。保留 03 手账自绘视觉，按契约用 mock 起步；G2 后改用真实 API，测试从结算到骑手和管理流转。即时与预约明确分开，COD 显示待收，余额按能力与状态，微信只显示待接入，不伪造成功或收入。交三端构建、桌面/移动截图、实联调证据和变更路径；mock 绿不等于验收。
```

### P07 · 部署与运行

```text
执行校零智选 P07，读 docs/superpowers/plans/2026-11-18-backend/{00-master,09-deploy,10-contracts,08-acceptance}.md。G0 后只写 deploy/、.github/workflows/、docs/runbooks/。建单主机 Caddy/Go、私网 PostgreSQL 和独立审计的部署模板、CI、健康与告警、回滚及恢复流程，先本地/预发模拟。生产密钥和真实资金不用作测试，不实际部署或切公网，生产余额默认关闭、无微信入口。G3/G4 提交配置核验、恢复/回滚/告警证据、变更路径和开放项。
```

### P08 · 独立验收与安全审计

```text
你是校零智选独立验收员，执行 P08。读 docs/superpowers/plans/2026-11-18-backend/{00-master,08-acceptance,10-contracts,09-deploy}.md。只用经授权预发、合成账号/资金与脱敏证据，不修改生产代码、不测试未经授权环境。逐 ID 记录实际命令、版本、结果和证据；尤其复核越权、COD 上缴与拒付、余额重复扣退、预约争抢、恢复/RPO/RTO。G5 输出 COD 与余额分别 GO/NO-GO。有阻断项按包号退回修复并复测，不能以扫描通过替代人工审核或编造签字。
```
