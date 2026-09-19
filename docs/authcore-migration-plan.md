# PSP 接入 authcore：迁移计划与实施步骤

状态：修订 2，可执行设计，尚未实施。日期：2026-09-18。

本文规定迁移范围、接口边界、行为变化、提交顺序和验收方法。文中的新增文件、接口、测试和发布版本均为实施要求，不代表代码已经存在或检查已经通过。

## 1. 决定与交付目标

**先在 PSP 现有实现上独立交付安全加固，再以相同安全行为为基线，逐包验证接入 authcore 是否值得。允许得出“不迁移这个包”的结论；补足第二个消费者本身不是验收目标。**

authcore 是 Go 模块，编译进 PSP 的现有二进制，不新增认证服务器、网络跳转层或集中用户数据库。接入该模块不等于多个产品共享登录会话。

authcore 的 2026-09-18 评估明确把 PSP 列为有信息价值的第二消费者实验；ADR 3 又有“只有一个消费者就不能认定为共享包”的退出标准。本计划要检验的是：PSP 在保持行为与安全控制后，是否真正减少了自行维护的协议判断/机制，以及为此付出的适配成本是否合理。不能为了证明共享成功而强制迁移。

### 1.1 交付范围和允许的结论

1. SAML 是首个必须给出实测结论的共享候选；Passkey 是独立候选，不与 SAML 捆绑通过。结论可以是 adopted、rejected、deferred 或 not_attempted，后两者不算实验已经证明有效或无效。
2. 原有 SSO 账户和已注册 Passkey 可以继续使用；不批量重建账户、不要求正常用户重新注册凭证。
3. 账户禁用、SSO 绑定冲突、角色/用户组映射、自动开户、流量/到期默认值、JWT、恢复码等仍由 PSP 决定。
4. 有意收紧的行为在加固线 H 中先落地，不归功于后续替换线 M；M 不改变这套行为。
5. GeoIP 推荐作为低成本独立实验，可先做、后做或明确不做，不是 SAML/Passkey 结束条件。
6. 验证码默认不进入实施队列。§8.2 仅保留候选设计，重新准入后才实施；OIDC 继续排除在本轮之外。
7. 通过共享验收的包只保留一套生产实现；实验失败的包保留加固后的本地实现及其测试，不因此撤销加固。

### 1.2 P0 必须冻结的测量口径

对每个参与实验的包，记录三个提交：原实现 B0、包含已交付加固且尚未替换该包的对照 H、替换候选 M。**共享成本和收益比较 H→M；B0→H 的安全收益与成本单列。** GeoIP 没有前置加固时 H 可等于 B0。若加固发布后 main 已有其他改动，以 M 实际分支基点中仍用本地引擎的版本作为测量 H，另保留最初加固发布 SHA；不能把期间无关变化计入该包实验。

评估文档的 PSP 711/537/212/156 行与 authcore 1117/888/753/448 行，是当时 `wc -l` 对选定非测试文件的测量，不能直接当作今天的删除量、适配预算或代码质量结论。

P0 新增 `docs/authcore-migration-measurement.md`，在看候选结果前写入：

- 文件范围：旧实现、替换适配器、ports DTO、service/handler/app 装配改动、配置快照、存储桥接和 authcore 新增 API。不得只计 `adapters/authcore`。
- `A`：H→M 在 PSP 增加的生产 Go 行；`D`：同一范围真正删除的生产 Go 行；`Δ=A-D`。计数包含行内/整行注释，使用固定的物理行口径，另列测试/文档和 authcore 的增量，不混进生产净值。
- 保存 `git diff --numstat --find-renames H M` 和逐文件归属表；纯移动不算减少职责，禁止通过改格式、压行、移动注释或删测试美化测量。共享装配文件按变更块分配一次，不能多包重复认领删除量。
- H 已有配置快照、请求表和保护逻辑原样保留的成本不再记作 M 新增；若替换造成额外桥接或重复实现，该部分必须计入 M。
- 职责清单：每个协议判断/通用机制给固定 ID，写出 H 的实现位置、测试和 M 的责任方。只有 PSP 已删除该判断、authcore 有正式契约与回归测试，才算一项真正转移；包装库调用、移动文件、减少 import 不算。
- 区分“安全判断转移”和普通机制转移。账户/角色/克隆拒绝策略本来就留在 PSP，不冒充共享收益。
- 依赖成本分列：直接 require、完整模块图、实际 PSP 二进制模块和测试专用依赖；记录新增契约及未来升级要跑的测试。

### 1.3 可证伪的采用/退出标准

以下是本方案预先设定的工程预算，不是由上述历史行数推导的事实。P0 固定后不得看到结果再移动门槛；改变门槛必须重新立项并保留原结论。

| 判据 | 决定 |
| --- | --- |
| 任一安全/兼容验收失败，或依赖 authcore 未声明且无上游测试的行为 | 不得合并替换；先修复契约，修不了则 rejected/deferred |
| `D=0`，或没有任何一项实际转移的协议判断/通用机制 | rejected，只有多一层包装不足以支持采用 |
| `Δ<=0`，且至少转移一项职责，安全/兼容全通过 | 可以采用；报告实际减少了什么 |
| `0<Δ<=25%×D`，且至少转移一项有上游回归保护的安全判断 | SAML/Passkey 可以采用；这是显式接受有限维护成本换取集中安全维护 |
| `Δ>25%×D`，或 `Δ>0` 但 PSP 自维护安全判断没有减少 | rejected；必须记录失败并保留 H，不用“已经花了时间”解释继续迁移 |
| GeoIP / 重新准入的验证码 | 要求 `Δ<=0` 且有真实机制转移；不采用上述安全预算例外 |

用户体验、安全强度和正确性是硬门槛，不能用行数收益抵偿。若适配新增量超过真正删除的 SAML 实现、又没有减少 PSP 自维护判断，该候选必定失败；不把历史的 711 行机械设为永恒阈值。

失败后关闭该包的替换候选，保留测量、未兼容行为和失败测试证据；若已部署，则按 §12 回到同等加固的 H 制品。删除仅服务于该失败候选的 PSP 适配层；authcore 已发布 API 如何维护，由该库按其他消费者实际需求另作决定，不在 PSP 任务里删除整个共享仓库。

实验结论只证明这个包对 PSP 的适用性。即使 SAML 成为第二个消费者，也不能据此替验证码、Passkey 或整个 authcore 自动宣布成功。

## 2. 核对基线与开始条件

### 2.1 本次核对的代码

| 仓库 | 基线 | 事实 |
| --- | --- | --- |
| PSP | `1e2f003b9e1ef5fbf704091e60c5e0d8b8335a52`，核对时的 `origin/main` | 尚未依赖 authcore |
| authcore | `1d5b0ee6a6799cfe57c5d5ec57037bcefd0fc5f8`，核对时的 `origin/main` | 有 `saml`、`passkey`、`captcha`、`geoip`，没有 `oidc` |
| authcore `v0.3.0` | tag 解引用为 `560647fc2705ce129f350e32c2b440a41e53514c` | 与上述 main 的差异仅为一份评估文档，协议实现相同 |
| Report Portal | 已检查本地接入源码 | 可参考适配方式；不能据此声称 PSP 已兼容或这些改动已经部署 |

authcore README 的“尚无 tag”和部分迁移说明已落后于代码。具体能力以以上版本源码和新增测试为准。

实施时先更新基线；如果相关代码变化，更新本文受影响的结论，不直接套用旧行号。

修订时再次核对 authcore 远端仍为上述 SHA，但本机 `~/Codes/authcore` 的 HEAD 落后 8 个提交。工作目录残留的 oidc/ratelimit/mail 等目录不证明远端仍有这些包：以 `git ls-tree origin/main` 与 `git show origin/main:<file>` 为准；不为本计划擅自删除残留文件。PSP 的原始核对 SHA 也保留为历史基线，后续 H/M 必须各记实际 SHA。

### 2.2 已有代码入口

| 职责 | 当前文件 |
| --- | --- |
| SAML 协议、配置重载、Metadata 刷新 | [internal/service/auth/saml.go](../internal/service/auth/saml.go) |
| SAML 请求入口、开户、Cookie、回跳 | [internal/transport/http/handler/auth_saml.go](../internal/transport/http/handler/auth_saml.go) |
| SAML 设置保存、Metadata 预览 | [internal/transport/http/handler/admin_saml.go](../internal/transport/http/handler/admin_saml.go) |
| 修改 panel_path 时的 SSO 配置迁移/补偿 | [internal/transport/http/handler/panel_path_sso.go](../internal/transport/http/handler/panel_path_sso.go) |
| 持久化防重放 | [internal/adapters/sqlstore/saml_replay_repo.go](../internal/adapters/sqlstore/saml_replay_repo.go) |
| 现行防重放架构决定 | [ADR 0023](adr/0023-saml-assertion-replay-protection.md) |
| SSO 账户与业务映射 | [internal/service/user/user.go](../internal/service/user/user.go)，`EnsureSSO` |
| Passkey 服务及挑战存储 | [internal/service/passkey/passkey.go](../internal/service/passkey/passkey.go)、[session_store.go](../internal/service/passkey/session_store.go) |
| Passkey 持久化 | [internal/adapters/sqlstore/webauthn_credential_repo.go](../internal/adapters/sqlstore/webauthn_credential_repo.go) |
| Passkey 无用户名登录、注册、step-up | [auth_passkey.go](../internal/transport/http/handler/auth_passkey.go)、[user_me_passkey.go](../internal/transport/http/handler/user_me_passkey.go)、[user_me_2fa_stepup.go](../internal/transport/http/handler/user_me_2fa_stepup.go) |
| 密码后的 Passkey 第二因素 | [internal/transport/http/handler/auth_local.go](../internal/transport/http/handler/auth_local.go)，`TwoFAPasskeyBegin/Finish` |
| OIDC | [internal/service/auth/oidc.go](../internal/service/auth/oidc.go)、[auth_oidc.go](../internal/transport/http/handler/auth_oidc.go) |
| 验证码与 GeoIP | [captcha.go](../internal/service/captcha/captcha.go)、[geoip.go](../internal/pkg/geoip/geoip.go)、[geo.go](../internal/service/geo/geo.go) |
| 装配与路由 | [internal/app/app.go](../internal/app/app.go)、[internal/transport/http/router.go](../internal/transport/http/router.go) |

### 2.3 分支与依赖纪律

每个仓库分别执行：

```sh
git fetch origin main --prune
git status --short --branch
git rev-list --left-right --count HEAD...origin/main
```

有其他任务的改动时，新建基于 `origin/main` 的工作树，不切换或清理别人的工作目录。任务分支使用 `kazuha/authcore-<阶段>`。

安全加固 H 不依赖 authcore 发布。替换实验 M 才要求先在 authcore 完成相应契约/API 并发布明确版本，再让 PSP 引用；GeoIP 可独立验证已固定的 v0.3.0。本文不预占未来版本号。执行人把实际 tag、解引用 SHA、PR 链接写入实施记录。

PSP 的 `go.mod` 固定该模块版本；提交前设置 `GOWORK=off` 验证。不得提交本地绝对路径 `replace`，不得以 `@main` 作为交付依赖。四个包属于同一个模块版本，不是分别发布的四个模块。

fetch 只更新远端引用，不会更新本地 checkout。实现使用基于新 `origin/main` 的干净工作树，不在落后的本地 main 上写代码，也不把未跟踪目录当作已发布 API。

### 2.4 依赖成本，不宣称消除上游库

authcore 的公开 API 包含 `*crewjam/saml.EntityDescriptor` 和 `webauthn.Credential`，因此 SAML/Passkey 适配器仍需直接导入上游库。当前方案即使四包全部采用，crewjam、go-webauthn、base64Captcha、maxminddb 也仍在模块依赖图里，另外增加 authcore。

但不能把它写成“go.mod 的直接依赖集合完全不变”：验证码用内置 driver 参数、GeoIP 只用包装 API 时，PSP 可能不再直接 import base64Captcha/maxminddb，`go mod tidy` 可将其变成间接依赖。完整图还可能受最小版本选择、测试库和未来 authcore 发布影响。

P0/H/M 各记录 `GOWORK=off go list -m -json all`、`GOWORK=off go mod graph`；对构建制品记录 `go version -m <制品路径>`。用 `go mod why -m <模块>` 解释四个上游依赖的保留原因。允许 direct→indirect，但不把它计作依赖风险消失；authcore 新增依赖和版本升级必须在 A1/M 的审计中逐项说明。

## 3. 范围及不变量

### 3.1 责任分配

下表描述该包被采用后的边界，不是“所有包必须采用”的清单。

| 能力 | authcore | PSP |
| --- | --- | --- |
| SAML | 构建请求、生成 SP Metadata、校验响应、输出验证后的中性断言 | 配置、Metadata 网络获取、请求与浏览器关联、持久化适配、账户与属性映射 |
| Passkey | WebAuthn 注册/登录编排、挑战与凭证存储接口 | 账户标识、RP 配置、凭证数据库、用途隔离、克隆告警策略、设备管理 |
| 验证码 | 图片挑战、一次性校验、第三方 token 校验 | 何时要求验证码、实时设置、客户端 IP、密钥、公共域名和日志 |
| GeoIP | MMDB 解码与查询 | 文件路径、下载和更新、管理 API、后台任务生命周期 |
| OIDC | 本轮不新增 | 保留原实现 |
| 用户、角色、用户组、配额、订阅 | 不进入 | 全部保留 |
| JWT、刷新、TokenVersion、认证缓存失效 | 不进入 | 全部保留 |
| TOTP、恢复码、登录锁定、审计 | 本轮不抽取 | 全部保留 |

### 3.2 必须保持的兼容条件

- SAML Subject 继续来自 NameID；可展示的 UPN 继续来自明确配置的来源。不得用 email/UPN 自动替代 Subject。
- `EnsureSSO` 的绑定、改名、同名冲突、管理员/操作员开户例外、角色与用户组重算保持原语义。
- 超额/到期属于服务轴，不因此禁止用户登录；账户轴禁用仍然禁止登录。
- Passkey user handle 保持 `binary.BigEndian.PutUint64(uint64(user.ID))` 的 8 字节编码。
- 凭证 ID 保持 Base64 Raw URL 编码，凭证 JSON、用户归属和已有主键不做全量重写。
- Passkey RP ID 和 Origin 继续从已配置的 `SubBaseURL` 推导，不能改成请求 Host、当前访问域名或新配置项。
- 无用户名登录必须要求 `UserVerification=Required`；作为第二因素的流程保持当前验证要求。
- SSO 不在此次迁移中被附加 PSP 本地 TOTP 挑战；无用户名 Passkey 登录也不额外叠加原本没有的 TOTP。
- 注册首个 Passkey 后的恢复码发放、删除最后一个因素时的处理、管理员撤销行为保持不变。
- HTTP 路径、前端字段和 `panel_path` 支持保持兼容，新增内部状态 Cookie 不替代既有 JWT Cookie。

### 3.3 行为变化归属：加固线与替换线分开

| 变化 | 所属交付 | 落地要求 |
| --- | --- | --- |
| SAML fail-closed、过期行接管原子化 | H2，本地加固 | 用 §6.5.1 的权衡替代 ADR 0023 的相关决定；不依赖 authcore |
| 服务端一次性请求及浏览器绑定 | H2，本地加固 | 请求表、Cookie、HTTPS/同源校验、自检先在 crewjam 路径生效 |
| Destination 必填、多断言拒绝、弱算法拒绝 | H1，本地加固 | 原始 XML 预检 + 原 crewjam 验签；M1 只把相同规则的实现替换为 authcore |
| 配置快照及配置失败时不可登录 | H2，本地加固 | 与绑定和预检一起单独发布；M1 不重新设计配置生命周期 |
| Passkey 撤销竞态修复、挑战用途隔离及容量限制 | H3，本地加固 | 先保留当前 WebAuthn 引擎完成，M2 继续使用相同行为测试 |
| SAML/WebAuthn 协议实现换成 authcore | M1/M2，替换实验 | 不增加或放宽 H 已规定的安全行为；通过成本判据才采用 |
| GeoIP 的组播过滤、额外字段回退 | G1，可选实验 | 明确列为展示行为差异并测试；不计作 SAML 共享收益 |
| 验证码硬容量/HTTP client 改变 | C1 默认不做 | 若重新准入，先单列本地行为变化再测量纯替换；不能塞进 M1/M2 |

这些变化是实施设计，本文没有把现行 ADR 自动标记为已被替代。P0 提交加固 ADR 与共享实验判据两个独立决定，并在 ADR 0023 加后继决定链接；旧决定正文保留历史语境。ADR 编号届时按仓库可用编号分配，不能占用同步状态等其他任务的编号。

H 与 M 必须分别合并、验收、产生可部署制品。H 的价值不以 M 成功为条件；M 的日常回滚目标是 H，不是未加固的 B0。单个 H 改动本身也有独立发布/回滚记录，其安全后果不能借库迁移名义隐藏。

## 4. 目标代码结构

新增以下文件是 M 候选通过后的职责拆分；未准入的包不创建空接口。H 先在现有 service 路径实现，SAML XML 预检可放 `internal/pkg/samlguard`，请求记录仍使用下列 domain/ports/sqlstore 文件。H 的实现不导入 authcore，且不会等待 A1。

```text
internal/ports/
  saml_protocol.go           # PSP 自有协议边界和验证后断言 DTO
  saml_request.go            # 一次性登录请求存储接口
  passkey_protocol.go        # PSP 自有 WebAuthn 引擎接口/用途枚举
  captcha.go                 # 图片与 token 验证机制接口
internal/domain/
  saml_request.go            # 登录请求记录；没有 authcore 类型
internal/adapters/authcore/
  saml.go                    # authcore/saml + Metadata/证书适配
  saml_replay.go             # 调用 PSP 的 SAMLReplayRepo
  passkey.go                 # authcore/passkey 编排适配
  passkey_credentials.go     # 凭证读写、标签和拒绝写入策略
  passkey_sessions.go        # 有界、一次性、用途隔离的挑战存储
  captcha.go                 # authcore/captcha 适配
internal/adapters/sqlstore/
  saml_request_repo.go        # 新表与原子消费
```

M 的 `service` 依赖 PSP 的 `ports/domain/internal/pkg`，不导入 `internal/adapters/authcore`。authcore 自有类型不扩散到 handler/账户服务；crewjam/go-webauthn 类型仍会出现在适配层内部，因为 authcore 的公开 API 本来就暴露它们。这里隔离的是业务层边界，不是消除 PSP 对上游库的依赖；依赖审计按 §2.4 执行。

`internal/app/app.go` 创建采用包的适配器、共享存储及服务，传入 router 的 `Deps`。目前 Passkey 与验证码服务在 `NewRouter` 内构造：Passkey 随 M2 移入 app，验证码仅在 C1 准入时移动，不能顺手扩大改动范围。router 测试改为注入相应依赖，不在生产路由构造器里悄悄创建另一份挑战存储。

GeoIP 是小型兼容包装：保留 `internal/pkg/geoip` 的现有 PSP API，在其中调用 authcore/geoip 并转换返回字段。无需为这组简单方法再搭一套通用引擎。

## 5. authcore 前置改动：A1 的硬性交付

A1 可以与 H 独立推进。M1/M2 不能仅因当前源码碰巧可用就跳过这里的契约和测试；A1 未完成时继续保留 H。

### 5.1 增加按原始 Name 索引的属性表

在 `saml/assertion.go` 的 `Assertion` 中增加：

```go
// AttributesByName 只按 SAML Attribute.Name 索引，不混入 FriendlyName。
AttributesByName map[string][]string
```

执行步骤：

1. 在 `assertionFromCrewjam` 遍历已经验签的断言属性时，同时构建该 map。
2. 保留每个 Name 的值顺序、重复元素合并行为和空值；空值处理属于调用方映射规则。
3. 原 `Attributes` 继续包含 Name 和 FriendlyName，保持 Report Portal 的 API 行为。
4. 两个 map 的 value slice 分别分配，避免修改一个影响另一个。
5. 新字段只从 crewjam 验证后的 assertion 产生；不得从原始请求单独解析一份身份数据。
6. 补测试：Name/FriendlyName 不同、相同、跨属性碰撞、同 Name 多值、空值、多个 AttributeStatement，以及旧 `Attributes` 行为未变。
7. 至少一个测试通过真实签名的 `ValidateResponse` 路径验证新字段，而不只调用转换函数。

PSP 用 `AttributesByName` 建自己的断言 DTO。不能根据管理员目前填了哪些键去过滤旧的合并 map，因为同名碰撞后已经无法恢复值的来源。

### 5.2 Passkey 写回失败必须使认证失败

这是正式接口契约变更，不是只纠正文档措辞。必须同时修改 `passkey/credential_store.go`、包说明、`LoginResult`/Finish 注释和 `MIGRATION.md` 中相互矛盾的部分。

新的约定是：

1. 两种 Finish 在密码学验证通过后均调用 `UpdateSignCount`，包括带 CloneWarning 的结果；**调用必经不等于应用必须无条件持久化或接受认证**。
2. Store 允许在写入之前执行应用策略拒绝，并返回可分类 error。它仍不能在返回 nil 的同时谎称完成所承诺的写入。
3. `UpdateSignCount` 返回非 nil error 时，`FinishLogin` 和 `FinishDiscoverableLogin` 必须返回 `nil LoginResult + 非 nil error`，并通过包装保留 `errors.Is/As`。禁止记录错误后返回成功结果。
4. 拒绝后挑战已经消费，重试相同 session 失败；未通过写回/策略接受阶段不构成成功认证。
5. 零写入是 PSP Store 的保证：CloneWarning 分支在数据库调用前退出。库不承诺自动撤销一个已经部分写入后才返回 error 的自定义 Store。

A1 新增表驱动契约测试，分别覆盖 allow-listed 与 discoverable 的真实虚拟认证器流程：

- 完成有效密码学响应，让 fake Store 在 `UpdateSignCount` 返回 sentinel error；确认该方法确实被调用一次，两个 Finish 均得到 nil result、非 nil error，`errors.Is(err, sentinel)` 成立。
- 分别使用普通存储失败和 CloneWarning 的策略拒绝；后者的输入要由真实计数回退触发，不能只 mock Finish。
- 用同一 session 再试必须失败。增加 Store 成功时返回正常结果的对照，避免测试因协议流程始终失败而假绿。
- 测试必须进入仓库常规 CI；临时把错误传播改成吞错成功，测试应变红，记录这次针对性的测试敏感性验证。

PSP M2 也保留实引擎测试：适配器拒绝→authcore 传播→PSP 不签发令牌，并断言数据库三字段不变。上游测试保护接口演进，PSP 集成测试保护接线；不能以任一层已有测试为由省略另一层。

**A1 完成条件：§5.1、上述接口文档与两种 Finish 的契约测试全部合并，并出现在 PSP 固定版本中。** 不接受只有 README 修正、只有其中一种 Finish 测试，或只测 PSP mock 的交付。

### 5.3 文档和兼容验证

- 修正 README 的版本状态，列明固定 tag 的安装方法。
- 修正 `MIGRATION.md` 的 Passkey 说明：Finish 返回前就会调用 `UpdateSignCount`；结果返回后检查 CloneWarning 不足以保证没有写库。
- 修正同一指南对 SAML Base64 换行处理的过时说明：当前 `ValidateResponse` 已执行空白清理，PSP 不必保留第二套实现。
- 文档对写回契约的表述必须与 §5.2 的实际代码及测试一致；不要把“协议验证通过”等同于“应用已接受登录”。
- 核实 PSP 和 Report Portal 对新版本的编译及回归。不要趁本 PR 升级 SAML/WebAuthn 上游版本，避免混合两种变量。
- 运行 authcore 的 build、vet、格式检查、race tests 和现有漏洞检查 CI，合并后发布固定版本。

authcore 本阶段不新增 OIDC、JWT、TOTP、审计、用户模型或通用 authflow。

## 6. PSP SAML：先加固 H，再替换 M1

### 6.0 H 的本地实现与对照制品

H1/H2 不调用 authcore。H1 在现有 crewjam 路径前增加原始 XML 结构/算法/Destination 预检，继续让 crewjam 做签名、Issuer、Audience 和时间验证；H2 实现下面的请求记录、绑定、持久化策略、配置快照和自检。

XML 预检必须识别命名空间、只统计正确层级的 Assertion/EncryptedAssertion、同时检查 SignatureMethod/DigestMethod，不用字符串数量或正则代替 XML 结构。预检和验签针对同一份原始字节，不能预检后再序列化 XML 改变签名语义；身份数据只从 crewjam 验证后的对象取得。保留 body/解码长度限制和错误分类。

H 的防重放保留期统一为 `max(now+5m, Conditions.NotOnOrAfter, 各 SubjectConfirmationData.NotOnOrAfter) + crewjam.MaxClockSkew + 1m`，采用可注入时钟并测试所有分支。这是与拟采用 authcore 版本一致的保守记忆窗口，不把防重放过期时间当作延长断言自身有效性的许可。

H1/H2 单独完成 §10 的 SAML 行为测试并发布制品，记录 `H_SAML_SHA` 与制品摘要。随后 M1 在独立测试数据上跑同一组测试，只替换协议实现。不要把新请求表、浏览器绑定和配置重写藏进 M1 的 diff。

### 6.1 M1 定义窄协议接口

`ports.SAMLProvider` 至少提供：

| 方法职责 | 输入 | 输出 |
| --- | --- | --- |
| 构建认证请求 | `ctx`、opaque RelayState | 请求 ID、完整重定向 URL |
| 验证 ACS | `ctx`、`*http.Request`、允许的请求 ID 列表 | 已验证的断言 DTO |
| 输出 SP Metadata | `ctx` | XML 字节 |

PSP 的断言 DTO 包含 `ID`、`Issuer`、`NameID`、`NameIDFormat`、`AttributesByName`，以及需要的有效期信息，不携带账户/角色对象。

Factory 接收 PSP 自有配置 DTO：EntityID、ACSURL、证书与私钥 PEM、IdP Metadata URL。它在适配层解析证书、用注入的受保护 HTTP 客户端获取并解析 Metadata、构建 authcore Provider。管理页的 Metadata 预览也走同一解析适配器。

本地 `auth.SAMLService` 保留配置生命周期和属性映射。将“验证响应 + 返回所用配置快照”作为一次操作完成；handler 不得验证后再次独立调用 `Config()`，否则并发改配置会导致验签使用 A、开户规则使用 B。

### 6.2 H/M 共有行为与 M1 显式选项

H 通过本地校验及 crewjam 设置实现相同行为。下表 authcore 字段只在 M1 接线，不能据此让 H 等待 authcore。

| authcore 配置 | PSP 选择 |
| --- | --- |
| `NameIDFormat` | `UnspecifiedNameIDFormat`，保持请求中不强制 transient |
| `ReplayCache` | 必填，适配现有持久化 repo；nil 视为装配错误 |
| `AllowIDPInitiated` | `false`，与当前 SP 构造的默认行为一致；不新增无请求登录模式 |
| `RequireDestination` | `true` |
| `RejectWeakSignatures` | `true` |
| `StrictAttributes` | `false`，保持当前同 Name 合并的映射行为；PSP 只读取新 `AttributesByName` |
| `AllowEncryptedAssertions` | `true`，保持现有配置了 SP 私钥时的解密能力；补真实加密断言测试 |
| `MaxResponseBytes` / `MaxInflatedBytes` | 显式设为 1 MiB；保留 router 已有原始 HTTP body 上限 |

注意：authcore 的弱算法扫描针对原始 XML，不能据此宣称已检查加密断言内部的全部签名算法。加密样本的解密与签名验证交给底层库；若产品以后要求“内外所有签名一律 SHA-256 以上”，应另补解密后算法检查，不能通过放宽现有检查来假装满足。

原始 HTTP body 上限和解码 XML 上限是两回事。保留 ACS 的登录限流中间件，新增 Login 入口的限流，不能因新库有限制而删掉路由保护。

### 6.3 一次性登录请求表

新增 `saml_login_requests`，不修改 users 或现有凭证表。

| 字段 | 约束与含义 |
| --- | --- |
| `token_hash` | 64 字符 SHA-256 hex，主键；原始 RelayState token 不落库 |
| `browser_hash` | 64 字符 SHA-256 hex，浏览器绑定随机数的摘要 |
| `request_id` | SAML AuthnRequest ID，非空 |
| `config_digest` | 64 字符，发起时所用有效配置的确定性摘要 |
| `return_to` | 已清理的站内路径，应用层上限 2048 字节；超限回退 `/user/me` |
| `created_at` / `expires_at` | UTC；有效期固定 5 分钟；过期列建索引 |
| `consumed_at` | nullable UTC；已消费的记录仍保留到过期清理 |

配置摘要必须覆盖信任和业务映射配置，包括 SP、IdP URL、AttributeMapping、角色/组规则与开户默认值；按固定 DTO 序列化后计算，不使用不确定 map 遍历顺序。Metadata 同一 URL 的定期证书更新不改变该配置摘要。摘要与原始配置都不写入公开错误信息。

新增 repo 接口：`Create`、`Consume`、`DeleteExpired`。`Consume` 输入 token hash、browser hash、config digest 和 now，返回被占有的记录或统一的无效/过期错误。

数据库/API 返回的 `request_id` 必须与创建时的值逐字节一致。时间比较统一 UTC；Cookie 的 MaxAge 只用于浏览器清理，真正的有效性由服务端 expires_at 决定。

关键 SQL 语义：只允许更新匹配全部条件、`consumed_at IS NULL`、`expires_at > now` 的行；只有 `RowsAffected == 1` 才成功。可以先读不可变字段再做条件 UPDATE，但不得“读出未消费后无条件 UPDATE”。不依赖 MySQL 不支持的 RETURNING。

所有 hash、ID 长度由应用校验。文本列不设置 MySQL 不支持的 DEFAULT。将新 row 加入 `schemaModels`、repo 加入 `ports.Repos` 和 `NewRepos`，更新仓库完整性测试。使用现有整体 repo 构建方式，避免逐字段复制丢掉其他 repo。

清理挂到既有定期清理生命周期，按 `expires_at <= now` 删除，不新增未跟踪 goroutine。Login 在数据库创建请求失败时不重定向 IdP。

### 6.4 浏览器流程

Login：

1. 获取当前有效配置/Provider 快照；要求外部 Login 和配置的 ACS 同源且为 HTTPS。host-only Cookie 不能跨主机传递，因此不能只检查 ACS 字符串写了 https。代理场景通过已有信任链判断，不能信任任意 forwarded header；来源不匹配时失败并提示使用配置的正式入口。
2. 清理 return_to；生成独立的 32 字节 RelayState token 和 32 字节浏览器绑定随机数，均用 Base64 Raw URL 编码。
3. 将 token 传给认证请求构建器，得到请求 ID。H 用 crewjam 的 MakeAuthenticationRequest/Redirect，M1 用 `NewAuthnRequest(token)`；此时 token 已知，避免旧实现“先生成 req.ID 再拼 RelayState”的 API 不匹配问题。
4. 将 token hash、browser hash、request ID、配置摘要、return_to 和期限写入新表。
5. 设置每次请求独立的 Cookie `psp_saml_<token>`，值为绑定随机数。Host-only，无 Domain，`HttpOnly`、`Secure`、`SameSite=None`、`MaxAge=300`。Path 使用配置中 ACS 的外部路径：普通部署为 `<panel_path>/api/auth/saml/acs`，自定义代理回调则用其真实外部路径，不得用已被路由分发器剥离的内部路径。Cookie 只需发送到 ACS，不必覆盖 Login。
6. 重定向到构建器返回的完整 URL。不要重新拼接或修改可能被签名覆盖的 SAMLRequest 查询参数。

ACS：

1. 在已有 body limit 下解析表单；只接受固定长度/字符集的 token，不再解释旧 `reqID|returnURL`。
2. 读取该 token 对应的浏览器 Cookie；没有 Cookie、token 非法或不匹配均拒绝。
3. 获取一次运行时快照；调用 repo 原子消费请求，同时核对 browser hash、配置摘要和过期时间。
4. 无论后续成功与否，删除对应 Cookie；Path/Secure/SameSite 与设置时一致。有效绑定的失败尝试也消耗请求。
5. 将记录中的 request ID 作为唯一 possibleRequestID 传给 Provider；禁止从响应声明的 InResponseTo 反推“允许列表”。
6. 验签通过后执行持久化防重放；H 由原服务调用，M1 由 authcore 调用适配器。请求一次性消费与断言防重放是两个独立控制，不能互相替代。
7. 用同一快照的映射规则生成 `EnsureSSOInput`，继续原有账户检查、令牌签发和认证事件记录。
8. 只从服务端记录取 return_to，回跳前再次 `sanitizeReturnTo`，不使用 ACS 回传的任意地址。

不同浏览器即使拿到 token 和 SAMLResponse，没有绑定 Cookie 也不能完成该登录。多标签页依靠独立 Cookie 名工作；客户端 Cookie 数量仍受浏览器限制，不能承诺无限同时进行的登录。

外部 HTTPS 和同源入口是本阶段的新上线条件。跨域 ACS 不能靠扩大 Domain 或关闭绑定来兼容，应先统一正式入口；复杂跨域认证另立协议设计。无需关闭 SameSite 检查或让失败请求回退到旧流程。浏览器若拒绝绑定 Cookie，应给出可重试的失败提示并记录原因，不静默放行。

### 6.5 持久化防重放

1. 适配现有 `SAMLReplayRepo`；保留现有 assertion ID 键，不在切换时改成带 issuer 前缀的键，否则已有已消费记录可能失效。
2. 存储错误原样传播为可分类失败。删除生产路径中的内存回退；不得让 nil repo 使用 authcore 默认内存缓存。
3. H 按 §6.0 计算 expiresAt，M1 使用相同固定版本算法输出并直接传给 repo；不得为控制表大小擅自缩短防重放保留期。
4. 修正现有 repo 的“过期行接管”：当前过期后无条件更新可能让多个并发接管者都返回首次。改为有条件 UPDATE，只有旧行仍过期且更新到一行才返回首次；其余判已使用。加入跨数据库并发测试。
5. 现有 `Take`/查询过程遇到数据库错误也必须返回 error，不把所有读取失败都伪装成“已重放”，以便区分基础设施故障。
6. 清理仅删除关闭窗口；迁移滚动期间保留已消费的 ID。旧记录短保留期与新算法之间的差异由下面的切换等待窗口处理。
7. 不照搬其他项目可能存在的“把 expiresAt 截短到固定上限”的适配方式；除非同时拒绝超出相同有效期的断言，否则截短会重新打开重放窗口。

#### 6.5.1 后继 ADR 必须正面回应的可用性取舍

现行 ADR 0023 的内存回退有真实的局部故障可用性收益，不能只以“数据库反正不可用”否定它。

源码证据：`EnsureSSO` 每次先调用 User repo 的 `GetBySSO`，未绑定时还要查询/写入账户，持续的整个数据库故障本来就会使新 SSO 登录失败。但 `IssueTokens` 的签名运算是本地操作，app 注入的 JWT 参数读取失败还有默认值回退；不能说“签 JWT 必须写数据库”。

| 故障形态 | 旧实现可能的结果 | fail-closed 的增量代价 |
| --- | --- | --- |
| 整个数据库持续不可读写 | 后续用户查询同样失败 | 通常没有额外丢失的可成功登录，但不能给出未测的概率或 SLA 数字 |
| 仅 replay 表缺失/权限错误/锁超时 | 内存回退后用户查询可能正常，登录可能成功 | 新实现明确拒绝这些登录，这是实际牺牲的可用性 |
| 数据库只读、已绑定用户无需更新资料 | 用户读取及本地签名可能成功 | replay 无法记录，新增拒绝；不能宣称风险近零 |
| replay 调用短暂失败，下一次用户查询已恢复 | 旧实现可能成功 | 新实现仍拒绝这次请求，恢复后用户重新 Begin |

选择 fail-closed 的理由是：接受 SAML 登录必须持久化证明该断言未使用；只记在当前进程不能保护重启或其他实例。明确接受上述局部故障可用性损失，换取这个不变量，不把它包装成“没有代价”。

H2 增加故障注入证据：全库不可用、仅 replay write/read 错误而 User repo 正常、只读数据库、一次性故障后恢复，分别记录有没有到达 EnsureSSO/IssueTokens。原行为仅在隔离基线测试中对比，不在生产保留故障回退开关。没有真实运行数据时，ADR 记录定性边界和测试结果，不编造增量故障率。

运维要求：

- 每次 replay 基础设施失败记 `saml_replay_store_error`，与攻击重放、断言过期分开；对外提示本次登录暂不可用并要求重新开始，不泄露表名/SQL。
- 使用进程日志和现有 metrics 基础设施记录计数/原因，采集不依赖 auth_events 的数据库写入；全库故障时只依赖数据库审计会失明。metrics 若只能经受 DB 影响的管理路由读取，不能宣称它独立可用，部署必须保留进程日志采集。
- 持续出现该原因应检查数据库写权限、schema、锁/连接错误与清理任务；修复后重新发起一次登录验证。不能仅清空 replay 表或启用内存回退来“恢复服务”。
- 已发出的 JWT 不因该错误主动撤销，但其后续可用性仍取决于现有账户检查；本地管理员入口也依赖数据库，不能把它当作全库故障的保证后门。

对 ADR 0023 第 4 条的处理必须写明：在“保留期覆盖全部可接受窗口、时钟正确、GC 只删到期记录”的条件下，正常 GC 删除的行已经不再需要防重放，内存缓存并没有额外互补收益。若这些条件不成立，应修复过期计算/GC/时钟并做边界测试，而不是靠单进程缓存遮盖。

删除内存回退也意味着不再对误删尚有效行提供偶然的进程内兜底；该缓存本来不能为未见过断言的另一实例或重启提供保证。新 ADR 明确把活跃 replay 行误删列为运维违规/完整性故障，禁止清理任务或操作流程这么做，不能宣称“数据库永不会被误删”。

同时记入过期接管的新并发语义：新的有效断言复用已过期 ID 时，条件 UPDATE 恰好一人获得首次资格，其余判已使用；不是一次普通 SELECT 后所有调用者都成功。活跃记录冲突始终判重放，真正的数据库查询错误单独返回 error。

### 6.6 配置、Metadata 与并发

将运行时状态改成一个不可变快照：`config copy + digest + provider + generation`。slice/map 也必须深拷贝。

- 将管理员配置的保存和 generation 分配放入同一个串行应用流程；不要让两个 handler 各自 Save 后无序 Reload，从而把较旧的 cfg 发布在较新的数据库配置之后。可以由 SAML 服务协调 repo.Save 与换代；锁只覆盖保存/换代，不覆盖 Metadata 网络请求。
- 同步修改 `PanelPathSSOMigrator` 的 SAML apply/rollback：使用同一保存/换代入口，不能绕过它直接 Save 后 Reload。补偿写回必须核对它要撤销的 generation/配置摘要，不能覆盖期间管理员已经保存的更新。保留 OIDC 原有逻辑及两者部分失败时的错误报告，不把本次 SAML 改造扩展成 OIDC 协议迁移。
- 管理员保存配置后增加 generation，新进入 Login/ACS 的请求不能取得旧快照；随后在锁外构建候选 Provider。
- 构建成功且 generation 仍匹配，原子发布完整快照。若构建失败，SAML 暂不可用，绝不把新映射规则配旧 Provider。
- 配置保存 API 继续返回现有 `saved: true, reload_error: ...` 形状；这里的 saved 表示已持久化，不表示可登录。更新管理页已有错误展示的必要测试。
- 单纯的同配置 Metadata 定期刷新失败，可以继续使用该配置的既有 Provider并记录错误；没有可用 Provider 时保持禁用。
- 刷新结果提交前检查 generation，避免旧 URL 的慢响应覆盖新配置或重新启用已关闭的 SAML。
- 改变配置后，旧摘要的未完成请求拒绝并要求重新开始。不能拿新角色规则解释旧流程。
- Config 查询只返回副本；handler 完成验证后使用验证结果携带的快照，不再二次读取可变配置。
- 保留 Metadata URL 的 safehttp、超时、证书解析、后台恢复及关闭等待；不能改成默认无超时的 `http.Get`。

并发语义以 ACS 取得有效快照为边界：配置先换代，旧摘要请求就失败；ACS 已取得快照的单个请求可以按该完整快照继续，账户状态仍执行原有实时检查。本阶段不承诺追回已经在处理中的认证，也不为了持有配置锁而阻塞整个验签、数据库与签发过程。测试要控制这个边界，不能把两种执行顺序都要求成同一个结果。

### 6.7 提前校验和不发起 SSO 的自检

H2 在 `admin_saml.go` 保存路径增加静态检查，放在 auto/default 派生之后、repo.Save 之前；启用状态不满足时返回 HTTP 400 和明确字段错误，不先保存后等首次登录失败。检查包括：正式公共 Login URL 可确定、它和 ACS 均为 HTTPS 且同源、ACS URL 没有 userinfo/fragment、Cookie 外部 Path 可用、SP EntityID/证书/私钥配置可解析且配对。

正式 Login URL 由配置的公共基址（当前为 SubBaseURL）和 panel_path 得出，不能取管理员请求里未经信任的 Host。未配置公共基址时提示先配置；关闭 SAML 的操作允许完成，不得因失效证书/入口检查阻止管理员停用。

为已保存配置新增**管理员专用、只读 GET** `/api/admin/settings/saml/preflight`。用现有 adminGroup 权限，返回 `configuration_valid`、固定枚举的 `checks`、正式 Login/ACS URL、配置/运行态一致性和 `supported_topology: single_instance`，不返回密钥、Cookie、断言或完整属性；不创建用户、不签发令牌、不写 replay/request 表、不调用真实认证流程。

该端点报告静态和当前运行态检查，不把“数据库可读”当成“replay 可写”，未执行的存储写权限/浏览器 Cookie 验证标为 `not_checked`。真实浏览器验证仍必须单独完成。若请求无法读取配置，明确 5xx；不能返回“检查通过”。

存量配置升级时在 boot 检查并输出可定位错误，管理页显示检查结果；新规则不通过则仅让 SAML 不可用，不阻止面板其他管理功能启动。运行时 Login 仍核对实际可信外部源，防止保存时通过后代理/域名发生变化。

### 6.8 本轮部署能力的明确结论

**H2 和 M1 的发布支持范围均为单实例 SAML。Passkey 挑战仍为进程内存储，也只承诺单实例/原有部署条件。本轮不承诺多实例无粘性登录。** S11 的两个 repo/服务实例只验证持久化防重放，不是两个 HTTP 实例的完整登录证明。

若以后宣布 SAML 多实例可用，必须另补下列证据；缺一项就维持上述单实例结论：

| 后续编号 | 必须新增的端到端验证 |
| --- | --- |
| HA1 | 同一外部 HTTPS origin：Login 路由至 A、真实 IdP 回调路由至 B，共享请求/replay 表，Cookie/账户/JWT 全链路成功 |
| HA2 | 两个 ACS 实例同时消费同一请求/断言、A 在发起后重启或下线：最多一次签发，正确区分可恢复与已消费失败 |
| HA3 | 从 A 修改/关闭 SAML、变更角色规则/密钥、Metadata 轮换：B 按已定义时限看到变更，旧快照不能继续接受本应失效的请求 |
| HA4 | LB 非粘性、可信代理、时钟差、统一配置/密钥及版本切换：运行行为与运维流程都验证，混合旧版本入口不会绕过绑定 |

HA3 目前没有在本计划实现跨进程配置传播，因此不能仅补 HA1 的固定配置 happy path 就把部署范围改成多实例。持久化控制仍保留，既保护单实例重启，也为以后验证提供必要条件。

### 6.9 错误与日志

适配器将外部错误映射到 PSP 可分类的验证失败、请求失效、重放、配置不可用、存储故障。保持对外现有 SSO 失败页面路径；新增原因码时同时补中英文文案。

不把底层错误全文直接塞进回跳 URL。认证事件记录低基数原因码，内部结构化日志可附可定位的错误类别，但不记录 SAMLResponse、私钥、原始 RelayState、Cookie、JWT 或完整属性包。新增日志不重复记录同一层的错误。

## 7. Passkey：H3 修复与独立 M2 实验

先用现有 go-webauthn 路径完成撤销竞态修复、用途隔离和容量限制，并通过对应 W 用例，记录 `H_PASSKEY_SHA`。克隆告警的“拒绝且零写入”原本已存在，属于必须保持的基线，不是 M2 新增收益。

下面端口/Store 适配只在 M2 准入后实施；§7.3 的撤销竞态与 §7.4 的用途/容量规则先在 H3 实现，M2 不改变其语义。M2 依赖 A1 写回失败正式契约及测试，最后按 §1.3 单独决定 adopted/rejected，不因 SAML 已采用而自动通过。

### 7.1 端口与装配

PSP 的 Passkey 协议端口覆盖以下操作，不抽象整个账户登录流程：

- 注册 Begin/Finish。
- 无用户名登录 Begin/Finish。
- 已知用户认证 Begin/Finish，显式携带 `login_second_factor` 或 `step_up` 用途。

输入由服务提供：当前 RP 配置、稳定 user handle、显示名、用途，以及 Finish 的 session ID/请求。Begin 返回 `json.RawMessage` 形式的浏览器 PublicKey options 和 session ID；Finish 返回 PSP 自有证明/已保存凭证结果，不输出外部库 User 类型。

服务仍先读取全局/用户组有效设置并执行允许注册、允许 passwordless 等判断，Finish 再检查一次。handler 仍负责本地登录限制、账户状态、pending token、step-up 操作和最终签发。

适配器每次操作可构造轻量 authcore Service；底层共享的是同一挑战管理器和数据库 repo。不要为每个请求创建新的内存挑战存储。

### 7.2 凭证存储适配

实现 authcore 的 `CredentialStore`：

| 方法 | PSP 实现要求 |
| --- | --- |
| `FindByID` | raw ID → Raw URL Base64 → `FindByCredentialID`；解码原 JSON；由数据库 UserID 生成 handle |
| `FindByUserHandle` | handle 长度必须为 8，解码值必须为正且可表示为 int64；按 userID 查凭证 |
| `Save` | 保持原 JSON/ID/名字处理；使用当前已认证用户归属，禁止前端指定别人的 userID |
| `UpdateSignCount` | 先检查 CloneWarning；没有告警才调用原有条件更新 |

未知凭证映射为 authcore 的 `ErrCredentialNotFound`，数据库错误保留分类，不能全部当作未找到。用户已删除、损坏 JSON、handle 与凭证归属冲突必须失败。

注册标签属于 PSP。使用**每次 FinishRegistration 独立的适配器实例**携带已校验的 userID、规范化后的标签和保存结果；`Save` 一次性写入完整记录，并把设置好 ID/CreatedAt 的结果返回外层。不使用全局可变字段，不先插入空标签再补第二次更新，不把未知类型的业务数据散放到 context。

### 7.3 CloneWarning 和并发写入

以下 M2 执行顺序以 A1 §5.2 已发布为前提，不能依赖旧版本碰巧传播错误：

```text
authcore 完成密码学验证
  → PSP CredentialStore.UpdateSignCount
      → CloneWarning：返回可分类拒绝错误，零数据库写入
      → 正常：调用 UpdateAfterLogin，保留 WHERE sign_count <= newCount
  → PSP 再检查最终证明/当前账户策略
  → 签发令牌或完成第二因素
```

不能只在 Finish 返回后增加一个 if。告警情况下 `credential`、`sign_count`、`last_used_at` 都不得变化。

`UpdateAfterLogin` 返回 false 且没有 error 的现行语义，是另一并发成功登录已写入更高计数时允许本次正常认证继续；不能直接将其改判 clone。真实数据库错误必须阻止认证完成。全零计数的合法认证器仍可使用。

若实现发现凭证被并发撤销时也会得到相同 false，需要专门识别不存在的凭证并拒绝；不要为修复撤销竞态而把所有正常并发计数竞态都拒绝。测试中分别覆盖这两种情况。

具体做法：更新返回 false 时重新按 credential ID 查询，缺失则拒绝，读取错误则失败，仍存在且归属正确、计数已不低于本次结果才按正常竞争继续。认证接纳点是条件更新成功，或这次对正常竞争的确认；后发生的设备撤销阻止后续认证，不在本轮扩展为追回所有在途/已签发令牌。

### 7.4 挑战存储与用途隔离

authcore 的默认 SessionStore 只保存 WebAuthn 会话，不足以表达 PSP 的用途。实现一个共享管理器，每条记录包含：

```text
random_session_id
webauthn_session
purpose = registration | discoverable_login | login_second_factor | step_up
expected_user_handle（discoverable 登录为未知）
rp_config_digest
expires_at
```

要求：

1. ID 使用 32 字节密码学随机数；有效期 5 分钟；一个进程共享一个管理器。
2. 每次 authcore 调用注入带固定 purpose/预期用户/RP 摘要的 SessionStore 代理；代理对共享管理器执行 Put/Take。
3. Take 原子取出并删除，再判断期限和绑定。任何 Finish 尝试不能重用同一挑战；purpose 不符也不允许拿到会话继续验证。
4. 总容量 10,000；先回收过期条目，仍满则 Put 返回资源不可用，不淘汰仍有效挑战。
5. RP 摘要包含 RP ID、Origin 等验证相关参数；修改这些参数后旧挑战失效。品牌文案变化不必让已有挑战失效。
6. 不因重建 authcore Service、普通设置更新而清空管理器。关闭 enrollment/passwordless 则由 Finish 的实时业务检查立即拒绝。
7. 本阶段保持进程内挑战：重启会中断在途 Passkey 操作，用户重新 Begin；不宣称新增跨节点无粘性会话能力。
8. 已知用户来自有效 pending token 或现有认证上下文，不能从未验证的请求字段选择。

现有 `BeginLoginForUser/FinishLoginForUser` 被第二因素和 step-up 共用，需要增加内部用途参数或分别提供两个清晰入口，并更新 `auth_local.go`、`user_me_2fa_stepup.go`。用途由服务端调用点确定，不由浏览器自由传入。

### 7.5 HTTP 与前端兼容

authcore Begin 返回的 CredentialCreation/CredentialAssertion 是包装对象；PSP 现有 API 的 `publicKey` 放的是其 `.Response`。适配器必须输出 `.Response`，不能造成 `publicKey.publicKey` 多一层。

保留：

- Begin 的 `{ session_id, publicKey }`。
- Finish 的 `?session=`，注册的 `?name=`。
- 自服务列表只返回 ID、Name、CreatedAt、LastUsedAt。
- 首个 Passkey 注册成功时按原逻辑发放一次恢复码。
- 现有无用户名登录、密码后二次验证和 step-up 三条路径各自的令牌/权限逻辑。

删除无生产用途的旧 webauthnUser、重复 ceremony 实现和旧存储，而不是为了让旧单元测试继续通过永久保留死代码。把这些测试改到实际使用的新接口。

## 8. 可选实验：GeoIP 与验证码

**G1 推荐独立评估，可以不做；C1 默认不做。** 未准入的章节是备用实施设计，不是本轮漏项。二者均不阻塞 H/M 的交付或实验失败结论。

### 8.1 GeoIP（G1，推荐但可选）

1. `internal/pkg/geoip.Reader` 包装 authcore Reader，保持 Open/Close/Info/Lookup/IsResolvable 的调用方式。
2. 显式转换 `Location → domain.GeoLocation`；不要让领域层 type alias 到外部包。
3. 保留空 Reader/非法 IP/私有 IP/无数据的零值语义，以及损坏数据库时的 error。
4. 验证 MaxMind/GeoLite2、ipinfo flat 和 country_code-only 三类 fixture 的国家、地区、城市映射及空白/大小写处理。额外覆盖 authcore 的 flat city/region、name 和 country_code 回退；这些允许补充过去为空的展示值，不影响账户判断。
5. 确认新增组播过滤后，登录来源等上层展示仍正确。
6. 继续由 `service/geo` 管理文件替换、读写锁和关闭旧 Reader；本阶段不用 authcore Watcher，避免双重刷新和重复关闭。

### 8.2 验证码（C1，默认暂缓）

重启 C1 需要新的准入记录：先列出预期可删除的机制和所有保留兼容要求，按 §1.3 预设预算，再批准候选实现范围。不能仅因 authcore 已经在 go.mod 中就认为新增验证码适配免费。以下硬容量和出站客户端变化若决定做，先在本地验证码单独提交并记 H_CAPTCHA，再比较纯库替换。

1. 图片生成器与图片 Store 在 app 中创建一次，登录/注册/找回密码共用同一实例。
2. 显式设置图片参数为当前 80×240、5 位数字、skew 0.7、80 个噪点，TTL 5 分钟、容量 10,000。
3. 保持空答案不消费；非空错误答案和正确答案都消费挑战。补并发双提交测试。
4. 每次验证从当前 settings 解析 provider/secret/expectedHost；构建固定配置的 TokenVerifier。HTTP client 可共享，不能把旧 secret 固定在长期 verifier 中。
5. 显式传入 `WithAllowedHostnames`、`WithHTTPClient`、`WithTimeout(10*time.Second)`，保留三种第三方 provider 的固定 endpoint。
6. 当前 PSP 的验证码客户端是普通 `http.Client{Timeout: 10s}`，不是已经使用 safehttp。新适配器由 app 注入符合仓库出站规范的客户端；生产 endpoint 仍为固定白名单，测试可注入本地 server client，测试开关不能成为管理 API。
7. 保持现有 hostname 语义：有 expectedHost 且 provider 返回 hostname 时必须匹配；未配置或 provider 省略 hostname 时按现有行为处理。不能把迁移描述成已经实现了强制 hostname 存在。
8. `Result.Success=false` 与网络/配置 error 分开；不把网络失败当作验证码通过。
9. 触发策略、失败次数、注册/找回开关留在 PSP；site key 可公开，secret/token 不进入日志或响应。

## 9. PR 顺序、依赖和结束条件

| 阶段 | 仓库/建议分支 | 工作内容 | 结束条件 |
| --- | --- | --- | --- |
| P0 | PSP / `kazuha/authcore-experiment-baseline` | 加固 ADR、共享实验判据、部署清单、账户/凭证 fixture、计数范围/职责表、依赖快照 | 失败判据已冻结，原行为有证据；不预先宣布所有包迁移 |
| H1 | PSP / `kazuha/saml-validation-hardening` | 原 crewjam 路径的 Destination/多断言/弱算法预检及边界测试 | 不依赖 authcore 的加固可独立发布 |
| H2 | PSP / `kazuha/saml-state-hardening` | 请求表、Cookie、fail-closed、过期接管原子化、配置快照、保存前校验/自检 | SAML 单实例全链路、三数据库与故障注入通过；部署 H_SAML 制品 |
| H3 | PSP / `kazuha/passkey-state-hardening` | 原 WebAuthn 路径的撤销竞态、用途隔离、容量限制 | 相关 W 用例通过；部署 H_PASSKEY 制品 |
| A1 | authcore / `kazuha/authcore-protocol-contracts` | AttributesByName；写回错误拒绝两种 Finish 的正式契约、真实流程测试、文档；发布版本 | §5 的硬性交付全部完成，当前消费者回归通过 |
| M1 | PSP / `kazuha/authcore-saml-experiment` | 从 H_SAML 出发，只替换协议实现、适配器/ports/装配 | 同一组 S 用例通过；§1.3 成本/职责判据通过才采用，否则记录 rejected 并保留 H |
| M2 | PSP / `kazuha/authcore-passkey-experiment` | 从 H_PASSKEY 出发，只替换 WebAuthn 编排与存储桥接 | A1 写回契约 + W 实引擎测试 + 独立收益判据通过才采用 |
| G1 | PSP / `kazuha/authcore-geoip-experiment` | 可选 MMDB 包装实验，可使用固定 v0.3.0 | G 用例及净成本门槛通过才采用；也允许 not_attempted/deferred |
| C1 | 暂不创建分支 | 验证码候选设计保留 | 默认 deferred；重新准入不属于 M1/M2 的完成条件 |
| E1 | PSP + authcore 文档 | 每包结论、成本、职责/依赖变化、回滚制品与剩余范围 | adopted/rejected 均有证据；deferred/not_attempted 不伪装成实验结果 |

两条依赖线：`P0 → H1 → H2 → H_SAML 发布`，以及 `P0 → H3 → H_PASSKEY 发布`；`P0 → A1` 独立进行。`H_SAML + A1 → M1`，`H_PASSKEY + A1 → M2`。默认先给出 M1 结论，再决定是否启动 M2；M1 被拒绝不自动否定 M2，但也不自动授权扩大抽象修补范围。

表中“从 H 出发”指具备该加固基线，不要求从过时发布提交分叉。每个 PSP 阶段仍从新 fetch 的 `origin/main` 建分支，确认前置 H 已在其中；M2 必须保留 M1 及其他已合并改动。测量对照与回滚制品也要包含当时其他已交付的安全控制，不能为回退 SAML 而顺带撤销后来发布的 Passkey 加固。

G1 只依赖自己的 P0 记录/固定版本，不要求 A1，也不与 SAML 发布绑定。C1 默认不执行。新增行为测试随相应 H 实现提交；P0 不把 main 留在红灯。

M 候选先建立适配器、更新调用方、运行与 H 相同的测试，再移除被替代实现并测量。可以在候选工作树临时保留对照测试输入，生产只运行一种引擎。不要“双跑两个有副作用的验证器”：它们会分别消费请求/断言/挑战，无法在同一生产请求上直接比较。

每个 PR 描述列出：属于 H 还是 M、具体行为变化、涉及数据、验证结果、未覆盖场景和回滚制品。M 的描述必须附 H→M 计数/职责报告；不能把 H 的加固收益当成 M 的新增收益。缺少这份报告就不是可合并的实验结论。

## 10. 验收矩阵

适用范围：S 用例在 H1/H2 分阶段落地，并在 M1 原样重跑；A 用例属于 A1；W 的既有行为/加固用例先在 H3 跑，再在 M2 实引擎接线下重跑。G/C 仅在对应实验准入时适用，未执行不算失败也不算通过。R 用例按该阶段受影响路径执行。

### 10.1 authcore 与 SAML

| ID | 场景 | 必须验证的结果 |
| --- | --- | --- |
| A01 | Name 与 FriendlyName 不同/碰撞 | 新 map 只按 Name；旧 map 行为保持；无权限映射范围扩大 |
| A02 | 多 Statement、多值、重复 Name、空值 | 顺序及数据不丢失，PSP 映射与基线一致 |
| A03 | 两种 Finish 的有效认证后 Store 返回 sentinel error | Store 调用一次；result 必须 nil，error 非 nil 且 errors.Is 可识别；session 不能重用 |
| A04 | 两种真实认证流程触发 CloneWarning 并由 Store 拒绝 | 同 A03；与正常 Store 成功的对照都通过，不能靠协议始终失败假绿 |
| A05 | 临时变异为吞掉写回错误后返回成功 | A03/A04 必须失败；恢复实现后通过；该正式契约与说明同步 |
| S01 | 有效签名响应 | 完成原有账户绑定/开户和令牌签发 |
| S02 | 错签名、错误 Issuer/Audience、过期/未来断言 | 拒绝，无账户创建/令牌/成功事件 |
| S03 | 缺 Destination、错误 Destination、多 Assertion | 拒绝；必须经过真实 HTTP ACS 入口 |
| S04 | 弱算法、超大 body/XML、压缩膨胀输入 | 拒绝且有限资源占用；对应算法测试不只测字符串扫描 |
| S05 | 加密且签名有效的断言、错误解密密钥 | 前者兼容，后者拒绝；报告算法检查的实际边界 |
| S06 | Entra 风格 Base64 换行、未指定 NameID Format | 可解析；AuthnRequest 不强制 transient |
| S07 | NameID 不变而 UPN 改名、同名冲突 | 原账户延续；冲突不串号 |
| S08 | NameID/配置的 UPN 属性缺失 | 拒绝，不偷偷用 email 或其他 claim 补齐 |
| S09 | 角色/用户组撤销、首条规则匹配、开户例外 | 与原 EnsureSSO 一致，包含管理员/操作员路径 |
| S10 | 账户禁用/待审批、仅配额或到期暂停服务 | 前者拒绝登录；后者仍可登录 |
| S11 | 同断言并发、重建服务、共享数据库两个 repo/服务实例 | 存储/防重放层最多一次成功；不把它命名为多实例 HTTP 全链路测试 |
| S12 | replay repo 写入/查询故障、缺失 repo | 拒绝且记录存储故障；无内存放行 |
| S13 | 已过期 replay 行并发接管 | SQLite/MySQL/PostgreSQL 恰好一人取得首次资格 |
| S14 | replay 清理边界和 clock skew | 有效窗口内记录不能被删除或提前过期 |
| S15 | 伪造/过期/重复 RelayState、错误请求 ID | 拒绝，无开户和令牌 |
| S16 | token/断言复制到另一浏览器、Cookie 缺失 | 拒绝；不能把服务器记录存在当成浏览器绑定 |
| S17 | 相同请求并发 ACS、绑定正确但验签失败后再试 | 一次性消费；失败后也必须重新 Begin |
| S18 | 非根 panel_path、自定义同源代理回调、可信反代 HTTPS、两个标签页 | Cookie 外部路径/属性、请求关联、清除和回跳正确 |
| S19 | 恶意 return_to、篡改 RelayState 的回跳参数 | 只回服务端已清理路径，无开放重定向 |
| S20 | Reload 与 Metadata 慢请求交错、禁用配置 | 无旧结果覆盖；没有新 cfg/旧 Provider 组合 |
| S21 | 配置构建失败、同配置 Metadata 临时失败 | 前者停止新登录；后者保留同配置 Provider |
| S22 | Login 后改变信任或映射配置 | 原请求拒绝，重新发起后使用新配置 |
| S23 | 存储的请求消费失败、过期清理 | 数据库 error 不当作未命中；只清理过期请求 |
| S24 | Cookie 被浏览器拦截、外部非 HTTPS、Login/ACS 不同源 | 可定位失败，无安全降级 |
| S25 | panel_path SSO apply/rollback、同时有管理员更新 | 配置和运行态一致；旧补偿不能覆盖新修改；OIDC 行为保持 |
| S26 | 保存启用配置前的入口校验、GET preflight、停用及存量启动检查 | 非 HTTPS/不同源/无法确定正式入口在 Save 前失败；停用允许；自检无写入/无签发/无秘密 |
| S27 | 全库故障、replay 单独故障、只读、短暂故障后恢复 | 按 §6.5.1 记录原行为与新拒绝差异，不声称增量为零；修复后重新 Begin 成功 |
| S28 | auth_events 写入失败及 replay 活跃行清理边界 | 日志/进程计数仍记录正确基础设施原因；合法 GC 不删活跃行，内存兜底不是验收依赖 |

现有 `TestAssertionAlreadyConsumed_StoreErrorFallsBackToMemory` 等测试与新决定冲突，应替换断言并引用后继 ADR，而不是简单删除。已有 repo 的重启、并发、清理测试继续保留。

### 10.2 Passkey

| ID | 场景 | 必须验证的结果 |
| --- | --- | --- |
| W01 | 迁移前导出的测试凭证数据 | 不重注册即可登录；handle、RP ID、Origin 一致 |
| W02 | 注册真实虚拟认证器、重复凭证 | 正常注册；重复不能覆盖别人的凭证 |
| W03 | 用户 A 挑战被用户 B 完成 | 拒绝，无跨账户注册或认证 |
| W04 | usernameless 返回其他账户 handle | 依据数据库凭证归属且执行 handle 交叉校验 |
| W05 | 无用户名登录缺 UV，合法认证器 UV | 前者拒绝、后者通过 |
| W06 | password 后第二因素与 step-up | pending token/当前用户/用途正确；不能相互替代 |
| W07 | session 过期、重放、并发 Take、用途互换 | 最多一次可取且不能越用途 |
| W08 | CloneWarning | 拒绝且 credential、sign_count、last_used_at 零变化 |
| W09 | 全零计数、正常增长、两次正常并发增长 | 全零不误杀；数据库不回退；无告警的正常竞争不误判 clone |
| W10 | 凭证在最终写入/竞争确认前撤销、账户删除、损坏 JSON | 不签发；不重新创建被删除凭证；接纳点之后的撤销语义见 7.3 |
| W11 | 数据库写入失败 | 不签发、不报告注册完成 |
| W12 | 注册标签、默认标签、数据库返回 ID | 原子保存完整记录，界面没有暂时的空标签 |
| W13 | enrollment/passwordless 开关和组覆盖变化 | Begin/Finish 按当前策略；无默认开放 |
| W14 | RP 配置改变、仅品牌改变、服务重新构造 | RP 改变的旧挑战拒绝；仅品牌改变不无故丢失挑战 |
| W15 | 非管理员禁本地登录、账户/服务两轴状态 | 与密码登录及既有 Passkey 策略保持一致 |
| W16 | 浏览器 Begin/Finish JSON | publicKey 层级、session/name 参数不变 |
| W17 | 首个 Passkey、已有恢复码、撤销最后因素 | 恢复码只按现有策略产生或保留，管理权限不变 |
| W18 | 容量耗尽与过期回收 | 拒绝新建、已有有效挑战仍可完成；回收后恢复 |
| W19 | M2 真实 authcore 引擎传播 Store 策略拒绝/存储错误到 handler | 无 LoginResult/令牌；CloneWarning 分支数据库三字段不变；不能只测 mock 引擎 |

至少 W01/W02/W03/W05/W06/W08/W16/W17 使用真实虚拟认证器完成 WebAuthn ceremony；不能只把 mock 的 Finish 设置为成功。浏览器层另验证 ArrayBuffer/Base64 转换及调用参数。

### 10.3 验证码、GeoIP 与回归

| ID | 场景 | 必须验证的结果 |
| --- | --- | --- |
| C01 | 图片成功、错误答案、空答案、并发提交 | 一次性语义明确，空答案行为兼容 |
| C02 | 登录发挑战，注册/找回验证 | 共享同一存储；对应业务启用策略分别正确 |
| C03 | Turnstile/reCAPTCHA/hCaptcha | 请求字段、成功/失败/异常 JSON、超时按既有契约 |
| C04 | provider/secret/site 域名热更新 | 下一次请求使用新配置，旧 verifier 不继续生效 |
| C05 | hostname 错误、省略、未配置 | 与显式规定一致；不声称省略时也强制校验 |
| C06 | 容量/TTL/进程重启 | 有界、过期失效；重启后重新获取验证码 |
| G01 | 三类 MMDB fixture | 国家/代码/地区/城市、空白及大小写符合契约 |
| G02 | nil/私有/非法/组播 IP、未知地址 | 零值无异常；损坏库仍返回真正错误 |
| G03 | 下载替换与查询并发、关闭 | 无已关闭 Reader 访问、无双重 watcher |
| R01 | 原 OIDC 登录、退出、刷新 | 无非预期变化，state/nonce/PKCE 仍有效 |
| R02 | 原密码/TOTP/恢复码登录 | 无因素绕过、账户状态及 TokenVersion 仍有效 |
| R03 | 全新数据库及升级数据库 | 新表可建；旧用户/配置/凭证数据保持可读 |
| R04 | 日志与错误响应 | 无断言、密钥、token、Cookie 或完整凭证泄漏 |
| R05 | M1/M2 切回对应 H 制品 | 原始凭证/数据可读；SAML 严格校验、绑定、fail-closed 和 Passkey 加固仍通过 |
| R06 | H/M 依赖与职责报告 | direct/indirect/实际制品分开记录；结果按冻结判据决定，不把安全加固计作共享收益 |

## 11. 验证命令与证据

以下是实施阶段命令，不表示编写本文时已经执行。使用仓库实际 `go.mod`/toolchain；不要依据旧文档自行降低 Go 版本。

authcore 工作树：

```sh
GOWORK=off go build ./...
GOWORK=off go vet ./...
gofmt -l .
GOWORK=off go test -race -count=1 ./...
```

`gofmt -l` 应无输出；漏洞检查沿用该仓库固定配置的 CI，不引入浮动工具版本改变结果。

H 不需要 `internal/adapters/authcore` 目录，先跑原路径的服务/HTTP/sqlstore 测试和 H 新增的 `internal/pkg/samlguard` 测试。下面是 M 阶段检查示例，按实际采用的包选择，不能因 G/C 默认不做而创建空包凑命令：

```sh
GOWORK=off go test -race -count=1 ./internal/adapters/authcore/... ./internal/service/auth/... ./internal/service/passkey/...
GOWORK=off go test -race -count=1 ./internal/transport/http/...
GOWORK=off go test -race -count=1 ./internal/adapters/sqlstore/...
GOWORK=off go test -race -count=1 ./internal/service/captcha/... ./internal/pkg/geoip/... ./internal/service/geo/...
GOWORK=off go vet ./...
```

提交前跑当前 CI 要求的全部 Go 测试；本地可用 `GOWORK=off go test -race -count=1 ./...`，CI 当前以分片执行。不要反复跑已经通过的全套测试，除非后续修改或失败需要复验。

数据库测试在独立测试实例运行，不能指向生产 DSN：

```sh
PSP_TEST_DB_KIND=postgres \
PSP_TEST_DB_DSN='postgres://psp:psp@localhost:5432/psptest?sslmode=disable' \
GOWORK=off go test -count=1 ./internal/adapters/sqlstore/...

PSP_TEST_DB_KIND=mysql \
PSP_TEST_DB_DSN='root:psp@tcp(127.0.0.1:3306)/{schema}?parseTime=true&multiStatements=true' \
GOWORK=off go test -count=1 ./internal/adapters/sqlstore/...
```

DSN 是现有 CI 测试 fixture 的示例，按本地测试实例调整。当前 PostgreSQL/MySQL CI 跑 sqlstore 包，而不是所有包；涉及新 SQL 的用例必须放到会实际执行的位置。

前端和可运行二进制验证，先构建前端：

```sh
cd web-react
npm ci
npm run test
npm run build
npm run smoke:dist
cd ..
GOWORK=off go build -ldflags='-s -w' -o psp ./cmd/panel
```

检查 `tsc -b` 生成的受跟踪文件是否确有必要变化；不要带入无关构建产物。前端 smoke 成功不等于 SAML 真正跨站登录成功。

每阶段记录：两个仓库 SHA、H/M 分类、authcore 固定版本（H 不适用）、命令、通过/失败、数据库类型、浏览器、测试 IdP、验收矩阵编号与证据位置。失败用例记录原因，不能只写“编译通过”。M 还必须附 §1.2 的行数/职责/依赖报告及判据计算。

## 12. 发布、切换和回滚

### 12.1 上线前

1. 保存可恢复的数据库备份和上一版本制品；记录当前 schema、配置版本和依赖版本。上线 M 前额外保留对应 H 的源码 SHA、可部署制品、摘要及回滚演练记录，不能只有 B0 制品。
2. 使用测试 IdP/测试账户检查：外部 Login/ACS HTTPS 且同源、Destination、实际签名算法、NameID 稳定性、加密断言是否使用、Metadata 刷新是否正常。自定义回调单独检查 Cookie 的外部 Path。
3. 确认一个现有本地管理员恢复入口可用，沿用既有账户策略，不为迁移新开公共后门。
4. 完成真实浏览器跨站 POST、非根 panel_path 和代理部署验证。至少覆盖当前支持的 Chromium 与 WebKit 浏览器。
5. 检查新的认证错误分类可观察，日志脱敏；密钥和 IdP 样本不提交到仓库。
6. 调用下面的管理自检，先发现静态入口问题，无需尝试一次 SSO 登录。该端点由 H2 新增，当前基线尚不存在；使用既有管理员凭证，不提供公开免认证检查。

```sh
PSP_PUBLIC_ORIGIN='https://panel.example.com'
PSP_PANEL_PATH='/panel'
# 在当前安全终端中提供既有管理员 token；不要把真实 token 写进文档或日志。
curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer ${PSP_ADMIN_TOKEN:?请先提供管理员令牌}" \
  "${PSP_PUBLIC_ORIGIN}${PSP_PANEL_PATH}/api/admin/settings/saml/preflight"
```

根路径部署将 `PSP_PANEL_PATH` 设为空。检查 `configuration_valid`、每项 checks 和单实例声明；HTTP 200 本身不代表检查全通过。preflight 不证明浏览器接受 Cookie 或数据库未来可写，仍需相应验收证据。

### 12.2 B0→H：安全加固的第一次切换

这次切换才引入新的 RelayState、Cookie 和严格校验；不要把它与 M1 的库替换当成同一次发布。未加固与加固的入口不长期混合，旧入口可能绕过请求绑定。

1. 安排短维护窗口，停止发起新的 SAML 登录并排空旧请求。
2. 等待窗口取“在途 AuthnRequest TTL”和“部署中断言/SubjectConfirmation 最大可接受有效期 + clock skew”的较大值；先从测试 IdP 和配置确认，不能固定拍成 5 分钟。不能确认窗口时不声称已完成无缝安全切换。
3. 保留现有 replay 表数据，上线 H1/H2，新增请求表由 H2 建立；先验证单实例正式入口符合 §6.8 的支持范围。
4. 旧登录页面/旧 RelayState 一律提示重新开始，不开兼容回退。
5. 验证新登录、旧账户、角色/组映射、配置重载和 replay 存储故障路径。
6. H 通过后记录可回滚制品，再启动 M；不要让 H 首次上线与 M 首次上线共享一个不可拆的提交或镜像。

已有多实例部署不在本轮自动升级承诺中。先完成 HA1–HA4，或按部署计划暂收敛到已支持的单实例；“共享数据库且配置看起来一致”不是替代验证。

### 12.3 H→M：库替换与可选包切换

M1 必须保留 H 的请求表、RelayState/Cookie 格式、配置摘要算法、错误语义和 replay 保留期。不得在这个阶段再改流程协议；如果发现必须改变它们，应回到独立 H 修订，产生新 H 制品后重新测量。

只有验收与 §1.3 判据通过的包才上线。对 M1/M2 先排空单实例的在途请求再换制品，不承诺跨版本保留所有临时状态。回滚演练在复制测试数据上进行，不能重放用户真实断言。

持久凭证和账户不迁表。进程内在途挑战不能跨版本保留，发布说明提示重新开始注册/登录或刷新验证码；用户已有 Passkey 不应重注册。

GeoIP 作为 G1 独立发布/撤销；验证码 C1 默认不发布。若 C1 重新准入，先发布它自身的行为变化 H，再测量/发布包装替换。

不能用“生产双校验”验证正确性：第一次校验会消费挑战或写计数器。比较应在复制的测试数据和独立测试流程中完成。

### 12.4 回滚边界

| 回滚/退出对象 | 正常目标 | 必须保留 |
| --- | --- | --- |
| M1 失败或共享收益不成立 | 已验证的 H_SAML 制品；源码恢复 H 引擎 | Destination/多断言/弱算法校验、请求绑定、fail-closed、配置快照和原子存储 |
| M2 失败或共享收益不成立 | 已验证的 H_PASSKEY 制品；源码恢复 H 引擎 | 原凭证格式、克隆拒绝零写入、撤销竞态修复、用途隔离与计数器单调性 |
| G1 不成立 | G1 自己的本地 Reader 基线 | 不影响 SAML/Passkey 是否采用 |
| H 本身有故障 | 优先暂停受影响登录方法，修复/回滚该独立 H 发布 | 不伪称退回 B0 没有安全损失；具体被撤销的保护另行记录 |

M 的回滚不 DROP 请求/replay 表，不恢复旧数据库备份覆盖上线后的业务数据，不回退 Passkey 计数器。H 与 M 用相同 schema/线上状态契约；恢复本地引擎后用 R05 验证保护仍在。

上述 H 制品必须包含待回滚版本中其他已经交付的加固。若最初的 H_SAML/H_PASSKEY 制品早于后来独立发布的安全修复，不能直接拿该旧二进制回退；应在当前发布基线上只恢复相应本地引擎，重新构建、验证并保存回滚制品。上线每个 M 前就完成这个准备，并对照安全控制清单验证，不在故障时临时推断。

源码回退不能在日后混有其他功能改动时简单执行大范围 git revert。单独替换协议后端并保留 H 控制，重新跑共享行为测试；只要其他包仍采用 authcore，就不能为退出一个包删除整个模块依赖。

停用/切换制品后清理或等待临时 Cookie 过期，在途操作重新开始。不提供“authcore 校验失败则调用本地校验”的运行时降级开关。保留 H 制品/源码供回滚，不等于生产同时运行两套可由输入选择的认证路径。

## 13. OIDC 后续阶段的准入

本轮完成记录明确写“OIDC 仍在 PSP”。只有在另一实际消费者也确认可共用的协议接口后，才提出 `authcore/oidc` 的独立实验计划，预先定义采用/退出判据。

届时允许抽取：Discovery、授权 URL、code exchange、ID Token 的 issuer/audience/签名/有效期验证、nonce/PKCE 的协议约束、中性 claims 输出。

PSP 继续承担：state 与浏览器关联、Cookie、return_to、配置生命周期、SSO 绑定、角色/组规则、JWT 和账户控制。出站 discovery/token HTTP 必须可注入，保留 PSP 的 safehttp 与超时。

不能因名字相同就抽一个跨 SAML/OIDC/Passkey 的泛型认证流程；三种协议的状态与身份语义并不相同。

## 14. 最终交接清单

### 14.1 必交：加固和实验结论

- [ ] P0 在候选结果出来前固定文件/职责范围、计数方法、依赖快照与退出标准。
- [ ] H1/H2/H3 各自完成相应验收和发布，H_SAML/H_PASSKEY 的 SHA、制品及摘要可找回。
- [ ] 后继 ADR 正面说明 ADR 0023 的局部故障可用性损失、缓存删除的条件/边界及过期接管并发语义。
- [ ] H2 的保存前校验、存量启动诊断、管理员 GET preflight 可用；只读自检不伪称完成浏览器验证。
- [ ] 新表已纳入 schema/repo 注册，三数据库通过；原账户/凭证不要求重写。
- [ ] SAML 候选有实际 adopted/rejected 结论和证据；若受阻则明确 deferred，不能宣称实验已完成。
- [ ] 每包状态分别写明，GeoIP 可不做、验证码默认 deferred、OIDC 未实施；没有强制凑第二消费者。
- [ ] 发布声明明确为单实例 SAML；HA1–HA4 未完成就不声明多实例支持；Passkey 内存挑战边界单列。

### 14.2 仅对 adopted 的包要求

- [ ] 所需 A1 契约/API 和上游测试已发布到实际固定 tag，当前消费者兼容验证通过。
- [ ] 若采用 Passkey，两个 Finish 的写回错误均保证 nil result + error；上游敏感性测试及 PSP 实引擎 W19 通过。
- [ ] 没有本地 replace/go.work 隐含依赖；direct/indirect/二进制依赖分别记录。
- [ ] H→M 同行为测试、成本/职责计算和 §1.3 判据全部通过；不把 H 的安全收益算成 M 的收益。
- [ ] 相应构建、race、HTTP/浏览器、测试 IdP 或认证器证据齐全，未验证项明确列出。
- [ ] 生产只有选定实现；被替代本地实现保存在 H 源码/制品中用于回滚，不为旧测试留下死代码。
- [ ] M→H 回滚演练通过 R05：拒绝库迁移不会撤销已交付的安全加固。

### 14.3 对 rejected/deferred/not_attempted 的包要求

- [ ] rejected 附实际测量和失败判据，保留 H；删除仅服务于失败候选的 PSP 桥接代码。
- [ ] deferred/not_attempted 写明原因和重新准入条件，不以缺少实验数据断言该包必定有/无价值。
- [ ] 共享库新增 API 的保留/弃用交由共享库依据其他消费者处理，不在 PSP 任务中擅自删除。

实施记录新增 `docs/authcore-migration-implementation.md`，按每个包填写下表，并链接测量原始数据。每阶段只写已完成且有证据的结果；本计划不代填成功。

| 包 | B0/H/M SHA | A/D/Δ | 转移的职责及上游测试 | 依赖变化 | 行为验证 | 采用判据 | 结论与回滚制品 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| SAML | 待实施 | 待测量 | 待测量 | 待测量 | 待执行 | §1.3 | 待实验 |
| Passkey | 待准入 | 待测量 | 待测量 | 待测量 | 待执行 | §1.3 | 独立候选 |
| GeoIP | 未开始 | 未测量 | 未测量 | 未测量 | 未执行 | Δ<=0 | 可选 |
| 验证码 | 未开始 | 未测量 | 未测量 | 未测量 | 未执行 | 重新准入后 Δ<=0 | 默认 deferred |

实施改变设计时记录原因和替代方案；不得删除失败实验，也不得在结果出来后改写 P0 的门槛来制造成功。

## 15. 外部源码参考

- [authcore v0.3.0](https://github.com/KazuhaHub/authcore/tree/v0.3.0)
- [SAML 配置及请求构建](https://github.com/KazuhaHub/authcore/blob/v0.3.0/saml/provider.go)
- [SAML 校验顺序](https://github.com/KazuhaHub/authcore/blob/v0.3.0/saml/validate.go)
- [SAML 属性转换](https://github.com/KazuhaHub/authcore/blob/v0.3.0/saml/assertion.go)
- [Passkey 凭证存储契约](https://github.com/KazuhaHub/authcore/blob/v0.3.0/passkey/credential_store.go)
- [Passkey 登录的写库顺序](https://github.com/KazuhaHub/authcore/blob/v0.3.0/passkey/authentication.go)
- [Passkey 无用户名登录的写库顺序](https://github.com/KazuhaHub/authcore/blob/v0.3.0/passkey/discoverable.go)
- [authcore ADR 3：共享范围及退出标准](https://github.com/KazuhaHub/authcore/blob/1d5b0ee6a6799cfe57c5d5ec57037bcefd0fc5f8/docs/adr/0003-ratelimit-and-clientip.md)
- [authcore 对共享收益的评估](https://github.com/KazuhaHub/authcore/blob/1d5b0ee6a6799cfe57c5d5ec57037bcefd0fc5f8/docs/does-authcore-earn-its-place.md)

以上链接用于定位基线，不能替代实施时对所固定版本的检查。
