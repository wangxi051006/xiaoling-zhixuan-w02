# P01 本地验收复核记录

日期：2026-11-18，Asia/Shanghai。结论：**P01 本地验收通过**。本轮重新执行全部必需检查，发现的域内缺陷已按失败用例、最小修复、复测闭环处理。此结论不签发 G1，不代表真实三端登录、供应商、预发或上线验收；本轮未启动 P05，未派发其他包。

## 基线、快照与写域

- 工作区 `D:/校零`；开发根 `D:/校零/校零智选-开发`。原型 `D:/校零/校零智选` 只读。没有初始化 Git、提交或推送。
- P00 基线为 `D:/校零/校零智选-P00-复核.patch`；实测 SHA-256：`1831037543955abf19a543416112bc75971652cb7fd304b78f7d943fd21c8250`，与指定值及 `校零智选-P00-复核清单.json` 一致。未重复应用完整 P00 补丁。
- 按 P00 清单逐文件核对，仅 `backend/go.mod`、`backend/go.sum`、`backend/internal/platform/migrate_pg_test.go` 存在交接中已记录的既有差异；记录见 `D:/校零/.p01-recheck-20261118/p00-differences.json`。保留这些共享依赖及 P01–P04 成果。
- 起始独立副本：`D:/校零/.p01-recheck-20261118/start`。起始摘要清单：同目录 `start-manifest.json`，120 个源文件，排除 node_modules、dist 和 Git 元数据；清单 SHA-256：`7865bb13c4675a19c78db290ce656d0d235fc1d62f3c0394793585bf6228d9a8`。
- 本轮仅变更 `backend/internal/identity/{http.go,otp_pg_test.go,oidc_pg_test.go,recheck_test.go,recheck_pg_test.go}` 和本报告。没有改既有迁移或新增迁移；共享路径、其他包和原型没有本轮改动。起始及最终逐文件摘要见交付清单。

## 缺陷与修复

| ID | 本轮复现的 FAIL | 最小修复及复测 | SEC |
| --- | --- | --- | --- |
| P01-R01 | 401、409、503 返回的三个错误码不在冻结 ErrorCode 枚举中 | 分别映射为 UNAUTHENTICATED、INVALID_STATE、NOT_READY；保持 HTTP 状态和三字段错误包络。直接读取冻结契约的回归测试转绿 | SEC-02/03 |
| P01-R02 | 未提供会话存储时，`/me` 把基础设施缺失错误报告为 401 | 入口检查 policy、sessions、pool、clock，缺失时明确 503；负例转绿。真实存储下缺 OTP/IdP 的 503 另外执行 | SEC-02 |
| P01-R03 | OTP 校验请求的空 challenge 或非法验证码格式返回 401，未按格式错误返回 400 | HTTP 层先检查冻结请求形状；合法形状的错误凭据继续由 OTP 域验证。回归测试转绿 | SEC-02/03 |
| P01-T01 | 既有 identityPool 加载全部 W1 迁移，无法证明 P01 独立验收 | 测试夹具只加载嵌入文件中的 000/010；空 schema 实际重放，断言没有其他编号段，并验证再次执行时历史摘要、时间和已有数据不变 | SEC-01/03 |

失败及转绿日志均在 `D:/校零/.p01-recheck-20261118/`：`red-contract-migrations.log` / `green-contract-migrations.log`、`red-unavailable.log` / `green-unavailable.log`、`red-otp-http-shape.log` / `green-otp-http-shape.log`。各红灯命令退出 1，均为预期断言失败；绿灯退出 0。新增其余用例用于补足实际验收覆盖，没有重写原有 OTP、OIDC、会话或事务实现。

## 验收矩阵

以下 PASS 均限定本轮域内自动化和真实 PostgreSQL；证据为测试名称及后文实际执行日志。

| 项目 | 状态 | 本轮证据和边界 | SEC |
| --- | --- | --- | --- |
| OTP 有效期与到期边界 | PASS | PostgresOTPExpiryAttemptsDeliveryAndDigest：持久化到期为 5 分钟，到期时拒绝 | SEC-02 |
| OTP 一次性及并发双验 | PASS | PostgresOTPThrottlesAndConsumesExactlyOnce：同步屏障启动两次验证，恰好一次成功 | SEC-02 |
| OTP 错误次数上限 | PASS | 真实 PG 持久化五次错误尝试，随后正确值仍拒绝；单测另验格式正确但值错误的路径 | SEC-02 |
| 账号及来源限频 | PASS | 电话一分钟冷却、每小时五次；来源每小时二十次；并发同账号发送仅一次成功，IPv6 同 /64 合并限额 | SEC-02 |
| 发送失败拒绝登录 | PASS | 短信替身故障后 PG 中 sent 未成立，验证码不能验证；未暴露适配器原始错误 | SEC-02/03 |
| 注册状态不泄露 | PASS | 已有与不存在账号的请求均为 202，只返回 challenge_id / expires_at；请求阶段不查询账号 | SEC-02/03 |
| OTP 不明文存储或日志输出 | PASS | PG 存 32 字节 HMAC 摘要；HTTP 日志私密值负例通过，测试适配器只存在于 `_test.go` | SEC-03 |
| OIDC 签名、issuer、audience、有效期 | PASS | 本地 TLS IdP + 成熟验签组件；错误签名/发行者/受众/过期令牌均拒绝 | SEC-02 |
| nonce/state 与重放拒绝 | PASS | 错误 nonce、未知 state、跨浏览器、跨入口、state 到期和重复回调均拒绝；未绑定请求不会到达 token endpoint | SEC-02 |
| PKCE 及固定回跳 | PASS | 校验实际 S256 challenge；IdP 检查 verifier、redirect_uri 和客户端认证；错误交换拒绝。HTTP 拒绝自选回跳，成功只回配置入口根路径 | SEC-02 |
| MFA、本地绑定、停用 | PASS | 缺可信 MFA、缺本地绑定、停用账号拒绝；StaffClaimsFailClosed 与 EntrySeparationAndRecentMFA 验时间及授权钩子 | SEC-01/02 |
| 不信任外部应用角色 | PASS | 合成 IdP 声称安全管理员仍只获得本地 FINANCE；HTTP 角色数据来自本地数据库，输入解码拒绝未定义字段 | SEC-01/02 |
| 会话轮换与退出 | PASS | 新登录替换旧摘要，旧令牌拒绝；HTTP 退出后查找失败；数据库摘要不能直接作为 bearer 使用 | SEC-02 |
| 停用及角色变化撤销 | PASS | 停用再启用、骑手禁用再启用、员工角色撤销再授予，旧会话均不能恢复 | SEC-01/02 |
| 三入口 Cookie 隔离 | PASS | 客户 Cookie 用于管理和骑手入口均 401；会话按 entry 查询。此处为合成 HTTP Host，真实三域另列待验 | SEC-01/02 |
| Cookie 属性 | PASS | 会话及员工浏览器绑定 Cookie 为 Host-only、Path=/、Secure、HttpOnly、SameSite=Lax；员工 flow 设置短有效期 | SEC-02 |
| Origin、CSRF 与伪造来源 | PASS | 错误/缺失/null/重复 Origin、错误/重复 CSRF、伪造 forwarded origin 拒绝；不从 X-Forwarded-For 推导短信来源 | SEC-02 |
| 近期 MFA | PASS | 特权骑手写动作使用近期 MFA 钩子；安全管理员角色命令同时核对当前数据库会话和 MFA，过期负例拒绝 | SEC-01/02 |
| 地址跨账号及跨站点 | PASS | 跨账号解析/更新/删除拒绝；站外楼栋、错站点写操作及直接存入的跨站点地址不可解析 | SEC-01 |
| 地址输入、软删除及版本 | PASS | 空/过长/控制字符房间、不合法联系方式拒绝；软删除保留记录并增版本、解析不可见；旧版本冲突，旧快照不变 | SEC-01/03 |
| 运营与安全管理员边界 | PASS | 财务/安全管理员不能管理骑手作业状态；运营不能变更角色；安全管理员不能给自己授予角色 | SEC-01 |
| 冻结事务端口 | PASS | 编译实现 IdentityPort；ResolveAddress、AssertRider 读取调用者 tx 中尚未提交的身份及地址、识别 pending disable，调用者回滚后无残留 | SEC-01 |
| 权限、撤销及审计事务一致 | PASS | 注入审计 INSERT 故障，角色/骑手状态/版本和会话撤销全回滚；正常角色/骑手变更审计存在 | SEC-01/03 |
| 状态码及错误包络 | PASS | 冻结枚举核验、request_id 非空、严格三个字段、内部诊断不外泄；格式错误 400、认证 401、权限 403、对象 404、冲突 409、限频 429、不可用 503 | SEC-02/03 |
| 缺适配器明确不可用 | PASS | 未提供 OTP、IdP 或会话基础设施返回 503/NOT_READY，无假登录回退 | SEC-02 |
| 日志和认证参数脱敏 | PASS | OTP/地址/会话日志私密值负例、真实 HTTP OIDC 回调日志负例通过；查询、令牌和 Cookie 动态值未写报告或交付日志 | SEC-03 |
| 000 + 010 空 schema 独立重放 | PASS | 每个 PG 用例新建随机 schema，仅加载 000_001 与 010_001，不依赖 020–040 私有表 | SEC-01/03 |
| 迁移重复执行无副作用 | PASS | schema_migrations 版本、文件摘要、applied_at 均未变，已有标记记录未变；原迁移只读，编号无新增或重复 | SEC-03 |

数据库型新增测试为 `TestPostgresOTPExpiryAttemptsDeliveryAndDigest`、`TestPostgresOTPPhoneSourceLimitsAndConcurrentRequest`、`TestIdentityPortsReadCallerTransactionAndRollback`、`TestAddressValidationAndSiteBoundary`、`TestIdentitySecurityMutationsRollbackWithAuditFailure`、`TestIdentityHTTPRegistrationPrivacyUnavailableAndForgedSource`、`TestIdentityMigrationsStandaloneReplaysExactlyOnce`。其余扩展在既有 OIDC 集成测试及新增 `recheck_test.go`。独立副本详细日志记录 **24 个顶层测试、46 个子用例 PASS，0 FAIL，0 SKIP**。

## 实际命令、目录及结果

日志根记为 `E = D:/校零/.p01-recheck-20261118`。开发后端目录为 `D:/校零/校零智选-开发/backend`；开发根为 `D:/校零/校零智选-开发`。缓存显式放在既有工作区缓存目录，未运行 go get，未修改共享依赖。

| 工作目录 | 命令 | 退出码 / 关键结果 | 证据（相对 E） |
| --- | --- | --- | --- |
| 开发后端 | `go test -count=1 ./internal/identity/...` | 0；PASS，最终执行 0.023s | identity-unit-final.log |
| 开发后端 | `go test -count=1 -tags=integration ./internal/identity/...` | 0；真实 PG，PASS，最终执行 2.008s | identity-integration-final.log |
| 开发后端 | `go test -count=1 ./...` | 0；所有含测试包通过，无测试文件包按工具原样记录 | all-unit-final.log |
| 开发后端 | `go vet ./...` | 0；无诊断 | vet-final.log |
| 开发根 | `node contracts/generate.mjs --check` | 0；60 operations and 8 enums verified | contract-check-final.log |
| 开发后端；移除 TEST_DATABASE_URL | `go test -count=1 -tags=integration -run '^TestPostgresOTPThrottlesAndConsumesExactlyOnce$' ./internal/identity/...` | **1，预期失败**；明确要求独立测试连接，没有静默跳过 | missing-test-database.log |
| 独立起始副本根 | `git -c core.autocrlf=false apply --check <本轮增量补丁>`，随后实际 apply | 0 / 0；无需 Git 仓库，未向开发树重放 P00 | delivery-manifest：applicationVerification |
| E/replay/backend | `go build ./...` | 0；补丁回放后全模块编译 | replay-build.log |
| E/replay/backend | `go test -count=1 ./internal/identity/...` | 0；PASS | replay-unit.log |
| E/replay/backend | `go test -count=1 -tags=integration -v ./internal/identity/...` | 0；24 顶层/46 子例通过，无跳过 | replay-integration.log |
| 开发后端 | `go run E/database-proof.go` | 0；真实 PG 18.6，专用回环端口，临时 schema 残留 0 | database-cleanup-proof.log |
| 工作区 | `pg_ctl -D <本轮数据目录> -m fast -w stop` | 0；本轮实例已停止 | database-stop-final.log |
| 工作区 | `pg_ctl -D <本轮数据目录> status` | **3，预期无进程** | database-stopped-status.log |

命令及工作目录的机器记录另见 `required-go-results-final.json`、`required-contract-result.json`、`replay-results.json`。初始已有实现的单测和 PG 测试本轮也实际执行并通过，后续缺陷由新增负例捕获，未据初始绿灯直接签通过。

工具链实测：Go 1.27.4 windows/amd64、Node v26.13.1；测试 PostgreSQL 18.6。显式 TEST_DATABASE_URL 只指向本轮新建的 `127.0.0.1:55491` 专用实例，角色 `p01_fixture`，数据目录为 `E/pg-data`；未借用此前停止的 G0 端口或应用/迁移/生产连接。TLS IdP 和短信只用本地合成替身。

初始化使用现有 ASCII 路径 PostgreSQL 二进制；沙箱启动因 restricted token 退出 1，获准外部启动后退出 0。停止时沙箱信号权限拒绝退出 1，获准停止后退出 0。只停止本轮创建的实例；数据/缓存不进入补丁。辅助元数据探针曾把数据库地址的 `/32` 文本格式误当非回环，修正为数据库 host() 后通过；不影响任何业务断言。原始补充尝试未计为成功门禁。

## SEC 证据与未覆盖项

| 编号 | 本地结论 | 证据 | 本轮未覆盖 |
| --- | --- | --- | --- |
| SEC-01 | PASS（本包） | 地址归属/站点矩阵、职责边界、停用与重授失效、端口及审计事务回滚 | 全项目角色×对象矩阵、实际三端联调、非实现者独立安全复核 |
| SEC-02 | PASS（本包） | OTP 并发/限频/到期、TLS IdP 正负例、HTTP 回调和入口/CSRF/MFA/会话检查 | 真实短信供应商、正式 IdP、实际三域和可信反代来源传递 |
| SEC-03 | PASS（本包） | 摘要存储、字段白名单、内部错误脱敏、日志私密值断言、审计一致性；交付材料脱敏扫描 | 独立审计副本、反代访问日志策略、预发 CSP、后续订单大厅/历史字段 |

| 开放项 | 状态 | 责任与处理边界 |
| --- | --- | --- |
| 真实短信主体、模板和生产 IdP 配置 | NOT RUN | 运营/总控及 P07/P08；未接真实供应商，本地替身不能作为上线证据 |
| 三域反代、可信来源传递及预发登录 | NOT RUN | P07/P08 与总控；当前直接使用连接端 RemoteAddr，上线前验证边缘限频或受信来源传递，禁记认证查询参数 |
| 应用启动接线 | NOT RUN | 总控共享路径：cmd/api 与 bootstrap 配置后续另审。当前 main 的 platform.NewHandler 仍传 nil API；域内处理器通过不等于应用已提供登录路由，本轮不将其判为域实现 FAIL |
| 独立安全复核和 G1 | NOT RUN | P08 / 总控；本轮不充任独立审计签字，不签发 G1 |
| P00 遗留 race | BLOCKED（继承）；本轮 NOT RUN | P07 在有 C 工具链的 CI 实测；继承缺 gcc 问题，不宣称通过 |
| 依赖安全复扫与历史 picomatch | NOT RUN | P07/P08 执行 govulncheck、npm audit 及制品扫描；本轮无新的已确认依赖漏洞结论 |

本包本地必需项没有剩余 FAIL、BLOCKED 或 NOT RUN。开放项阻止把本地结果解释为 G1、预发或上线放行。本轮没有必须改动共享路径的实现需求；后续接线须按共享写域单独批准和验证。

## 增量交付

交付补丁 `D:/校零/校零智选-P01-复核-20261118.patch` 相对本轮起始快照，仅含上述五个身份源码/测试文件与本报告。保留既有 `校零智选-P01.patch` 及所有旧补丁。补丁 SHA-256、字节数、逐文件起始/最终摘要及应用验证结果，以同目录 `校零智选-P01-复核-20261118-清单.json` 为准，避免报告自包含摘要循环。

独立副本已实际应用代码增量、核对五个源码/测试文件字节一致并完成编译和单测/真实数据库集成回放；最终含本报告的交付补丁另外在全新起始副本执行 apply --check、实际 apply 和逐文件摘要核对。补丁不包含其他包、共享依赖、缓存、数据库或凭据。动态联系方式、房间和认证材料只在合成测试内存或可销毁数据库中构造，不输出到交付日志/报告；日志中的操作名和字段名不作为凭据值。
