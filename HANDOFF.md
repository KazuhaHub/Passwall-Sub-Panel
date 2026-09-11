# 自研节点后端项目 — 交接文档

> 这份文档是**入口**,不是全部内容。它告诉你：项目在哪、决定了什么、下一步做什么、
> 以及三份详细文档各自回答什么问题。
>
> **它刻意不复述那三份文档**——一份真相源，是这个项目从头到尾在守的规矩。

## 0. 三十秒版本

PSP 今天通过 3X-UI / S-UI 这两个第三方面板管理节点。这个项目**自研一个节点后端**替掉它们，
理由和判据在 ADR 0024。**协议、PSP 原生适配器与生产同步路由已合流；C2 真 agent 契约测试已通过，
Passwall-Node 的 Xray 生产 daemon、精确 core 目录、遥测、离线执法、六平台发布与 Docker 均已接通。**

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
`task_results` 明确拒收并留在节点 outbox，绝不用成功响应把未来副作用结果丢掉。
全量计数的新鲜度只读 PSP 的实际接收时间，不信任 agent 自报时钟；缓存过期会令
`want_full_report=true`，有限额客户端在刷新前保持关闭。
双向同步载荷共用协议包的 16 MiB 上限；PSP 只接受自己确实铸造过的 applied epoch/version/ETag
组合，节点不能用未来坐标或同版本错摘要污染收敛状态。协议包同时约束响应调度值，后台设置校验
直接引用同一常量，避免控制面和 agent 各自维护不同上限。配额 pending delta 还会重新核验
agent→panel→client 所有权，越权 client key 与已退役 agent 缓存都不能影响别的面板用户。
跨仓模块发布闸已于 2026-09-11 完成：Passwall-Node revision `54705828baf3` 已进入 main，PSP
依赖已更新到 `v0.0.0-20260911213024-54705828baf3`。关闭父目录 `go.work` 后，PSP 全量 Go
测试、vet、关键路径 race、C2 真 agent 契约测试和六平台交叉编译均通过。

管理端现在可直接创建 `panel_type=psp`：PSP 在一个事务中建立 panel、agent 和三条流，返回一次性的
agent ID / Bearer 凭据 / sync endpoint；数据库只存 SHA-256。原生节点只能编辑名称和备注，旧凭据可
立即吊销并轮换，新值同样只显示一次。删除采用 fail-closed 规则：仍有节点/客户端，或空 config/roster
尚未由 agent 精确确认时，不能先删掉认证身份而留下一个继续服务、却再也接管不了的 core。

Xray core 选择已改成声明式 desired state：PSP 原生节点只能从 Passwall-Node `corecatalog/` 的精确
版本目录选择，`latest` 和未列入版本一律拒绝，受限版本要求管理员二次确认。PSP 选择器展示同一份
兼容说明、REALITY 客户端矩阵和结构化实测证据。当前默认 `26.6.27`；`26.7.28` 经
`minClientVer=0.0.0` 转换后 Xray/Mihomo/sing-box 均实测通过；`26.9.9` 的 Mihomo 输出固定
`chrome + support-x25519mlkem768=true`，sing-box 订阅会省略该 REALITY 节点而不是下发一个死节点。

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
- **任务 #49**:异地并发被标记的账号该怎么处理。停在证据不足上，
  v1 的 `ip_shadow` 影子执行就是为了给它攒证据。
- **§9 的剩余项**：sing-box core adapter、agent 自升级、带 exactly-once 结果状态的任务协议与
  RealityProbe。当前未知 `tasks[]` 仍必须明确拒绝；不能为了这些后续能力放宽现有失败语义。
- **跨仓发布闸已完成（2026-09-11）**：Passwall-Node 先发布、PSP 再更新 pseudo-version，且已在
  `GOWORK=off` 下通过全量 Go 测试/vet、关键路径 race、C2 真 agent 契约测试及六平台交叉编译。
  前端 31 文件/208 测试和生产构建通过；浏览器 smoke 交由 PR 的 Linux Chrome job 做最终确认。
- **`Passwall-Node` 的 licence**（README 写着 TBD）。

## 4. 这个项目的三条底线

写在最后，因为它们解释了上面很多看起来啰嗦的决定：

1. **一份真相源。** 协议类型只住在 `Passwall-Node`,PSP 用 Go module 引。
   在 PSP 里复制一份，就是把这次自研要摆脱的问题在自己内部复刻一遍。
2. **机制优于纪律。** 能让编译器或类型守住的，不要写成「记得调用某个校验函数」——
   本仓库反复证明后者活不过三个月。
3. **「不知道」永远不许长得像「没问题」。** 三态编码、含零值的全量枚举、
   缺席不解释成无限额、探测不出结论就不报健康——都是这一条的不同写法。
