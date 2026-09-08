# PSP 自研节点后端（agent）设计

- **状态**：编写中。**协议层尚未定稿**，等 [ADR 0024](adr/0024-psp-native-node-backend.md) 的前置作业（上游协议调研）。
- **决策依据**：[ADR 0024](adr/0024-psp-native-node-backend.md)（2026-09-08 所有者已决定自研）、[ADR 0025](adr/0025-push-pull-decision-rule.md)（推/拉判据）、[data-plane-plan.md](data-plane-plan.md) Phase 2/3
- **相关代码**：`internal/ports/xui.go`、`internal/adapters/panel`、`docs/panel-adapters.md`、`docs/inbound-ownership.md`

## 这份文档写到哪、为什么停在那

**能从 PSP 自己的代码推导出来的部分现在就写死**——它们不依赖上游长什么样，调研结论不会推翻它们。

**协议的线上形状（端点、载荷、认证、谁拨号）留空**，因为 ADR 0024 给自己立了一条规矩：先读完 3X-UI 3.7.0 自带的 master/node 协议再定，「不查就自己造，风险是重复造一个明年被上游做得更好的东西」。决心不废除这条约束。

下文每个「⏸ 等调研」的小节都写明了它在等什么。

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
- **代价说在前面**：「别人也能用」不是免费的——它意味着协议要**对外文档化并保持稳定**，破坏性变更要走废弃周期。这是一项长期成本，值得当作选择来接受，而不是事后才发现。

## 1. 边界：agent 要做什么，不做什么

PSP 的上游访问走的是一层与厂商无关的适配器（`xui_panels.kind` → `adapters/panel.Registry` → 构造函数，`Pool` 按面板 ID 路由）。所以自研后端**是新增一个 `PanelKind`,不是新架构**——节点管理、客户端下发、流量轮询、订阅渲染、异地并发检测全部不动。

**PSP 已经拥有的（agent 不要重做）：**

| | 依据 |
|---|---|
| inbound 配置的真相源（自有 DB 存完整配置、订阅渲染零回源、reconcile 反向下发） | [inbound-ownership.md](inbound-ownership.md) |
| 客户端模型（`psp_clients` + `psp_client_inbounds`，按 (user, panel, credClass) 分区） | `internal/domain/pspclient.go` |
| 订阅渲染、模板、规则集 | `internal/service/render` |
| 用户体系、分组、SSO、配额与到期的判定 | `internal/service/user` |
| 期望态与重试（sync task 队列） | `internal/service/user` runUserTask |

**agent 的职责因此很窄**：接收 PSP 已经拥有的配置 → 生成 core 配置 → 管进程 → 上报计数器。**UI、用户体系、订阅、Telegram bot 一律不需要。**

## 2. agent 必须实现的接口（已定，来自 PSP 代码）

`ports.PanelClient` —— **21 个必需方法**，任何 `PanelKind` 都要实现：

**inbound（7）**
`ListInbounds` · `ListInboundsSlim` · `GetInbound` · `AddInbound` · `UpdateInbound` · `DelInbound` · `SetInboundEnable`

**client 单体（5）**
`AddClient` · `UpdateClient` · `DelClientByEmail` · `GetClient` · `ListClientInbounds`

**client 批量与挂载（8）**
`AddClientToInbounds` · `AttachClient` · `DetachClient` · `BulkAttach` · `BulkDetach` · `BulkCreateClients` · `BulkDelByEmail` · `BulkSetEnabled`

**状态（1）**
`GetServerStatus`

**可选能力接口（8 个，按需实现，`CapabilityProvider` 自报）：**

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

按 §0，这两件事已经分开了。这 21 个方法里有相当一部分**是被 3X-UI 的模型逼出来的**：客户端按 email 全面板唯一、inbound 挂载是一张 junction、整结构 Save 语义、批量接口是为了少触发 xray reload——**其中两个（`BulkSetEnabled`、`BulkDetach`）连生产调用点都没有。**

所以上表的用途是**给 `psp` 适配器当验收清单**（它必须让这 21 个方法都能工作，PSP 的 service 层才不用动），**不是给 agent 协议当规格**。

agent 协议要回答的是另一组问题，从我们自己的领域出发：

- 一个**节点**要接收什么才能提供服务？（PSP 已拥有的 inbound 配置 + 该节点上的客户端集合）
- 一个**客户端**在我们的模型里是什么？（`domain.PSPClient`：按 (user, panel, credClass) 分区，凭据由 UUID 派生，挂在若干 inbound 上）
- 节点要回报什么？（累计计数、在线 IP、**已应用的配置版本**、进程健康）

这三个问题的答案和 3X-UI 的方法表没有对应关系，也不应该有。

**待 §7 协议定稿时给出映射表**：agent 协议的每个操作 → `psp` 适配器如何用它兑现那 21 个方法。**哪些方法兑现不了、需要 port 让步，就是第 3 步重塑 port 的清单。**

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

⏸ **等调研**——因为 Q2b 的答案取决于上游那套协议是否可复用（若可复用，拨号方向可能已被它定死）。但下列几条与调研无关，现在就能定：

- **累计计数 vs 增量**和推/拉**正交**。PSP 现在读的是面板的累计值、自己做单调差分（`LastRawXxx` 基线），agent 应当继续上报**累计值**——可重放，丢一轮自愈。
- **遥测若改成 agent 主动推，必须是「数字没变也照发」的心跳。** 否则「死掉的上报器」和「没人用的空闲节点」长得一模一样——这是本仓库反复出现、也反复设防的失效模式。
- **单一拨号方今天在偷偷提供四样东西**，翻转前必须逐条给出替代品：对远端一行的互斥（`clientWriteLocks` 是包级锁）、`UpdateInbound` 的读-改-写窗口、失败域独立的第二观察者（健康探测）、以及**「这次没到」这个证据由谁制造**（PSP 拨号时它由 PSP 的传输层制造；改成 agent 推之后，它变成被观测组件对自己是否故障的自述）。

## 7. ⏸ 协议形状（等调研）

**在读完以下四份源码之前不写：**

| 源码 | 本地路径 | 版本 | 要回答 |
|---|---|---|---|
| 3X-UI | `/home/user/mhsanaei/3x-ui` | v3.7.0 | 自带的 master/node 协议（`/inbounds/pushClientTraffics`、`node-sync` token scope、节点 mTLS、`/nodes/history/*`）成熟度如何？可复用还是绑死在它自己的 master 上？ |
| S-UI | `/home/user/alireza0/s-ui` | — | client 模型与 3X-UI 的差异，决定我们的模型要覆盖什么 |
| XrayR | `/home/user/xrayr-project/xrayr` | `5ceba41` | 机场生态的经典形状：节点拉配置 + 推用量 |
| V2bX | `/home/user/wyx2685/v2bx` | v0.4.1 | XrayR 的后继，多 core |

产出应当回答：**协议里哪些能借、哪些必须自己造、哪些上游已经做得比我们会做的好。**

## 8. ⏸ 其余未决

- 交付形态的细节（单文件二进制 + Docker 已定，见 ADR 0024 §4；构建矩阵与自升级机制未定）
- core 选型：xray 与 sing-box 都支持（已定，ADR 0024 §2.5）；配置生成的抽象层未定
- 证书：沿用 PSP 的托管证书下发，还是 agent 自己签
- 自注册：复用现有的一行安装机制（ADR 0024 §5），但 §6 的拨号方向会影响凭据形状
