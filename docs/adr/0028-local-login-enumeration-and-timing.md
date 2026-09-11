# ADR 0028：本地登录的账号枚举与计时旁路防护

- **状态**：已接受（校验顺序与锁定计数归一化：v3.9.2-beta.4；`dummyBcryptHash` 计时抹平早于此）
- **日期**：2026-08-14
- **相关代码**：`internal/service/user/user.go`（`VerifyLocalPassword`、`dummyBcryptHash`）、`internal/domain/upn.go`（`NormalizeUPN`）、`internal/transport/http/handler/auth_local.go`（`AuthLocalHandler.Login` 里的 `guardUPN`）、`internal/adapters/sqlstore/user_repo.go`（`GetByUPN` 的双探针）、`internal/service/loginguard`

## 背景

本地登录端点是匿名可达的，所以它的**每一个可观测差异**都是一条信息通道：状态码、错误文案、响应时间，以及"这次失败有没有被计进锁定计数"。这里记录两处曾经被打破、且都属于"把便宜的检查提前"这种直觉性重构会再次破坏的属性。

### 一、状态检查排在密码校验之前 = 免费的"禁用账号"枚举

`VerifyLocalPassword` 要做三件事：查账号、比密码、看账号状态（`AccountLoginAllowed`）。状态检查显然比 bcrypt 便宜得多，把它提前是任何性能直觉都会给出的建议。

但只要状态检查跑在密码校验之前，**任何未认证的调用方发一个乱填的密码，就能凭返回的 403 把"这个账号存在且被禁用/待验证"与其它所有结果区分开**。而且这类探测**连锁定都不会触发**——登录失败计数器记的是 `invalid_credentials`，走 `ErrForbidden` 这条路的探测根本不进计数，可以无限次重复。

泄漏范围是有限的：启用的账号和纯 SSO 账号返回的都是同样的 401，所以能枚举出来的只是"已禁用"这个子集，不是完整用户列表。但那个子集恰恰是有价值的（配额耗尽的、被封的、待验证的）。

### 二、UPN 不归一化 = 锁定与验证码门可以被空白字符绕过

`users.upn` 是一个没有 `COLLATE` 的普通 `varchar(255) UNIQUE`，而三个后端对它的理解并不一致（MySQL 默认折叠大小写甚至重音，Postgres 与 SQLite 逐字节比较）。`domain.NormalizeUPN`（`ToLower(TrimSpace)`）就是为了让这个既有索引在三个方言上表达同一个不变量而存在的。

其中 **`TrimSpace` 是更不显眼、也更要命的那一半**。Go 的 `TrimSpace` 会剥掉空格、`\t`、`\n`、`\v`、`\f`、`\r`、U+0085 和 U+00A0——这是一个**无界的变体集合**。账号查找一直是 trim 过的，所以 `" admin"`、`"admin\t"`、`"\nadmin"` 认证的都是同一行；但锁定 / 验证码计数器当初键在**原始字段**上，于是每个变体各自持有一份独立的失败计数——攻击者只要每次换一个空白前缀，就能一路走过 `"admin"` 早已触发的锁定与验证码门。

## 决策

### 1. 密码 FIRST，账号状态 SECOND

`VerifyLocalPassword` 里 `bcrypt.CompareHashAndPassword` 必须跑在 `domain.AccountLoginAllowed` **之前**。顺序本身就是那条安全属性：**先证明你拥有这个账号，才轮到你知道它的状态。**

调用方仍然拿得到 `ErrForbidden`（处理器需要它来给出带原因的 403），只是必须先过密码这一关。为此 `VerifyLocalPassword` 在 `ErrForbidden` 这一条路上**仍然返回非 nil 的用户指针**（其它错误一律返回 nil），这是它与众不同的签名语义的由来。

### 2. 未命中的路径也要烧掉一次 bcrypt

包级的 `dummyBcryptHash` 用 `bcrypt.DefaultCost` 生成一次，然后在两条"根本没有真实哈希可比"的路径上被拿来做一次真实的比较：

- 账号查不到（`GetByUPN` 返回错误）
- 账号存在但没有本地密码（纯 SSO 账号，`!u.HasLocalPassword()`）

不这么做的话，未知 UPN 会**明显更快**地返回——一个不需要任何猜测的 UPN 枚举计时 oracle。成本必须用同一个 `DefaultCost` 生成，否则抹平的只是量级、不是差异。

### 3. 锁定 / 验证码 / 审计的键必须与账号解析用**同一个**归一化

`AuthLocalHandler.Login` 一开始就算出 `guardUPN := domain.NormalizeUPN(req.UPN)`，`loginguard.Evaluate`、验证码判定和 `recordAuthEvent` 全部用它。

要点不是"用了 `NormalizeUPN`"，而是**它和账号解析用的是同一套归一化**。`GetByUPN` 采用双探针：先用 trim 后的原串精确命中（解析归一化之前写入的历史行），未命中再用 `NormalizeUPN` 命中规范行。`guardUPN` 取的是**更粗的**那一端（始终归一化），所以两个历史遗留行 `"Admin"` 与 `"admin"` 会共用一个失败计数——多算，不漏算。这是安全的方向：守门的粒度可以比身份粒度粗，绝不能比它细。

`GetByUPN` 的探针 1 是一层**有退休计划的兼容垫片**：归一化之前，`CreateLocal` 存的是管理员原样输入、`EnsureSSO` 存的是 IdP 断言原文（未 trim）。只折叠输入、只探一次，会让这些行**立刻全部不可达**——一次真实的用户（可能包括管理员）硬锁死。等启动期的 upn 审计报告干净、且运维跑过一次性回填之后，它才能去掉。

## 后果

**正面**：匿名调用方无法凭 403 枚举被禁用账号；未知 UPN 与错误密码在响应时间上不可区分；锁定与验证码门无法用空白字符变体绕过。

**代价**：每一次登录失败——包括打向不存在账号的——都要付一次完整的 bcrypt 成本。这是刻意的：让攻击者也付这个成本正是设计目的之一。

**运维**：启动期的 upn 归一化审计**只告警、绝不拒启**。它报告两类行：`LOWER(upn)` 重复的行组（折叠会撞唯一索引，必须由人决定保留哪个账号——谁的 sub token、谁的配额、谁的流量历史）；以及 `upn != NormalizeUPN(upn)` 的非规范行（可安全折叠）。它是回填的前置条件。

**必须保持的不变量**（未来修改时不能破坏）：

- `bcrypt.CompareHashAndPassword` 必须排在 `AccountLoginAllowed` 之前。把状态检查提前是一次纯性能优化，**不会有任何测试变红**。
- 两条未命中路径必须继续烧一次 `dummyBcryptHash` 比较。看起来像死代码（结果被丢弃），它的全部价值就在于耗时。
- `guardUPN` 与账号解析必须共用同一套归一化。改了一侧就要改另一侧，否则分歧只是换了个位置继续存在。
- `NormalizeUPN` 的输出**不能随便改**——它一改，每一个既存账号的身份分区都会被静默重新划分。改它需要配套迁移方案。
- `GetByUPN` 的探针 1 在启动审计报干净且回填跑完之前不能删。

## 考虑过但未采用的方案

- **状态检查前置**（"便宜的检查先做"）：正是被修掉的枚举面。这条会被反复重新提出，因为它在纯性能视角下永远是对的。
- **不烧 dummy bcrypt，未命中直接返回**：省一次 bcrypt，换回一个计时 oracle。
- **用方言侧的 `COLLATE` 解决大小写归一**：三个后端做不出一致语义（SQLite 的 `NOCASE` 只折 ASCII，MySQL 连重音一起折，Postgres 要 `CITEXT` 扩展），而且改列排序规则意味着在 `EnsureSchema` 里维护三套 DDL。放在 Go 侧归一化，那个**本来就存在**的普通唯一索引就在三个方言上开始强制"一名一身份"，顺带堵掉 `GetByUPN` → `Create` 之间的 TOCTOU 竞态。
- **在 `NormalizeUPN` 里做 Unicode NFC 归一化**：明确**排除在范围之外**。同一个重音名字的组合形式与分解形式在这里仍是两个身份。这是已知且被接受的限制，不是疏忽——重新考虑它需要带上迁移方案，因为改变这个函数的输出会静默重新划分每一个既存账号。
- **用 `ToLowerSpecial`**：它依赖 locale，会让身份随服务器 locale 变化——正是这个函数要消灭的那一类 bug。
