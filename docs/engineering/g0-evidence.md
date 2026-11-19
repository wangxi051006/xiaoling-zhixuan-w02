# G0 工程基线证据 · 2026-11-18

范围：P00 工程与契约。仅本地隔离环境，无生产数据、公开入口或外部仓库。后续实现仍须按实施总计划依次验证，不能将本记录用于 G1–G5 或上线签发。

总控判定：G0 本地工程门禁的必需检查已于 2026-11-18 重新执行并通过；详见 [本次复核报告](./p00-recheck.md)。补齐独立合成样本、迁移编号表及外置补丁摘要清单。完整依赖审计和 race 的开放项仍须在 P07/P08 前复核；这不授权预发或生产发布，本轮未派发 W1。

| 项目 | 命令或证据 | 结果 |
| --- | --- | --- |
| 原型基线 | `tools/prototype-smoke.mjs`，390px / 1440px，用户、配送、管理三端；11 个原始文件逐一 SHA-256 比对 | PASS：6 个预览无页面异常；原型文件一致 |
| 公共 API | `node contracts/generate.mjs --check`、Swagger Parser 13.1.0、Go 契约测试 | PASS：54 条路径、60 个操作、8 组枚举；OpenAPI 合规，Go/TS 产物未漂移 |
| Go 骨架 | `go test -count=1 ./...`、`go vet ./...`、构建 `./cmd/api`、`tools/api-smoke.mjs` | PASS：回环健康 200，活数据库就绪 200、不可用 503，未接线订单 404 |
| PostgreSQL | `go test -count=1 -tags=integration ./...`，PostgreSQL 18.6，独立测试 schema 与运行角色 | PASS：000 并发、幂等重跑、失败回滚、摘要变更拒绝、审计只追加与最小运行权限 |
| 三端 | `npm ci`、`npm run typecheck`、`npm test`、`npm run build`、`npm run test:e2e`；生产 mock 负例 | PASS：2 个请求单测、3 个 E2E（每端两种视口）；跨端和未知路径 404；生产构建拒绝 mock |
| 生产依赖 | `npm audit --omit=dev`、`govulncheck ./...` | 历史扫描当次无报告项；本次未复扫，不作为当前安全通过证据 |
| 完整 npm 审计 | `npm audit` | OPEN：历史 picomatch 告警，本次未查询 registry，不声明当前是否已有修复版。开发服务仅回环；P07/P08 前复扫并处理 |
| race | `go test -race ./...`、`CGO_ENABLED=1` 后重试 | BLOCKED：前者要求 cgo，显式开启后报告缺少 gcc；需在有 C 工具链的 P07 CI 执行，不得计入通过 |
| 合成样本与负例 | `contracts/samples.json`，JSON Schema 2020 校验；隔离副本变异验证 | PASS：6 份样本、3 个非法请求；枚举漂移、缺幂等头、TS 产物漂移均导致校验失败 |

验证工具：Go 1.27.4、Node 26.13.1、npm 11.20.0。本地 PostgreSQL 仅监听 127.0.0.1，测试库和本地缓存未加入交付件。Git 未授权，使用项目根可应用的 P00 源码补丁和串行本地开发副本；不初始化仓库或推送。
