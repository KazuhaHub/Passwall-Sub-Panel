# authcore 迁移实施记录

本文件按 [计划书](authcore-migration-plan.md) §14 记录**已完成且有证据**的结果。未实施或未验证的阶段写"未开始"/"未验证"，不用计划代替结果，也不把设计描述成已通过。

最后更新：2026-09-19（H 线完成，M 线未开始）。

## 1. 分支与提交

四条叠放的分支，均基于核对时的 `origin/main`（`4697f29b`），后一条包含前一条：

| 阶段 | 分支 | 提交 | 内容 |
| --- | --- | --- | --- |
| P0 | `kazuha/authcore-experiment-baseline` | `d9284b80` | ADR 0036/0037、测量与职责表、部署清单、依赖快照、凭证 fixture、B0 基线证据 |
| H1 | `kazuha/saml-validation-hardening` | `e28afa93` | `internal/pkg/samlguard` + 接入 `ParseACSResponse`（ADR 0036 D3） |
| H2 | `kazuha/saml-state-hardening` | 11 个提交（见 §2） | ADR 0036 D1–D8 全部 |
| H3 | `kazuha/passkey-state-hardening` | 4 个提交（见 §3） | ADR 0036 §7.3、§7.4 |

**未 push，未开 PR。** 制品的构建与运行证据见 [h-saml-artifact-smoke.txt](authcore-baseline/h-saml-artifact-smoke.txt)。

## 2. H2：SAML 加固线（ADR 0036 D1–D8）

| 决定 | 落地 | 提交 | 主要测试 |
| --- | --- | --- | --- |
| D1 防重放 fail-closed、删除内存回退 | `assertNotConsumed` 只返回 error；nil store 视为装配错误；`app.Build` 启动即拒 | `b6e1c13b` | `saml_replay_store_test.go`（5 例，含"缺 store 拒绝"与"存储故障 ≠ 重放"） |
| D2 一次性请求表 + 浏览器绑定 | `saml_login_requests` 表与 repo；`BeginLogin` / `CompleteLogin`；绑定 Cookie | `da26dfc0` | `saml_request_repo_test.go`（9 例，SQLite + PostgreSQL 18.4，含并发消费） |
| D2 配置摘要 | `SAMLConfigDigest`（反射遍历 + 三处显式排除，排除路径有存活测试） | `1047b543` | `saml_config_digest_test.go`（确定性、25 个敏感字段、3 处不敏感、排除路径存活） |
| D2 浏览器流程 | RelayState 只载 opaque token（形态先校验再用作 Cookie 名）；请求 ID 与回跳目标均取自服务端记录；入口同源/HTTPS 校验 | `a96bd6ef` | `saml_login_test.go`（token 形态、入口、绑定、单次消费、中途改配置失效、过期） |
| D3 原始 XML 预检 | `samlguard`：Destination 必填、拒绝多断言、拒绝弱算法；在验签之前、对同一份字节执行 | `e28afa93` | `samlguard_test.go`（38 例，含四个与 authcore 的 parity 锚点） |
| D4 保留期公式 | `samlReplayExpiry`：`max(now+5m, Conditions, 各 SCData) + MaxClockSkew + 1m`；可注入时钟 | `ea01ea44` | `saml_replay_expiry_test.go`（9 个分支） |
| D5 过期接管原子化、读取错误不再伪装成重放 | `takeOverExpiredRow` 条件 UPDATE；`readExistingRow` 区分"行不存在"与"读失败" | `87fa8c93` | `saml_replay_repo_test.go`（确定性 + 并发，变异验证过） |
| D6 不可变快照与 generation | `samlSnapshot`（cfg+digest+generation+provider）；`applyMu` 串行化；失败构建发布"无 provider"的世代 | `d9c34f8b` | `saml_snapshot_test.go`（失败不配旧 provider、禁用即拆、在途快照不受影响、世代单调、刷新不得覆盖新配置） |
| D6 保存与换代同一串行入口 | `SaveConfig`（`saved` 与 `applyErr` 分开）；`admin_saml` 与 `PanelPathSSOMigrator` 均改走它；删除已无调用点的 `Reload` | `5c9b726c` | 同上（保存失败不改运行态、无 repo 拒绝） |
| D7 保存前校验 + 只读自检 + 启动审计 | `StaticPreflight` / `RuntimeChecks`；`PUT` 在 Save 前校验（停用始终放行）；`GET /api/admin/settings/saml/preflight`；`app.Build` 记录失败检查项 | `d08c4317` | `saml_preflight_test.go`（11 例拒绝 + 根路径部署 + not_checked 边界）、`admin_saml_preflight_test.go`（两个独立结论、缺件为 failed、读不到配置不返回绿色、不回显密钥） |
| D8 单实例声明 | 报告恒带 `supported_topology: single_instance`；部署清单 §1 声明 | `d08c4317` | 同上 |
| §6.5.1/§6.9 可观测 | 封闭原因码集合 + `psp_saml_acs_failure_total` | `9a79ecb4` | `auth_saml_failure_test.go`（重放与存储故障必须可区分） |

顺带交付的测试设施：`saml_testidp_test.go`（crewjam `IdentityProvider` 构成的真实签名测试 IdP，无新增依赖）与 `saml_acs_test.go`（S01–S08、S11 等服务层验收）。

## 3. H3：Passkey 加固（ADR 0036 §7.3、§7.4）

| 项 | 落地 | 提交 |
| --- | --- | --- |
| §7.3 撤销竞态 | 写门失败后重读凭证：缺失/换行/换账户/计数反而落后一律拒绝；读失败归类为基础设施错误而非"未授权" | `2cbcd68c` |
| §7.4 用途隔离、账户与 RP 绑定、硬容量 | 挑战记录 purpose/userID/rpDigest/expires；Take 先删后判；容量满拒绝而不淘汰有效挑战；两个 allow-listed ceremony 各自声明用途 | `ace08b3a` |
| §7.4 验收 | 自建软件认证器（真实 ES256 签名，未新增依赖）；W01/W02/W03/W05/W06/W08/W09 + 撤销 + 三个否定对照 | `ffc29c16` |
| 部署影响记录 | 部署清单 §8.2 | `af11cdb0` |

## 4. 制品

| 制品 | 源码 | sha256 |
| --- | --- | --- |
| H_SAML | `ea01ea44` | `ef5b9b470e2f552e258d4164833d53efdb58803f43bb712a5071d6584f84c08a` |
| H_PASSKEY | `af11cdb0` | `1e1f669daf04f2db8dd79d1c2e5c5d8109e7f3ea9a194164cf7d962e0ffecb1d` |

H_SAML 制品已在本机以真实进程启动并验证：内嵌 SPA 返回 200、`renderIndex` 注入生效、自检端点返回完整报告（`replay_store`/`request_store` 均为 `passed`）、未认证访问 401、SSO 未启用时登录与 ACS 均 404。详见 [h-saml-artifact-smoke.txt](authcore-baseline/h-saml-artifact-smoke.txt)。

## 5. 每包状态

| 包 | 状态 | 证据 |
| --- | --- | --- |
| SAML | **H 线完成**，M1 未开始 | 见 §2 与制品 |
| Passkey | **H 线完成**，M2 未开始 | 见 §3 |
| GeoIP | **未开始**（G1 可选，计划书允许 not_attempted） | — |
| 验证码 | **未开始**（C1 默认 deferred） | — |
| OIDC | 本轮不实施，仍在 PSP | — |

## 6. 未完成 / 未验证（不声称已完成）

- **M1 / M2 未开始**：未引用 authcore 任何版本；`go.mod` 至今不含该模块。因此 §1.3 的成本/职责判据（`A` / `D_pre` / `D_hardening` / `Δ_conservative`）**尚无数据**，测量表 [authcore-migration-measurement.md](authcore-migration-measurement.md) §6 保持"待填"。
- **A1 的前置改动未提交到 authcore**：`AttributesByName`、以及 §5.2 的写回契约（`UpdateSignCount` 返回 error 时两种 Finish 必须返回 nil result + error）都还没做。**A1 必须同时补一个锁定摘要白名单的双向测试**——authcore 目前对该白名单没有任何测试（见 §7）。
- **三数据库**：SQLite（全量）与 PostgreSQL 18.4（新增 SQL 定向）已实测；**MySQL 本机无实例**，仅由 CI 覆盖。
- **真实浏览器 / 真实 IdP**：未执行。S18/S24 的 Cookie 属性、非根 `panel_path`、代理部署仍需浏览器验证；`npm run smoke:dist` 在本机因 headless Chrome 环境问题不可运行（脚本自身提示非渲染回归），且本次改动未触碰前端文件。
- **§6.9 的用户可见原因码**：`auth_events.reason` 与 metric 已分开记录，但 SSO 失败页仍沿用 `error=auth_failed` + `description`；把它收敛为带中英文文案的低基数原因码仍未做。
- **多实例**：按 D8 只声明单实例；HA1–HA4 未做。

## 7. 给下一阶段的输入

1. **A1 的两件事**（缺一不可）：`AttributesByName`；写回错误必须使两种 Finish 失败的正式契约 + 真实流程测试。外加**摘要白名单的锁定测试**，否则 H 侧 `TestCheck_RejectsDigestOutsideAuthcoreAllowlist` 这个 parity 锚点是单向的。
2. **测量基线已就绪**：`git diff --numstat H M` 的两个 H 尖端分别是 `ea01ea44`（SAML）与 `af11cdb0`（Passkey）；职责表 ID 见测量文档 §5，`D_hardening` 的归类必须从该表推导，不得在看到 M 行数后调整。
3. **H 与 M 共用同一 schema 与线上状态契约**：M1 不得改动请求表、RelayState/Cookie 格式、配置摘要算法、错误语义与 replay 保留期。
