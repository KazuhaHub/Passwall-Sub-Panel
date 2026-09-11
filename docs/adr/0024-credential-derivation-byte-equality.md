# ADR 0024：渲染侧派生的凭据必须与共享客户端存储的凭据逐字节相等

- **状态**：已接受（追认既有决策；实现自 v3.9.0 共享客户端模型起）
- **日期**：2026-08-14
- **相关代码**：`internal/pkg/crypto/derive.go`（`DeriveProxyPassword` / `NewProxyPassword` / `ss2022KeyLen`）、`internal/pkg/clientplan/clientplan.go`（`passwordForClass`、`pwClassDefault` / `pwClass256` / `pwClass128`）、`internal/service/sharedclient/sharedclient.go`（`buildSharedClientSpec`）、`internal/service/render/protocols.go`、`internal/service/render/singbox.go`、`internal/service/render/urilist.go`、`internal/domain/pspclient.go`（`PSPClient.UUID` / `.Password`）

## 背景

面板有两条完全独立的代码路径会产生"同一个用户在同一个节点上的那一份凭据"：

- **写入路径**：v3.9.0 的共享客户端模型把凭据**存**在 `psp_clients` 行上（`domain.PSPClient.UUID` / `.Password`），由 `clientplan.passwordForClass` 在建计划时按凭据类生成一次，之后 `sharedclient.buildSharedClientSpec` 原样推给 3X-UI。
- **渲染路径**：`/sub` **从来没有读过 `psp_clients` 上的凭据**。三种输出格式（Clash/Mihomo 的 `protocols.go`、sing-box 的 `singbox.go`、URI-list 的 `urilist.go`）**至今仍对每一份凭据现算** `crypto.DeriveProxyPassword(u.UUID, protocol, settings.Method)`。（它确实会读这张表，但只为一件与凭据无关的事：Naive 用客户端名做认证，所以三个渲染器都会调 `render.sharedClientEmailsByNode` 取出**已 provisioned** 的共享客户端 email——`UUID` / `Password` 两列则一次都没被读过。）

这两条路径之间**没有任何编译期或运行期的耦合**：它们不共享函数、不互相调用；即使 render 读到了同一行 `psp_client`，它取的也只是 `Email`，凭据两列从不参与。把它们绑在一起的只有一个约定——对同一个 `uuid`，两侧算出的字符串必须**逐字节相同**。

这个等式今天成立，且是精心构造出来的：

- `pwClassDefault` 存的就是**裸 UUID**，而 `DeriveProxyPassword` 对 VLESS / VMess / Trojan / SS / AnyTLS / TUIC / Naive 返回的也是裸 UUID。
- `pwClass256` 存 `crypto.NewProxyPassword(uuid)` = `base64(sha256(uuid))`（全部 32 字节），而 `DeriveProxyPassword(uuid, SS2022, "…aes-256-gcm")` 走的是同一个 `sha256` 后按 `ss2022KeyLen` 取 32 字节——同一个值。
- `pwClass128` 干脆**直接调用** `DeriveProxyPassword(uuid, ProtoSS2022, "2022-blake3-aes-128-gcm")`，等式在这一类上是显式的。

v3.9.0 的迁移之所以能对订阅者**完全无感**（没有任何人需要重新拉一次订阅），靠的正是这个等式：每个用户在 3X-UI 里的客户端从"每节点一个"合并成"每面板每凭据类一个"，但那份凭据的字节没有变过，所以已经在跑的代理连接和已经下发的配置继续有效。`clientplan.go` 的常量注释把这句话写成了设计目标（"every class is BYTE-IDENTICAL to what the legacy per-node `DeriveProxyPassword` emitted"）。

**两侧的派生函数本身守得很牢**：`crypto.NewProxyPassword` 与 SS-2022-256 分支的相等由 `TestNewProxyPassword`（`internal/pkg/crypto/derive_test.go`）直接断言，非 SS-2022 协议返回裸 UUID 由 `TestDeriveProxyPassword_NonSS2022` 钉住，每个凭据类存出来的具体密码值由 `internal/pkg/clientplan/clientplan_test.go` 逐类断言（含 PSK 字节长度）。真正没有测试覆盖的是**把两侧接起来的那两个环节**：render 侧的调用点是否还在，以及两侧喂给派生函数的**输入**（protocol 与 SS method）是否指向同一个东西。

**危险在于失败模式是静默的。** 等式一旦被打破，渲染出来的订阅在语法上完全正确、下发过程不报错、面板日志一片干净——用户拿到配置，连上去，3X-UI 拒绝认证。故障现象出现在"连接时"，而不是"渲染时"，离改坏它的那次提交可能隔着好几个版本。

**更危险的是代码里已经有一条误导性的注释。** `internal/domain/pspclient.go` 在 `UUID` / `Password` 字段上写着 "Credentials — the single source of truth (no `DeriveProxyPassword` at render time)"。这句话描述的是 v4 想要到达的**终局**，不是当前事实：render 侧的三个文件此刻仍在调 `DeriveProxyPassword`。任何据此注释做"清理冗余派生"的重构——比如把 render 改成读 `psp_clients`、或者反过来认为存储字段已经没人用而改动它的生成方式——都会**立刻打断全部订阅**。

## 决策

**`clientplan.passwordForClass` 的输出与 `crypto.DeriveProxyPassword` 的输出是同一个函数的两种写法，必须始终保持逐字节相等。** 任何一侧的改动都必须同时改另一侧，且必须视为一次**破坏性凭据轮换**（所有受影响用户需要重新拉订阅），而不是内部重构。

具体约束：

### 1. 两侧的等价关系按凭据类逐条成立

| 凭据类 | 存储侧（`passwordForClass`） | 渲染侧（`DeriveProxyPassword`） |
|---|---|---|
| `pwClassDefault` | 裸 `uuid` | Trojan / SS / VLESS / VMess / AnyTLS / TUIC / Naive → 裸 `uuid` |
| `pwClass256` | `NewProxyPassword(uuid)` = `base64(sha256(uuid))` | SS-2022 + 32 字节密码套件 → `base64(sha256(uuid)[:32])` |
| `pwClass128` | `DeriveProxyPassword(uuid, SS2022, "…aes-128-gcm")` | SS-2022 + `aes-128-gcm` → `base64(sha256(uuid)[:16])` |

VLESS / VMess 的 `id` 与 Hysteria2 的 `auth` 都直接是 UUID（`buildSharedClientSpec` 把 `ID` 和 `Auth` 都填成 `c.UUID`），所以这两类协议不经过密码字段，等式天然成立——但它们对 `u.UUID` 本身的依赖同样不能被改动。

### 2. SS-2022 的 PSK 长度必须继续按密码套件分类

`ss2022KeyLen` 存在的理由是 SIP022 硬性规定了 PSK 字节数：`aes-128-gcm` 要 16 字节，`aes-256-gcm` / `chacha20-poly1305` 要 32 字节。长度错了 Xray 直接以 `bad key length, required 16` 拒绝这个客户端。这也是 `pwClass128` 必须**单独成一类**（而不是和 `pwClass256` 合并）的全部原因：一个 16 字节的 PSK 和一个 32 字节的 PSK 塞不进 3X-UI 客户端上那**唯一一个** `password` 字段。

### 3. 渲染侧不读存储凭据是过渡期的既定事实，不是待修的缺陷

v3.9.0 的迁移是**逐用户、逐节点**推进的：`sharedclient.DeleteLegacyForUser` 只删除那些"已被确认 provisioned 的共享客户端覆盖到"的旧的每节点客户端，覆盖不到的节点**保留旧客户端**作为回退（见 ADR 0025）。也就是说，在整个过渡期里，同一个用户在不同节点上可能一边挂着共享客户端、一边挂着 legacy 客户端。渲染必须对这两种形态都产出**能用的**订阅——凭据侧它做到这一点的方式，正是两边都不看、只按 UUID 现算（两种形态下派生出的字节完全一样）。唯一与形态相关的字段是 Naive 的用户名（客户端 email）：`sharedClientEmailsByNode` 按节点显式解析，attachment 未 provisioned 时回退到 legacy 每节点 email，指向的正是那台节点上此刻真实存在的客户端。

### 4. 两侧必须对"这个节点是什么协议、什么密码套件"取得一致

派生函数相等还不够：只要两侧喂进去的 `protocol` / `ssMethod` 不同，算出来的字节就不同。render 侧的这两个输入来自**实时入站**（`crypto.DetectProtocol` 加上入站 settings 里的 method），计划侧来自**节点记录**。

这条不是假想。`user.ResyncMembership` 里有一行 `s.resolveShadowsocksMethods` 专门在建 `psp_client` 计划**之前**解析出实时的 Shadowsocks 密码套件，注释写明了原因：一个从未被 capture 过的 Shadowsocks 节点 `InboundSettings` 为空，计划构建器分不出 SS 与 SS-2022，会把它扔进裸 UUID 的凭据类——于是存储侧存了裸 UUID、渲染侧算出的却是 base64 PSK，订阅静默失效。这行代码就是本条不变量的现役守卫，删掉它等于把这个 bug 请回来。

## 后果

**正面**：迁移期的两种客户端形态在凭据上对订阅者完全不可见——共享客户端与 legacy 每节点客户端渲染出的密码逐字节相同，所以迁移不需要任何人重拉订阅。（**不要**把"省一次查库"算进收益：render 为了 Naive 的 email 本来就在 `sharedClientEmailsByNode` 里读了 `psp_clients` 及其 attachment，凭据即使改成读库也拿的是同一批已加载的行。）

**代价**：一个必须靠人守住的跨包不变量，没有编译器帮忙。

**运维**：等式被打破时不会有任何告警。症状是用户报"配置拉下来了但连不上"，而面板侧的同步、渲染、健康检查全部正常。

**必须保持的不变量**（未来修改时不能破坏）：

- `passwordForClass` 与 `DeriveProxyPassword` 对同一 `uuid` 的输出保持逐字节相等。改一侧必须改另一侧。
- `DeriveProxyPassword` 对非 SS-2022 协议返回裸 UUID 这一行为不能改——它同时是 `pwClassDefault` 存储值的定义。
- `ss2022KeyLen` 的分类不能合并；`SS2022KeyLen` 这个导出壳存在的唯一目的就是让 `clientplan` 识别 128 位这一特例。
- `ResyncMembership` 必须在建计划前调用 `resolveShadowsocksMethods`，否则未 capture 的 SS 节点会落错凭据类。
- **不要相信 `internal/domain/pspclient.go` 上 "no `DeriveProxyPassword` at render time" 那句注释**。它描述的是 v4 目标状态。在 render 真正改成读存储凭据之前，删掉 render 侧的派生就是删掉用户的订阅。

## 考虑过但未采用的方案

- **让 render 直接读 `psp_clients` 的存储凭据**（即上面那句注释描述的终局）：语义上更干净，也是 v4 的方向。但在 legacy 每节点客户端尚未清空之前不能这么做——`DeleteLegacyForUser` 会保留未被共享客户端覆盖的节点，render 必须同时为这两种形态出配置，而存储凭据只覆盖前者。等 legacy 路径整体拆除后再收口，且收口时等式必须仍然成立（否则收口本身就是一次凭据轮换）。
- **给共享客户端随机生成密码**：会让 v3.9.0 的迁移对每一个订阅者可见（全员必须重拉），并且迁移无法离线完成——`NewProxyPassword` 之所以是**确定性**的，正是为了让迁移能仅凭已有的 UUID 算出密码，不需要先把每个面板的客户端读回来。
- **给 SS-2022-128 也用 32 字节 PSK，从而只留一个密码类**：Xray 会以 `bad key length, required 16` 拒绝，这条路根本走不通。
- **为端到端的等式加一个断言测试**：没有被否决，只是还没写——这是本 ADR 留下的最直接的后续动作。两侧的派生函数各自已有测试，缺的是把它们接起来的那一个：给定一组 `NodeCred`，用 `clientplan` 算出存储密码，再用 render 侧同样的 `(protocol, ssMethod)` 调 `DeriveProxyPassword`，断言两者相等。有了它，本 ADR 的核心不变量就从"文档约定"变成"CI 门禁"。
