# P00 · 工程基线与公共契约实施

> 总控独占；按勾选项 TDD 执行。目标：交付一套能启动、能跑测试、可安全分发的空骨架，不写业务实现。

[总计划](./00-master.md) · [冻结契约](./10-contracts.md) · [验收](./08-acceptance.md)

**写域：** `backend/go.mod`、`backend/go.sum`、`backend/cmd/api/main.go`、`backend/internal/{bootstrap,contract,platform}/`、`backend/migrations/000_001_base.sql`、`contracts/{openapi.yaml,README.md}`、`web/{package.json,package-lock.json,vite.config.ts,src/shared/,user.html,rider.html,admin.html}` 及本包所需共享配置。授权后建立 Git 基线时可编辑根 `.gitignore`。

## 执行步骤

- [ ] 从干净基线记录现有页面与演示验证结果，不把 `node_modules`、密钥、生成预览及本机环境入库。资源锁定、分支/工作树与测试资源分离，记录选择的受支持工具链版本及复现方式。
- [ ] 在 `contracts/openapi.yaml` 列出 10-contracts 的路由、请求体、响应、角色、错误和枚举；写 `backend/internal/contract/{ports.go,types.go,contract_test.go}` 和共享 TS 类型/校验，测试改动枚举、缺 `Idempotency-Key` 契约或生成类型漂移能被发现。生产骨架仅暴露真正实现的端点，尚未接线的路由不返回假成功。
- [ ] 在 `backend/internal/platform/{db.go,migrate.go,http.go,http_test.go}` 实现最小 PostgreSQL 连接池、版本化迁移、事务与 HTTP 错误外壳、请求 ID、body 上限、panic 隐藏、启动配置、日志脱敏和私有健康。先写非法 prod 配置失败、未注册路由 404、敏感信息不入日志、超大请求拒绝、临时数据库不可连时就绪失败的测试。
- [ ] `000_001_base.sql` 创建站点、幂等、只追加审计及持久任务，加入唯一键、租约与索引；生产 migrate 角色独立。先测空库可迁移、版本表重复运行无副作用、错误迁移不半成功。定义可注入 `Clock` 和单包隔离 PG 测试 helper；缺 `TEST_DATABASE_URL` 明确报错，不隐式连接默认库或生产库。
- [ ] 建三个独立静态入口与共享 fetch 客户端；项目脚本包括 `typecheck/test/build/test:e2e`，初始化最小可运行断言、拒绝 prod mock 的守卫、契约构建。约定开发代理/预发三域，各端404无 fallback 到另一个端。三端骨架不触原型。
- [ ] 提交基线与 G0 检查单：JSON/YAML/Go/TS 同名枚举，P01–P04 同一事务端口，测试数据库可从空库复现，所有包明确本地运行方法。未授权 Git 时输出补丁方式，不初始化或推送外部仓库。

**测试命令（开发后执行）：** `backend/`: `go test ./...`、`go vet ./...`、`go test -tags=integration ./internal/platform/...`；`web/`: `npm ci`、`npm run typecheck`、`npm test`、`npm run build`。期望 000 迁移执行、三静态入口可打开、无未实现 API 假成功。**G0 签署人：总控。**
