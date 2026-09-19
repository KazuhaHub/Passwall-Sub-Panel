# authcore 共享收益实验：测量与职责表

本文件是 [ADR 0037](adr/0037-authcore-shareability-experiment.md) 的测量载体。**判据与归类规则在 ADR 0037 中冻结,本文件只记录数据与职责归属。** 看到 M 的候选结果后再改判据或改归类,视为重新立项。

状态：P0 已建立基线;H/M 数据待填。最后更新：2026-09-18。

## 1. 基线

| 对象 | 值 |
| --- | --- |
| PSP B0 | `4697f29b`（`origin/main`,2026-09-18） |
| authcore 核对基线 | `1d5b0ee6a6799cfe57c5d5ec57037bcefd0fc5f8`（`origin/main`;有 `saml`/`passkey`/`captcha`/`geoip`,无 `oidc`） |
| authcore `v0.3.0` | `560647fc2705ce129f350e32c2b440a41e53514c`（与上述 main 差异仅为一份评估文档） |
| B0 制品 | `sha256:0bff4657aed336decc24e8dbe6d9fe65580fd61075a4138a87175119a379b269` |

**B0 制品的边界**：该二进制用 `-ldflags='-s -w'` 构建于工作树内,`internal/web/dist` 只有 `.gitkeep`,因此**不内嵌 SPA、不是可部署制品**。它的用途仅是 `go version -m` 的依赖证据与体积参考。可部署的 H/M 制品另行构建并记录。

`go version -m` 记录的模块版本为 `v1.3.2-0.20260919052840-4697f29bc0ef+dirty`（`+dirty` 来自本文件等未跟踪的新增文档;文档不进入二进制,不影响可比性）。

## 2. 计数口径（ADR 0037 §2 的执行细则）

- 固定**物理行**口径,含行内注释与整行注释;测试与文档**单列**,不混进生产净值。
- 在册范围**不得只计 `internal/adapters/authcore`**;见 §5 的职责表逐项。
- 保存 `git diff --numstat --find-renames H M` 全文与逐文件归属表（H/M 实现后写入 §6）。
- 纯移动不算减少职责;禁止改格式、压行、移动注释或删测试美化测量。
- 共享装配文件（如 `internal/app/app.go`、`router.go`）的删除量按**变更块**分配一次,不得多包重复认领。

## 3. B0 在册行数（已重新测量）

用上述口径在当前 `origin/main` 重测,与 authcore 评估文档记录的历史数字**完全一致**,因此该文档的历史数字可作为 B0 参照（但**不作为预算阈值**,见 ADR 0037 §4）。

| 概念 | 在册文件 | 行数 | 历史数字 | 一致 |
| --- | --- | --- | --- | --- |
| SAML | `internal/service/auth/saml.go` 636 + `saml_replay.go` 75 | **711** | 711 | ✅ |
| Passkey | `internal/service/passkey/passkey.go` 462 + `session_store.go` 75 | **537** | 537 | ✅ |
| GeoIP | `internal/pkg/geoip/geoip.go` | **156** | 156 | ✅ |
| 验证码 | `internal/service/captcha/captcha.go` | **212** | 212 | ✅ |

测试行单列（B0,同口径）：SAML `saml_replay_store_test.go` 89 + `saml_replay_test.go` 72 = 161;Passkey `passkey_test.go` 203;GeoIP `geoip_test.go` 95。

## 4. 依赖快照

原始数据：`docs/authcore-baseline/dependencies-B0.txt`（工具链、`go mod why -m` 归属、`go mod graph` 的 sha256、`go list -m all` 全文及再生命令）。

B0 关键事实：

- 模块总数（`go list -m all`）：**371**。
- 四个上游库**当前都是直接依赖**，且各自的原因唯一：
  - `crewjam/saml` ← `internal/service/auth`
  - `go-webauthn/webauthn` ← `internal/service/passkey`
  - `base64Captcha` ← `internal/service/captcha`
  - `maxminddb-golang` ← `internal/pkg/geoip`
- **采用 authcore 后这些库不会消失**：authcore 的公开 API 暴露 `*crewjam/saml.EntityDescriptor`（`saml.Config.IDPMetadata`）与 `webauthn.Credential`（`passkey.StoredCredential.Credential`）。允许 `direct → indirect`,但不计作依赖风险消失（ADR 0037 §6）。

H/M 各阶段用同一组命令重测并追加 `dependencies-H.txt` / `dependencies-M.txt`,对比模块集合与版本变化。

## 5. 职责表（固定 ID，归类已冻结）

**归类规则（ADR 0037 §3）**：`D_pre` 只收**B0 已存在**的实现;`D_hardening` 收 **H 阶段新增、独立成立**的加固实现;`D_bridge` 收**纯为接库搭建、随后移除**的桥接代码,且**不产生任何收益**。

只有同时满足「PSP 已删除该判断的实现」+「authcore 有正式契约」+「authcore 有回归测试」才算**一项真正转移**。包装调用、移动文件、减少 import 均不算。

「M 责任方」一列的 `authcore` 表示**转移候选**（须在 M 验收时逐项证明三条同时成立）,`PSP` 表示**保留在 PSP**,不计入共享收益。

### 5.1 SAML

| ID | 协议判断/机制 | H 的实现位置（计划） | 删除时归类 | M 责任方 | 上游契约与测试 |
| --- | --- | --- | --- | --- | --- |
| S-AUTHNREQ | 构建 AuthnRequest 与重定向 URL | `saml.go` `BuildAuthnURL` 改为接受 opaque RelayState | `D_pre` | authcore | `saml.Provider.NewAuthnRequest(relayState)`;`provider_test.go` |
| S-SPMETA | 生成 SP Metadata XML | `saml.go` `SPMetadataXML` | `D_pre` | authcore | `saml.Provider.MetadataXML()`;`provider_test.go` |
| S-VALIDATE | 签名、Issuer、Audience、NotBefore/NotOnOrAfter 校验 | crewjam（不变） | `D_pre` | authcore | `saml.Provider.ValidateResponse`;`validate_test.go` |
| S-DESTINATION | Destination 必填 | **H1 新增** `internal/pkg/samlguard` | `D_hardening` | authcore | `Config.RequireDestination` 默认 true;`provider_test.go` |
| S-MULTIASSERT | 拒绝多断言 | **H1 新增** `internal/pkg/samlguard` | `D_hardening` | authcore | 包文档明列 `ErrTooManyAssertions`;`validate_test.go` |
| S-WEAKSIG | 拒绝弱签名算法 | **H1 新增** `internal/pkg/samlguard` | `D_hardening` | authcore | `Config.RejectWeakSignatures`;`validate_test.go` |
| S-REPLAY | 持久化防重放（原子性、过期接管、保留期） | `saml_replay_repo.go` 在 H2 修原子性；保留期改按 ADR 0036 D4 | `D_pre` | authcore（适配层仅桥接 PSP repo） | `saml.ReplayCache` 接口 + `replay_test.go`;**repo 本身仍是 PSP 的实现** |
| S-EXPIRY | 保留期计算 | **H2 新增**（与 authcore 同公式） | `D_hardening` | authcore | `Provider.expiryFor` + `replay_test.go` |
| S-B64WS | Entra Base64 换行清理 | `saml.go` `ParseACSResponse` | `D_pre` | authcore | `ValidateResponse` 已做空白清理;`validate_test.go` |
| S-NAMEFMT | NameIDFormat 不强制 transient | `saml.go` `buildSP` | `D_pre` | authcore | `Config.NameIDFormat`;`provider_test.go` |
| S-REQTABLE | 一次性请求记录与原子消费 | **H2 新增** `saml_request_repo.go` | 不删除（H/M 共用） | **PSP** | — |
| S-BINDING | 浏览器绑定 Cookie | **H2 新增** `auth_saml.go` | 不删除 | **PSP** | — |
| S-CFGSNAP | 配置快照与 generation | **H2 新增** `saml.go` | 不删除 | **PSP** | — |
| S-PREFLIGHT | 保存前校验与只读自检 | **H2 新增** `admin_saml.go` | 不删除 | **PSP** | — |
| S-ATTRMAP | AttributeMapping 与 RoleRules/GroupRules 求值 | `saml.go` `ParseACSResponse` + `EnsureSSO` | 部分保留 | **PSP**（读 authcore 的 `AttributesByName`） | authcore 提供 `Assertion.AttributesByName`（A1 新增） |
| S-RELAYSTATE | RelayState 生成与回跳清理 | `saml.go` + `auth_saml.go` | 不删除 | **PSP** | — |
| S-ACCOUNT | 账户绑定/开户/两轴状态 | `user.go` `EnsureSSO` | 不删除 | **PSP** | — |
| S-ERRMAP | 错误分类与日志 | **H2 新增**错误映射 | 部分保留 | authcore（哨兵错误）+ PSP（映射与日志） | `saml/errors.go` 哨兵 |

### 5.2 Passkey

| ID | 协议判断/机制 | H 的实现位置（计划） | 删除时归类 | M 责任方 | 上游契约与测试 |
| --- | --- | --- | --- | --- | --- |
| P-REG | 注册 Begin/Finish 编排 | `passkey.go` | `D_pre` | authcore | `Service.BeginRegistration/FinishRegistration`;`registration_test.go` |
| P-DISCO | 无用户名登录编排 | `passkey.go` | `D_pre` | authcore | `BeginDiscoverableLogin/FinishDiscoverableLogin`;`discoverable_test.go` |
| P-LOGIN | 已知用户登录编排 | `passkey.go` | `D_pre` | authcore | `BeginLogin/FinishLogin`;`authentication_test.go` |
| P-ASSERT | 断言验签与计数器比对 | go-webauthn（不变） | `D_pre` | authcore | go-webauthn 之上;`authentication_test.go` |
| P-SIGNWRITE | 计数器写回与克隆拒绝策略 | `passkey.go` `finalizeAssertion` | `D_pre`（调用点） | authcore（调用时机与错误传播）+ **PSP**（策略） | `CredentialStore.UpdateSignCount` + **A1 新增契约测试** |
| P-REVOKE | 撤销竞态修复 | **H3 新增**（`UpdateAfterLogin` 返回 false 时重新查询归属） | `D_hardening` | **PSP** | — |
| P-PURPOSE | 挑战用途隔离与容量上限 | **H3 新增** `session_store.go` | `D_hardening` | **PSP** | — |
| P-UV | UV 强制（无用户名 Required、第二因素按现值） | `passkey.go` | `D_pre`（调用点） | authcore（per-call option）+ **PSP**（策略） | `Config.UserVerification` + `webauthn.WithUserVerification` |
| P-EXCLUDE | 注册排除列表 | `passkey.go` | `D_pre` | authcore | `BeginRegistration` 自动排除;`registration_test.go` |
| P-ORIGINS | RP ID/Origin 推导 | `passkey.go` `rpFromBaseURL` | `D_pre` | authcore（`Config.RPOrigins`）+ **PSP**（来源为 `SubBaseURL`） | `passkey.go`;`config_test.go` |
| P-DTO | PublicKey options 包装层级（`.Response`） | **M2 适配层** | `D_bridge` | **PSP** | — |
| P-HANDLE | user handle 8 字节编码 | `passkey.go` | 不删除 | **PSP** | — |
| P-CREDDB | 凭证数据库、标签、列表、撤销 | `webauthn_credential_repo.go` | 不删除 | **PSP** | authcore 明确不做（见 `CredentialStore` 文档） |

### 5.3 GeoIP（G1，可选）

| ID | 协议判断/机制 | H 的实现位置（计划） | 删除时归类 | M 责任方 | 上游契约与测试 |
| --- | --- | --- | --- | --- | --- |
| G-DECODE | MMDB 解码与字段映射 | `geoip.go` `Lookup`/`mapRecord`/`asString`/`mapOf`/`enName` | `D_pre` | authcore | `geoip.Reader.Lookup`;`geoip_test.go`/`fixture_test.go` |
| G-MULTICAST | 组播地址过滤 | `geoip.go` | `D_pre` | authcore | `geoip_test.go` |
| G-FALLBACK | name/city/region 字段回退 | `geoip.go` `mapRecord` | `D_pre` | authcore | `fixture_test.go` |
| G-READER | Reader 生命周期、文件替换、读写锁 | `service/geo` + `pkg/geoip.Open/Close/Info/IsResolvable` | **不删除**（保留 PSP API） | **PSP** | — |
| G-CONVERT | `Location → domain.GeoLocation` 转换 | `geoip.go` | **不删除** | **PSP** | — |

**G1 的判据要点**（ADR 0037 §4）：必须 `Δ_conservative <= 0` 且存在真实机制转移,**不适用** SAML/Passkey 的预算例外。`G-READER`/`G-CONVERT` 是必须保留的包装面,不产生删除量。

### 5.4 验证码（C1，默认 deferred）

未准入,不实施。ID 预留：`C-IMG`（图片生成参数）、`C-STORE`（图片挑战存储与容量）、`C-TOKEN`（第三方 token 校验）、`C-CLIENT`（出站客户端与超时）。重新准入前不填实现列。

## 6. 三提交与 A/D/Δ 记录（H/M 实现后填写）

| 包 | B0 | H | M | `A` | `D_pre` | `D_hardening` | `D_bridge` | `Δ_actual` | `Δ_conservative` | 判据 | 结论 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| SAML | `4697f29b` | 待填 | 待填 | 待测 | 待测 | 待测 | 待测 | 待测 | 待测 | ADR 0037 §4 | 待实验 |
| Passkey | `4697f29b` | 待填 | 待填 | 待测 | 待测 | 待测 | 待测 | 待测 | 待测 | ADR 0037 §4 | 独立候选 |
| GeoIP | `4697f29b` | = B0 | 待填 | 待测 | 待测 | 0 | 待测 | 待测 | 待测 | `Δ_conservative<=0` | 可选 |
| 验证码 | — | — | — | — | — | — | — | — | — | 重新准入 | deferred |

每行必须附：`git diff --numstat --find-renames H M` 全文、逐文件归属表、以及每个被记入 `D_*` 的 ID 的归类依据（引用 §5 的 ID）。

## 7. 填表纪律

- **不得在看到 M 行数后调整 §5 的归类**。若某 ID 的归类确有误,记录为「归类更正」并说明理由,同时保留原结论。
- `D_bridge` 永不产生收益,不进入 `Δ_actual`。
- 结论取值只允许 adopted / rejected / deferred / not_attempted;后两者**不算实验已证明有效或无效**。
- 每包结论独立;SAML 被拒绝不自动否定 Passkey。
