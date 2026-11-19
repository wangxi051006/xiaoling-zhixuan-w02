# 校零智选 API 公共契约

`operations.json`、`schemas.mjs`、`enums.json` 是机器契约源；`openapi.yaml` 使用 YAML 兼容的 JSON 语法。`node contracts/generate.mjs` 更新 OpenAPI、Go 枚举与 TypeScript DTO；`node contracts/generate.mjs --check` 比较所有产物的精确内容。不直接编辑生成文件。HTTP 清单表示后续模块要实现的合同，当前未接线的路径仍返回 404。

三端共享 `/api/v1`，角色由对应入口的服务端会话确定。`x-roles` 是允许角色，空数组仅用于清单列出的公开操作；`x-recent-mfa` 要求近期 MFA。修改接口按操作要求带 CSRF 与幂等头。缺省允许之外的角色及跨站对象一律拒绝。业务失败使用错误包络；乐观锁冲突使用 `409`。所有金额限制为 JavaScript 可精确表示的整数分。

`CatalogPort.RestockCanceled` 只处理取货前、已分拣取消的预占：一笔事务按损坏数扣实存并释放整笔预占，按库存项追加损耗流水。损坏数不可超过原预占，重复命令无第二笔损耗。`Restock` 只处理取货后验收入库的可售部分。P05 通过 P02 端口调用，不写库存表。

角色、接口、状态转换或跨包 DTO 变更须更新契约源、Go 编译签名和对应验收断言后统一生成。不要从示例页面推导生产授权或资金状态。

`samples.json` 冻结合成请求、错误、空列表和渠道能力样本，按 `schema` 对应 OpenAPI components 校验。样本不含身份验证材料、真实电话、房间或退款凭据，不作为运行时 mock 导入。预约必须传 slot_id，即时不能由客户端指定窗口，下单不接受客户端金额，微信能力始终为禁用「待接入」。迁移编号见 `docs/engineering/p00-migrations.md`。
