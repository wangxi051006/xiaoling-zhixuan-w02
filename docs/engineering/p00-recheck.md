# P00 / G0 复核与补齐记录

日期：2026-11-18（Asia/Shanghai）。总控范围为 P00 工程和公共契约。已读取 00-master、01-foundation、10-contracts、链接架构及验收规则。

结论：G0 本地工程检查通过。此前缺少独立交付的合成样本、迁移编号冻结表和可逐文件核对的补丁摘要清单，本次补齐。未实现域逻辑，未接微信，未部署或推送，本轮未派发 W1。此结论不代表 G1–G5、安全验收或上线放行。

## G0 清单

| 检查项 | 本次结果 |
| --- | --- |
| 原型只读 | 390px / 1440px × 用户/配送/管理，共 6 个浏览器检查通过；11 个原型源文件 SHA-256 与开发副本一致 |
| 安全施工与基线 | 工作区、原型和开发副本均无 Git 仓库，未找到授权 Git 基线；继续独立副本、串行移交补丁，不初始化 Git |
| Go + pgx 空骨架 | 独立应用原 P00 补丁后，Go 单测、vet、构建通过；进程健康 200，真实 PG 就绪 200，不可用 PG 就绪 503，未接线业务 404 |
| 公共契约 | OpenAPI 3.1：54 路径、60 操作、8 组枚举；Go/JSON/YAML/TS 产物一致；P01–P04 端口均接调用者 pgx.Tx，Clock 可注入 |
| 防漂移 | 隔离副本中分别修改枚举、删除下单幂等参数、修改生成 TS：对应校验均失败；恢复后再次通过 |
| PostgreSQL 与 000 | 独立 PostgreSQL 18.6，仅 127.0.0.1:55490；并发重放、失败全回滚、摘要变更拒绝、审计只追加和运行角色权限通过；缺 TEST_DATABASE_URL 的负例明确失败 |
| 三端共享脚手架 | 独立 P00 与开发副本分别执行 npm ci、typecheck、2 个请求单测、build、3 个 E2E 通过；每端覆盖两个视口、未知路径及跨端入口 404 |
| 生产 mock 守卫 | 设置 VITE_ENABLE_MOCK=true 后，构建因 Mock services are forbidden in production builds 明确失败；不以缺依赖导致的失败作为通过 |
| 样本和编号 | 新增 contracts/samples.json 的 6 份纯合成样本及 p00-migrations.md；样本经 JSON Schema 2020 校验，预约缺窗口、即时指定窗口、下单带金额 3 个负例均被拒绝；日期格式未纳入此次样本校验 |

## 基线与补丁

原始 P00 补丁：`D:/校零/校零智选-P00.patch`，SHA-256 为 `f2579eabc9da52a6e8afb17bfca6ffb81c4a94e6d759e5362633a398d658173b`。原补丁保留不覆盖。

本次完整 P00 交付为 `D:/校零/校零智选-P00-复核.patch`，逐文件及补丁 SHA-256 见同目录的 `校零智选-P00-复核清单.json`。这是可替代原补丁的完整空骨架，不能在已应用原补丁的目录重复应用；不包含 P01–P04 域实现、010–040 迁移、OIDC 依赖、node_modules、dist、本机数据库、凭据或原型文件。

在**新的独立原型副本根目录**先运行 `git -c core.autocrlf=false apply --check <补丁绝对路径>`，再去掉 `--check` 应用；无需 git init。清单记录了每个交付文件的 SHA-256 和字节数。Windows 禁止应用时改行尾，避免破坏迁移摘要和生成文件的精确内容。

开发副本已有 P01–P04 成果；其 go.mod、go.sum 及 platform/migrate_pg_test.go 相对原 P00 基线有既存变化。本轮保留这些变化，只补齐 P00 文档和样本。P00 独立复核目录为 `.p00-local/p00-recheck-20261118`；所有变异负例只在该隔离副本中临时修改并恢复。开发树的既有迁移不混入新 P00 补丁。

## 实际命令与结果

使用工具链：Go 1.27.4、Node 26.13.1、npm 11.20.0；Go 模块最低版本 1.25.0；pgx 5.9.2、Vue 3.5.45、TypeScript 5.9.3、Vite 7.3.8、playwright-core 1.65.1，均锁定在交付清单中。Go 缓存通过 GOCACHE/GOMODCACHE 指向工作区的 `.p00-local`；不将本机缓存交付。

| 目录 / 命令 | 结果 |
| --- | --- |
| 工作区 / `git status --short --branch`、两副本的 `git rev-parse --is-inside-work-tree` | 均报告不是 Git 仓库；不创建基线提交 |
| 工作区 / `git -c core.autocrlf=false apply --directory=.p00-local/p00-recheck-20261118 校零智选-P00.patch` | 43 个原基线文件成功应用 |
| P00 根 / `node contracts/generate.mjs --check` | PASS：60 operations and 8 enums verified |
| P00 backend / `go test -count=1 ./...`、`go vet ./...`、`go build -o D:/校零/.p00-local/p00-recheck-api.exe ./cmd/api` | 均 PASS |
| 开发 backend / `go test -count=1 ./...`、`go vet ./...` | 均 PASS；未修改域代码 |
| P00 和开发 backend / `go test -count=1 -tags=integration ./internal/platform/...` | 均 PASS，真实 PG；P00 的 000 测试与开发树的共享测试分别执行 |
| P00 backend / 移除 TEST_DATABASE_URL 后运行 `go test -count=1 -tags=integration -run ^TestMigrationAtomicAndDigestChecked$ ./internal/platform` | 预期 FAIL：TEST_DATABASE_URL is required |
| P00 根 / `node tools/api-smoke.mjs D:/校零/.p00-local/p00-recheck-api.exe` | PASS：live 200、ready 200/503、unset route 404 |
| 两份 web / `npm ci --offline --cache D:/校零/.p00-local/npm-cache --ignore-scripts --no-audit`，然后 typecheck/test/build/test:e2e | 均 PASS；离线安装使用锁文件和既有缓存，无更新依赖 |
| 开发 web / VITE_ENABLE_MOCK=true 后 `npm run build` | 预期 FAIL，确认是 Vite 的生产 mock 守卫 |
| 开发根 / `node tools/prototype-smoke.mjs D:/校零/校零智选 D:/校零/校零智选-开发/web/node_modules/playwright-core/index.mjs <Chrome绝对路径>` | PASS：6/6；Chrome 为 C:/Program Files/Google/Chrome/Application/chrome.exe |
| 工作区 / `node .p00-local/p00-contract-audit.mjs .p00-local/p00-recheck-20261118 .p00-local/openapi-validator` | PASS：Swagger Parser、Ajv2020 合成样本校验及 3 个契约漂移负例；临时审核工具不进入交付补丁 |
| P00 backend / `go test -race ./...`，设置 CGO_ENABLED=1 后重试 | BLOCKED：先报告需要 cgo，重试报告 gcc 不在 PATH；未计入通过 |

PostgreSQL 初始化先在沙箱内因 restricted token 和中文二进制路径编码失败。经工具权限批准，将已有 PostgreSQL 二进制复制到 ASCII 临时目录，使用 `initdb -D <临时数据目录> -U p00_fixture --auth=trust --encoding=UTF8 --locale=C`，再以 `pg_ctl -o "-h 127.0.0.1 -p 55490" -w start` 启动独立合成测试实例后通过。没有采用生产数据库。测试完成后停止该实例，不改其他进程。

## 未完成项与后续边界

- P07：在有 C 工具链的 CI 执行 go test -race ./...；本机失败不视为已执行通过。
- P07/P08：重新运行完整 npm audit、npm audit --omit=dev、govulncheck，并处理历史 picomatch 告警。本次未访问 registry，不声明告警当前状态；历史扫描不是当前放行证据。
- Git 基线授权仍未取得：当前交付采用摘要固定的补丁；不自行初始化仓库、提交或推送。
- P01–P04 既存实现、本轮以外的 G1、真实 API 联调、预发入口部署、恢复演练与生产签字不在此次 P00 验收范围。三域规则已冻结，真实网关配置由 P07 验证。
