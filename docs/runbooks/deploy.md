# P07 部署与升级手册

日期：2026-11-19，时区 Asia/Shanghai。此文是模板及操作规程，**没有执行预发或生产部署**。命令只在已获准的隔离目标执行；生产操作不在本任务授权内。状态及证据填写见 [evidence/README.md](evidence/README.md)。

## 1. 候选、门禁与实际边界

默认起点为 P06 封存 source/，206 文件规范摘要 `1835d919cc37e8c569c58288e2a3b411fd00f102828a4361a19adabbc04627dd`；最终制品与接线摘要由总控填写。正式三 HTML 接线仍由总控集成；原封存 HTML 指向 shared/entry.ts，占位构建不能冒充三端交付。本机拟议接线的 16/16 与 MOCK 21 项是继承证据，不能签 G3/G4。

当前正式 G3 **BLOCKED**，G3 预发用例 **NOT RUN**；G4 **BLOCKED**，正式告警、回切、PITR、负载各 **NOT RUN**；G5 COD 与生产余额均 **NO-GO**。没有实际仓库保护、预发授权、资源或签名，不写成已具备。G2 继承本地自动化 PASS，不重签 G1/G2。

实际 `backend/cmd/api/main.go`：同一 API 进程启动 worker，每秒扫描，每轮最多 100 项；不是独立消息队列服务。`ORDERS_ENABLED`、`DISPATCH_ENABLED`、支付开关均启动读取，修改文件必须重启。`NewApplication` 的 OTP/Staff 参数没有在 main 注入；设置任何示例 OTP/IdP 环境变量都不能接通登录。由总控/P01 交正式适配器、配置加载与注入补丁并复测后才能进入 G3。

## 2. 目标拓扑与资源登记

| 组件 | 目标路径、边界与需确认输入 | 责任人 |
|---|---|---|
| 三 HTTPS 入口 | 用户/骑手独立域；管理域仅 VPN/受控网关。三 Host 各一份静态依赖闭包，仅同源 `/api/v1/` 反代。未知 Host、串端 HTML、健康/诊断路径拒绝 | 发布负责人、网络负责人（登记姓名） |
| Linux 应用机 | Caddy :443；Go 非 root `campus`，仅 `127.0.0.1:8080`；SSH 仅堡垒/VPN。CPU/RAM/磁盘/同地域/系统版本/时钟源填资产单，单机为单点 | P07 运维值班 |
| PostgreSQL | 独立预发实例、同地域私网 DNS，TLS 证书 SAN 匹配 DNS，ACL 仅应用、批准的迁移/备份/只读运维来源；不开放公网 :5432 | DBA |
| 审计与监控 | journald/Caddy 日志经受控采集器；审计数据库记录由独立只读导出者转存独立账户的受限追加存储；告警 relay、送达回执与升级值班表 | 安全、SRE |
| 身份/备份 | 测试短信/OTP、员工 OIDC/MFA、归档 WAL、全量备份及隔离恢复目标，分别记录资源 ID/账户 ID/版本/授权 | P01、DBA、发布 |

资产单必须给出三域名、DNS/证书及到期时间、TLS 私钥托管位置、网关来源网段、私网路由/ACL、PG 版本与 CA 指纹、备份/WAL 保留期和审计存储 ID。字段未填是 BLOCKED，模板默认值不代表资源实际存在。预发与生产分别登记，禁止复用身份应用、站点、数据库及凭据。

Linux 布局与 `deploy/systemd/campus-api.service` 对齐：

```text
/opt/campus/releases/<artifact-id>/{bin/campus-api,static/{user,rider,admin},migrations/}
/opt/campus/releases/<artifact-id>/{VERSION.json,SHA256SUMS,migrations.json,artifact-manifest.json,sbom.spdx.json}
/opt/campus/current -> /opt/campus/releases/<artifact-id>
/etc/campus/api.env                    # 0600，仅root/systemd读；campus进程继承环境
/etc/campus/migration.env              # 0600，短时迁移作业专用
/etc/campus/caddy.env                  # Host/静态根/私钥路径/网关网段
/etc/campus/tls/entries.{crt,key}        # caddy可读，API不可读私钥
/etc/campus/postgres/ca.crt             # API/迁移者可读的PG信任根
```

制品按清单验证 SHA-256 后解到新版本目录；目录由发布者拥有，`campus` 只有读/执行。保留上一版本和对应配置摘要。每端 static 只含本端 HTML 与其 assets 闭包；不发布根原型、demo/、tests/、mock、p06-fixture、合成会话签发器、环境文件或微信支付入口。

CI配置的证据制品名为 `p07-backend-<sha>`、`p07-frontend-<sha>`、`p07-p06-real-windows-<sha>`、`p07-security-<sha>`。仅main上的人工workflow_dispatch显式prepare_release且前置检查全绿后准备 `p07-prepared-candidate-<sha>`，内含 `xiaoling-candidate.tar.gz`及校验文件、`VERSION.json`、`SHA256SUMS`、`migrations.json`、`artifact-manifest.json`、`sbom.spdx.json`。这是候选准备，无部署动作；artifact命名不证明远端CI运行/环境保护已启用。P06真实业务夹具依赖pwsh/证书及固定55497，CI使用独立Windows必需job，不能用Linux跳过代替通过；三HTML未集成仍阻断。

## 3. 环境键、来源与轮换

| 实际消费者 / 键 | 值约束、可信来源、轮换方法 |
|---|---|
| API `APP_ENV` | 预发拒绝启动测试及正式运行使用 `production`；仅允许 development/test/production，`prod` 非法 |
| API `HTTP_ADDR` | 显式 `127.0.0.1:8080`；不接受非回环、空生产值、无效端口 |
| API `DATABASE_URL` | 密钥管理系统签出的 `campus_app` URL；私网 DNS + `sslmode=verify-full&sslrootcert=/etc/campus/postgres/ca.crt&connect_timeout=5`。仅这三种查询参数允许；不要加入 search_path/options 等参数。密码 URL 编码，禁止写入制品/证据/命令行 |
| API `USER_ORIGIN / RIDER_ORIGIN / ADMIN_ORIGIN` | 发布资产单的三个不同 HTTPS origin，无路径；与 Host、IdP 回调严格一致，由总控审核 |
| API `COD_ENABLED` | COD 候选按批准配置 true；初始模板可关闭。关闭影响新方式能力，不追改历史账 |
| API `BALANCE_ENABLED` | 生产 false，测试余额另用隔离配置；独立四方签发才可开启 |
| API `ORDERS_ENABLED / DISPATCH_ENABLED` | 试点开放前均 false；开关暂停新报价/下单与放单，均需重启；按 [incident.md](incident.md) 验存量流程 |
| API `WECHAT_ENABLED / ALLOW_TEST_AUTH` | 留空或 false；前者任何其他值拒绝，production 禁止测试认证。禁用测试身份变量不等于main供应商已经接好 |
| migrate `MIGRATION_DATABASE_URL` | 仅 migration.env，独立 `campus_migrator`，同库私网 verify-full；迁移模式仍要求有效 DATABASE_URL 及 LoadConfig 其他键，生产两 URL 用户名必须不同；不加载到常驻 API |
| Caddy `USER_HOST / RIDER_HOST / ADMIN_HOST` | 无 scheme/path 的真实授权域名，从资产单复制；不填占位域签发 |
| Caddy `TLS_CERT_FILE / TLS_KEY_FILE` | 证书管理系统交付路径，CA 链完整，私钥仅 caddy读取；换证先校验 SAN/有效期/链，原子替换后，用运行服务相同的Caddy环境validate，再按批准停机窗口restart并从三入口握手核验 |
| Caddy `STATIC_ROOT / API_UPSTREAM / ADMIN_VPN_CIDRS` | `/opt/campus/current/static`、`127.0.0.1:8080`、实际网关来源网段；不得信任客户端自报转发地址；若网关改变源地址，先提供实际信任链及验证 |
| 仅测试 `TEST_DATABASE_URL / BROWSER_EXECUTABLE / P06_FIXTURE_EXECUTABLE / P06_EVIDENCE_DIR` | 独立测试进程使用；不得替代部署/迁移 URL，测试签发器及临时证书不得进入发布包 |
| OTP/IdP 拟议配置（**尚无main加载键**） | P01 提供 SMS 凭据、≥32字节OTP密钥、StaffOIDCConfig 的 Issuer/AuthURL/TokenURL/JWKSURL/ClientID/ClientSecret/MFAContext，且完成注入。安全审核配置契约后才登记正式键，不能杜撰“已支持”环境项 |

DB 凭据轮换：DBA 为同等最小权限的新登录角色发短期凭据 → 在隔离预发验证权限/CA → 密钥系统更新 api.env（不输出内容）→ 重启并验 readyz/业务 → 确认旧会话耗尽 → 撤销旧登录。不要先废旧凭据造成停机。CA 轮换先布署获准的双根信任链，再换服务器证书，最后撤旧根，每步验证主机名与链。供应商密钥按 P01 接线后契约轮换；OTP单密钥当前无双键机制，轮换需评估有效challenge失效并经P01测试。每次记录凭据版本 ID、操作者、时间及成功探针，禁止记录值。

Caddyfile明确 `admin off`，`caddy reload`及发行版unit中的ExecReload依赖管理API，会失败；不得为换证打开管理端口。Caddy服务本身及其EnvironmentFile仍须由SRE在目标安装核准。换证操作先保存可回切的旧证书版本，核 `systemctl cat caddy` 的实际ExecStart/config与EnvironmentFile均指向获准文件；用同一份 `/etc/campus/caddy.env`、同一服务用户及权限校验，随后在批准停机窗口执行 `sudo systemctl restart caddy`、`sudo systemctl is-active caddy`。这会短暂中断HTTPS连接。逐个用户/骑手/受控管理入口握手核对新证书指纹、SAN、链与有效期，并核API请求；失败则回切获准旧证书并再次restart，保留首次失败及重测。

若目标使用systemd且caddy二进制为已核准的 `/usr/bin/caddy`，下面一次性校验作业可复用运行环境；配置路径/用户/二进制以目标资产单核定，不满足时先改作业参数而不盲执行：

```sh
sudo systemd-run --unit=caddy-cert-check-<run-id> --wait --collect --pipe \
  --property=User=caddy --property=Group=caddy \
  --property=EnvironmentFile=/etc/campus/caddy.env \
  /usr/bin/caddy validate --config /etc/campus/Caddyfile --adapter caddyfile
sudo systemctl restart caddy
sudo systemctl is-active caddy
```

## 4. 分权与站点/角色初始化

DBA 创建非超级用户的 `campus_app`（运行 DML、无DDL/建角色/审计改删）、`campus_migrator`（schema owner，仅获准迁移窗口）、备份角色（托管备份或最小复制权限，不能作API凭据）、`campus_reconcile`（下列核对表 SELECT，不能 SELECT sessions/otp_challenges/addresses/quotes 的私密列）。使用数据库级 ACL/角色 search_path 固定目标 schema，生产 URL 不传 search_path。公网禁止连接；`pg_hba.conf` 或云 ACL 仅私网允许项。运行者不得拥有 schema/tables、membership 到 migrator 或 backup。

`docs/engineering/p00-runtime-grants.sql` 仅覆盖 000 共享表，不是完整 010–050 权限方案。**当前完整域表授权及专用审计导出账户未交付，是预发阻塞**。P00 汇总 P01–P05 最小 DML/sequence 清单；禁止用 `GRANT ALL ON ALL TABLES` 或超级用户绕过。实测 app 可执行业务但不能 CREATE/DROP/TRUNCATE、改删审计/不可变资金流水、修改schema_migrations；专用审计导出者只有受审列的 SELECT。新增迁移后复核权限。

当前没有可直接使用的站点/账号/角色初始化 CLI 或管理授权路由。需总控/P01 交审核过的一次性初始化脚本及审批记录，P07只负责在获准库短时执行；不要把下面清单当已初始化：

1. 运营批准唯一 station ID、名称和 Asia/Shanghai；安全批准 service_buildings ID/名称/启用范围。`stations` 有单站唯一约束，不能再插测试站。
2. 安全管理员与人事核对员工 IdP 的稳定 issuer/subject，分别申请骑手、运营、财务制单、财务复点、安全管理员账号。两名财务必须不同账号，同一敏感业务不能一人兼两岗；不得用邮箱/显示名自动赋权。
3. 经安全审批的一次性数据库操作者，按脚本在事务中建 `accounts`、`role_bindings` 和骑手 `rider_profiles`（初始 available=false），显式绑定同一 station。客户手机号 OTP 仅匹配已批准客户账户；不写合成会话。不得将Sessions.Issue测试签发器部署为登录入口。
4. 审核人复核结果、审批号与摘要，写独立受限审计副本；撤销一次性 DB 权限。角色变更同流程留审批与审计。账号停用验证会话撤销；角色移除验证每次请求不再有权。
5. 用真实测试 OTP 与真实员工 MFA 分别登录，核 `/me` 角色/站点、跨端Cookie/CSRF/越权拒绝、财务自复点拒绝。登录材料/个人信息不入普通证据。

## 5. 模板核验、迁移与启动

下列 shell 命令在批准 Linux 目标使用。`<...>` 必须先由资产单解析，未解析禁止执行；配置通过受限 EnvironmentFile 提供，不在终端展开 DSN。

```sh
# cwd: 已核验候选根；先用非生产占位参数校验模板，不连接业务库
caddy validate --config deploy/Caddyfile --adapter caddyfile
docker compose -p p07-<run-id> -f deploy/compose.dev.yaml config
# cwd: /opt/campus/current；数据库目标/账户由DBA双人核准后，单次运行
sudo systemd-run --unit=campus-migrate-<run-id> --wait --collect --pipe \
  --property=User=campus-migrate --property=Group=campus-migrate \
  --property=WorkingDirectory=/opt/campus/current \
  --property=EnvironmentFile=/etc/campus/migration.env \
  /opt/campus/current/bin/campus-api migrate
sudo systemctl daemon-reload
sudo systemctl restart campus-api
sudo systemctl is-active campus-api
curl --fail --silent --show-error http://127.0.0.1:8080/healthz
curl --fail --silent --show-error http://127.0.0.1:8080/readyz
sudo ss -lntp
```

迁移清单固定为 `000_001,010_001,020_001,020_002,030_001,040_001,050_001,060_001`，名称与 SHA-256 记录于 `deploy/ci/migration-lock.json`，正式冻结前须审定。打包时对源码的所有 `*.sql` 和包内 `migrations/` 的所有条目分别校验；非法命名、缺失、多余、非普通文件或字节改动都会在写 `VERSION.json` 前失败。每个文件摘要须与 schema_migrations 一致。迁移使用候选嵌入 SQL，单事务且校验前缀和摘要；迁移完成不代表域权限已正确。空库执行 000–060、合法带数据前缀升级及重放均需真实 PostgreSQL 证明；未批准 schema 时不能自动执行。迁移 stdout 只保留 migration_complete，详细失败 SQL 可能包含敏感数据，留受限 DB 诊断。

从API主机用受限 `PGSERVICEFILE`/`PGPASSFILE`（0600，含独立角色信息）执行，不在普通日志打印服务定义：

```sh
psql 'service=campus-check' -X -v ON_ERROR_STOP=1 -c \
 'SELECT current_user, current_database(), inet_server_addr(), ssl, version, cipher FROM pg_stat_ssl WHERE pid=pg_backend_pid();'
```

保存脱敏私网地址/证书指纹及ssl=true；故意用不匹配主机名和错误CA分别验证连接失败（独立用例）。仅看到ssl=true不证明verify-full，必须同时核配置查询参数与负测。PG健康和运维URL不放进三端。

入口实测覆盖三Host、本端HTML、assets、同源API、跨端HTML、未知Host/伪造转发头、管理网关外拒绝、`/healthz /readyz /debug/pprof /metrics`外拒绝；从未获准来源证实Go/PG/SSH不可达。API 64KiB body、5/10/15/60秒HTTP超时与Caddy限额分别核验；CSP与错误响应检查不泄露凭据。当前OTP来源取 RemoteAddr，Caddy后会聚合为回环来源；若需要客户端来源限频，P01/P00必须给出可信代理解析方案与防伪测试，不能把X-Forwarded-For视为已可信。

## 6. 固定版本升级、回切与试点核验

升级前记录旧/新 artifact-id、二进制/静态/迁移/SBOM摘要、工具版本与配置版本；备份/WAL确认可读。先在批准预发用同一数据副本验证：旧版读前向兼容数据、新版升级及新写入、旧版恢复运行和既有单收尾。旧版迁移器会拒绝非前缀历史，**回切不运行旧版 migrate**。

1. 按incident手册关新下单/放单并重启；逐项核存量退款、上缴、归架可继续。更新单机进程会有短暂不可用，窗口预先批准。
2. 新目录解包校验，执行已批准向前迁移。原子切换 `/opt/campus/current` 到新固定版本并重启；验证ready、三入口及一个批准合成闭环，核只读对账及outbox。
3. 不达标时停止新流量；将current切回已验证兼容的旧固定版本及对应非秘密配置版本，重启，核ready/存量操作/审计/outbox/不变量。不强行反迁移，不手改金额、库存、占位或任务状态。
4. 旧版不能读新数据或新迁移不兼容时禁止应用回切；保持停新单，交总控/DBA前向修复或按recovery恢复隔离目标。数据恢复是另一次批准的停写作业，不能在业务运行中覆盖主库。

试点前核验由P08独立复核：真实OTP/IdP/MFA、三端正式接线、最小授权、告警到人、审计副本、回切/PITR/15分钟负载、首错诊断、SEC-04全部证据；运营/财务/安全/发布分别签COD单。余额另单签，未签false。P07交付和G5签发均不授权本任务操作生产。

外部机制参考（执行时按实际安装版本核对）：[PostgreSQL 18 SSL](https://www.postgresql.org/docs/18/libpq-ssl.html)、[systemd EnvironmentFile](https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html#EnvironmentFile=)、[Caddy validate](https://caddyserver.com/docs/command-line#caddy-validate)。项目入口及权限约束以固定源码为准。
