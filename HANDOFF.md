# 自研节点后端项目 — 交接文档

> 这份文档是**入口**,不是全部内容。它告诉你：项目在哪、决定了什么、下一步做什么、
> 以及三份详细文档各自回答什么问题。
>
> **它刻意不复述那三份文档**——一份真相源，是这个项目从头到尾在守的规矩。

## 0. 三十秒版本

PSP 今天通过 3X-UI / S-UI 这两个第三方面板管理节点。这个项目**自研一个节点后端**替掉它们，
理由和判据在 ADR 0024。**协议、PSP 原生适配器与生产同步路由已合流；C2 真 agent 契约测试已通过，
Passwall-Node 的 Xray / sing-box 生产 daemon、精确 core 目录、遥测、离线执法、六平台发布与 Docker
均已接通，PSP 可分别选择已核验的 engine + exact version。**

| | 在哪 |
|---|---|
| **做什么、什么顺序、完成判据** | [`docs/psp-node-plan.md`](docs/psp-node-plan.md) ← **接手先读这份** |
| **为什么这样定**（每个决定的推翻记录） | [`docs/psp-node-agent.md`](docs/psp-node-agent.md) |
| **该不该做**（判据、成本、开工闸门） | [`docs/adr/0024-psp-native-node-backend.md`](docs/adr/0024-psp-native-node-backend.md) |
| **推还是拉怎么判**（以及 PSP 今天为何没得选） | [`docs/adr/0025-push-pull-decision-rule.md`](docs/adr/0025-push-pull-decision-rule.md) |
| **协议类型 + 节点侧交接** | <https://github.com/KazuhaHub/Passwall-Node> 的 `HANDOFF.md` |

## 1. 已经定了的（不要重开）

**架构**:自研后端是新增一个 `PanelKind`,**不是新架构**。节点管理、客户端下发、
流量轮询、订阅渲染、异地并发检测全部不动。

**方向**:节点主动连面板，但面板说了算。

| 问的其实是 | 答案 |
|---|---|
| 谁拨号 | **节点 → 面板**（不要公网 IP、不开端口、不要证书、NAT 后能跑） |
| 谁决定配置 | **面板**。`NodeReport` 结构上就没有字段能陈述期望配置 |
| 配置怎么过去 | 一次往返，面板在响应里下发；**周期 30 秒**,PSP 可临时压到 1~2 秒 |
| 用量怎么上来 | 累计值，**数字没变也发**;全量枚举 **60 秒**一次 |
| 谁判断节点活着 | **面板独立判定**（`last_seen`）,节点无权自证 |

**两个默认值**:轮询 **30 秒** / 全量上报 **60 秒**,都可调，都由 PSP 下发。
两个数字各有实义，改之前读 §8.7——尤其是 60 秒那个，它是「用户最多能偷跑多少」的上界。

**协议形状**:两份带版本的文档（`config` 监听器 / `roster` 名册）+ 一段指令流（`directives`），
作用域一台 agent,主键用 PSP 铸造的行 id。六个洞全部关闭（§2.3 → §8）。

**永久关闭的选项**见 §8.5。重新打开需要一次破坏性协议升级，不是随手改。

## 2. 下一步：三条轨道

详细的完成判据在 [`docs/psp-node-plan.md`](docs/psp-node-plan.md),这里只给骨架和依赖。

```
A1 ─→ A2 ─┬─→ A4
      A3 ─┘        ╲
                    ╲
B1 ─→ B2 ────────────╫─→ C1 ─→ C2 ─→ B3
                    ╱
A5（随时，独立）────╯
```

**轨道 A（PSP 侧，关键路径，现在就能开工）**——ADR 0024 明写的开工闸门：

- **A1 客户端行身份稳定（已完成，2026-09-09）**。`psp_clients.id` 现在是唯一稳定身份；
  email 已降级为可变属性，1↔2 分区与域名变更都会按旧挂载延续行 id 和流量基线。
- **A2 期望文档只有一个铸造者（已完成，2026-09-09）**。`clientdoc` 统一铸造客户端定义，
  `nodesync` 从同一事务快照铸造 config / roster / directives。
- **A3 agent 表 + 每流状态（已完成，2026-09-09）**。挂载 bool 已替换为四态、应用版本、
  首次失败时间与**最后确认凭据快照**；新 roster pending/rejected 时订阅仍使用旧快照，只有 read-back
  确认后才切换。节点 Issue 进入独立持久化收件箱，重复报告更新 last-seen，管理页可人工确认已查看；
  全量报告缺对象由 PSP 生成 `report_missing_object`，不再静默吞掉。
- **A4 desired / observed 列拆分（已完成，2026-09-09）**。报告路径只能写 observed 窄接口。
- **A5 §4 成本表按真实拓扑重算（已完成，2026-09-09）**。

**轨道 B（Passwall-Node 仓库，第一天就能并行）**:见该仓库的 `HANDOFF.md`。

**轨道 C（合流，已完成，2026-09-09）**:`internal/adapters/pspnode` 已接入现有 pool；
`TestLive_RealNodeAgentContract` 已启动 sibling 仓库的真 agent，完成两轮 HTTP/SQLite/apply/report 验收。
同步边界统一调用协议包校验，有限额但计数未知的客户端默认关闭；`node_poll_seconds` 与
`full_report_seconds` 均由管理员配置并随响应下发，信封中的新鲜度与剩余授权按全 agent 统计。
config/roster 覆盖度按本机闭包精确校验，directives 覆盖度则保留全车队分母语义；未定义的
任务通道现已从“未知即拒绝”推进到独立的 durable coordinator：PSP 只向本轮同时声明
`task.execution.v1` 与 `task.<kind>` 的 agent 下发，`queued` 第一次成为 `offered` 时临时把下一轮压到
1 秒，之后稳定重发同一 `(id, kind, input_sha256, args)`，不持久化或猜测 `running`。节点回报的
`succeeded` / `failed` / `indeterminate` 结果按整批事务接收；同一终态可重放。身份完整但找不到
任务的结果，或身份匹配但备份中仍为 queued 的结果，完整持久化到独立 quarantine；后者同时
关闭 dispatch，却不制造执行终态。跨 agent、身份不符或终态冲突仍令本轮非 2xx。
这里只承诺 **task-results 整批原子**：正式终态、隔离证据与 dispatch 关闭一起提交；报告后续的
Issue/stream/observed 写入仍是多个事务，任一失败依赖节点保留 immutable outbox 并重放来收敛。
2xx 只表示证据已可靠接收，不表示隔离结果已经过人工核对或成为任务终态。
全量计数的新鲜度只读 PSP 的实际接收时间，不信任 agent 自报时钟；缓存过期会令
`want_full_report=true`，有限额客户端在刷新前保持关闭。
双向同步载荷共用协议包的 16 MiB 上限；PSP 只接受自己确实铸造过的 applied epoch/version/ETag
组合，节点不能用未来坐标或同版本错摘要污染收敛状态。协议包同时约束响应调度值，后台设置校验
直接引用同一常量，避免控制面和 agent 各自维护不同上限。配额 pending delta 还会重新核验
agent→panel→client 所有权，越权 client key 与已退役 agent 缓存都不能影响别的面板用户。
任务基础的跨仓模块发布闸已于 2026-09-11 完成：Passwall-Node PR #6 的 revision
`83a91c39304e` 已进入 main，PSP PR #38 将依赖更新到
`v0.0.0-20260912005623-83a91c39304e`。关闭父目录 `go.work` 后，PSP 全量 Go 测试、vet、
关键路径 race、C2 真 agent 契约测试通过；GitHub CI 的 SQLite/MySQL/PostgreSQL、Web 与
六平台交叉编译均通过。

管理端现在可直接创建 `panel_type=psp`：PSP 在一个事务中建立 panel、agent 和三条流，返回一次性的
agent ID / Bearer 凭据 / sync endpoint；数据库只存 SHA-256。原生节点只能编辑名称和备注，旧凭据可
立即吊销并轮换，新值同样只显示一次。删除采用 fail-closed 规则：仍有节点/客户端，或空 config/roster
尚未由 agent 精确确认时，不能先删掉认证身份而留下一个继续服务、却再也接管不了的 core。

core 选择已改成声明式 desired state：PSP 原生节点从 Passwall-Node `corecatalog/` 同时选择 engine
和精确版本；`latest`、未知 engine 和未列入版本一律拒绝，受限版本要求管理员二次确认。PSP 分开显示
desired 与 agent 最后报告的 observed engine/version，切换完成前不把“已接受意图”伪装成“已运行”。
选择器展示同一份兼容说明、REALITY 客户端矩阵和结构化实测证据。Xray 当前默认 `26.6.27`；`26.7.28` 经
`minClientVer=0.0.0` 转换后 Xray/Mihomo/sing-box 均实测通过；`26.9.9` 的 Mihomo 输出固定
`chrome + support-x25519mlkem768=true`，sing-box 订阅会省略该 REALITY 节点而不是下发一个死节点。
sing-box 当前核验 `1.14.0`，原生编译 VLESS、VMess、Trojan、Shadowsocks-2022，并通过真实 REALITY
握手矩阵；切换 engine/version/binary/启动参数是一个可回滚的原子部署身份。

订阅默认规则已把 HTTP/3 常见的 `UDP/443` 与其他 UDP 分开：`⚡ QUIC控制` 默认委托
`🎮 UDP控制`，用户也可分别选节点、直连或拒绝。规则位于私网/LAN 直连之后，不包含“中国直连、
其他拒绝”这类地区假设。sing-box 对 `AND(NETWORK=UDP,DST-PORT=443)` 生成同等路由；其不支持
`PASS`，因此省略该匹配继续向下，绝不映射为直连。详见 ADR 0030。
旧官方模板和规则集两个文件都未修改时，seeder 按旧内容 SHA-256 自动升级；任一文件已自定义时
两者都保留，避免半升级覆盖用户意图。

## 3. 还欠着的账（都不阻塞开工，但别忘了）

- **`grant_ceiling` 的默认值**（§8.4）。断网时节点靠手上的 `headroom` 继续执法，
  但今天发的是**全额剩余**,于是长时间断网最坏可跑 `P × 剩余配额`（P 实测 4.0）。
  公式和旋钮已写进文档，**默认值待测**——取决于单客户端 60 秒吞吐分布，那个分布从未被测过。
  第一版按不设上限上线（即今天的行为），同时记录 `overburn_headroom_bytes` 的分布。
- **可预知到期的协议形状与执行均已闭合**：权威链是
  `User.PushExpireTime → PSPClient.DesiredExpiryTime → Client.ExpiresAtMS`；Node runtime 在
  PSP 断线时仍按绝对截止时间本地停用，不等 roster 条目消失。真正无法预知的是断线之后
  才发生的管理员/策略撤销；不增加租约或第二通道就只能等下次同步，属于 §9 的生产取舍。
- **任务 ID 铸造器已落地，但不表示 restore gate 已关闭**：`idgen.NewTaskIDMinter` 使用 fresh
  192-bit issuer + 不回绕的 uint64 CAS 序列；它尚未接入生产任务入口，不重写任何已有任务 ID。
  机制与边界见 [ADR 0031](docs/adr/0031-native-task-id-incarnations.md)。结果证据 quarantine 已实现；
  后续双端 expiry、retention 与完整恢复演练见 [ADR 0032](docs/adr/0032-native-task-lifecycle.md)；其中
  离线对账/备份恢复窗口/完整结果保留期默认 **30/30/90 天**已获所有者确认，并已接入管理员
  全局设置（各 1–3650 天整数，完整结果保留不得短于另两个窗口）。修改不改已有 task/quarantine；
  尚未接入不可变任务策略快照或清理器，不能将可配置策略当作已实现/已测量的恢复保障。
- **任务 #49**:异地并发被标记的账号该怎么处理。停在证据不足上，
  v1 的 `ip_shadow` 影子执行就是为了给它攒证据。
- **任务 transport / durable result state 已闭合，真实任务尚未开放**：独立的
  `node_agent_tasks` 仓储覆盖 `queued / offered / succeeded / failed / indeterminate`，不会复用面向
  第三方面板重试的 `sync_tasks`。RealityProbe 管理 API/UI 与 agent 自升级仍未实现；当前没有任何
  生产入口创建真实任务。
- **per-agent active task quota 已闭合（2026-09-11）**：`CreateOrGet` 在 owner-lock 事务内同时限制
  `queued + offered` 为 **256 行 / 16 MiB 原始 args**；这相当于四个满额 offer window，是不可经
  UI settings 动态放大的 compiled hard cap。创建、下发、完成与 agent 删除共用同一 owner lock，
  不依赖缓存或可漂移的计数列。只有新插入占用 quota；满额时 exact task/idempotency replay 仍成功，
  身份冲突仍是 `ErrConflict`，新工作超限则为 `ErrResourceExhausted`（HTTP 429）。三种终态均释放
  active quota，但 tombstone 继续保留。这里限制的是原始输入，不是 JSON/base64 或数据库页占用。
- **恢复后的结果收存已闭合（2026-09-11）**：quarantine 以 `(agent_id, task_id)` 为主键，完整保存
  canonical wire result、SHA-256、原因和首次/最近接收时间。它是未核实证据，不是任务表；一个
  agent 不能靠猜 TaskID 占用其他 agent 的身份。相同 payload 重放成功，冲突不覆盖旧证据；同
  agent 的隔离 ID 不得用于新建任务。后来恢复的匹配 queued/offered 行只关闭 dispatch，不自动
  晋升隔离证据为终态；即使恢复行尚无关闭标记，已有隔离证据也会阻止 Offer。独立 hard cap
  为每 agent **256 行 / 16 MiB canonical JSON**，
  满额整批回滚并返回 HTTP 429，绝不先 ACK 再丢结果。关闭 dispatch 的 queued 任务仍占 active
  quota；有隔离证据的 agent 不可删除。暂不提供人工核对 API/UI 或证据清理器。
  `TestLive_RealNodeTaskEvidenceReceipt` 覆盖真实 Node worker/journal/outbox/HTTP 的丢行与丢 ACK
  路径；最后一步为真实 Processor 的本地原响应重放，不冒充完整备份恢复演练。CI 的
  `node-contract` job 从 `go.mod` 固定、`go.sum` 校验过的 Node 源码副本运行它与 C2 契约测试，
  禁用 `go.work`，不追另一仓库的浮动 main。
- **开放第一个真实任务前仍有三道硬门**，不可用“随机 ID 冲突概率很低”替代：
  1. 定义并实现双端 expiry：PSP 过期后不再 offer，Node 在副作用开始前也必须拒绝过期任务；已经
     开始后失去确定结论要回报 `indeterminate`，不能伪装为普通失败；
  2. 定义 terminal tombstone 保留期与清理器，保留期必须覆盖 Node outbox 重放、最长离线时间及
     备份恢复窗口；现有 quarantine 提供收存与 backpressure，不等于 retention 已闭合；
  3. 定义 DB restore 后的 task identity epoch / ID 禁止复用窗口。恢复旧备份既可能遗失已完成
     tombstone，也可能复活曾经下发的任务。当前单边丢行/丢 offer 标记的接收测试不替代完整
     restore-finalize、Node/双端回滚演练；全部通过前不得暴露有副作用的 kind。
- **Node v8 的回滚边界**：v8 SQLite 增加 durable task journal。旧 v7 binary 看到
  `PRAGMA user_version=8` 会以 “newer than supported” 直接拒绝启动；它不会只读运行或误写数据库。
  回滚必须同时恢复 v7 数据库备份（并处理上述 task identity 窗口），不能只替换二进制。
- **任务基础的跨仓发布闸已完成（2026-09-11）**：Passwall-Node PR #6 先合并，PSP PR #38 再更新
  pseudo-version，并脱离父目录 `go.work` 通过上述测试与三库/六平台 CI。后续 expiry 等新的
  additive wire revision 仍必须按同一顺序先发布 Node、更新 PSP 依赖并重跑；本地 workspace
  通过不能替代这道闸。
- **`Passwall-Node` 的 licence**（README 写着 TBD）。

## 4. 这个项目的三条底线

写在最后，因为它们解释了上面很多看起来啰嗦的决定：

1. **一份真相源。** 协议类型只住在 `Passwall-Node`,PSP 用 Go module 引。
   在 PSP 里复制一份，就是把这次自研要摆脱的问题在自己内部复刻一遍。
2. **机制优于纪律。** 能让编译器或类型守住的，不要写成「记得调用某个校验函数」——
   本仓库反复证明后者活不过三个月。
3. **「不知道」永远不许长得像「没问题」。** 三态编码、含零值的全量枚举、
   缺席不解释成无限额、探测不出结论就不报健康——都是这一条的不同写法。
