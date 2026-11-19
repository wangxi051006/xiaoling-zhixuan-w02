# 校零智选 W02 正式候选冻结实施计划书

> **执行交接：** DSH 负责实施与取证，按 `superpowers:executing-plans` 逐项执行；只有另获明确委派授权时才使用 `superpowers:subagent-driven-development`。复选框是待执行事项，不是既成结果。

**工单：** DSH-RC-01 · 2026-11-19（Asia/Shanghai）  
**目标：** 将 W02 及经审查的必要增量收敛成一个可重建、可校验、可交给 P08 的 COD 首发候选。  
**方案：** 重放核验 → 交易、财务及平台审查 → 修复 060 打包阻断 → 锁定同一 Git 提交、制品、迁移和配置 → 提交独立复核。  
**技术栈：** Git、Go / PostgreSQL、Vue / TypeScript / Vite，现有 P07 Linux/amd64 打包与 GitHub Actions。  
**依据：** [上一轮总工单第 02 节](D:/校零/docs/superpowers/plans/2026-11-19-dsh-cod-reconciliation-single-candidate.md)、[W02 待审交接](D:/校零/docs/evidence/launch/W02-20261119/README.md)、[P08 正式验收矩阵](D:/校零/校零智选-开发/docs/evidence/launch/matrix.md)。本工单只展开第一步，不改 PAY-02、SEC-04 或 P08 的放行标准。

## 范围与交付位置

- A01、W01、W02、开发基线、P06/P07 封存及历史验收只读。新建隔离工作副本和测试库，不复用现有预览进程或数据库。不自行初始化正式仓库、推送、建标签、改远端保护规则或触发工作流；这些需要仓库所有者明确授权。
- 允许在新副本修改本工单所列打包校验、测试、CI 调用和运维文档。新增交易增量若涉及订单、资金、权限、契约或迁移，先取得写域和差异批准。禁止修改历史 SQL、账本或历史验收结论，禁止降低门禁。
- 不部署预发/生产，不执行线上迁移或开关变更，不接触真实资金和凭证原件。COD 首发目标配置登记为 `COD_ENABLED=true`、`BALANCE_ENABLED=false`、`WECHAT_ENABLED=false`，不是已生效声明。未批准前保持现网原状；拟议配置中禁用测试身份，默认暂停新单/放单及邮箱功能，实际变更另行审批。
- 待审证据根：`D:/校零/docs/evidence/launch/W02-freeze-review/`。正式证据放入 `D:/校零/docs/evidence/launch/<candidate_id>/`，`candidate_id` 为最终提交的完整 SHA。归档仅进获准制品库；证据目录存脱敏报告、指纹与受限引用。若同提交更换配置，另建冻结记录，不覆盖旧证据。

| 交付物 | 内容 |
|---|---|
| `kickoff.md`、`input-review.md`、`inputs.json`、`replay.json` | 人员与授权、审查意见、基线和补丁白名单、干净重放证据。 |
| `deploy/ci/release-manifest.mjs`、`deploy/ci/release-manifest.test.mjs`、`deploy/ci/migration-lock.json` | 严格校验八份迁移的名称、字节、顺序和打包副本。 |
| `.github/workflows/ci.yml`、`deploy/ci/README.md`、`docs/runbooks/deploy.md` | 把打包回归纳入 CI，更新旧迁移列表和复核命令，不改变生产部署行为。 |
| `verification.md`、`config-profile.json`、`freeze-manifest.json`、`approvals.md`、`README.md` | 命令证据、配置指纹、候选身份、具名审签及 P08 交接。 |

## 01 · 开工与独立重放

- [ ] DSH 写 `kickoff.md`：登记实际执行人、交易/财务/平台/发布责任人、独立复核人、上海时间截止点、受控仓库与工具链授权、证据根。缺人或缺授权如实列出，继续能在独立副本完成的核验，不代填名字或签字。
- [ ] 在 `D:/校零` 运行以下命令，把结果写进 `inputs.json`。未匹配则暂停合入，逐文件追查差异；不要通过额外排除源码来匹配摘要。

```powershell
node .w01/tree-digest.mjs '校零智选-账号候选-A01' 'web/node_modules;web/dist;evidence;A01-交付报告.md;.git'
node .w01/tree-digest.mjs '.dsh-resume/W02-precheck' 'web/node_modules;web/dist;.git'
Get-FileHash -Algorithm SHA256 -LiteralPath '.dsh-resume/evidence/final/A01-to-W02-review.patch'
```

| 输入 | 预期 |
|---|---|
| A01 基底 | 251 文件，`a0483eee634eba3e6faa6b08251e59c76a6e730ed66dbd6b6b094e08ef598ef6` |
| W02 待审树 | 256 文件，`5482473f4fa0a53ce4a0459d129d7828f365b4003220eab12eae3e62330a584b` |
| A01 → W02 补丁 | 新增 5、修改 16、删除 0，`fe646dc22c506451fd7b604f743328a1dd0d830d1b964abd3d7ecd719ffb2805` |

- [ ] 从 A01 源码清单复制到新目录并复算 A01。在新目录运行 `git -c core.autocrlf=false apply -p1 --check --whitespace=nowarn D:/校零/.dsh-resume/evidence/final/A01-to-W02-review.patch`，通过后去掉 `--check` 实际应用。复算全树 256 文件，按 `D:/校零/.dsh-resume/evidence/final/A01-to-W02-files.json` 逐条核对增删改和新旧摘要，保存到 `replay.json`。A01 已含 P06 三入口接线，不重复打接线补丁。

## 02 · 具名审查与差异锁定

- [ ] 交易负责人对重放结果具名裁决：「W02 已覆盖全部应合交易修复」或「另有增量」。后者必须给已审 commit 或带 SHA-256 的补丁、共同基线、逐文件白名单及否决项关闭意见。`RC01` 目录未出现不能代替此裁决；缺裁决或缺输入记 `BLOCKED`。
- [ ] 财务确认 `expected_fen - actual_fen`（正为少缴、负为多缴、零为对平、空为未复点）。在 `input-review.md` 分别记录口径签认、线下差额追溯、退款实际兑付见证、告警到人的现状和责任人；自动化不能代签。上轮项目裁决若仍要求这些事项先关单，未满足只交待审包，由发布负责人确认前置条件，不由 DSH 自行后移。
- [ ] 将获批增量逐份并入重放树。审查交易事务、`backend/cmd/api/main.go`、身份与邮件、三端接线、契约、锁文件及迁移；保留 A01 授权/注册行为、W02 对账和旧幂等回放。按批准白名单逐路径核验，无额外差异才进入下一门禁。打包回归可并行准备，但独立审查通过前不正式冻结。

## 03 · 修复 060 打包阻断

**已复现的原始红灯：** W02 运行 `node deploy/ci/release-manifest.mjs metadata` 时，实际迁移数 8 与脚本要求 7 不符（`8 !== 7`、退出 1）。A01 已增加 `060_001_account_identity_email.sql`，不能靠删除它或去掉校验来通过打包。

- [ ] 读取 `D:/校零/.dsh-resume/evidence/final/precheck-identity.json` 的 `migrations`，从独立 A01 和重放树分别复算八份 SQL 的原始字节，核对名称、顺序、SHA-256；由发布负责人审定后写入 `deploy/ci/migration-lock.json`，每项 `{name,sha256}`。不从当前待打包目录自动刷新冻结清单。
- [ ] 新建 `deploy/ci/release-manifest.test.mjs`。用 `node:test`、`mkdtemp` 和 `spawnSync(process.execPath, …)` 调现有 CLI；每例独立临时根，从源码复制八份 SQL 到 `backend/migrations/` 及输出根 `migrations/`，运行 `metadata`，使用合成 `CANDIDATE_ID=LOCAL-REHEARSAL-MIGRATION-TEST` 和 `SOURCE_DATE_EPOCH=0`。正例断言退出 0 且清单精确等于八份 `{name,bytes,sha256}`；先保留其因 `8 !== 7` 失败的日志。负例分别删除 060、添加未批准 SQL、改历史 SQL 字节、只改包内同名 SQL，均断言非零退出且不生成 `VERSION.json`。
- [ ] 更新 `release-manifest.mjs`：用 `new URL('./migration-lock.json', import.meta.url)` 读取批准清单，用 `assert.deepEqual(migrations.map(({name,sha256})=>({name,sha256})), migrationLock)` 取代旧的数量与首末判断。独立读取输出 `migrations/` 的完整文件集合并按同一清单校验，拒绝额外项、非普通文件及符号链接；两次校验通过后才写元数据。保留原有 `VERSION.json` / `migrations.json` 结构与 `productionRelease: 'NO-GO'`。不得修改实际 SQL。
- [ ] 将测试纳入既有 CI Node 门禁，同步两处打包文档。运行 `node --test deploy/ci/gates.test.mjs deploy/ci/sbom.test.mjs deploy/ci/release-manifest.test.mjs deploy/scripts/split-static.test.mjs`、`node deploy/scripts/check-template-contract.mjs`、`actionlint -shellcheck= -pyflakes= .github/workflows/ci.yml`，需零失败、零跳过。复算八份 SQL 无变更、提交平台独立复核。如果审批确实新增前向迁移，另审清单和负例，不能放宽门禁。

## 04 · 固定版本并双构建

- [ ] 仓库所有者授权后，才将已审树导入受控 Git。排除缓存、预检制品、测试凭据和证据原件。用最终干净提交的完整 SHA 作 `candidate_id`，`git show -s --format=%ct HEAD` 作固定 `SOURCE_DATE_EPOCH`。记录 Git 树、检出换行规则与逐文件 `{path,bytes,sha256}`；构建前后跟踪源码不得变化。缺授权只交待审包，不把源码摘要冒作提交。
- [ ] 平台按候选 `deploy/ci/toolchain-lock.json` 校验实际版本。W02 锁定 Go `1.27.4`、Node `26.13.1`、npm `11.20.0`，race 的 GCC `13.3.0`。记录实跑版本和运行环境；不擅自升级或换成本机预演工具链。受保护 `staging-preparation` 需要独立审批、禁止自审、受保护 `main` 和必需检查，不能以 YAML 名称代替现网设置证据。
- [ ] 在同一提交跑 `node contracts/generate.mjs --check`、`node deploy/ci/gates.mjs wiring .`、`node deploy/scripts/check-template-contract.mjs`。用锁定 CI 和隔离真实 PG 验证空库 000–060、带数据合法前缀升级与重放、A01 身份、W02 COD 四场景和旧幂等回放。带真 PG 的 `CGO_ENABLED=1 go test -race -tags=integration -count=1 ./...` 单独留结果，现有 CI 的单测 race 和普通 integration 不能代替它；必需门禁若未跑或失败，不得记通过。完整 P08 正式预发矩阵不属于本工单。
- [ ] 用受保护发布准备作业构建。在获准 Linux 构建环境中，可用下列同源双构建命令核验打包和归档；此命令不能替代受保护作业、扫描和审批。

```bash
set -euo pipefail
test -z "$(git status --porcelain)"
export CANDIDATE_ID="$(git rev-parse HEAD)"
export SOURCE_DATE_EPOCH="$(git show -s --format=%ct HEAD)"
export PREPARATION_ENVIRONMENT=staging-preparation
out="$(mktemp -d)"
npm ci --prefix web
bash deploy/ci/build-release.sh "$out/a"
bash deploy/ci/build-release.sh "$out/b"
diff -ru "$out/a" "$out/b"
for side in a b; do
  (cd "$out/$side" && sha256sum --strict -c SHA256SUMS)
  tar --sort=name --mtime="@$SOURCE_DATE_EPOCH" --owner=0 --group=0 --numeric-owner \
    -cf - -C "$out/$side" . | gzip -n > "$out/$side.tar.gz"
done
cmp "$out/a.tar.gz" "$out/b.tar.gz"
sha256sum "$out/a.tar.gz" "$out/b.tar.gz"
go version -m "$out/a/bin/campus-api"
test -z "$(git status --porcelain --untracked-files=no)"
```

- [ ] 检查实际归档完整内容：Linux/amd64 API、三套独立静态根、八份 SQL、`VERSION.json`、SBOM、`artifact-manifest.json` 与 `SHA256SUMS`。在新目录取回并解包只封存的一份，复算 SHA-256，确认包内无清单外文件、夹具和密钥；核实际链接依赖与 SBOM。保存受保护 CI、审批和所需源/历史/制品扫描记录；不得绕开有效阻断。正式 P08 安全结论仍须独立签发。
- [ ] 平台出 `config-profile.json`：含环境 ID、来源版本、脱敏有效配置、三入口、代理和数据库角色/TLS 策略、开关、密钥引用版本。对经批准的 UTF-8、LF、递归键排序 JSON 重算 SHA-256。不得记录密钥值、散列单个密钥或导出现网 `.env`。若仅有模板，标记「拟议配置」，不得当正式配置指纹。

## 05 · 验收与交接

- [ ] `freeze-manifest.json` 绑定完整提交 SHA、Git 树、源码清单和批准补丁、Go/npm/工具链锁与契约、API/三端/SBOM/归档/逐件指纹、迁移顺序及摘要、配置模板与有效配置指纹、环境 ID、CI 运行与获准制品引用。在新目录独立验算；每条实跑命令把 cwd、提交、环境、时间、退出码、计数和脱敏证据及摘要写进 `verification.md`，另记临时资源清理。历史 W01/W02/P08 仅作对照。
- [ ] `approvals.md` 留具名交易、适用财务、平台、独立复核及发布意见。只有受控基线、必需门禁、双构建、实际配置指纹和签字全满足，发布负责人才能标记 `FROZEN_FOR_P08`。有待审证据但未批准记 `REVIEW_READY`；缺硬条件记 `BLOCKED`，逐项写明责任人、补证要求和下一步。任何源、制品、迁移或获准有效配置变更，必须重新登记冻结并按影响重跑，不沿用旧批准。
- [ ] 给 P08 的 `README.md` 只汇报：**冻结状态｜候选 SHA｜归档 SHA-256｜配置 SHA-256｜取回入口｜未完成项与责任人**。正式 G3/G4、P08 全矩阵及 G5 上线签收在后续；本工单完成也不改变 COD 和余额的 `NO-GO`。

## 给 DSH 的执行指令

