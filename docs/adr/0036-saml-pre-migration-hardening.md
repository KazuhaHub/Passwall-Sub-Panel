# ADR 0036：SAML 加固线——先收紧本地校验与请求绑定，再考虑替换协议实现

- **状态**：已接受（2026-09-18）。**H1/H2 的行为契约与失败判据已冻结；实现待完成。** 本 ADR **替代 ADR 0023 的第 3、4 项决定**（存储层出错回退到内存缓存、内存缓存始终参与判断）；ADR 0023 的第 1、2 项（持久化集合、单语句原子插入、过期行不算重放）继续有效。ADR 0023 正文保留原文作为历史语境。
- **日期**：2026-09-18
- **相关代码**：`internal/service/auth/saml.go`、`internal/service/auth/saml_replay.go`、`internal/adapters/sqlstore/saml_replay_repo.go`、`internal/adapters/sqlstore/schema.go`、`internal/transport/http/handler/auth_saml.go`、`internal/transport/http/handler/admin_saml.go`、`internal/transport/http/handler/panel_path_sso.go`、`internal/ports/repos.go`、`internal/config/saml.go`
- **前置**：`docs/authcore-migration-plan.md`（§3.3、§6）、ADR 0023、`docs/authcore-deployment-compatibility.md`

## 背景

### 为什么要先做这条线，而不是直接换实现

把 SAML 协议实现换成共享库（`authcore/saml`）的收益与风险，和一个独立的加固问题是两件事。加固项——Destination 必填、拒绝多断言、拒绝弱签名算法、服务端请求记录与浏览器绑定、防重放 fail-closed、配置快照——**在现有 crewjam 用法上就能落地**，不依赖任何外部库的发布节奏。把两件事捆在同一个切换里，会造成一个不该有的耦合：替换实验失败时回滚，连带丢掉已经拿到的安全修复（见 R05）。

因此拆成两条线：**H 线先独立交付并发布制品；M 线（替换）在 H 的行为基线上单独验收、单独回滚，回滚目标是 H 而不是未加固的 B0。**

### 现状事实（B0，核对于 `origin/main` = `4697f29b`）

以下每条都取自当前源码，不是推测：

1. **`RelayState` 没有服务端记录。** `auth.SAMLService.BuildAuthnURL`（`saml.go`）把 AuthnRequest ID 和 returnURL 拼成 `req.ID + "|" + returnURL` 交给 IdP；`ACS`（`handler/auth_saml.go`）直接从回传的 `RelayState` 里切出 `reqID` 并把它当作唯一 `possibleRequestIDs`。**回传的 RelayState 本身没有任何服务端状态与之对应**，因此"这个请求确实是我们发起的"从未被验证过；能拿到的只是"InResponseTo 与 RelayState 里那个字符串一致"。
2. **防重放在存储故障时回退到进程内缓存。** `SAMLService.assertionAlreadyConsumed`（`saml.go`）先无条件写内存缓存，再查持久化 repo；`store.SeenOrAdd` 返回 error 时记 `ERROR` 日志并**返回内存结果放行**。这是 ADR 0023 第 3 项的既定选择。
3. **内存缓存始终参与判断。** 同一函数返回 `seen || memorySeen`。这是 ADR 0023 第 4 项。
4. **过期行接管不是原子的。** `samlReplayRepo.SeenOrAdd`（`sqlstore/saml_replay_repo.go`）在 `INSERT ... ON CONFLICT DO NOTHING` 未插入行后读回旧行，若旧行已过期，则执行 `Where("assertion_id = ?").Updates(...)`——**UPDATE 条件里没有 `expires_at` 与 `consumed_at` 的约束**。两个并发接管者都能拿到 `RowsAffected == 1`，都被判为"首次"。
5. **查询错误被伪装成"已重放"。** 同一函数在读回旧行失败时 `return true, nil`，把基础设施故障和真实重放记成同一个结论，`err` 被丢弃。
6. **保留期没有覆盖 SubjectConfirmationData，也没有下限。** `ParseACSResponse`（`saml.go`）取 `exp = assertion.IssueInstant.Add(10 * time.Minute)` 作兜底，否则 `exp = Conditions.NotOnOrAfter.Add(saml.MaxClockSkew)`。**没有取 `SubjectConfirmationData.NotOnOrAfter` 的最大值**，也没有 `now+5m` 下限——而 crewjam 会同时校验两者（`service_provider.go` 中 `subjectConfirmation.SubjectConfirmationData.NotOnOrAfter.Add(MaxClockSkew)` 与 `assertion.Conditions.NotOnOrAfter.Add(MaxClockSkew)` 两处独立判断）。
7. **配置切换可能留下"新配置 + 旧 Provider"。** `SAMLService.Reload`（`saml.go`）先写 `s.cfg = cfg`，再调 `buildSP`；`buildSP` 失败时 `s.sp` 仍是上一个成功构建的 Provider。此后 `Config()` 返回**新**配置、验签用的是**旧**Provider，而 `ParseACSResponse` 的属性映射读的是 `cfg.AttributeMapping`（新）——同一请求里的验签信任源与开户规则来源不一致。
8. **`Config()` 返回活指针。** `SAMLService.Config`（`saml.go`）在 RLock 下取出 `s.cfg` 后返回指针，调用方在锁外使用；管理端 `Reload` 可同时替换它。
9. **crewjam 的两个已知缺口未被补上。** `service_provider.go:1008-1014` 只在 `responseHasSignature || response.Destination != ""` 时才校验 Destination——未签名且不带 Destination 属性的 Response 直接跳过；`service_provider.go:1028-1090` 收集**全部**有效断言（明文与加密各自遍历）后返回**第一个**，源码内注释自述"less than fully correct"。
10. **管理端保存路径在保存前不做入口静态校验。** `admin_saml.go` 的 `Put` 流程是 `ApplySAMLDefaults` → `repo.Save` → `saml.Reload`，失败时返回 `{"saved": true, "reload_error": ...}`。入口是否 HTTPS、Login 与 ACS 是否同源，要到第一次真实登录才暴露。

### 与 ADR 0023 的关系

ADR 0023 第 3 项的选择（存储故障回退到内存缓存）是在"安全控制 vs. 一次数据库抖动让全部 SSO 用户登不进面板"之间做的权衡，理由写得很清楚，不是疏漏。本 ADR 翻转这个选择，因此必须正面回应它的可用性论据，见"决策 · D1"与"后果"。

ADR 0023 第 4 项（内存缓存与持久表互补）的论据是"内存缓存能抓住表中行被 GC 扫掉后的同进程重复提交"。在"保留期覆盖全部可接受窗口、时钟正确、GC 只删到期行"三个条件都成立时，**被 GC 删掉的行已经不再需要防重放**（断言自身的窗口已关闭，crewjam 会独立拒绝），所以第 4 项没有额外收益。这三个条件不成立时应修过期计算/GC/时钟并补边界测试，而不是靠单进程缓存遮盖。

## 决策

### D1 防重放存储故障改为 fail-closed，删除生产路径的内存回退

`assertionAlreadyConsumed` 在持久化 repo 返回 error 时**拒绝本次登录**，错误向上传播为可分类的"存储故障"。

理由：接受一次 SAML 登录等价于断言"这个断言此前未被使用过"。只把结论记在进程内**不足以支撑这个断言**——重启会遗忘，第二个实例不可见，这恰是 ADR 0023 第 1 项自己论证过的两个窗口。在无法证明未使用时放行，等于把这个不变量降级为"尽力而为"。

**明确接受可用性代价**（不包装成"没有代价"）。局部故障下的增量代价如下表，这是本 ADR 与 ADR 0023 分歧的实质：

| 故障形态 | 旧实现可能的结果 | fail-closed 的增量代价 |
| --- | --- | --- |
| 整个数据库持续不可读写 | 后续 `EnsureSSO` 用户查询同样失败 | 通常没有额外损失的可成功登录；不给出未实测的概率或 SLA 数字 |
| 仅 replay 表缺失/权限错误/锁超时 | 内存回退后用户查询可能正常，登录可能成功 | **这些登录被明确拒绝**——这是实际牺牲的可用性 |
| 数据库只读、已绑定用户无需更新资料 | 用户读取与本地 JWT 签名可能成功 | replay 无法记录，新增拒绝 |
| replay 调用短暂失败、随后用户查询已恢复 | 旧实现可能成功 | 本次仍拒绝；恢复后用户需重新 Begin |

依据（`saml.go`、`auth.go`、`handler/auth_audit.go`）：`EnsureSSO` 每次先经 User repo 读账户，持续全库故障本来就会挡住新 SSO 登录；`auth.Service.IssueTokens` 是纯本地签名，不写库；`recordAuthEvent` 写失败只记 `WARN`，不让登录失败。因此"仅 replay 后端故障"确实是一个旧实现能成功、新实现会拒绝的真实场景——**不能宣称增量风险为零**。

### D2 服务端一次性请求记录 + 浏览器绑定

新增 `saml_login_requests` 表（字段见下），Login 时写入、ACS 时原子消费：

| 字段 | 约束 |
| --- | --- |
| `token_hash` | 64 字符 SHA-256 hex，主键；原始 RelayState token 不落库 |
| `browser_hash` | 64 字符 SHA-256 hex，浏览器绑定随机数摘要 |
| `request_id` | SAML AuthnRequest ID，非空；ACS 只把它作为唯一 `possibleRequestID` |
| `config_digest` | 64 字符，发起时有效配置的确定性摘要（覆盖 SP、IdP URL、AttributeMapping、角色/组规则、开户默认值） |
| `return_to` | 已清理的站内路径，应用层上限 2048 字节，超限回退 `/user/me` |
| `created_at` / `expires_at` | UTC；有效期固定 5 分钟；`expires_at` 建索引 |
| `consumed_at` | nullable UTC；已消费行保留到过期清理 |

`Consume` 的 SQL 语义：**只允许更新同时满足全部绑定条件、`consumed_at IS NULL`、`expires_at > now` 的行；只有 `RowsAffected == 1` 才算占有成功。** 不得"读出未消费后无条件 UPDATE"，不依赖 MySQL 不支持的 `RETURNING`。

Cookie：每次请求独立的 `psp_saml_<token>`，值为绑定随机数；host-only、无 Domain、`HttpOnly`、`Secure`、`SameSite=None`、`MaxAge=300`，Path 为**外部** ACS 路径（不得用路由分发器已剥离的内部路径）。

理由：回传的 RelayState 完全由 IdP 往返、可被攻击者控制（`handler/auth_saml.go` 注释自己也这么说），因此"服务端确实发起过这个请求"必须由服务端状态证明，而不是由请求自己声明。请求一次性消费与断言防重放是**两个独立控制**，不能互相替代。

### D3 在 crewjam 之前加原始 XML 预检：Destination 必填、拒绝多断言、拒绝弱签名算法

新增 `internal/pkg/samlguard`，对**同一份原始字节**做结构检查，然后在原 crewjam 路径上验签：

- **Destination 必填**：Response 必须带 `Destination`，且等于配置的 ACS URL。这补上 crewjam 的 `responseHasSignature || Destination != ""` 短路。
- **拒绝多断言**：按 XML 命名空间只统计 `Response` 直接子级中的 `Assertion` 与 `EncryptedAssertion`，总数必须恰好为 1。**不用字符串计数或正则代替 XML 结构解析。**
- **拒绝弱签名算法**：检查 `SignatureMethod` 与 `DigestMethod` 的算法 URI 达到 SHA-256 及以上。

约束（必须遵守，否则预检本身会引入新的绕过面）：

1. 预检与验签针对**同一份原始字节**；不得"预检后再序列化 XML"，序列化会改变签名语义。
2. 预检只做拒绝判断，**不产出任何身份数据**；身份数据仍只从 crewjam 验证后的对象读取。
3. 保留现有 body/解码长度限制与错误分类。
4. **预检失败一律拒绝，不降级到旧解析器。**

### D4 保留期与共享库对齐，并补上缺失的分支

统一为：

```
max(now + 5m, Conditions.NotOnOrAfter, 各 SubjectConfirmationData.NotOnOrAfter)
    + crewjam.MaxClockSkew
    + 1m
```

这是"必须记住这个 ID 多久"的保守上界，与拟采用的 `authcore/saml` 的 `expiryFor` 完全一致（该函数取同样的 `max`，再 `Add(MaxClockSkew).Add(ReplayExpiryMargin)`，且**不设上限截断**）。H 与 M 用同一个公式，切换时保留期不变。

采用可注入时钟并覆盖所有分支的测试。**不把防重放过期时间当作延长断言自身有效性的许可**；也不为控制表大小擅自缩短（缩短会重新打开重放窗口）。

### D5 过期行接管改为有条件 UPDATE，查询错误单独返回

- 接管更新必须带条件：只有旧行**仍然过期**且更新命中一行才判"首次"，其余判已使用。
- 读回旧行失败时**返回 error**，不再 `return true, nil`。活跃记录冲突始终判重放；真正的数据库查询错误单独归类为存储故障，以便运维区分。
- 加入跨数据库（SQLite/MySQL/PostgreSQL）并发测试。

**新并发语义要写明**：一个有效断言复用了某个已过期 ID 时，条件 UPDATE 使**恰好一个**调用者取得"首次"资格，其余判已使用；不是"一次 SELECT 后所有调用者都成功"。

### D6 配置快照与 generation

运行时状态改为一个不可变快照：`config copy + digest + provider + generation`（slice/map 深拷贝）。

- 管理端保存与 generation 分配走**同一个串行入口**；锁只覆盖保存/换代，**不覆盖 Metadata 网络请求**。
- `PanelPathSSOMigrator` 的 SAML apply/rollback 必须走同一入口，不得绕过它直接 `Save` 后 `Reload`；补偿写回要核对它要撤销的 generation/配置摘要，不能覆盖期间管理员已保存的更新。
- 保存后 generation 递增；新进入 Login/ACS 的请求不能取得旧快照；随后在锁外构建候选 Provider；**构建成功且 generation 仍匹配才原子发布**。
- 构建失败 ⇒ SAML 暂不可用，**绝不把新映射规则配旧 Provider**（修 D7 现状事实第 7 条）。
- 配置保存 API 保持现有 `{"saved": true, "reload_error": ...}` 形状；`saved` 只表示已持久化，**不表示可登录**。
- 同配置的 Metadata 定期刷新失败可继续用既有 Provider 并记错误；没有可用 Provider 时保持禁用。刷新结果提交前检查 generation。
- `Config()` 只返回副本；handler 完成验证后使用验证结果携带的快照，不再二次读取可变配置。

并发语义的边界：**以 ACS 取得有效快照为界**。配置先换代 ⇒ 旧摘要请求失败；已取得快照的单个请求按该完整快照继续，账户状态仍走原有实时检查。不承诺追回处理中的认证，也不为持锁而阻塞验签、数据库与签发。

### D7 保存前静态校验 + 管理员只读自检

- `admin_saml.go` 的 `Put` 在 `ApplySAMLDefaults` 之后、`repo.Save` **之前**做静态检查，不通过返回 HTTP 400 与明确字段错误（不先保存后等首次登录失败）。检查项：能确定正式公共 Login URL；它与 ACS 均 HTTPS 且同源；ACS URL 无 userinfo/fragment；Cookie 外部 Path 可用；SP EntityID/证书/私钥可解析且配对。
- 正式 Login URL 由配置的公共基址（当前为 `SubBaseURL`）与 `panel_path` 推出，**不取管理员请求里未经信任的 Host**。未配置公共基址时提示先配置。
- **关闭 SAML 的操作始终允许完成**，不得因失效证书/入口检查阻止管理员停用。
- 新增管理员专用只读 `GET /api/admin/settings/saml/preflight`（挂在现有 `adminGroup`）：返回 `configuration_valid`、固定枚举的 `checks`、正式 Login/ACS URL、配置/运行态一致性、`supported_topology: single_instance`。**不返回密钥、Cookie、断言或完整属性；不创建用户、不签发令牌、不写 replay/request 表、不调用真实认证流程。**
- `checks` 的语义必须分清：**已知缺失（例如 replay 后端未装配）是 `failed`，不是 `not_checked`**；只有未实际执行的验证（如数据库写权限、浏览器 Cookie 接受）才是 `not_checked`。`configuration_valid` 只表示**配置是否合法**，运行准备情况另行报告，不用一个布尔值混淆两者。
- 存量配置在 boot 检查并输出可定位错误；新规则不通过只让 SAML 不可用，**不阻止面板其他管理功能启动**。装配错误若直接阻止启动，自检端点自然不可用——**启动错误仍是第一道检查**。
- 运行时 Login 仍核对实际可信外部源，防止保存时通过后代理/域名发生变化。

### D8 本轮的支持范围：单实例

**H1/H2 的发布支持范围为单实例 SAML。** 持久化控制保护单实例重启，也为将来的多实例验证提供必要条件，但**不构成多实例可用的证明**。宣布多实例前必须补齐 HA1–HA4（见 `docs/authcore-deployment-compatibility.md`）。

## 后果

- **安全能力提升**：Destination 短路、多断言走私、弱签名算法、请求伪造/跨浏览器重放、配置切换期的信任源不一致，这五类问题被关闭。
- **可用性下降（有意）**：replay 后端故障期间 SSO 登录被拒绝，用户需在恢复后重新 Begin。运维要求：每次基础设施失败记独立原因码（如 `saml_replay_store_error`），与攻击重放、断言过期分开；**持续出现该原因应检查数据库写权限/schema/锁/清理任务，不能靠清空 replay 表或启用内存回退"恢复服务"。** 采集不依赖 `auth_events` 的数据库写入——全库故障时只依赖数据库审计会失明，部署必须保留进程日志采集。
- **新表与新 Cookie**：`saml_login_requests` 为增量表，旧版本可忽略，回滚时不 DROP；绑定 Cookie 是新增内部状态，不替代既有 JWT Cookie。
- **新上线前提**：Login 与 ACS 必须同源且为 HTTPS（`SameSite=None` + `Secure` 的硬件要求）。不满足的部署需先统一正式入口，不能靠扩大 Domain 或关闭绑定兼容。
- **不变量（后续修改不得破坏）**：请求一次性消费与断言防重放相互独立；Preflight 只读且不含秘密；`saved: true` 不等于可登录；保留期只由断言窗口与时钟偏差决定，不由管理员偏好决定；H 与 M 共享同一 schema 与线上状态契约。

## 考虑过但未采用的方案

1. **保留内存回退，只在文档里强调"降级可见"。** 未采用：ADR 0023 已经试过这条路，它的结论是"保护级别等于表存在之前"。本 ADR 翻转的理由是"接受登录必须能证明断言未使用"，内存回退无法支撑该断言；把降级写清楚不等于补上这个证明。
2. **给 fail-closed 加一个运维开关（配置项回退到内存）。** 未采用：一个可由配置选择的降级路径同样能被攻击输入利用，且会在最需要 rigour 的时刻被打开。改为把失败做**可观测**（独立原因码 + 进程日志），而不是可绕过。
3. **在预检里放宽到"有签名就校验 Destination"，跟随 crewjam。** 未采用：那正是要关闭的短路；保持"Destination 一律必填"这一条更简单，也更容易测试。
4. **用字符串/正则统计断言数量。** 未采用：XML 命名空间、嵌套层级、注释都能骗过字符串计数，而预检是拒绝型控制，误判方向不可接受。
5. **让浏览器绑定靠 `SameSite=Lax` 或只校验 ACS 字符串写了 `https`。** 未采用：ACS 是跨站 POST，Lax Cookie 不会随该 POST 发送；只检查字符串无法证明同源。两者都会造成"看起来绑定了、实际没有"。
6. **在 M 线里顺便改流程协议（把加固和替换合成一次发布）。** 未采用：见"背景"首节——替换实验失败时回滚会连带丢掉加固，而加固本身不依赖共享库。

## 实施与验收

实现与验收结果由实施记录说明，本 ADR 不代为表述。H 的落地顺序为 H1（D3）→ H2（D1/D2/D4/D5/D6/D7/D8），各自独立发布并保存制品；验收用例见 `docs/authcore-migration-plan.md` §10.1 的 S 系列，S26/S27/S28 为本 ADR 新增。
