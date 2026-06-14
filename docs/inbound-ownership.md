# inbound 配置本地化与订阅渲染零回源（自 v3.5.0-beta.1 起实现）

> 状态：**已实现（首个切片，v3.5.0-beta.1；客户端清零等安全修补在 v3.5.0-beta.2）**。后端写路径 / render / reconcile 轴 A 均已落地并有单测覆盖。
> 关联：[ARCHITECTURE.md](ARCHITECTURE.md) §3.2 / §4 / §9 / §16；[internal/migrate/README.md](../internal/migrate/README.md)。
> 实现位置：映射逻辑统一在 [internal/service/inboundcfg](../internal/service/inboundcfg/)（node / render / reconcile 共用）。
> 历史：原计划走 v4.0.0 major 切版，最终决定非破坏性、增量发布在 v3.5.x（升级无需迁移工具）。

## 0. 一句话

把 PSP 从「3X-UI 是 inbound 配置单源真相、订阅渲染时实时回源」改为「**PSP 自己的 DB 是它所创建 inbound 的配置真相源**、订阅渲染**只读本地、零回源**、reconcile 反向下发覆盖漂移」。**客户端级（client/email）的纳管边界与安全护栏完全不变。**

---

## 1. 背景与动机

### 1.1 问题

当前每次用户拉订阅，render 都在**请求热路径**上实时调 3X-UI 的 `ListInbounds`（[render.go `prefetchInboundsForRender`](../internal/service/render/render.go)）。原因是面板 `nodes` 表**只存展示元数据**（display_name / region / tags / 缓存的 protocol+port），不存连接配置（端口、stream settings、TLS/Reality、传输层），而生成 proxy 块必须要这些——只能回源拉。

这不合理：

- 订阅会被客户端高频轮询，热路径上打 3X-UI 把压力直接传导到上游；
- 3X-UI 临时不可达 → 订阅直接渲染失败；
- 而且这次回源是**重复劳动**——见下。

### 1.2 关键发现：回源是重复的

后台已有三个周期 worker 在**周期性拉同一份 inbound 列表**：

| Worker | 频率 | 已在 `ListInbounds` |
|---|---|---|
| traffic poll | 5 min | ✅ 每 panel 一次 |
| health check | 5 min | ✅ 每 panel 一次 |
| reconcile | 15 min | ✅ 每 panel 一次 |

health worker 甚至**已经在做「从 inbound 抽字段写回 nodes 表」**（持久化 `Port`/`Protocol`，[health.go](../internal/service/health/health.go)），只是没把渲染需要的连接配置一起存下来。所以把配置本地化几乎是"白捡"——复用已有 poll 的结果即可，对 3X-UI **零新增请求**。

### 1.3 横向参考：V2board / XrayR / V2bX

V2board 这类机场面板**面板自己拥有节点配置**：节点连接参数存在面板 DB，边缘节点跑哑 agent（XrayR / V2bX）**反向轮询面板**拉配置 + 用户列表、并上报流量。订阅渲染是**纯 DB 读、零上游调用**。本设计借鉴其「配置本地化、render 零回源」的解耦思路，但**保留 PSP 的定位前提**：3X-UI 仍是实际跑 xray 的地方，PSP 通过其 API 下发，而不是引入新 agent。

---

## 2. 核心模型（定稿）

### 2.1 两条独立的轴

inbound 的状态分两层，归属与方向**不同**：

| 轴 | 内容 | 真相源 | reconcile 方向 | render 读哪 |
|---|---|---|---|---|
| **轴 A — inbound 连接配置** | 端口 / listen / 协议 / stream / TLS / Reality / sniffing / allocate | **PSP DB**（仅限 PSP 创建/接管的 inbound） | **下发覆盖**（持续强制） | 本地 DB |
| **轴 B — client / email** | 具体客户端条目（uuid / enable / expire / password） | 沿用现状（user 状态为准 + 归属表） | 校正自己的 email（**不变**） | 不需要（凭证从 `user.uuid` 推导） |

### 2.2 节点归属：只有「托管」与「无关」两态

- **PSP 节点 ⟺ 托管 inbound**：一一对应。凡是 PSP 要渲染进订阅的 inbound，PSP 就托管它（存配置 + 负责下发）。**不存在"只引用不接管"的中间态，不做镜像。**
  - `CreateInbound`：配置存 DB → 下发 3X-UI。
  - `ImportExisting`：**改为接管**——把 3X-UI 现有 inbound 配置吸进 DB 一次，此后 PSP 是该 inbound 配置的真相源。
- **PSP 不管理的 inbound**（同台 3X-UI 上别人的、PSP 从没创建/接管的）：**完全不碰**——不存、不镜像、不渲染、不 reconcile。

### 2.3 client 级混合与"绝不误伤"——不变量

§4.1 / §9.5 的现实依然成立：**同一个 PSP 托管的 inbound 里，既有 PSP 发的 email，也会有手动在 3X-UI 里建的 client**（维护者私人 / 老朋友）。

- PSP **只维护自己发的 email**（轴 B），手动创建的 client **绝不删、绝不改**。
- ⚠️ **轴 A 的"下发覆盖配置"必须走 read-modify-write**：只覆盖连接配置部分，`settings.clients[]` 用 3X-UI 当前活着的列表合并保留——**PSP 的 email 和手动建的 client 全部不丢**。这正是现有 [`settingsWithCurrentClients`](../internal/adapters/xui/client.go) 的语义，延续使用。

---

## 3. 与现有架构文档的关系（supersedes / 保留）

本设计触及多条核心架构约定，以下条目按表更新。**实现时同步改 ARCHITECTURE.md。**

### 3.1 被推翻 / 修改的条目

| 位置 | 原约定 | v3.5 改为 |
|---|---|---|
| §3.2 表「修改 inbound 协议参数」 | 本地只存展示元数据，协议参数以 3X-UI 为真相源 | PSP 托管 inbound 的连接配置存本地 DB，PSP 为真相源 |
| §9.3 节点元数据存储表 | 协议/地址/端口/TLS/Reality 存 3X-UI | 上述参数对**托管 inbound** 存 `nodes` 表 |
| §9.4.3 #7「inbound 启用状态」 | 不修复，只记录（3X-UI 是协议参数真相源） | 托管 inbound 的配置与启用状态由 PSP 持续强制 |
| §9.4.5 🚫「修改 inbound 协议参数」 | 绝对不做 | 对托管 inbound：reconcile **会**下发覆盖配置漂移（仅连接配置层，RMW 保留 clients） |
| §9.5.1「inbound 协议参数零变更」 | 导入完全不调 3X-UI 写 API | 导入 = 接管：吸配置进 DB；此后配置以 PSP 为准 |

### 3.2 完全保留的不变量（v3.5 不动）

- §4.3 **client 写护栏** `ensureClientOwned`：所有写 client 入口必须命中归属表。
- §4.4 **inbound 删除护栏** `ensureInboundDeletable`：删 inbound 必须内部全部 client 纳管。
- §9.4.5 🚫 **绝不删除任何 3X-UI client**（含归属表外私人/朋友）。
- §9.5.2 **addClient 追加语义**、§9.5.3 **归属表是最后防线**。
- 轴 B 的全部 client 检查项（§9.4.2 #1–#5）。

> 一句话：**安全模型只在 inbound 配置层做了"PSP 当家"的改动；client 层的"绝不误伤"丝毫未动。**

### 3.3 一个需要明确的张力

§9.5 的复用哲学是"复用现有 inbound 而不动它"。v3.5 的"导入=接管"会让**被接管的既有 inbound 的连接配置改由 PSP 持续强制**。结论与约束：

- 接管**只影响连接配置层**（端口/TLS/stream），**不影响任何 client**（私人/朋友 client 全程保留）。
- 接管后，该 inbound 的连接配置应**经 PSP UI 修改**；若维护者绕过 PSP 直接在 3X-UI 改，reconcile 会按 PSP 版本改回（这是"持续强制"的有意行为，用户已确认接受）。
- §9.5.5 老朋友渐进迁移路径**不受影响**（那是 client 级认领，走轴 B）。

---

## 4. 数据模型变更

### 4.1 `nodeRow` 新增列（[schema.go](../internal/adapters/sqlstore/schema.go)）

GORM AutoMigrate 自动加列，符合"自用项目无迁移脚手架"约定（[CLAUDE.md](../CLAUDE.md)）。

**全保真**：存下完整 inbound（对齐 `ports.InboundSpec` 可存字段），使 PSP 能独立重建 inbound、不依赖 live 3X-UI 保留任何字段。

| 列 | 类型 | 对应 InboundSpec |
|---|---|---|
| `InboundListen` | `string size:64` | `Listen`（服务端监听地址，≠ 客户端拨号的 `server_address`） |
| `InboundRemark` | `string size:255` | `Remark`（3X-UI inbound 备注，与 PSP `DisplayName` 解耦） |
| `InboundSettings` | `text` | `Settings`，**去掉 `clients[]`**（下发时由归属表物化 + 合并活客户端） |
| `StreamSettings` | `text` | `StreamSettings`（传输层 / TLS / Reality） |
| `Sniffing` | `text` | `Sniffing` |
| `Allocate` | `text` | `Allocate` |
| `InboundExpiryTime` | `int64` | `ExpiryTime`（inbound 级到期，一般 0；≠ 用户 client 级到期，后者属轴 B） |
| `ConfigSyncedAt` | `*time.Time` | — 最近一次成功下发/对齐时间，nil = 未捕获（render 回源兜底的判据） |
| `ConfigSyncState` | `string size:32` | — `synced` / `drift` / `pending`，供 UI 显示 |

> `Port` / `Protocol` / `Enabled` 已存在，分别对应 InboundSpec 的 `Port` / `Protocol` / `Enable`，现在升级为**权威字段**而非缓存。
> render 实际只用 `Port` + `Protocol` + `Settings` + `StreamSettings`；其余字段为 push（轴 A）完整重建 inbound 而存。
> 全保真后，轴 A 下发**只有 `clients[]` 需 RMW 合并**（client 混合、单独管理），listen/remark/expiry 等 PSP 自有，无需从 live 读。
> 未被表单结构化建模的字段：靠前端已有的 `raw_settings` / `raw_stream_settings` / `raw_sniffing` round-trip 原样保留（[NodesView.tsx](../web-react/src/views/admin/NodesView.tsx)），存进上述 text 列即可全量保真。

### 4.2 为什么 `clients[]` 不入存档

clients 始终由 ownership 表（`user_xui_clients`）+ sync 管理；inbound 的 client 列表是混合的（PSP 的 + 手动的）。存档只存"配置模板（去 clients）"，下发时用 3X-UI 活客户端合并，既避免存档里的 clients 走样，也天然满足"保留手动 client"。render 也根本不需要 clients[]。

### 4.3 schema 充分性论证（逐协议核验）

render 生成 proxy 块（[protocols.go `emitProxy`](../internal/service/render/protocols.go) + singbox.go + urilist.go 三种输出格式来源一致）**只从这几处读**：`inb.Settings`、`inb.StreamSettings`（两块 raw JSON）、`inb.Port`、`inb.Protocol`，外加 `node.ServerAddress` / `node.Flow`（已是 nodes 表列）、`user.UUID`（user 表，轴 B）。因此"存 raw Settings(去 clients) + StreamSettings + Port + Protocol + Listen"**按构造即完整**。

| 协议 | inbound 侧需要的字段 | 全在存档 JSON 内 |
|---|---|---|
| VLESS | network/security/Reality/TLS/transport（StreamSettings）；flow 在 nodes 表 | ✅ |
| VMess | network/TLS/transport（StreamSettings） | ✅ |
| Trojan | TLS/transport（StreamSettings）；password 由 UUID 派生 | ✅ |
| SS (SIP002) | `settings.method`；password 由 UUID 派生 | ✅ |
| SS-2022 | `settings.method` + server PSK `settings.password`；user PSK 由 UUID+method 派生 | ✅ |
| Hysteria2 | obfs（`streamSettings.finalmask.udp`）/TLS；password = UUID | ✅ |

**两个易丢点，均已确认无碍：**

1. **去 `clients[]` 不丢协议级配置**：SS/SS-2022 的 `method`、SS-2022 server PSK(`settings.password`)、VLESS/VMess 的 `decryption`/`fallbacks` 都是 `clients` 的同级字段，剥 clients[] 后保留。
2. **Reality publicKey（老版 3X-UI 只存 privateKey）**：render 已有"publicKey 为空则用 privateKey 现场 X25519 派生"逻辑（[protocols.go:104-109](../internal/service/render/protocols.go#L104-L109)）；privateKey 在 StreamSettings 内、随存档保留，故改读 DB 后该逻辑**零改动照常工作**，无需入库规范化。

> 存 raw JSON 是 3X-UI 配置的**超集**：render 当前对部分传输层（xhttp/httpupgrade，`applyTransportOpts` 仅处理 ws/grpc）支持不全，将来补齐也不需改 schema、不需回源。

---

## 5. 实现阶段（checklist）

### 阶段 1 · Schema ✅
- [x] `nodeRow` 加上述列（§4.1）+ to/from 映射（[schema.go](../internal/adapters/sqlstore/schema.go)）。
- [x] domain `Node` 加对应字段（[types.go](../internal/domain/types.go)）。

### 阶段 2 · 写路径 write-through（[node.go](../internal/service/node/node.go)）✅
- [x] `CreateInbound`：`inboundcfg.ApplySpec` 存配置进 node 行，再 `AddInbound`；失败仍入 `node_create` 任务（任务处理器同样 ApplySpec）。
- [x] `UpdateInboundConfig`：local-first——先 `ApplySpec` + `nodes.Update`，再 `UpdateInbound`；失败入 `node_update`。
- [x] `ImportExisting`：`GetInbound` → `inboundcfg.Capture` 存进 node 行（接管）。
- [x] **存量回填**：移到 reconcile 轴 A（见阶段 4），不放 health——回填本质是"无快照则捕获"，与轴 A 同源，health 保持纯健康职责。

### 阶段 3 · render 改读 DB（[render.go](../internal/service/render/render.go) / [config_source.go](../internal/service/render/config_source.go)）✅
- [x] `buildProxies` 改为 `inboundcfg.InboundFromNode` 读本地；仅 `ConfigSyncedAt==nil` 的节点回落到 `prefetchInboundsForRender`（live）。
- [x] 协议块 builder 不变（`emitProxy` 本就只吃 `Settings`/`StreamSettings`/`Port`/`Protocol`，喂本地重建的 `ports.Inbound` 即可）。
- [x] 验证：单测 `TestBuildProxies_LocalConfig_ZeroFetch`（pool 被调用即 panic）证明零回源；`..._FallsBackToFetch` 证明过渡期兜底。

### 阶段 4 · reconcile 轴 A（[reconcile.go](../internal/service/reconcile/reconcile.go) `checkNodes`/`reconcileInboundConfig`）✅
- [x] **无快照（含存量回填）**：`inboundcfg.Capture` 从 live 拉进 node（pull），不下发。
- [x] **有快照且漂移**：`InSync` 比对（语义 JSON、忽略 clients[]/键序）→ `UpdateInbound` 下发 `SpecFromNode`，**RMW 保留全部 client**；推后 `GetInbound` 回采收敛。
- [x] **轴 B（旧）**：client 检查项 #1–#5 完全不动。
- [x] ConfigSyncState `"pending"` 状态（beta.7）：reconcile 推送 / 回采失败时落盘 `"pending"`，下一轮成功时由 `Capture` 复位为 `"synced"`；每条 `inbound_config_*` 事件单独写 `audit_log`，actor=`reconcile`、target 含 `node/panel/inbound` id。

### 阶段 5 · 顺带优化
- [x] health 改读本地 Port/Protocol（beta.7）：不再 `ListInbounds`，控制面 / 数据面解耦（3X-UI 控制 API 挂掉时 health 仍能跑）。`panel_unreachable` / `inbound_missing` 两个旧状态在 health 不再写入（前者已无意义，后者由 reconcile §9.4.3 #6 兜底）；`health.Service.pool` 字段一并去除。
- traffic poll 仍拉流量计数（流量属 xray，搬不走）。

### 阶段 6 · 文档与版本
- [x] CHANGELOG（中文，v3.5.0-beta.1）。
- [x] ARCHITECTURE.md 正文回写：§3.2 / §9.3 / §9.4.3（#7 改写 + 新增 #8 轴 A 配置漂移）/ §9.4.5 / §9.5.1 已改为 v3.5 现实（PSP 为 inbound 配置真相源），并标注撤销旧表述。
- [ ] *TODO*：`internal/migrate/` 改写为 v3.x→v4.0.0 的迁移逻辑等到真正切下个 major 时再做（本特性非破坏性、增量发布）。
- [x] 编辑对话框 `GetInboundConfig` 改读本地快照（beta.6）：已捕获节点读本地、与 render/reconcile 一致，未捕获才回源；"是否有本地配置"统一为 `inboundcfg.HasLocalConfig`。
- [ ] *TODO*：前端可选——节点列表展示 `ConfigSyncState`。

---

## 6. 风险与权衡

| 风险 | 说明 | 缓解 |
|---|---|---|
| 配置走样导致订阅错 | render 读本地，若本地与 3X-UI 实际不一致则发出错误 proxy | 轴 A 持续强制对齐；ConfigSyncState=drift 时 UI 告警 |
| 接管既有 inbound 改变维护者习惯 | 接管后不能再从 3X-UI 原生 UI 改该 inbound 配置（会被改回） | 文档明确；UI 提示"该节点由 PSP 托管，请在此修改"；client 层不受影响 |
| 存量回填窗口 | 升级后到首次 poll 之间，老节点无本地配置 | 回填前 render 对该节点临时回源一次（仅过渡期 fallback，回填后消失） |
| 前端工作量 | — | **极小**：表单已暴露全套字段并构建完整 JSON，仅 API 数据源从 live 3X-UI 切到 PSP DB |

---

## 7. 决策记录（本设计形成过程）

1. render 热路径回源 3X-UI 不合理 → 要本地化。
2. 评估三方案（持久化镜像 / 内存短 TTL / 完整 V2board 化）→ 选**完整 V2board 化**：PSP DB 为 inbound 配置真相源。
3. 冲突策略：PSP 覆盖，但**不碰非 PSP 管理的** inbound。
4. 去掉"镜像"中间态：**导入 = 接管**，render 零回源无例外。
5. 维护粒度澄清：PSP 维护**自己做的 inbound + 自己的 email**；同一 inbound 的手动 client 共存且保留。
6. 下发力度：inbound 配置层**持续强制覆盖漂移**（非仅改时下发）。
