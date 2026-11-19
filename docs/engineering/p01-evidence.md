# P01 身份与权限：本地验证记录

状态：域代码与路由注册已实现并通过本地验证；总控接线和预发验证待办，未签 G1。

| 验收项 | 本轮实际验证 | 未覆盖 |
| --- | --- | --- |
| SEC-01 | PostgreSQL 合成账号：地址跨账号拒绝、跨站骑手管理拒绝、财务不能改骑手、运营不能改角色、安全管理员不能给自己授予角色、撤销角色不会因重新授予而恢复旧会话。 | 全项目角色矩阵、预发三域和独立复核。 |
| SEC-02 | OTP 单次消费、并发和限频；TLS 合成 IdP 验证签名、issuer、audience、MFA、本地绑定、入口/浏览器绑定及状态重放拒绝；HTTP 覆盖 Host-only Cookie、来源和 CSRF。 | 供应商生产配置、实际域名和与实际反代一起验证。 |
| SEC-03 | HTTP 日志不含合成手机号、房间、姓名、验证码、challenge、CSRF 或 Cookie；骑手状态和员工角色变化写入追加审计。 | 独立审计副本、反代访问日志策略、预发 CSP 和订单大厅字段核验。 |

验证命令（在 `backend/`）：`go test -count=1 ./...`、`go test -count=1 -tags=integration ./...`、`go vet ./...`。集成使用回环 PostgreSQL 18.6 的独立 schema 和本地 TLS IdP，无真实手机号或供应商调用。

## 总控接线

- `IdentityHTTP.Register` 注册 13 个已冻结的路由。三个入口分别配置固定 HTTPS origin；前置服务保留原始 Host。`Authenticated` 提供后续域需要的会话、角色、来源、CSRF 和近期 MFA 检查。
- OTP 按入口同源和连接端来源限频，当前直接使用 `RemoteAddr`，不会信任外部 `X-Forwarded-For`。反代上线前须配置经过验证的来源传递或边缘限频，避免所有请求共享反代地址的配额。客户必须先有已启用的本地手机号账号、站点和客户角色；验证码请求不查账号。正式开户和来源白名单不从示例数据推断。
- 员工 IdP 需明确固定 issuer、authorize/token/JWKS HTTPS 端点、RS256、`client_secret_basic`、S256 PKCE 与可信 ACR。后台与骑手分别注册 `/api/v1/auth/staff/callback`。会话只使用本地 `(issuer, subject)` 映射，不接受 IdP 的角色字段。
- `staff_login_flows` 只保存 state 和浏览器令牌的摘要；短期 nonce/verifier 只驻留在服务端，成功匹配时原子删除。过期索引用于清理。反代必须关闭认证路径的查询参数日志。
- 运营可修改本站骑手状态，权限绑定变更由单独的安全管理员域命令处理，未新增公共 API。骑手停用、员工角色变化与审计写入及撤销会话在同一事务中。
- 应用进程默认依旧只提供 P00 健康入口。正式短信适配器及配置验收前，不接真实短信、不部署公网；缺失适配器的身份处理器返回 503。P02 可在 G0 后独立实现，P05 必须等待 G1。
