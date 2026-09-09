# PSP 自研节点后端（agent）设计

- **状态**：**协议形状已定稿（§8，2026-09-09）**——六个洞全部关闭，端点、版本号粒度、ETag 语义、引用完整性、部分失败、`EnforcementDirective` 语义都已写死。**下一步是建仓库、把 §8 变成类型定义与线上格式**；§8.6 列的五条前置作业是 PSP 侧的，不阻塞协议落纸。
- **决策依据**：[ADR 0024](adr/0024-psp-native-node-backend.md)（2026-09-08 所有者已决定自研）、[ADR 0025](adr/0025-push-pull-decision-rule.md)（推/拉判据）、[data-plane-plan.md](data-plane-plan.md) Phase 2/3
- **相关代码**：`internal/ports/xui.go`、`internal/adapters/panel`、`docs/panel-adapters.md`、`docs/inbound-ownership.md`

## 这份文档写到哪

**协议形状已经定稿。** 走到这里用了四步，每一步都是前一步的前提：

| 步 | 产物 | 日期 |
|---|---|---|
| 1. 上游调研 | §7 —— 3X-UI 没有可复用的节点协议（它的「节点」就是另一台 3X-UI 面板）；机场生态那套有**两样值得抄、两样必须避开**，而 XrayR 已经没有维护者 | 09-08 |
| 2. 验收映射表 | §2 —— 21 个 port 方法逐个对抗验收，**13 个塌成 1 个操作**；顺带删掉三样死代码 | 09-09 |
| 3. 六个洞 | §2.3 —— 映射表暴露的、协议定稿前必须回答的问题 | 09-09 |
| 4. 协议形状 | §8 —— **六个洞全部关闭** | 09-09 |

**方法是固定的**：每个决策先让三个独立方案各自论证，再让怀疑者逐条推翻，最后由裁判独立核实引用后定夺。这不是形式——三轮里有两轮的**全部方案都被判 fatal**，而定稿采用的是从残骸里救出来的部分。其中一轮推翻了本文档自己在同一天早些时候写下的一条处方（§2.4 ① 的订正）。

仍然留空的见 §9，都是交付与运维形态，不影响协议。

## 0. 第一原则：我们的模型是主，3X-UI 和 S-UI 是被适配的对象

**协议按 PSP 自己的领域模型设计。3X-UI 与 S-UI 从此只做兼容，不再是我们对齐的目标。**

这条推翻了 [ADR 0024](adr/0024-psp-native-node-backend.md) §2「agent 的 API 直接照 `ports.PanelClient` 的形状设计」。那句话有个没说出口的前提：`ports.PanelClient` 是中立的。**它不是。**

### 实证：port 已经被上游塑形了

`BulkSetEnabled` 和 `BulkDetach` 这两个方法：

- 在 `ports.PanelClient` 里声明
- 在 `adapters/xui` 和 `adapters/sui` 里各实现一遍
- 被 `traffic_test.go` 的测试替身实现（因为接口逼它）
- **在生产代码里被调用零次**

它们在这个接口里的唯一原因是 **3X-UI 提供了这两个端点**。这就是「端口被厂商塑形」的教科书样本——六边形架构里，端口应当表达**领域的需要**，而不是某个供应商的 API 表面。现在它表达的是后者，这也正是 `sui` 适配器一直别扭的原因：S-UI 不是 3X-UI，却要挤进一个 3X-UI 形状的洞。

### 关键区分：哪一层不可改，哪一层随时能改

| | 生命周期 | 改动成本 |
|---|---|---|
| **agent 的线上协议** | 部署到现场的节点上，要活很多年 | **高**——改一次要动所有节点 |
| **`ports.PanelClient`** | 纯内部接口 | **低**——重塑它不需要碰任何一台已部署的节点 |

**所以不可被 3X-UI 污染的是前者。** 后者是内部实现细节，什么时候重塑都行。

### 由此得出的三步走

1. **agent 协议 = 我们的模型，从第一天起，不妥协。** 从 `domain.PSPClient` / `domain.Node` / PSP 已经拥有的 inbound 配置出发去设计，**不是**从 `ports.PanelClient` 的方法表出发。
2. **`psp` 适配器吸收阻抗。** 我们自己的协议和今天这个 port 之间的落差，压在**我们自己写的**那个适配器里——放在看得见、也拆得掉的地方，而不是塞进要活很多年的线上协议。
3. **之后再增量重塑 port**，让 `xui` / `sui` 去迁就它。那时才真正到达「只兼容，不再主动向着他们」的终局。

**代价记明**：ADR 0024「其它什么都不用改」这个卖点被主动放弃了一部分——它当初是决策依据之一，所以不含糊过去。已实测的爆炸半径：port 被 8 个 service 包消费，但**每个方法只有 1~5 个非适配器调用点**，两个方法是 0。这是个有界的重构，不是五百处调用点的噩梦。

### 能力声明的极性也跟着翻转

今天 `Capabilities()` 回答的是「这个面板相对于一个 3X-UI 式的基线，能做什么」。模型换主之后，**基线是我们的模型**，于是 3X-UI 和 S-UI 变成**有缺口的那一方**——`reportCapabilityGaps` 报的将是「上游做不到我们要求的什么」，而不是「我们的后端还差上游多少」。

纪律不放松：agent 早期版本大可以不实现 REALITY 扫描或证书管理，那就不声明。**能力回答的是「这个部署能不能做」，不是「这个项目打算不打算做」**——这条不因为后端是自己的就松口。

## 0.5 独立仓库（已定），以及它带来的那个陷阱

**agent 单独开一个仓库**，目标是别人也能用。

这个决定是对的，理由比「代码分开放」强：真正有价值的不是二进制，是**协议变成一份公开契约**。XrayR / V2bX 之所以被广泛采用，正是因为它们实现的是一套协议（V2board / SSPanel 那一套），于是很多面板都能驱动它们。我们要复制的是这个形状，而不只是「把文件夹搬走」。

顺带的好处都是实的：发布节奏可以和 PSP 脱钩（agent 发到现场的节点，PSP 只发到一台主机，升级风险画像完全不同）；依赖面收窄（agent 不该拖进 GORM、Web 栈、模板引擎）；外部贡献者可以只碰 agent 不碰面板。

### ⚠️ 陷阱：这会在我们自己的两个仓库之间，重建我们正在逃离的那个问题

今天 PSP 改 `ports.PanelClient`,**编译器会把每一个调用点指出来**。仓库一拆，协议就变成一份**没有编译器把关的线上契约**——

> 而「上游改一个 API、影响范围铺开、没人第一时间知道」，正是这次决定自研的**全部理由**。

拆仓库如果不处理这件事，等于把 3X-UI 的问题**在我们自己内部原样复刻一遍**，而且这次没有别人可以怪。

### 三条必须同时成立的工程约束

1. **协议类型定义住在 agent 仓库里，PSP 用 Go module 引它。** 一份真相源，两侧都被编译器检查类型。不复制粘贴，也不为此再开第三个仓库。
2. **协议要有显式版本和兼容策略**——兼容底线、能力声明、版本探测。**PSP 已经有这整套机制**（`internal/version/compat.go`、`compat_sui.go`、能力声明、面板版本探测），只不过它现在是用来对付上游的。**拆仓之后我们站到了对面那一侧**，同一套纪律要反过来施加到自己身上。
3. **`psp` 适配器的测试必须能对着一个真 agent 跑。** 契约测试不能只测我们这边的想象——这个仓库里已经有 `client_live_test.go` / `client_live_surface_test.go` 这类活体测试的先例，照做。

### 命名与时机

- **名字不该叫 `psp-agent`。** 目标是别人也能用，那它就该按**它是什么**命名（一个由外部控制面驱动的 xray / sing-box 节点后端），而不是按谁在用它命名。
- **建议等协议定稿再建仓库。** 一旦 PSP `import` 了它的 module path，改名和改路径的成本就上来了。现在建一个空仓库买不到任何东西，却要提前锁死名字。

  > **2026-09-09 已定：仓库 `Passwall-Node`，module path `github.com/KazuhaHub/passwall-node`**（所有者拍板）。大小写沿用本仓库的惯例：仓库名首字母大写加连字符，module 路径全小写——与 `Passwall-Sub-Panel` / `github.com/KazuhaHub/passwall-sub-panel` 一致。
  >
  > **取舍写下来，免得日后有人翻出上面那条判据来质疑这个名字。** 上面写的判据是「按它是什么命名，不按谁在用它命名」,而 `Passwall-Node` 带了品牌前缀，**这是明知代价后的选择**：
  >
  > - **付出的**：外面的人看到名字会默认「这是 Passwall 专用的」,对「别人也能用」这个目标有一点劝退。
  > - **换来的**：与 `Passwall-Sub-Panel` 成一个可辨认的系列；`Node` 是这个生态里的既有词汇（`Marzban-node` 的先例摆在那里），看到名字就知道是什么关系。
  > - **代价为什么不大**：`3x-ui`、`s-ui`、`Marzban-node` 全是品牌名，照样有人用。真正决定别人用不用的是**协议有没有公开文档、稳不稳定**——那是本节三条工程约束的事，跟名字关系不大。
  >
  > 也就是说：判据没有被推翻，是被权衡后让位了。**能让它继续成立的办法不是改名，是把那三条约束真的做到。**
- **代价说在前面**：「别人也能用」不是免费的——它意味着协议要**对外文档化并保持稳定**，破坏性变更要走废弃周期。这是一项长期成本，值得当作选择来接受，而不是事后才发现。

  > **2026-09-09 已建仓并落地**：<https://github.com/KazuhaHub/Passwall-Node>，§8 的线上契约以 `protocol/` 包的形式住在那里。PSP 里那份可编译草稿（`docs/agent-protocol/`）同时删除——留着就是第二份真相源，正是约束 1 明文禁止的那件事，而且是这次自研要摆脱的那个问题在自己内部的复刻。
  >
  > **PSP 的 `go.mod` 依赖不在这一步加，要等第一个 import。** Go 的 `require` 没有消费者就会被 `go mod tidy` 删掉，所以「加依赖」在技术上不可能早于 §10 第 4 步的 `psp` 适配器——搬家清单里把它排在删除之前是写错了顺序，不是漏做了一步。
  >
  > 已实测 module path 能解析：默认 `GOPROXY`（proxy.golang.org + sum.golang.org，校验和已入 `go.sum`）和 `GOPROXY=direct` 两条路都拉得到 `github.com/KazuhaHub/passwall-node@main`。这一条值得写下来，因为 GitHub 对 `?go-get=1` 回的 `go-import` 标签用的是仓库的原始大小写（`github.com/KazuhaHub/Passwall-Node`），与 module 路径不一致——看起来像个坑，实测两条路都不受影响。

## 1. 边界：agent 要做什么，不做什么

PSP 的上游访问走的是一层与厂商无关的适配器（`xui_panels.kind` → `adapters/panel.Registry` → 构造函数，`Pool` 按面板 ID 路由）。所以自研后端**是新增一个 `PanelKind`,不是新架构**——节点管理、客户端下发、流量轮询、订阅渲染、异地并发检测全部不动。

**PSP 已经拥有的（agent 不要重做）：**

| | 依据 |
|---|---|
| inbound 配置的真相源（自有 DB 存完整配置、订阅渲染零回源、reconcile 反向下发） | [inbound-ownership.md](inbound-ownership.md) |
| 客户端模型（`psp_clients` + `psp_client_inbounds`，分区键见下方注） | `internal/domain/pspclient.go`、`internal/pkg/clientplan/clientplan.go` |
| 订阅渲染、模板、规则集 | `internal/service/render` |
| 用户体系、分组、SSO、配额与到期的判定 | `internal/service/user` |
| 期望态与重试（sync task 队列） | `internal/service/user` runUserTask |

**agent 的职责因此很窄**：接收 PSP 已经拥有的配置 → 生成 core 配置 → 管进程 → 上报计数器。**UI、用户体系、订阅、Telegram bot 一律不需要。**

> **注：分区键不是 `(user, panel, credClass)`。** 这份文档早先这么写过，协议设计一度以它为前提，是错的。真正的分区键是 `clientplan.go:128` 的 `partKey{pwClass, flow}` —— **二维**，而落进 `domain.PSPClient` 的只有一维（`clientplan.go:306`：`CredClass: k.pwClass`）。所以同一面板上 VLESS+vision 与 VLESS+空 flow 是两个分区、两个客户端，`CredClass` 却相同：**`(user, panel, credClass)` 不单射，它是一个存下来的属性，不是判别式。**
>
> 存储层的唯一键是 `psp_client_repo.go:22-23` 的 `uk_psp_client(panel_id, email)`。而 `Email` 是**渲染产物、不是身份**：`clientplan.go:314-324` 在「这个面板只需要一个客户端」时会去掉分区后缀，所以分区总数跨越 1↔2 会让 email **re-key**（这段注释自己写明了）。协议主键因此既不能用 `credClass`，也不能用 `email`——见 §2.3 ①。

## 2. agent 必须实现的接口（已定，来自 PSP 代码）

`ports.PanelClient` —— **19 个必需方法**，任何 `PanelKind` 都要实现（映射表做完后从 21 降到 19，见 §2.5）：

**inbound（7）**
`ListInbounds` · `ListInboundsSlim` · `GetInbound` · `AddInbound` · `UpdateInbound` · `DelInbound` · `SetInboundEnable`

**client 单体（5）**
`AddClient` · `UpdateClient` · `DelClientByEmail` · `GetClient` · `ListClientInbounds`

**client 批量与挂载（6）**
`AddClientToInbounds` · `AttachClient` · `DetachClient` · `BulkAttach` · `BulkCreateClients` · `BulkDelByEmail`

**状态（1）**
`GetServerStatus`

**可选能力接口（7 个接口 / 9 个方法，按需实现，`CapabilityProvider` 自报）：**

| 接口 | 方法 | agent 该不该实现 |
|---|---|---|
| `CapabilityProvider` | `Capabilities()` | **必须**——PSP 靠它判断能力差异 |
| `LiveIPReader` | `ListLiveClientIPs()` | **必须**——异地并发检测依赖它 |
| `Fail2banReader` | `GetFail2banStatus()` | **不实现**，见 §3 |
| `CoreUpdater` | `GetCoreVersionList()` / `InstallCore()` | 应该——自研后 core 版本归我们管 |
| `PanelUpdater` | `GetPanelUpdateInfo()` / `UpdatePanel()` | 应该——agent 自升级 |
| `WebCertProvider` | `GetWebCertFiles()` | 视证书方案而定 |
| `RealityScanner` | `ScanRealityTargets()` | 应该——必须由节点自己测（它的路由/DNS/延迟视角才算数） |

### ⚠️ 上表是「今天的 port 长什么样」，不是「agent 协议该长什么样」

按 §0，这两件事已经分开了。这些方法里有相当一部分**是被 3X-UI 的模型逼出来的**：客户端按 email 全面板唯一、inbound 挂载是一张 junction、整结构 Save 语义、批量接口是为了少触发 xray reload——**其中两个（`BulkSetEnabled`、`BulkDetach`）连生产调用点都没有，已在 §2.5 删除。**

所以上表的用途是**给 `psp` 适配器当验收清单**（它必须让这些方法都能工作，PSP 的 service 层才不用动），**不是给 agent 协议当规格**。

agent 协议要回答的是另一组问题，从我们自己的领域出发：

- 一个**节点**要接收什么才能提供服务？（PSP 已拥有的 inbound 配置 + 该节点上的客户端集合）
- 一个**客户端**在我们的模型里是什么？（`domain.PSPClient`：按 `partKey{pwClass, flow}` 分区——**不是 credClass，见 §1 的注**——凭据由 UUID 派生，挂在若干 inbound 上）
- 节点要回报什么？（累计计数、在线 IP、**已应用的配置版本**、进程健康）

这三个问题的答案和 3X-UI 的方法表没有对应关系，也不应该有。

### 2.1 验收映射表（2026-09-09 完成）

先定名字。四组独立推导时给同一个操作起了三四个名字（「名册」被叫成 `client.list` / `NodeStateReport` / `roster`，字段集互不兼容），所以协议操作先收敛成这一套，表里只用这些名字：

| 操作 | 方向 | 语义 |
|---|---|---|
| `ConfigApply` | PSP → agent | 提交一份**带版本号**的期望监听器配置文档 |
| `ClientSetApply` | PSP → agent | 提交一份**带版本号**的期望客户端名册（声明式、全量或 keyed upsert） |
| `NodeReport` | agent → PSP | **唯一的观测流**：名册现状 + 每客户端累计计数 + 每监听器累计计数 + 在线源 IP + 已应用版本 + core 运行状态 |
| `ListenerEnumerate` | 应答式 | 枚举这台 agent 上**全部**监听器 key（孤儿回收） |
| `EnforcementDirective` | PSP → agent | **面板聚合后**的每用户上限（设备数、配额预算 + epoch），节点照它执行（§7.2） |
| `Issue` | agent → PSP | 稳定 issue code —— CONTESTED 类字段的出口 |
| `RealityProbe` | PSP → agent | 由节点的路由/DNS/延迟视角实测 |
| `CoreVersions` / `CoreInstall` / `AgentUpgrade` | PSP → agent | core 与 agent 自身的版本管理 |
| `TLSMaterial` | agent → PSP | 证书文件路径 |

**结论标记**：A = agent 提供更小的正交操作、`psp` 适配器本地合成；B = 协议不提供、PSP 用自有状态作答；C = port 让步（删除或改签名）；D = 一比一保留。**⚠ = 这一行在对抗验收中被真实生产调用点推翻过，处方是修正后的版本，括号里是它欠的债。**

**inbound（7）**

| 方法 | 结论 | 兑现方式 |
|---|---|---|
| `ListInbounds` | A ⚠ | `NodeReport` 的名册。**⚠ 名册必须携带 reconcile 真正比较的字段**（uuid/password/flow/expiry/enable），否则 `reconcile.go:623-754` 的逐字段比对两条路都是死的：填指纹 → 每轮对每个客户端触发一次 `RotateClientUUID`（每客户端一次写，破 §4）；填 PSP 自己的期望值 → 所有比较按构造成立、`found` 永不为 nil，`reconcile.go:639` 的 `missing_client_recovered`（断网自愈）永远不触发。这是 §5 硬约束 1 的**第二个**同义反复现场。 |
| `ListInboundsSlim` | A | `NodeReport`。**这是 §4 里那个占 poll p95 94% 的 `panel_fetch`**（`traffic.go:487`），它同时喂两样东西：`inb.ClientStats`（每客户端累计）与 `traffic.go:494` 的 `inboundCounter{up,down}` → `nodeTraffic.InsertBatch`（`traffic.go:812`，**节点流量图表的唯一数据源**）。§4 那句「一次调用返回整个节点的客户端计数」在这一行兑现，不在别处。 |
| `GetInbound` | ⚠ 未解决 | 不能由本地快照作答。`user.go:2582` 的 GetInbound 不是在读配置，它是「别在面板抖动时大规模丢 ownership」的守卫，其结果授权一次破坏性本地写（`:2632` staleInbound → `:2670` `ownership.RemoveByMatch`）。本地作答后「面板挂了」与「inbound 已被删除」同时变得**不可产生**。且 flow 合成不出来：`extractDefaultFlow` 读 `settings.clients[].flow`，而快照按构造过 `StripClients`。 |
| `AddInbound` | A ⚠ | `ConfigApply`。**⚠ 三处 `_ = DelInbound` 回滚不能直接删**：`node.go:461-473` 与 `:953-989` 都是先建监听器、后 `nodes.Create`，Create 失败就留下一个**不在任何 PSP 行里**的活监听器。PSP 铸造身份消灭的是「认领」问题，不是「孤儿」问题。所以要么保留回滚，要么 `ListenerEnumerate` 必须存在——不能两个都不要。 |
| `UpdateInbound` | A ⚠ | `ConfigApply`。**⚠ 前置条件：先收敛期望文档的唯一生产者。** `node.go:677` 推的是管理员表单原文，不是 `SpecFromNode(n)`；今天它安全**只是因为** xui 适配器做了 RMW（`client.go:668-679` 用实况换掉 `clients[]`）。新协议禁止适配器读回合并，于是表单里那份**可能带 `clients[]`** 的 blob 直接成为期望配置——§3 的静默清零换条路重现。 |
| `DelInbound` | A ⚠ | `ConfigApply`（缺席即删）。**⚠ 「已经不在了」这个否定不能由 agent 的 present 位提供**：`node.go:876` 今天由 PSP 的传输层制造它，并分岔到语义完全相反的两条路（`:878` 保留客户端的 unclaim vs `:886` 真删客户端）。agent 重启期间一次假 absent 会让 PSP unclaim + `nodes.Delete`，而监听器和它的客户端仍然活着：**无人拥有、无人可达、仍在服务**。 |
| `SetInboundEnable` | C ⚠ | 删除；enable 是 `ConfigApply` 文档里的一个字段。**⚠ 必须同时改 `node.go:708` 的能力门**——它在写库**之前**就 `return ErrPanelCapabilityUnsupported`，不改的话管理员点开关直接报错，连 `n.Enabled` 都没落库。（顺带：`inboundcfg.go:126` 的 `SpecFromNode` 本来就写 `Enable: n.Enabled`，不需要把 Enable 塞进快照。） |

**client 单体（5）与 client 批量挂载（8）—— 13 个方法塌成 1 个操作**

这是这张表最大的结果，单独写在 §2.2。

| 方法 | 结论 | 兑现方式 |
|---|---|---|
| `AddClient` / `UpdateClient` / `DelClientByEmail` | A | 同一条 `ClientSetApply` upsert / remove。协议里**不存在**「创建」与「更新」两个动作，upsert 天然幂等，`isDuplicateClientErr` 那段匹配 3X-UI 错误文案的 substring 代码随之删除。 |
| `GetClient` / `ListClientInbounds` | A | `NodeReport` 的名册（同一份，见 `ListInbounds` 行的 ⚠）。 |
| `AddClientToInbounds` · `AttachClient` · `DetachClient` · `BulkAttach` · `BulkDetach` · `BulkCreateClients` · `BulkDelByEmail` · `BulkSetEnabled` | A / C | **零个专用操作。** 挂载是 client 对象的 `inbounds` 字段，改挂载就是改字段；批量是 `ClientSetApply` 的天然形态而不是一个优化端点。`BulkSetEnabled` 与 `BulkDetach` **今天就删**（零生产调用点）。 |

**状态（1）**

| 方法 | 结论 | 兑现方式 |
|---|---|---|
| `GetServerStatus` | A ⚠ | `NodeReport` 的头部。**⚠ 必须带 core 运行状态**，不能只带版本身份：`ServerStatus.XrayState` 今天喂 `admin_servers.go:386/388` 的 Servers 页，只报 `agent_version/core_version` 会让「psp 节点的 core 挂了」在 UI 上没有任何表现。 |

**可选能力（7 接口 / 9 方法）**

| 方法 | 结论 | 兑现方式 |
|---|---|---|
| `Capabilities()` | A | agent 自报 `features[]`。**极性翻转**（§0.5）：不再是「上游缺什么」，而是「这个 agent 版本提供什么」。 |
| `ListLiveClientIPs()` | A | `NodeReport`。注意它今天与 `ListInboundsSlim` 骑**同一个 goroutine、同一个面板槽位**（`traffic.go:509`），新协议里它们本来就是同一份上报。 |
| `GetFail2banStatus()` | B | 不实现。`psp` 适配器直接答 `ErrPanelCapabilityUnsupported` —— 执行搬进 core 层之后这个探针没有被探测对象。**注意不要答 `ErrXUIEndpointUnsupported`**，那是版本闸的语义（见 §2.4 ④）。 |
| `GetCoreVersionList()` / `InstallCore()` | A | `CoreVersions` / `CoreInstall`。 |
| `GetPanelUpdateInfo()` / `UpdatePanel()` | A | `AgentUpgrade`。 |
| `GetWebCertFiles()` | A | `TLSMaterial`。 |
| `ScanRealityTargets()` | D ⚠ | `RealityProbe` —— 全表**唯一**真正的「调用」（只有节点的网络视角能答）。**⚠ 但不要连 JSON 契约一起保留**：`RealityScanResult` 的 tag 注释自己写着「deliberately follow 3X-UI's camelCase response contract」。前端要它是**加一层 handler DTO 的理由，不是冻结线上协议的理由**。 |

### 2.2 主结果：13 个方法塌成 1 个操作

`AddClient` / `UpdateClient` / `DelClientByEmail` / `GetClient` / `ListClientInbounds` / `AddClientToInbounds` / `AttachClient` / `DetachClient` / `BulkAttach` / `BulkDetach` / `BulkCreateClients` / `BulkDelByEmail` / `BulkSetEnabled` —— **13 个方法，在新模型里是 1 个 `ClientSetApply` 加 1 份 `NodeReport` 名册。**

原因正是 §7.4：3X-UI 的 client 住在某个 inbound 的 settings JSON 里，所以「挂载」必须是动词、必须有加法和减法、还必须有批量版本来省 xray reload。**S-UI 形状里挂载是 client 对象的一个字段**，于是 attach/detach/bulkAttach/bulkDetach 这一族在新协议里是**零个操作**，不是四个。

「批量」这个概念也一起消失：`BulkCreateClients` 存在的唯一理由是「一次重启而不是 N 次」，而声明式 apply 天生就是一次。**代价要写清楚**：「一次写 = 一次 reload」的成本转移到 agent —— PSP 无条件发期望态 + 版本号，agent 自己 diff 决定要不要 reload。于是 `clientUnchanged` / `lifecycleWriteReason` / Phase 0 那整套跳过指标在 psp 后端上**失去被测对象**，要保住它们衡量的东西得改成量 agent 侧的 reload 计数。这是要主动做的迁移，不是白得的。

### 2.3 验收暴露的六个洞（协议定稿前必须回答）

> **进度（2026-09-09）**：洞 1、2、6 已定稿，见 **§8**。洞 3 由声明式模型**结构性消解**（PSP 从自己的行生成文档，行没建成的监听器从来不会出现在任何一份已发布的文档里）；§8 因此不为它保留 `ListenerEnumerate`。洞 4 在 §8 拿到第一个真实生产者（`rejected` 是唯一「重试同一内容无用」的终态，必须能超时升级成 Issue）。**洞 5 已于同日定稿，见 §8.4。六个洞全部关闭。**

对抗验收把 21 行里的 19 行推翻过，绝大多数不是「映射错了」，而是**这个 port 方法今天在偷偷提供第二样东西**。六个洞按严重度排：

1. **「已应用版本 N」的作用域从来没被定义。** 三组分别按监听器、按名册、按一次 apply 调用使用它，而 §5 硬约束 2 只说「agent 回报的是我应用了版本 N」，**没说 N 是谁的版本号**。ADR 0025 的 Q2b 正好点名这个坑（部分失败怎么表达）。**版本号的粒度是协议的一等决策**，且它取决于第 2 条。**→ 已定稿：§8.2。**
2. **一个节点的配置文档和它的客户端名册，是一份带版本的文档还是两份？** `ConfigApply` 与 `ClientSetApply` 至今是两条独立通道，没有任何一行说明它们的版本关系。**→ 已定稿：两份文档 + 一段指令流，见 §8。**
3. **孤儿回收（`ListenerEnumerate`）被三条独立论证要求、被零行承载。** `AddInbound` / `ListInboundsSlim` / `DelInbound` 三行各自推出「必须能枚举这台 agent 上的全部监听器」，因为 `node.go:461-473` 与 `:953-989` 都是先建后 Create。这是全表里唯一被三次独立推导出来的必要操作。
4. **CONTESTED 这一整类没有出口。** §5 要求协议能表达全部四种归属，CONTESTED 的终态是「不修、记一个稳定 issue code、交给人」——而表里所有 issue code（`inbound_missing_disabled_node`、`flow_render_divergence`）**全是 PSP 侧自己产生的**，没有一处让节点报告一个它自己无法调和的状态。`Issue` 操作是为这一格留的，语义未定。
5. **§7.2 的「面板聚合、节点执行」没有被任何 port 方法牵引出来。** 它是 §7 调研里唯一被点名「要抄，而且要抄到配额上」的东西，但它不对应任何现有方法——所以按方法映射的做法**天然看不见它**。`EnforcementDirective` 是凭 §7.2 直接补进操作表的，不是从这 21 行推出来的。这本身是这次方法学的一个已知盲区。**→ 已定稿：§8.4。**
6. **`ClientSetApply` 需不需要同步语义，与 §7.5 的「节点拨出」直接冲突。** `user.go:2656` 的 `perr` 决定 sync task 是否重试，`sync.go:216-218` 的 `UpdateClient` → `ownership.UpdateUUID` 依赖顺序保证——这些理由都成立，但「调用返回时」这个概念在节点拨出的协议里**不存在**。要么承认凭据吊销需要一条即时通道（Q2b 对 revoke 有例外），要么回去改 §7.5。**不能含糊。** **→ 已定稿：用版本闸，不买第二条通道，§7.5 不动。见 §8.4。**

### 2.4 差点被走私进新协议的四种 3X-UI 形状

按 §2 那段警告逐条对照，验收自己也漏过了这四处：

1. **把「email 全面板唯一」立成协议主键。** 两组独立地提议用 `(panelID, Email)` 当 client key，理由是它在 PSP 存储层已经被证明唯一（`uk_psp_client`）。但 **`Email` 是渲染产物**：`clientplan.go:314-324` 在面板只需一个客户端时去掉分区后缀，分区总数跨越 1↔2 就 re-key；再叠上它依赖 `rules.Domain`（改域名 = 全量 re-key）。把一个会因为**别的分区**出现/消失而改变、且依赖一个可配置域名的字符串当协议主键，是把 3X-UI 的缺陷买断了。**正确形状**：主键用 **PSP 自己铸造的行 id**（`cli_{psp_clients.id}`），email 降级成随载荷下发的展示字段。

> **订正（2026-09-09，§8 定稿时）**：这一行原本开的处方是「用 `partKey.canon()` 派生的 partition id，并且把 flow 写进 canon」。**两半都错。** flow 早就在 canon 里（`clientplan.go:187-189`）；而 canon **不是客户端身份的纯函数，它是位置相关的**：`clientplan.go:250-274` 的 `keys[i] = {preq[i], freq[i]}` 把排好序的「所需密码类」与「所需 flow」**按下标配对**。于是同一个客户端的 canon 会因为**别的节点**出现或消失而改变——用户只有一个 VLESS+vision 节点时是 `{默认密码类, vision}`，管理员给他加一个 SS-2022-256 节点，同一个客户端变成 `{pw256, vision}`。
>
> 那句 "pure function of the key's content (never positional)"（`:191-193`）说的是 **key → email** 稳定，不是 **client → key** 稳定。用它当协议主键，就是 §2.4 ① 自己点名的那个错误换了个字符串重犯一次——而且后果更热：名册是全量 apply，旧 key 缺席即被删，agent 侧该客户端的累计计数从 0 重来。
2. **把 `/attach` 端点强行留下来。** 唯一撑着「必须保留一个加法原语」的调用点是 `sharedclient.go:813` 的 `BulkProvisionNodeInbound`，而它的 docstring 自陈是 **best-effort warm-up、与权威 resync 重叠、目的是 front-load 成一次重启**——正是 §2 警告点名的「批量接口只为少触发 xray reload」。所以这里应判 C：这条冷路径整个删掉，由 `ResyncMembershipOrEnqueue` 独自承担，协议只留声明式的挂载字段。
3. **把 3X-UI 的响应契约当成 agent 的线上格式**（`RealityScanResult` 的 camelCase tag）。见 §2.1 最后一行。
4. **把两个 error 语义对调。** `ErrXUIEndpointUnsupported` 的定义是**版本闸**（路由在这个面板版本上 404），`ErrPanelCapabilityUnsupported` 才是「这个适配器不实现这个可选操作」——`ports/xui.go:17-20` 明写两者 distinct。对一个自研 agent 根本不存在「路由在某个面板版本上 404」，继续用前者就是把 3X-UI 的版本兼容坐标系搬过来。

### 2.5 这一刀立刻删掉的东西

映射表证明为死的，本次一并删除，不留到第 3 步：

- **`BulkSetEnabled`**、**`BulkDetach`** —— 零生产调用点（只有 port、两个适配器、测试替身）。删掉等于 `xui` 与 `sui` **永久**各少实现两个方法。
- **`DelClientByEmail` 的 `inboundID` 参数** —— `xui` 适配器函数体从不引用它，`sui` 直接命名为 `_`，三个调用点里两个传字面量 `0`。3X-UI 3.2.0 之前按 inbound 删客户端时代的残留。

`ports.PanelClient` 因此从 21 个方法降到 **19 个**。

## 3. 要消除的具体缺陷（已实测，非推测）

这些不是「不想依赖别人」，每一条都在这个仓库里被复现过：

| 现状 | 根因 | 自研后 |
|---|---|---|
| **设备数上限从不生效** | 3X-UI 只在客户端拉取**它自己的**订阅端点时才认设备，而 PSP 接管了订阅 | 消失——执行点搬到我们控制的一端 |
| **并发 IP 上限需要 fail2ban**，且 `XUI_ENABLE_FAIL2BAN=1` 会**静默关掉**它（只认字面量 `"true"`） | 上游把执行绑在 fail2ban + 环境变量两道闸上 | 消失——agent 在 core 层直接断连，不需要防火墙配合。**所以 `Fail2banReader` 不实现** |
| **3.7.0 上用户名/密码模式不可用** | `/login` 被 CSRF 挡住，而 PSP 在登录成功之后才取 token | 消失——认证我们自己定 |
| **能力差异**（S-UI 存不下 `limitIp`/`limitHwid`；`limitHwid` 要 3.7.0） | 两个上游的 client 模型不同 | 消失——一套模型 |
| **四个字段静默清零**（`limitHwid`、`resetDay` 组、inbound 的 `total`/`subSortIndex`、客户端的 `comment`/`group`） | 与整结构 Save 语义的阻抗失配，**没有一个是 PSP 写错代码** | 消失——但要靠 §5 的机制，不是靠小心 |

**同时换走的**（写下来，免得被忘记）：**xray-core 兼容责任**。现在 3X-UI 替 PSP 挡着 core 的变化（例：26.7.11 把 `minClientVer` 空值默认从「不限」改成 `26.3.27`,直接让 mihomo/Clash Verge 连不上），而 core 发版比 3X-UI 频繁。这一层自研后归我们。

## 4. 性能预算（已定）

**不比现在差。** 不是「以后总会用上」。

2026-09-08 生产实测（22.2 小时，667 轮 poll，间隔 120s，25 用户 / 5 面板）：

| | 实测 | Phase 2 触发线 |
|---|---|---|
| 一轮 poll p95 | 2998ms = 周期的 **2.5%** | 50% |
| `panel_fetch` p95 | 2827ms = 周期的 **2.4%**（占 poll p95 的 94%） | 50% |
| 推送并发闸等待 p95 | **0.048ms**（无争用） | —— |
| 用户数 | 25 | 四位数 |

适配器方案还有 **20~40 倍余量**。所以新后端若更慢，那是净亏损，不是「早晚要付的代价」。

**可直接借用的事实**：poll 的开销 94% 是等面板回话，且随**面板数**而非用户数扩展。agent 的读接口设计应当保住这个性质（一次调用返回整个节点的客户端计数，而不是每客户端一次）。

### 翻转后的坐标系（写清楚，因为三份协议提案都算错过）

拓扑要按真实的来，不能拿节点数当主机数：

| | 今天 | 翻转后 |
|---|---|---|
| 拨号方 | PSP → 5 个**面板** | **5 台 agent** → PSP |
| 「节点」 | 9 行 `(panel_id, inbound_id)` | 同样 9 个**监听器**，散在那 5 台主机上 |
| 稳态请求率 | 5 次 `panel_fetch` / 120s = **0.042 req/s** | 5 次 `sync` / 120s = **0.042 req/s** |

**9 是监听器数，不是 agent 数。** 一个 PSP「节点」是一行 `(panel_id, inbound_id)`（ADR 0025 Q2a），不是一台主机；而 §8 把协议作用域定成了一台 agent。三份协议提案都用了「9 台 agent」这个不存在的机群，其中一份还把周期悄悄从 120s 减半到 60s——方向性结论不受影响，但数字曾经是照着一个虚构的拓扑算的。

**两条不许记错账的话：**

- **94% 的收益来自「节点拨出」，不来自「文档拆成两份」。** 翻转之后 `panel_fetch` 那 2827ms 的等待整个消失（PSP 不再等任何人回话），这是拨号方向翻转买到的，**不许记在 §8 文档拆分的账上**。
- **拆文档省下的字节，在比例上显著时绝对值可忽略，在绝对值要紧时比例可忽略。** 三个形状各自算过，比值 1.005~1.19。§8 否掉「一份文档」靠的是 reload 隔离，不是带宽——这条护栏放在这里，免得后来者重新把带宽捡起来当论据。

## 5. 字段所有权必须是机制，不是逐字段判断

§3 那四个静默清零的教训不是「下次小心点」。**只要协议里存在「整结构写回」这个动作，就会有字段在某条路径上被遗漏。**

按 [ADR 0025](adr/0025-push-pull-decision-rule.md) 的 Q1，每个字段有四种归属，协议必须能表达全部四种：

| 归属 | 含义 | 协议要求 |
|---|---|---|
| **PSP 拥有** | 期望态（启用、限额、到期、UUID/密码、IP/设备上限） | PSP 下发，agent 不得自行改写 |
| **节点拥有** | 观测态（流量计数、在线 IP、在线数、进程健康、**已应用的配置版本**） | agent 上报，PSP 不得写 |
| **JOINT** | `f(PSP 的量级, 节点的原点)`——例如配额 cap | 写前必须现读节点那一半；**相等比较无效**，只能用带方向偏置的容差 |
| **CONTESTED** | 运行时决定归属（如 flow） | 不修、记一个稳定的 issue code、交给人 |

**两条硬约束**（同样来自 ADR 0025）：

1. **观测态不得覆盖期望态。** 要能对账，就必须分开存两个字段。PSP 现在的 reconcile 推完 `UpdateInbound` 会 `GetInbound` + `Capture` 覆盖自己的快照，于是「确认」退化成同义反复，`config_sync_state` 的 `"drift"` 至今没被写过一次——新协议不能重犯。
2. **agent 回报的是「我应用了版本 N」，不是「这是我的配置」。** 后者会被 PSP 当成新的期望值吞下去。

## 6. 推还是拉：Q2a 在这里翻转

[ADR 0025](adr/0025-push-pull-decision-rule.md) 的 Q2a 是「你控制节点上的软件吗」。对 3X-UI/S-UI 答案是**否**，所以 PSP 今天没有选择权。**自研后答案变成是**,Q2b 才第一次成为真问题。

**调研已完成（§7.5）：方向定为节点拨出，但心跳与用量解耦，且面板侧对失联做独立判定。** 下列三条在调研之前就成立，调研只是给它们补上了外部证据：

- **累计计数 vs 增量**和推/拉**正交**。PSP 现在读的是面板的累计值、自己做单调差分（`LastRawXxx` 基线），agent 应当继续上报**累计值**——可重放，丢一轮自愈。**§7.3 给出了反例的代价**：V2bX 在发送前就把计数器清零，一次失败的 POST 永久丢账。
- **遥测若改成 agent 主动推，必须是「数字没变也照发」的心跳。** 否则「死掉的上报器」和「没人用的空闲节点」长得一模一样——这是本仓库反复出现、也反复设防的失效模式。**§7.3：V2bX 正是这样翻的车**（没有流量就整轮不发）。
- **单一拨号方今天在偷偷提供四样东西**，翻转前必须逐条给出替代品：对远端一行的互斥（`clientWriteLocks` 是包级锁）、`UpdateInbound` 的读-改-写窗口、失败域独立的第二观察者（健康探测）、以及**「这次没到」这个证据由谁制造**（PSP 拨号时它由 PSP 的传输层制造；改成 agent 推之后，它变成被观测组件对自己是否故障的自述）。

## 7. 上游协议调研（已完成 2026-09-08）

四份源码里只有三份可读——**XrayR 上游仓库已被清空**（`xrayr-project/XrayR` 的 HEAD 是一次 "Clear all files" 提交，工作树为空），所以它由继承同一形状的 V2bX 代表。这一条本身就是结论的一部分：机场生态里那个"经典形状"的原始实现已经没有维护者了。

### 7.1 三种形状，不是三个协议

| | 3X-UI 3.7.0 | V2bX（XrayR 后继） | S-UI |
|---|---|---|---|
| 节点是什么 | **另一个完整的 3X-UI 面板** | 瘦 agent（xray / sing-box） | 面板本身，无 master/node |
| 谁拨号 | master → node | **node → panel**（节点无入站端口） | — |
| 认证 | Bearer token（`node-sync` scope）或 **mTLS**（master 自签 CA） | `?token=` 查询参数 + `node_id` + `node_type`，**全局共享密钥** | — |
| 配置传递 | master 写：`/inbounds/add`、`/clients/update/:email` … | node 拉：`GET /UniProxy/config`、`/user`，**ETag + 304** | — |
| 用量传递 | master 拉（读 node 的累计计数器） | **node 推增量**：`POST /UniProxy/push` `{uid:[up,down]}` | — |
| 在线设备 | master 拉 `/clients/onlinesByGuid` | node 推 `POST /UniProxy/alive`；**再从 `/alivelist` 拉回全局聚合值** | — |

**3X-UI 没有"节点协议"可借。** 它的 node-sync 是同一套面板 API 加一张 21 条路由的白名单（`internal/web/controller/api.go:91`），节点必须**是**一个 3X-UI 面板。采用它等于让我们的 agent 变成 3X-UI——正是本次自研要摆脱的那笔兼容税。所以它对我们的价值是**参考**，不是复用。

### 7.2 能借的三样

- **ETag + `If-None-Match` + 304 的配置拉取**（V2bX `api/panel/node.go`、`user.go`）。这正是 ADR 0025 的 Q4 想要的东西：拉取的代价可以压到一次条件请求，配置不变时零载荷。V2bX 还在 304 之外再做一次 body 的 SHA-256 比对——因为**上游面板的 ETag 不可信**；我们两端都自己写，不需要这层补丁，但它提醒了一件事：ETag 必须由内容决定，不能由时间戳决定。
- **msgpack 作为可选编码**（`X-Response-Format: msgpack`）。用户列表是唯一会随用户数线性增长的载荷，值得给它留一个编码协商位。
- **面板聚合、节点执行**（`/UniProxy/alivelist`：面板把跨节点聚合后的每用户在线设备数发回节点，节点照它执行）。这正面回答了我们在 [connection-limits.md](connection-limits.md) 和 `PanelQuotaCap` 里记录的 **P× 放大**问题：单个节点看不见其他节点的加数，所以**求和必须发生在面板，把和发下去**，而不是把同一个上限发给每个节点。这条要抄，而且要抄到配额上，不只是设备数。

### 7.3 必须自己造的两样（上游都做错了）

**一、用量上报必须是累计值，不能是增量。**

V2bX 的节点在**发送之前**就把本地计数器清零了：

```go
// core/xray/user.go:60  GetUserTrafficSlice(tag, reset=true)
up := traffic.UpCounter.Load(); down := traffic.DownCounter.Load()
if reset { traffic.UpCounter.Store(0); traffic.DownCounter.Store(0) }   // ← 先清零
...
// node/user.go:11  调用方
userTraffic, _ := c.server.GetUserTrafficSlice(c.tag, true)
err = c.apiClient.ReportUserTraffic(userTraffic)                        // ← 后发送
if err != nil { log.Info("Report user traffic failed") }                // ← 只记日志
```

一次失败的 POST**永久丢掉那一窗口的用量**：计数器已经清了，没有重试、没有补发、没有任何一处知道丢了多少。而这恰恰发生在面板不可达的时候——也就是最可能连续失败的时候。

PSP 今天读的是面板的累计值、自己做单调差分（`LastRawXxx` 基线），这个选择现在有了外部证据：**累计值可重放，丢一轮自愈；增量不可重放，丢一轮就是永久错账。** 本仓库已经为"重置检测"付出过代价（`counter_reset_test.go`），那是累计模型的已知成本，比永久丢账便宜得多。

**二、心跳必须与用量解耦。**

V2bX 在没有流量时**根本不发**（`node/user.go:12`：`if len(userTraffic) > 0`），而且 `GetUserTrafficSlice` 还会把低于阈值的用户整个跳过。于是"上报器死了"和"这个节点没人用"在面板侧**完全同形**——这正是本仓库反复设防的那个失效模式（诊断页的「零要看窗口」、`psp_live_ip_users_incomplete_total` 的 floor 标记、地理检测器的 `unknown ≠ clean`）。

我们的 agent **必须在数字没变时也照发**，并且面板必须能区分"上报了 0"与"没有上报"。这条已经写在 §6，调研只是把它从"我们的经验"升级成"上游最广泛部署的实现确实这样翻车"。

### 7.4 模型形状：跟 S-UI，不跟 3X-UI

S-UI 的 `Client` 是**一等对象，横跨多个 inbound**（`database/model/model.go:25`，`Inbounds json.RawMessage` 是一个列表），并且自带 `Volume` / `Expiry` / `AutoReset` / `ResetDays` / `NextReset`。

3X-UI 的 client 住在**某一个 inbound 的 settings JSON 里面**，所以"一个用户"在 P 个 inbound 上就是 P 份互相独立的副本——PSP 的扇出、`clientplan` 的拆分、以及 §5 那整套字段所有权机制，全都是在补这个模型缺陷。

**我们的模型采用 S-UI 的形状**：一个用户对象，挂到 N 个 inbound 上，配额和到期是用户的属性而不是副本的属性。这不是偏好问题——P× 放大、`psp_user_clients_per_panel > 1` 时每份副本各带一套连接上限、以及"改一个字段要写 P 次"，三个已记录的问题都直接来自 3X-UI 的那个模型。

### 7.5 拨号方向：Q2b 的答案

调研没有把方向定死（3X-UI master 拨、V2bX node 拨，两种都在生产里跑），所以 §6 列的四件事仍然是判据。但调研加了一条**新的成本项**：

V2bX 那种"节点拨出"的形态，让节点**不需要任何入站端口、不需要公网可达、不需要证书**——这是它在机场生态里胜出的真正原因，跟性能无关。代价是 §6 已经列出的第四条：**"这次没到"这个证据变成被观测组件对自己是否故障的自述**。V2bX 没有解决这个代价，它只是没有承认。

所以 Q2b 的答案是：**节点拨出，但心跳与用量解耦，并且面板侧对"该到没到"做独立判定**（`last_seen` 超过 N 个心跳周期即判定失联，不依赖节点自己承认）。这样既拿到"不需要公网可达"的运维收益，又不把故障检测交给故障方自己。

## 8. 协议形状（2026-09-09 定稿：洞 1、2、4、5、6）

**决定：两份带版本的文档（`config` = 监听器，`roster` = 名册）+ 一段同样带版本的指令流（`directives`），三个独立版本流装在一次往返的同一个响应里；作用域是一台 agent；两个主键都用 PSP 铸造的行 id。**

### 8.1 否掉「一份文档」的不是字节

三个形状各自算过带宽，比值 1.005~1.19 —— **这条论据被三方独立打穿，护栏留在这里免得后来者重新捡起来**：拆文档省下的量，在比例上显著时绝对值可忽略，在绝对值要紧时比例可忽略。

真正的理由是 **reload 隔离**。一份文档下，每一次停用用户都让**监听器配置**的 ETag 失效，agent 必须重新 diff 监听器配置；一个 diff bug 或一次 core 的 JSON 规范化变更 = **每停用一个用户 reload 一次 core**，正是 ADR 0025 对 `client.enable` 警告过的那个带 reload 的写循环。两份文档下 agent 连那份文档都不会打开，**这条路径结构上不可达**。这是一份文档无论加多少逐对象状态表都补不平的唯一一条。

而「一份文档买到引用闭包」这个卖点是虚的：它只买到**文档级**闭包，撑不过部分应用——一个 client 照样会挂在一个已接受但被 core 拒绝的监听器上。**可重入的 join 三个形状都得写。**

### 8.2 洞 1 的答案

**N 是「流」的版本号。一台 agent 的已应用状态是一个每流一个 N 的向量。期望侧按文档编版本，观测侧按对象报收敛。**

分工写死：**版本回答「哪一份」，状态回答「到哪一步」。** 洞 1 之所以有三组人给出三个答案，正是因为这两件事被挤进了一个数字。

- **不按监听器/客户端编版本**：删除在声明式全量里就是**缺席**，而一个不存在的对象不能携带版本号——版本号必须住在表达成员资格的那个容器上。
- **不按 apply 调用编版本**：随 §7.5（节点拨出）一起死，「调用返回时」这个概念不存在。
- 三个流的版本对严格说是**偏序**；此处可判定只因为节点的每个坐标都不可能超过 PSP。规格写「每流可比」，不写「全序」。

### 8.3 线上规格

**端点**：稳态唯一 `POST /v1/node/sync`。节点拨出，上行 `NodeReport`、下行三段，同一次往返——响应必须在知道 agent 刚报了什么的前提下计算。§2.1 的五个**调用**（RealityProbe / CoreVersions / CoreInstall / AgentUpgrade / TLSMaterial）转成响应里的 `tasks[]` + 下一轮的 `task_results[]`。**这笔账明着付**（ADR 0025 Q0：「这是要主动付的代价，不是换个拨号方就白得的」）：交互延迟最长一个心跳周期，响应带 `next_poll_s` 供 PSP 下调。**这句只记账，不结账——延迟怎么真正付掉见 §8.7（长轮询）。**

**版本序**：`(epoch, version)` 字典序单调，不是单个 int64。epoch 在注册时铸、在 PSP 侧文档行被**重建**时 +1；agent 遇到更高 epoch 时清零本地已提交版本。**没有这条，一次 DB 还原会让 agent 永久拒收、无限期以旧配置服务，唯一出路是重装。** DB 还原是常规运维事件。

**作用域**：一台 agent，身份是注册时铸的 `agent_id`（不是地址——NAT 后面的 agent 没有稳定地址）。协议明写 **agent ↔ panel 作用域 1:1**：否则同一个 client 的累计计数要跨 N 台 agent 求和折进**一个**标量基线（`pspclient.go` 的 `LastRaw*`，注释自陈无 per-inbound 求和），一台 agent 重装让和值下降，而 `monotonicDelta` 在 `d < 0` 时返回 current —— **这是超记，不是丢账，会把用户瞬间打到配额上限。**

**主键**：`lst_{nodes.id}`、`cli_{psp_clients.id}`。派生串一律禁止（email、credClass、`partKey.canon()`、端口、remark、上游 inbound_id）。理由见 §2.4 ① 的订正。

**ETag**：每段一个 = 该段 canonical 序列化的 sha256，**纯内容，版本号不进 ETag**（§7.2 唯一被点名的那条规则；单调序号和时间戳是同一类东西）。**PSP 铸造必须内容幂等**：CAS 前先比 canonical 内容，相同则不铸新版本——否则「重算并写回同值」的空写会让稳态零载荷正好在 churn 最多的路径上失效。验证器只在请求体里一处，**不用 HTTP 条件请求头**（其语义定义在 GET 上，这里是 POST 且永不返回 304）。**收敛判据是 etag 相等，不是 version 相等**（版本回滚 A/B/A 时不产生假的未收敛）。

**引用完整性**：闭包由 PSP 的**同一个读事务**保证，不由版本算术保证。roster 带 `min_config_version`，它是**可复核凭据**，不是 agent 的门。agent 侧 join 的对象是**已收敛的**监听器集合，不是「已持有的文档」——否则一个已接受但被 core 永久拒绝的监听器上挂的 client 会被判 applied、名册报绿，而那批 client 一个都不可达。三态裁定：

| 情形 | 裁定 |
|---|---|
| `min_config_version` > 我持有的 config 版本 | `roster_ahead_of_config` —— **自愈中的正常态**，不告警；超过 K 轮升 Issue |
| 版本闭合、但 key 找不到 | `attachment_unknown_listener` —— PSP 侧缺陷，按自己的 bug 告警 |
| 监听器存在但被拒 | 该挂载记 `blocked`，**该 client 不得判 applied** |

**禁止 `requires_config_version >= N` 这类门**——join 必须可重入，可重入的 join 严格强于一个有序投递保证。这条禁令一字不改地留着：它正是后来者会顺手改成门的那种字段。**交集为空仍物化 client，绝不删除**（删除会让累计计数从 0 重来）。应用顺序：config 新增与修改 → roster 全量 → config 删除。

**部分失败（ADR 0025 Q2b 欠的那个答案）**：**原子的是期望态落库，不是运行时收敛。** 这句逐字进规格。`accepted{version, etag}`（事务）与 `objects[]`（收敛）是两个字段，允许不一致。对象四态：`applied` / `pending{since_version, first_failed_at}` / `rejected{issue_code, first_failed_at}` / `blocked{on}`。**`pending` 与 `rejected` 都必须能超时**升级成 Issue —— ADR 0024 的开工闸门要的是「有长度、可观测、**能超时**」，而 `rejected` 是唯一「重试同一内容无用」的一格。**这是洞 4 的第一个真实生产者。** 失败对象不阻塞下一版本，绝不部分回滚，`degraded` 是一等稳态。文档级缺陷（schema 不认识 / 闭包被绕过 / 删除集超出 coverage 声明）→ 整段拒收、保留上一版、报 Issue 交给人。

**枚举与覆盖度**：`objects[]` 对 clients 是**含零值的全量枚举**，PSP 把「不在枚举里」判为 issue，**永不判为闲置**（这条只对 `partial=false` 的报告成立；轻量轮询整块省掉这些字段，见 §8.7） —— 这是 §7.3 那条禁令在 apply 方向上的对偶。每段带 `coverage{count}`；`directives` 额外带 `for_roster_version` 与聚合覆盖度（「聚合完整」与「聚合缺了三个节点」不得同形）。**`directives` 条目缺席 = 保持上一次已知预算，永不解释成无限额**；`quota_headroom_bytes` **三态编码**（`null` 未配置 / `0` 已耗尽 / `N` 剩余），**不得 omitempty** —— `traffic_cap.go` 让「我不知道」和「无限额」同列的那个缺陷不许搬进新协议。

**re-key 是一次计数器 epoch 事件**：report 逐 client 带 `counter_epoch`，PSP 在 epoch 变化时**结转基线**而不是当成重置。

**`NodeReport` 只能写 `observed_*`，永不写 `desired_*`**；健康探测目标的 host 与 port 只能来自期望文档。不写这一句，接上 `inboundcfg.Capture` 与 `SpecFromNode` 就是 ADR 0025 债务 3(d) 换了个入口。

### 8.4 `EnforcementDirective` 的语义（洞 5）

#### 成员资格规则

> **一个值当且仅当「算出它需要跨 agent 求和」时，才属于 `directives`。**

推论也是它必须是**第三个独立版本流**的全部理由：它的内容是观测的函数 ⟹ 每周期都可能重铸 ⟹ 并进 `roster` 会毁掉 §8.1 买到的 reload 隔离。

由此**永久删除 `device_limit`**：设备数不需要跨 agent 求和，而且数据面根本没有设备概念（`connection-limits.md` §12.2），执行点只能是 PSP 自己的订阅端（§4.1）。**§2.1 操作表里「设备上限」一词随之作废。**

独立成立的一条：**`roster` 的 client 对象增加 `subject = usr_{users.id}`**。agent 拿到它才能在本地把同一人的多个分区 client 求和、源 IP 求并集，消掉 `connection-limits.md` §12.1 那个双重放大的第一重。

#### 配额：下发绝对原点，不下发指针

- `baseline_bytes` = **PSP 在这一次往返里刚收到的**该 client 行累计计数（§8.3：响应在知道 agent 刚报了什么之后才计算）。不是采纳时刻的活计数器，所以不存在「每次重锚白送一段用量」。
- 节点谓词：`counter_now >= baseline_bytes + headroom_bytes`。与今天的 `PanelQuotaCap` **同形**。
- 编址与 epoch 一律**逐 client 行**，不提升到 subject 级。

**三态映射写死**（`limit <= 0` / 未配置 / 解析失败 → `null`；`limit > 0 且 limit − used <= 0` → `0`；其余 → `limit − used`）。**禁止经过 `TrafficFloorBytes`**：它的 `1` 同时表示「已耗尽」和「还剩 1 字节」（`rem <= 0` 返回 1，而 `rem == 1` 也返回 1），这个同形不许搬进新协议。不得 `omitempty`。

**闸态三个，没有第四个**：`unconfigured`（不执行，**不等于无限额**）/ `armed` / `closed`。条目缺席 = **保持上一次已知的 `(baseline, headroom)` 对，没有 TTL，永不解释成无限额**；「从未有过」与「本轮缺席」由闸态区分，编码不同形。`NodeReport` 逐 client 带 `gate_state`，**含零值全量枚举**。

**分子与 limit 分开处理**：分子（Σ 已用）只许用各 entry 的**最大已知累计值**——累计计数单调，所以一台沉默 agent 的加数冻结在最后已知值，永不因沉默而变小、永不推高 headroom。而 **`limit` 与计费周期的变化是 PSP 期望态，与聚合覆盖度无关，永远精确、立刻下发**：日历对齐滚动那一轮全体同时恢复，任何覆盖度门都不许挡住它。

#### 月初刷新不能依赖 PSP 当时在线（2026-09-09 定）

**先说今天的行为，因为这不是新协议引入的问题**:周期滚动发生在 PSP 的流量轮询里
（`traffic.go` 的 `shouldRollPeriod`）,PSP 不跑，滚动就不发生——**连 PSP 自己库里都没滚**。
所以「月底维护 PSP」时：

| 谁 | 会怎样 |
|---|---|
| 配额没用满的用户 | **不受影响**。手上还有 headroom,照常上网 |
| **已经用满的用户** | **继续被闸住**,直到 PSP 回来才补滚 |
| 全体用户失联 | **不会**。节点不需要 PSP 就能服务，缺席只是不更新 |

所以「所有用户失联」不会发生，但**「曾经用满的人，新一个月暂时用不了」会**——
维护多久就锁多久。PSP 回来后 `shouldRollPeriod` 看到月份变了会自动补滚，损失是延迟不是永久。

**这一条今天对 3X-UI 也一样成立，不是自研引入的**。但**自研恰好能修它，适配第三方面板修不了**——
因为修法需要节点能持有一份「未来才生效的授权」,而第三方面板没有地方放这个东西。

**修法：PSP 提前把下一期的授权发下去。** `QuotaEntry` 增加两个字段：

```
period_ends_at_ms            这一期什么时候到期
next_period_headroom_bytes   到期时生效的额度（三态，nil = 没有排期）
```

到点时节点把 `baseline` 重锚到自己当前的计数器、把 `headroom` 换成排期值，然后清掉这两个字段。
**只排一期，不是排程表。** PSP 下次联系上时会精确重锚，它自己的周期记账是权威的，不依赖这个。

**这不是「沉默即赦免」,这个区分是整条设计的重点。**
「PSP 不吭声了，那我就当无限额」**依然禁止**;
「PSP 在 20 号告诉我：1 号这一行拿 N 字节」是**把 PSP 已经做出的决定提前送达**。
前者是拿节点没有的信息猜，后者是 PSP 说过的话。

**缺失方向照旧倒向拒绝**：两半必须都在才生效。少了任何一半都不刷新——
漏刷一次是把付了钱的用户锁到 PSP 回来（吵、有人投诉、可恢复），错发一次是白送一期配额（安静、收不回）。
已双向变异验证（缺 grant 也刷新、忽略截止时间）。

**时钟偏差直接换成配额**,所以节点上报自己的墙上时钟，PSP 对偏移报 Issue。
泄漏量本身由上一节的 `grant_ceiling` 兜住——两条防线正好叠在一起。

> **⚠️ 镜像方向的洞，本次只记不修**：**到期/停用**走的是同一个机制的反面。
> 名册条目缺席 = 保持上次已知，所以 PSP 维护期间一个**账号到期的用户会继续被服务**。
> 这是漏收入，比「少服务」更值得修，而且修法同形（在 roster 条目上放一个「到期时间」,
> 让节点自己关）。**没有一并做，是因为「到期」在 PSP 侧有多个来源
> （`ServiceDisabledReason`、到期日、管理员停用），先要理清哪个是权威**,那是 A 轨的工作。

#### 断网时的偷跑上界：发放量必须有天花板（2026-09-09 补，未决 → 已定形状，数值待测）

**先说已经成立的部分**——断网时执法**不会失效**,三条各自独立：

1. **谓词是纯本地的**：`counter_now >= baseline_bytes + headroom_bytes`,两个操作数都在节点手上。
   连不上 PSP 不影响它求值。
2. **条目缺席 = 保持上一次已知的 `(baseline, headroom)`,没有 TTL,永不解释成无限额**（本节上文）。
3. **`unconfigured` 不等于无限额**（同上）。

所以节点**自然 fail-closed**:烧完手上的 headroom 就关闸，一直关到 PSP 重新出现。
**不需要再加一个「多久没联系上就停服」的超时**——那会让 PSP 变成整个机队的单点。

**洞在于发多少。** 三态映射写的是「其余 → `limit − used`」,也就是**每个 client 行都拿到全额剩余**。
连着的时候这不构成问题：每轮用刚收到的计数重新锚定 `baseline`,超发只存在于一个上报周期之内，
而 `overburn_headroom_bytes` 每轮把它算出来。**但断网时它变成一条不会缩水的信用额度**——
最坏可跑 `P × 剩余配额`,而 §8.4 自己记着 **P 实测 4.0**。

**修法是给发放量加天花板，不是给断网加超时**：

```
headroom = min(limit − used, grant_ceiling)
```

- **正常运行完全无感**。每 60 秒重发一次，一个客户端要在 60 秒内跑满 `grant_ceiling` 才会撞闸。
- **断网损失从 `P × 剩余配额` 降到 `P × grant_ceiling`**,与配额大小脱钩。
- **代价说在前面**：链路极快的客户端在 PSP 故障期间可能被误闸。
  这和上报周期是同一类账——**断网时容许超跑多少 ⟷ 断网时误伤多少**——所以它同样是个旋钮。

**数值待测，先不拍。** 按 §8.4 的纪律（「未测量的要标成未测量」）:
`grant_ceiling` 该定多大取决于单客户端 60 秒内的实际吞吐分布，那个分布**从未被测过**。
第一版按**不设上限**上线（即今天的行为，不静默改变任何事），同时记录
`overburn_headroom_bytes` 与单轮增量的分布；**拿到数据再定默认值**。
**但旋钮和公式现在就写进去**,否则「发全部剩余」会从一个未经检视的默认，变成一条没人记得动过的既成事实。

#### 并发源 IP：v1 只观测，不执行

三条理由，按强度排：

1. **PSP 自己今天对并发一个动作都没有**（`connection-limits.md` §12.5「现在只观测，什么也不做」，任务 #49 仍 pending）。节点执行它就不是「精确执法之下的一层网」,**它就是策略本身**——而策略从未被批准。
2. **跨节点并集天然滞后一个心跳，而三种候选形状没有一种能把这个残差写成诚实的形式。** 最诱人的那种（下发 `budget = 上限 − 别处已用`）**结构上就是错的**，代数一步就能看出来：节点维持 `|S_a| ≤ E_a`，而 `E_a = L − (|U| − |S_a|)`，于是驱逐量 `= |S_a| − E_a = |U| − L`——**与这台节点自己持有多少无关**。A 台节点各自剪掉全队的全部超额。代进真实拓扑（A=5、L=3、五台各持 1 个地址）：每台要剪 2、手上只有 1 → 全剪光 → 用户整机队离线 → 下一轮 `|U|=0` → 预算恢复 → 又全部放行。**振荡周期 = 一个心跳。** 方向正是 §12.3 判过死刑的那一类（会抖动的检测比没有更糟）。按 agent 均分则在 `L < A` 时超发到 `A/L` 倍，而 `IPLimit` 默认 0、L=1~3 是常态不是边角。
3. **误拒率不可测。** `liveips.go` 折叠的是 120s 一次的在线 IP **快照**；周期内出现又消失的地址在两轮快照里都不存在，而准入判定恰好发生在连接那一刻——PSP 侧无论怎么重放都算不出「照陈旧常量会拒掉谁」。

**所以 v1 下发 `ip_shadow`，agent 做影子执行**：动作为空，只算「我本会拒掉谁」并上报。字段**在 v1 就放进协议**，因为这是解锁任务 #49 唯一可能的证据来源，而事后加它要动协议。

谓词用 **`|local| > limit`**（纯本地，ADR 0025 Q3b「能」的那一侧，永不陈旧），它也是 v2 唯一可能被武装的谓词——v1 先量它的误拒代价。

#### 残差：滞后的聚合值不许被表述成「满足了上限」

节点侧的闸**从不声称** `Σ 用量 ≥ 配额` 被满足。它只声称一句可验证的话：「这一行从这个绝对原点起，最多再放行 `headroom` 字节」。跨节点那一半由 PSP 每轮**实算求和**写下来，不是任何一条 entry 里的标量：

```
overburn_headroom_bytes = Σ(baseline + headroom) − Σ(最新已报累计)
```

伴随三个整数，缺一不可：`numerator_oldest_report_age_ms`（这个求和自己的陈旧度上界）、`coverage.entries_stale`、`coverage.entries`。**分母规则写死**：所有上界、阈值、除数一律由 **PSP 自己的 roster 行**算出；`entries_stale` 只许打标与告警，**不得进入任何上界或分母**——否则覆盖度越差、宣称的误差上界反而越小。

`traffic_cap.go` 那三项误差预算在新协议里的去向：**STALENESS 没有消失，只是换了位置**（本 entry 自己那个加数变精确了，但分子是跨时刻马赛克）；**COUNTER RESET** 由 `counter_now < baseline` 落进 `unconfigured` + 逐 client `counter_epoch` 结转；**FAN-OUT 仍在，且分母是 client 行数（实测 P=4.0）而不是 agent 数**。

**措辞纪律**：PSP 对 `gate_state` 的复算是「**可复算的一致性检查，不是第二观察者**」——两个操作数都来自 agent，复算校验算术、不校验诚实（ADR 0025 Q4）。债务 3(b) 那张四格表仍然欠着。

**未测量的要标成未测量**：`607 MiB / p95 14.9 MiB` 测的是 *largest sibling drift*，**不是** 窗口内的超烧量；后者从未被测过，第一版上线后由 `overburn_headroom_bytes` 的分布补测。

#### 新鲜度字段坐在信封里

`computed_at_ms` / `numerator_as_of_ms` / `numerator_oldest_report_age_ms` / `next_poll_s` **写进响应信封，不进段体、不进 ETag、不触发重铸**——这是 §8.3「时间戳不进 ETag」+「铸造必须内容幂等」两条的直接后果。写进段体等于把 99% 的 skip 在协议层抵消掉。

`directives` 段**加性演进、未知字段一律忽略**（收窄 §8.3 的整段拒收）：只有结构性违规才整段拒收，否则一次 PSP 先升级就是全机队新用户停服。

### 8.5 关掉了哪些选项

**永久关闭**：按监听器/按 apply 调用编版本；一份文档（以后要合并只能走一次破坏性协议升级）；事件日志 + 游标模型（它推翻 §2.2 的声明式塌缩，需要快照 + 日志尾巴两套实现，并把状态从「当前文档的函数」退化成「历史的折叠」）；一台 agent 服务多个 panel 作用域；agent 自报探测目标 host/port。

**没有关掉**：增量传输。同一个版本号下以后可以加 patch 段，**字节可以以后买回来**。

**顺带关掉洞 6，不留含糊**：凭据吊销**不买第二条通道**，用**版本闸** —— 订阅渲染与本地凭据切换以 `applied.roster.version >= N` 为闸。`sync.go` 的 `RotateClientUUID` 是一个 happens-before（`UpdateClient` 成功之后**才** `ownership.UpdateUUID`），长轮询只把窗口从 60s 缩到 <1s，买到的是**延迟**不是**顺序**，而 25 秒和 0 秒在授权/吊销上不是同一件事。版本闸把洞 6 变成洞 1 那个版本号的一个消费者，§7.5 不动。

### 8.6 卡在哪（是前置条件，不是不做决定的借口）

1. **期望文档必须只有一个铸造者**（ADR 0025 债务 1b）。今天不存在装期望客户端状态的本地行，三处各自现算且**算出的值不同**。且 `node.go` 推的是管理员表单原文，今天安全**只因为** xui 适配器做 RMW。必须在第一版协议实现前还掉，**但不阻塞本决定落纸**。
2. **客户端行身份必须稳定**（债务 1c）—— 这是 `cli_{psp_clients.id}` 成立的唯一条件。`psp_client_repo` 的 Upsert 按 `(panel_id, email)` 查，email 跨 1↔2 分区边界 re-key → 新行、新 id、计数器从 0。**解锁条件已经确定：把 email 从唯一键降级成可变属性，行按稳定身份查。** 债务 1c 剩下的全部工作就是这一句。
3. **落库位置不存在**：PSP 今天没有「agent / 主机」这一行。需要一张 agent 表 + 每流一行的 `(applied_version, applied_etag, pending_since, last_seen)`，以及把 `psp_client_inbounds.provisioned` 那个 bool 换成 `(state, applied_version, first_failed_at)`（ADR 0024 明写的开工闸门）。
4. **版本差不能当存活信号**：失联判定走 `last_seen` + 显式陈旧度上限，独立于任何版本号（§7.5：面板侧独立判定）。
5. **§4 的成本表要按真实拓扑重算**：5 台 agent、合计 9 个监听器、T=120s。9 是 `(panel_id, inbound_id)` 行数，不是主机数；三份提案的成本表都用了不存在的 9 台机群。方向性结论不受影响（**94% 的收益来自节点拨出，不来自文档拆分——这一条不许记在文档拆分的账上**），但数字要重算。

### 8.7 下发延迟：拨出不等于慢（2026-09-09 定）

§8.3 说「交互延迟最长一个心跳周期」并给了 `next_poll_s`,那是**把账记下来**,不是**把账付掉**。
这一节付掉它。

**先量化。** T = 120s（§4 的生产实测值）。纯周期性拨出下：

| | 延迟 |
|---|---|
| 改动刚好落在节点签到之后 | **T = 120s** |
| 平均 | T/2 = 60s |
| 多步交互（探测 Reality → 取结果 → 装 core → 确认） | **4×T ≈ 8 分钟** |

**和今天比是退步，而且退在有人等的那条路上。** 今天 PSP 自己拨，下发是**秒级**的——
`node.go` 的 `provisionNodeMembersInBackground` 注释自陈目标是
「clients appear within seconds, **not on the next sync-task tick**」,并且为此专门把它挪出请求线程
以免撞上 30s HTTP 超时。**秒级不是巧合，是当初写下的目标。** 纯周期性拨出把它变成最坏 120s。

**但「延迟」这个词底下是四件不同的事，混着谈会得出错的结论：**

| 什么变化 | 有人在等 | 120s 能不能接受 |
|---|---|---|
| 新增用户 / 新订阅生效 | **是** | **不能** |
| 交互式操作（探测、装 core、升级 agent） | **是，盯着转圈** | **更不能**，多步累加 |
| 配额超限停用 | 否 | 能——他已经超了 |
| 吊销被盗凭据 | 是 | 想快，但**正确性不靠快**（见下） |

第四行必须单独说清楚，否则会被当成「所以延迟是安全问题」:**吊销的正确性由版本闸保证，不由速度保证**
（§8.5）。PSP 在 `applied.roster.version >= N` 之前不会把订阅切到新凭据，所以不会出现
「以为吊销了其实没有」。快慢影响的是**暴露窗口**,不影响**顺序**。这两件事在 §8.5 已经被刻意分开,
这里再钉一次。

**解法不是翻转拨号方向，而是把周期缩短——但缩到多短，是一笔要算的账。**

| 轮询周期 | 每节点 | 全车队（5 台） | 500 台的机群 |
|---|---|---|---|
| 5s | 17,280 次/天 | 1.00 req/s | 100 req/s |
| 15s | 5,760 次/天 | 0.33 req/s | 33 req/s |
| **30s** | **2,880 次/天** | **0.17 req/s** | **17 req/s** |
| 60s | 1,440 次/天 | 0.08 req/s | 8 req/s |

**5 秒被否掉了，理由不是 CPU。** 1 req/s 对 PSP 确实无感，但：

- **它是为几件事敲了一万七千次门。** 25 人的部署里配置变更一天也就几次，其余 17,000 次轮询
  全部返回「没变化」。
- **日志噪声是真成本。** 每天八万多条访问日志盖在真实流量上面，出事时最需要看的东西被埋了。
- **它不适合当一份公开协议的默认值。** §0.5 把「别人也能用」当成目标，而 500 台机群 100 req/s
  的心跳基线会劝退。**默认值是给不读文档的人用的**,它必须在别人的规模上也说得过去。

**定为 30 秒。** 延迟 ≤30s,代价降到一天 2,880 次，500 台时也只有 17 req/s。

**它欠下的那点延迟，由 `next_poll_s` 还，而不是由基线还：**

- **多步交互**（探测 → 取结果 → 装 core → 确认）:PSP 一旦手上有排队的活，就在响应里说
  「1 秒后再来」。于是代价从 `N×30s` 降到 `30s + (N-1)×1s`。协议里这个字段已经有。
- **头一跳仍然要付满一个基线**——PSP 学到变更时节点刚走，这是 `next_poll_s` 治不了的。
  对此 PSP 可以开**热窗**:管理员正在某个节点的页面上操作、或某个用户刚拉过订阅时，
  把那台 agent 的 `next_poll_s` 压到 2 秒并维持两分钟。管理员点下按钮时那台 agent 早已在快轮询。
  **这整套是 PSP 侧的策略，不进协议**——朴素实现永远返回基线也能跑，以后再变聪明。

**长轮询（PSP 把请求挂住）仍是备选，不是首选。** 延迟接近 0,但挂起请求会撞上路径上的反向代理：
本项目部署路径上有 Cloudflare，其源站响应超时约 100 秒。挂 60s 技术上能过，
但那是**在别人的超时阈值边上跑**。真出现需要亚秒下发的需求（例如实时封禁）再启用，
那时协议不用改，只改传输行为。

**翻转回 PSP 拨入**能解决延迟，但要把节点拨出的**全部**收益还回去（公网可达 + 每台节点一张证书 +
NAT 后面的节点直接做不了）。**不划算，不要走这条。**

**代价（唯一一笔，必须付）：轮询节奏与报告节奏必须解耦。** 轮询比上报勤，全量枚举不该跟着轮询走。
所以 `NodeReport` 增加一个 `partial` 标记：轻量轮询只带 `have`（三个流各自持有什么）与 `issues`,
省掉 `objects` / `clients` / `subjects`;全量报告按自己的节奏发。

**它省下多少，取决于两个周期的比值。** 默认 30s / 60s 下是一半；有人为了低延迟把轮询调到 5 秒时，
省掉 11/12。**所以这个机制的价值随「谁把轮询调快」而增长**——正是需要它的人得到它。

**两个周期，两个默认值，都可调（2026-09-09 定）：**

| | 字段 | 默认 | 它决定什么 |
|---|---|---|---|
| **轮询周期** | `next_poll_seconds` | **30 秒** | 配置多快到达节点（PSP 可临时压到 1~2 秒） |
| **全量上报周期** | `full_report_seconds` | **60 秒** | 车队计数拼图能有多陈旧 |

**60 秒不是随手取的整数，它有实义：这个周期是 `overburn_headroom_bytes` 的上界。**
聚合值的新鲜度只等于其中最旧的那份报告（§8.4），所以**把它减半，就把一个客户端在全车队上
能悄悄超用的量减半**。从 120s 改成 60s 买到的正是这个。

**这也正是它该可调的理由**——旋钮换的是一笔真实的账：**带宽 ↔ 允许超用多少**。
在意配额严格的部署调低（例如 15 秒）并多付带宽；带宽按量计费的部署调高（例如 300 秒）
并接受一个更松的上界。**不是「能配就配一下」,是这个数字有两侧代价。**

**两个周期都由 PSP 在响应里下发，节点不存策略。** 与 §5 一致：节点不决定任何配置，
周期也是配置。管理员在 PSP 的设置页改，下一轮就生效。

**另外给 PSP 一个立即索要的开关** `want_full_report`:PSP 重启丢了缓存、管理员点刷新、
上一份报告对不上账——不必等周期。

**缺失方向照旧失效于安全侧**：`full_report_seconds` 为 0 或缺失 → **每轮都发全量**。
多发浪费带宽，是可测量的、响亮的；少发会让流量记账**静默停止**,而屏幕上什么都不变。
一个缺失的数字不许让计数器安静下来。判定规则写成 `protocol` 包里的
**`ShouldSendFull` 一个函数**——PSP 必须能精确预测 agent 会怎么做，
在面板侧再抄一份就是这次拆仓要避免的那个两份真相源。已用变异验证
（把缺失读成「不发全量」会让 `TestMissingReportIntervalReportsMoreNotLess` 变红）。

这个标记的**零值必须落在严格那一侧**,所以字段按**例外**命名（`partial`,不是 `full`）:

| 误判方向 | 后果 |
|---|---|
| 轻量报告被当成全量 | 每个客户端都「缺席」→ **响亮、可见、自愈**的假警报 |
| 全量报告被当成轻量 | 一个真正丢失的客户端被**静默吞掉**,检测器停止检测而屏幕上毫无变化 |

第二种正是 §7.3 记录的上游翻车方式，所以零值必须落在第一种上。`Full bool` 在调用点读起来更顺，
**但它是错的**——它的零值会给每一次静默省略发许可。`protocol_test.go` 的 `TestPartialReportFailsSafe`
守着这一条，并且已用变异验证（给字段加上 `omitempty` 会让它变红）。

收到 `partial` 报告时 **PSP 对缺席字段一律不更新**：不得把空 `objects` 读成「全部收敛」,
不得把空 `clients` 读成零流量。

**协议端点不变**（还是 `POST /v1/node/sync`）,**agent 的同步循环也回到最朴素的形态**:
发请求、拿响应、按 `next_poll_s` 睡一会儿。「请求超时 = 故障」这个直觉在快轮询下**是对的**,
不需要长轮询那套「超时是正常的」的反直觉语义——这也是选第 1 档的一个附带收益：
它让 agent 的核心循环、重连策略与失联判定三者保持同一套直觉。

#### 「本地一有新连接就立刻拉一次」——方向反了

这个方案会被提（本次就提了一次），所以把推翻它的推理写下来，免得下一个人重走一遍。

想法是：节点平时慢轮询，**但本地一有用户连上来就立刻拉一次**,用事件代替轮询。
「事件驱动优于轮询」这个直觉是对的——**但这个事件触发得太晚。**

要修的延迟是「管理员在 PSP 新建用户 → 这个用户的配置多久到达节点」。按时间顺序走一遍：

1. T=0 管理员建号
2. 配置还没到节点
3. 用户去连节点 → **节点不认识这个凭据，core 直接把它当噪声丢掉**
4. 于是「有新用户连上来」这个事件**从来没有发生过**

触发条件依赖的，正是它本该促成的那件事。**对已有用户它会触发，但那时没有急事要取；
对新用户它有急事，却触发不了——触发时机与需求恰好反相关。**

**那用「有人拿不认识的凭据来敲门」当触发器？** 不行，两条各自足够：

- **它是攻击者可控的触发器。** 任何人往节点端口发垃圾都能让节点去敲 PSP，一条免费的放大通道。
  加限流就把延迟加回来了，而限流阈值正好是攻击者用来**淹掉合法触发**的那个东西。
- **core 刻意不暴露这个事件。** VLESS / Reality 的抗探测设计就是让失败握手与噪声不可区分，
  失败流量被静默转给 fallback。**没有一个干净的「未知凭据」信号可取**——能取到就说明抗探测坏了。

**根本原因写在这里，因为它对所有同类方案都成立**：知道「有东西要取」的只有 PSP，节点无从知道。
而 PSP 要告诉一个 NAT 后面的节点，只有两条路——**挂住一条连接，或者节点问得足够勤**。没有第三条。
任何「让节点自己猜什么时候该问」的设计，都是在用节点手上没有的信息做决定。

**注意这条不依赖「轮询很便宜」。** 轮询是有成本的——正是为了这个成本，基线才从 5 秒退回 30 秒。
但那笔成本的正确解法是**把基线调稀 + 让 PSP 用 `next_poll_s` 在需要时加速**,
因为决定何时加速所需的信息只有 PSP 有。用节点本地事件去省这笔钱，省下的是同一笔钱，
换来的却是一个在需要时不触发的触发器。

#### 但这个想法在**反方向**上成立，而且要做

本地事件是**上报**的好触发器——节点确实知道自己身上发生了什么，这正是它比 PSP 有信息优势的地方：

- 某个用户刚撞上配额 → **立刻**上报，不等下一个整周期
- core 崩了 / 某段配置被拒 / 出现无法自行调和的状态（`Issues`） → 立刻上报

这和运营商在基站那一侧的分法是同一个：**告警走常连接立刻上报，性能计数器攒着 15 分钟一批。**
不同的事该有不同的节奏，而节奏由**谁先知道**决定。

`NodeReport` 已经有 `Issues` 这一格，缺的只是一条行为规则：**产生 Issue 时不等周期，立即发一次报告。**
用 `partial` 报告发即可，成本可以忽略，协议不用改。

**一件缩短周期也治不了的事**：节点离线时任何拨号方向都送不到。区别只在**谁先知道**——
PSP 拨入时「这次没到」的证据由 PSP 的传输层产生，节点拨出时它变成故障方的自述，
所以才有 `last_seen` 的独立判定（§7.5）。这是**检测**问题，不是**延迟**问题，不要混。

## 9. ⏸ 其余未决（都不影响协议）

- 交付形态的细节（单文件二进制 + Docker 已定，见 ADR 0024 §4；构建矩阵与自升级机制未定）
- core 选型：xray 与 sing-box 都支持（已定，ADR 0024 §2.5）；配置生成的抽象层未定
- 证书：沿用 PSP 的托管证书下发，还是 agent 自己签
- 自注册：复用现有的一行安装机制（ADR 0024 §5）；方向已定为节点拨出（§7.5），凭据形状随之确定为「注册时铸 agent_id + 长期凭据」,细节未写


## 10. 下一步

> **可交接的展开版见 [`psp-node-plan.md`](psp-node-plan.md)**——每一项带完成判据、涉及文件、依赖顺序，以及一份「十条不要」。这一节只留骨架。

按依赖顺序，不是按难度：

1. ~~拍板仓库名~~ **已定：`Passwall-Node`（§0.5）。**
2. ~~建仓库，把 §8 变成 Go 类型定义~~ **已做：<https://github.com/KazuhaHub/Passwall-Node> 的 `protocol/` 包。** PSP 用 module 引它这一半要等第 4 步——没有 import 的 `require` 活不过一次 `go mod tidy`（§0.5）。
3. **还掉 §8.6 的三条硬前置**（期望文档的单一铸造者、客户端行身份稳定、agent 表）。这三条是 PSP 侧的工作，与 agent 仓库并行。
4. **`psp` 适配器最小实现 + 对着一个真 agent 的契约测试**（§0.5 约束 3）。

**不要先写 core 管理**。§1 已经划定：agent 的职责是「接收配置 → 生成 core 配置 → 管进程 → 上报计数器」,而前三步的价值全部依赖第 4 步的契约测试能跑起来。
