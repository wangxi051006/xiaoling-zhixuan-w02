# DSH 实施计划：先修 COD 对账，再冻结单一候选

> 执行时逐项留证；用 `superpowers:executing-plans`，或在获准委派后用 `superpowers:subagent-driven-development`。本文件是实施交接，不是已派单、已合并或已签收的记录。

**目标：** 先关闭 P08-F01 / CP-10，再将 A01 独立复验后改动、经审查的交易修复和依赖整改汇入一个候选，由 P08 在该版本重做验收。

**方案：** 账本保留已持久化的差额，API 返回同一差额，后台显示方向和金额；发布负责人只接收与冻结源码、制品、迁移、配置相匹配的证据。

**技术栈：** Go / pgx / PostgreSQL、Vue / TypeScript / Vite / Playwright、P07 构建与扫描链。

**依据：** `D:/校零/校零智选-开发/docs/evidence/launch/` 中的 `README.md`、`handoff.md`、`cod-retest-04.json`、`security-review.md`；`D:/校零/校零智选-开发/docs/superpowers/plans/2026-11-18-backend/08-acceptance.md`；`D:/校零/校零智选-账号候选-A01/evidence/identity/independent-acceptance.md`。

## 全局约束

- DSH 为总控，开工时逐单登记具名执行人、独立复核人、截止时间和证据位置。01 首先开工；交易输入核验、依赖整改准备可并行。顺序：01 修复及审查 + 经审交易增量 + 依赖整改 → 02 冻结 → 03 正式复扫和 04 同候选回归 → 四方签收。
- 原 P07 封存、P08 首错、A01 独立记录只读。用隔离工作区、合成账号、固定上海业务日及独立 UTF-8 PostgreSQL。不用生产数据库、资金或密钥。本方案不触发生产部署、开关变更或公网切换。

## 01 · 第一张工单：P08-F01 / PAY-02 上缴差额

**主责：财务 + 交易研发 P04/P05；协作：P06 后台、P07 运维；独立复核：P08。** P08 已复现少缴 10 分：1440px、390px 后台均显示 `-10`，而账本为 `+10`。A01 代码里 `web/src/admin/entry.ts` 仍用 `actual_fen - expected_fen`，与 `backend/migrations/040_001_settlement.sql` 的生成列方向相反。下述代码路径从新工作区项目根解析。

- [ ] **财务确认定义。** 应缴是进入同一批次的已收 COD 凭证金额之和，不是下单应收；实缴是第二名财务复点确认的金额。`difference_fen = expected_fen - actual_fen`，整型分；正数少缴、负数多缴、零已对平。未复点是空值、不是已对平；复点也不代表退款到账。
- [ ] **先让回归失败。** 把 P08 的 `verify-cod-discrepancy.mjs` 扩充为 `web/tests/p08-cod-discrepancy.e2e.mjs`；复用 `web/tests/p06-harness.mjs` 启真实 API、PostgreSQL 和浏览器，不能 mock 复点响应。不要直接用绑定旧源码摘要的 `rehearse.ps1` 出新版本证据。旧代码先失败在少缴文案和差额断言；修后本用例纳入必跑 CI。

| 分支 | 应缴 / 实缴（分） | API 与账本差额 | 后台（1440px、390px） |
| --- | --- | --- | --- |
| 未复点 | 1000 / null | `IN_BATCH`，`null`，无复点行 | 尚未复点 |
| 少缴 | 1000 / 990 | `DISCREPANCY`，`+10` | 少缴 ¥0.10 |
| 多缴 | 1000 / 1010 | `DISCREPANCY`，`-10` | 多缴 ¥0.10 |
| 对平 | 1000 / 1000 | `VERIFIED`，`0` | 已对平 |

每个场景单独起种子或 schema，走真实报价、下单、备货、放单、骑手接取与全额现金确认。财务甲制单，财务乙近期 MFA 后复点。以同一 `order_id → receipt_id（当前即订单 ID）→ batch_id` 关联 API 响应、账本逐行结果和后台截图。1000 分是测试种子，不伪造复点记录、不改同一已复点批次来造差额，不用站点日汇总冒充逐单对账。

```js
const delta = expectedFen - actualFen; // 已复点分支
assert.equal(api.id, ledger.batch_id);
assert.equal(api.difference_fen, delta);
assert.equal(Number(ledger.difference_fen), delta); // 单批次，不用于大额汇总
```

- [ ] **修接口、契约和 UI。** `backend/internal/settlement/reconcile.go` 给 `CODBatch` 增加 `DifferenceFen *int64`（JSON `difference_fen`）。新复点用 `INSERT ... RETURNING difference_fen`，已有同人同参结果读同一行，制单返回 `null`。`contracts/schemas.mjs` 将新字段定义为可空有符号整数，运行 `node contracts/generate.mjs` 重生成后 `--check`。`web/src/admin/entry.ts` 不用输入框或反向减法推结果，只按 API 差额选标签，少缴/多缴用 `money(Math.abs(delta))`，保留原业务状态枚举。`040` 已正确，**不改历史迁移、不回填、不改旧账**。
- [ ] **覆盖旧重放。** `backend/internal/settlement/http.go` 直接重放持久化 `response_body`。仅对两个 COD 批次命令的历史缓存缺新字段时，在出参补齐原 `expected_fen - actual_fen`（未复点补 `null`），不改缓存、审计或复点记录，也不重执行。`backend/internal/settlement/settlement_pg_test.go` 固定升级前的旧 JSON，验证同键同参仍成功、同键异参和再次改金额被拒、制单人也被 `409` 拒绝；保留 `recheck_concurrency_pg_test.go` 的防重断言。
- [ ] **验收并交证据。** 在 `web` 跑 `node --test --test-reporter=tap tests/p08-cod-discrepancy.e2e.mjs`。只读关联 `cod_receipts`、`cod_batch_receipts`、`cod_batches`、`cod_batch_verifications`，查同单实收、收入、批次关联、复点及相应审计不重复；对照 `docs/runbooks/reconciliation.sql` 和 `docs/runbooks/incident.md`。交逐笔表、截图、原响应、SQL 和退出码。财务另见证线下差额追溯、退款实际兑付凭证；授权预发补告警到人，记录批次、方向/金额、接收人与送达时间。页面修复不能代替到人和线下实测。

## 02 · 白名单合并与冻结

**主责：DSH/总控、A01、交易、P07/平台；独立复核：安全。**

- [ ] **核验输入。** 独立整改后 A01 是本轮合并基底。本次实算 `D:/校零/校零智选-账号候选-A01` 251 个源码文件的摘要为 `a0483eee634eba3e6faa6b08251e59c76a6e730ed66dbd6b6b094e08ef598ef6`，与独立复验记录一致；排除 `evidence/`、交付报告、`web/node_modules/`、`web/dist/`。它基于封存 P07，已接 P06 三入口，**不含外部 COD RC01 修复**。本次 `D:/校零/校零智选-COD候选-RC01` 不存在；DSH 须提交交易已审 commit 或有 SHA-256 的补丁、共同基线、文件白名单、审查人与否决项关闭记录。未收到可验输入时，交易合并记 `BLOCKED`，不以 A01 或历史 P07 成绩顶替。
- [ ] **干净副本集成。** 仅引入 01 已审增量、白名单交易增量和 03 依赖整改，保留独立来源。重审冲突和生成物，重点查 `backend/internal/fulfillment/`、`backend/cmd/api/main.go`、`web/src/admin/entry.ts`、契约、迁移和锁文件，保住邮件出箱、员工授权和交易事务。`node deploy/ci/gates.mjs wiring .` 验三端入口，不重复打 P06 补丁。产生干净 commit/tag；目录快照先校验并可重放，再导入受控仓库，不冒填 commit。
- [ ] **冻结身份。** 一个 `candidate_id` 绑定 commit、源码逐文件 SHA-256、补丁/审查索引、Go/npm 锁文件摘要及构建工具链；绑定 API、三端静态产物、包、SBOM 和逐件 SHA-256；绑定迁移清单、顺序/哈希、配置模板及脱敏生效配置、环境 ID、开关值与密钥引用版本。保留原 000–050 与 A01 `060` 的字节摘要；若其他经审修复确需迁移，只允许前向追加。密钥值不进包、不进报告。
- [ ] **同源双构建，只封一份。** 在 P07 锁定工具链上对同一 `CANDIDATE_ID`、固定 `SOURCE_DATE_EPOCH`，用 `bash deploy/ci/build-release.sh` 写入两个不同的新目录，对比逐件字节并校验 `VERSION`、`SHA256SUMS`，按 P07 规则封存唯一正式候选包。禁止采用既有 `LOCAL-REHEARSAL` 制品；夹具不打包。部署、取证和签收时核运行制品、迁移与配置指纹。

## 03 · P08 高危依赖处置与同候选复扫

**主责：P01/后端、P00/共享前端；裁决：非实现者的安全复核人。** 以下是 P08 快照，不声称当前公告状态或已经扫描。

| P08 历史项 | DSH 派单与关闭条件 |
| --- | --- |
| `go-jose/v4 v4.1.4`，`CVE-2026-116587 / GHSA-mgcc-cww5-fp2q`，原记 `HIGH / OPEN` | 核当前维护者公告，按记录修复下限 `v4.1.6` 选择经审、无阻断的版本，审 `go.mod`、`go.sum`；用最终 API 的 `go version -m` 与 SBOM 核对实际链接版本，重跑 OIDC/JWK、MFA、员工会话。`govulncheck` 无可达项不豁免包级高危。 |
| `picomatch 4.0.7`（开发依赖），`CVE-2026-112366 / GHSA-fc5q-39xc-vjpf`，原库命中 HIGH | 保留历史非零退出及 P08 记录的 2026-11-18 上游撤回依据；更新到覆盖撤回时间的漏洞库，重扫完整 npm 锁图（含开发依赖），用 `npm ls picomatch --all` 查引入链。由安全具名解释和关闭差异；不以 `npm audit=0`、运行包未含它或单一撤回状态当作关单。 |

- [ ] 冻结后运行 `bash deploy/ci/scan.sh`（源及 Git 历史密钥扫描、govulncheck、含开发依赖的 npm audit/Trivy、源 SBOM），再按 P07 门禁扫同一归档解包后的制品和制品 SBOM；实际交付若含镜像，按镜像 digest 扫。关联源码与制品 SHA、扫描器/漏洞库版本和时间、原始退出码/报告摘要及独立裁决。拒绝空覆盖、旧库冒充新库、添加忽略或降低阈值；有效 HIGH/CRITICAL 未关闭不签。

## 04 · 重跑、记录、签收

**执行：QA/P08、平台、DBA；见证：财务、运营；独立安全复核。**

- [ ] **工程回归。** 候选根跑 `node contracts/generate.mjs --check`、`node deploy/ci/gates.mjs wiring .`、`node deploy/scripts/check-template-contract.mjs`。`backend` 跑 `go test -count=1 ./...`、`go vet ./...`、`go test -tags=integration -count=1 ./...`、`go test -race -tags=integration -count=1 ./...`（有受支持 C 工具链、真 PG）。显式验证空库与合法前缀带数据升级、重放、存量订单/账本不变。`web` 跑 `npm ci`、`npm run typecheck`、`npm test`、`npm run build`、`npm run test:e2e`；显式运行 P06 角色/恢复、真 API+PG 三端业务和新增 P08 对账。零匹配、跳过、工具链或夹具不符不能记 PASS。预发实联调使用封存产物，不是重新打包的测试夹具。
- [ ] **业务与外部链路。** 重跑 P08 的 TRADE、SLOT、PAY、RIDER、UI、SEC；关注库存/时段并发、下单/取送/收款防重、取消/退款/返库。COD-only 用 `COD_ENABLED=true`、`BALANCE_ENABLED=false`、`WECHAT_ENABLED=false`，只在授权环境开测试下单/调度；PAY-03/04 在同制品独立测试配置/数据库跑，不能当余额上线签发。A01 重跑注册/开关、发码/验码/限流、存量绑定、开户权限与停用会话、邮件出箱及故障隔离；预发用获准真实 IdP/MFA、SMTP/TLS 和收件箱，核代理限流、SMTP 故障隐私及仍需的短信供应商。合成证据不得代替真实链路。
- [ ] **授权预发 G3/G4。** 以封存包实测受控入口、TLS、最小权限、告警到人、暂停新单/放单仍处理存量、升级/应用回切、PITR 和冻结负载，按 OPS-01～04 留据。缺授权、环境、账号或外部服务时用例记 `NOT RUN`，门禁 `BLOCKED`。
- [ ] **一个候选一份账。** 在 `docs/evidence/launch/<candidate_id>/` 新建索引：用例、commit、候选 ID、源/制品/迁移/配置摘要、环境 ID、操作与预期/实际、退出码、证据摘要、执行/复核人、时间和结论。敏感原件仅存受限引用。历史版本只列为参考或首错。凡身份不匹配或非该用例允许的配置，不计 PASS；任一阻断项 FAIL/NOT RUN 或有效高危未关，一律 NO-GO。源码、制品、迁移或获准配置变动必须重新冻结编号，重跑门禁，不拼接旧通过记录。
- [ ] **四方签字。** P08 独立复核后，运营、财务、安全、发布在同一候选和对应实际配置上分别签字、记录上海时间。COD 与余额分别出单，COD 放行不自动开余额；生产余额保持关闭，微信保持关闭。未批准不切公网。

**DSH 开工回执：** 先回财务与交易两位具名主责、对账验证窗口、交易已审变更入口。首个检查点是财务确认差额口径及旧版失败证据，不是候选签收。
