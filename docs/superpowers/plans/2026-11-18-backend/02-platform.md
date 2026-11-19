# P01 · 身份、权限与地址实施

> G0 后独立执行。先写失败用例，再提供最小实现；以 [契约](./10-contracts.md) 为准，不接触订单、资金或共享接线。

**写域：** `backend/internal/identity/{http.go,otp.go,oidc.go,session.go,authorization.go,address.go,rider.go,identity_test.go}`、`backend/migrations/010_001_identity.sql`。目标：三个入口拥有不同信任边界与可撤销身份。

## 执行步骤

- [ ] 建客户 OTP 用例：短时有效、一次性消费、错误尝试封顶、按账号/来源防刷、发送失败不绕过、响应不泄露手机号是否已注册。实现服务端 challenge，验证码不可在日志或数据库明文存放；使用成熟密码学随机源，隔离短信适配器。测试替身不能在生产配置中加载；真实短信主体和模板由运营确认。
- [ ] 建员工 OIDC 用例：错误 issuer/audience/nonce/state/回跳被拒、缺 MFA 或本地绑定被拒、停用账号会话撤销。使用成熟 OIDC 组件验签，角色由本地权限映射；轮换会话，Cookie 只在对应入口，写入检 CSRF 与来源，固定允许的回跳地址，不以公网入口的 header 直接推导身份。
- [ ] 建校验：普通客户不得读另一个账号的地址；站外楼栋、空房间和不合规手机号拒绝；后台/骑手读各自允许的字段。提供 `ResolveAddress`、`AssertRider`；地址软删除不改旧单快照。管理接口仅运营角色改变骑手状态，安全管理员的角色/绑定操作独立于运营角色。服务楼栋与单站点初始化用生产必配清单，不内置演示电话和房间。
- [ ] 校验私有日志只记内部 ID、请求 ID、动作和结果；入口分离用负例证明客户 Cookie 在骑手/管理入口无效。敏感写入管理员/财务近期 MFA 校验的代码和测试以本包提供公共钩子。
- [ ] 本包提交协议/表索引描述，向总控申请接线；G1 合并时用三域和多角色合成账号回归。

**命令：** `go test ./internal/identity/...`、`go test -tags=integration ./internal/identity/...`（在 `backend/`）。**证据：** `SEC-01/02/03`，涉及短信和员工 IdP 的实际预发检查由 P07/P08 完成。[总计划](./00-master.md) · [验收](./08-acceptance.md)
