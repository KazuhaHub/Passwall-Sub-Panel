# 节点访问控制（目的地策略、命中记录、白名单分组、隐私页）：最终实施计划

- **状态**：第六版／最终执行版（2026-10-04），按 §11 执行。部分工作包已有开发实现，但尚未完成阶段验收或发布；本文的测试与验收都是交付要求，实际进度单独记录在 §11.5。
- **文档来源**：第三版 `bb50b09f12f0bdaeb234c1390ce97c57af53b9bb`，原分支 `kazuha/access-control-and-dashboard-plans`。本版保留完整的界面、权限与发布设计，将七项复核问题落实到正文、协议、表结构、测试与验收，不另留待解释的补丁清单：
  1. 回退按实际 mint 的候选摘要与来源判定，修剪后的 LKG 被拒也能退出；
  2. 拦截、全局观察、白名单试运行、用量从聚合器到批次、队列、预算分别隔离；
  3. 匿名试运行行不按用户孤儿清理，按分组与面板清理；
  4. 重试最长 48 小时、允许未来偏差 1 小时、批次去重保留 72 小时；
  5. 明确内存积压与重启丢失的真实边界，删除「最多一个同步周期」保证；
  6. B 档统计正常放行与仅观察连接，命中与用量分别计数；
  7. 采集关闭或降档时，节点清理旧版本待发数据，PSP 投递与每块入库检查当前档位及持久化 revision；快速关闭再开启也不复活旧批次。

  第六版保留第五版的并发编辑版本、后台刷新提交条件、发布错误 CAS、分阶段迁移边界和工作包交接证据，落实所有者确认的「社区分类剔除过宽条目并显示解析报告」，同步存储、API、界面、空结果保护与真实数据验收；保留第四版七项修订及完整 23 屏设计。

  §1 原有 6 条所有者决定保持不变；本次分类处理选择另记在 §1.3。§1.2 的两项按已写明的默认执行，不阻塞核心功能；其中 N0-S 必须先完成真实环境验证。
- **涉及仓库**：Passwall-Protocol（线上类型）、Passwall-Node（执行与采集）、Passwall-Sub-Panel（策略、存储、界面）
- **核对基线（2026-10-04 重新 fetch 三个仓库，main 与 2026-10-03 一致）**：
  - PSP `origin/main` `0ae05e1e40170c030acd23cdf637310b740b50a6`：含第三版基线 `aa8a1b4b`，以及 #269（Claude 路由与 QUIC 默认值）、#270（3X-UI 3.9.0 兼容性）。这两项没有改变本计划依赖的节点同步、成员同步与审计控制路径；DNS 基础放行仍按开工时实际模板读取。
  - Passwall-Node `origin/main` `8e5e3db`（tag `v4.0.1.8`）：`753d3a6` 之后合入 #73（每次上报重新判断升级就绪）、#74（预发布不再审批）、#75（Docker updater 跟随 agent 自升级）、#76（updater 拒绝原因转发给 PSP）。
  - Passwall-Protocol `origin/main` `d8b2713`：PSP 与 Node 都钉 `v0.2.0`（= `c678bd5`），之后两个提交只改了 `.github`、README 和测试。
  - core catalog 未变（Node `corecatalog/catalog.json`，`updated_at` 2026-09-11）：Xray 26.6.27 recommended、26.7.28 verified、26.9.9 restricted；sing-box 1.14.0 recommended。
  - **本地检出不是代码基线**：复核时 Node 本地 `main` 落后远端 4 个提交；本版按已 fetch 的 `origin/main` 读代码。每个仓库开工前重新 fetch、记录 HEAD 与工具链，从各自最新 `origin/main` 切实施分支。
- **相关文档**：
  - [psp-node-agent.md](psp-node-agent.md)：协议形状。
  - [connection-limits.md](connection-limits.md)：§13.7、§14.9、§14.14 是风控存储与保留的既有规矩；§14.2–§14.4 是界面约定的先例；§14.15「升级与降级」、§14.16「指标」是本功能文档要照抄的写法。
  - [routing-rule-dsl.md](routing-rule-dsl.md)：规则名称。
  - [server-dashboard-rework-plan.md](server-dashboard-rework-plan.md)：WP-D4 服务器详情页。本功能**不依赖**它先落地（§7 S15）。

**读本文的方式**：
- §2 是事实，设计约束从这里来，每条都带出处。
- §3 是行为模型。匹配顺序一旦写错就是安全漏洞，先读它。
- §4–§6 是协议、节点、PSP 后端的做法；§7 是界面设计，实现者**不得即兴**的部分见 §7.0；§8、§9 是隐私页与白名单的行为。
- §11 是 PR 切分、发版顺序和升级降级；§12 是验收；§13 是测试文件清单。

**每个工作包先写失败的测试，再写实现。**

---

## 0. 一句话

PSP 定义两件事：哪些目的地拦截、只观察或放行；哪些分组只能访问白名单。定义编进原生节点代理内核的路由。
节点通过内核的 webhook 回传**命中了哪条规则**（默认开启）。管理员可以按节点再打开**按主域名聚合的用量**。
原始访问日志永远不离开节点。

## 1. 所有者已定的决定

| # | 问题 | 决定 | 日期 |
|---|---|---|---|
| 1 | 动机 | **防滥用**（BT、SMTP、扫描导致 VPS 被投诉）、**屏蔽高风险金融网站**、**白名单** | 2026-09-27 |
| 2 | 适用范围 | **只做 PSP 原生节点**。3X-UI / S-UI 的 Xray 路由模板归运营者，PSP 不写；它们显示「不执行」 | 2026-09-27 |
| 3 | 记录粒度 | **默认 A 档**（只记规则命中）；**管理员可按节点开启 B 档**（每用户每小时按主域名聚合）；C 档（原始日志）不回传 | 2026-09-27 |
| 4 | 对用户披露 | 做**隐私与协议页面**，**默认关闭** | 2026-09-27 |
| 5 | 白名单的含义 | **按分组的白名单模式：该分组的用户只能访问名单内的目的地，其他一律拒绝**（§9）。放行例外与用户豁免同样保留 | 2026-09-28 |
| 6 | 默认值 | 存在启用的策略时 A 档对所有原生节点默认开启；命中保留 30 天、用量保留 7 天 | 2026-09-28 |

### 1.1 本版由规划者定下的决定（所有者可推翻，推翻时改本节和对应小节）

| # | 决定 | 理由 | 落点 |
|---|---|---|---|
| R1 | 界面做**独立的「访问控制」页**（四个 tab），不并进风控中心 | 两点理由。其一，可发现性：要「禁止 BT」的运营者不会去「风控中心 › 策略 › 分段」里找。其二，风控中心导航注释写明它是「看人，不看管道」（`layouts/AdminLayout.tsx:121-126`），而列表、下发、节点执行情况都是管道。并入还要求改动由 48 键守卫锁住的策略页，并让同一个 tab 里出现两种保存方式（`useLeaveGuard.ts:18-21` 只认 `tab`，切分段会丢草稿） | §7.1 |
| R2 | 策略下发改为**发布快照 + 尾随去抖 + 「立即下发」** | 第二版的「先下发，再限速」有两个问题：一轮连续修改会让内核重启两次；去抖窗口内新加入白名单分组的成员不受限，与「成员变动不去抖」自相矛盾 | §6 P3 |
| R3 | 策略被内核拒绝、嗅探不足或超额度时，PSP **回退到该节点上一版已生效的策略**（LKG），并在下发前**预检嗅探** | Node 的 `runtime.converge` 整份编译（F25）。策略一被拒，名单变更也部署不下去，被停用或删除的用户在该节点继续可用 | §6 P3、§5 N1 |
| R4 | 下发的采集档位取「面板档位」与「节点能力」中**较低的那个**；节点自己再按「过滤器 / 接收器是否真的在运行」把关 | 否则把 B 档发给只有 A 档能力的节点，会把每条连接重新写进 journald，正是 N0 修的泄漏 | §6 P3、§5 N0/N5 |
| R5 | 白名单**从试运行开始**就不给组员开不执行策略的节点；**试运行命中只存「分组 × 主域名」**，不存账号与完整主机名 | 不这样做，试运行报告会漏掉这些节点上的流量；切到执行时还会再触发一轮 client 删除。按人按主机名存兜底命中，等于默认对整组开浏览记录，比要按节点显式开启的 B 档还细，违背决定 3 | §9.4、§6 P4 |
| R6 | 读取「按网站用量」要**点按钮才加载**，每次读取**写一条审计行** | 审计中间件只记写方法（CLAUDE.md「Audit scope is narrower than it looks」）；用量是逐人的浏览数据，谁看过要留痕 | §6 P7、§7 S16 |
| R7 | 提供一个**「暂停全部执行」应急开关**（`dest_policy_state.paused`），跳过去抖立即下发 | 一条写坏的全局拦截可能让全站断网。开关可撤销，不删任何定义 | §6 P3、§7 S1 |
| R8 | 豁免可以设**到期时间** | 排障用的临时豁免最容易被遗忘，最后变成永久口子 | §6 P1、§7 S5 |
| R9 | webhook 带一个节点本地的**鉴权头** | unix socket 0600 是唯一防伪手段；Xray webhook 本来就支持 `headers`（F5），加一道几乎零成本 | §5 N1-X/N4 |
| R10 | 运维员在分组列表能看到一个只读的「白名单」徽章，看不到名单内容 | 白名单会让组员的可用节点变少。运维员能看到用户和节点，却看不到原因 | §7 S17 |
| R11 | **不做拖拽排序**，只做「上移 / 下移」 | 同一动作内的先后只决定命中记在哪条策略名下，不影响放行或拒绝；还能省掉 HTML5 拖拽在键盘和触屏上的缺陷 | §7 S2 |
| R12 | 目的地文本**不进页面 URL** | 页面 URL 会进浏览器历史、被复制分享、在刷新时进反向代理访问日志；用户访问过的目的地不应活过 `dest.hit_retention_days` | §7.1 |
| R13 | `PolicyStatus` 随下行一起进 Protocol `v0.3.0`，不等到 `v0.4.0` | R3 的回退依赖 PolicyStatus。若放到 2a，第一批能执行策略的节点被拒时 PSP 看不到，名单就会冻结 | §4、§11 |
| R14 | 一律用相对时间加悬停绝对时间；不在文案里写死任何可调数值；固定的统计窗口一律**夹到对应保留期**再插值 | 沿用风控中心与运行诊断的规矩（`utils/relativeTime.ts:14`、`i18n/tunableText.test.ts:24`）。保留期可调到 1 天，写死的「7 天」会在这些面板上说谎 | §7.2 |
| R15 | 白名单试运行的「分组 × 主域名」折叠**在节点上做**，并用独立的聚合器 | 只在 PSP 入库时折叠的话，逐人逐主机的数据仍先离开节点；试运行的海量兜底命中还会挤掉真正的拦截命中（N4 的键上限、单次 4096 行） | §4.2、§5 N4、§6 P4 |
| R16 | 定义层面的问题（超额、整份无效）在**发布时**拦下，不进入任何节点的回退 | 这类问题对每个面板都一样。若按节点回退，会让全机队同时进入回退，白名单组员一个可用节点都没有；且额度与合格性互相依赖，会来回振荡 | §6 P3 |
| R17 | 「这台节点在不在记录命中」由 PSP **自行推导**，不依赖节点上报 Coverage | 安静的节点（没有命中）不发 Audit，PSP 区分不了「在采集但 0 命中」与「没在采集」 | §4.2、§6 P6 |
| R18 | 策略的修改只有一种写法：`PUT` 全字段 + `updated_at`，**不另设 PATCH** | S2 的开关、S4 的转为拦截都已持有完整的策略 DTO；两种写法意味着两套校验和两套冲突处理 | §6 P7 |
| R19 | 一个 Audit 批次只含一个种类与一个 UTC 小时桶；四种数据各有 pending、队列槽与入库预算 | 只拆聚合器仍会在报告、冻结重发、队列和共享预算处挤掉拦截记录；拦截保证仅覆盖低优先级数据不能消耗它的配额，不承诺故障下零丢失 | §4.2、N4–N6、P4 |
| R20 | Audit 是尽力交付的非持久遥测；重试与时间窗口固定，去重窗口更长 | 48 小时积压仍可能在重启时全部丢失；已入库而响应丢失的批次必须在整个有效重试期内保持幂等 | §4.2、N6、P1、P4、P5 |
| R21 | 采集档位以 PSP 当前保存值作为入库闸门；关闭不删除已经入库的历史数据 | 防止旧配置、冻结重发与后台队列在关闭后继续写入；已有数据照保留期清理 | N6、P4、S14 |
| R22 | 定义写入统一串行化；编辑版本严格单调，后台刷新与发布错误只提交到自己读到的版本 | UTC 毫秒时间可能碰撞，慢刷新与旧发布失败可能覆盖新修改；仅把写入放进事务并不能防止这些问题 | P1、P2、P3、P7、§12 15a/17a |
| R23 | 社区分类先筛选属性，再剔除过宽条目，保存解析报告；过滤后为空不覆盖旧列表 | 已校验的真实金融分类包含 `domain:hsbc`，整份拒绝会使正常分类不可用；逐条剔除仍保证过宽条目不下发。远程 URL 的整份拒绝规则不变 | §1.3、F49、P1/P2/P7、S6/S7、§12 15b |

R21 同时使用持久化的采集 revision：每次档位实际变化都递增，批次冻结时带上已部署的 revision；PSP 只接收当前 revision。先 off 再开启、节点尚未收到中间 off 配置或 PSP 重启，都不能让旧 pending 复活。

### 1.2 可覆盖的默认决定（实施按最后一列执行）

| # | 问题 | 为什么需要所有者 | 默认 |
|---|---|---|---|
| Q1 | sing-box 节点是否默认拦截私有与保留地址（N0-S）？ | Node `singbox/compiler.go:121` 的路由只有 `final: direct`，没有任何私有地址拦截（F9）。也就是说，今天的 sing-box 原生节点允许代理用户访问节点本机的回环服务和云厂商元数据 `169.254.169.254`，与本功能是否上线无关。补上会改变现有行为：家用节点上有人可能正通过代理访问家里的局域网（Xray 节点本来就不允许，F9） | **先在 `psp-node` VM 上用真 sing-box 1.14 确认能否访问；确认能访问后，在 Node 下一个修复版里加无条件的 `{"ip_is_private":true,"action":"reject"}`，不加 `resolve`**。所有者说「保留现状」就只写进 §10 |
| Q2 | 白名单组员在用户中心是否看到一行「你所在的分组只能访问指定网站」？ | 这是面向用户的披露，而决定 4 把披露页默认关闭；两者需要所有者一起权衡 | **不显示**，S23 保留位置但不实现 |

### 1.3 本次所有者确认的补充决定（2026-10-04）

社区分类库中的过宽条目采用**剔除并显示解析报告**，不采用整份分类拒绝。过宽判定标准不放松；被剔除的条目绝不进入下发内容。适用于该分类被 block、observe、allow 或白名单引用的情况。远程 URL 列表仍按 P2 整份拒绝，自定义列表仍逐行忽略。分类过滤后为空时，保留旧内容并标记刷新失败；从未成功的列表保持未就绪。存储、API、界面及验收统一按 P2 与 §12 15b 执行。

---

## 2. 已核实的事实

实现者发现与此不符时，**先改本文，再动代码**。Node 路径指 `origin/main` `8e5e3db`；PSP 事实最初在 `aa8a1b4b` 核实，第四版在 `0ae05e1e` 复核对应路径。行号是定位提示，开工时以实际符号为准。

### 2.1 代理内核（Xray / sing-box 上游）

| # | 事实 | 出处 | 影响 |
|---|---|---|---|
| F1 | Xray 路由与 sing-box 路由都是**第一条命中即终止**。命中「仅观察」规则（路由到一个放行出站）之后，后面的规则不再评估 | Xray `app/router`；sing-box `route/rule.md` | §3 的顺序是安全关键 |
| F2 | Xray 规则的 `user` 为空数组时**不增加任何条件，等于匹配所有人** | `app/router/config.go:64`（v26.6.27；26.9.9 为 `:68`） | 编译器遇到空 subject 集合必须**跳过整条规则** |
| F3 | Xray 一条规则内的各字段是「与」；sing-box 默认规则里 `domain*` 与 `ip_cidr` 是「或」，与端口、网络、协议是「与」 | Xray 路由文档；sing-box `route/rule.md` Default Fields | Xray 要把 domain 与 ip 拆成两条，sing-box 不拆 |
| F4 | 嗅探出的域名要同时满足三个条件才进入路由：`destOverride` 含对应协议、`metadataOnly` 为 false、域名不在 `domainsExcluded` 里 | `app/dispatcher/default.go:232-260,295-316` | 嗅探配置不够时，域名规则会**静默失效** |
| F5 | **Xray v26.6.27 起每条路由规则支持 `webhook`**：命中时向一个 HTTP 或 unix socket 地址 POST 一段 JSON。<br>• 字段：`email`、`source`、`destination`、`originalTarget`、`routeTarget`、`outboundTag`、`ts`。<br>• `destination` 是 `net.JoinHostPort(嗅探域名或 IP, 端口)`，IPv6 带方括号；`ts` 是 Unix **秒**。<br>• 支持 `headers`（自定义请求头）。<br>• 每个事件起一个 goroutine，超时 5 秒；每条带 webhook 的规则各自一个 notifier（各自一个 `http.Client`）。<br>• unix socket 的写法是 `"<绝对路径>:/<http 路径>"`。<br>• catalog 的三个 Xray 版本都具备 | 26.9.9 `app/router/webhook.go:23-35,60,103,107-134`；`infra/conf/router.go:126-152,264-273`；`common/utils/unixsocket.go:36-54`。`headers` 在 catalog 三个版本的 tag 提交里都存在：v26.6.27 = `45cf2898ab12`、v26.7.28 = `5ca6f4b7d4dc`、v26.9.9 = `52a412d9e2f5`（`git ls-remote` 核实），三者的 `infra/conf/router.go` 都有 `WebhookRuleConfig.Headers`（`:126-151,264-268`）。注意：Go 模块缓存里最早的那份 `…20260711155151-50231eaff98c` 是 26.7.11，**不是** 26.6.27，不能拿它当 26.6.27 的出处 | **A 档命中用 webhook 采集**，不解析日志（§5 N4） |
| F6 | Xray 访问日志一行的格式：`2006/01/02 15:04:05.000000 from <src> accepted\|rejected <tcp\|udp>:<dest>:<port> [<in> -> <out>] email: <e>`。<br>• 分隔符：命中规则时是 ` -> `，走默认出站时是 ` >> `，强制出站时是 ` ==> `。<br>• `<dest>` 是**客户端请求的**目的地，不是嗅探出的域名。<br>• `rejected` 行带来源 IP，没有 email | `common/log/access.go`、`common/log/logger.go`、`app/dispatcher/default.go:489-499`、`proxy/vless/inbound/inbound.go:601-603` | B 档用量要解析日志（§5 N5）；日志不能原样转发 |
| F7 | Xray 在 `log.access` 为空时把访问日志写到 stdout，只有 `"none"` 能关闭 | `infra/conf/log.go:31-41` | 见 F10 |
| F8 | `log.maskAddress` 会对**整行**做 IP 正则替换，目的 IP 也会被抹掉 | `app/log/log.go`（`MaskedMsgWrapper`） | **不能用** maskAddress 隐藏来源 IP |
| F9 | Xray `freedom` 出站对代理入站**默认在 DNS 解析后拦截私有与保留地址**。地址表：0/8、10/8、100.64/10、127/8、169.254/16、172.16/12、192.0.0/24、192.0.2/24、192.88.99/24、192.168/16、198.18/15、198.51.100/24、203.0.113/24、224/3、`::/127`、fc00::/7、fe80::/10、ff00::/8。<br>sing-box 的 `direct` 没有这个默认，Node 的 sing-box 编译器也没有补任何拦截 | 26.9.9 `common/geodata/consts.go:9-30`（`GetPrivateIPMatcher`），由 `proxy/freedom/freedom.go` 使用；Node `internal/core/singbox/compiler.go:121`（`Route: {"final":"direct"}`） | 「内网与元数据」模板在 Xray 上主要起**记录**作用；sing-box 见 §1.2 Q1 |

### 2.2 节点侧（Passwall-Node）

| # | 事实 | 出处 | 影响 |
|---|---|---|---|
| F10 | **生产 Xray 节点正在把每条连接（含 email 与目的地）写进 journald / Docker 日志**。编译器只写 `loglevel`，只有 `AccessLog` 非空时才写 access 字段；生产接线只传 `APIListen/CoreVersion/AllowRestrictedReality`；core 的 stdout 直接接 agent 的 stdout。**截至 `v4.0.1.8` 仍未修**：753d3a6 之后三个版本（v4.0.1.6/.7/.8）都没带，远端也没有在做它的分支 | `internal/core/xray/compiler.go:133-134,152-154`；`cmd/node/main.go:200-203`；`internal/core/process/supervisor.go:497-498`；F7 | **N0 热修**，与本功能是否上线无关，现在就单独派工 |
| F11 | Xray 配置**没有 `routing` 段**；`blocked` 黑洞出站存在，但没有流量指向它；`xrayConfig` 已有一个 `Policy` 字段，对应 Xray 自己的 levels/stats 段 | `internal/core/xray/compiler.go:64-71,140-150` | 新增 routing 生成；节点侧的策略字段命名为 `DestinationPolicy`，避免与 `xrayConfig.Policy` 混淆 |
| F12 | 节点**没有 geosite/geoip 数据文件** | `internal/core/install/install.go:186-190` | 分类由 PSP 展开成明文条目下发 |
| F13 | **任何 config/roster 变化都会让 core 全量重启**，没有热重载 | `supervisor.go:403-432`；`internal/core/runtime/runtime.go:277-351` | 策略变更要去抖；远程列表只在内容变化时下发 |
| F14 | `stream_documents.stream` 有 `CHECK (stream IN ('config','roster','directives'))`，state schema 为 9；**schema 一变，两种远程升级都直接拒绝** | `internal/state/sqlite/store.go:23,454`；`internal/upgrade/helper_linux.go:138`（systemd）；`internal/upgrade/docker_helper.go:880-885`（Docker updater，按镜像标签比较 schema） | **不新增 stream**；策略作为 `ConfigBody` 的可选字段下发 |
| F15 | outbox 表 `kind CHECK (kind IN ('issue','task_result'))` | `store.go:496` | 命中与用量不走 durable outbox，走 `NodeReport` 上的非持久可选字段（先例 `Host`） |
| F16 | 旧节点收到不认识的字段会静默丢弃，但仍把 PSP 的 ETag 报成已应用 | `internal/agent/receive.go:145-150` | 必须用 capability 把关 |
| F17 | 两个编译器解析 listener config 时都用 `DisallowUnknownFields` | `xray/compiler.go:311-325`、`singbox/compiler.go:458-472` | 策略不能塞进 listener config |
| F18 | 编译器输入 `core.Snapshot` 只有 `Listeners/Clients/Now`；`CompilerFactory` 只拿到 `CoreSelection` | `internal/core/core.go:16-20`；`runtime.go:32` | 要给 Snapshot 加字段 |
| F19 | processor 对未变化的 listener **不调用 runtime 就标记 applied**。若只有策略变化而内核拒绝，节点只产生 `core_convergence_failed` issue，继续跑**旧**策略（supervisor 回滚） | `internal/agent/processor.go:251-257`；`internal/agent/issues.go:25` | config ETag 不能证明策略已生效，需要单独的 `PolicyStatus` |
| F20 | config 的 `ObjectStatus` key 必须是 listener key；新增 key 类型会让**旧 PSP 拒收整次上报** | Protocol `validate.go:188-195`（`validateReportedObject`） | `PolicyStatus` 做成独立字段，不借用 ObjectStatus |
| F21 | sing-box 1.14 拒绝旧式的 per-inbound sniffing；要在 `route.rules` 里用 `action: "sniff"` | `singbox/compiler.go:184-186,493-500` | sing-box 的编译方式不同 |
| F22 | 新上报字段必须同时加进手写的 `partialReport`。`fitReportToWire` 在 **Node**（`internal/agent/report.go:163`，`:172-182` 先丢 Host），不在 Protocol。`ReportBuilder.Build(ctx, partial, host)` 把 Host 作为参数传入，`BuiltReport.HostDropped` 报告是否被丢 | Protocol `report.go:158-169`；Node `report.go:50-59,151,163-185` | Audit 照 Host 的路径接入 |
| F23 | supervisor 的 stdout 是可注入的 `Options.Stdout io.Writer` | `supervisor.go:38,147-151,497-498` | B 档的日志过滤器从这里接入 |
| F24 | 能力分两部分（#73）：进程生命周期内固定的 `Capabilities`（`main.go:308` 的静态切片，`host.telemetry.v1` 也在这里追加）；以及每次上报都重新询问的 `CapabilitySource`，目前只给任务能力用 | Node `internal/agent/report.go:27-41,70-73`；`cmd/node/main.go:308,390` | 新能力进静态切片；PSP 比较能力时只看本功能关心的那一个，不比整个集合（§9.2） |
| F25 | **一份 config 里任何一处编译失败，整份配置都不再部署**：`converge` 遇到 `Compile` 出错直接返回，不调用 `Deploy`；两个编译器遇到任一 listener 错误就中止整个产物；processor 在 `waiting \|\| runtimeErr != nil` 时把对象留在 pending。于是同一轮及之后的 roster 变化（新增、删除用户，额度与到期的停用）全部卡住，节点继续跑旧配置 | `runtime.go:322-327,345`；`singbox/compiler.go:136-142`；`xray/compiler.go:180,197`；`processor.go:380-400` | 策略失败不能拖住名单。PSP 必须预检并回退（R3，§6 P3） |
| F26 | `saveDeployment` 在部署成功后写入 `CoreDeployment`（artifact、digest、`ConfigBody`、`RosterBody`）。**启动时只有 `cmd/node/main.go:525` 的 `prepareInitialCore` 读它**，而且只取 artifact 和 digest，用来启动 supervisor；runtime 本身**没有**启动读取（`runtime.go:183` 位于 `nextExpiry`，只用于计算本地到期时间） | `runtime.go:170-200,354-385`；`cmd/node/main.go:177-191,525-526` | 重启恢复要新增一个显式的 `RestoreStatus`，由 main.go 调用（§5 N3） |
| F27 | Docker 节点经 PSP 远程升级成功后，updater 会删除旧 agent 容器（连同它的 json-file 日志）；updater 跟随 agent 升级自己（#75）。updater 早于 4.0.1.7 的 Docker 节点要手动重建一次，之后才能远程升级 | `internal/upgrade/docker_helper.go:730-739`；README「The updater follows the Agent」 | N0 发布说明里的清理指引（§5 N0） |

### 2.3 PSP 侧

| # | 事实 | 出处 | 影响 |
|---|---|---|---|
| F28 | 期望状态的唯一生成点是 `nodesync.Service.Sync`；`buildConfig` 是纯函数（无 ctx、无 repo），每轮每节点都跑 | `internal/service/nodesync/nodesync.go:127,189,346` | 策略编译要单独注入并缓存 |
| F29 | `Sync` 先剥离 `report.Host`，最后才入库，入库不返回错误；能力在 `ingestReport` 里**每次上报整体覆盖** | `nodesync.go:137-138,466,479`（`UpdateProtocolObservation`） | Audit 照此处理（剥离、晚入库、不返回错误）。**PolicyStatus 例外**：它是控制输入，在 Compile 之前处理（§6 P3 接入点）。能力比较见 §9.2 |
| F30 | 报告校验分两层。PSP 只跑 `ValidateNodeReportBase`（控制半边），Host 子树单独用 `ValidateHostObservation` 隔离校验，失败只丢样本（`sanitizeHost`，计数走 `psp_node_host_report_total`）。`ValidateNodeReport` 是**发送方**用的全量校验。PSP 用 `json.Unmarshal`（非严格），不认识的字段静默忽略 | Protocol `validate.go:12-31,125-158`；PSP `transport/http/handler/node_sync.go:157-187,224,256` | Audit、PolicyStatus **不得**进 `ValidateNodeReportBase`，要照 Host 隔离校验；旧 PSP 收到新字段不会报错 |
| F31 | 节点上 client email 形如 `u{userID}{-k8hex}@{site.email_domain}`，**一人一面板可有多个**；roster 带 `Subject: usr_{userID}` | `domain/pspclient.go:218-224`；`pkg/clientplan/clientplan.go:195-204,481-526`；`nodesync.go:440` | 协议按 subject 指人，节点编译时展开成 email |
| F32 | config 对象只在 `ObjectApplied` 时调用 `ConfirmAppliedConfig`，其他状态和 IssueCode 不落库 | `nodesync.go:609-650` | 节点上报的「嗅探不足」等信息要走 PolicyStatus，不能指望 ObjectStatus |
| F33 | 面板表是 **`xui_panels`**（`xuiPanelRow`，`sqlstore/schema.go:930`；domain `domain.Panel`，`domain/types.go:1276`，`XUIPanel` 是它的别名，`:1322`）。`xuiPanelRepo.Save` 更新时整行 `Omit("CreatedAt").Save(row)`；按列写入的入口是 `UpdateNativeMetadata(ctx, id, name, remark, channel)`，它校验 `kind == psp` | `adapters/sqlstore/xui_panel_repo.go:84-110,118-166` | 采集档位加在这张表上；必须排除在整行 Save 之外（§6 P1） |
| F34 | 分组选节点共 **10 处**：<br>• **7 处经 `group.Service.NodesFor`**：`service/render/render.go:108`；`service/user/user.go:302,1475,2179,2231`（经 `s.selector`）；`transport/http/handler/admin_rules.go:239`；`transport/http/handler/user_me.go:250`。<br>• **3 处直接调纯函数 `group.Matches`**：`service/reconcile/reconcile.go:429`；`service/node/node.go:597,1362`（这两个服务只持有分组 repo）。<br>`NodesFor` 走 `listEnabledCached`；对 `TagFilter.All` 有捷径，直接返回全部节点 | `service/group/group.go:96-113` | 白名单把 10 处收拢成一个判断（§9.2） |
| F35 | 风控新表走「具体 repo + 使用方自带窄接口」模式，不进 `ports.Repos`；接线有守卫测试 | `internal/app/app.go:589,607,613`；`app/flag_recorder_wiring_test.go:31`；`app/cleanup_test.go:351` | 本功能的表照此办理 |
| F36 | MySQL 的 `TEXT` 上限 64 KiB；MySQL 默认排序规则不区分大小写，仓库已经为此吃过亏 | MySQL；`sqlstore/node_agent_repo.go:515-521` | 大列表条目用 `[]byte`（longblob，先例 `node_agent_streams.desired_body`）；目的地入库前统一转小写（§4.2） |
| F37 | 远程拉取的先例是地理库更新（`safehttp`、大小上限、原子替换），**没有 ETag/摘要持久化先例**；它的读法遇到超限会**静默截断** | `service/geo/update.go:108-181,318` | 列表刷新照它写，但超限要报错 |
| F38 | 已有设置键：`sub.sub_log_retention_days`（**0 = 永久**，`settings_kv_repo.go:365`）、`security.auth_event_retention_days`、`risk.hwid_capture_off`（`:500`）、`risk.connection_retention_days`（`:517`）、`risk.flag_record_retention_days`（`:520`）。管理审计日志已经占用 `audit` 这个词（`security.audit_retention_days`、`audit_log`、`runAuditCleanupLoop`） | 同左 | 本功能的设置用前缀 **`dest.*`** |
| F39 | **#262 之后的风控中心**：<br>• 标签是 `queue, live, records, policy`（`views/admin/risk/riskParams.ts:14`）。<br>• 单个账号的视图是抽屉 `views/admin/risk/drawer/RiskUserDrawer.tsx`：四个 tab overview/connections/devices/timeline（`:39-40`），`host: 'risk' \| 'users'`（`:47,:117`），宽 560、手机 100vw、`md.surfaceContainerLow`（`:203`），`riskDrawerZIndex = modal+1`（`:29`），每次打开都重置回 overview（`:131-139`）。<br>• 风控中心用 `?user=` 打开抽屉，用户页用 `?risk=`（`UsersView.tsx:295,1642`）；抽屉参数 hook 只接受数字 id（`drawerParam.ts`，`parseUserId`）。<br>• 旧链接 `?tab=user&id=N` 由 `legacyRedirect` 改写成 `?tab=queue&user=N`（`riskParams.ts:50-76`）。<br>• `UserLookupDetail.tsx` 等已删除 | 同左 | 用户维度的界面加在抽屉里（§7 S16）；不要再写 `?tab=user&id=N` |
| F40 | **风控设置只有一个写入方**：<br>• UISettings 里每个以 `risk_` 或 `geo_anomaly_` 开头的 json tag 都必须出现在 `ports.RiskCenterPolicy` 中，数量钉死为 48（`ports/risk_center_policy_test.go:71,172`）。<br>• 前端用 `policyKeys.json`（48 键）与 `policyLayout.ts`（六张卡，`PolicyCardId` 在 `:57`）双向锁住（`risk_center_policy_spa_test.go:19`）。<br>• 可按分组覆盖的 risk.* 键数必须等于 `domain.RiskPolicySettings` 的字段数（`policy_settings_test.go:96`）。<br>• 系统设置页的 PUT 保留这些键（`admin_settings_policy_preserve_test.go:55`）。<br>• 专属端点是 `/risk-center/policy`（`router.go:647-648`）。<br>• `RuntimeEffective` 只覆盖 23 个 geo/risk 运行时旋钮（`ports/policy_settings.go:138`）。<br>• `OverridableScopeKeys` 在 `ports/repos.go:1997` | 同左 | 新风险设置必须进 RiskCenterPolicy（48 → 50）；`dest.*` 照这个「领域自带端点」模式另起一份（§6 P5） |
| F41 | **运行诊断的指标目录（#267）**：`internal/pkg/metrics/psp.go` 每新增一个 family，都必须同时在 `web-react/src/utils/diagnosticsCatalog.ts` 的 `FAMILY_CATALOG` 归到一张卡上（类型、是否带标签与 Go 一致），并在两份语言包里有 `admin:diagnostics.metric.<f>.label/.desc`；带标签的还要在 `FAMILY_LABEL_GROUP` 指定 `admin:diagnostics.labels.<group>.*` | `diagnosticsCatalog.ts:5-9,119-127,140`；`diagnosticsCatalog.test.ts:80-99` | 每个新 family 与它的目录项、文案放在同一个 PR（§6 P9） |
| F42 | 审计日志的读接口在 `staffGroup`（运维员可读，`router.go:656`）。`AuditWrites` 会把路由、参数、请求体写进审计行。`adminOnlyAuditTargets` 按前缀把风控中心的审计行对运维员隐藏 | `handler/admin_audit.go:26-35`；`transport/http/risk_center_route_test.go:223`（`TestRiskCenterAuditRowsAreAdminOnly`） | `/api/admin/dest/` 加进这个列表（§6 P7） |
| F43 | **#268**：原生节点离线时，user_resync 不再被当成「用户已删除」记为完成，而是按 1 分钟节奏重试，最多 `maxUserTaskAttempts = 100` 次（约 1.5 小时）。用尽之后，由 reconcile 周期里的 `HealSharedClients` 兜底：迁移完成后每第 4 个 reconcile 周期跑一次 | 提交 `aa8a1b4b`；`service/user/user.go:2349,3246`；`app/app.go:2111,2120` | 白名单「删除组员在不执行节点上的 client」依赖它（§9.2） |
| F44 | 主题对 `MuiChip` 的 root/filled 覆盖会抹掉 `color` 变体（`theme/index.ts:233`）。生产上因此出现过五个意思各不相同、颜色却完全一样的灰 chip。诊断页的徽章改用 M3 token 自己画，并且每个徽章都带图标和文字 | `views/admin/diagnostics/StatusBadge.tsx:16-20` | 状态一律用 token 徽章，禁止 `<Chip color>`（§7.2） |
| F45 | 前端已迁到共享查询缓存：`src/query/*`、`query/keys.ts`、`query/policies.ts`（每类资源要写新鲜度理由 `note`）、`useQueryScope` | `web-react/src/query/policies.ts:1-24` | 本功能的数据层照此写（§7.3） |
| F46 | 已有协议字段 `ClientCounters.LiveIPs` 上报活跃客户端 IP | Protocol `report.go` | 本功能**不重复**上报来源 IP（数据最小化） |

### 2.4 规则来源

| # | 事实 | 影响 |
|---|---|---|
| F47 | v2fly 每个 release 都发布 `dlc.dat_plain.yml`（约 3.6 MB，已展开 `include:`）和 `.sha256sum`。导出器支持 `domain:`/`full:`/`keyword:`/`regexp:`；属性首项以后缀 `:@attr` 表示，后续项用 `,@attr`（F49） | PSP 下载并校验后按分类取条目，不用解析 protobuf |
| F48 | `category-finance` 收的是**正规**银行和券商；`category-cryptocurrency` 235 条（无正则）；`category-porn` 6660 条，其中 140 条是 `regexp:`；**没有**博彩总分类，只有仅覆盖俄罗斯的 `category-betting-ru`（13 条）。2026-10-02 用下载的 `dlc.dat_plain.yml` 复核，sha256 与发布的 `.sha256sum` 一致 | 「高风险金融」没有现成分类；成人内容模板会占用 140 条正则额度 |
| F49 | 属性有**否定形式**，如 `domain:airchina.ae:@!cn`。2026-10-04 核对上游 [datdump 导出器](https://github.com/v2fly/domain-list-community/blob/master/cmd/datdump/main.go)：首个属性用 `:@`，后续属性用 `,@`（如 `domain:example.com:@cn,@ads`）；兼容读取多个 `:@` 的旧输入。release `20261004053124` 的 YAML 为 3 614 228 字节，sha256 与同版 `.sha256sum` 一致，当前样本仍只有单属性。该版 `category-finance` 含 `domain:hsbc`，现有公共后缀判定会拒绝它；这是实际解析复现，不是对分类名称的推测 | P2 第 3 条按字面剥离和匹配属性；`!cn` 与 `cn` 是两个不同的属性名；不能把第二个属性残留在下发域名里 |

### 2.5 第三版复核时补充核实的事实（2026-10-02）

| # | 事实 | 出处 | 影响 |
|---|---|---|---|
| F50 | Node **每轮**都对整份报告跑 `protocol.ValidateNodeReport`，失败就整轮不同步（收不到 roster）。Host 能避开，是因为 `HostReporter` 在挂上报告之前已单独校验过；已构建未送达的 Host 样本**原样重发**（`cached`），不并回采集器 | Node `internal/agent/sync.go:121-126`；`host_reporter.go:140-152,195-207` | Audit 与 PolicyStatus 必须先单独校验，失败只丢子树（§5 N6）；上报批次照 Host 冻结重发 |
| F51 | `supervisor.validateCandidate` 失败时返回普通的 `fmt` 错误，`-test` 超时也只是包一层 `context` 错误；core 下载失败、stop 失败在 `converge` 里同样是普通错误。runtime 区分不了「核心明确拒绝了配置」与其他部署失败 | Node `internal/core/process/supervisor.go:468-487`；`internal/core/runtime/runtime.go:335-350` | 「被拒」的归因要专门设计（§5 N3） |
| F52 | processor 每轮都调用 `round.Finalize` → `converge`。即使什么都没变，也要全量编译、序列化，并在 digest 相同的分支里照样 `saveDeployment`，把 artifact 和两个 body 整行写回 SQLite。默认 30 秒一轮。PSP 侧每轮 `MintStream` 把 `desired_body` 整行读出来做 `bytes.Equal` | Node `processor.go:194-206`；`runtime.go:325-333,354-385`；`state/sqlite/core_deployment.go:37-56`；PSP `ports/repos.go:1163-1166`（`node_poll_seconds`，0 = 30 秒）；`adapters/sqlstore/node_agent_repo.go:415-428` | 策略最多 4 MiB，必须加空闲短路（§5 N1、§6 P3） |
| F53 | PSP 的 sync handler 用 `json.Unmarshal(body, &report)` 把整份报告解进带类型的 `NodeReport`，任何类型错误（如 `uint16` 收到 70000）都让整次同步返回 400 | PSP `transport/http/handler/node_sync.go:220-227` | Audit、PolicyStatus 要用 RawMessage 隔离解码（§6 P4） |
| F54 | `ResyncMembership` 只在 `provisioned && lifeErr == nil` 时调用 `deleteRetiredSharedClients`；后者删除失败**只打日志**，不返回错误。整个面板被移出时，`clientprov.Sync` 在期望为空的分支里把该面板现有的全部 client 列为 retired。`ReconcileOrphans` 只扫描用户**还有期望 client** 的面板（`desiredByPanel`） | PSP `service/user/user.go:2268-2331`；`service/clientprov/clientprov.go:69-78`；`service/sharedclient/sharedclient.go:971-985` | 白名单把组员移出 3X-UI 面板时，删除不能被其他面板的失败拖住（§9.2 第 6 条） |
| F55 | 审计中间件记录 `/api/admin/` 下**所有** POST/PUT/PATCH/DELETE 及其请求体（每个字符串截到 8192 字符）。PSP 自己的访问日志把 `params.Path` 写进 stdout，gin 的 `Path` 含查询串。未注册的 `/api/...` 路径走 `NoRoute` → `StaticSPA`，返回 **200 + index.html**，不是 404 | `middleware/audit.go:106-130,250`；`transport/http/access_log.go:35-60`；`router.go:882-893`；`handler/static.go:122-126` | 预览接口要豁免审计；`/dest/` 的查询串不进访问日志；界面不能靠「404 = 未上线」判断（§6 P7、§7 S15） |
| F56 | 前端现状：<br>• `stateTone` 没有 `quiet` 这一档；`none` 用 `RadioButtonUnchecked`，`not_applicable` 用 `Block`，`idle` 用 `RemoveCircleOutline`。<br>• 主题没有设置 `warning`、`success`，用的是 MUI 默认色：浅色下 `warning.dark` 压在自己 16% 的底色上约 3.4:1，`success.main` 压在 `surfaceContainerHigh` 上约 4.2:1。<br>• `FieldHint` 只有 `'warning' \| 'muted'`，`detail` 必填。<br>• `CodeEditor` 写死 `yaml()` 扩展，没有 ref。<br>• `KpiGrid` 已存在（`KpiTile.tsx:32`）。<br>• `DetectorStateChip` 用的是 `<Chip color>`。<br>• 仓库没有 MSW 一类的请求拦截层。 | `views/admin/diagnostics/StatusBadge.tsx:30-51`；`theme/index.ts:56-62`；`components/FieldHint.tsx:20-28`；`components/CodeEditor.tsx:10-37`；`views/admin/risk/evidence/DetectorStateChip.tsx:24-33`；`package.json` | §7.2 的色表、UI-0、§7.6 的种子数据 |
| F57 | 页外深链现状：`NodeIssuesView` 不读任何 URL 参数；`NodesView` 的入站编辑框只能通过 `openEditInbound(n)` 打开（`:2991`），没有深链；`ServersView` 的搜索框是 `useState('')`（`:193`），而列表按 URL 的 `q` 过滤，于是深链进来时显示「已过滤的列表 + 空搜索框」，失焦还会把筛选清掉（`:1480`） | 同左 | §7 S14 的三个跳转要先补深链 |
| F58 | `golang.org/x/net`：Node 直接依赖 v0.58.0，PSP 只是间接依赖 v0.59.0；两边的 publicsuffix / idna 表可能不同 | Node `go.mod:9`；PSP `go.mod:151` | eTLD+1 只在节点侧计算（§4.2） |
| F59 | `docs/compat/verification-v1.json` 的 `contract_source` 必须是**已发布**的 Node tag 加 commit。Node 的 `protocol/alias.go` 是待退役的兼容层（文件头注释），唯一的外部使用者是 PSP 的两个 live 测试，只用到已有名字 | PSP `docs/compat/verification-v1.json:29-34`；Node `protocol/alias.go:1-20`；PSP `service/nodesync/task_*_live_test.go` | §4.3 第 7、8 条 |

---

## 3. 行为模型（先读这一节）

### 3.1 三个动作

| 动作 | 语义 | Xray 出站 | sing-box |
|---|---|---|---|
| `block` 拦截 | 断开 + 记命中 | `psp-deny-<id>`（blackhole） | `action: "reject"` |
| `observe` 仅观察 | 放行 + 记命中 + **终止匹配**（F1） | `psp-watch-<id>`（freedom） | 路由到 `psp-watch-<id>`（direct） |
| `allow` 放行 | 放行，不记命中，终止匹配 | `direct` | `action: "route", outbound: "direct"` |

界面必须写明：**「仅观察」规则命中的连接，后面的规则不再对它生效**。

### 3.2 匹配顺序（编译器必须严格按此输出）

```
0. [sing-box] {"action":"sniff"}（有域名或协议规则时）
1. 全局 allow 策略（按 priority）                          → direct
2. 豁免用户  user:[豁免者全部 email]                        → direct
3. 全局 block 策略（按 priority）                           → psp-deny-p<id>
4. 各白名单分组（§9），每组依次：
     4a. 该组名单            user:[组员]                    → direct
     4b. 该组基础放行 + 补充  user:[组员]                    → direct
     4c. 该组兜底            user:[组员]（CatchAll）        → 试运行 psp-watch-g<gid> / 执行 psp-deny-g<gid>
5. 全局 observe 策略（按 priority）                         → psp-watch-p<id>
6. （无规则命中）                                           → direct（Xray 默认出站；sing-box route.final）
```

**为什么这样排**：

- 全局 `block` 排在白名单之前：白名单里的网站同样不许跑 BT、不许发信。
- 全局 `observe` 排在**白名单之后**：放前面的话，白名单组员访问 observe 列表里的站点会被直接放行（F1），等于绕过白名单。
  代价：白名单组员命中全局 observe 列表时不会被记录（他们要么被白名单放行，要么被兜底拒绝），可以接受。
- 全局 `allow` 对白名单组员同样生效，相当于给每个白名单分组都追加了放行项。allow 策略的编辑框要提示这一点。
- 豁免用户不受 3、4、5 约束，因而也不受白名单约束。界面写明「豁免 = 不受任何访问限制」。
- 应急开关 `paused`（R7）打开时，整份策略不下发（只剩 B 档的采集配置），各节点按第 6 步全部放行。**paused 优先于一切**，包括 §6 P3 第 7 条的回退：回退中的节点同样停止执行。

### 3.3 总体结构

```
                     PSP                                              Node
┌──────────────────────────────────────────┐        ┌──────────────────────────────────────────┐
│ 列表（自定义 / 远程URL / v2fly 分类）        │        │ ConfigBody.Policy → Snapshot.DestinationPolicy│
│ 策略；豁免；白名单分组；节点采集档位          │        │   compiler：routing.rules / route.rules    │
│  └ 任何定义写入 → generation+1              │ config │   psp-deny/psp-watch 规则挂 webhook         │
│  └ 尾随去抖后发布快照（超额 / 无效则不发布） │ ─────▶ │ webhook 接收器（unix socket）→ 命中聚合器   │
│  └ destpolicy.Compile：快照 × roster ×      │        │   试运行兜底命中 → 折叠到主域名 → 独立聚合器│
│     用户→分组 × 档位 × 能力；预检嗅探；      │        │ [B 档] core stdout 过滤器 → 用量聚合器      │
│     被拒 / 嗅探不足 → 修剪后的上一版（LKG）   │        │ PolicyStatus（内核真正在跑的策略摘要）       │
│ PolicyStatus → LKG 状态机（Compile 之前）    │ report │ Audit（冻结成 pending 批次，原样重发）       │
│ Audit → 有界队列 → dest-audit-ingest 入库    │ ◀───── │                                            │
│ 风险信号 dest_block                          │        └──────────────────────────────────────────┘
└──────────────────────────────────────────┘
```

---

## 4. 协议（Passwall-Protocol）

加性改动：不升线上协议版本、不加端点、不加 stream（F14）。**模块只能用标准库，测试也一样**（`.github/workflows/test.yml:62`「no non-standard-library dependencies」、`:140`「go.mod carries no requirements」），所以 eTLD+1、IDN 的**计算**都不在这里，Protocol 只做**校验**。

### 4.1 下行与生效状态：`ConfigBody.Policy`、`NodeReport.PolicyStatus`（模块版本 `v0.3.0`）

```go
type ConfigBody struct {
    Listeners []Listener      `json:"listeners"`
    Coverage  SegmentCounts   `json:"coverage"`
    Core      CoreSelection   `json:"core"`
    // Present only for an agent that advertised CapabilityDestinationPolicy.
    // omitempty is load-bearing: a panel with no policy must mint byte-identical
    // config for every existing node, or upgrading the panel restarts every core.
    Policy *DestinationPolicy `json:"policy,omitempty"`
}

type DestinationPolicy struct {
    Rules   []DestinationRule `json:"rules"`              // 已按 §3.2 排好序；可为空（仅 B 档时）
    Exempt  []SubjectKey      `json:"exempt,omitempty"`   // 编译为 §3.2 第 2 条
    Collect CollectLevel      `json:"collect,omitempty"`  // "" 不采集 | "hits" | "hits_and_usage"
    CollectRevision uint64    `json:"collect_revision,omitempty"` // Collect 非空时为面板当前采集版本，>=1
}

type DestinationRule struct {
    ID        string       `json:"id"`                  // 见下方「ID 分配表」；用作出站 tag 后缀
    Action    RuleAction   `json:"action"`              // "block" | "observe" | "allow"
    Subjects  []SubjectKey `json:"subjects,omitempty"`  // 空 = 所有人（CatchAll 规则除外，见校验）
    Domains   []string     `json:"domains,omitempty"`   // "domain:x" | "full:x" | "keyword:x" | "regexp:x"
    CIDRs     []string     `json:"cidrs,omitempty"`
    Ports     string       `json:"ports,omitempty"`     // "25,465,587" / "6881-6889"
    Network   string       `json:"network,omitempty"`   // "tcp" | "udp" | ""
    Protocols []string     `json:"protocols,omitempty"` // 仅 "bittorrent"
    Private   bool         `json:"private,omitempty"`   // 私有与保留地址；sing-box 编为 ip_is_private，Xray 编为 F9 的 CIDR 表
    // CatchAll marks a rule with no match fields that applies to every connection
    // of its Subjects — the default-deny tail of an allowlist group. It must be
    // explicit: a rule whose match lists merely came out empty (a list failed to
    // load) must be rejected, never silently widened into "match everything".
    CatchAll  bool         `json:"catch_all,omitempty"`
}

type NodeReport struct {
    // ...existing fields...
    PolicyStatus *PolicyStatus `json:"policy_status,omitempty"`
}

// PolicyStatus is what the core is ACTUALLY running, updated only after a
// deployment succeeded or a NEW policy was rejected BECAUSE OF THE POLICY
// (a PolicyError from the compiler, or a core -test failure that passes once
// the policy is removed). Unrelated deploy failures leave it unchanged. A config
// ETag cannot say this: the processor marks an unchanged listener applied
// without touching the runtime, and a core that rejects a policy-only change
// keeps running the previous one.
type PolicyStatus struct {
    Digest    string        `json:"digest"`                // 最近一次「部署成功」或「被拒」的那份策略的 PolicyDigest；无策略为 ""
    State     string        `json:"state"`                 // "applied" | "rejected"
    IssueCode string        `json:"issue_code,omitempty"`  // rejected 时：destination_policy_rejected | destination_policy_sniffing_insufficient
    Listeners []ListenerKey `json:"listeners,omitempty"`   // sniffing_insufficient 时，列出不满足的 listener；≤ MaxPolicyStatusListeners
}
```

**ID 分配表**（PSP 生成，Node 不改写）。ID 正则 `^[pg][0-9]{1,12}(x[0-9]{1,4})?$`：

| 来源 | ID | 说明 |
|---|---|---|
| 全局策略 | `p<policyID>` | 一条策略同时含 `Domains`（或 `CIDRs`/`Private`）与 `Protocols` 时，拆成 `p<id>x1`（域名、CIDR、私有地址部分）和 `p<id>x2`（协议部分），端口与网络两边各复制一份 |
| 白名单分组名单 | `g<gid>x1` | §3.2 第 4a 条 |
| 白名单基础放行 + 补充 | `g<gid>x2` | §3.2 第 4b 条 |
| 白名单兜底 | `g<gid>` | §3.2 第 4c 条，`CatchAll` |
| 豁免 | 无 ID（`Exempt` 字段） | Xray ruleTag 固定为 `psp-exempt` |

- Node 只在 Xray 内部给 ruleTag 追加 `a`/`b`（domain 与 ip 拆分，F3）。出站 tag 是 `psp-deny-<id>` / `psp-watch-<id>`，**不含** `a`/`b`，所以 webhook 的 `outboundTag` 去掉前缀就是协议 ID。
- PSP 入库时按正则去掉 `x\d+` 得到 `source`（`p12x2 → p12`、`g5x1 → g5`）。**这一步只做语法映射，不查策略是否还存在**：刚删除的策略产生的命中照样入库，界面显示「已删除的策略」。

**`func ValidateDestinationPolicy(*DestinationPolicy) error`**（新增；PSP 在 mint 前、Node 在接收时调用同一个函数）：

- 上限写进 `limits.go` 的新常量（编译期常量）：
  - 规则数 ≤ 256；
  - 全部 `Domains` 合计 ≤ 50 000；
  - `regexp:` 合计 ≤ 256，且每条都能被 Go `regexp` 编译；
  - `CIDRs` 合计 ≤ 20 000；
  - 全部 `Subjects` 加 `Exempt` 合计 ≤ 50 000；
  - Policy 规范序列化 ≤ 4 MiB。
- `ID` 唯一，且匹配上面的正则。
- Collect 非空时 CollectRevision 必须 >=1；不采集时为 0。PSP 只在本面板采集档位变化时递增该版本，集合与 Rules 的其他变化不递增它；nil Policy 的字节不变性不受影响。
- `Ports` 匹配 `^\d{1,5}(-\d{1,5})?(,\d{1,5}(-\d{1,5})?)*$`，每个值在 1–65535 之间，区间左端 ≤ 右端。
- 同一条规则内，`Domains ∪ CIDRs ∪ Private` 之间是「或」，它们与 `Ports`/`Network`/`Protocols` 是「与」。`Protocols` 不得与 `Domains` 同时出现（BT 识别不产生域名；PSP 按 ID 表拆分）。
- **匹配字段（Domains、CIDRs、Ports、Network、Protocols、Private）全空的规则，必须 `CatchAll: true` 且 `Subjects` 非空**；`CatchAll: true` 的规则不得带任何匹配字段。违反任何一条，整份 Policy 无效。所以 PSP 编译时，凡是匹配字段算下来全空的非 CatchAll 规则一律**跳过**（不只是引用未就绪列表的那种，§6 P3 第 4 条），永远不让一条空规则使整份失效。
- 主机名条目是小写 ASCII（`^[a-z0-9._-]+$`，长度 ≤ 253；`regexp:` 与 `keyword:` 除外）；CIDR 必须是 `netip.ParsePrefix` 的规范形式（已对齐到网络号）。
- `Domains`、`CIDRs`、`Subjects`、`Protocols`、`Exempt` 等集合切片**排序去重**后才算合法。`Rules` 是有序序列，必须保留 §3.2 的动作分段、分组内部顺序与 priority，**不得按 ID 或字典序重排**；只检查 ID 唯一。排序由 PSP 负责，校验只检查。
- Node 的 `validateConfig`（`internal/agent/validate.go:11`）调用它。**校验失败 = 整段 config 被拒**（连带 listener 变更），所以 PSP 必须在 mint 前先调用：定义层面的失败在发布时就拦下（§6 P3 第 1 条，R16），每节点编译出的结果再校验一次，失败走 §6 P3 第 7 条的回退。

**`func ValidatePolicyStatus(PolicyStatus) error`**：`State` 是两个合法值之一；Digest 为 64 位小写 hex，只有 applied 的无策略状态允许为空；applied 不带 IssueCode/Listeners；rejected 的 IssueCode 必须是上面两个策略错误之一且 ≤ `MaxIssueCodeBytes`。`Listeners` 只用于 sniffing_insufficient，数量 ≤ 新常量 `MaxPolicyStatusListeners = 256`，每个都是合法的 `ListenerKey`，排序去重。
**发送方**的 `ValidateNodeReport` 要求：PolicyStatus 非空时必须声明 `policy.destination.v1`。**PSP 不把它放进 `ValidateNodeReportBase`**（F30），而是在 handler 里单独校验，失败只丢这棵子树。Node 在挂上报告之前也先单独调用 `ValidatePolicyStatus`，失败就不带（§5 N6），绝不让整份报告的校验因它失败（F50）。

**`func PolicyDigest(*DestinationPolicy) string`**：规范序列化的 sha256；nil 返回 `""`。PSP 与 Node 共用。

**`func MatchDestination(p *DestinationPolicy, subject SubjectKey, target string, port uint16, network string) MatchResult`**（纯函数，PSP 的「测试目的地」用它，§6 P7；`subject` 为空表示「不属于任何分组、未被豁免」）：

- 按 §3.2 逐步评估，返回每一步的结果和终止步：`{Steps []MatchStep, Verdict string}`。
- 语义：
  - `domain:` 按标签边界做后缀匹配；`full:` 完全相等；`keyword:` 子串；`regexp:` 对完整域名做**不锚定**的 `regexp.MatchString`，与 Xray、sing-box 一致，需要锚定时由条目自己写 `^…$`（P2 的「过宽探针」也依赖这个语义）；
  - CIDR 用 `netip.Prefix.Contains`；
  - 目标是域名时**不做解析**，也不匹配 CIDR/Private（与 §10 第 3 条一致）；
  - `Protocols` 规则一律记为「无法测试」。

**conformance 向量**（放在已有的 `protocol/conformance` 包里，作为导出的 Go 数据，PSP 与 Node 的测试都能直接引用）：

- `conformance.DestinationMatchVectors`：每条是 `(policy, subject, target, port, network) → steps/verdict`；
- `conformance.SniffingVectors`：每条是 `(listener sniffing JSON) → 是否满足 F4 + 原因`。PSP 的预检和 Node 编译器都要通过同一组向量（§5 N1、§6 P3）。

**能力**：`CapabilityDestinationPolicy = "policy.destination.v1"`。它**不是**升级能力，不进 `AgentUpgradeCapabilities`（同 `host.telemetry.v1`：缺了它不代表协议不兼容）。PolicyStatus 随这个能力一起提供。

**ETag 不变性**：`Policy == nil` 时，`ConfigBody` 的规范序列化必须与 v0.2.0 **逐字节相同**，加 conformance 测试锁住。

### 4.2 上行：`NodeReport.Audit`（模块版本 `v0.4.0`）

```go
type NodeReport struct {
    // ...existing fields...
    Audit *AuditObservation `json:"audit,omitempty"`
}

// 不带 engine / coverage：「这台节点在不在记录」由 PSP 自行推导（R17，§6 P6）。
// One immutable batch per kind and UTC hour. Counter-only batches are allowed:
// losses must remain visible even if no row survives. All state is in memory.
type AuditObservation struct {
    BatchID   string       `json:"batch_id"`   // 16 字节随机数的小写 hex（32 字符）；一个冻结批次一个，重发时原样沿用（§5 N6）
    Kind      string       `json:"kind"`       // block | observe | trial | usage
    Hour      int64        `json:"hour"`       // 整点 UTC 毫秒；本批全部行与计数属于这一小时
    CollectRevision uint64 `json:"collect_revision"` // 冻结时已部署策略的采集版本，>=1
    Hits      []AuditHit   `json:"hits,omitempty"`
    Usage     []AuditUsage `json:"usage,omitempty"`
    Dropped   uint64       `json:"dropped"`    // 聚合器键满而丢弃的事件数
    Unmatched uint64       `json:"unmatched"`  // 解析失败、无 email、未知规则的事件或行数
}

type AuditHit struct {
    Hour    int64      `json:"hour"`     // 整点 UTC 毫秒（节点收到事件的时间）
    RuleID  string     `json:"rule_id"`
    Action  string     `json:"action"`   // "block" | "observe"，取自出站前缀 psp-deny- / psp-watch-
    Subject SubjectKey `json:"subject"`
    Dest    string     `json:"dest"`     // 规范化主机名或规范 IP，不带端口
    Port    uint16     `json:"port"`
    Count   uint32     `json:"count"`
    FirstMS int64      `json:"first_ms"`
    LastMS  int64      `json:"last_ms"`
}

type AuditUsage struct {
    Hour    int64      `json:"hour"`
    Subject SubjectKey `json:"subject"`
    Site    string     `json:"site"`     // eTLD+1；目的地是 IP 时固定为 "(ip)"，不记 IP 本身
    Count   uint32     `json:"count"`
}
```

**`Action` 字段的用途**：同一个 `g<gid>` 在试运行时是 observe，执行时是 block；同一条全局策略的动作也可能在命中之后才被修改。入库与展示都按命中**当时**的动作来区分。

**批次种类**：`block` 只含 Action=block 的 Hits（包括白名单执行兜底）；`observe` 只含 `p*` 且 Action=observe 的 Hits；`trial` 只含精确 `g<gid>`（不含 x 后缀）且 Action=observe 的匿名 Hits；`usage` 只含 Usage。一个批次不得混合种类，种类按事件当时的规则与动作冻结，不按 PSP 当前定义重新解释。`Dropped`、`Unmatched` 是这一种类、这一小时的事件数；不含账号、目的地或分组明细。

**试运行行（R15）**：`RuleID` 以 `g` 开头、且 `Action = observe` 的 `AuditHit` 是白名单试运行的兜底命中，节点在聚合前就把它折叠成「分组 × 主域名」：`Subject` 必须为空，`Port` 必须为 0，`Dest` 必须是主域名（eTLD+1）或字面量 `(ip)`。其余命中的 `Subject` 必须非空。这样逐人逐主机的试运行数据从不离开节点。

**规范化**（Node 在聚合前做；PSP 入库前只做校验，不再改写；Protocol 只校验结果）：

- 主机名：转小写；IDN 用 `golang.org/x/net/idna` 的 Lookup profile 转 punycode；去掉尾点。
- IP：用 `netip` 的规范文本形式，不带方括号和端口。
- `Site` 与试运行行的 `Dest`：用 `golang.org/x/net/publicsuffix` 取 eTLD+1，IP 记为 `(ip)`。**主域名只在节点侧计算**：PSP 对 x/net 只是间接依赖，版本也与 Node 不同（F58），两边各算一次可能得到不同的主域名。

**`ValidateAuditObservation`** 的逐字段规则：

| 字段 | 规则 |
|---|---|
| `BatchID` | 32 位小写 hex |
| `Kind` | block / observe / trial / usage；行必须符合上面的批次种类，usage 批次不得带 Hits，其余不得带 Usage |
| `CollectRevision` | >=1；PSP 另外检查等于面板当前保存的 audit_collect_revision，不相等则丢弃，不影响控制响应 |
| 批次 `Hour` | 与逐行 Hour 同样的格式；本批全部行的 Hour 必须相等；丢弃与无法解析计数按事件接收小时累计 |
| `Hits`、`Usage` | 有行，或 Dropped/Unmatched 至少一项非零；全空全零不发送；允许只有计数的批次 |
| `Hour` | > 0，且是 3 600 000 的整数倍（PSP 另外检查它落在 [now−48h, now+1h] 内，超出的丢弃并计数） |
| `RuleID` | 同 §4.1 的正则 |
| `Action` | `block` \| `observe` |
| `Subject` | `g*` 且 `observe`（试运行行）时必须为空；其余必须是合法的 `SubjectKey` |
| `Dest` | 小写 ASCII 主机名（`^[a-z0-9._-]+$`）或 `netip.ParseAddr` 能解析且等于其规范形式；≤ 253 字节；不带端口。试运行行另外允许字面量 `(ip)` |
| `Site` | 同 `Dest` 的主机名规则，或字面量 `(ip)` |
| `Port` | 试运行行必须为 0；其余 1–65535 |
| `Count` | ≥ 1 |
| `FirstMS` / `LastMS` | `FirstMS ≤ LastMS`，且两者都在 `[Hour, Hour+3600000)` 内 |
| 整体 | 每次 `Hits` ≤ 4096 行、`Usage` ≤ 8192 行，序列化 ≤ 1 MiB（`MaxAuditObservationBytes`）；同一个 observation 内不得出现重复的聚合键 |

- **能力**：`CapabilityAuditHits = "audit.hits.v1"`、`CapabilityAuditUsage = "audit.usage.v1"`。
- **校验位置**：
  - 发送方的 ValidateNodeReport 要求 Audit 非空时必须有 audit.hits.v1；Kind=usage 时即使只有损失计数也必须有 audit.usage.v1。
  - Node **每一行在进入聚合器时**就做逐行规范化与校验，不合规的计入 `Unmatched`，不进聚合器；组装报告时再对整棵子树单独调用 `ValidateAuditObservation`，失败就丢掉整棵子树（§5 N6）。**绝不能让 `ValidateNodeReport` 因 Audit 失败**：那会让节点整轮收不到 roster（F50）。
  - PSP 照 Host 的做法隔离校验，并且**隔离解码**（§6 P4 第 1 条，F53）。
  - 字段类型保持 `uint16` / `uint32`：PSP 对这棵子树单独解码，类型越界与校验失败的结果相同（丢子树、计数），不会波及控制面，所以不需要为此改成 `int64`。
- **`partialReport`**：Audit（含 Kind/Hour/CollectRevision 与仅计数批次）和 PolicyStatus 都要加进去（F22）。
- **时间常量**：Protocol 定义 `MaxAuditReplayAgeMS = 48h`、`MaxAuditFutureSkewMS = 1h`、`AuditBatchRetentionMS = 72h`（均为毫秒常量）。Node 丢弃 `now > Hour + MaxAuditReplayAgeMS` 的整批 pending 与未冻结小时桶，PSP 按批次 Hour 检查 `[now−48h, now+1h]`。过期不再换 BatchID 重新发送；冻结后禁止删行重试。72h 从首次接收计算，大于最长可接收时长 48h + 1h，覆盖小时桶边界；清理只在到期后运行。测试注入时钟，不等待真实小时数。
- **非持久**（F15）：Node 的四个聚合器、各类 pending、损失计数都在内存，重启会丢失**全部尚未确认的积压**；正常同步且没有积压时通常只涉及最近一轮，连续失败时可以涉及整个 48h 重试窗口。键数、批次行数、序列化大小与 TTL 有界，**不是按同步周期数保证丢失量**。PSP 同步成功不代表遥测已落库，队列满、崩溃或部分入库失败仍会丢数据（P4）。写进协议注释、Node README 与 UI 帮助，不能描述成可靠审计证据链。

### 4.3 发版与跨仓开发流程

1. **Protocol 分两次发**：1a（下行 + PolicyStatus + 匹配函数 + 向量 + fuzz）合并后打 `v0.3.0`；2a（Audit）合并后打 `v0.4.0`。不要等 2a 一起发，否则会阻塞 Node 1b 和 PSP 1c。
   Protocol 没有 release workflow，tag 就是模块 tag：main 上的 `test.yml` 跑完且全绿后，推注解 tag 即可。按所有者的规矩，非正式发布不需要人工确认。
2. **API 门禁**：`test.yml:169`「exported API is compatible with the latest tag」跑 apidiff，加性改动不会报错。`.github/api-breaks.txt` 目前没有条目，只有确实不兼容时才在同一 PR 里登记，并在下一个 tag 打出后清空（见该文件头注释）。**本功能不应需要登记**；如果出现条目，说明设计走偏了，先停下来改本文。
3. **harness**：`harness.yml` 在每个改动 `protocol/**` 的 PR 上自动运行（非必需检查，`:28-30`）。ConfigBody 和 NodeReport 的变化预计会让它变红，按 README「Provenance」一节的规矩，在同一 PR 里记录这是有意的分歧，或者退役它。
4. **fuzz**：新增 `FuzzValidateDestinationPolicy`、`FuzzValidatePolicyStatus`（1a）和 `FuzzValidateAuditObservation`（2a），在 `.github/workflows/weekly.yml` 各加一个 `-fuzz` 步骤，照现有两个（`:41-46`）的写法。
5. **工具链**：main 上会用 go1.27.1 和 go1.26.8 跑测试（`test.yml:223-230`，PR 上不跑）。所以合并后要看 main 的那次运行，绿了再打 tag。
6. **跨仓开发**：本地可用不提交的 `go.work` 同时包含三个仓库，先用 `go env GOWORK` 检查当前环境，不能照抄其他机器的绝对路径。CI 是 `GOWORK=off`，所以 PR 里用 `go get github.com/KazuhaHub/passwall-protocol@<commit>` 的伪版本，或打 `v0.3.0-rc.N`。**禁止合并含 `replace` 的 go.mod。**
7. **Node 升级依赖后**：`cmd/contract-agent` 在同一个 PR 里适配签名变化；dependabot 对 protocol 的升级 PR 不用，手动升。**不扩展 `protocol/alias.go`**：它是待退役的兼容层，Node 自己的代码直接 import passwall-protocol，唯一的外部使用者只用到已有名字（F59）。除非出现需要新名字的外部消费者，否则不加别名。
8. **PSP 升级依赖后**：1c 合并时 `docs/compat/verification-v1.json` 的 `contract_source` 可以保持旧值：策略受能力门控，旧节点的契约照样成立；而 `contract_source` 只能指向**已发布**的 Node tag 加 commit（F59）。Node 1b 发布之后，用一个单独的 PSP PR（§11.1 的 1c′）把 `contract_source` 切到 1b 的 tag，并让 contract-agent 声明 `policy.destination.v1`，覆盖 PolicyStatus 的往返。PSP 的「node contract (pinned published source)」与「node compatibility (released range)」两个 job（`test.yml:50,209`）都要绿。
9. **发布顺序**：Protocol tag → Node（声明能力）发版 → PSP 发版。PSP 必须在「所有节点都是旧版」时照常工作；新 Node 配旧 PSP 也必须照常工作（F30：旧 PSP 静默忽略新字段）。完整的组合矩阵见 §11.2。

---

## 5. 节点实现（Passwall-Node）

### N0　热修：关掉泄漏的访问日志（最先做，单独发 Node 修复版）

**状态（2026-10-02 核实）：未做**（F10）。它**不依赖** Protocol 和 PSP 的任何改动，现在就作为独立任务派工。
版本号：Node 当前线是 4.0.1，修复版用第四段，**`v4.0.1.9`**（若在它之前已有别的发版，就取下一个第四段号）。

- 改动只在 `internal/core/xray/compiler.go` 及其测试：`c.AccessLog == ""` 时写 `config.Log["access"] = "none"`；`c.AccessLog` 非空时照旧使用（`:152-154`，测试与开发场景会用到）。
  B 档那一支（不写 `"none"`、让日志走 stdout 过滤器）**留到阶段 4**，由 N5 引入。N0 不碰 Policy。
- 测试（先写）：`AccessLog` 为空 → `access == "none"`；`AccessLog` 非空 → 原样使用。另外用真二进制跑 `xray run -test`，三个 catalog 版本都要跑（环境变量见 N8）。
- 发布：
  1. PR 合并后，等 main 上该提交的 `test.yml` **跑完且全绿**；
  2. 推注解 tag `v4.0.1.9`；
  3. `release.yml` 构建、签名并发布**预发布**版，不需要审批（#74 起 `release-signing` 环境已无审阅人）；
  4. 升为正式版走 `promote.yml`（`release-promotion` 环境，有审阅人），这一步由所有者决定。

  本改动不碰 `internal/upgrade/docker_*`，所以不需要等非必需检查 `docker-updater.yml`。
- **发布说明**（按原样写进 release notes）：
  - state schema 不变（仍为 9），可以直接用 PSP 远程升级下发：systemd helper（`helper_linux.go:138`）和 Docker updater（`docker_helper.go:880-885`）都只要求 schema 相同。
  - 升级后编译出的配置摘要会变化，`runtime.converge` 会重新部署：**每个节点的 core 重启一次，正在使用的连接断开一次**。建议分批远程升级。
  - **健康同步与部署条件下，升级后第一个同步周期（通常约 30 秒）内仍可能写出访问日志**：supervisor 启动时先用已持久化的旧 artifact 拉起 core（`main.go:177-191`，`RequireInitialConfigDigest`），要等首次成功 converge 才换成 `access:"none"`。离线、同步失败或部署失败时，旧 core 可能继续写日志，不能承诺 30 秒后自动停止；运维须检查实际部署产物与日志，未切换则按故障流程处理。
  - 清理旧日志：
    - systemd 节点：`journalctl --vacuum-time=…`。注意它作用于整台机器的 journal。
    - 经 PSP 远程升级的 Docker 节点：一般不用操作。升级成功后旧容器连同 json-file 日志一起被删除（F27）。但如果 updater 的日志里出现 `retained rollback container <名称>`（删除备份容器失败时保留它，`docker_helper.go:736-738`），执行 `docker rm <名称>` 清掉它的旧日志。
    - 手动部署的 Docker 节点：先把 `NODE_VERSION` 改成含 N0 的版本，再执行 `docker compose pull && docker compose up -d`；镜像一变，agent 容器就会重建，旧日志随之丢弃。**不要**在没改版本时执行 `down && up -d`，那样会用旧镜像重建，泄漏照旧。
    - 没有 shell 的 NAS 节点：updater 不低于 4.0.1.7 时，直接用 PSP 远程升级；否则在 NAS 的 Docker 界面把镜像 tag 改成 `v4.0.1.9` 再重建。**只按「重建」而不改 tag 不会生效**，那会用旧镜像重建。
- 验收：§12 第 1 条，systemd 和 Docker 部署各做一次，检查时间点是**升级完成 1 分钟之后**。

### N1　策略进入编译器

1. `core.Snapshot` 加 `DestinationPolicy *protocol.DestinationPolicy`。不叫 `Policy`，避免与 `xrayConfig.Policy`（F11）混淆。`runtime.converge` 在装配 Snapshot 处（`runtime.go:294` 附近）从 config 流里填入。
2. 测试：**只有 DestinationPolicy 变化**时，必须产生新 digest 并触发 Deploy。
3. 编译失败的处理：
   - 返回新类型 `*agentcore.PolicyError{Code, Listeners}`。它**不实现成** `ObjectError`：包成「字典序第一个 listener」的 ObjectError，会在那个 listener 本轮也有变化时把一个无关的 listener 标成 `ObjectRejected`（`runtime.go:261-275` 的 `objectResult`）；只有策略变化时它又根本到不了任何对象（`processor.go:254-257`）。策略失败**只通过 PolicyStatus 上报**，其他对象照常走 retryable 路径。
   - 把 `PolicyStatus` 置为 `{Digest: 期望摘要, State: "rejected", IssueCode, Listeners}`（N3 规定了何时才算「被拒」）。
   - **绝不静默去掉策略继续部署**：那等于把拦截和白名单全部放行。
   - 代价是 F25：这份 config 整体不部署。所以 PSP 会预检并回退上一版（§6 P3），节点侧这条只是第二道防线。
4. 嗅探（F4）：只在存在 Domains 或 Protocols 规则、**且 resolve 后的 engine == xray** 时处理。sing-box 不认 per-inbound sniffing（遇到启用了嗅探的入站直接报错，`singbox/compiler.go:184-186`），它只依赖 `route.rules` 里的 `{"action":"sniff"}`（N1-S），不做这一步。
   - **未启用**嗅探的 listener：注入 `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":true}`。`routeOnly` 保证不改变实际连接目标。
   - **已启用**嗅探的 listener：出现以下任一情况，编译器返回 `PolicyError{Code: "destination_policy_sniffing_insufficient"}`，并在 PolicyStatus 的 `Listeners` 里列出这些 listener：
     - `metadataOnly: true`；
     - `destOverride` 缺少 `http`、`tls`、`quic` 中的任何一个；
     - 存在 `domainsExcluded`。

     **不擅自改写**：`routeOnly:false` 时追加 destOverride 会改变真实的连接目标。
   - 判据要通过 `conformance.SniffingVectors`（§4.1），保证与 PSP 预检一致。向量只适用于 Xray。
   - PSP 新建入站的默认值是 `enabled:true, destOverride:[http,tls,quic,fakedns], routeOnly:false`（`web-react/src/views/admin/NodesView.tsx:460-463`），满足条件。
5. **空闲短路**（F52）：策略最多 4 MiB，不能每 30 秒全量编译并把它写回 SQLite 一次（NAS 的 eMMC 会被明显加速磨损）。
   - runtime 记下上一次**成功** converge 的输入指纹：config ETag、roster ETag、本地 gate 摘要（`store.Clients` 里影响 `Enabled` 的那部分）、resolve 后的 core 选择，以及**截至 `now` 已到期的 client 集合**。最后一项不能省：两个编译器都按 `snapshot.Now` 剔除到期的 client（`xray/compiler.go:120`、`singbox/compiler.go:98`），漏掉它，到期循环唤醒的那次 converge 会被短路，到期用户继续可用。指纹不变、且 supervisor 仍在运行同一 digest 时，`converge` 直接返回，不编译、不写库。
   - `saveDeployment` 在 digest 与两个 body 都和上一次保存的相同时，**只更新 `applied_at_ms` 一列**，不重写 artifact 与 body。这一列不能不动：`nextExpiry` 用它判断到期处理到了哪里（`runtime.go:183-185`），不前移会让到期循环对同一个时刻反复唤醒。
   - 测试：连续两轮无变化 → 第二轮编译器调用次数为 0、整行写入次数为 0；本地 gate 关闭 → 指纹变化，照常编译；一个 client 到期 → 指纹变化，编译结果不含它；到期但 digest 不变（该 client 已被 gate 关掉）→ 只更新 `applied_at_ms`，到期循环不再对同一时刻唤醒。

### N1-X　Xray 编译（`internal/core/xray/compiler.go`，新文件 `policy.go`）

`xrayConfig` 加 `Routing map[string]any \`json:"routing,omitempty"\``，没有策略时不出现。按 §3.2 顺序生成，每条规则的形状如下：

```json
{"type":"field","ruleTag":"psp-p12","user":["u5@psp.local","u5-k1a2b3c4d@psp.local"],
 "domain":["domain:example.com","full:a.example.org"],"port":"443","network":"tcp",
 "outboundTag":"psp-deny-p12",
     "webhook":{"url":"/opt/passwall-node/data/runtime/audit.sock:/hit","headers":{"X-PSP-Audit":"<token>","X-PSP-Audit-Revision":"<collect_revision>"}}}
```

- **`Subjects` → `user`**：从**本次部署的** roster 里取这些 subject 的**全部** client username（F31）。
  - 展开后为空 → **跳过整条规则**（F2）。加测试断言编译结果里不存在 `"user":[]`。
  - `Subjects` 为空（所有人）→ 不写 `user` 字段。
- **`Exempt`** → §3.2 第 2 条：`{"ruleTag":"psp-exempt","user":[豁免者 email],"outboundTag":"direct"}`；展开为空则不生成。
- **拆分**：同一条规则里既有 `Domains` 又有 `CIDRs`/`Private` → 拆成两条，出站和 webhook 相同（F3），`ruleTag` 分别加后缀 `a`/`b`。
- **`Private`** → `ip` 字段写入 F9 那张私有与保留地址表，**与 Xray `common/geodata/consts.go` 逐项一致**，保证命中记录与 freedom 实际拦截的集合相同。
- **`Protocols: ["bittorrent"]`** → `"protocol":["bittorrent"]`。
- **`CatchAll`** → 只写 `user` 字段。
- **出站**：每条 block 规则一个 `{"tag":"psp-deny-<id>","protocol":"blackhole"}`，每条 observe 一个 `{"tag":"psp-watch-<id>","protocol":"freedom"}`；保留原有的 `direct`、`blocked`。
  - **`outbounds[0]` 必须是 `direct`**（现状如此，`xray/compiler.go:147-148`），新增出站一律追加在末尾。Xray 在没有规则命中时走第一个出站，把 `psp-deny-*` 放到前面就等于全部拦截。加测试断言。
- **`routing` 显式写 `"domainStrategy":"AsIs"`**：§10 第 3 条依赖 AsIs 的语义，不能靠默认值。
- **webhook**：
  - 只挂在 `psp-deny-*` 与 `psp-watch-*` 规则上，并且只在 `Collect != "" && c.AuditSocket != ""` 时挂。`AuditSocket` 是新的编译器字段，**只有** `cmd/node/main.go` 在 N4 接收器真正监听之后才设置（R4）；否则不挂 webhook，也不声明 `audit.hits.v1`（N7）。
  - `deduplication` 不设（要计数，不去重）。
  - socket 路径 = `<DataDir>/runtime/audit.sock`（绝对路径）。路径超过 100 字节时不监听、不挂 webhook、不声明 `audit.hits.v1`。
  - **鉴权头**（R9）：`headers: {"X-PSP-Audit": <token>}`。
    同时带 X-PSP-Audit-Revision，值来自本次编译 Policy.CollectRevision。sink 只接收与已部署采集版本一致的事件，迟到的旧 core webhook 不得被重新贴成新版本。
    - 令牌用现成的 `loadOrCreateSecret(filepath.Join(DataDir, "secrets", "audit-webhook"))` 生成和保存，与 sing-box API 密钥同处（`cmd/node/main.go:155,455`），首次启动生成，之后一直沿用。不要每次启动重新生成：令牌在编译产物里，一变就会改变部署摘要，每次 agent 重启都会让 core 重启一次。
    - 令牌不进任何日志。

### N1-S　sing-box 编译（`internal/core/singbox/compiler.go`，新文件 `policy.go`）

- 有 Domains 或 Protocols 规则时，`route.rules` 第一条是 `{"action":"sniff"}`（F21）。
- 规则字段：
  - `auth_user`：sing-box 按 inbound user 的 `name` 匹配，编译器以 `Credentials.Username` 作为 name（`:263-267`）；
  - 前缀映射：`domain:`→`domain_suffix`、`full:`→`domain`、`keyword:`→`domain_keyword`、`regexp:`→`domain_regex`；
  - `ip_cidr`；`Private` → `ip_is_private: true`；
  - `Ports` → `port: [int]` 与 `port_range: ["a:b"]`（**sing-box 用冒号**）；
  - `network`；`protocol: ["bittorrent"]`。
  - **不拆分** domain 与 ip（F3）。
- 动作按 §3.1；observe 出站为 `{"type":"direct","tag":"psp-watch-<id>"}`。
- sing-box 没有 webhook：**首版只执行、不采集**，不发 Audit。PSP 按 engine 自行推导出「只执行」（R17，§6 P6），节点不需要另外上报。
- 不做 per-inbound 嗅探的注入与检查（N1 第 4 条只适用于 Xray）；嗅探只靠 `{"action":"sniff"}`。
- **N0-S**（§1.2 Q1 的默认）：先用真 sing-box 验证字面 IP 与解析到私有 IP 的域名两种请求。确认风险后，在 `route.rules` 最前面无条件加 `{"ip_is_private":true,"action":"reject"}`，与 Policy 是否存在无关；它只承诺覆盖路由当时可见的 IP，不能把未 resolve 的域名也写成已受保护。`singbox/compiler_test.go` 加用例并用真二进制复验；若验证结果表明该规则不足，先记录剩余风险、修订这一项，再扩大热修，不能假称元数据风险已全部消除。

### N3　PolicyStatus

- runtime 在 `saveDeployment`（`runtime.go:354`）成功之后，把**已部署**策略的 `PolicyDigest` 写进内存状态：`{Digest, State:"applied"}`。
- **只有两种情况置 `rejected`**（F51：runtime 本来分不清失败的原因，必须专门判别）：
  1. `Compile` 返回 `*agentcore.PolicyError`（N1 第 3、4 条），`IssueCode` 取它的 `Code`；
  2. `validateCandidate` 明确拒绝了候选配置（下面的 `process.ErrCandidateRejected`），**并且**把同一份快照去掉 `DestinationPolicy` 后重新编译、再跑一次 `-test` 能通过。这时才认定是策略导致，`IssueCode = destination_policy_rejected`。

  其余部署失败一律**不改** PolicyStatus，保持上一个值，交给已有的 `core_convergence_failed` 处理：core 安装或下载失败、`-test` 超时、stop 失败、去掉策略之后仍然失败（多半是某个 listener 的配置被新核心拒绝）。否则 PSP 会把一个与策略无关的故障当成「策略被拒」，进入回退，再按 §9.2 把这台节点判为对白名单分组不合格，删掉组员的 client；而 `rejected_generation` 锁住之后，同一 generation 内不会再试。
- supervisor 导出可判别的错误 `process.ErrCandidateRejected`（用 `errors.Is` 判断）：`validateCandidate` 里 `-test` 进程正常结束但退出码非 0 时包它；超时（`checkCtx.Err() != nil`）与 exec 失败**不**包它。去掉策略的复检只在 `ErrCandidateRejected` 时做，只多跑一次 `-test`，不部署。
- **安装器的提前校验**：`install.execVerifier.Verify` 也会校验 `Request.CurrentConfiguration`。带 Policy 的 converge 不向安装器传候选配置，安装器仍验证 catalog、下载校验和与二进制版本；候选配置统一交给 supervisor 在任何 stop/switch/start 之前校验。否则策略会在安装路径被拒而没有 `ErrCandidateRejected`，PSP 无法启动策略回退。无 Policy 时保留现有安装器预检。测试断言该分工，并覆盖 supervisor 拒绝时旧部署和对象的 retryable 语义。
- **重启恢复**（F26）：新增 `(*Runtime).RestoreStatus(ctx) error`：
  - 读 `store.CoreDeployment(ctx)`，解出 `ConfigBody.Policy` 与 `RosterBody`；
   - 设置 PolicyStatus 的已应用摘要，并调用 N4 的 sink.SetRoster(saved roster, ruleIDs, saved.Policy 的 Collect/CollectRevision)；阶段 4 的 stdout 初始部署 Writer 同样绑定保存的 revision，不从 PSP 当前期望值反推旧进程；
  - 读不到记录（全新节点）时，PolicyStatus 为 `{Digest:"", State:"applied"}`；
  - `cmd/node/main.go` 在构造 runtime 之后、`lifecycle.Run` 之前调用它，所以一定早于 sink 开始接收和第一份报告的构建。

  否则 agent 重启后若 config 没变，就不会重新部署（N1 第 5 条的空闲短路更是如此），PSP 会永远显示「下发中」，并把重启后的命中全部算作 Unmatched。
- 节点有 `policy.destination.v1` 能力时，每次上报（full 与 partial）都带 `PolicyStatus`；挂上之前先单独 `ValidatePolicyStatus`，失败就不带（§4.1）。

### N4　A 档：webhook 接收器（`internal/agent/auditsink/`，新包）

1. agent 启动时在 `<DataDir>/runtime/audit.sock` 监听 unix socket：
   - 先 `os.MkdirAll(<DataDir>/runtime, 0o700)`：全新安装上这个目录未必已经存在；
   - 删除残留的 socket 文件；权限 0600；systemd 下 DataDir 是唯一可写目录，符合 rootless 约束；
   - `http.Server` 设 `ReadHeaderTimeout: 2s` 与 `MaxHeaderBytes`（如 8 KiB）；
   - **不用** Linux abstract socket（`@…`）：它绕过文件权限，本机任何进程都能伪造命中。

   监听成功后，才把 `AuditSocket` 交给编译器（N1-X）。
   **sink 作为 `lifecycle.Service` 运行**（与 `core supervisor` 等并列，`cmd/node/main.go:359-366`）：`Serve` 出错或 panic 就返回错误，让 agent 退出，由 systemd 或 Docker 重启，与 supervisor 的失败语义一致。**不允许在声明了 `audit.hits.v1` 之后悄悄降级**：那样 PSP 会继续下发 `Collect=hits`，webhook 全部失败，命中静默丢失，界面却仍显示「在记录」。
2. 极简 HTTP handler：
   - 只接受 `POST /hit`；请求体 ≤ 4 KiB；
   - 请求头 `X-PSP-Audit` 与令牌不符 → 401，计数；
   - 解析 F5 的 JSON，取 `email`、`destination`、`outboundTag`；**丢弃 `source` 字段**（来源 IP 不进任何结构）；
   - `destination` 用 `net.SplitHostPort` 拆出主机与端口，拆不开的计入 `Unmatched`；主机按 §4.2 规范化，**规范化后的这一行就按 §4.2 的逐行规则校验**，不合规的计入 `Unmatched`，不进聚合器；
   - 立即回 204。handler 内只做一次 map 更新，**不做任何 I/O**：Xray 每个事件都占一个 goroutine 等响应（5 秒超时）。
3. `outboundTag` 以 `psp-deny-` 开头 → `Action = block`，以 `psp-watch-` 开头 → `observe`；去掉前缀得到 rule ID。不在当前已部署策略里的 → `Unmatched`。
4. email → subject：读 runtime 在 `saveDeployment` 成功后（以及 N3 启动恢复时）推送的快照 `SetRoster(map[email]SubjectKey, ruleIDs)`。要取**已部署的**，不是最新存储的。查不到 → `Unmatched`。
   这份快照还含 Collect/CollectRevision；sink 先验证请求 revision 与快照匹配，旧版本只计本地 stale_collect_revision，不进入新版本聚合器。off 时拒绝全部事件，避免改档边界重贴标签。
5. **按种类分开聚合**：block 与 observe 都用键 `(hour, ruleID, action, subject, dest, port)`，分别上限 20 000 与 10 000 键；不共享容量、计数或 pending。满了只增加本种类、本小时的 `Dropped`，不扩容。未知事件能确定种类时计入该类 `Unmatched`；无法判定种类时只记节点本地错误计数，不伪造一种 Audit。
6. **试运行行走独立的聚合器**（R15、R19）：精确 `psp-watch-g<gid>` 的事件，在节点上直接用 `publicsuffix` 把主机折叠成主域名（IP 记为 `(ip)`），`Subject` 置空、`Port` 置 0，放进 trial 聚合器，键 `(hour, ruleID, dest)`，上限 10 000 键。g 的 x 后缀不是试运行兜底，拒绝伪造的这种事件。四类按 N6 独立发送，低优先级数据不能占用 block 的聚合、pending、PSP 队列或入库配额。各小时桶按 §4.2 的 48h TTL 清理。
7. 压测（验收必做，§12）：在 VM 上每秒制造 1000 个被拦截的连接；另做一个场景，50 条带 webhook 的规则同时命中；再做一个试运行场景：一个试运行分组，200 个虚拟用户，名单外流量每秒 1000 次。同期 block 流量保持在自己的键数、发送和入库预算内，断言 trial 达到上限不会让 block 因共享配额而丢失。再独立制造 block 超额，确认正确计数丢失且控制同步不受影响。记录 Xray 的 goroutine 数、文件描述符数和 agent CPU。
   若 goroutine 持续堆积，退路是给 webhook 设 `deduplication: 1`（每用户每秒最多一条），计数改为「至少」语义，界面标注。

### N5　B 档：访问日志过滤器（`internal/agent/auditlog/`，新包；仅在 `Collect == "hits_and_usage"` 且 engine = xray 时启用）

1. 过滤器接入 core stdout，保留 process.Options.Stdout 默认路径；B 档新增可选 `StdoutForDeployment func(agentcore.Deployment) io.Writer`，为每次实际启动的 core 创建绑定部署 CollectRevision 的 Writer，读取迟到旧日志时不能按全局当前 revision 重贴标签。Deployment 与进程内 Status 增加 CollectRevision，runtime 从本次 Policy 传入；启动恢复从持久化 ConfigBody.Policy 读，不改 state schema。revision 改变时 runtime/supervisor 即使 artifact digest 相同也要重新部署，确保无规则的 B 档进程换用新 Writer。exec 对非 *os.File 的 Writer 起拷贝 goroutine，**Write 绝不能阻塞**：
   - 按行切分（bufio，单行上限 8 KiB，超长截断），把行与该进程固定的 revision 非阻塞投递到容量 4096 的 channel；旧 revision 的行直接丢弃，只记本地 stale_collect_revision，不能进入当前用量桶。满了计本种类 Dropped；
   - 转发到 agent stdout 也走独立的非阻塞队列；
   - 解析 goroutine 带 panic 防护；panic 或退出时，过滤器仍先吞掉访问行，再把错误交给 lifecycle 让 agent 退出重启，不能在已声明 `audit.usage.v1` 后静默停止统计并继续运行。转发路径始终不输出访问行。
2. 编译器新增 `AccessLogFiltered bool`，**只有** `cmd/node/main.go` 在过滤器已作为 Stdout 接入之后才置 true（R4）。规则：
   - **只有** `DestinationPolicy.Collect == "hits_and_usage" && AccessLogFiltered` 时，才不设 `log.access`（日志走 stdout，由过滤器吞掉）；
   - **其余情况一律 `"none"`**。
   - 测试（先写）：`Collect=hits_and_usage` 且 `AccessLogFiltered=false` → `access` 必须是 `"none"`。
3. 访问行判定：匹配 `^\d{4}/\d\d/\d\d \d\d:\d\d:\d\d\.\d{6} from \S+ (accepted|rejected) ` 的行**一律不转发**（它们带来源 IP）。
4. 解析正则：
   `^\d{4}/\d\d/\d\d \d\d:\d\d:\d\d\.\d{6} from (\S+) (accepted|rejected) (\S*)(?: \[(\S+) (?:->|>>|==>) (\S+)\])?(?: (.*?))?(?: email: (\S+))?$`
   - `rejected` 行、无 email 的行 → `Unmatched`，不聚合；
   - 出站 tag 以 `psp-deny-` 开头的行不计用量：它们是被拦截的连接尝试，即使日志写作 accepted，也不表示目的连接成功；过滤器**从不写 Hits**，命中只由 webhook 统计。
   - 其他 `accepted` 行（**包含 `psp-watch-*`，也包含试运行的 `psp-watch-g<gid>`**）→ 用量：目的地去掉 `tcp:`/`udp:` 前缀与端口（IPv6 形如 `[::1]:443`），用 `golang.org/x/net/publicsuffix` 取 eTLD+1，IP 记为 `(ip)`；得到的行按 §4.2 逐行校验，不合规的计入 usage 的 `Unmatched`，不进聚合器。仅观察的一次连接可同时贡献一条 A 档命中和一次 B 档用量，两个表口径不同，不是重复命中。
   - 用量是「日志中正常放行连接的次数」，不是字节数，也不保证远端应用请求成功。未开启 B 档时，试运行仍只有匿名的 trial 行；显式开启 B 档后该节点按既有 B 档规则记账号与主域名，UI 和隐私页必须披露这一点。
   - 小时桶用节点收到这一行时的 UTC 时间，不解析日志里的时间前缀（那是节点本地时区）。
5. 聚合键 `(hour, subject, site)`，上限 50 000。email → subject 用 N4 同一份已部署快照。

### N6　上报组装（`internal/agent/report.go` 等）

1. **四类独立 pending，批次冻结后原样重发**（照 Host 的 `cached`，F50）。每类最多一个 pending、各 ≤ 1 MiB，总 pending ≤ 4 MiB。不能失败后 Merge 并沿用 BatchID，也不能改 BatchID 重发相同数据。规则如下：
   - 发送调度的循环槽为 `block, block, block, block, observe, trial, usage`；跳过空槽，空出的机会先借给 block，再按槽顺序借给有数据的其他类。每个非空低优先级类在一个完整槽循环内至少有一次发送机会，block 至少有四次；网络失败也推进槽，避免一个失败 pending 卡住全部类别。每轮 NodeReport 最多携带一个 Audit，控制字段优先。
   - 只从本轮选中的类 `Take()`：有 pending 就使用它；没有 pending 就取该类**最旧的未过期小时桶**。一批只含一个小时，不混入其他类。被选类以外的聚合器与 pending 保持不变。
   - 经过下面第 2 条的按行裁剪之后，剩下的行**冻结**为 pending 批次，配一个新 `BatchID`；被裁掉的行在冻结之前退回聚合器；
   - 该类有 pending 时，只带这一批、不再 Take 该类新行；该类新事件在自己的聚合器积累，其他类仍可正常冻结和发送；
   - 发送失败（网络错误，或响应校验失败）都**原样重发 pending**：同一个 BatchID、同一组行，不 Merge；
   - 响应校验通过后，**只确认本轮确实带入线上报告的 BatchID**。Audit 被延期、剥离，或本轮只发另一类时，不得清除未发送的 pending。PSP 对重复、档位关闭、超额或队列满的合法报告仍回 200，收到有效控制响应就视为尽力交付结束，不要求落库确认。
   - 每轮构建前按 §4.2 清理过期 pending 与小时桶，增加节点本地 `audit_expired_dropped` 计数（行数与事件数分开），不修改或重新编号已冻结批次。聚合器 Count 达到 uint32 上限时饱和，新增事件转入该小时 Dropped；不得溢出成 0 后让整批校验失败。

   Build 改为接收一个 options 结构，同时容纳 Host 与 Audit，不再加位置参数；在 `report.Host = host` 之后挂上 Audit。
2. `fitReportToWire`（`report.go:163`）的丢弃顺序改为：Host → 本轮 Audit 的行逐行裁剪 → 部分上报。新批次按对应行数上限与 1 MiB 子树上限裁剪，固定排序 `(hour, ruleID/subject, dest/site, port)` 保证可复现；裁掉的行回到原类原小时桶。**逐行裁剪只发生在冻结之前**；pending 一旦冻结就不再裁剪，若重发时整份报告放不下，就把这一轮的 Audit 整棵拿掉、pending 留到下一轮，不改变内容也不确认。`BuiltReport` 照 `HostDropped` 的写法加 `AuditKind`、`AuditBatchIDSent`、`AuditUsageTrimmed`、`AuditHitsTrimmed`、`AuditDeferred`，供测试断言；只有确实丢失才计 Dropped，延期与返回聚合器不算丢失。
3. **校验隔离**（F50）：组装时先对 Audit 子树单独调用 `ValidateAuditObservation`。失败就丢掉整棵子树（连同 pending）、计 `audit_invalid_dropped`、打一条只含计数的 Warn，**绝不能让 `ValidateNodeReport` 因 Audit 失败**；否则一行坏数据会让节点每一轮都收不到 roster，被停用或删除的用户在这台节点上一直能用。PolicyStatus 同理：先单独 `ValidatePolicyStatus`，失败就不带。逐行校验已经在进入聚合器时做过（N4、N5），这一步是第二道防线。
4. full 与 partial 都参与同一槽调度；无行但有本类本小时的 Dropped/Unmatched 时，允许冻结成仅计数批次；全空全零不发。每类每小时的损失计数槽同样按 48h TTL 保留，最多 50 个小时桶；过期的损失计数只计节点本地过期指标，不伪装成当前小时的事件。
5. **关闭、降档与 revision 切换**：runtime 在新档位成功部署后通知各采集器；Collect 为空时停止新事件并清空四类聚合器、损失计数与 pending。任何新的 CollectRevision 到达都清空旧 revision 的四类内存数据，再开始采集；因此 B→A 时，旧 Usage 和旧 Hits 都可能损失，新 A 档命中照常继续。冻结时绑定已部署 revision，不能给旧批次换成新 revision。
   - PSP 保存档位实际变化时，档位与 revision+=1 在同一个事务提交；Node 即使没收到中间 off、直接收到再次开启的同档位新 revision，也必须清空旧数据。已开始的 HTTP 请求无法撤回，由 PSP 当前档位与 revision 闸门处理。清空计 audit_collect_off_dropped；PSP 保存 off 后到 Node 收到新配置间仍可能本地采集，但不再落库。已有历史不立即删除。

### N7　能力声明

三个新能力都加进 `cmd/node/main.go:308` 的**静态** `capabilities` 切片（与 `host.telemetry.v1` 同处），**不放进 `CapabilitySource`**（F24）：

- `policy.destination.v1`：两种 engine 都支持执行，始终声明。
- `audit.hits.v1`：N4 接收器监听成功才声明；监听失败则不声明，并打一条 Warn。声明之后 sink 出错就让 agent 退出重启（N4 第 1 条），不会出现「声明了却不在收」。
- `audit.usage.v1`：阶段 4 起始终声明。sing-box 节点不发 Audit，PSP 按 engine 推导出「只执行」（R17），不需要节点另外表达。

不按 engine 动态声明，是因为 PSP 可以在运行中更换 engine：动态声明会让能力随换核闪烁，触发 §9.2 的白名单重同步。

### N8　测试与完成判据（Node）

- 编译器测试：表驱动，断言解码后的 JSON（沿用现有 `compiler_test.go` 的风格，不用 golden 文件）。必须覆盖：
  - 无策略 → 没有 `routing`，且 `access:"none"`；
  - §3.2 顺序（allow → exempt → block → 白名单 → observe）；
  - 一人多 email 的展开；
  - subject 不在 roster → 整条跳过，且没有 `"user":[]`；
  - Xray 拆分 domain 与 ip、sing-box 不拆；`Ports` 两种写法；Private 两种编译（Xray 的列表与 F9 逐项相同）；
  - CatchAll；
  - 嗅探（仅 Xray）：注入、不足时返回 `PolicyError`，并通过 `conformance.SniffingVectors`；sing-box 不做这一步；
  - webhook：只挂在 deny/watch 规则上，且只在 `Collect != "" && AuditSocket != ""` 时挂；`headers` 带令牌；令牌在重启前后不变，编译摘要也不变；
  - `outbounds[0]` 始终是 `direct`；`routing.domainStrategy == "AsIs"`；
  - B 档的 `AccessLogFiltered` 规则。
- 真二进制：
  - catalog 的**全部三个** Xray 版本（`PSP_TEST_XRAY_26627_BIN`、`PSP_TEST_XRAY_26728_BIN`、`PSP_TEST_XRAY_2699_BIN`；`PSP_TEST_XRAY_BIN` 只是 26.6.27 的回退，见 `xray/compiler_test.go:150-158`）跑 `xray run -test`；
  - `PSP_TEST_SING_BOX_BIN` 跑 `sing-box check`；
  - 至少一份 50 000 条域名的配置，在 VM 上记录一次启动耗时，以及**空闲状态**下每轮的 CPU 占用和 `state.db` 的写入字节数（N1 第 5 条的短路要让后两者接近 0）；
  - 26.6.27 下，带 `X-PSP-Audit` 头的 webhook 请求能被 sink 收到（F5 的头部支持在最旧的 catalog 版本上实测）。
- webhook 接收器：真 Xray 在 VM 上命中后，聚合器里出现正确的 (rule, action, subject, dest, port)；来源 IP 不出现在任何结构里；令牌不符返回 401。
- 日志过滤器：fixture 用真 Xray 在 VM 上抓一批行，覆盖 `->`、`>>`、`==>`、IPv6 `tcp:[::1]:443`、UDP、`rejected`、无 email、mux 子连接；背压测试：下游不读时 core 不阻塞。
- 上报：
   - 冻结批次：同类发送失败后重发逐字节相同；该类 pending 未确认时不 Take 新行，但其他类仍发送；第一次已入库但响应丢失 → 重发计数不翻倍，预算、TTL 和容量以内的新事件随后到达；
  - 按行裁剪只发生在冻结前，被裁的行回到聚合器；冻结后放不下 → `AuditDeferred`，内容不变；
  - **往聚合器注入一行非法数据**（绕过入口校验）→ `SyncOnce` 仍然成功，roster 照常应用，Audit 被丢弃并计数；
   - 试运行行在节点上已折叠：没有 Subject、Port 为 0、Dest 是主域名；四类排队、槽公平性、计数批次、48h 过期、Count 饱和、off 与 revision 切换清空旧数据、降档后新 Hits 继续；
   - B 档 fixture：同一次 watch 连接产生一次 Hits 与一次 Usage，deny 不产生 Usage，trial 在未开 B 档时无逐人用量，过滤器 panic 让 lifecycle 退出且不转发访问行；旧进程 stdout 与旧 revision 的 webhook 不得进入新 revision 的桶；仅 B 档无规则、artifact digest 相同时 revision 变化仍重新部署。
- runtime：只有 DestinationPolicy 变化时触发部署；空闲短路（N1 第 5 条）；PolicyStatus 的状态迁移；**重启后第一次上报就是 applied**，并且能把命中解析到用户（`RestoreStatus`）。
- 「被拒」的归因（N3），三例：某个 listener 的配置被核心拒绝 → 不产生 rejected，PolicyStatus 保持原值；`-test` 超时 → 不产生 rejected；只有策略导致的 `-test` 失败（去掉策略后能通过）→ 产生 rejected。
- sink：`Serve` 返回错误 → `lifecycle.Run` 返回错误（agent 退出）；全新 DataDir 下 `runtime/` 不存在也能监听。
- **完成判据**：`go test ./...`、`go vet`、仓库现有 lint 全过；`deployment/coreacceptance` 的全部叶子测试都跑且通过。

---

## 6. PSP 实现

### P1　数据模型（`internal/domain` + `internal/adapters/sqlstore`，走 F35 的具体 repo 模式）

通用规则：
- 所有表进 `schemaModels`。
- `type:text` 列不带 DEFAULT（`TestSchemaNoDefaultOnTextColumns`）；JSON 切片字段实现 `GormDBDataType() = "text"`。
- **三方言测试都要跑**（`PSP_TEST_DB_KIND`，见 CLAUDE.md）。
- 新 repo 照 flagRecords、riskReviews 那段在 `app.go` 里构造（`:589,607`）；setter 一律 nil-tolerant，并写明降级行为。
- 照 `TestBuildWiresTheFlagRecorders` 加接线守卫测试 `TestBuildWiresTheDestinationRepos`。

**定义写事务与编辑版本**：
- 策略、列表、豁免、分组模式及阶段、自带列表、排序和到期清理，共用定义写事务入口：先确保并锁住 `dest_policy_state(id=1)`，再读写涉及的定义行，最后在同一事务推进 generation 和去抖时间。任何一步失败全部回滚；不允许先改定义、再另开事务加 generation。所有入口保持这个锁顺序。
- `PUT` 的 `updated_at` 比较在该事务内执行。该字段是 UTC 毫秒编辑版本：成功修改时取 `max(now_ms, previous_updated_at_ms + 1)`，即使同一毫秒内连续保存或时钟回拨，也不能沿用旧版本。比较完整的毫秒值，不转成秒、不宽松接受旧值；冲突时定义、generation 和调用方成功结果均不推进。
- 排序入口在锁内核对该动作的全部 ID（含停用策略），拒绝重复、缺失或额外 ID；被改动 priority 的策略同时推进自己的编辑版本，避免旧编辑表单随后写回旧顺序。创建及改变动作的服务端 priority 分配也在锁内完成。
- 一次请求包含多行改动时只推进一次 generation（如首次创建全局例外、open→trial、排序）；没有有效改动不推进。后台刷新摘要不变时只更新刷新元数据，不推进 generation。
- 三方言测试必须证明同毫秒编辑、过期编辑、并发创建 priority、完整排序校验和注入故障的事务回滚，不能仅检查表是否存在。
- 可以提前创建后续阶段的空表以简化加性迁移，但这只算 schema 准备：相应阶段之前不得启动采集、入库、查询 API 或清理作业，不得产生逐人 Usage。阶段完成仍按 §11.1 的行为与验收范围判定。

| 表 | 列 | 说明 |
|---|---|---|
| `dest_lists` | `id`；`name` varchar(128)；`kind` varchar(16)（`custom`/`remote`/`geosite`）；`source_url` varchar(1024)；`geosite_category` varchar(128)；`geosite_attrs` varchar(128)；**`entries` `[]byte`**（规范化条目，一行一条；MySQL 映射 longblob，F36）；**`source_text` `[]byte`**（只对 custom：管理员粘贴的原文，含注释和行号，≤ 4 MiB；S7 编辑时回填用）；**`parse_report` text(JSON，可空、无 DEFAULT)**（最近成功内容的解析报告，P2）；`entry_count` int；`regexp_count` int；`content_sha256` varchar(64)；`last_fetched_at`；`last_error` varchar(512)；**`owner_group_id` bigint NOT NULL DEFAULT 0**；`created_at`/`updated_at` | `owner_group_id` 非 0 表示这是某个白名单分组自带的列表（基础放行或补充），只能在那个分组里使用（§9.3）。三方言测试插入 2 MiB 的 entries。**没有**每列表的刷新间隔：远程与分类列表一律按全局 `dest.list_refresh_hours` 刷新（S6、S7 只显示全局值） |
| `dest_policies` | `id`；`name` varchar(128) 唯一；`action` varchar(16)；`list_ids` text(JSON)；`inline` text(JSON：cidrs、ports、network、protocols、private)；`scope` varchar(16)（`all`/`groups`）；`group_ids` text(JSON)；`priority` int；`enabled` bool；`counts_as_risk` bool；**`template_key` varchar(32) NOT NULL DEFAULT ''**（由哪个模板创建，S4 的「已添加」徽章用）；时间戳 | `PUT` 带 `updated_at` 作为前置条件，冲突返回 409 `dest_policy_stale`。`priority` 由服务端分配（P7） |
| `dest_exemptions` | `user_id` PK；`reason` varchar(255)；`created_by` bigint；`created_at`；**`expires_at`（可空）** | R8；过期行由清理循环删除，删除算一次定义写入 |
| `dest_group_modes` | `group_id` PK；`mode` varchar(16)（`open`/`allowlist`）；`stage` varchar(16)（`trial`/`enforce`）；`list_ids` text(JSON)；`base_list_id` bigint；**`extra_list_id` bigint**；`stage_changed_at`；`updated_at` | §9。不往 `groups_` 加列（分组行被多处按列写） |
| `dest_policy_state` | 单行（`id = 1`）：`generation` bigint；`published_generation` bigint；`first_unpublished_at`（可空）；`last_write_at`；`published_at`；`paused` bool；**`publish_error` text(JSON `{kind,used,limit}` 或 `{kind:"invalid",field}`，无 DEFAULT，可空)**；`publish_error_at`（可空） | R2、R7、R16；§6 P3 |
| `dest_policy_snapshots` | `generation` PK；`body` `[]byte`（策略、展开后的列表条目、豁免、分组模式、paused 的规范序列化）；`created_at` | 只保留当前已发布的这一份，以及正在发布的那一份 |
| `dest_agent_policy` | `agent_id` varchar(64) PK；`desired_sha256` varchar(64)；`minted_sha256` varchar(64)；**`minted_body` `[]byte`**（实际下发 Policy 的规范字节）；**`minted_kind` varchar(16)**（`desired`/`fallback`/`empty`/`paused`）；**`minted_generation` bigint**；**`minted_context` varchar(64)**（发布 generation、三个能力、core engine/version、listener 嗅探摘要的指纹，不含 roster）；**`minted_at`**（候选首次下发时刻，候选不变不重置）；`fallback_reason` varchar(32) NOT NULL DEFAULT ''（`''`/`rejected`/`sniffing`/`over_limit`）；`rejected_generation` bigint；**`fallback_exhausted` bool NOT NULL DEFAULT false**（回退候选无效或被拒，本上下文只下发无执行规则的版本）；**`over_limit` text（JSON `{kind,used,limit}`，无 DEFAULT，可空）**；`precheck_listeners` text（JSON，无 DEFAULT）；`applied_sha256` varchar(64)；**`applied_body` `[]byte`**（最近已确认可执行的 Policy，即 LKG）；**`applied_at`**；**`applied_rule_count` int**；**`applied_groups` text（JSON `[group_id]`）**；**`collect_effective` varchar(16) NOT NULL DEFAULT ''**（本次期望采集档位）；`reported_sha256` varchar(64)；`reported_state` varchar(16)；`reported_issue` varchar(64)；`reported_listeners` text（JSON）；`reported_at`；`updated_at` | 实际候选的字段与 config stream 的 mint 在同一事务提交，不能只存摘要再从最新定义猜 body。空 Policy 的 minted_body 存规范 null，摘要仍按 PolicyDigest 为 ''；empty/paused 不覆盖 LKG。字段只列级更新，PSP 重启后可恢复候选来源与回退终止状态 |
| `dest_hits` | 主键 `(hour_ms bigint, panel_id, user_id, source varchar(24), action varchar(8), dest varchar(253), port int)`；`count` bigint；`first_at`；`last_at`。索引 `(user_id, hour_ms)`、`(source, hour_ms)`、`(panel_id, hour_ms)` | `source` = `p<policyID>` 或 `g<groupID>`。白名单**试运行**行由节点折叠好再送来，入库为 `user_id = 0`、`port = 0`、`dest = 主域名`（R5、R15，§4.2）。**不存来源 IP**（F46）。MySQL utf8mb4 下主键约 1.1 KB，低于 3072 字节上限 |
| `dest_usage_hourly` | 主键 `(hour_ms, panel_id, user_id, site varchar(253))`；`count` bigint | B 档 |
| `dest_audit_batches` | 主键 `(agent_id, batch_id varchar(32))`；`kind varchar(16)`；`hour_ms bigint`；`received_at` | BatchID 去重，从首次接收保留 72 小时，晚到的重复不延长首次接收时间 |
| `dest_audit_loss_hourly` | 主键 `(observed_hour_ms bigint, panel_id, kind varchar(16), reason varchar(32))`；`rows` bigint；`events` bigint；`unmatched` bigint | 入库端按当前 UTC 小时记录丢行，接收的节点 Dropped/Unmatched 按批次小时记录事件计数；只存计数，不存用户、域名或原始行。用于 dropped_in_range 的持久来源，不能只用进程 Counter。kind 为 block/observe/trial/usage；试运行保留期同 trial，usage 同 usage，其余同 hits；面板删除时清理 |
| `dest_audit_ingest_budget` | 主键 `(agent_id, received_hour_ms bigint, kind varchar(16))`；`rows_reserved` bigint | 按 PSP 接收的 UTC 小时与种类预留入库行数，在首块事务内与 BatchID 去重原子执行；PSP 重启不重置配额；超过 72 小时的预算桶随批次清理 |
| `xui_panels` 加列 | `audit_collect varchar(16) NOT NULL DEFAULT 'hits'`；`audit_collect_revision bigint NOT NULL DEFAULT 1`（模型 xuiPanelRow，domain Panel.AuditCollect/AuditCollectRevision） | off/hits/hits_and_usage，只对 psp 生效；revision 只由专用档位写入口原子递增，普通整行 Save 不得擦除或回滚 |

**`audit_collect` 的写入路径**（F33）：
- xuiPanelRepo.Save 更新分支 Omit CreatedAt、AuditCollect、AuditCollectRevision；Create 经 NormalizeAuditCollect 规范为 hits，revision 初始化为 1。
- 唯一的写入口：UpdateNativeMetadata 扩成接收 auditCollect 指针，仍校验 kind=psp；实际档位改变才原子递增 revision，同值保存不递增。它与 P4 worker 共用采集闸门，由 app 接线；请求 DTO 不接受客户端设置 revision。
- `admin_servers.go` 的 PUT 照 `UpdateChannel` 的写法传指针。
- 三方言测试：面板已设为 `off`，再调用不带 AuditCollect 的 `Save`，存储值仍是 `off`。

### P2　列表服务（`internal/service/destlist`，新包）

1. **自定义**：管理员粘贴文本，逐行解析，`#` 开头是注释。接受的写法：
   - 纯域名 `example.com` → `domain:example.com`；带 `domain:`/`full:`/`keyword:`/`regexp:` 前缀的原样保留；`*.x` 与 `.x` → `domain:x`；
   - Clash 行：`DOMAIN-SUFFIX,x` / `DOMAIN,x` / `DOMAIN-KEYWORD,x` / `DOMAIN-REGEX,x` / `IP-CIDR,x` / `IP-CIDR6,x`（第三个字段是策略名，忽略；名称沿用 routing-rule-dsl §4.1）；
   - hosts 行 `0.0.0.0 x` / `127.0.0.1 x`：一行多个主机名逐个接受，`localhost` 一类忽略；
   - AdGuard `||x^`；带 `$` 修饰符的行忽略，并给出原因；
   - CIDR `1.2.3.0/24`：对齐到网络号（`1.2.3.4/24` 记为 `1.2.3.0/24`，并在报告里注明）；裸 IP 转为 /32 或 /128。

   **规范化**：小写；IDN 转 punycode；去尾点。规范化之后去重。
   **大小**：上限 4 MiB 或 50 000 条，超出返回 400 `dest_list_too_large`。
   **解析报告**：`{accepted, ignored, ignored_broad, rewritten, samples:[{line, text, reason}]}`，samples 最多 20 条。`accepted` 是最终规范化并去重的有效条目数；`ignored` 计被丢弃条目（含过宽与重复），`ignored_broad` 只计过宽条目且包含在 ignored 内；`rewritten` 计规范化改写，允许与 accepted 重叠；samples 的单条 text 最多 512 UTF-8 字节且不得截断字符。自定义的 line 是原文行号；分类的 line 是所选分类原始 rules 中的 1 基序号，属性筛选后仍保留原序号，界面标为「源条目」而非编辑器行号。
   保存成功时把报告与 entries、摘要及来源版本在同一事务提交到 `parse_report`；预览不写库。列表详情返回该报告，后台刷新后或 PSP 重启后仍可查看；同摘要只更新报告等元数据，不推进 generation。旧行无报告时返回 null，不伪造一次成功解析；下一次成功保存或刷新补齐。失败刷新保留上一次成功报告，另写 last_error；旧版本的报告与失败都遵守第 6 条提交条件。
2. **远程 URL**：
   - **只允许 `https://`**，否则 400 `dest_list_insecure_url`；
   - 用 `safehttp` 客户端，超时 60s，非 2xx 算错误；
   - 读取 `limit+1` 字节来判断，**超过 16 MiB 报错，不截断**（不要照抄 F37 的截断写法）；
   - 另支持 Clash rule-provider YAML（`payload:` 列表，`+.x` / `.x` 视为 `domain:x`）；
   - 解析后没有有效条目时返回 `dest_list_empty`，本次刷新失败并保留旧条目、摘要、last_fetched_at 与成功报告；不能用空文件、只有注释或全部无效的文件覆盖正在使用的列表。首次这样的输入保持未就绪，不能借已收到 2xx 标记就绪。
   - 计算 `content_sha256`，**只有摘要变化才更新 `entries` 并让 generation +1**（F13：否则每次刷新都会让全部 core 重启）。
3. **v2fly 分类**：
   - 下载 `https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat_plain.yml` 与 `.sha256sum`，**校验 sha256 之后**才解析（F47）；
   - 整份缓存到 `<DataDir>/destlists/dlc_plain.yml`，所有 geosite 列表共用一次下载；YAML 仍受 16 MiB 上限约束，校验文件读取上限 4 KiB。并发刷新合并一次下载；先校验并解析成功，再临时文件写入、同步并原子替换，失败不替换旧缓存或已成功列表。重启从成功缓存恢复；缓存不存在或损坏时返回 503，不创建空分类库。
   - 条目先剥掉全部属性后缀再下发。首个属性是 `:@attr`，后续是 `,@attr`，并兼容多个 `:@attr` 的旧输入。属性可以是否定形式 `!attr`（如 `:@!cn`，F49）；剥离和筛选都**按字面**处理属性名，`!cn` 与 `cn` 是两个不同的属性。
   - `geosite_attrs` 为空表示取全部条目，非空表示条目必须含所列的全部属性。可选的属性由 `GET /geosite/categories` 按分类给出（P7），界面只列这些值。
   - 依次做属性筛选、剥离属性并规范化、第 4 条过宽过滤、去重，计算 entry_count、regexp_count 和 content_sha256。分类目录中的 count 与 regexp_count 是无属性筛选时的有效数量，另返回 source_count 与 ignored_broad_count；属性组合的准确数量及报告以 preview 为准。缓存可以保留上游原始规则，任何下发内容都只能来自过滤结果。
4. **过宽条目**（判定适用于所有列表；过滤后的有效内容还须在策略引用与发布时防御校验）：
   - `regexp:` 对一组固定探针（8 个随机域名，写成常量）全部匹配；
   - `keyword:` 少于 4 个字符；
   - `domain:`/`full:` 本身就是公共后缀（`publicsuffix.PublicSuffix(x) == x`，如 `com`、`co.uk`）；
   - IPv4 前缀短于 /8，或 IPv6 前缀短于 /16。

   处理方式：
   - 自定义列表保存时，这些条目逐行列进报告的「忽略」部分；
   - **社区分类**：按 §1.3 逐条剔除，计入报告 ignored，并记录条目与原因；有剩余有效条目时刷新成功，清除旧 last_error。报告中的剔除提示不是刷新故障，不应让有效列表显示 failed。过滤结果为空则失败，`last_error = "dest_list_empty_after_filter"`，保留旧 entries、摘要、last_fetched_at 和成功报告；首次刷新遇到它保持未就绪；
   - **远程 URL**：出现过宽条目，本次预览或刷新算失败（`last_error = "broad_entry: <条目>"`），保留旧条目；空解析结果同样按第 2 条失败处理。
   - 策略或白名单引用了含过宽条目的列表 → 400 `dest_list_too_broad`。
5. **未就绪**：`last_fetched_at IS NULL`，或 `entry_count = 0 且 kind ≠ custom`。自定义列表为空时，被策略引用会被 API 拒绝（400 `dest_policy_no_match`）。
6. **刷新循环** `dest-list-refresh`：
   - 用 `safego.GoTracked` 启动；间隔读设置 `dest.list_refresh_hours`（每轮重读，改了即时生效）；单飞；
   - 失败时保留旧 `entries` 并写 `last_error`。刷新得到的条目超过 50 000 条（§4.1 的域名上限）也算失败，`last_error = "dest_list_too_large"`，保留旧条目：否则一次远程更新就会让所有引用它的策略在发布时被拦下（P3 第 1 条）；
   - 登记在 `docs/access-control.md` 与 `docs/ARCHITECTURE.md`（§13 文档交付），标为「间隔可热改」。PSP 的 `CLAUDE.md` 被 `.gitignore:80` 忽略，不随 PR 提交，由所有者本地自行同步，不作为交付物。
   - **刷新提交的版本条件**：拉取前记住列表 ID、编辑版本和来源参数（kind、URL 或分类/属性），网络下载与解析在 SQL 事务外执行。提交时进入 P1 的定义写事务，重新核对版本和来源；列表已删除或被编辑时丢弃这次旧结果，不重建列表、不覆盖新内容，下次按当前来源重试。成功内容和失败 `last_error` 都遵守该条件，不能让旧来源的迟到失败覆盖新来源的成功状态。只列级更新刷新拥有的字段，不整行 Save 回旧 name、owner_group_id 或 source_text。刷新成功或失败实际改变行时，同样按 P1 推进该列表的 updated_at（摘要不变仍不推进 generation），保证同毫秒的两个刷新结果也只能有一个提交；前端遇到版本冲突重新加载当前列表。
7. 管理端「立即刷新」与地理库更新同形：POST 触发、返回状态、前端轮询。
8. **完成判据**：
   - 解析器对每种格式都有表驱动测试；
   - 规范化测试：`Example.COM`、`example.com.`、`例子.cn` 各自得到唯一一个规范条目；
   - 超限报错、http 地址被拒、过宽条目（每类一例）的测试；分类剔除与报告、远程整份拒绝、过滤后为空保护及真实 release 回归按 §12 15b 验证；
   - 摘要不变时 generation 不变的测试；
   - 慢刷新期间修改来源、删除列表、两次刷新交错，以及旧失败迟到的测试；确认旧结果不提交，摘要变化与 generation 原子推进；
   - `safehttp` 拒绝回环地址的测试沿用现有的。

### P3　策略编译（`internal/service/destpolicy`，新包）

**接入点**：`nodesync.Options`（`nodesync.go:77`）加 `Policies DestPolicyCompiler`，可为 nil（nil = 功能关闭）。`Sync` 里分两步调用它：

- **第一步**，`ingestReport`（`nodesync.go:157`）之后、`buildConfig`（`:189`）之前：`ObserveStatus(ctx, agent, report.PolicyStatus, report.Capabilities)`。它处理 P4 第 1 条隔离校验过的 PolicyStatus：列级写 `reported_*`，推进第 7 条的 LKG 状态机，**只在值变化时写库**。
  **PolicyStatus 是控制输入，这一点与 Host 不同**：Host、Audit 在控制响应构建完成之后才入库（F29）；PolicyStatus 若也放到那里，「被拒 → 回退」就要晚一轮，而且第 7 条依赖的状态在本轮 Compile 时还没更新。
- **第二步**，`buildConfig` 之后：

```
Compile(ctx, panelID, agentID, report.Capabilities, snapshot, configBody) (CompiledPolicy, error)
```

CompiledPolicy 含 `Policy *protocol.DestinationPolicy` 与 `MintMetadata{kind,generation,context,desired_sha256,collect_effective}`。Policy 挂到 ConfigBody；**本方法不提前提交 minted_* 字段**，nodesync 通过新增窄 repo 方法 `MintConfigWithPolicyCandidate` 在与 config stream 相同的 SQL 事务里记录这些字段和规范 body。该方法复用 MintStream 的校验与 ETag 等值逻辑，即使 stream 无变化也必须检查候选来源；失败则两者都不提交。ObserveStatus 仍单独更新上报/回退状态。新增 repo、假实现与接线测试在 1c 一起完成。

**用本次上报的能力**，不用 agent 行上的旧值。engine 取 `configBody.Core.Engine`（空 = xray，与 Node resolveSelection 一致），因为预检针对即将编译的配置。

1. **发布快照**（R2、R16）：
   - **任何定义写入**（策略、列表内容摘要变化、豁免、分组模式与阶段、自带列表）都与 `generation += 1`、`last_write_at = now` 在**同一个事务**里完成；如果 `first_unpublished_at` 为空，同时写入它。
   - `ensurePublished(ctx)` 在每轮 `Compile` 开头运行（开销是一次单行读）。满足以下任一条件时发布：
     - `generation > published_generation` 且 `now - last_write_at ≥ dest.policy_apply_min_seconds`（尾随去抖）；
     - `now - first_unpublished_at ≥ 5 × dest.policy_apply_min_seconds`（连续修改不至于永远不发布）；
     - 收到「立即下发」（`POST /dest/publish`）；
     - `paused` 发生变化（R7，立即发布）。
   - **一致快照与原子发布**：在同一个一致读事务中**先**读 `generation = G`，**再**读全部定义并编出候选 body。PG/MySQL 显式指定 REPEATABLE READ，不依赖服务器默认；SQLite 同样使用读事务，单连接本身不能替代事务一致性。发布写事务原子插入 body 与 CAS 更新状态，条件同时包含 `published_generation = <旧值>`、`generation = G`；成功才清空对应未发布时间与错误。CAS 失败回滚候选，下一轮重读，不把旧 body 标成新 generation，也不清掉并发新写的去抖时间。
     一致读期间不做网络拉取或真实内核检查。发布失败写 `publish_error` 也必须同时比较读到的 generation 和 published_generation：旧候选的错误不能覆盖新定义或已经成功的发布；相同错误不反复刷新首次错误时间。发布状态与快照的读取必须一致；published_generation > 0 而对应 body 缺失或损坏时返回可观测的存储错误，不能伪装成「没有策略」并 mint 空配置。
     反过来「先读定义、后读 generation」的话，两次读之间提交的一次写入会让 generation 变成 G+1，而 body 里没有它；记成已发布 G+1 之后 `generation > published_generation` 不再成立，这次改动**永远不会下发**，直到有人再写一次定义。
   - **发布前校验**（R16）：用快照编译覆盖全部定义的检查策略，不展开真实 Subjects 与 Exempt；对需要 Subjects 的 CatchAll 放入一个规范的测试占位 SubjectKey，只用于调用 `ValidateDestinationPolicy`，绝不 mint 给节点。定义额度不计占位主体，真实主体数与最终字节数在每节点检查；不能把空 Subjects 的 CatchAll 直接送校验，否则白名单发布必然失败。规则数、域名、正则、IP 段在这里一次判断。
     - 超额或整份无效：**不推进** `published_generation`，保留上一份快照，各节点继续执行当前版本；写 `dest_policy_state.publish_error`（`{kind,used,limit}` 或 `{kind:"invalid",field}`）与 `publish_error_at`，由 `/dest/status` 返回（S1 结论条显示）。下一次发布成功时清空。
     - 保存策略、列表或分组模式的 API 在写入前就用同一个检查，返回 400 `dest_policy_over_limit`（带 `{kind,used,limit}`）。所以 `publish_error` 只会在几次各自合法的并发写入叠加时出现。
     - 远程或分类列表刷新超过 50 000 条判为刷新失败，保留旧条目（P2 第 6 条）。
   - 发布后删掉更早的快照。
   - **成员、分组、roster 的变化都不进快照**，所以天然立即生效：新成员加入白名单分组，与 roster 变化在同一轮生效，只重启一次。去抖只作用于管理员对定义的修改。
2. **输出**：`f(已发布快照, 本面板 roster, 本面板用户的 user→group 映射, 有效档位, 能力, paused)`。
   - 节点没有 `policy.destination.v1` → 返回 nil（F16）。
   - **paused 优先于一切**：paused 时，不论这台节点是否在回退，一律按「没有策略」输出：有效档位是 `hits_and_usage` 时返回 `{Collect: hits_and_usage, CollectRevision: 当前面板 revision}`，否则返回 nil；并且不改写 LKG（`applied_*`）。
   - 快照里没有启用的策略、也没有白名单分组 → 同上（保证 ETag 不变）。
   - 按 §3.2 排序，按 §4.1 的 ID 表分配 ID。
   - **user→group 映射**：`NativeDesiredSnapshot` 只有 `Nodes` 和 `Clients`（`ports/repos.go:680-683`），没有分组。从 `UserRepo` 新增的窄方法 `GroupIDsByIDs(ctx, ids []int64) (map[int64]int64, error)` 读，ids 取自 `snapshot.Clients`。为了不每轮都读：进程内维护一个 `membership_generation`，`user.Service` 在用户换组、新建、删除时 +1（setter 注入，nil-tolerant：nil 时每轮都读，只是慢）；缓存键用它，不每轮重算摘要。
   - **Subjects 与 Exempt 一律只保留本面板 roster 里有 client 的主体**（§3.2 第 2、4 条同样）：否则任何一个账号的豁免增删都会改变每个节点的 ConfigBody，全机队每个 core 各重启一次。过滤之后为空的规则整条不下发；Exempt 为空就不写。
   - `scope=groups` 的 `Subjects` = 这些分组里在本面板有 client 的用户的 `usr_{id}`；为空则不下发该规则。
   - 豁免 → `Exempt`（按上一条过滤）。到期的豁免由清理循环删除并让 generation +1（P5），所以正常同步时到期最迟在一个清理周期（1 小时）加最长发布等待（5 倍去抖窗口）和一个同步周期后失效；界面按这个口径说明（S5）。
   - 集合切片排序去重；Rules 保持 §3.2 的执行顺序。
3. **有效采集档位**（R4）：取面板 `audit_collect` 与本次能力中较低的一个，写进 `dest_agent_policy.collect_effective`（变化时才写）。
    Collect 非空时，将当前面板 audit_collect_revision 填入 Policy.CollectRevision；Collect 为空时填 0。revision 是面板采集设置，不从定义快照或旧 LKG 继承；恢复/回退和仅 B 档版本同样用当前 revision。
   - `hits` 需要 `audit.hits.v1`；
     - `hits_and_usage` 同时需要 `audit.hits.v1` 与 `audit.usage.v1`，缺 usage 降为 hits，缺 hits 则为 `""`；Observed/期望 engine 为 sing-box 时本次输出 `""`（能力表达二进制功能，不能替代 engine 的采集支持）；
   - `off` → `""`。

   测试矩阵：3 种档位 × 4 种能力组合。
4. **空规则**：
   - 编译时去掉规则中引用未就绪列表（P2 第 5 条）的那部分匹配。
   - **任何**匹配字段算下来全空的非 CatchAll 规则一律跳过，不报校验错：不只是引用未就绪列表的，也包括被改成空的自定义列表。一条空规则永远不能让整份 Policy 失效（§4.1）。
   - `/dest/status` 与策略行显示「列表未就绪」或「列表为空」。
   - 白名单试运行时同样跳过；执行阶段不会遇到，因为切换时有门槛（§9.4）。
5. **嗅探预检**（R3）：
   - **只在 engine == xray**、且策略里有 Domains 或 Protocols 规则时做：用 `destpolicy.SniffingInsufficient(listeners)` 检查本次 `ConfigBody.Listeners` 里每个 listener 的嗅探配置。判据与 Node N1 第 4 条相同，并通过 `conformance.SniffingVectors`。sing-box 只依赖 `route.rules` 里的 sniff，预检跳过（N1-S）。
   - 只要有一个不满足 → 不下发新策略，按第 7 条回退；`fallback_reason = sniffing`，`precheck_listeners` 写入这些 listener；写一条 PSP 侧 issue `destination_policy_sniffing_insufficient`。
   - 运营者改好入站之后，下一轮预检通过，自动恢复。
6. **每节点超额**（R16）：定义层面的超额已在发布时拦下（第 1 条），这里只剩本节点特有的一项：Subjects 与 Exempt 合计、以及含它们的整份序列化大小。
   - **计数口径**是「该面板按 TagFilter 匹配的分组的**全部**组员」，通过 `group.Service.TagMatchedMembers(ctx, panelID)` 读（只看 TagFilter，不看合格性；它在 group 包内部调用 `Matches`，不违反 §9.2 第 3 条的守卫）。**不能**用「在本面板有 client 的组员」：谁有 client 取决于合格性，合格性又取决于是否超额（§9.2），会形成「超额 → 不合格 → 删组员 → 回到额度内 → 合格 → 加回组员 → 再超额」的振荡，每振荡一次核心重启一次。
   - 超额 → `fallback_reason = over_limit`，`over_limit` 写 `{kind:"subjects"|"bytes",used,limit}`，写 issue `destination_policy_over_limit`（写法照 nodesync 里 `IssueReportMissingObject` 的用法，`nodesync.go:573`）。**绝不截断**：截断等于悄悄放行。
   - 每节点编译结果在 mint 前再调用一次 `ValidateDestinationPolicy`，失败同样回退（理论上不应发生，发生即是 bug，计 `invalid`）。
7. **回退到上一版已生效的策略（LKG）**：
   - **候选来源必须持久化**：mint 前记录实际 Policy 的 `minted_body`、`minted_sha256`、`minted_kind`、`minted_generation`、`minted_context`，与 stream mint 原子提交。desired 是当前已发布定义的正常候选；fallback 是实际修剪后的 LKG；empty 是无执行规则的回退终点；paused 是应急暂停。相同摘要在不同来源之间切换时也要更新来源与上下文，不能只用 ETag 相同就跳过状态写入。
   - **记录**：仅对 `State == applied && Digest == minted_sha256` 的当前候选推进。kind=desired/fallback 且有执行规则时，从 **minted_body** 写 `applied_sha256`/`applied_body` 和相应元数据；empty、paused、仅采集版本的成功不覆盖 LKG。过期或不匹配的状态只记 reported 字段，不改变回退决策。
   - **新版被拒**：当前 kind=desired 且匹配 minted 摘要的 rejected，设 `fallback_reason = rejected`、`rejected_generation = minted_generation`，写 issue `destination_policy_rejected`，下一轮编修剪后的 LKG。不能将稍后发布的 generation 当作本次被拒版本。
   - **回退候选本身被拒**：当前 kind=fallback 且被拒摘要等于 **minted_sha256**，无论它是否等于旧的 applied_sha256，都清空 LKG，置 `fallback_exhausted = true`，写 issue `destination_policy_lkg_rejected`。同一上下文内持续下发 empty，不再重建被拒的 fallback；重启 PSP、roster 变化都不解除这一锁定。empty 为 nil，或在 B 档仍有效时为 `{Collect:hits_and_usage, CollectRevision:当前面板 revision}`，均不含执行规则。这只解除「目的地策略」造成的部署失败，**不承诺无关的 listener/core 故障一定能部署**；那些故障仍走原有 issue 与 retryable 路径。
   - **回退期间 mint 修剪过的 LKG**；不添加新的目的地匹配规则，但不能将修剪一概称为「只放宽」：删除 allow 可能暴露后续 block，更新 Exempt 也可能超过主体或字节额度。具体规则为：
     - 删除 source 已删除或已停用的策略的规则；这些检查与豁免并集都读同一份已发布快照，不偷读去抖窗口内的未发布定义；
     - 删除当前**不处于**白名单模式的分组对应的 `g<gid>`、`g<gid>x1`、`g<gid>x2`。否则分组切回「不限」之后，组员会被重新加回这台节点（Eligible 退化为 Matches），旧 LKG 却仍对他们执行 deny-all；sniffing 回退可能持续数天；
     - Exempt 取**当前已发布快照**的有效豁免并按 roster 过滤；不保留已取消或已清理到期的旧豁免，也不与旧 LKG 无条件取并集。否则回退持续数天时，已到期豁免会永久留在节点，违反 R8；恢复原限制属于管理员明确的取消/到期行为；
     - 每条规则的 `Subjects` 只保留当前 roster 里的主体，过滤后为空的规则删除。

     当前有效采集档位与 revision 必须覆盖到回退候选，不能继续使用旧 LKG 的 Collect/CollectRevision。候选再次跑完整协议校验、当前 engine 的嗅探预检与字节/主体额度检查；若仍无效、嗅探不满足或超额，直接置 fallback_exhausted、转 empty，写同一 LKG issue，不把非法候选送给 Node。修剪后的版本成功后按「记录」成为新 LKG；没有 LKG 就转 empty。删除 allow 导致的行为变化在帮助里说明。
   - **paused 优先**：见第 2 条。
   - **何时重试新版**：
     - 在 `published_generation`、三个相关能力、core engine/version、listener 嗅探摘要中任一项变化，或管理员点「重新下发」时，清除 fallback_exhausted 并再试新版。这些字段组成 minted_context；#73 之后任务能力会逐次变化，所以不看整个能力集合。换核或修好入站不能仍被旧上下文永久锁住；
     - 成员变化**不**触发重试，避免每次 roster 变动都重走一遍「被拒 → 回退」。
     - 代价：回退期间新加入作用分组的成员在这台节点上不受该分组策略约束。白名单分组不受影响，因为回退中的节点对白名单分组是「不合格」（§9.2）；该限制写进 §10。
   - **何时清除回退**：只有 kind=desired 的匹配 applied 状态才能按 desired_sha256 清除 rejected，不能将 fallback 或 empty 生效当作新版恢复；嗅探预检通过时清除 sniffing；回到额度以内时清除 over_limit。每次清除都列级写库并触发 §9.2 的重同步。仍有 fallback_exhausted 而上下文未变时不清除；手动 retry 会原子清除相应锁定、失效缓存并触发重同步。
8. **缓存与空闲开销**（F52）：
   - Compile 的缓存键是 `(published_generation, roster ETag, membership_generation, 有效档位, audit_collect_revision, 三个相关能力是否存在, engine, core version, listener 嗅探摘要, paused, fallback_reason, fallback_exhausted, applied_sha256)`。mint 的来源/上下文仍单独核对，不能由命中缓存跳过持久化；回退终点与实际 LKG 变化都必须失效缓存。
   - nodesync 另外缓存**整份 ConfigBody 的规范字节和 ETag**，键是「Policy 为 nil 时 ConfigBody 的摘要」加上面的 Compile 键：不带策略的部分序列化很便宜，带 4 MiB 策略的那份每轮重新 `json.Marshal` 不便宜。
   - `MintStream` 的等值路径不再读 `desired_body`：Select 只取 `id`、`desired_version`、`desired_etag`，ETag 相同就视为相同。这意味着删去现有的整体 `bytes.Equal` 碰撞校验（`node_agent_repo.go:419-424`）；在注释里写明理由：SHA-256 碰撞在这里不构成现实风险，而这一处的代价是每 30 秒每节点整行读一次最多数 MiB 的 body。
9. **完成判据**（测试先写，`internal/service/destpolicy/compile_test.go`、`publish_test.go`、`fallback_test.go`）：
   - 无策略时 `ConfigBody` 的规范序列化与改动前逐字节相同；无能力的节点不带 Policy；
   - §3.2 顺序；ID 表（含 `x1/x2` 拆分）；分组展开；豁免与过期；Subjects 与 Exempt 按 roster 过滤：给一个在本面板没有 client 的账号加豁免，本面板 ConfigBody 不变；
   - 去抖：窗口内连续修改只发布一次；窗口内「改策略 + 新成员加入白名单分组」→ 新成员下一轮就受限；分组之间移动但 roster 不变 → 编译结果随之变化；PSP 重启后去抖仍然成立；
   - 最长等待（5×）；立即下发；paused（包括回退中的节点：paused 后同样不再执行，且 LKG 不被改写）；
   - **发布的读一致性**：三方言分别在读 generation 与 CAS 之间插入一次定义写入 → CAS 失败不发布旧候选，下一轮发布完整新版本；body 与指针只能一起提交；白名单 CatchAll 的定义检查不会因占位主体缺失而误拒；
   - **发布前校验**：导入超额的列表 → `published_generation` 不推进、`publish_error` 有值、各节点 ConfigBody 不变；修正之后下一轮发布并清空 `publish_error`；被引用的自定义列表改成空 → 该规则被跳过，整份照常发布；
   - 有效档位矩阵；未就绪列表；
   - 嗅探预检（通过 `SniffingVectors`；sing-box 跳过）；每节点 Subjects 超额回退，且不振荡（超额节点被判不合格、组员 client 被删之后，计数不变，回退保持）；
   - 被拒回退；LKG 为空得到 empty；**修剪后摘要与 applied 不同且再次被拒 → empty**；PSP 重启仍保持 exhausted，roster 变化不重试；generation/core/listener 变化或手动 retry 后重试；任务能力变化不重试；
   - 当前有效豁免更新使回退候选超额、LKG 嗅探不足均在 mint 前转 empty；取消/到期的旧豁免不再留在回退中；空/暂停生效不覆盖 LKG、不冒充新版恢复；minted_body 与 stream 原子写入失败时全部回滚；
   - **修剪**：分组关闭白名单后，回退节点上该组的 deny-all 消失；策略删除后，回退节点上它的规则消失；
   - 同一输入编译两次 ETag 相同；缓存命中时不查库、不重新序列化。

### P4　上报入库

1. **隔离解码与校验**（F30、F53）：handler 不再把整份报告直接解进带类型的 `NodeReport`，而是先解进一个影子结构：

   ```go
   type nodeReportWire struct {
       nodeprotocol.NodeReport
       // Same JSON names as the embedded fields: encoding/json resolves the
       // shallower field, so these two subtrees never reach the typed decode.
       Audit        json.RawMessage `json:"audit,omitempty"`
       PolicyStatus json.RawMessage `json:"policy_status,omitempty"`
   }
   ```

   这两棵子树再照 `sanitizeHost`（`handler/node_sync.go:157-187`，在 `:256` 调用）分别处理：先量原始大小（Audit ≤ `MaxAuditObservationBytes`），再单独 `Unmarshal` 进各自的类型并调用 `ValidateAuditObservation` / `ValidatePolicyStatus`，再检查能力绑定（缺 `audit.hits.v1` 丢 Audit，Kind=usage 且缺 audit.usage.v1 时丢整棵 Audit（包括仅计数批次），缺 `policy.destination.v1` 丢 PolicyStatus）。任何一步失败（包括类型错误，如 `port` 是 70000 或 count 是字符串）都只丢这棵子树并计数（P9），控制响应照常 200。
2. **剥离**：`Sync` 里 `audit := report.Audit; report.Audit = nil`，与 Host 在同一处剥离（F29）；缓存的 `cloneReport` 不含 Audit。PolicyStatus 不在这里入库，而是在 Compile 之前由 `ObserveStatus` 处理（P3 接入点的第一步）。
3. **四类入库队列**：Sync 持有每 agent 锁，SQLite 又是单写连接；一批最多 4 096 条 Hits 或 8 192 条 Usage，不能在 Sync 内同步入库。控制响应构建完成后，按 Kind 非阻塞投递到对应队列：

   | Kind | 每 agent 排队上限 | 全局排队上限 | 全局排队字节上限（按规范 JSON） | 每 agent 每接收小时预留行数上限 |
   |---|---:|---:|---:|---:|
   | block | 4 批 | 128 批 | 32 MiB | 20 000 |
   | observe | 1 批 | 32 批 | 8 MiB | 20 000 |
   | trial | 1 批 | 32 批 | 8 MiB | 20 000 |
   | usage | 1 批 | 64 批 | 16 MiB | 40 000 |

   - 行数、槽数与字节配额**不借用 block 的预留容量**；排队与处理中合计最多 256 批、64 MiB 规范 JSON。另实测解码后的堆占用并写进压测结果，不将 JSON 字节数当作真实堆大小。队列满只丢本类，计 `queue_full{kind}`。每类独立的容量不表示磁盘、CPU 或 DB 故障下零丢失。
   - **投递时去重**：键为 `(agent_id,BatchID)`。in-flight 集合保存全部排队和正在处理的批次，容量受以上队列约束，**不得被 LRU 淘汰**；完成后移入分类的 recent LRU（block 5 000、observe 1 000、trial 1 000、usage 3 000 个）。检查、预留槽位与记录 in-flight 在同一进程临界区完成，满队列不留下假的 in-flight。持久去重仍由首块事务兜底，LRU 只是减轻重复投递。
   - `safego.GoTracked` 启动一个 `dest-audit-ingest` worker，使用与 N6 相同的七槽调度，**每个 DB 分块事务之后**让出机会，不允许一份 8 192 行 usage 整批霸占写库循环。低优先级类有稳定机会，block 至少四个槽；空槽优先给 block。正在处理的批次各类最多一份，仍计入对应 agent 与全局槽/字节限制，预算也按类隔离。
   - Shutdown 先停止新投递，再随 bgWG 排空现有队列；强制终止或崩溃仍可丢掉全部内存积压，不能声称只有一个同步周期。明确记录未排空计数。
   - 同步应答**不等**入库：有效控制响应确认的是本轮携带批次的尽力投递，队列满、档位关闭、超额与 worker 失败都不阻断 roster/config；不新增遥测持久 ACK，也不返回 500 迫使控制面重试。控制本身的存储失败仍按现有错误处理。
4. **worker 的入库**：
   - **逐行映射**：subject → user_id（试运行行的空 subject → 0）；rule_id → `source`（只做语法映射，见 §4.1）；未知 subject 丢弃并计 `unknown_subject`；`Hour` 不在 [now−48h, now+1h] 内的丢弃并计 `out_of_range`。
   - **先在内存里按最终主键归并**：`count` 相加，first 取 min，last 取 max。映射之后同一批里会出现主键相同的行（`p12x1` 和 `p12x2` 都映射到 `p12`），而 PostgreSQL 的多行 `INSERT … ON CONFLICT DO UPDATE` 遇到同一语句内的重复键会直接报错（`ON CONFLICT DO UPDATE command cannot affect row a second time`），整批失败。
   - **去重行**：第一个事务先 `INSERT dest_audit_batches … ON CONFLICT DO NOTHING`，以 `RowsAffected == 0` 判重复；重复不再扣预算、写损失计数或累加行。该事务同时预留该类预算、记录收到的 Dropped/Unmatched 与写第一块数据；三方言都用无异常冲突处理，不能靠捕获 PG 主键错误。首次 received_at 决定 72h 清理时间。
   - **分块**：每个事务不超过 1 000 行，每条语句 200 行；`count` 累加，first/last 合并：PG 与 MySQL 用 `LEAST`/`GREATEST`，SQLite 用两参数的 `MIN(a,b)`/`MAX(a,b)`。照 `service/rollup/rollup.go:441` 的 `onConflictClause(dialect, …)` 写方言分支，三方言各有测试。
   - **失败**：某一块失败时，已提交块保留，其余不再写；首块提交过则去重行与整批预算预留保留，后续重发不补写未提交块，防止重计；首块回滚则本次未落任何数据。Warn 只带 agent_id、行数和错误，计 `ingest_error{kind}`。错误计数进入独立的有界计数缓冲，由 worker 合并写 loss 表，不在 Sync 中等 DB；缓冲最多 10 000 个面板/小时/种类/原因键，满或写失败计诊断指标并将查询标为不完整，不能让损失记录再阻塞控制面。
5. **试运行行**：节点已经折叠好（§4.2，R15），PSP **只校验、不折叠**。执行阶段（`action = block`）的 `g*` 命中照常带账号与完整主机名，因为它们是真正的拦截，管理员要能回答「某人为什么打不开某站」。
6. **入库预算与丢失口径**：按第 3 条表中四个独立预算执行。额度按**最终主键归并后的逻辑写入行数**计算，不是 Count 中的连接事件数，也不是数据库中首次出现的新键数。首块事务按原子条件更新 budget 桶，防止并发绕过；超出时按固定顺序保留额度内行，其余计 `over_budget{kind}`。部分块失败不退还已预留预算，PSP 重启不重置它；旧批次按已去重路径不再次消耗预算。
   - loss 表分别累加丢失行数、节点丢失事件数与无法解析事件数，计数批次也去重入库；队列满、档位关闭、worker 错误使用上述计数缓冲记录丢行。`dropped_in_range` 仅兼容表示 PSP 已观测的丢行估计，另返回 `losses:{rows,events,unmatched,scope:"panel",complete:false}`，**不得把行数与事件数相加**，不得把遥测描述成完整日志。
   - 损失没有账号与组明细，按节点/时间查询。带 user_id、source 或 group 筛选时只能显示相关面板的总体损失提示，不能标成该账号/分组的准确丢失量。进程崩溃、未送达的节点计数与已过期积压不可完整还原，所以 complete 不返回 true；对外文案为「相关节点有记录未统计；统计可能不完整」，有计数时分别注明行/事件单位。实际可用的历史值来自 loss 表，不能由进程 Counter 倒算。
7. **当前采集档位与 revision 闸门**：投递前读 PSP 当前面板档位/revision，worker 每个分块事务前再次检查。off 丢全部；hits 禁止 usage；hits_and_usage 按能力、engine 允许种类；**任何档位下旧 revision 都丢全部类别**，计 stale_collect_revision。不能用旧 minted_body 或节点上报的 Collect 作为当前许可。
   - 面板档位写入与该面板 worker 每块入库共用短临界区（入库闸门锁），设置写成功后失效缓存、移除对应排队数据。设置返回成功以后，旧 pending、重发或队列里的数据都不得在后续事务写入禁止的表。正在提交的事务若先获得闸门，可以在设置返回之前完成；不能在持有 agent 同步锁的情况下等待此闸门或后台入库。先关闭再开启不会复活已丢弃批次。
   - 档位修改不会立即删已入库历史，历史按 P5 保留期清理。测试通过屏障制造 off 与入库交错，并断言设置返回后的写入边界。

### P5　设置与保留

**`dest.*` 设置**，照 #262 的「领域自带端点」模式（F40），**不**照 #259 的系统设置页模式：

- UISettings 字段 + `settingDescriptors` 一行 + 「0 = 默认」 + domain 层 `settingOr` 夹取。
- `ports.AccessControlSettings`：字段与 json tag 与 UISettings 中所有 `dest_` 前缀字段一一对应。守卫测试 `TestAccessControlSettings_IsExactlyTheDestFields` 按前缀 `dest_` 取全集。
- `GET/PUT /api/admin/dest/settings`：语义照 `/risk-center/policy`。只提交改过的键，未送的键保留存储值；任何解码错误都整体返回 400；响应带服务端默认值与当前生效值。PUT 与系统设置 PUT 共用写锁。
- 系统设置的 PUT 保留这些键：照 `TestSettingsPut_PreservesEveryPolicyKey`（`admin_settings_policy_preserve_test.go:55`）加 `TestSettingsPut_PreservesEveryDestKey`，照 `TestSettingsPut_NeverReadsPolicyFieldsFromTheRequest`（`:101`）加 `TestSettingsPut_NeverReadsDestFieldsFromTheRequest`。系统设置页**不显示**它们。
- 保留期类的 `dest.*` 键在 `settings_kv_repo.go` 里**不使用** Load 的 key-presence 默认：0 表示默认值，永远不表示永久保留（照 `:513-516` 对 `connection_retention_days` 的注释）。

| key | 默认 | 范围 | 说明 |
|---|---|---|---|
| `dest.hit_retention_days` | 30 | 1–365 | A 档 |
| `dest.trial_retention_days` | 7 | 1–30，有效值不超过 `dest.hit_retention_days` | 白名单试运行的折叠行（R5） |
| `dest.usage_retention_days` | 7 | 1–30 | B 档，更敏感，上限更低 |
| `dest.list_refresh_hours` | 24 | 6–168 | 远程与分类列表刷新 |
| `dest.policy_apply_min_seconds` | 60 | 30–3600 | 尾随去抖窗口（P3 第 1 条） |

**风险设置**，进 `ports.RiskCenterPolicy`（F40；48 → 50）：

| key（json） | 默认 | 范围 | 说明 |
|---|---|---|---|
| `risk.dest_block_off`（`risk_dest_block_off`） | false | — | 进 `OverridableScopeKeys`（`ports/repos.go:1997`，与其他 risk.* 同组）、`domain.RiskPolicySettings` 加 `DestBlockOff` |
| `risk.dest_block_threshold`（`risk_dest_block_threshold`） | 20（0 = 默认） | 1–10000 | 同样可按分组覆盖，`RiskPolicySettings` 加 `DestBlockThreshold` |

两个键必须一起完成以下改动：
- `domain.RiskPolicySettings` 加 `DestBlockOff`、`DestBlockThreshold`；**评估器读的是净化后的 `domain.RiskPolicy`**（由 `RiskPolicyFromSettings` 与 `Bounded` 生成，`domain/riskpolicy.go:41-71`），所以 `RiskPolicy` 也要加这两个字段，并在 `RiskPolicyFromSettings` 里夹取：阈值 1..10000，0 取默认值 20。只加在 Settings 上，阈值永远读不到；
- `ports.RiskCenterPolicy` 加字段；`UISettings.RiskCenterPolicy()`（`risk_center_policy.go:88`）与 `SetRiskCenterPolicy`（`:149`）补手写赋值，`riskCenterPolicyKeys`（`RiskCenterPolicyKeys()`，`:219`）与 `RiskCenterPolicyDefaults()`（`:234`）补键和默认值；
- 守卫测试里的 48 改为 50（`risk_center_policy_test.go:71,172`，同时改错误消息）；
- 前端 `policyKeys.json`、`ALL_POLICY_KEYS`、`api/riskCenter.ts` 的 `RiskPolicyKey`；`policyLayout.test.ts` 的 48 → 50、六张卡 → 七张卡；
- `components/scope/scopeOverrides.ts:81` 的 `SCOPE_KEYS` 加两行（cat `risk`）；
- 两份语言包；`docs/connection-limits.md` §14.11 的卡片表与「48 个设置」字样同步改。

它们**不进** `RuntimeEffective`：那是 23 个运行时旋钮，这两个不属于（`ports/policy_settings.go:138`）。

**保留期清理**：在 `runAuditCleanupLoop`（`app.go:1311`）的 `a.pruneRiskReviews(ctx)`（`:1342`）之后、`a.pruneCertEvents(ctx)`（`:1343`）之前，依次加入：

1. `pruneDestHits`：按 `dest.hit_retention_days` 删除；精确 `g<gid>` 且 `action = observe` 的行按 `dest.trial_retention_days` 删除。相应 block/observe/trial 的 loss 行按各自保留期清理；截止时间取整到 UTC 整点。
2. `pruneDestUsage`：按 `dest.usage_retention_days` 删除用量与 usage 的 loss 行。
3. `pruneDestBatches`：按 §4.2 常量删除首次 received_at 已超过 72h 的批次与超过 72h 的 budget 桶；小时取整只能延后删除，不能提前于 72h。测试覆盖 24h、48h、72h 边界和未来 1h 的批次。
4. `pruneDestExemptions`：删除已过期的豁免；有删除时 generation +1。
5. `pruneDestOrphans`，删除以下几类孤儿：
   - `user_id > 0` 且用户已不存在的命中，以及用户已不存在的用量、豁免、同意记录；**合法 trial 行的 user_id=0 不参与用户孤儿清理**；
   - 匿名 trial 行按精确 `source=g<gid>` 对应的分组是否仍存在清理（分组切回 open 不提前删除试运行历史，删除分组才算孤儿）。不要把所有 user_id=0 都豁免：source/action/port 不符合 trial 形状的行清为非法孤儿；
   - `panel_id` 已不存在的命中和用量；
   - agent 已不存在的 `dest_agent_policy`、批次和预算行，面板已不存在的 loss 行。

行为照 `pruneConnectionHistory`（`app.go:1552`）：读不到设置时跳过按时间删除并打 Warn；孤儿照样清。测试照 `app/cleanup_test.go` 里 flag records 的四个写，并照 `TestBuildPrunesFlagRecords`（`:351`）加 `TestBuildPrunesDestRows`。

### P6　风险信号 `dest_block`（只观察，不驱动任何自动停用）

**状态与代码**（`AllRiskCodes[dest_block]` 必须与评估器的分支完全一致，`domain/risk.go:82`）。共**六态**：`GeoState` 有七个值，但 `dest_block` 不产生 `exempt`（信任不隐藏它），`destblock_test.go` 要断言评估器永远不返回 `exempt`：

| 状态（`GeoState`，`domain/geopolicy.go:132-147`） | code | 条件 |
|---|---|---|
| `disabled` | `signal_off` | 开关关闭（全局或分组覆盖） |
| `unknown` | `no_collector`（新） | 该用户有 client 的原生节点中，没有一台「在记录」。「在记录」由 PSP 推导：当前面板档位允许 Hits；ObservedCoreEngine=xray；有 audit.hits.v1；最近上报未超过三个同步周期；PolicyStatus=applied 且 Digest 与当前 minted_sha256 匹配；minted_body 的 Collect 非空且含命中采集的执行规则。不能只凭期望 collect_effective 或旧 applied 状态宣称已开始记录；sing-box 永远按只执行处理 |
| `idle` | `no_hits`（新） | 窗口内计数为 0 |
| `clean` | `within` | 0 < 计数 < ⌈阈值/2⌉ |
| `suspect` | `over_building` | ⌈阈值/2⌉ ≤ 计数 < 阈值 |
| `flagged` | `over` | 计数 ≥ 阈值 |

- **计数口径**：窗口固定 24 小时；只统计 `dest_hits` 中 `action = block`、且 `source` 对应的 `p*` 策略 `counts_as_risk = true` 的行。`g*`（白名单兜底）**永不计入**；已删除的策略不计。
- **完整性**：读取相关节点 block 类 loss 时，证据带 `coverage_complete:false` 与分开计量的损失摘要，不增加地址信息。计数仍是可观测下界，达到阈值可以 flagged；未达到阈值的 idle/clean/suspect 必须标明统计可能不完整，不呈现成已经证明安全，沿用 §7 的不完整证据样式。
- **证据**：形如 `{"v":1,"window_hours":24,"threshold":20,"total":37,"by_source":[{"source":"p12","count":30},{"source":"p15","count":7}],"nodes":2}`，最多 5 个 source。**不放目的地、端口、IP**：风险证据不得含地址（connection-limits.md §14.14），而且证据会被复制进 `flag_records`，保留期比命中更长。照 `service/risk/subspread_test.go:643` 的 `TestRefresh_SavedEvidenceHasNoInputAddress` 加一条测试：证据里任何字符串都不能解析为 IP 或网段。
- **复核与信任**：`dest_block` 不属于地点类来源（`LOCATION_SOURCES`，`api/riskCenter.ts:344`），所以「信任此账号」**不会**隐藏它；「忽略」照常可用。状态变化由 `riskSignals.Save` 自动写进标记记录。
- **进入待处理与铃铛**：`flagged` 会自动进入「待处理」队列（queue 遍历 `RiskKinds()`），并计入唯一的 `risk_queue` 铃铛（CountUrgent 只计 flagged 或被暂停的账号）。**不新增铃铛类型**。
- **后端改动**：
  - `domain.RiskKinds()` 末尾追加 `RiskKindDestBlock`；
  - 评估器通过 `risk.Deps` 的窄只读接口读 `dest_hits` 与 agent 档位，不破坏 `TestRiskServiceCannotWriteServiceState`（`service/risk/readonly_test.go:38`）；
  - 写代码完备性测试，每个分支一例。
- **前端改动**：
  - `api/riskSignals.ts:12,21` 的 `RISK_KINDS` 与 `RISK_CODES`（`QUEUE_SOURCE_FILTERS` 由它展开，`api/riskCenter.ts:341`）；
  - `utils/riskSignals.ts` 的文案；`evidence/RiskEvidence.tsx` 加 `case 'dest_block'`；`queue/queueText.ts` 的原因句；`recordValues.ts`；
  - 风控中心策略页的第七张卡（§7 S19）；
  - 两份语言包的 `risk_signals.code.dest_block.*`。
- **文档**：`docs/connection-limits.md` 新增 §13.x「访问拦截」；§14.3 的来源列表、§14.4 的「五个检测器」同步改。

### P7　管理 API

通用约定：
- 全部挂在 `adminGroup` 下。写操作由审计中间件自动记录。
- **把 `"/api/admin/dest/"` 加进 `adminOnlyAuditTargets`**（`handler/admin_audit.go:35`），并照 `TestRiskCenterAuditRowsAreAdminOnly` 加 `TestDestAuditRowsAreAdminOnly`（F42）。`/api/admin/legal/` 的内容本来就是公开的，不加。
- **两个预览接口不写审计**：`POST /dest/lists/preview` 与 `POST /dest/policies/preview` 是只读计算，S3、S7 在输入停顿后就会调用；按现在的 `shouldAuditPath`（F55），每次停顿都会写一条审计行，自定义列表的预览还会带上最多 8 KiB 的列表原文。在 `middleware/audit.go` 的 `shouldAuditPath` 里按**精确路径**豁免这两条（不按前缀），加测试 `TestDestPreviewEndpointsAreNotAudited`，并在同一个测试里断言 `/dest/test` 仍然写审计。
- **访问日志不写 `/dest/` 的查询串**：PSP 自己的访问日志把 gin 的 `params.Path`（含查询串）写进 stdout（F55），`/dest/hits?q=`、`/dest/usage?user_id=` 会因此进 journald，不受 `dest.hit_retention_days` 约束。在 `access_log.go` 的 formatter 里，对 `/api/admin/dest/` 前缀只写路径、去掉查询串；`access_log_test.go` 加一例。反向代理自己的访问日志 PSP 管不到，写进 `docs/access-control.md` 的「存储与隐私」。
- 每组路由都有一条 admin-only 路由测试，照 `risk_center_route_test.go` 写。
- 400 响应一律是 `{error, field?, bad?:[…]}`。
- **额度的形状**（`/lists`、`/policies`、`/policies/preview` 三处相同）：`budget: {rules, domains, regexps, cidrs, subjects, bytes}`，每项都是 `{used, limit}`。前四项与 `bytes` 按 P3 第 1 条的上界计算（与面板无关）；`subjects` 取各面板按 TagFilter 计数（P3 第 6 条）的最大值。
- **策略的写法只有一种**（R18）：`POST` 与 `PUT` 都带完整的 Policy，`PUT` 另带 `updated_at` 作为前置条件。**priority 由服务端分配**：`POST`，以及改变了 `action` 的 `PUT`，把 priority 设为该动作现有最大值 + 1（排在本段末尾）；同一动作内的 `PUT` 不改 priority；调整顺序只能用 `PUT /policies/order`。
- 时间字段一律 UTC 毫秒；用于编辑 CAS 的 `updated_at` 按 P1 严格单调，并直接回传成功写入后的新值。`apply_eta_ms` 一类的「还要多久」由服务端算好，前端不自己推导（前端拿不到同步周期）。

| 方法 | 路径 | 请求 / 响应 |
|---|---|---|
| GET | `/api/admin/dest/lists` | `{items:[{id,name,kind,entry_count,regexp_count,state:"ready"\|"refreshing"\|"failed"\|"pending",last_fetched_at,last_error,parse_report_summary,owner_group_id,updated_at,used_by:[{kind:"policy"\|"group",id,name}]}], refresh_hours, budget}`。`parse_report_summary` 只含 accepted、ignored、ignored_broad、rewritten，完整 samples 从详情读取；无成功报告为 null。`used_by` 与 409 `dest_list_in_use` 同形；`refresh_hours` 是全局 `dest.list_refresh_hours` 的生效值 |
| POST | `/api/admin/dest/lists/preview` | 请求体同新建，**不写库、不写审计**；返回解析报告与前 50 条规范化后的条目。远程类型会实际拉取一次（同样走 safehttp、同样的上限），另返回 `http_status` 与 `bytes` |
| POST / PUT / DELETE | `/api/admin/dest/lists[/:id]` | 请求 `{name,kind,source_url?,geosite_category?,geosite_attrs?,text?}`；`PUT` 另带 `updated_at`，冲突返回 409 `dest_list_stale`。自定义列表把 `text` 原样存进 `source_text`。响应含解析报告。删除被引用的列表 → 409 `dest_list_in_use`，带 `used_by` |
| GET | `/api/admin/dest/lists/:id` | 统计 + parse_report + 前 200 条规范化样本，不返回全量；`?text=1` 时（只对自定义列表）另返回 `source_text`，供 S7 编辑框回填 |
| POST | `/api/admin/dest/lists/:id/entries` | `{add:[…], remove?:[…]}`，只对自定义列表；服务端在一个事务里读出原文、追加或删除行、重新解析并保存，generation 只 +1 一次；返回解析报告。列表被放行类引用时出现过宽条目 → 400 `dest_list_too_broad`。S10「加入白名单」、S12「加入放行例外」一律调用它，**前端不重建全文** |
| POST | `/api/admin/dest/lists/:id/refresh` | 202；状态随 GET 返回 |
| GET | `/api/admin/dest/geosite/categories` | `{categories:[{name,count,regexp_count,source_count,ignored_broad_count,attrs:[…]}], updated_at}`；`count`/`regexp_count` 是过滤去重后的有效数量，`source_count` 是上游原始规则数，`ignored_broad_count` 是过宽条目数；这些分类总数不代表任意属性组合，组合计数以 preview 为准。`attrs` 是该分类里出现过的属性名（字面量，含 `!cn` 这种，F49）；从未下载过 → 503 `dest_geosite_unavailable`；`POST …/geosite/refresh` 触发下载 |
| GET | `/api/admin/dest/policies` | `{allow:[Policy],block:[Policy],observe:[Policy], exemptions:{count}, allowlist_groups:[{group_id,name,stage,stage_days}], hit_window_days, budget}`；Policy 带 `hits_recent`（窗口 `hit_window_days = min(7, dest.hit_retention_days)` 内的次数；2c 之前或没有节点在记录时是 null）、`last_hit_at`、`list_states`（含 `empty`）、`scope_missing:bool`、`template_key`、`updated_at`；按 `(source, day)` 聚合并做 60s 进程内缓存 |
| POST | `/api/admin/dest/policies/preview` | **不写库、不写审计**；返回「假设保存后」的 `budget` |
| POST / PUT / DELETE | `/api/admin/dest/policies[/:id]` | Policy 全字段（含 `template_key`）；`PUT` 带 `updated_at`，冲突 409 `dest_policy_stale`；超额 400 `dest_policy_over_limit`，带 `{kind,used,limit}`。priority 规则见上 |
| PUT | `/api/admin/dest/policies/order` | `{action, ids:[int64]}`；`ids` 必须恰好等于该动作当前的全部策略，否则 409 `dest_policy_order_stale` |
| POST | `/api/admin/dest/exceptions` | `{target, match:"site"\|"host", scope:"group"\|"global", group_id?}`。`group`（阶段 5 起）：写进该组的「补充」列表，等同 `lists/:id/entries`。`global`：第一次使用时，服务端在**同一个事务**里建一个「放行例外」自定义列表，以及一条排在「放行」第 1 位的 allow 策略，响应带 `created:{list_id, policy_id}`；之后只往这个列表追加。「只豁免某账号」走豁免接口，不走这里 |
| GET / POST / PUT / DELETE | `/api/admin/dest/exemptions[/:user_id]` | `{user_id,reason,expires_at?}`；响应带 `created_by_upn` 与 `expired:bool`（已到期、等待清理的行照样返回）；同一账号重复添加 → 409 `dest_exemption_exists` |
| GET | `/api/admin/dest/groups` | **所有分组**（含 `mode = open`）：`{items:[{group_id,group_name,members,mode,stage,stage_changed_at,list_ids,base_list_id,extra_list_id,eligible_nodes,total_nodes,ineligible:[{panel_id,name,reason:"xui"\|"sui"\|"capability"\|"over_limit"\|"rejected"\|"sniffing"}],applied_nodes,would_deny_recent:{days,sites,count},denied_24h}]}`。S9 第 1 步用它列出「不限」的分组与人数 |
| GET / PUT | `/api/admin/dest/groups/:group_id` | 单个分组同上，另带 `readiness:{lists_ready, not_ready_lists:[…], unapplied_nodes:[…], base_nonempty, remaining_sites_24h, top_sites:[…]}`。`PUT`：<br>• open→trial：`{mode:"allowlist", stage:"trial", list_ids, base_hosts:[…]}`，服务端在**同一个事务**里建好两个自带列表（`owner_group_id = gid`；基础放行写入 `base_hosts`，补充为空），再写 `dest_group_modes`。S9 只发这一次请求，不会留下孤儿列表；<br>• 其他转换：`{mode, stage, list_ids}`。<br>非法的状态转换 → 409 `dest_mode_invalid_transition`（只允许 open→trial、trial→enforce、enforce→trial、任意→open）；切到执行而有列表未就绪 → 409 `dest_list_not_ready` |
| GET | `/api/admin/dest/groups/:group_id/preview?list_ids=` | 启用白名单前的影响预演：`{members, eligible_nodes, ineligible:[…], base_hosts:[…]}` |
| GET | `/api/admin/dest/groups/:group_id/trial-report?days=` | `{effective_days, items:[{site,count,last_at,added:bool}], dropped_in_range, losses}`；`days` 夹到 `min(请求值, dest.trial_retention_days)`。**没有示例主机名**：试运行只存主域名（R5、R15） |
| POST | `/api/admin/dest/test` | 请求 `{target, port?, network?, user_id?, panel_id?}`；响应 `{verdict, terminating_step, steps:[{step,policy_id?,group_id?,name?,result:"miss"\|"n/a"\|"hit"\|"shadowed"\|"skipped"\|"untestable",list_id?,entry?}], notes:[…], unpublished:bool, nodes:[{panel_id,name,state}]}`。`skipped` = 终止步之后、没有命中的步骤；`unpublished` = `generation > published_generation`。实现调用 `protocol.MatchDestination`，用已发布快照加当前 roster 编出策略：带 `panel_id` 就用该面板；只带 `user_id` 就用该用户有 client 的第一个原生面板；都不带就按「不属于任何分组、未被豁免」在一份不含 Subjects 限制的虚拟策略上评估。**绝不做 DNS 解析** |
| GET | `/api/admin/dest/hits` | 分页；筛选 `user_id`、`panel_id`、`source`、`source_kind=policy\|group`、`action`、`since`、`until`（最长 31 天，超出 400）、`include_trial`、`q`（dest 关键词，用 `likeCols` 的 `LOWER(col) LIKE ? ESCAPE '!'`，`sqlstore/pagination.go`）；`group_by` = `none`\|`site`\|`user`\|`policy`。响应另带：`dropped_in_range` 与 P4 的 losses（分别计行/事件，scope=panel）；`summary:{block, deny, observe, users}`（`deny` = block 且 `g*`；**不受** `action`、`source_kind` 筛选影响，受时间、账号、节点筛选影响，照风控中心「指标卡数整个队列」的规矩）；`sources:[{source, name\|null}]`（时间范围内出现过的来源，`name` 为 null 表示已删除，供 S12 规则下拉的「已删除」组） |
| GET | `/api/admin/dest/users/:id` | `{group:{id,name,mode,stage}, exemption, hits_available, recent_hits:{days, items:[{source,source_name,action,count,top_dests:[≤3],panels:[…],last_at}]}, usage_available, usage_nodes:[…]}`。`hits_available` = 该用户有 client 的节点中至少一台「在记录」（P6 的推导）。1c 只返回 `group` 与 `exemption`，其余字段为 null。加 `?usage=24h\|<N>d`（N ≤ `dest.usage_retention_days`）时额外返回 `usage_top`（Top 20），**并写审计行**（见下） |
| GET | `/api/admin/dest/usage` | **必须带 `user_id`**，否则 400 `dest_usage_user_required`；可选 `panel_id` 和时间范围；返回 usage 及 P4 的 losses；**写审计行** |
| GET | `/api/admin/dest/status` | 全局 `{generation, published_generation, last_write_at, next_publish_at, apply_eta_ms, paused, publish_error, totals}`。`apply_eta_ms` = 距 `next_publish_at` 的剩余时间（没有待发布的改动时为 0）加一个同步周期（`node_poll_seconds` 的生效值）。<br>每节点 `{panel_id, agent_id, panel_name, kind, engine, agent_version, supports:{policy,hits,usage}, collect, collect_effective, collecting:bool, state, fallback_reason, fallback_exhausted:bool, minted_kind, losses, over_limit:{kind,used,limit}\|null, sniffing_insufficient:[{listener,label,node_id}], minted_at, pending_since, applied_at, applied_rules, allowlist_groups:[{id,name}], last_report_at, hits_24h}`。`collecting` 是 P6 推导出的「在记录」；`pending_since` = minted ≠ reported 开始的时刻（即 `minted_at`），其余时候为 null。<br>`state` 取值：`none`（期望为 nil）、`paused`、`unsupported_kind`、`unsupported_version`、`pending`（minted ≠ reported）、`applied`、`rejected`、`over_limit`、`sniffing`、`offline`（agent 超过 3 个同步周期未上报） |
| POST | `/api/admin/dest/publish` | 立即发布（P3 第 1 条）；返回新的 `published_generation`；发布前校验失败时返回 409 `dest_policy_over_limit`，带 `publish_error` |
| POST | `/api/admin/dest/agents/:agent_id/retry` | 原子清除 rejected/exhausted 锁定、失效编译与合格性缓存、触发重同步，下一轮重试新版 |
| PUT | `/api/admin/dest/pause` | `{paused:bool}`（R7） |
| GET / PUT | `/api/admin/dest/settings` | P5 |
| GET | `/api/admin/servers`（已有） | psp 行的 DTO 加只读的 `audit_collect`（S14、S15 的记录方式用） |
| PUT | `/api/admin/servers/:id`（已有） | 加 `audit_collect`；DTO 用 `*string`，省略时保留原值（同 `UpdateChannel` 的「省略不擦除」规矩），只对 psp 生效 |
| GET | `/api/admin/groups`（已有，staffGroup） | 列表 DTO 加只读的 `dest_mode: "open"\|"allowlist_trial"\|"allowlist_enforce"`（R10，S17）。handler 通过窄接口读 `dest_group_modes`；加一条路由测试断言**运维员**能读到这个字段、读不到名单内容 |

**读取用量要写审计**（R6）：
- `GET /dest/usage`，以及带 `usage` 参数的 `GET /dest/users/:id`，在 handler 里显式写一条审计行：action `dest.usage.read`，target 是路由模板，参数只有 user_id 和时间范围。用的是审计中间件写行时调的同一个 repo 方法。
- 同一管理员对同一用户，10 分钟内只记一次。
- 加一条路由测试断言这一行存在，并且运维员读审计日志时看不到它（它在 `/api/admin/dest/` 前缀下）。

**错误码汇总**：
- 列表：`dest_list_parse_failed`、`dest_list_in_use`、`dest_list_not_ready`、`dest_list_insecure_url`、`dest_list_too_broad`、`dest_list_too_large`、`dest_list_empty`、`dest_list_empty_after_filter`、`dest_list_stale`
- 策略：`dest_policy_invalid`（带 field）、`dest_policy_no_match`、`dest_policy_over_limit`（带 `{kind,used,limit}`）、`dest_policy_stale`、`dest_policy_order_stale`、`dest_name_taken`
- 分组与豁免：`dest_group_not_found`、`dest_mode_invalid_transition`、`dest_exemption_exists`
- 其他：`dest_usage_user_required`、`dest_geosite_unavailable`

**B 档接口只能按人查，是刻意的**：它回答「这个被投诉的账号在干什么」，不提供「全站都在访问什么」的浏览面。

**`POST /dest/test` 也会留审计行**：它是 POST，请求体（目的地和 user_id）会进审计行。这是有意的：谁替谁测试过什么目的地，属于管理员行为记录；而这一行对运维员不可见。

### P8　删除时的清理

- 删除分组：删掉它的 `dest_group_modes` 行（照 `group.Delete` 删 scope settings 的写法，`service/group/group.go:75-92`）；同时删掉它的两个自带列表（`owner_group_id = 该组`）；generation +1。
- 分组切回「不限」：保留两个自带列表，界面显示「来自分组 X（已关闭白名单）」，允许删除。它们不会被别处引用：界面和 API 都不允许其他策略引用带 `owner_group_id` 的列表。
- 删除用户：删掉 `dest_exemptions` 行；`dest_hits`、`dest_usage_hourly`、`legal_consents` 交给 P5 的孤儿清除。
- 删除面板：在删除的事务之后，同步删除对应 agent 的 `dest_agent_policy` 行。
- 删除策略：历史命中保留，界面把找不到的 `source` 显示为「已删除的策略」。

### P9　可观测性

`internal/pkg/metrics/psp.go` 新增以下 family。**每个 family 都要在同一个 PR 里**登记进 `web-react/src/utils/diagnosticsCatalog.ts` 的 `FAMILY_CATALOG`（卡片 `'node'`；不新增卡片，以免改动 `CARD_ORDER` 与 AreaCards），并在两份语言包里补上 `admin:diagnostics.metric.<f>.label/.desc`；带标签的还要在 `FAMILY_LABEL_GROUP` 指定标签组，并补 `admin:diagnostics.labels.<group>.<value>`（F41）。

| family | 类型 | 标签（标签组） | 阶段 |
|---|---|---|---|
| `psp_dest_policy_compile_total` | CounterVec | `result` = `cache_hit`\|`compiled`\|`fallback_rejected`\|`fallback_sniffing`\|`fallback_over_limit`\|`fallback_nil`\|`invalid`（`dest_compile`） | 1c |
| `psp_dest_policy_compile_ms` | Histogram | — | 1c |
| `psp_dest_policy_publish_total` | CounterVec | `trigger` = `debounced`\|`max_wait`\|`manual`\|`pause`（`dest_publish`） | 1c |
| `psp_dest_policy_publish_rejected_total` | Counter | —（发布前校验失败，P3 第 1 条） | 1c |
| `psp_node_policy_status_dropped_total` | Counter | —（PolicyStatus 子树解码、校验或能力绑定失败，P4 第 1 条） | 1c |
| `psp_dest_list_refresh_total` | CounterVec | `result` = `unchanged`\|`updated`\|`failed`\|`broad`（`dest_list_refresh`） | 1c |
| `psp_dest_pruned_rows_total` | CounterVec | `table` = `hits`\|`trial`\|`usage`\|`batches`\|`loss`\|`budget`\|`exemptions`\|`orphans`（`dest_table`） | 1c（随表陆续出现） |
| `psp_node_audit_report_total` | CounterVec | `kind` = `block`\|`observe`\|`trial`\|`usage`\|`unknown`（非法或超大子树无法安全判定 Kind 时用 unknown）；`outcome` = `accepted`\|`oversized`\|`invalid`\|`no_capability`\|`duplicate_batch`\|`queue_full`\|`ingest_error`（`node_audit_report`） | 2c |
| `psp_dest_audit_rows_total` | CounterVec | `kind` = `block`\|`observe`\|`trial`\|`usage`；`outcome` = `stored`\|`unknown_subject`\|`out_of_range`\|`over_budget`\|`collect_off`\|`stale_collect_revision`（`dest_rows`） | 2c |
| `psp_dest_audit_node_dropped_total`、`psp_dest_audit_node_unmatched_total` | Counter | — | 2c |
| `psp_dest_audit_loss_buffer_dropped_total` | Counter | 损失计数缓冲满或强制终止时未保存的键数；只用于诊断，不能倒算历史 losses | 2c |

Node 同步记录 `audit_expired_dropped`、`audit_collect_off_dropped`、聚合溢出与过滤器故障的本地诊断计数，分别注明行数/事件数；这些未必能到达 PSP，不把 PSP 的 dropped 值当作完整丢失数。PSP 计数缓冲写失败保留待重试的增量；重启丢失、TTL 过期、无法确认的统计按 R20 披露。

**日志规矩**（两个仓库都写进代码注释和文档）：Info 及以上只记计数和 agent_id；**Debug 也不记 email、目的地、来源 IP**。Node 的 webhook 接收器和日志过滤器出错时也只记计数。
**运行诊断页不新增 finding**：节点层面的问题由访问控制页自己的结论条呈现（§7 S1），避免出现两个真相。

---

## 7. 界面设计（`web-react`）

本章取代第二版的 §7、§9.6 和 §8 的界面部分。它沿用风控中心（#262，`docs/connection-limits.md` §14.2–§14.4）和运行诊断（#267）已经建立的版式、组件和配色，不另起一套。

### 7.0 实现者不得即兴的事项

下面每一条都来自仓库里真实出现过的「做丑了」或「做错了」。觉得哪条不对，**先在 PR 里向所有者提出、改本文，再改代码**；不要先做出来再解释。

1. **结构**：页面、tab、抽屉、对话框的数量、顺序和名字以 §7.1 为准。不新增 tab；未上线阶段的 tab 不渲染，**不放占位**。
2. **状态的画法**：一律用 `ToneBadge`（§7.2）。**禁止 `<Chip color=…>` 表示状态**（F44）。整页结论用 `StatusLine`，不用 `<Alert severity>`。Alert 只用于三种场合：读取失败、503 未接线、前提缺失。
3. **颜色**：只用 §7.2 的封闭色表与 `theme.palette.md.*`。不写 hex，不写 `rgba(`；不新增颜色。
4. **文案**：
   - 以本章为准，zh-CN 与 en-US 同时改；
   - §7.2 术语表左栏的内部词不得出现在界面上；
   - 不显示原始代码（`p12`、issue code、`hits_and_usage`），一律解析成名称，解析不了时写「已删除的策略」；
   - **不把可调数值写死**（R14）。
5. **确认框**：只有 §7.4 S20 清单里的动作才弹确认框。不增，不减。
6. **排序**：不做拖拽（R11）。
7. **URL**：不把目的地文本、搜索词放进页面 URL（R12）。
8. **说明文字**：tab 顶部不放说明段落，长说明一律收进 `HelpTip`。
9. **空值**：「没有数据」不显示成 0。没有采集节点时，命中数一列整列隐藏，或写「—」并附原因。
10. **共用件**：节点状态只有一个组件 `components/NodePolicyStatusRow.tsx`，状态用词只有一张表 `utils/accessControl.ts`。不许在别处另写一份。
11. **尺寸与表面**：对话框宽度、抽屉宽度、圆角、表面层级以 §7.2 为准。

### 7.1 信息架构

**导航与门禁**（三道门都要点名，并各有测试）：

- 导航项 `{ to: '/admin/access-control', labelKey: 'nav:admin.access_control', Icon: PolicyOutlinedIcon, adminOnly: true }`，放进风控中心所在的独立分组，排在 `/admin/risk` 之后（`layouts/AdminLayout.tsx:127-129`）。**同时改写那段注释**（`:121-126`）：从「看人，不看管道」改成「看人，以及他们能去哪」，说明访问控制为什么放在这里。
- `ADMIN_ONLY_ROUTES` 加 `'/admin/access-control'`（`router/home.ts:25-30`）；`utils/permissions.ts:37-40` 新增能力 `access.view`，只给 admin。
- `router/viewModules.ts` 加 loader 键 `'/admin/access-control'`（`:20` 那组）；`router/index.tsx` 的 `/admin` 子路由加 `{ path: 'access-control', element: <AccessControlView /> }`（`:104` 附近）。
- 页面在 hooks 之后再判断一次 `useCan('access.view')`，没有权限就 `<Navigate to="/admin/dashboard" replace />`（照 `RiskCenterView.tsx:70`）。
- 测试：`router/home.test.ts`、`router/prefetch.test.ts`、`utils/permissions.test.ts`。

**页面**：`/admin/access-control`（`views/admin/accessControl/AccessControlView.tsx`），结构自上而下：

```
PageHeader（标题 + 副标题 + [测试目的地] [查看账号…] [⋯]）
整页结论条 StatusLine（S1）
tab 条：策略 policies | 列表 lists | 白名单分组 allowlist | 记录 records        (?)
当前 tab（只挂载这一个）
```

**URL 归属**：

| 参数 | 含义 | 写法 |
|---|---|---|
| `tab` | 当前 tab，默认 `policies`；非法值回退到默认，不改写 URL | replace |
| `lst_state`、`lst_kind` | 列表 tab 的筛选 | replace，页码重置为 1 |
| `al_group`、`al_view=trial`、`al_days`、`al_enable` | 白名单分组子视图、试运行报告时间范围、启用对话框预选的分组 | 进子视图用 push，切范围用 replace |
| `rec_user`、`rec_source`、`rec_panel`、`rec_action`、`rec_since`、`rec_until`、`rec_group_by`、`rec_trial`、`rec_page`、`rec_size` | 记录 tab 的筛选（**没有 `rec_q`**） | replace，页码重置为 1 |
| `user` | 账号抽屉（RiskUserDrawer，host=`access`） | push |
| `sheet` = `test`\|`nodes`\|`exemptions`\|`list`，`list=<id>`，`node_state` | 页面级抽屉 | push |

- **抽屉互斥**：同一时刻只开一个右侧抽屉。
  - 从页面打开任何抽屉：push，history state 标记为 `{drawer: '<param>'}`。
  - 从页面级抽屉（`sheet=…`）里「打开账号」：用 **replace**，同一次 replace 删掉 `sheet`、写入 `user`，state 标记为 `{drawer: 'user'}`。历史里于是只剩「页面」与「账号抽屉」两条：关闭账号抽屉（✕ 或 Back）回到页面，再按一次 Back 离开页面。
    不能用 push：push 会把带 `sheet=…`（以及它的预填 state）的那一条留在历史里，Back 会把页面级抽屉重新打开，离开页面要按三次。
  - 关闭时，标记是本参数的就 `navigate(-1)`；不是（深链、冷启动）就 replace 删掉参数、留在页面上。这是 `drawerParam.ts` 现有的语义（`:27-55`），只是把标记从 `{riskDrawer}` 改名为 `{drawer}`。
- **history state 的形状**：`{ drawer?: string; prefill?: { target?: string; port?: number; network?: string; userId?: number; groupId?: number } }`。`useDrawerParam` 的 `open` **合并**已有 state（不覆盖 `prefill`），`close` 不读 `prefill`。
- **实现**：把 `views/admin/risk/drawerParam.ts` 移到 `src/hooks/useDrawerParam.ts`，泛化为 `useDrawerParam(param, { parse, exclusive })`，支持字符串值、互斥参数表和上面的 replace 打开方式；`views/admin/risk/RiskCenterView.tsx` 与 `UsersView.tsx` 改为从新位置导入（UI-0）。`useDrawerParam.test.tsx` 加两例：sheet → user → Back 回到页面且 sheet 不复活；prefill 在 Back 之后不复活。
- **测试面板的预填**（目的地、端口、账号）通过 history state 的 `prefill` 传入，**不进 URL**（R12）。
- **记录 tab 的搜索词**只存在组件状态里。API 请求里的 `q` 会进入反向代理的访问日志；与页面 URL 不同，它不会被复制、分享或留在浏览器历史里，这一点接受，并写进 `docs/access-control.md` 的「存储与隐私」。PSP 自己的访问日志不写 `/dest/` 的查询串（P7）。

**页外落点**：

| 位置 | 内容 | 屏 |
|---|---|---|
| 服务器页 `/admin/servers` | psp 行状态列下加一行「访问控制：<状态>」，点击打开单节点对话框 | S15 |
| 服务器详情 `/admin/servers/:id`（WP-D4，尚未实现） | 概览 tab 的最后一个区块，复用 `NodePolicyStatusRow` | S15 |
| 账号抽屉 `RiskUserDrawer` | 第五个 tab「访问」，三个宿主都显示 | S16 |
| 分组 `/admin/groups` | 编辑对话框第四个 tab「目的地」（只读摘要）；列表行显示模式徽章 | S17 |
| 风控中心「策略」tab | 第七张卡「访问拦截」；抽屉概览加第六行检测器；待处理原因句 | S19 |
| 系统设置 | 新 tab「隐私与协议」（`legal`，排在 `sso` 之后，`SettingsView.tsx:439-445`） | S21 |
| 公开路由 `/legal/terms`、`/legal/privacy` | 放在 `RequireAuth` 之外，与 `/login` 同级（`router/index.tsx:74-83`） | S22 |
| 用户侧 | 同意对话框（`UserLayout`，只对 role=user）；注册页必勾项；登录页、注册页、用户中心页脚链接 | S23 |
| 规则库页 | HelpTip 加一句反向说明并链接过来：「服务端拦截在「访问控制」；规则库只决定客户端怎么分流」 | — |

**各阶段出现的内容**（§11）：

| 阶段 | 出现 |
|---|---|
| 1c | 页面骨架、策略、列表、豁免抽屉、测试面板、节点覆盖抽屉（不含记录方式）、服务器页状态行与单节点对话框、数据与下发设置、结论条、账号抽屉「访问」tab 的「分组 / 豁免」两行 |
| 2c | 记录 tab、命中数列、记录方式（S14、S15）、抽屉「访问」tab 的命中部分、风控中心集成 |
| 3 | 隐私与协议三屏 |
| 4 | 记录方式里的「命中和按网站用量」选项、抽屉的按网站用量 |
| 5 | 白名单分组 tab、启用对话框、试运行报告、分组对话框 tab 与列表徽章 |

未上线的 tab 不渲染；指向它的链接（如 `?tab=records`）回落到 `policies`。

**逐元素出现的阶段**（§7.0 第 1 条不许放占位，所以凡是数据来自后续阶段的元素，都按下表出现；表里没有的元素随它所在的屏一起出现）：

| 元素 | 1c | 2c 起 | 4 起 | 5 起 |
|---|---|---|---|---|
| S1 副标题 | 「决定用户能通过原生节点访问哪些目的地。」 | 加「并记录规则命中。」 | — | — |
| S1 副行「记录命中的节点 N 台」 | 不出现 | 出现 | — | — |
| S1 [查看账号…]、S5「打开账号」 | 出现：打开账号抽屉的「访问」tab，1c 只有「分组 / 豁免」两行（S16） | 「访问」tab 加命中区块 | 加用量区块 | — |
| S2 命中数列、「在记录中查看」 | 不出现 | 出现 | — | — |
| S4 转为拦截对话框 | 只有正文与复选框，不读命中，不显示数字区 | 数字区出现 | — | — |
| S13 [加入放行例外…] | 只有「全局放行例外」与「只豁免」两项 | — | — | 加「该组的补充列表」 |
| S14「记录了什么」、行内「24 小时 N 次」、记录方式 | 不出现 | 出现 | 记录方式第三项 | — |
| S15 对话框 | 只有状态行与问题行 | 加「记录方式」「24 小时命中」两块 | — | 加「白名单分组」一块 |
| 轨道第 ④ 步、S12「白名单拒绝」卡与「包含白名单试运行」 | 不出现 | 不出现 | 不出现 | 出现 |

这张表决定了 §11.1 的 1c 也要带上 S16 的「分组 / 豁免」两行与 `GET /dest/users/:id` 的这一部分。

### 7.2 共用视觉规则

**前置 PR UI-0**（行为不变，阶段 1c 之前合并）：

| 动作 | 从 | 到 |
|---|---|---|
| 抽出 `Tone`、`amber`、`stateTone`、徽章组件（现为未导出的 `Badge`，`:66`） | `views/admin/diagnostics/StatusBadge.tsx` | `components/ToneBadge.tsx`，导出 `ToneBadge({ tone, label, testId?, data? })`；诊断页的 `CardStateBadge`/`SeverityBadge` 改为调用它 |
| **修正对比度**（F56）：`attention`、`warn`、`ok`、`notice` 的**文字**改用 `md.onSurface`，图标保留 amber / `success` / `info` 色，照 `StatusLine` 的 attention 已有的 `text: md.onSurface`（`diagnostics/StatusLine.tsx:25,31`）。实际对浅/深色卡片表面合成 alpha 底色后，warn 与 notice 也未达到 4.5:1，不能只修 attention/ok。这是 UI-0 里唯一一处会改变诊断页外观的地方 | 同上 | 同上；新增 `components/ToneBadge.contrast.test.ts`：浅色与深色主题下，逐个 tone 计算文字与合成后的底色的对比度，必须 ≥ 4.5 |
| 抽出结论条外壳（底色 + 图标 + 标题 + 副行 + 动作槽）。现在的 props 绑死在 `DiagnosticsSnapshot` 上（`:48-57`） | `views/admin/diagnostics/StatusLine.tsx` | `components/StatusLine.tsx`，props `{ tone: 'failing'\|'attention'\|'measuring'\|'ok'\|'quiet', title, detail?, actions?, meta? }`；`quiet` 是新增的一档：底 `md.surfaceContainerHighest`、文字 `md.onSurfaceVariant`、图标 `RadioButtonUnchecked`；诊断页把自己的 `VerdictTone` 映射过来 |
| 移动 `KpiTile` 与 `KpiGrid`（两者都已存在，`KpiTile.tsx:32`），并让 `KpiTile` 支持可点：`<KpiTile pressed? onToggle? />`。可点时外层是 `CardActionArea` + `aria-pressed`，尺寸、底色、字号与静态砖完全相同；按下时用 `md.secondaryContainer` 底加 `1px md.primary` 描边 | `views/admin/diagnostics/KpiTile.tsx` | `components/KpiTile.tsx` |
| `FieldHint` 加 `tone='amber'`：图标用 `amber(theme).fg`，文字用 `md.onSurface`。原来的 `'warning'`（`warning.main` 文字，约 3:1）保留给旧页面，新页面不用 | `components/FieldHint.tsx` | 原地 |
| 扩展 `CodeEditor`（F56）：加 `language?: 'yaml' \| 'plain' \| 'markdown'`（默认 `yaml`，旧调用不变；`plain` = 不加语言扩展；`markdown` 需要新增依赖 `@codemirror/lang-markdown`，首版若不加就用 `plain`）、`minRows?: number`（换算成高度）、用 `forwardRef` 暴露 `{ revealLine(n: number): void }`（跳到第 n 行并选中），以及 `dark` 默认取 `theme.palette.mode === 'dark'`。调用处一律保持 `React.lazy` | `components/CodeEditor.tsx` | 原地 |
| 移动 `PolicyField`，spec 类型泛化为 `{ key, label, hint?, min?, max?, tail? }`，不再绑死 `admin:risk_center.policy.` 前缀；风控中心把自己的 `PolicyFieldSpec` 映射过来 | `views/admin/risk/policy/PolicyField.tsx` | `components/PolicyField.tsx` |
| 移动 `useLeaveGuard`，参数化离开判定 `(next, current) => boolean`。它只管**路由离开与刷新** | `views/admin/risk/policy/useLeaveGuard.ts` | `src/hooks/useLeaveGuard.ts` |
| 新增 `useDirtyClose(dirty, copy)`：给对话框的 `onClose` 用（✕、Esc、点背景都不是导航，`useLeaveGuard` 拦不到），内部调 `confirm()` | — | `src/hooks/useDirtyClose.ts` |
| 移动并泛化 `useDrawerParam` | `views/admin/risk/drawerParam.ts` | `src/hooks/useDrawerParam.ts`（§7.1） |

完成判据：除 import 路径、上面写明的对比度修正与 §7.1 明确要求的 history 标记字段 `riskDrawer` → `drawer` 的断言迁移之外，诊断页、风控中心的现有测试不改。`drawerParam.test.tsx`、`queue/QueueTab.test.tsx` 等直接 import 旧路径的测试只改 import（涉及上述标记的断言仅更名字段）；旧路径各保留 re-export，下一个 PR 再删。新页面只从 `components/` 与 `hooks/` 引用这些部件。

**复用清单**（新页面必须用这些，不另写）：

| 部件 | 路径 | 用途 |
|---|---|---|
| `PageHeader` | `components/PageHeader.tsx` | 页标题（h4）+ 副标题 + 右侧动作，全站唯一的页头 |
| `HelpTip` | `components/HelpTip.tsx` | tab 条右端的「?」；`labelKey` 传 `admin:access_control.help.label`（默认值是风控中心的键，`:22`） |
| `FieldHint` | `components/FieldHint.tsx` | 字段下一句结论，可展开细节。新页面只用 `tone='amber'`（提醒）或 `'muted'`（说明）。`detail` 必填：**§7.4 里凡是写到 FieldHint 的地方，都要同时给出 summary 与 detail 两句文案**，实现者不得自己补 |
| `AsyncButton` / `AsyncIconButton` | `components/AsyncButton.tsx` | 所有发请求的按钮 |
| `confirm()` | `components/ConfirmHost.tsx` | 只放文字的确认框 |
| `pushSnack` | `components/SnackbarHost.tsx` | 写入结果 |
| `PagedTableFooter`、`SortableTableCell` | `components/` | 分页与排序 |
| `UserAutocomplete` | `components/UserAutocomplete.tsx` | 选账号 |
| `CodeEditor` | `components/CodeEditor.tsx`（UI-0 扩展后） | 自定义列表、IP 段用 `language="plain"`；隐私页用 `markdown`（或首版 `plain`） |
| `ToneBadge`、`StatusLine`、`KpiTile/KpiGrid` | `components/`（UI-0） | 状态、结论、数字砖。**所有数字砖都用 `KpiTile`**，可点的用 `pressed/onToggle`（`aria-pressed`，再点一次撤销）。**不要再照抄** `QueueMetricCards` 的 outlined Card 写法：同一页上不出现两种数字砖 |
| 粘底保存栏 | 写法照 `views/admin/risk/policy/PolicyTab.tsx:287-288` | 页内设置 |
| `PolicyField` | `components/PolicyField.tsx`（UI-0 移动后） | 「留空 = 默认」的数字字段 |
| `useDirtyClose` | `src/hooks/useDirtyClose.ts`（UI-0） | 有未保存修改的对话框，关闭前确认 |
| 右侧抽屉 | 尺寸与表面照 `RiskUserDrawer.tsx:198-203`；层级见下方「表面层级」 | 查看单个对象 |
| 时间 | `utils/relativeTime.ts:14` `formatRelativeTimeShort`；`utils/datetime.ts:27` `formatMsDualTz(ms, panelTz)` | 列表写相对时间，悬停显示面板时区的绝对时间 |
| 上移 / 下移按钮 | 写法照 `components/ProxyGroupMembersEditor.tsx`（`ArrowUpward`），**不抄它的 draggable** | 排序 |

**页面骨架**：
- 外层 `Box sx={{ p: { xs: 2, sm: 3 }, minWidth: 0 }}`，同 `DiagnosticsView.tsx:220`。**不要抄**风控中心的 `p: 3`（`RiskCenterView.tsx:78`），那在手机上是 24px 页边。
- tab 条：外面包一层 Box，加 `1px md.outlineVariant` 下边线；`Tabs` 显式写 `variant="scrollable" allowScrollButtonsMobile`，因为紧凑密度会覆盖 `MuiTabs` 的默认值（`theme/index.ts:180,342`）；右端放 `HelpTip`（照 `RiskCenterView.tsx:86-94`）。

**封闭色表**（`utils/accessControl.ts` 导出 `accessTone(theme, kind)`；节点状态复用 `stateTone`，个别图标在这里覆盖，见 S14）。**每一行是一种语义，任意两行的 (底色, 文字色, 图标) 三元组都不相同**；同一语义的不同措辞（如「已停用」「已暂停」都是「管理员主动关掉的」）共用一行。`accessTone` 的表驱动测试断言三元组两两不同。

| 语义 | 底色 | 文字色 / 图标色 | 图标 |
|---|---|---|---|
| 动作：拦截 | `md.surfaceContainerHighest` | `md.error` | `Block`（**只给拦截用**） |
| 动作：白名单拒绝 | `md.surfaceContainerHighest` | `md.error` | `LockOutlined` |
| 动作：仅观察 | `md.secondaryContainer` | `md.onSecondaryContainer` | `VisibilityOutlined` |
| 动作：本应拒绝（试运行）；阶段「试运行」 | `md.secondaryContainer` | `md.onSecondaryContainer` | `ScienceOutlined` |
| 动作：放行 | `md.surfaceContainerHighest` | `md.onSurfaceVariant` | `CheckCircleOutline` |
| 结论：豁免 | `md.surfaceContainerHigh` | `md.onSurface` | `VerifiedUserOutlined` |
| 白名单执行中 | `md.surfaceContainerHigh` | `md.onSurface` | `LockOutlined` |
| 状态 ok（已生效、已更新） | `md.surfaceContainerHigh` | 文字 `md.onSurface`，图标 `palette.success.main` | `CheckCircleOutline` |
| 状态 measuring（下发中、刷新中、等待首次下载、等待清理） | `md.secondaryContainer` | `md.onSecondaryContainer` | `HourglassEmpty` |
| 状态 attention（需升级、刷新失败仍用旧内容、列表未就绪、下发超过 10 分钟、24 小时内到期） | `amber(theme).bg` | 文字 `md.onSurface`，图标 `amber(theme).fg` | `WarningAmber` |
| 状态 failing（被拒、规则太多、入站没有识别域名、可用节点为 0、引用从未下载的列表） | `md.errorContainer` | `md.onErrorContainer` | `ErrorOutline` |
| 主动关掉的（策略已停用、全局已暂停） | `md.surfaceContainerHighest` | `md.onSurfaceVariant` | `PauseCircleOutline` |
| 不适用（3X-UI / S-UI，设计如此） | `md.surfaceContainerHighest` | `md.onSurfaceVariant` | `RemoveCircleOutline` |
| 没有（没有策略、不限、未豁免） | `md.surfaceContainerHighest` | `md.onSurfaceVariant` | `RadioButtonUnchecked` |
| 未知（离线、无法判断） | `md.surfaceContainerHighest` | `md.onSurfaceVariant` | `HelpOutline` |
| 永久（豁免没有到期时间） | `md.surfaceContainerHighest` | `md.onSurfaceVariant` | `AllInclusive` |

- 「动作」是用词，不是告警：拦截不用红底。
- 红色（errorContainer）只给需要管理员处理的事。设计如此的「不执行」（3X-UI / S-UI）只用安静色。
- 未判断、不适用、0 次一律安静色，**绝不画绿**。
- 整页结论取最严重的一项；只要有启用的策略、而 0 台节点会执行它，就不能画绿。
- 「试运行」与「下发中」以前共用 `HourglassEmpty`，S8 把「试运行 · 第 3 天」与「2 台节点正在下发本组规则」并排放，正是 F44 的那种同形异义，所以试运行改用 `ScienceOutlined`。`Block` 以前同时表示拦截、已停用和 `stateTone` 的 `not_applicable`，现在只表示拦截。

**表面层级**：

| 层 | 表面 | 形状 |
|---|---|---|
| 页面 | `md.surface` | — |
| 区块、卡片、列表行容器 | `md.surfaceContainerLow` + `1px md.outlineVariant` | `borderRadius: 3`，内边距 12–16 |
| 数字砖 | `md.surfaceContainer` | `borderRadius: 2` |
| 中性提示条 | `md.surfaceContainerHigh` + `InfoOutlined` | `borderRadius: 2` |
| 粘底保存栏 / 批量栏 | `Paper elevation={3}` + `md.surfaceContainerHigh` | `borderRadius: 3`，`position: sticky; bottom: 0` |
| 对话框 | `md.surfaceContainerHigh` | `borderRadius: 3`；长表单 `maxWidth` 720，列表编辑 `md`，确认 `sm`；xs 下 `fullScreen` |
| 右侧抽屉 | `md.surfaceContainerLow` | 桌面宽 560、手机 100vw，`borderTopLeftRadius: 16`。**页面级抽屉（S5、S6、S13、S14）用默认的 `zIndex.drawer`**：它们会从自己里面打开对话框（S6 → S7、S5 → 添加豁免、S14 → 记录方式、S13 → 加入放行例外），默认层级下对话框（`zIndex.modal`）自然在上；它们从不叠在对话框上。**只有 RiskUserDrawer 用 `riskDrawerZIndex`**（`modal + 1`），它靠 `aboveDrawer` 重设子树主题把自己的对话框再抬高（`RiskUserDrawer.tsx:29,37,209`）。每个页面级抽屉加一例测试：从它打开的对话框 z-index 高于抽屉本身 |

**字体**：
- 页标题只由 PageHeader 输出；区块标题 16/600，`component="h2"`；卡片标题 14/600。
- 正文 13–14；辅助文字 12–13，颜色 `md.onSurfaceVariant`。
- 数字右对齐、`fontVariantNumeric: 'tabular-nums'`，千分位用 `Intl.NumberFormat(i18n.language)`。
- `ui-monospace` 13px 只用于需要复制的内容：目的地、列表条目、URL、Markdown 源文。

**行**：
- 列表一律两行式：第一行是主语 + 徽章 + 右侧数字或操作；第二行是 13px、`md.onSurfaceVariant` 的说明。
- `≥ md` 才用表格；`< sm` 时把次要列折进第一格的第二、三行（写法照 `risk/queue/QueueTab.tsx`），或整行改成块（照 `diagnostics/PanelOpTable.tsx`）。
- 网格一律 `minmax(0, 1fr)`。
- 长目的地单行省略，悬停 Tooltip 看全文；换行处用 `overflowWrap: 'anywhere'`。

**加载、空、错误**：
- 首次读取：与内容同形的 Skeleton。
- 再次读取：表格上方固定 4px 槽位放 `LinearProgress`，布局不跳。
- 读取失败：`Alert severity="error"` + [重试]。
- 已有数据、刷新失败：保留数据，副行加 amber 文字「刷新失败，数据可能已过期」。
- 503：`Alert severity="info"`「访问控制未接线：服务端没有启用这个功能」。
- 空状态：一句话说清为什么空，再给下一步按钮。筛选后的空与真的空分开写。

**术语表**（界面上永不出现左栏）：

| 内部词 | zh-CN | en-US |
|---|---|---|
| block / observe / allow | 拦截 / 仅观察 / 放行 | Block / Watch only / Allow |
| allowlist deny / trial would-deny | 白名单拒绝 / 本应拒绝 | Allowlist denied / Would be denied |
| destination（不叫 URL、网址） | 目的地 | Destination |
| eTLD+1、site（试运行报告与用量只到这一级，区别于完整的「目的地」） | 主域名 | Site |
| list | 列表 | List |
| CatchAll | 名单外 | Outside the list |
| off / hits / hits_and_usage | 不记录 / 只记录规则命中 / 记录命中和按网站用量 | Don't record / Rule matches only / Matches and per-site usage |
| core restart | 节点重启代理程序 | The node restarts its proxy |
| sniffing insufficient | 入站没有识别域名 | Inbound does not detect domains |
| capability missing | 节点版本过旧，需升级 | Node agent too old — upgrade needed |
| over_limit | 规则太多 | Too many rules |
| fallback / LKG | 仍在执行上一版 | Still running the previous version |
| open | 不限 | Unrestricted |
| trial | 试运行 | Trial |
| enforce | 执行中 | Enforced |
| base allow | 基础放行 | Always allowed |
| extra | 补充 | Additions |
| allowlist lists | 名单 | Allowlist |
| coverage | 节点覆盖 | Node coverage |
| audit_collect | 记录方式 | Recording |
| exception | 放行例外 | Allow exception |
| paused | 已暂停 | Paused |
| listener | 入站 | Inbound |
| publish | 下发 | Apply |
| exemption | 豁免 | Exemption |
| A 档 / B 档、PolicyStatus、ETag、webhook、rule id、issue code、generation | 永不出现 | never shown |

**i18n**：
- 键名：
  - `admin:access_control.<区域>.<键>`（蛇形命名）；
  - 账号抽屉：`admin:risk_center.drawer.tab_access` 与 `admin:access_control.drawer.*`；
  - `admin:groups.destination.*`、`admin:servers.access.*`、`admin:settings.legal.*`；
  - 公开页与注册页用 `auth:legal.*`，用户中心用 `user:legal.*`，导航用 `nav:admin.access_control`。
- 不新增命名空间。zh-CN 与 en-US 同时改，zh-TW 由构建生成，不手改。`t()` 不带中文 defaultValue。
- 守卫测试扩展：
  - `src/i18n/riskKeys.test.ts:184-187` 的目录表加 `views/admin/accessControl`（atLeast 20）、`views/admin/groups`、`views/admin/legal`；
  - `src/i18n/tunableText.test.ts:24` 的 `scanned` 加 `access_control.`、`groups.destination.`、`servers.access.`、`settings.legal.`。
- **固定窗口一律夹到保留期并插值**（R14）。`tunableText.test.ts` 的字面量清单（`:27-30`）含「最近 7 天」，本章的文案一旦写死这类数字，加进 `scanned` 之后构建就会失败——这正是要的效果。规则：
  - `win = min(固定窗口, 对应保留天数)`，文案一律写 `{{days}}`；
  - 「最近 7 天」类文案统一写成「过去 {{days}} 天」；
  - `access_control.*` 下不出现 tunableText 字面量清单里的任何一项。

### 7.3 数据层

- `src/api/accessControl.ts` 只放 DTO 与请求；`src/query/accessControl.ts` 放 hooks。键放进 `query/keys.ts` 的 `accessControlKeys`。**文件名不要叫 `policies.ts`**：那个名字已被缓存新鲜度占用。
- `query/policies.ts` 新增条目，每条都写 `note` 说明理由（F45）。`ResourcePolicy` 的 `gcTime` 是必填字段，`refetchInterval` 的类型是 `number | false`（`query/policies.ts:18-20`），表达不了「有条件地轮询」，所以条件轮询这样写：**policy 里的 `refetchInterval` 是上限；hook 用 `refetchInterval: q => active(q.state.data) ? policy.refetchInterval : false`**。

  | 资源 | staleTime | gcTime | refetchInterval（上限） | 何时轮询 | note |
  |---|---|---|---|---|---|
  | `destStatus` | 15s | 5 分钟 | 10s | 有节点 `pending`，或有待发布的改动；页面隐藏时暂停 | 观察下发进度 |
  | `destDefinitions`（策略、列表、豁免、白名单分组） | 5 分钟 | 15 分钟 | false | — | 写入后主动失效 |
  | `destListRefreshing` | 0 | 1 分钟 | 5s | 有 `state = refreshing` 的列表（S6 [立即刷新] 之后） | 等刷新结束 |
  | `destHits` | 30s | 5 分钟 | false | — | 查询型页面 |
  | `destTrialReport` | 60s | 5 分钟 | false | — | 聚合查询，较重 |
  | `destUser` | 30s | 5 分钟 | false | — | 抽屉 tab |

- **写后失效矩阵**（每个变更 hook 都要有一条测试，断言失效了哪些键）：

  | 写入 | 失效 |
  |---|---|
  | 策略 | policies、status、policyPreview、lists（引用计数） |
  | 列表（含 `entries` 追加、放行例外） | lists、policies、status、trialReport（`added` 标记）、groups（「补充」条数） |
  | 豁免 | exemptions、policies（计数）、status、`riskCenter.user(id)`、`destUser(id)` |
  | 白名单 | groups、status、trialReport、lists（自带列表）、policies（轨道第 ④ 步）、`groupKeys`（分组列表徽章） |
  | 记录方式 | status、`serverKeys` |
  | 设置 | settings、status、lists（刷新间隔的说明）、hits（保留期的说明）、trialReport（可选的天数） |
  | 暂停、立即下发 | status、policies |

- 全部用 `useQueryScope()`，与风控中心一致。

### 7.4 各屏设计

下面每一屏都写了：用途、线框、状态、交互、关键文案（zh-CN；en-US 按术语表逐句对应）、组件与文件、375px 的处理。

---

#### S1　页面骨架与整页结论条

**用途**：所有 tab 共用的外壳。结论条用一句话回答两件事：策略在整个机队上落实到了什么程度；哪些节点不会执行、为什么。

```
访问控制                                [⌕ 测试目的地] [查看账号…           ▾] [⋯]
决定用户能通过原生节点访问哪些目的地，并记录规则命中。
┌──────────────────────────────────────────────────────────────────────────┐
│ ✓ 策略在 9 台节点上生效                                     [节点覆盖 ›]   │
│   不执行：3X-UI / S-UI 2 台（设计如此）· 需升级 1 台 · 记录命中的节点 8 台  │
│   读取于 14:02:11                                                         │
└──────────────────────────────────────────────────────────────────────────┘
  策略    列表    白名单分组    记录                                       (?)
──────────────────────────────────────────────────────────────────────────────

结论条的其他形态（取最严重的一项，自上而下优先）：
⊘ 访问控制已暂停：所有节点都不执行策略                              [恢复执行]
⊗ 1 台节点拒绝了新策略，仍在执行上一版                            [节点覆盖 ›]
  法兰克福 03：入站「vless-443」没有识别域名
⊗ 有 3 条启用的策略，但没有任何节点会执行它们                      [节点覆盖 ›]
⊗ 改动没有下发：域名 57,312 / 50,000，超出额度；各节点仍执行上一版    [查看列表 ›]
⚠ 1 台原生节点需升级才能执行策略                                  [节点覆盖 ›]
⚠ 2 台原生节点离线，恢复上报后才会执行最新版本                      [节点覆盖 ›]
⚠ 列表「反诈名单」刷新失败，仍在使用 2 天前的内容                  [查看列表 ›]
◷ 改动已保存，45 秒后下发到 9 台节点                                [立即下发]
  每台节点会重启代理程序一次，正在使用的连接断开一次；连续修改会合并为一次下发。
◷ 正在下发：已生效 4 / 9 台                                       [节点覆盖 ›]
○ 还没有启用的策略                                         （安静色，不画绿）

⋯ 菜单：数据与下发设置… / 隐私与协议设置 › / ──── / 暂停全部执行…
```

**状态**：
- 首次读取：结论条位置放一个高 72 的圆角 Skeleton；tab 条照常显示，各 tab 自己画 Skeleton。
- `/dest/status` 读取失败：结论条位置换成 Alert error「读取节点状态失败」+ [重试]。各 tab 照常可用，因为定义与状态是分开读的。
- 已有数据、刷新失败：副行末尾加 amber「刷新失败，数据可能已过期」。
- 503：info「访问控制未接线：服务端没有启用这个功能」，tab 不渲染。
- 结论由纯函数 `utils/accessControl.ts` 的 `accessVerdict(status, policies)` 计算，要有表驱动测试。优先级：
  1. paused → failing；
  2. 发布被拒（`publish_error`）、有节点被拒、规则太多、入站没有识别域名，或有启用的策略而覆盖为 0 → failing；
  3. 需升级、有原生节点离线（副行写「离线 N 台」）、列表刷新失败、列表未就绪 → attention；
  4. 待下发或下发中 → measuring；
  5. ok；
  6. 没有启用的策略 → quiet。
- 副行永远列出「不执行」的构成。3X-UI / S-UI 不执行是设计如此，**只写在副行，绝不让结论条变色**。

**交互**：
- 切换 tab：replace `?tab=`，各 tab 前缀下的筛选参数保留。
- 页头：
  - [测试目的地]：push `sheet=test`（S13）。
  - [查看账号…]（`UserAutocomplete`）：选中后 push `user=<id>`，打开 RiskUserDrawer（host=`access`），直接停在「访问」tab（S16）。
- 结论条：
  - [节点覆盖 ›]：push `sheet=nodes`；副行里「需升级 1 台」等片段是链接，打开同一抽屉并带上 `node_state` 筛选。
  - [立即下发]：AsyncButton，调用 `POST /dest/publish`；之后结论条转为「正在下发」。
  - [恢复执行]：确认后调用 `PUT /dest/pause {paused:false}`。
- ⋯ 菜单：
  - 「数据与下发设置…」打开 S18。
  - 「隐私与协议设置」跳转到 `/admin/settings?tab=legal`。
  - 「暂停全部执行…」打开 S20 中的确认框。
- `(?)`：只解释当前 tab。策略 tab 的说明第一句写清与「规则库」的区别。
- **live region 只播报状态变化**：只有主句放在 `role="status"` 的元素里，并且只在结论种类或 `done/total` 变化时更新。倒计时（「{{eta}}后下发」）与「读取于 hh:mm:ss」放在 `aria-hidden` 的副行里，另给一个 `aria-label` 写绝对的下发时间。否则每次 10 秒刷新、每秒倒计时，屏幕阅读器都会读一遍。

**文案**：
- 标题：访问控制
- 副标题：1c 写「决定用户能通过原生节点访问哪些目的地。」，2c 起写「决定用户能通过原生节点访问哪些目的地，并记录规则命中。」（§7.1 的逐元素阶段表）
- 结论主句：
  - 策略在 {{n}} 台节点上生效
  - 正在下发：已生效 {{done}} / {{total}} 台
  - 改动已保存，{{eta}}后下发到 {{n}} 台节点
  - {{n}} 台节点拒绝了新策略，仍在执行上一版
  - 有 {{count}} 条启用的策略，但没有任何节点会执行它们
  - {{n}} 台原生节点需升级才能执行策略
  - 列表「{{name}}」刷新失败，仍在使用 {{ago}}的内容
  - 访问控制已暂停：所有节点都不执行策略
  - 改动没有下发：{{what}} {{used}} / {{limit}}，超出额度；各节点仍执行上一版（`{{what}}` 由 `publish_error.kind` 映射为「规则」「域名」「正则」「IP 段」「配置大小」，或「配置无效」）
  - {{n}} 台原生节点离线，恢复上报后才会执行最新版本
  - 还没有启用的策略
- 副行：
  - 不执行：3X-UI / S-UI {{a}} 台（设计如此）· 需升级 {{b}} 台 · 离线 {{d}} 台 · 记录命中的节点 {{c}} 台（最后一段 2c 起出现；值为 0 的段不写）
  - 每台节点会重启代理程序一次，正在使用的连接断开一次；连续修改会合并为一次下发。
- 按钮：测试目的地 / 查看账号… / 立即下发 / 节点覆盖 / 恢复执行
- 各 tab 的帮助：见 §7.4 末尾「帮助文案」。

**组件与文件**：
- 新建：`views/admin/accessControl/AccessControlView.tsx`、`accessParams.ts`（`parseAccessTab` 与各前缀的解析和构建）、`utils/accessControl.ts`（`accessVerdict`、`accessTone`、节点状态映射）。
- 复用：PageHeader、`components/StatusLine`、`components/ToneBadge`、HelpTip、UserAutocomplete、AsyncButton、RiskUserDrawer、`hooks/useDrawerParam`。

**375px**：页头操作区换到标题下一行：[测试目的地] 变成带 `aria-label` 的图标按钮，账号选择框占满一行，⋯ 保留。结论条的动作按钮换到正文下方，左对齐。tab 条可以横向滑动。

---

#### S2　策略 tab：匹配顺序轨道（默认 tab）

**用途**：把 §3.2 的六步画成一条轨道，让管理员一眼看出：一条策略在第几步判断、豁免和白名单插在哪里、「仅观察」命中后会终止匹配、每条策略最近有没有命中。

```
额度  规则 12 / 256 · 域名 8,420 / 50,000 · 正则 140 / 256 · IP 段 3 / 20,000
按匹配顺序，从上到下，命中第一条即停止                                 [新建策略 ▾]
│
①─ 放行 · 命中即放行，不记录；对白名单分组同样生效                    [+ 放行策略]
│  ┌───────────────────────────────────────────────────────────────────────┐
│  │ 公司内网直连  [✓ 放行]                                          [●] ⋯ │
│  │ 列表「内网」142 条 · 所有人                                             │
│  └───────────────────────────────────────────────────────────────────────┘
②─ 豁免账号 · 3 个 · 不受任何访问限制，包括白名单                          [管理]
│
③─ 拦截                                                              [+ 拦截策略]
│  ┌───────────────────────────────────────────────────────────────────────┐
│  │ 禁止发信  [⊘ 拦截] [计入风险]                                   [●] ⋯ │
│  │ TCP 25、465、587 · 所有人 · 7 天 214 次 · 2 小时前 ›                    │
│  ├───────────────────────────────────────────────────────────────────────┤
│  │ 禁止 BT  [⊘ 拦截] [已停用]                                      [○] ⋯ │ ← 整行 0.6
│  │ BT 协议 · 所有人 · 保留期内未命中                                       │
│  ├───────────────────────────────────────────────────────────────────────┤
│  │ 反诈名单  [⊘ 拦截] [⚠ 列表未就绪]                               [●] ⋯ │
│  │ 列表「反诈名单」等待首次下载，这部分暂不生效 · 分组「学生」              │
│  └───────────────────────────────────────────────────────────────────────┘
④─ 白名单分组 · 研发组 [🔒 执行中]  访客 [◷ 试运行 · 第 3 天]    [打开白名单分组 ›]
│
⑤─ 仅观察 · 放行并记录；命中后不再检查后面的规则                     [+ 仅观察策略]
│  ┌───────────────────────────────────────────────────────────────────────┐
│  │ 加密货币交易所  [◉ 仅观察]                          [转为拦截…] [●] ⋯ │
│  │ 社区分类 cryptocurrency 235 条 · 所有人 · 7 天 1,032 次 · 5 分钟前 ›    │
│  └───────────────────────────────────────────────────────────────────────┘
⑥─ 其余连接：直接放行
```

**状态**：
- 加载：额度那一行放 Skeleton，轨道上放六块高 56 的圆角 Skeleton。读取失败：Alert error + [重试]。
- 行上的徽章（ToneBadge）：
  - 已停用：安静色，整行 `opacity: 0.6`；
  - 列表未就绪：attention，悬停「引用的列表还没下载成功，这部分暂不生效，其余照常下发」；
  - 作用分组已删除：failing，第二行写「作用分组已删除，这条策略现在对谁都不生效」；
  - 规则太多：failing，「未下发，节点仍执行上一版」。
- 命中数（窗口 `hit_window_days = min(7, dest.hit_retention_days)`，由 `/dest/policies` 返回，R14）：
  - 阶段 1c 或所有节点都不记录：整列不显示；
  - 单行没有采集节点：写「—」，悬停「需节点开启记录」；
  - 0 次写「{{days}} 天未命中」；`last_hit_at` 为空（保留期内一次都没有）写「保留期内未命中」（安静色徽章）。
- 额度：平时显示规则、域名、正则、IP 段四项；`subjects`、`bytes` 只在 ≥ 80% 时追加一项。≥ 80% 用 amber 数字；> 100% 用 failing，并写「超出额度时新版本不会下发，节点继续执行上一版」（与 P3 第 1 条一致：整版不下发，绝不截断）。
- 某一步为空：在该步下面放一行虚线占位「这一步还没有策略」+ 文字按钮「添加拦截策略」等。②为空写「没有豁免账号」；④为空写「没有分组启用白名单」+ [去启用]。
- **阶段 5 之前**，轨道里没有「白名单分组」这一步：步骤编号由 `utils/accessControl.ts` 的 `pipelineSteps(features)` 计算（①放行 ②豁免 ③拦截 ④仅观察 ⑤其余放行），不要写死圆圈数字；S13 的轨迹用同一个函数。
- **整页没有任何策略**：轨道换成模板网格（S4 空状态）。
- **没有任何节点能执行**：不另放 Alert。S1 的结论条已经是 failing「有 N 条启用的策略，但没有任何节点会执行它们」，S2 只在额度行末尾加一段安静色的「当前没有节点执行」，避免同一件事出现两条不同颜色的横幅。
- 开关写入中：开关禁用，旁边显示小进度圈；失败时 pushSnack 报错，开关回滚。

**交互**：
- 点行（开关和 ⋯ 以外的区域）：打开 S3 编辑框。段标题右侧的 [+ 拦截策略] 等也打开 S3，并预设好动作。
- 行上 ⋯ 菜单：编辑 / 复制 / 上移 / 下移 / 移到本段顶部 / 移到本段底部 / 在记录中查看 / 分隔线 / 删除（`md.error` 文字）。
  - 移动：立即调用 `PUT /dest/policies/order`，提交该动作的全部 id；收到 409 就重新读取，并 snack「顺序已被其他人修改，已重新载入」。
  - 删除：先弹 S20 的确认框。
- 启用开关：立即 `PUT` 全字段（R18，只改 `enabled`）+ `updated_at`，不弹确认。收到 409 `dest_policy_stale`：重新读取，开关回滚，snack「这条策略刚被其他人修改过，已重新载入」。唯一的例外：如果这会是机队的第一次下发（S20 的 `needsFirstPublishConfirm`），先弹「启用第一条访问策略？」。
- 「{{days}} 天 214 次 ›」：replace 到 `?tab=records&rec_source=p12&rec_since={{days}}d`。
- ② [管理]：push `sheet=exemptions`（S5）。
- ④ [打开白名单分组 ›]：切到 `tab=allowlist`；分组徽章可点，push `al_group=<id>`。
- [转为拦截…]：只出现在「仅观察」行上，打开 S4 的「转为拦截」对话框。
- 额度数字：悬停列出占用最多的 3 个列表。
- 帮助里说明：同一动作内的先后只决定命中记在哪条策略名下，建议把「计入风险」的拦截策略放在上面。

**文案**：
- 顶部：按匹配顺序，从上到下，命中第一条即停止 / 额度  规则 {{a}} / {{A}} · 域名 {{b}} / {{B}} · 正则 {{c}} / {{C}} · IP 段 {{d}} / {{D}}
- 各步：
  - ① 放行 · 命中即放行，不记录；对白名单分组同样生效
  - ② 豁免账号 · {{n}} 个 · 不受任何访问限制，包括白名单
  - ③ 拦截
  - ④ 白名单分组
  - ⑤ 仅观察 · 放行并记录；命中后不再检查后面的规则
  - ⑥ 其余连接：直接放行
- 行：{{days}} 天 {{count}} 次 · {{ago}} / {{days}} 天未命中 / 保留期内未命中 / 已停用 / 列表未就绪 / 列表为空 / 作用分组已删除 / 规则太多，未下发 / 转为拦截…
- 额度：超出额度时新版本不会下发，节点继续执行上一版 / 当前没有节点执行
- snack：已移到「{{section}}」第 {{n}} 位 / 顺序已被其他人修改，已重新载入 / 已停用「{{name}}」，将随下一次下发生效 / 这条策略刚被其他人修改过，已重新载入

**组件与文件**：`accessControl/policies/PoliciesTab.tsx`、`PolicyRail.tsx`（左侧 2px `md.outlineVariant` 竖线，24px 圆点，底色 `md.surfaceContainerHighest`，数字 600 字重）、`PolicyRow.tsx`、`QuotaMeters.tsx`（compact 变体）。数据用 `useDestPolicies`。

**375px**：竖线贴左 8px，圆点缩到 20px；行的第一行是名称和徽章（徽章可换行），开关与 ⋯ 靠右；第二行是说明。段标题右侧的按钮变成 `[+]` 图标按钮（`aria-label`「新建拦截策略」等）。额度改成 2×2 的 KpiGrid。

---

#### S3　策略编辑对话框

**用途**：新建或编辑一条策略。保存之前就要让管理员看到：这条策略落在第几步、额度还剩多少、保存后多少台节点会重启，以及每个选项的真实含义。

```
┌ 新建策略 ─────────────────────────────────────────────────────────────── ✕ ┐
│ 名称  [禁止发信__________________________________]                          │
│                                                                            │
│ 动作  [ ⊘ 拦截 │ ◉ 仅观察 │ ✓ 放行 ]                                         │
│       断开连接并记录命中。                                                 │
│                                                                            │
│ 匹配什么                                                                   │
│ 列表 [内网 · 自定义 · 142 条 ✕] [＋ 选择列表…                         ▾]   │
│ ▾ 更多条件                                       已设：端口 25、465、587   │
│   IP 段（每行一个） ┌────────────────────┐  只对以 IP 发起的连接生效 ⓘ    │
│                    │10.0.0.0/8          │                                 │
│                    └────────────────────┘                                 │
│   端口 [25,465,587          ]   网络 [ 全部 │ TCP │ UDP ]                  │
│   ☐ BT 协议   只识别明文握手与 uTP ⓘ        ☐ 内网与云元数据地址            │
│                                                                            │
│ 对谁生效  (•) 所有人    ( ) 指定分组 [学生 ✕][＋                     ▾]    │
│                                                                            │
│ 记录  [●] 计入风险信号   24 小时内达到 {{threshold}} 次会进入风控中心「待处理」 ⓘ │
│                                                                            │
│ 额度（保存后）                                                             │
│ 域名   ▓▓▓░░░░░░░░░░░░░░░   8,562 / 50,000                                │
│ 正则   ▓▓▓▓▓▓▓▓▓▓▓░░░░░░░     140 / 256                                   │
│ IP 段  ░░░░░░░░░░░░░░░░░░       4 / 20,000                                │
├────────────────────────────────────────────────────────────────────────────┤
│ 拦截 · 所有人 · TCP 25、465、587 · 在第 ③ 步、拦截的第 2 条判断              │
│ 保存后约 {{minutes}} 分钟内在 9 台节点生效，节点会重启代理程序一次。        │
│                                                   [取消]  [保存]           │
└────────────────────────────────────────────────────────────────────────────┘
```

**状态**：
- 从模板打开：字段已预填，标题写「新建策略（模板：禁止发信）」。编辑：没有改动时保存按钮禁用。
- 列表选择器：
  - 选项写作「名称 · 类型 · 12,345 条（正则 3）」；
  - 未就绪的列表带 attention 小徽章，选中后 `FieldHint tone="amber"`：summary「这部分在列表下载成功前不生效」，detail「节点照常执行这条策略的其他条件；列表第一次下载成功后，下一次下发时补上。」；
  - 白名单自带的列表（`owner_group_id ≠ 0`）不出现在选项里；
  - 空的自定义列表置灰，悬停写原因。
- 额度：数据来自 `POST /dest/policies/preview`，输入停 400ms 后请求。
  - 读取中数字显示「…」；预览失败显示「—」+ [重试]，不阻止保存（服务端会再校验）；
  - 阈值与 S2 相同：≥ 80% 用 amber；> 100% 用 failing，写「超出额度时新版本不会下发，节点继续执行上一版」，并禁用保存（服务端同样返回 400 `dest_policy_over_limit`）。
- 校验：
  - 名称必填；409 `dest_name_taken` 显示在名称字段上；
  - 没有任何匹配条件：「至少选择一个列表或设置一个条件」；
  - 端口格式错：给出格式示例；IP 段逐行校验，列出出错的行号；
  - 同时勾选 BT 与列表、IP 段或「内网与云元数据地址」时：不禁用，底部摘要写「会拆成两条规则下发；端口与网络对两部分都生效」（对应 ID 表的 `x1/x2`）。BT 加端口、BT 加网络**不**拆分，它们是「且」；
  - 服务端 400 按 `field` 落到对应输入框；没有 field 的放在对话框顶部的 Alert error 里；
  - 409 `dest_policy_stale`：顶部 Alert「这条策略刚被其他人修改过」+ [载入最新]。
- 按钮一律写「保存」：下发是尾随去抖的（P3 第 1 条），「保存并下发」会让人以为点完就生效。底部说明随改动变化：
  - 会改变节点执行内容：「约 {{minutes}} 分钟内在 {{n}} 台节点生效，节点会重启代理程序一次」；
  - 策略已停用：「已停用的策略不会下发」；
  - 只改了名称或「计入风险」：「只改名称或「计入风险」不会让节点重启」。
- 保存中：按钮显示进度，所有字段只读。

**交互**：
- 动作切换：
  - 选「放行」：隐藏「计入风险信号」，显示 `FieldHint tone="amber"`：summary「对白名单分组同样生效」，detail「放行在白名单之前判断：白名单分组的成员访问这些目的地也会被放行，等于给每个白名单分组都加了一条放行项。」；
  - 选「仅观察」：隐藏「计入风险信号」。
- 对谁生效：选中的分组正处于白名单模式时，显示 `FieldHint tone="muted"`：summary「这个分组启用了白名单」，detail「拦截在白名单之前判断，名单里的目的地也会被拦截；仅观察在白名单之后判断，对这个分组不起作用。」。
- 「更多条件」：手风琴默认折叠；有值时默认展开，标题右侧写已设条件的摘要。
- 底部摘要：由纯函数 `matchSummary` 实时生成，并算出「在第几步、第几条判断」。位置按 P7 的 priority 规则算：新建、或改变了动作的策略排在该动作那一段的末尾；同一动作内编辑不改位置。
  - **文法**：`（列表「内网」 或 IP 段 2 个 或 内网与云元数据地址 或 BT 协议） 且 端口 25、465、587 且 TCP`。「或」连接的是列表、IP 段、内网地址、BT；「且」连接的是端口与网络。只有一项时不加括号。
  - `matchSummary.test.ts` 至少覆盖四例：单列表；列表 + 端口；BT + 端口（不拆分）；BT + 列表 + 端口（拆分，摘要带拆分说明）。
- 保存成功：关闭对话框，pushSnack「已保存「{{name}}」，将随下一次下发生效」，结论条进入「待下发」。
- 关闭：✕、Esc、点背景都经 `useDirtyClose`（有未保存的修改时先确认）；切页面、刷新由 `useLeaveGuard` 兜底。
- 键盘：Tab 的顺序与视觉顺序一致；ToggleButtonGroup 支持方向键。

**文案**：
- 标题：新建策略 / 新建策略（模板：{{template}}）/ 编辑策略「{{name}}」
- 动作说明：
  - 拦截：断开连接并记录命中。
  - 仅观察：放行并记录命中；命中后不再检查后面的规则。
  - 放行：直接放行，不记录；对白名单分组同样生效，等于给每个白名单分组加了放行项。
- 提示：
  - 只对以 IP 发起的连接生效（展开：域名解析出的 IP 不参与匹配，节点不做额外解析）
  - BT 只识别明文握手与 uTP，加密 BT 和 DHT 识别不到
  - 端口写法：25,465,587 或 1000-2000
  - 第 {{lines}} 行不是有效的 IP 段
- 离开确认：放弃未保存的修改？ / 关闭后，这次的修改不会保存。 / [继续编辑] [放弃]

**组件与文件**：`policies/PolicyEditorDialog.tsx`（Dialog `maxWidth` 720，xs 下 fullScreen）、`QuotaMeters.tsx`（full 变体：4px 高圆角条，正常用 `md.primary`，接近上限用 amber 前景色，超限用 `md.error`）、`matchSummary.ts`。复用 ToggleButtonGroup、Autocomplete、Accordion、CodeEditor（IP 段，`language="plain"`、`minRows={6}`）、FieldHint、HelpTip、`hooks/useDirtyClose`、`hooks/useLeaveGuard`、confirm、pushSnack。

**375px**：fullScreen；顶栏左 ✕、中标题、右 [保存]（与桌面同一个按钮，同样的文案）；底部摘要与重启说明 sticky；动作 ToggleButtonGroup 用 `fullWidth`；网络分段换到端口下一行；额度条的数字换到条的下方。

---

#### S4　新建菜单、模板与「转为拦截」

**用途**：提供 5 个模板，以及「先观察一周、再决定拦截」的晋级路径。晋级之前，让管理员看到影响有多大，并说清实际影响为什么可能更大。

```
[新建策略 ▾]                         空状态（没有任何策略时替换轨道）：
 ┌───────────────────────────────┐  ┌ 还没有访问策略 ──────────────────────────────────┐
 │ 空白策略                        │  │ 从模板开始，或新建空白策略。分类模板默认「仅观察」，│
 │ ─ 常用模板 ─                    │  │ 先看一周命中再决定是否拦截。                       │
 │ 禁止 BT                 拦截    │  │ ┌ 禁止 BT ────────┐ ┌ 禁止发信 ───────┐ ┌ 禁止访问内网与… ┐│
 │ 禁止发信                拦截    │  │ │[⊘ 拦截][计入风险]│ │[⊘ 拦截][计入风险]│ │[⊘ 拦截]         ││
 │ 禁止访问内网与云元数据  拦截    │  │ │加密 BT、DHT 识别 │ │TCP 25 / 465 / 587│ │Xray 节点本来就会 ││
 │ 加密货币交易所          仅观察  │  │ │不到   [使用模板] │ │      [使用模板] │ │拦，主要产生记录   ││
 │ 成人内容                仅观察  │  │ └─────────────────┘ └─────────────────┘ └─────────────────┘│
 │ ───────────────                 │  │ ┌ 加密货币交易所 ─┐ ┌ 成人内容 ───────┐ ┌ 高风险金融网站 ─┐│
 │ 高风险金融网站为什么没有模板？  │  │ │[◉ 仅观察] 235 条 │ │[◉ 仅观察]       │ │没有现成分类，用  ││
 └───────────────────────────────┘  │ │      [使用模板] │ │占用正则 140/256 │ │自定义或远程列表  ││
                                    │ └─────────────────┘ └─────────────────┘ │      [新建列表] ││
                                    │ [新建空白策略]                            └─────────────────┘│
                                    └─────────────────────────────────────────────────────────┘

┌ 把「加密货币交易所」改为拦截？ ─────────────────────────────────────┐
│ ┌ 7 天命中 ─────┐ ┌ 涉及账号 ─────┐   ← 2c 起；1c 没有这一块         │
│ │ 至少 1,032    │ │ 14            │                                 │
│ └───────────────┘ └───────────────┘                                 │
│ 最多的目的地：binance.com 612 · okx.com 201 · bybit.com 98            │
│ 改为拦截后，这些连接会被断开。它会排到白名单之前判断：白名单分组      │
│ 的成员访问这些目的地也会被拦截；之前被更靠前的「仅观察」策略记走      │
│ 的连接，这次也会被拦截。所以实际影响可能比上面的数字大。              │
│ ☐ 同时计入风险信号                                                   │
│                                              [取消]  [改为拦截]      │
└──────────────────────────────────────────────────────────────────────┘
```

**模板**：

| 模板 | 内容 | 默认动作 | 计入风险 | 卡片上的说明 |
|---|---|---|---|---|
| 禁止 BT | `protocols: [bittorrent]` | 拦截 | 是 | 只识别明文握手与 uTP，加密 BT、DHT 识别不到 |
| 禁止发信 | TCP 25、465、587 | 拦截 | 是 | — |
| 禁止访问内网与云元数据 | `private: true` | 拦截 | 否 | Xray 节点本来就会拦这些地址（F9），这里主要产生记录；sing-box 见 §1.2 Q1 |
| 加密货币交易所 | v2fly `category-cryptocurrency` | 仅观察 | 否 | 235 条 |
| 成人内容 | v2fly `category-porn` | 仅观察 | 否 | 占用 140 / 256 条正则额度 |

「高风险金融网站」**不做模板**（F48）。说明卡和菜单底部的问答写清原因，并提到可选的 `category-betting-ru`（只覆盖俄罗斯，13 条）。

**状态**：
- 社区分类数据从未下载（503 `dest_geosite_unavailable`）：分类模板卡显示「社区分类数据尚未下载」，按钮换成 AsyncButton「立即下载」（`POST /geosite/refresh`）。S6、S7 用同一句话、同一个按钮，**不自动下载**。
- 已有由同一模板生成的策略（`template_key` 相同）：卡片上显示安静色徽章「已添加」，按钮换成「再添加一条」。
- 会让正则超额度：卡片上显示 `FieldHint tone="amber"`：summary「会超出正则额度」，detail「这个分类有 {{n}} 条正则，加上现有的会超过 {{limit}} 条上限。超出时新版本不会下发；可以先删掉其他正则较多的列表。」；仍可打开编辑框，由编辑框的门槛拦住。
- 转为拦截对话框：
  - **1c**：只有正文与复选框，不读命中，不显示数字区（§7.1 的逐元素阶段表）；
  - 2c 起，读取中：数字处放 Skeleton；
  - 没有命中数据（节点都不记录）：数字区写「节点还没有记录命中，无法估计影响」，**不显示 0**；
  - 读取失败：在框内显示错误，按钮仍可用。

**交互**：
- 菜单项和 [使用模板] 都打开 S3，并预填名称、动作、条件和「计入风险」。
- 高风险金融卡的 [新建列表]：切到 `tab=lists` 并打开 S7。
- [改为拦截]：非破坏性按钮（contained primary）。它 `PUT` 全字段（`action=block`，`counts_as_risk` 取复选框）+ `updated_at`（R18，没有 PATCH）；服务端按 P7 的 priority 规则把它排到「拦截」一段的末尾；snack「已改为拦截，排在「拦截」第 {{n}} 位」。409 的处理同 S2 的开关。数字来自 `GET /dest/hits?source=p<id>&group_by=site&since=<days>d`：目的地取前 3 个，「涉及账号」取响应的 `summary.users`；`days = min(7, dest.hit_retention_days)`。

**文案**：
- 空状态：还没有访问策略 / 从模板开始，或新建空白策略。分类模板默认「仅观察」，先看一周命中再决定是否拦截。
- 高风险金融：没有现成的社区分类。用自己维护的列表，或可信来源的远程列表。
- 转为拦截：
  - 标题：把「{{name}}」改为拦截？
  - 正文（2c 起）：过去 {{days}} 天至少命中 {{hits}} 次，涉及 {{users}} 个账号。……所以实际影响可能比上面的数字大。1c 只有后半段：改为拦截后，这些连接会被断开……
  - 按钮：改为拦截

**组件与文件**：`policies/TemplateMenu.tsx`（`Button` + `ArrowDropDown` 打开 `Menu`，与 S6 的「新建列表」相同；MUI 没有 SplitButton 组件，仓库也没有先例）、`TemplateGrid.tsx`（CSS grid：xs 1 列、sm 2 列、md 3 列）、`templates.ts`（字段与上表一致，加测试）、`ConvertToBlockDialog.tsx`。

**375px**：模板网格单列；转为拦截对话框 fullScreen，目的地每行一个。

---

#### S5　豁免抽屉（`sheet=exemptions`）

**用途**：豁免是最大的口子：被豁免的账号不受任何限制，包括白名单。所以豁免集中在一处管理，每一条都写明谁加的、为什么、何时到期。

```
┌ 豁免账号 ─────────────────────────────────────────────── ✕ ┐
│ 豁免的账号不受任何访问限制，包括白名单。只用于排障或特殊账号。 │
│                                               [添加豁免]   │
│ ┌──────────────────────────────────────────────────────┐   │
│ │ alice@corp                     [◷ 23 小时后到期]   ⋯ │   │
│ │ 排查客户端连不上 · 由 admin 于 1 小时前添加            │   │
│ ├──────────────────────────────────────────────────────┤   │
│ │ ops-bot                        [永久]              ⋯ │   │
│ │ 监控探针 · 由 kazuha 于 3 天前添加                     │   │
│ └──────────────────────────────────────────────────────┘   │
└────────────────────────────────────────────────────────────┘

┌ 添加豁免 ────────────────────────────────────────┐
│ 账号  [选择账号…                            ▾]  │
│ 原因  [______________________________] 0 / 255  │
│ 到期  [ 永久 │ 24 小时 │ 7 天 │ 自定义… ]          │
│ 约 {{minutes}} 分钟内生效。                      │
│                                [取消]  [添加]   │
└──────────────────────────────────────────────────┘
```

**状态**：
- 加载：3 行两行式 Skeleton。读取失败：Alert error + [重试]。
- 空：「没有豁免的账号。建议只在排查问题时临时豁免，并设置到期时间。」+ [添加豁免]。
- 到期徽章：24 小时内到期用 attention；永久用「永久」那一行的安静色。徽章悬停写「正常同步时到期后最迟约 1 小时加最长下发等待才真正失效」（P3 第 2 条）。
- **已到期、尚未清理**（`expired: true`）：到期的行要等每小时一次的清理加一个去抖窗口才消失，这期间不能显示成负数。用 measuring 色调写「已到期，等待清理（最多 1 小时）」，排在列表末尾。
- 添加：
  - 账号必选，原因必填；
  - 409 `dest_exemption_exists`：账号字段下写「这个账号已被豁免」；
  - 账号属于白名单分组：`FieldHint tone="amber"`：summary「这个账号属于白名单分组「{{group}}」」，detail「豁免后，白名单和所有访问策略都不再对它生效，直到豁免到期或被取消。」；
  - 选「自定义」：出现 `datetime-local`，旁边注明「浏览器时间」；
  - 「24 小时」选项下方写同一句：「正常同步时到期后最迟约 1 小时加最长下发等待才真正失效」。

**交互**：
- 行上 ⋯：打开账号 / 修改原因与到期 / 取消豁免。
  - 打开账号：push `user=<id>`，并在同一次 push 里删掉 `sheet`；
  - 取消豁免：先弹 S20 的确认框。
- 添加：不弹确认；snack「已豁免 {{upn}}，将随下一次下发生效」，结论条进入待下发。
- 同样的操作在账号抽屉的「访问」tab 里也能就地完成（S16），两处调用同一个 mutation。

**文案**：
- 标题：豁免账号 / 豁免的账号不受任何访问限制，包括白名单。只用于排障或特殊账号。
- 行：{{reason}} · 由 {{by}} 于 {{ago}}添加 · {{expiry}}
- 到期：{{left}}后到期 / 永久 / 已到期，等待清理（最多 1 小时）/ 正常同步时到期后最迟约 1 小时加最长下发等待才真正失效

**组件与文件**：`sheets/ExemptionsSheet.tsx`、`sheets/AddExemptionDialog.tsx`（S16 共用）。

**375px**：抽屉 100vw；添加对话框 fullScreen，到期分段占满一行。

---

#### S6　列表 tab 与条目抽屉

**用途**：管理所有目的地列表，回答三个问题：额度还剩多少；哪个列表坏了、但还在用旧内容；谁在用这个列表。

```
[新建列表 ▾ 自定义 / 远程地址 / 社区分类]
┌ 域名条目 ──────┐┌ 正则 ──────────┐┌ IP 段 ─────────┐┌ 有问题 ────────┐  ← 四块都是 KpiTile；最后一块可点即筛选
│ 8,420          ││ 140            ││ 3              ││ 1              │
│ 共 50,000      ││ 共 256         ││ 共 20,000      ││ 刷新失败       │
└────────────────┘└────────────────┘└────────────────┘└────────────────┘
远程与分类列表每 24 小时刷新一次 [修改]
名称                       条目          状态                          使用情况
内网                       142           [✓ 已更新] 3 天前              2 条策略       ⋯
  自定义
反诈名单                   8,012         [⚠ 刷新失败，仍用旧内容]        1 条策略       ⋯
  远程 · raw.githubusercontent.com       HTTP 404 · 2 天前
加密货币交易所             235           [✓ 已更新] 6 小时前            1 条策略       ⋯
  社区分类 · cryptocurrency
访客 · 基础放行            1             [✓ 已更新]                    白名单「访客」专用 ⋯
  自定义
新闻源                     —             [◷ 等待首次下载]               未使用         ⋯
  远程 · example.org

条目抽屉（sheet=list&list=<id>）：
┌ 反诈名单 ─────────────────────────────────────────── ✕ ┐
│ 远程 · https://raw.githubusercontent.com/…/list.txt     │
│ [domain 7,990] [full 12] [regexp 0] [IP 段 10]  ← 可点即筛选 │
│ 共 8,012 条，这里只显示前 200 条（搜索只在这 200 条里进行）│
│ domain   example-scam.com                 [测试此目的地] │
│ full     login.fake-bank.net                             │
│ [立即刷新]  [编辑]                                       │
└──────────────────────────────────────────────────────────┘
```

**状态**：
- 加载：KpiGrid 与 5 行都用 Skeleton。读取失败：Alert error + [重试]。
- 空：「还没有列表。策略和白名单都从列表里取目的地。」+ [新建列表 ▾]。
- 状态徽章：
  - 已更新：ok；
  - 刷新中：measuring；
  - 刷新失败，仍用旧内容：attention，第二行用 `md.error` 小字写错误原文和时间，悬停看完整的 `last_error`；
  - 等待首次下载：被启用的策略引用时用 failing「从未下载，引用它的部分暂不生效」，未被引用时用 measuring；
  - **下载失败，从未成功**（`failed` 且 `last_fetched_at` 为空）：failing，第二行写错误原文。这时没有旧内容可用，不能写「仍用旧内容」；
  - 「白名单「X」专用」不是状态，写在「使用情况」一列，纯文字，不画徽章。
- 社区分类行的 parse_report_summary.ignored_broad > 0 时，显示 amber 提示「已剔除 {{n}} 条过宽条目」，可打开条目抽屉查看最近成功解析报告；不把它算作 failed 或「有问题」筛选。过滤后为空的刷新按失败状态显示，区分有无旧内容。
- 社区分类数据整体不可用（503）：表格上方 Alert info「社区分类数据尚未下载」+ AsyncButton「立即下载」，与 S4、S7 同一句话。
- 额度砖：阈值与 S2 相同（≥ 80% amber，> 100% failing），写「超出额度时新版本不会下发，节点继续执行上一版」。
- `lst_state` 只有一个取值 `problem`：`problem` = `failed`（含从未成功），或「被启用的策略引用、但仍在等待首次下载」。

**交互**：
- 行上 ⋯：查看条目 / 编辑 / 立即刷新（只对远程与分类）/ 删除。
  - 被引用时「删除」置灰，悬停写「被 N 条策略、M 个白名单分组使用」；
  - 如果仍收到 409 `dest_list_in_use`：不弹确认，改弹 S20 的「列表正在被使用」说明框。
- 「使用情况」单元格可点：Popover 列出引用方，每项都是链接（策略 → S3，分组 → S8）。
- 「有问题」数字砖（`KpiTile pressed/onToggle`）：切换 `lst_state=problem`，再点一次撤销。
- [立即刷新]：AsyncIconButton，返回 202 后徽章变「刷新中」，每 5 秒重读一次，直到状态变化。
- [修改]：打开 S18，并聚焦刷新间隔。
- 条目抽屉：
  - 点行或「查看条目」后 push `sheet=list&list=<id>`；
  - 类型数字可点即筛选；条目用等宽字体；
  - [测试此目的地]：打开 S13，用 history state 预填该条目；
  - 搜索框只在组件状态里。
  - 抽屉显示持久化的最近成功解析报告（最多 20 条样本及其余数量）；无报告显示「暂无解析报告」。分类源条目不跳转编辑器。

**文案**：
- 类型：自定义 / 远程地址 / 社区分类
- 状态：已更新 / 刷新中 / 刷新失败，仍用旧内容 / 下载失败，从未成功 / 等待首次下载 / 从未下载，引用它的部分暂不生效
- 使用情况：{{n}} 条策略 / 白名单「{{group}}」专用 / 来自分组「{{group}}」（已关闭白名单）/ 未使用
- 说明：远程与分类列表每 {{hours}} 小时刷新一次

**组件与文件**：`lists/ListsTab.tsx`、`ListRow.tsx`、`UsedByPopover.tsx`（读 `used_by:[{kind,id,name}]`，每项一个链接）、`ListEntriesSheet.tsx`；复用 KpiGrid、KpiTile、ToneBadge、SortableTableCell（名称、条目、状态三列可排序，状态按最近更新时间排，与线框一致）、AsyncIconButton。[立即刷新] 之后的 5 秒重读用 `destListRefreshing`（§7.3）。

**375px**：KpiGrid 两列；每个列表改成一块：第一行名称 + 状态徽章 + ⋯；第二行类型 · 条目 · 使用情况；第三行错误或更新时间。条目抽屉 100vw。

---

#### S7　列表新建与编辑对话框（含解析报告）

**用途**：保存之前就让管理员看到列表会被怎样理解：接受多少条、忽略了哪几行、为什么、哪些被改写、哪些条目宽到等于放行全部，以及保存后会不会让节点重启。

```
┌ 新建列表 ────────────────────────────────────────────────────────────── ✕ ┐
│ [ 自定义 │ 远程地址 │ 社区分类 ]               ← 编辑已有列表时锁定        │
│ 名称 [高风险金融_____________________]                                    │
│ ┌──────────────────────────────────────────────────────────────────────┐ │
│ │  1  # 每行一条，# 开头是注释                                           │ │
│ │  2  example-scam.com                                                  │ │
│ │  3  *.fake-bank.net                                                   │ │
│ │  4  ||ads.example^$third-party                                        │ │
│ │  5  regexp:.*                                                         │ │
│ └──────────────────────────────────────────────────────────────────────┘ │
│ ▸ 支持的写法                                                              │
│ 解析结果  接受 1,204 条 · 改写 1 条 · 忽略 2 条                            │
│   第 3 行  *.fake-bank.net → domain:fake-bank.net（已改写）               │
│   第 4 行  ||ads.example^$third-party   带修饰符的规则不支持               │
│   第 5 行  regexp:.*                    过宽：几乎匹配所有域名             │
├──────────────────────────────────────────────────────────────────────────┤
│ 被 1 条启用的策略使用；内容变化会让 9 台节点各重启一次。                    │
│                                                     [取消]  [保存]       │
└──────────────────────────────────────────────────────────────────────────┘
远程地址：地址 [https://example.org/list.txt              ]  [测试拉取]
          刷新：跟随全局设置（每 {{hours}} 小时）[修改]
          拉取结果  HTTP 200 · 312 KB · 接受 8,012 条 · 忽略 0 条   ▸ 前 50 条
社区分类：分类 [cryptocurrency · 235 条 · 正则 0                    ▾]  属性 [cn ✕]（只列该分类出现过的属性）
          数据来自 v2fly domain-list-community，更新于 6 小时前
```

**状态**：
- 编辑已有的自定义列表：先 `GET /dest/lists/:id?text=1` 取回原文（含注释与行号）回填编辑框；保存时 `PUT` 带 `updated_at`，409 `dest_list_stale` 时顶部 Alert「这个列表刚被其他人修改过」+ [载入最新]（编辑框内容保留到用户确认）。
- 自定义：输入停 500ms 后调用 `POST /dest/lists/preview`（不写审计，P7），解析结果那一行显示小进度圈。
  - 被忽略的行最多列 20 条，超出时写「还有 {{n}} 条」。
  - 预览失败：Alert「无法解析：{{error}}」，不阻止编辑。
  - 超过大小上限：对话框顶部 Alert error，保存禁用。
- 过宽条目：
  - **自定义列表**：按 P2 第 4 条，过宽的行在保存时进「忽略」，以 amber 列在报告里，**不禁用保存**——保存下来的列表本来就不含它们；
  - **社区分类**：过宽条目以 amber 列在解析报告的「忽略」部分，写「已剔除，不会下发」；保留有效条目时允许保存，显示有效数量。过滤后为空时 failing「过滤后没有可用条目」，禁用此次内容提交；已存在列表继续用旧内容，首次列表未就绪。
  - **远程 URL**：预览出现过宽条目时，以 failing 列出并禁用此次保存，任何引用动作都不能绕过整份拒绝规则（P2 第 4 条）。
  - **远程 URL 的空内容**：预览没有有效条目时 failing「没有可用条目」，禁用此次内容保存；后台遇到同样结果按刷新失败显示，保留旧内容或首次未就绪。
- 远程地址：
  - 非 https 地址在输入时就报错「只支持 https 地址」，不发请求；
  - **只在点 [测试拉取] 时**才调用预览，不随输入触发（远程预览会真的去拉一次）；读取中显示进度；结果行的 HTTP 状态与大小来自响应的 `http_status`、`bytes`；
  - 失败时在框内显示 HTTP 状态或错误原文。拉取失败仍允许保存，保存后状态为「等待首次下载」。
- 社区分类：数据未下载时，选择器的位置换成 Alert info「社区分类数据尚未下载」+ AsyncButton「立即下载」（`POST /geosite/refresh`），与 S4、S6 同一句话，**不自动下载**。属性选择器只列 `GET /geosite/categories` 返回的该分类的 `attrs`（含 `!cn` 这种否定属性，按字面显示）。
- 底部说明：
  - 被启用的策略引用且内容摘要变化：写重启说明；
  - 摘要没变：「内容未变化，不会触发重启」；
  - 没被使用：「这个列表没有被使用，保存不会影响节点」。
- 有未保存的修改时关闭：经 `useDirtyClose` 先确认。

**交互**：
- 切换类型：已有输入时先弹 S20 的「切换列表类型」确认框。
- 自定义报告里的行号可点：调用 CodeEditor 的 `revealLine(n)`（UI-0），跳到该行并选中。分类报告标为「源条目 {{n}}」，展示原条目与原因，没有编辑器跳转。
- 「支持的写法」折叠区：示例覆盖 `domain:` / `full:` / `keyword:` / `regexp:` / `*.x` / `.x` / hosts 行 / AdGuard `||x^` / IP / CIDR / `#` 注释，并说明规范化规则（转小写、去尾点、中文域名转 punycode、CIDR 对齐到网络号）。
- 保存成功：snack，并打开条目抽屉让管理员确认结果。
- 白名单分组自带的列表也用这个对话框编辑，类型锁定为自定义。
- 刷新间隔不在这里设：远程与分类列表一律跟随全局 `dest.list_refresh_hours`（P1 没有每列表的刷新间隔），[修改] 打开 S18 并聚焦该字段。

**文案**：
- 解析：解析结果  接受 {{a}} 条 · 改写 {{r}} 条 · 忽略 {{b}} 条
- 忽略原因：无法识别的写法 / 带修饰符的规则不支持 / 过宽：几乎匹配所有域名 / 过宽：{{x}} 是公共后缀 / 过宽：IP 段太大 / 重复
- 改写：已改写为 {{to}}
- 远程：只支持 https 地址 / 拉取失败：{{error}} / 拉取结果  HTTP {{code}} · {{size}} · 接受 {{a}} 条 · 忽略 {{b}} 条
- 底部：被 {{n}} 条启用的策略使用；内容变化会让 {{nodes}} 台节点各重启一次。 / 内容未变化，不会触发重启 / 这个列表没有被使用，保存不会影响节点

**组件与文件**：`lists/ListDialog.tsx`（Dialog `maxWidth="md"`，xs fullScreen）、`ParseReport.tsx`（`md.surfaceContainer` 底，忽略行用等宽字体）、`GeositeCategoryPicker.tsx`。编辑框用 `CodeEditor language="plain"`（列表不是 YAML，用 YAML 高亮会把 `domain:example.com` 画成键）。

**375px**：fullScreen；类型分段 `fullWidth`；编辑器至少 10 行高，只在框内横向滚动；被忽略的行改成两行：行号 + 原因 / 原文。

---

#### S8　白名单分组 tab（阶段 5）

**用途**：列出所有启用白名单的分组。每张卡回答：处于哪个阶段、名单是什么、有多少节点不再对本组开放、试运行拦到了什么、下一步做什么。

```
白名单分组的成员只能访问名单里的目的地。                         [启用白名单…]  (?)
┌ 研发组 ───────────────────────────────────────── [🔒 执行中] ──────── ⋯ ┐
│ 成员 24 · 名单：研发白名单（312）、补充（18）· 基础放行 1 条                │
│ 可用节点 8 / 12   4 台节点不对本组开放 ⓘ                                   │
│ 过去 24 小时拒绝 1,203 次                                [查看被拒记录 ›] │
└──────────────────────────────────────────────────────────────────────────┘
┌ 访客 ──────────────────────────────────── [◷ 试运行 · 第 3 天] ───── ⋯ ┐
│ 成员 51 · 名单：访客白名单（40）、补充（0）· 基础放行 1 条                   │
│ 可用节点 8 / 12   ◷ 2 台节点正在下发本组规则                               │
│ 过去 7 天本应拒绝 128 个主域名，共 9,420 次（天数按保留期插值）             │
│                                      [试运行报告]  [切换到执行…]           │
└──────────────────────────────────────────────────────────────────────────┘
┌ 外包 ───────────────────────────────────────── [🔒 执行中] ──────── ⋯ ┐
│ ⊗ 本组成员现在没有任何可用节点                                [查看原因]   │  ← errorContainer 行内带
│ 成员 6 · 名单：外包白名单（55）· 基础放行 1 条                              │
└──────────────────────────────────────────────────────────────────────────┘
```

**状态**：
- 加载：两张卡的 Skeleton。读取失败：Alert error + [重试]。
- 空：「还没有分组启用白名单」/「白名单分组的成员只能访问名单里的目的地。先试运行，看看会拦到什么，再切换到执行。」+ [启用白名单…]。
- 阶段徽章：试运行 · 第 N 天（「试运行」那一行：secondaryContainer + `ScienceOutlined`，与「下发中」的 HourglassEmpty 区分）；执行中（`md.surfaceContainerHigh` + LockOutlined）。
- 可用节点为 0：卡片顶部一条 errorContainer 行内带（它是卡片的一部分，不是 Alert）。
- 有可用节点还没生效本组规则：一行 measuring。
- 名单里有未就绪的列表：一行 attention「名单「X」还没下载」。试运行阶段不阻止任何操作；切换到执行时会被拦下。
- 「不对本组开放的节点」用 `FieldHint tone="muted"`：summary「{{n}} 台节点不对本组开放」，detail 逐台列出「节点名 · 原因」。原因按 `ineligible.reason` 映射为六种：`xui` 3X-UI / `sui` S-UI / `capability` 节点版本过旧 / `over_limit` 规则太多 / `rejected` 节点拒绝了新策略 / `sniffing` 入站没有识别域名。

**交互**：
- [启用白名单…]：打开 S9。带 `al_enable=<id>` 进来时，第一步已选好该分组。
- 卡片 ⋯：编辑名单（打开 S9 的第 2 步单独编辑）/ 切回试运行… / 关闭白名单…（都走 S11）/ 打开分组（跳 `/admin/groups?edit=<id>&dtab=destination`）。
- [试运行报告]：push `al_group=<id>&al_view=trial`（S10）。
- [查看被拒记录 ›]：切到 `tab=records`，带 `rec_source=g<id>&rec_action=deny&rec_since=24h`。
- 名单名可点：打开该列表的条目抽屉。

**文案**：
- 成员 {{members}} · 名单：{{lists}} · 基础放行 {{base}} 条
- 可用节点 {{ok}} / {{total}} / {{n}} 台节点不对本组开放 / {{n}} 台节点正在下发本组规则 / 名单「{{list}}」还没下载
- 本组成员现在没有任何可用节点
- 过去 {{days}} 天本应拒绝 {{sites}} 个主域名，共 {{count}} 次（`days` = `would_deny_recent.days`，即 `min(7, dest.trial_retention_days)`）/ 过去 24 小时拒绝 {{count}} 次
- 原因：3X-UI / S-UI / 节点版本过旧 / 规则太多 / 节点拒绝了新策略 / 入站没有识别域名

**组件与文件**：`allowlist/AllowlistTab.tsx`、`AllowlistGroupCard.tsx`；数据用 `useDestGroups`。

**375px**：卡片单列；阶段徽章换到名称下方，⋯ 留在右上角；两个按钮放到最后一行，各占一半宽。

---

#### S9　启用白名单（三步对话框，阶段 5）

**用途**：启用白名单是风险最高的配置：配错了，用户会「所有网站都打不开」。三步各做一件事：选分组、定名单、看影响。新建一律从试运行开始。

```
┌ 启用白名单 ─────────────────────────────────────────────────── ✕ ┐
│  ① 选择分组 ── ② 名单 ── ③ 影响                                    │
├───────────────────────────────────────────────────────────────────┤
│ ① 只列出「不限」的分组                          [⌕ 搜索分组]（>8 个时）│
│   ( ) 研发组                                              24 人    │
│   (•) 访客                                                51 人    │
│   ( ) 外包                                                 6 人    │
├───────────────────────────────────────────────────────────────────┤
│ ② 本组成员只能访问这些列表里的目的地                               │
│   名单      [访客白名单 · 自定义 · 40 条 ✕] [＋ 选择列表…        ▾] │
│   基础放行  已预填 1 条：l9f26nnn5d.cloudflare-gateway.com   [编辑] │
│             订阅模板里经代理发出的 DNS 主机；缺了它，成员会全部打不开 ⓘ│
│   补充      自动创建；试运行报告里「加入白名单」的条目放这里         │
├───────────────────────────────────────────────────────────────────┤
│ ③ ┌ 成员 ────┐┌ 可用节点 ┐┌ 不再开放 ┐                             │
│   │ 51       ││ 8        ││ 4        │                             │
│   └──────────┘└──────────┘└──────────┘                             │
│   从试运行开始，本组成员不再获得以下 4 台节点：                      │
│     美西 A   3X-UI      美西 B   S-UI                               │
│     大阪 05  节点版本过旧    新加坡 04  规则太多                      │
│   试运行期间访问不受限制，只记录本应被拒绝的目的地。                 │
├───────────────────────────────────────────────────────────────────┤
│                                   [上一步]  [下一步 / 开始试运行]  │
└───────────────────────────────────────────────────────────────────┘
```

**状态**：
- 第 1 步：数据来自 `GET /dest/groups`（返回所有分组，含 `mode = open` 与人数）。纵向单选列表，每行是名称 + 人数；超过 8 个分组时顶部加搜索框（只在组件状态里）。所有分组都已启用白名单时，写「所有分组都已启用白名单」，[下一步] 禁用。
- 第 2 步：
  - 名单至少选一个；
  - 选到未就绪的列表：`FieldHint tone="amber"`：summary「试运行时跳过，切换到执行前必须就绪」，detail「这个列表还没下载成功。试运行期间它不参与判断，报告里会把它覆盖的目的地也列为本应拒绝；切换到执行时，服务端要求所有名单都已就绪。」；不阻止继续；
  - 没能从模板读出 DNS 主机：amber 提示「没能从订阅模板读出 DNS 主机，请手动填写」，允许继续，第 3 步会再提醒一次。
- 第 3 步：
  - 数字来自 `GET /dest/groups/:id/preview?list_ids=`，读取中用 Skeleton；
  - 可用节点为 0：failing 行内带「启用后本组成员将没有任何可用节点」；[开始试运行] 仍可点，点击后先弹 S20 的「白名单启用后可用节点为 0」确认框。
- 提交中：按钮显示进度。失败：框内 Alert error，停在第 3 步。

**交互**：
- 用 MUI `Stepper`，只能顺序前进，已完成的步骤可以点回去。
- 基础放行的 [编辑]：在框内展开一个 6 行的 CodeEditor。
- [开始试运行]：**只发一次请求** `PUT /dest/groups/:id {mode:"allowlist", stage:"trial", list_ids, base_hosts}`；服务端在同一个事务里建好「基础放行」与「补充」两个自带列表并写模式（P7），中途失败不会留下孤儿列表。成功后关闭对话框，push 到该分组的试运行报告，snack「「{{group}}」已开始试运行，约 {{minutes}} 分钟内生效」。
- 有未保存的输入时关闭，经 `useDirtyClose` 先确认。

**文案**：
- 步骤：选择分组 / 名单 / 影响
- 第 1 步：只列出「不限」的分组 / {{n}} 人 / 所有分组都已启用白名单
- 第 2 步：本组成员只能访问这些列表里的目的地 / 基础放行 / 已预填 {{n}} 条：{{hosts}} / 订阅模板里经代理发出的 DNS 主机；缺了它，成员会全部打不开 / 补充 / 试运行时跳过，切换到执行前必须就绪
- 第 3 步：从试运行开始，本组成员不再获得以下 {{n}} 台节点： / 试运行期间访问不受限制，只记录本应被拒绝的目的地。 / 启用后本组成员将没有任何可用节点
- 按钮：上一步 / 下一步 / 开始试运行

**组件与文件**：`allowlist/EnableAllowlistDialog.tsx`（Dialog `maxWidth="md"`）。

**375px**：fullScreen；Stepper 改成「第 2 步，共 3 步」加进度点；分组列表与节点列表都是每项一行。

---

#### S10　试运行报告（白名单子视图，`al_group=<id>&al_view=trial`）

**用途**：回答「如果现在就执行，会拦掉什么」。管理员在这里把需要的主域名逐个或批量加进白名单，再决定是否切换到执行。

```
← 白名单分组 › 访客 › 试运行报告                                 [切换到执行…]
时间 [ 1 天 │ 3 天 │ 7 天 ]   受保留期限制，最多 7 天
┌ 本应拒绝的主域名 ┐┌ 次数 ─────────┐┌ 未处理 ───────┐┌ 统计完整性 ────┐
│ 128              ││ 9,420         ││ 41            ││ 可能不完整     │
└──────────────────┘└───────────────┘└───────────────┘└────────────────┘
☐  主域名 ⓘ           次数                                 最近
☐  gstatic.com        ▓▓▓▓▓▓▓▓▓▓▓  2,210                   5 分钟前  [加入白名单]
☐  figma.com          ▓▓▓▓▓▓       1,402                   1 小时前  [加入白名单]
▒  notion.so          已加入 · 下次下发后生效
   (ip)               ▓▓             302                   2 小时前  [编辑补充列表]
                      客户端直接用 IP 连接，节点只记录为 (ip)；要放行请在名单里写 IP 段
┌──────────────────────────────────────────────────────────────────────────┐
│ 已选 3 个          加到 [访客 · 补充 ▾]                    [加入白名单（3）] │  ← 粘底批量栏
└──────────────────────────────────────────────────────────────────────────┘
```

**状态**：
- 加载：KpiGrid 与 8 行 Skeleton。读取失败：Alert error + [重试]。
- 试运行不足 24 小时：中性提示条「试运行刚开始 {{hours}} 小时，数据还少；建议至少观察 {{recommend}} 天」，`recommend = min(7, dest.trial_retention_days)`。
- 「未统计」砖改为「统计完整性」，固定显示「可能不完整」，有已观测损失时用 amber；明细分别显示相关节点未统计的行数、事件数和无法解析事件数，不相加。说明写「相关节点有记录未统计；这些计数不是本组的准确丢失量，报告可能漏了一些主域名」。
- 空：「过去 {{days}} 天没有本应被拒绝的目的地」+ [切换到执行…]。搜索后为空：「没有符合的主域名」+ [清除搜索]。
- 已加入：整行 `opacity: 0.6`，写「已加入 · 下次下发后生效」，复选框隐藏。
- **没有「账号」列，也没有「示例」列**（R5、R15：试运行只存「分组 × 主域名」，节点在聚合前就折叠了，完整主机名从不离开节点）。「主域名」表头带 HelpTip：「试运行只记到主域名，看不到完整主机名和账号」。
- `(ip)` 行没有复选框，也没有 [加入白名单]：第二行写「客户端直接用 IP 连接，节点只记录为 (ip)；要放行请在名单里写 IP 段」，按钮是 [编辑补充列表]，打开 S7 编辑本组的「补充」列表。
- 分组已在执行中：标题改为「{{group}} · 被拒绝的目的地」，内容来自执行阶段的拦截命中，按钮换成 [查看被拒记录 ›]。

**交互**：
- 时间范围：选项从 `[1, 3, 7]` 里去掉超过 `dest.trial_retention_days` 的值（R14）；写进 `al_days`，用 replace；响应里的 `effective_days` 比请求值小时，在选择器旁写出原因。
- 「次数」「最近」两列可排序。
- 单行 [加入白名单]：AsyncButton，调用 `POST /dest/lists/<补充列表 id>/entries {add:["domain:<site>"]}`（P7），**前端不重建列表全文**。成功后该行变灰，「未处理」减 1。
- 多选后出现粘底批量栏；「加到」下拉列出补充列表和本组其他自定义列表；批量同样是一次 `entries` 请求。
- 点主域名：打开 S13，用 history state 的 `prefill` 预填该主域名和本组。
- ← 返回：用 `navigate(-1)`；直接打开的链接则回到 `tab=allowlist`。

**文案**：
- 白名单分组 › {{group}} › 试运行报告 / 受保留期限制，最多 {{days}} 天
- 砖：本应拒绝的主域名 / 次数 / 未处理 / 未统计
- 表头悬停：试运行只记到主域名，看不到完整主机名和账号
- 行：加入白名单 / 已加入 · 下次下发后生效 / 客户端直接用 IP 连接，节点只记录为 (ip)；要放行请在名单里写 IP 段 / 编辑补充列表
- 提示条：试运行刚开始 {{hours}} 小时，数据还少；建议至少观察 {{recommend}} 天
- 批量：已选 {{n}} 个 · 加到 · 加入白名单（{{n}}）
- snack：已把 {{n}} 个主域名加入「{{list}}」，约 {{minutes}} 分钟内生效

**组件与文件**：`allowlist/TrialReport.tsx`、`components/BarCount.tsx`（横条 + 数字；横条按本页最大值归一，颜色 `md.primary` 加 alpha；S16 共用）、`BulkBar.tsx`。

**375px**：KpiGrid 两列；每行改成一块：复选框 + 主域名 + 次数 / 横条 / 最近；[加入白名单] 放在块底，左对齐。批量栏两行。

---

#### S11　切换到执行、切回试运行、关闭白名单

**用途**：用逐项打勾的就绪清单为「切换到执行」把关。✕ 项会挡住切换；⚠ 项只提醒，不阻止（所有者偏好少设门槛）。

```
┌ 让「访客」开始执行白名单？ ──────────────────────────────────────────┐
│ [✓] 所有名单都已就绪                                                  │
│ [⊗] 2 台可用节点还没生效本组规则：东京 07、首尔 02             [查看]  │
│ [✓] 基础放行不为空（DNS 主机 1 条）                                   │
│ [⚠] 过去 24 小时仍有 41 个主域名本应被拒绝，最多的是                   │
│     gstatic.com、figma.com、zoom.us                           [看报告] │
│ [ⓘ] 试运行已 3 天（建议观察满 7 天）                                   │
│ ┌──────────────────────────────────────────────────────────────────┐ │
│ │ 切换后，本组 51 人访问名单外的目的地会被拒绝，约 2 分钟内生效。    │ │ ← errorContainer
│ │ 可以随时切回试运行。                                             │ │
│ └──────────────────────────────────────────────────────────────────┘ │
│                                           [取消]  [切换到执行]       │
└──────────────────────────────────────────────────────────────────────┘
```

**状态**：
- 清单项：✓ 用 ok，⊗ 用 failing，⚠ 用 attention，ⓘ 用安静色。读取中用行内进度；读取失败用 Alert + [重试]，确认按钮禁用。
- 会挡住切换的 ⊗ 项：名单未就绪；有可用节点还没生效本组规则；可用节点为 0。
- 基础放行为空时用 ⚠，不挡：所有者可能有意不放行 DNS。
- 409 `dest_mode_invalid_transition`：snack「状态已被其他人修改，已重新载入」，然后刷新。

**交互**：
- [查看]：打开节点覆盖抽屉，并按本组筛选。[看报告]：关闭对话框，进入 S10。
- 样式与 S20 一致：只有「切换到执行」是 destructive；「切回试运行」「关闭白名单」都只放宽限制，是普通按钮。成功后：snack「「{{group}}」已切换到执行，约 {{minutes}} 分钟内生效」，结论条进入待下发，卡片徽章更新。

**文案**：
- 就绪清单的「试运行已 {{days}} 天（建议观察满 {{recommend}} 天）」，`recommend = min(7, dest.trial_retention_days)`（R14）。
- 切换到执行：标题「让「{{group}}」开始执行白名单？」；后果「切换后，本组 {{members}} 人访问名单外的目的地会被拒绝，约 {{minutes}} 分钟内生效。可以随时切回试运行。」；按钮「切换到执行」。
- 切回试运行：「让「{{group}}」回到试运行？」/「成员访问名单外的目的地不再被拒绝，只记录。约 {{minutes}} 分钟内生效。」/ [切回试运行]
- 关闭白名单：「关闭「{{group}}」的白名单？」/「本组成员恢复访问所有目的地（仍受全局策略约束），并重新获得 {{n}} 台此前不开放的节点。名单保留，以后可以再次启用。」/ [关闭白名单]

**组件与文件**：`allowlist/EnforceDialog.tsx`（自定义 Dialog，`maxWidth="sm"`；ConfirmHost 只能放文字，放不下清单）；另外两个框走 `confirm()`。

**375px**：fullScreen；[查看] 与 [看报告] 换到文字下一行；底部按钮 sticky。

---

#### S12　记录 tab 与「加入放行例外」（阶段 2c）

**用途**：按小时汇总的命中，回答「谁、在哪台节点、因为哪条规则、访问了什么、几次」。可以筛选、聚合，也可以就地打开账号。

```
┌ 拦截 ─────────┐┌ 白名单拒绝 ───┐┌ 仅观察 ───────┐┌ 涉及账号 ─────┐  ← 四块都是 KpiTile，前三块可点即筛选
│ 2,134          ││ 1,203         ││ 4,012         ││ 37            │
└────────────────┘└───────────────┘└───────────────┘└───────────────┘
[账号 ▾] [策略 / 分组 ▾] [节点 ▾] [过去 24 小时 ▾] [⌕ 搜索目的地…]  ☐ 包含白名单试运行
查看 ( 明细 │ 按主域名 │ 按账号 │ 按策略 )            共 1,204 条 · 按小时汇总  (?)
── 今天 ─────────────────────────────────────────────────────────────────────
时段          账号          规则                           目的地                     节点      次数
14:00–15:00   alice@corp    [⊘ 拦截] 禁止发信               smtp.gmail.com:587         东京 01     37  ⋯
13:00–14:00   bob@corp      [🔒 白名单拒绝] 研发组           api.figma.com:443          洛杉矶 02    5  ⋯
13:00–14:00   carol@corp    [◉ 仅观察] 加密货币交易所        www.binance.com:443        东京 01     12  ⋯
12:00–13:00   dave@corp     [⊘ 拦截] 已删除的策略            tracker.example.org:6969   香港 02      3  ⋯
── 昨天 ─────────────────────────────────────────────────────────────────────
                                                     [‹] 1 / 25 [›]  每页 50 ▾
命中记录保留 30 天 [修改]

┌ 放行 api.figma.com？ ─────────────────────────────────────────┐
│ 写成  (•) 整个 figma.com 及子域名   ( ) 只这个主机名           │
│ 加到  (•) 白名单「研发组」的补充列表（只对本组生效）            │
│       ( ) 全局放行例外                                          │
│           ⚠ 会越过所有拦截策略，并让 2 个白名单分组都能访问它    │
│       ( ) 只豁免 bob@corp（不受任何访问限制）                    │
│           原因 [________________]  到期 [ 24 小时 │ 7 天 │ 永久 ] │  ← 只在选「只豁免」时出现
│ 约 2 分钟内生效。                             [取消]  [放行]    │
└────────────────────────────────────────────────────────────────┘
```

**状态**：
- 首次加载：指标卡与表格的同形 Skeleton；再次读取：4px 槽位里的 LinearProgress。读取失败：Alert error + [重试]。
- 空状态按原因分开写：
  - 没有启用的策略：「还没有启用的策略」+ [去新建]；
  - 有策略但没有节点在记录：「没有节点在记录命中：节点版本过旧，或记录方式为「不记录」」+ [节点覆盖]；
  - 有筛选：「这段时间内没有符合条件的命中」+ [清除筛选]；
  - 时间早于保留期：「命中记录只保留 {{days}} 天」。
- 统一用 `losses` 显示完整性提示：有丢行或丢事件时用 amber 并分别注明单位，未知损失不显示为 0。按账号或策略筛选时写「相关节点有记录未统计；统计可能不完整」，不把面板级损失说成该账号或策略的准确丢失量。
- 来源策略已删除：「已删除的策略」（斜体，`md.onSurfaceVariant`）；账号已删除：「已删除的账号 #{{id}}」。
- 白名单试运行的「本应拒绝」默认不显示；勾选「包含白名单试运行」后出现，用 measuring 徽章，账号列写「（分组级）」。
- 时间范围最长 31 天：自定义时前端限制在 31 天内，并说明原因。

**参数文法**（`recordsParams.ts`；默认值不写进 URL）：
- `rec_action ∈ {block, deny, observe}`：`block` = 拦截（block 且 `p*`），`deny` = 白名单拒绝（block 且 `g*`），`observe` = 仅观察（observe 且 `p*`）。发给 API 时分别是 `action=block&source_kind=policy`、`action=block&source_kind=group`、`action=observe&source_kind=policy`。
- `rec_since ∈ {1h, 24h, <N>d}`（N 不超过 `dest.hit_retention_days`），或 `datetime-local` 文本（浏览器时间，这时才有 `rec_until`）。默认 `24h`。风控中心同名的 `rec_since` 是 datetime-local 文本（`riskParams.ts:194,212`），这里多了相对写法，解析函数要两种都认。S2、S8、S16、S19 的链接都用相对写法。
- `rec_group_by ∈ {none, site, user, policy}`，默认 `none`；`rec_trial=1` 表示包含试运行；`rec_page`、`rec_size` 同风控中心。
- 非法值回落到默认值，不改写 URL。

**交互**：
- 指标卡（`KpiTile pressed/onToggle`）：前三张切换 `rec_action`，带 `aria-pressed`，再点一次撤销。卡上的数字取响应的 `summary`：**不随 `rec_action` 变化**，只随时间、账号、节点筛选变化，所以点了「拦截」另外两张不会归零（照风控中心「指标卡数整个队列」的规矩）。「涉及账号」取 `summary.users`，不可点。阶段 5 之前没有「白名单拒绝」这张卡，也没有「包含白名单试运行」勾选框（不显示为 0）。
- 筛选：
  - 账号用 UserAutocomplete，**精确选择**，写 `rec_user=<id>`，不做模糊匹配；
  - 规则下拉分三组：策略 / 白名单分组 / 已删除。「已删除」组来自响应的 `sources`（时间范围内出现过、`name` 为 null 的来源）；
  - 时间：过去 1 小时 / 过去 24 小时 / 过去 {{days}} 天（`days = min(7, dest.hit_retention_days)`）/ 自定义（`datetime-local`，注明「浏览器时间」）；
  - 搜索目的地只存在组件状态里（R12）。
- 「查看」分段写 `rec_group_by`。聚合视图的列是：对象 | 次数（横条 + 数字）| 涉及账号或策略数 | 最近。
- 点账号：push `user=<id>`，在本页打开 RiskUserDrawer（host=`access`）；点规则徽章：设为筛选。
- 行上 ⋯：测试此目的地（S13，history state 预填）/ 加入放行例外… / 打开账号。
- 加入放行例外（调用 `POST /dest/exceptions`，「只豁免」调用豁免接口；P7）：
  - 来源是白名单分组的命中，默认选「该组的补充列表」（阶段 5 起才有这一项）；
  - 来源是拦截策略，默认选「全局放行例外」。第一次使用全局例外时，**服务端**在同一个事务里建「放行例外」自定义列表和一条排在「放行」第 1 位的策略；框内写明「第一次使用会新建列表「放行例外」和一条放行策略」，成功后按响应的 `created` 写 snack；
  - 选「全局」时显示 `FieldHint tone="amber"`：summary「会越过所有拦截策略」，detail「放行在拦截与白名单之前判断：所有人访问它都不再被拦截，{{n}} 个白名单分组的成员也都能访问它。」；
  - 选「只豁免」时显示 `FieldHint tone="amber"`：summary「这个账号将不受任何访问限制」，detail「豁免对所有策略和白名单都生效，不只是这一个目的地。」；并出现「原因」必填框（与 S5 共用校验）与到期分段，默认 24 小时。
- 时段：列表显示面板时区里这一小时桶的起止。面板时区偏移不是整点时**照实显示**（如 14:30–15:30），不要四舍五入到整点。悬停「首次 14:03 · 最后 14:58」，同样用面板时区。日期分组的小标题按面板时区，sticky。
- 页脚 [修改]：打开 S18，并聚焦「命中记录保留」。

**文案**：
- 指标：拦截 / 白名单拒绝 / 仅观察 / 涉及账号
- 筛选：账号 / 策略或分组 / 节点 / 过去 1 小时、过去 24 小时、过去 {{days}} 天、自定义… / 搜索目的地… / 包含白名单试运行
- 查看：明细 / 按主域名 / 按账号 / 按策略
- 表头：时段 / 账号 / 规则 / 目的地 / 节点 / 次数
- 页脚：命中记录保留 {{days}} 天
- 帮助：每行是一个账号在一小时内对同一目的地的命中次数。记录的是节点识别出的域名，看不到网址路径。sing-box 节点只执行、不记录。白名单试运行只记到分组和主域名，默认不显示。

**组件与文件**：`records/RecordsTab.tsx`、`HitRow.tsx`、`HitsMetricCards.tsx`（KpiTile）、`AddExceptionDialog.tsx`、`recordsParams.ts`。`recordsParams.test.ts` 要断言：构造出的 URL 里永远没有搜索词；上面的参数文法逐条往返；`rec_action=deny` 映射成 `action=block&source_kind=group`。

**375px**：指标卡两列；筛选行 `flexWrap`，账号与时间各占一行，其余收进「筛选」弹出层（带已选数量角标）；每条命中三行：账号 + 次数 / 动作徽章 + 目的地（等宽、单行省略）/ 时段 · 节点。聚合视图只留对象和次数。

---

#### S13　测试目的地抽屉（`sheet=test`）

**用途**：回答「这个账号访问这个目的地会怎样，停在哪一步」。逐步画出 ①–⑥，并标出被前面截住的后续命中。

```
┌ 测试目的地 ─────────────────────────────────────────────── ✕ ┐
│ 目的地 [smtp.gmail.com            ]  端口 [587 ]  [ TCP │ UDP ] │
│ 账号（可选）[bob@corp            ▾]  节点（可选）[东京 01    ▾]  │
│                                                       [测试]   │
├──────────────────────────────────────────────────────────────┤
│ [⊘ 拦截]  在第 ③ 步被「禁止发信」拦截（端口 TCP 587）          │
│ ①  放行        未命中                                          │
│ ②  豁免        不适用：bob@corp 没有被豁免                     │
│ ③  拦截        ● 禁止发信：命中，停止                           │
│ ④  白名单      不再判断（bob@corp 在「研发组」，执行中）         │
│ ⑤  仅观察      加密货币交易所：也会命中（被前面截住）            │
│ ⑥  其余放行    不再判断                                        │
│ 按已保存并已下发的策略判断。东京 01：已生效。                   │
│ 只按目的地判断，不做 DNS 解析。BT 规则无法用测试判断。 ⓘ        │
│ [加入放行例外…]                          [打开「禁止发信」]     │
└──────────────────────────────────────────────────────────────┘
```

**状态**：
- 初始：只显示输入区，下面一行「输入目的地，看它在哪一步被放行或拦截」。
- 校验：目的地必填（域名或 IP）。写成 URL 时自动去掉 scheme 和路径，并提示「只看主机名，看不到网址路径」。端口 1–65535。
- 测试中：结果区显示 Skeleton。失败：Alert + [重试]。
- 结论徽章（§7.2 封闭色表）：拦截 / 白名单拒绝 / 仅观察（放行并记录）/ 本应拒绝（试运行，不会被拦）/ 豁免（`VerifiedUserOutlined`）/ 没有规则命中（放行）。
- 每一步的结果，与 API 的 `result` 一一对应：`miss` 未命中 / `n/a` 不适用 / `hit` 命中，停止 / `shadowed` 也会命中（被前面截住，amber 小字）/ `skipped` 不再判断（终止步之后、没有命中的步骤）/ `untestable` 无法测试。
- 节点提示按该节点的状态取 S14 的说明行，**不写死「上一版」**：选了节点、而它不是 `applied` 时，写「{{node}}：{{S14 说明行}}，实际结果可能不同」（如「香港 03：离线，恢复上报后自动下发」「美西 A：3X-UI / S-UI 的路由由运营者自己管理」）。没选节点时，列出各节点状态（最多 5 台，其余折叠）。
- `unpublished` 为真（有已保存、尚未下发的改动）：结果上方加一行 amber「有改动尚未下发，结果按已下发的版本判断」+ [立即下发]（`POST /dest/publish`）。
- 以 IP 测试：注「以 IP 测试时，域名规则不参与」。

**交互**：
- 打开方式：页头按钮；记录行、条目抽屉、试运行报告的「测试此目的地」通过 history state 预填。**输入不进 URL**。
- Enter 提交；调用 `POST /dest/test`。
- [打开「{{name}}」]：关闭抽屉，切到策略 tab，滚动到该行并高亮 2 秒，再打开 S3。
- [加入放行例外…]：打开 S12 的同一个对话框。

**文案**：
- 标题：测试目的地
- 初始：输入目的地，看它在哪一步被放行或拦截
- 结论：
  - 在第 {{step}} 步被「{{name}}」拦截（{{entry}}）
  - {{upn}} 所在分组「{{group}}」的名单里没有它
  - 会被记为本应拒绝，不会被拦
  - {{upn}} 不受任何访问限制
  - 没有规则命中，直接放行
- 注：按已保存并已下发的策略判断。 / 有改动尚未下发，结果按已下发的版本判断 / 只按目的地判断，不做 DNS 解析。BT 规则无法用测试判断。 / 以 IP 测试时，域名规则不参与。 / 只看主机名，看不到网址路径

**组件与文件**：`sheets/TestSheet.tsx`、`EvalTrace.tsx`（与 PolicyRail 共用竖线和圆点原件）。

**375px**：100vw；端口与 TCP/UDP 一行，账号和节点各占一行，[测试] 占满一行；每一步两行显示。

---

#### S14　节点覆盖抽屉（`sheet=nodes`）与记录方式对话框

**用途**：整个机队的视图：每台节点执行到哪一步、出了什么问题、下一步怎么做，以及现在实际记录了哪些数据。

```
┌ 节点覆盖 · 12 台 ────────────────────────────────────────── ✕ ┐
│ [已生效 9] [下发中 1] [有问题 2] [需升级 1] [不执行 2] ← KpiTile │
│ ┌ 记录了什么 ─────────────────────────────────────────────────┐ │
│ │ 4 台只记录规则命中（保留 30 天）· 1 台还记录按网站用量（保留  │ │
│ │ 7 天，只能逐个账号查看）· 原始访问日志不离开节点               │ │
│ │ 隐私与协议页：未启用  [去设置 ›]                             │ │
│ └─────────────────────────────────────────────────────────────┘ │
│ 东京 01        Xray                                 [✓ 已生效] ⋯│
│ 只记录规则命中 · 24 小时 1,204 次 · 30 秒前上报                  │
│ 法兰克福 03    Xray                       [⊗ 入站没有识别域名] ⋯│
│ 入站「vless-443」只识别元数据，域名规则在它上面不生效。          │
│ 仍在执行上一版。                                  [修改入站 ›] │
│ 新加坡 04      Xray                                [⊗ 规则太多] ⋯│
│ 域名 57,312 / 50,000。仍在执行上一版。            [查看列表 ›] │
│ 首尔 02        Xray                         [⚠ 下发中 · 12 分钟] ⋯│
│ 节点可能离线                                                   │
│ 香港 02        sing-box                     [✓ 已生效 · 只执行]  ⋯│
│ sing-box 节点只执行，不记录                                     │
│ 大阪 05        Xray                                [⚠ 需升级]   ⋯│
│ 节点版本过旧，不能执行访问策略。                    [升级 ›]   │
│ 美西 A         3X-UI                                [⊖ 不适用]   │
│ 3X-UI / S-UI 的路由由运营者自己管理，不执行访问策略。            │
│ 最后一次修改后等 {{seconds}} 秒再下发，连续修改会合并 [修改]     │
└────────────────────────────────────────────────────────────────┘

┌ 东京 01 记录什么？ ─────────────────────────────────────────────┐
│ ( ) 不记录                                                       │
│     只执行策略，不留任何记录。                                    │
│ (•) 只记录规则命中                                                │
│     记下命中拦截、仅观察和白名单的连接：账号、目的地、次数，按小时 │
│     汇总，保留 30 天。                                            │
│ ( ) 记录命中和按网站用量                                          │
│     另外按账号、按小时记下访问过的主域名和次数，保留 7 天；只能逐个 │
│     账号查看，查看会记入审计日志。                                 │
│     ⚠ 隐私与协议页未启用，用户不会被告知 ⓘ   [去启用 ›]           │
│ 保存后这台节点会重启代理程序一次。          [取消]  [保存]        │
└──────────────────────────────────────────────────────────────────┘
```

**状态**（`/dest/status.state` → 徽章，每个值一句固定文案，绝不显示原始代码）。「色调」列是 `stateTone` 的值；「图标」列只在与 `stateTone` 默认不同时才写（§7.2 封闭色表）：

| state | 徽章 | 色调 | 图标 | 说明行 |
|---|---|---|---|---|
| `applied` | 已生效（sing-box 写「已生效 · 只执行」） | `ok` | — | 记录方式 · 24 小时命中 · 最后上报（2c 起前两段才出现） |
| `pending` ≤ 10 分钟 | 下发中 | `measuring` | — | 节点会在下次同步时重启代理程序 |
| `pending` > 10 分钟 | 下发中 · {{minutes}} 分钟（从 `pending_since` 算） | `attention` | — | 节点可能离线，或代理程序重启失败 · [节点问题 ›] |
| `rejected` | 节点拒绝了新策略 | `failing` | — | 仍在执行上一版 · [节点问题 ›] · [重新下发]；另加一句「回退期间新加入作用分组的成员，在这台节点上不受该分组的拦截策略约束」（§10 第 10 条） |
| `sniffing` | 入站没有识别域名 | `failing` | — | 列出入站显示名，「域名规则在它上面不生效。仍在执行上一版」· [修改入站 ›] |
| `over_limit` | 规则太多 | `failing` | — | `over_limit.kind` 映射的项与 `{{used}} / {{limit}}`，「仍在执行上一版」· [查看列表 ›] |
| `unsupported_version` | 需升级 | `attention` | — | 节点版本过旧，不能执行访问策略 · [升级 ›] |
| `unsupported_kind` | 不适用 | `not_applicable` | `RemoveCircleOutline`（覆盖 `stateTone` 的 `Block`） | 3X-UI / S-UI 的路由由运营者自己管理 |
| `offline` | 离线 | `inhibited` | `HelpOutline` | 恢复上报后自动下发 |
| `none` | 没有策略 | `none` | `RadioButtonUnchecked` | — |
| `paused` | 已暂停 | `idle` | `PauseCircleOutline`（覆盖 `stateTone` 的 `RemoveCircleOutline`） | 访问控制已暂停，恢复后照原样执行。暂停时每台有能力的节点都显示这个，**不显示**「没有策略」 |

回退三行的「仍在执行上一版」只在最近匹配的 applied 报告证明 fallback 候选已部署时显示；候选等待确认时写「正在尝试上一版」。`fallback_exhausted=true` 则保留原失败徽章，说明改为「上一版也无法执行，当前没有执行访问策略 · [节点问题 ›] · [重新下发]」，执行规则数显示 0；若 empty 尚未确认则写「正在停止执行访问策略」。不得拿历史 applied_rule_count 冒充当前执行数。新增这三种条件的中英文文案与页面测试。

- **筛选卡五张**（`KpiTile pressed/onToggle`，写 `node_state`）：
  - 已生效 = `applied`；
  - 下发中 = `pending` 且 ≤ 10 分钟；
  - 有问题 = `rejected`、`sniffing`、`over_limit`、`pending` > 10 分钟；
  - 需升级 = `unsupported_version`；
  - 不执行 = `unsupported_kind`、`offline`、`none`、`paused`。
- 加载：6 行 Skeleton。读取失败：Alert + [重试]。筛选后为空：「没有符合的节点」+ [清除筛选]。
- 记录方式对话框：
  - 节点缺 `audit.hits.v1`：除「不记录」外都置灰，写「节点版本过旧，需升级」；
  - 缺 `audit.usage.v1`：只置灰第三项；
  - sing-box：对话框只读，写「sing-box 节点只执行，不记录」；
  - 选第三项而隐私页未启用：显示 `FieldHint tone="amber"`：summary「隐私与协议页未启用，用户不会被告知」，detail「按网站用量会按账号、按小时记下访问过的主域名。建议先在「系统设置 › 隐私与协议」发布隐私政策并启用，让用户知道记录了什么。」；**只提醒，不阻止**；
   - 保存按钮旁的说明就是提醒：「保存后这台节点会重启代理程序一次」，改为第三项时再加一句「另外按账号记下访问过的主域名，保留 {{days}} 天」。**不再另弹确认框**（S20 已删掉这一行）；
   - 「不记录」补充「保存后停止接收新的记录；节点会在下次同步停止采集，已有历史按保留期清理」。试运行的 A 档说明单独写「白名单试运行只记分组与主域名，不记账号」；显式启用 B 档时说明试运行连接也按账号统计用量。统计是连接次数，不是流量或完整访问日志。
  - 阶段 1c 不显示记录方式（行内也不显示），阶段 2c 起才出现。

**交互**：
- 行上 ⋯：设置记录方式…（只对 psp）/ 打开服务器 / 查看节点问题。
  - **查看节点问题**：`NodeIssuesView` 目前不读任何 URL 参数（F57）。本功能 1c 的 PR 给它加 `?agent=<agent_id>` 筛选（挂载时读、筛选变化时 replace 写回），并加测试；在那之前只跳 `/admin/node-issues`。
  - **打开服务器**：WP-D4 落地前跳 `/admin/servers?q=<name>`，落地后跳 `/admin/servers/:id`。`ServersView` 的搜索框现在是 `useState('')`（`ServersView.tsx:193`），深链进来会显示「已过滤的列表 + 空搜索框」，失焦还会把筛选清掉（`:1480`）。同一个 PR 把它改成 `useState(ps.keyword)`，并加测试。
- [重新下发]：调用 `POST /dest/agents/:id/retry`。
- [修改入站 ›]：`sniffing_insufficient` 带 `node_id`（P7）。`NodesView` 加深链 `?inbound=<nodeId>`：挂载时找到该节点并调用 `openEditInbound(n)`（`NodesView.tsx:2991`，嗅探字段在这个编辑框里），随后用 replace 去掉参数（照 S17 GroupsView 的写法），并加测试。1c 的 PR 一起做。
- [升级 ›]：跳到服务器页并打开该行的 Agent 升级对话框（用 WP-D4 决定 D-6 的 `/admin/servers?open=<action>&server=<id>`，动作名以 `ServerActionsMenu` 的 `ServerAction` 为准；D4 未落地前只跳到 `/admin/servers?q=<name>`）。
- 记录方式保存：PUT /api/admin/servers/:id，只带 audit_collect，revision 由服务端递增；snack「已保存，{{node}} 会在约 {{minutes}} 分钟内重启代理程序一次」。
- [去设置 ›] / [去启用 ›]：跳到 `/admin/settings?tab=legal`。底部 [修改]：打开 S18，并聚焦下发间隔。

**文案**：标题「节点覆盖 · {{n}} 台」；筛选卡「已生效 / 下发中 / 有问题 / 需升级 / 不执行」；状态与说明行见上表；记录方式三项的说明见线框，保留天数一律从设置插值；底部「最后一次修改后等 {{seconds}} 秒再下发，连续修改会合并」。

**组件与文件**：`sheets/CoverageSheet.tsx`、`components/NodePolicyStatusRow.tsx`（`variant: 'row' | 'block'`，S15 共用）、`components/AuditCollectDialog.tsx`（S15 共用）、`DataDisclosure.tsx`（中性提示条）。状态映射放在 `utils/accessControl.ts`，测试逐个状态断言色调与 zh/en 文案都存在。另外，`utils/nodeIssueGroups.ts:5` 的 `categories` 加入新 issue code（`destination_policy_over_limit`、`destination_policy_rejected`、`destination_policy_lkg_rejected`、`destination_policy_sniffing_insufficient`），归入 `sync`；两份语言包补 `code_titles`/`code_summaries`。

**375px**：100vw；筛选卡两列；每台节点三行：名称 + 徽章 + ⋯ / 引擎与说明 / 下一步链接；记录方式对话框 fullScreen，三个选项都是整块可点的卡片。

---

#### S15　服务器页入口（状态行、单节点对话框、WP-D4 区块）

**用途**：管理服务器的人在服务器页上就能看到每台原生节点的执行情况，并点进去处理。WP-D4 落地前，入口就是这一行加一个对话框。

```
服务器列表（ServersView）的状态列：
名称       地址                   状态                          版本     操作
东京 01    https://jp1.example…   [在线]                        4.1.2    测试 ⋮
                                  ✓ 访问控制：已生效
法兰克福   https://fra.example…   [在线]                        4.1.2    测试 ⋮
                                  ⊗ 访问控制：入站没有识别域名
美西 A     https://usw.example…   [在线]                        3.4.2    测试 ⋮
                                  （3X-UI 不显示这一行）

单节点对话框（ServerAccessDialog）与 WP-D4 概览 tab 的最后一个区块（同一组件 block 变体）：
┌ 东京 01 · 访问控制 ───────────────────────────────────── [✓ 已生效] ┐
│ 执行 9 条规则，与已保存的一致 · 3 分钟前生效                          │
│ ┌ 记录方式 ─────────┐┌ 24 小时命中 ──────┐┌ 白名单分组 ─────────┐   │
│ │ 只记录规则命中     ││ 1,204             ││ 2 个在本节点生效    │   │
│ │ 保留 30 天 [修改]  ││ [查看记录 ›]      ││ 研发组、访客        │   │
│ └───────────────────┘└───────────────────┘└─────────────────────┘   │
│ ⚠ 入站「vless-443」没有识别域名，域名规则在它上面不生效               │
│   影响：从这个入站进来的连接不受域名策略约束          [修改入站 ›]  │
│                                     [打开访问控制 ›]       [关闭]   │
└────────────────────────────────────────────────────────────────────┘
```

**状态**：
- 状态行只对 `kind = psp` 且机队已有期望策略时显示；整个机队都没有策略时整行不出现，不给每一行加噪音。
- 状态行带 12px 图标，颜色与 S14 的色调一致（不只靠颜色）。
- `/dest/status` 读取失败或返回 503（未接线）：状态行不显示，不影响服务器列表本身。**没有「404 = 未上线」这一支**：SPA 与 API 由同一个二进制发布，1c 之后接口一定存在；而未注册的 `/api/...` 路径本来就会落到 `NoRoute` → SPA，返回 200 加 index.html（F55），靠 404 判断是错的。
- 对话框和区块的状态与 S14 相同；问题行照 `diagnostics/FindingList.tsx` 的写法：标题 → 影响 → 动作。
- 对话框与区块里的数字都直接取 `/dest/status` 的字段：「执行 {{n}} 条规则」= `applied_rules`，「{{ago}}生效」= `applied_at`，白名单分组 = `allowlist_groups`，记录方式 = `collect_effective`，24 小时命中 = `hits_24h`。
- 分阶段（§7.1 的逐元素阶段表）：1c 只有状态行与问题行；「记录方式」「24 小时命中」两块从 2c 起出现；「白名单分组」一块从阶段 5 起出现。

**交互**：
- 状态行是 ButtonBase，点击打开 `ServerAccessDialog`；psp 行的 ⋮ 菜单（`ServerActionsMenu`，WP-D6a 之后）也加一项「访问控制」。
- [修改] 打开 AuditCollectDialog；[查看记录 ›] 跳到 `/admin/access-control?tab=records&rec_panel=<id>`；[打开访问控制 ›] 跳到 `/admin/access-control?sheet=nodes`。
- WP-D4 落地后，`views/admin/serverDetail/OverviewTab.tsx` 的最后一个区块渲染 `<NodePolicyStatusRow variant="block">`。两边谁后合并，谁负责挂上（[server-dashboard-rework-plan.md](server-dashboard-rework-plan.md) WP-D4 §4.3 已写明）。

**文案**：状态行「访问控制：{{state}}」；对话框标题「{{name}} · 访问控制」；「执行 {{n}} 条规则，与已保存的一致 · {{ago}}生效」/「仍在执行上一版」；其余同 S14。

**组件与文件**：`views/admin/ServerAccessDialog.tsx`（Dialog `maxWidth="sm"`，xs fullScreen）；`ServersView.tsx` 的状态列照 `ipCapBadge` 的写法加 `accessLine(server)`；测试加两例：psp 行有这一行、3X-UI 行没有；点击打开对话框。

**375px**：沿用 ServersView 现有的手机布局，状态行放在状态徽章下面；区块的 KpiGrid 改为两列。

---

#### S16　账号抽屉「访问」tab（RiskUserDrawer 第五个 tab）

**用途**：在一个账号的完整视图里回答：他在哪个访问模式下、有没有被豁免、最近被拦了什么、在开了用量记录的节点上主要去哪些网站；并可以就地豁免或取消豁免。

```
┌ alice@corp ……（DrawerHeader 原样）                               ✕ ┐
│  概览 │ 连接 │ 设备 │ 时间线 │ 访问                                   │
├─────────────────────────────────────────────────────────────────────┤
│ 分组  学生 · [◷ 白名单 · 试运行]                                     │
│ 豁免  未豁免                                         [豁免此账号…]   │
│ 过去 7 天命中（天数按保留期插值）              [在记录中查看全部 ›] │
│ [⊘ 拦截] 禁止发信                                             214   │
│   smtp.gmail.com:587 · mail.qq.com:465 · 东京 01                     │
│ [◉ 仅观察] 加密货币交易所                                       37   │
│   www.binance.com:443 · www.okx.com:443                              │
│ 按网站用量   [ 24 小时 │ 7 天 ]                                      │
│   [显示按网站用量]   查看会记入审计日志                               │
│   google.com        ▓▓▓▓▓▓▓▓▓▓▓▓  1,920                              │
│   youtube.com       ▓▓▓▓▓▓        1,104                              │
│   (ip)              ▓▓              302                              │
│   连接次数，不是流量 · 保留 7 天 · 只统计开启了用量记录的节点：东京 01 │
└─────────────────────────────────────────────────────────────────────┘
```

**状态**：
- 只在切到这个 tab 时请求 `GET /dest/users/:id`。加载：两行状态 Skeleton 加三块圆角 Skeleton。读取失败：tab 内 Alert + [重试]，只影响这个 tab。
- 豁免中：「豁免：{{reason}} · 由 {{by}} 于 {{ago}} · {{expiry}}」+ [取消豁免]，状态行底色用 `md.surfaceContainerHigh`，加 InfoOutlined 提示「访问策略和白名单都不对这个账号生效」。
- 不在白名单分组：模式徽章「不限」（「没有」那一行的安静色）。
- **1c 只有「分组」「豁免」两行**（§7.1 的逐元素阶段表）；命中区块从 2c 起出现，用量区块从阶段 4 起出现。
- 命中区块的窗口 `days = min(7, dest.hit_retention_days)`（由 `recent_hits.days` 返回，R14）。没有命中：「过去 {{days}} 天没有命中」；`hits_available` 为假（所在节点都不记录）：「这个账号所在的节点都不记录命中」+ [节点覆盖]。
- 按网站用量（阶段 4 起）：
  - 所在节点都没开：只显示一行「这个账号所在的节点没有开启按网站用量」，不调用接口；
  - 已开启：先只显示按钮 [显示按网站用量]，点击后才读取，并由服务端写审计（R6）；
  - 范围选项从 `[24 小时, min(7, dest.usage_retention_days) 天]` 生成，两者相同时只留一个；
  - 表头带 HelpTip：「客户端以 IP 连接时只记为 (ip)」（§10 第 5 条）；
  - 结果为空：「这段时间没有记录」。
- 白名单试运行的「本应拒绝」不出现在这里（试运行只存分组级，R5）。

**交互**：
- `TABS` 改为 `['overview','connections','devices','timeline','access']`，`DrawerTab` 类型同步；只对 `useCan('access.view')` 显示。
- `host` 联合类型改为 `'risk' | 'users' | 'access'`；新增 `initialTab` 属性，`seenKey` 重置时回到 `initialTab`（`RiskUserDrawer.tsx:131-139`）。host=`access` 时默认停在「访问」，其余宿主仍从「概览」开始。
- `ActionBar` 里「在用户管理中处理」的条件 `host === 'risk'`（`:66`）改为 `host !== 'users'`，否则从访问控制打开的抽屉会丢掉这个出口。
- [豁免此账号…]：打开 AddExemptionDialog，账号锁定为当前账号。[取消豁免]：S20 确认。
- [在记录中查看全部 ›]：在访问控制页内，关闭抽屉并设置 `rec_user`；在其他页面，跳到 `/admin/access-control?tab=records&rec_user=<id>&rec_since={{days}}d`。
- 点白名单徽章：跳到 `/admin/access-control?tab=allowlist&al_group=<gid>`。

**文案**：tab「访问」；状态行「分组 / 豁免 / 不限 / 白名单 · 试运行 / 白名单 · 执行中 / 未豁免 / 豁免此账号… / 取消豁免」；「过去 {{days}} 天命中 / 在记录中查看全部 / 过去 {{days}} 天没有命中」；「按网站用量 / 显示按网站用量 / 查看会记入审计日志 / 连接次数，不是流量 · 保留 {{days}} 天 · 只统计开启了用量记录的节点：{{nodes}} / 客户端以 IP 连接时只记为 (ip)」。

**组件与文件**：新建 `views/admin/risk/drawer/AccessTab.tsx`；改 `RiskUserDrawer.tsx`；复用 BarCount、ToneBadge、AddExemptionDialog。测试扩 `RiskUserDrawer.test.tsx`：第五个 tab 存在；切到「访问」才发请求；没有用量节点时不调用用量接口；三种 host 都能看到；运维员看不到。

**375px**：100vw；五个 tab 在手机上用 `variant="scrollable"`（否则英文的 Connections 会被挤压），桌面保留 `fullWidth`（560 宽时每个约 108px）；用量横条最长占 60%，数字在右侧。

---

#### S17　分组：对话框「目的地」tab 与列表徽章（阶段 5）

**用途**：在定义分组的地方显示这个分组的访问模式摘要，并给出跳转入口。白名单**只能在访问控制页编辑**，与风控「分组例外」只在策略页编辑的先例一致，避免两份草稿互相覆盖。

```
┌ 编辑分组「访客」 ─────────────────────────────────────────── ✕ ┐
│  节点范围   策略   成员   目的地                                  │
├─────────────────────────────────────────────────────────────────┤
│ 访问模式  [◷ 白名单 · 试运行 · 第 3 天]                          │
│ 名单      访客白名单（40）、补充（0）· 基础放行 1 条              │
│ 可用节点  8 / 12   4 台节点不对本组开放 ⓘ                        │
│ 这里只显示摘要；名单和阶段在访问控制页修改。                      │
│                                       [在访问控制中编辑 ›]       │
└─────────────────────────────────────────────────────────────────┘
不限模式：访问模式 [不限] / 本组成员可以访问所有目的地，仍受全局访问策略约束。 [为本组启用白名单… ›]

分组列表行：访客  [◷ 白名单]      ← 所有人可见，运维员也能看到，不含名单内容
```

**状态**：
- 「目的地」tab 只在 `useCan('access.view')` 时渲染。分组对话框本身由 `useCan('config.write')` 控制（`GroupsView.tsx:199,545,645`），实际只有 admin 能打开；这里的判断是双保险。
- 读取中：三行 Skeleton。读取失败：一行 `md.error` 文字 + [重试]，不影响对话框的其他 tab。
- 新建分组：tab 内只写「保存分组后即可设置访问模式」。
- 对话框的「保存」**不触碰** `dest_group_modes`。
- 列表徽章：分组列表接口（staffGroup）的 DTO 加只读字段 `dest_mode: 'open' | 'allowlist_trial' | 'allowlist_enforce'`（R10），由 handler 通过窄接口读 `dest_group_modes`。接口与运维员可读的路由测试见 P7 的 `GET /api/admin/groups` 一行，属于阶段 5（§11.1）。

**交互**：
- [在访问控制中编辑 ›]：关闭对话框（有未保存的修改时先确认），跳到 `/admin/access-control?tab=allowlist&al_group=<id>`。
- [为本组启用白名单… ›]：跳到 `/admin/access-control?tab=allowlist&al_enable=<id>`。
- 支持深链 `/admin/groups?edit=<id>&dtab=destination`：GroupsView 挂载时打开该分组并切到这个 tab，随后用 replace 去掉这两个参数。

**文案**：tab「目的地」；「访问模式 / 名单 / 可用节点」；「不限 / 白名单 · 试运行 · 第 {{days}} 天 / 白名单 · 执行中」；「本组成员可以访问所有目的地，仍受全局访问策略约束。/ 这里只显示摘要；名单和阶段在访问控制页修改。/ 保存分组后即可设置访问模式」；列表徽章「白名单」。

**组件与文件**：`views/admin/groups/GroupDestinationTab.tsx`；`GroupsView.tsx:708-715` 的 Tabs 加 `<Tab value="destination">`（值不能用 `access`，已被「节点范围」占用）。测试 `GroupsView.test.tsx` 加四例：admin 看到摘要与跳转；新建分组的提示；运维员在列表上看到徽章；深链打开对应 tab。

**375px**：跟随分组对话框的布局（顺带把对话框改为 xs 下 fullScreen，现为 `width: 600, maxWidth: '90vw'`，`GroupsView.tsx:681`）；Tabs 用 scrollable；标签与值上下排列。

---

#### S18　数据与下发设置对话框

**用途**：`dest.*` 设置只在这里写入（P5），系统设置页不出现。各页面凡提到这些数值的地方，都带一个 [修改]，打开本对话框并聚焦对应字段。

```
┌ 数据与下发设置 ───────────────────────────────────────────── ✕ ┐
│ 记录保留                                                        │
│ 命中记录保留（天）        [______]  默认 30 · 1–365              │
│ 试运行记录保留（天）      [______]  默认 7 · 1–30，不超过命中记录 │
│ 按网站用量保留（天）      [______]  默认 7 · 1–30                │
│ 列表刷新                                                        │
│ 远程与分类列表刷新间隔（小时） [______]  默认 24 · 6–168          │
│ 下发                                                            │
│ 策略下发等待（秒）        [______]  默认 60 · 30–3600            │
│ 最后一次修改后等这么久再下发；连续修改合并为一次。每次下发，      │
│ 节点会重启代理程序一次。                                          │
├────────────────────────────────────────────────────────────────┤
│ 已修改 2 项                                  [放弃]  [保存]     │
└────────────────────────────────────────────────────────────────┘
```

**状态**：
- 字段照 `PolicyField` 的写法：留空表示用默认值，占位写「默认 N」，**不显示 0**。越界时字段下写红色范围；试运行保留大于命中保留时两个字段都报错。
- 读取中：字段 Skeleton。读取失败：Alert + [重试]。503：info「设置未接线」。
- 保存时缩短了任何一个保留期：先弹 S20 的「缩短保留期？」。
- 服务端 400 按 key 落到字段。

**交互**：只提交改过的键（`PUT /dest/settings`）；成功后 snack「已保存」，并失效 settings、status。有未保存的修改时关闭，先确认。

**组件与文件**：`accessControl/AccessSettingsDialog.tsx`（Dialog `maxWidth="sm"`）；复用 `components/PolicyField`（UI-0 移动后）、`hooks/useDirtyClose`。

**375px**：fullScreen；字段单列；默认值与范围写进字段下方的 helperText；底部保存栏 sticky。

---

#### S19　风控中心集成（阶段 2c）

**用途**：`dest_block` 风险信号只出现在风控中心已有的三个位置：策略页的卡片、抽屉概览的检测器行、待处理队列的原因句。

```
风控中心 › 策略（第七张卡，排在「异地登录国家」login_country 与「数据」data 之间）：
┌ 访问拦截 ──────────────────────────────────────────────── 已启用 [●] ┐
│ 24 小时内，账号命中勾选了「计入风险信号」的拦截策略的次数。白名单拒绝 │
│ 和仅观察永不计入。                                  [打开访问控制 ›]  │
│ 达到 [______] 次标记账号（默认 20 · 1–10000）；达到一半为疑似。        │
│ 现在计入风险的策略：禁止 BT、禁止发信                                  │
└──────────────────────────────────────────────────────────────────────┘
用户抽屉 › 概览（第六行检测器）：
  访问拦截        [已标记]     37 / 20 次 · 24 小时         2 小时前
  按策略：禁止发信 30 · 禁止 BT 7                         [查看这些命中 ›]
待处理 › 原因句：  24 小时内命中拦截策略 37 次（阈值 20）
```

**状态**：
- 检测器行用 OverviewTab 现有的 DetectorRow；状态词与 P6 的六态表一致。**检测器行全部经 `DetectorStateChip`**：抽屉概览把每个 `RISK_KINDS` 都画成 `DetectorStateChip`（`OverviewTab.tsx:157-159,207`），`dest_block` 加进去之后自然也走它；若只让新行用 ToneBadge，同一个抽屉里就有两种徽章。所以 2c 的 PR 把 `DetectorStateChip` 的**内部实现**改成渲染 ToneBadge（`evidence/state.ts` 的映射不变，六行检测器一起换），顺带修掉它的 F44 问题。`evidence/evidence.test.tsx` 里断言 `MuiChip-colorSuccess` 的那几例改为断言 `data-state`；「不完整的 clean 不画绿」这条约束保留，只是改用 tone 表达。
- 没有任何策略勾选「计入风险信号」：卡内 `FieldHint tone="amber"`：summary「没有策略计入风险，这个检测项不会触发」，detail「在「访问控制 › 策略」里给拦截策略勾选「计入风险信号」后，命中次数才会计入这个检测项。白名单拒绝和仅观察永不计入。」。
- 证据面板：只列按策略的计数和节点数；策略已删除时写「已删除的策略」。
- 信任对话框的说明加一句：「信任只豁免地点类检测，不影响「访问拦截」」。

**交互**：
- 卡片开关映射为 `!risk_dest_block_off`；阈值字段带 tail。随策略页的草稿与粘底保存栏一起保存；分组例外卡里会出现这两个键。
- [打开访问控制 ›]：有草稿时由 `useLeaveGuard` 拦下。
- [查看这些命中 ›]：跳到 `/admin/access-control?tab=records&rec_user=<id>&rec_since=24h&rec_action=block`。

**文案**：卡片「访问拦截」及说明见线框；检测器「已关闭 / 没有节点在记录命中，无法判断 / 24 小时内没有命中 / 24 小时内命中 {{count}} 次 / 接近阈值 / 24 小时内命中 {{count}} 次（阈值 {{threshold}}）」；原因句「24 小时内命中拦截策略 {{total}} 次（阈值 {{threshold}}）」。

**组件与文件**：见 P5、P6 的清单（`policyLayout.ts` 的 `POLICY_CARDS` 与 `PolicyCardId` 加 `dest_block`）。

**375px**：沿用风控中心现有的响应式布局。

---

#### S20　确认框目录（`accessControl/confirmCopy.ts`）

**用途**：把「哪些动作要确认、确认框说什么」集中成纯函数，测试逐条断言。常态的提醒放在保存按钮旁边的说明里，不靠确认框。

| 动作 | 标题 | 正文（关键句） | 按钮 | 样式 |
|---|---|---|---|---|
| 机队第一次下发 | 启用第一条访问策略？ | {{n}} 台节点会在约 {{minutes}} 分钟内各重启代理程序一次，正在使用的连接断开一次，客户端会自动重连。之后的修改也会这样下发；{{seconds}} 秒内的连续修改合并为一次。 | 启用 | 普通 |
| 删除策略 | 删除策略「{{name}}」？ | 删除后约 {{minutes}} 分钟内不再执行。历史命中保留，显示为「已删除的策略」。 | 删除 | destructive |
| 删除列表（未被使用） | 删除列表「{{name}}」？ | 这个列表没有被使用，删除不会影响节点。 | 删除 | destructive |
| 列表正在被使用（409） | 列表正在被使用 | 先从这些地方移除它：{{used_by 链接}} | 知道了 | 说明框 |
| 转为拦截 | 见 S4 | 见 S4 | 改为拦截 | 普通 |
| 切换到执行 | 见 S11 | 见 S11 | 切换到执行 | destructive |
| 切回试运行 / 关闭白名单 | 见 S11 | 见 S11 | 切回试运行 / 关闭白名单 | 普通（只放宽） |
| 白名单启用后可用节点为 0（S9 第 3 步） | 启用后「{{group}}」成员将没有任何可用节点？ | 本组的 {{members}} 人在试运行开始后就拿不到任何节点：{{n}} 台节点都不执行白名单（3X-UI、S-UI 或版本过旧）。通常应先升级原生节点。 | 仍然开始 | destructive |
| 切换列表类型（S7） | 切换类型会清空已填内容？ | 自定义的文本、远程地址、分类选择互不通用，切换后当前输入不保留。 | 切换 | 普通 |
| 缩短保留期 | 缩短保留期？ | 超过 {{days}} 天的记录会在一小时内删除，无法恢复。 | 缩短 | destructive |
| 取消豁免 | 取消 {{upn}} 的豁免？ | 之后访问策略和白名单会对这个账号生效，约 {{minutes}} 分钟内生效。 | 取消豁免 | 普通 |
| 暂停全部执行 | 暂停全部访问控制？ | 所有节点会在约 {{minutes}} 分钟内停止执行策略和白名单，并各重启代理程序一次；定义全部保留，恢复后照原样执行。白名单分组的成员不会因此获得更多节点。 | 暂停 | destructive |
| 恢复执行 | 恢复访问控制？ | 所有节点会在约 {{minutes}} 分钟内重新执行策略和白名单，并各重启代理程序一次。 | 恢复 | 普通 |
| 离开有未保存修改的对话框 | 放弃未保存的修改？ | 关闭后，这次的修改不会保存。 | 放弃 | 普通 |

- 普通确认用 `confirm()`；带清单、链接或复选框的用自定义 Dialog（转为拦截、切换到执行、列表正在被使用）。
- 标题一律是点名对象的问句；按钮用动词，不用「确定」。
- 不在清单里的写入（改名称、改「计入风险」、新建停用的策略、启用或停用已有策略、修改列表内容、增加豁免、调整顺序、**修改节点记录方式**）**不弹确认**，由保存按钮旁的说明或结论条的「改动已保存，N 秒后下发」说明后果。记录方式对话框自己就带重启说明和 [保存]（S14），再加一个确认框等于确认两次。
- **「机队第一次下发」的触发写死为**：这次写入会让已发布快照从「没有启用的策略、也没有白名单分组」变为「有」。S2 的开关、S3 的保存、S4 的模板、S9 的开始试运行都经同一个纯函数 `needsFirstPublishConfirm(policies, groups, change)` 判断，`confirmCopy.test.ts` 对四个入口各断言一例。
- `{{minutes}} = ceil(apply_eta_ms / 60000)`（`/dest/status` 返回，P7）；读取失败时写「几分钟内」。前端拿不到同步周期，不要自己算。节点数取自 `/dest/status`，读取失败时写「各节点」，不写 0。

---

#### S21　隐私与协议：管理端（系统设置 › 隐私与协议，阶段 3）

**用途**：发布服务条款和隐私政策。「我们收集什么」由当前实际设置自动生成；只有勾选「重大变更」才会要求用户重新同意。

```
基本设置 | 登录与安全 | 站点品牌 | 订阅管理 | 用户门户 | 邮件提醒 | SSO 认证 | 隐私与协议
┌ 隐私与协议页 ──────────────────────────────────────────── 已启用 [●] ┐
│ 启用后，登录页和注册页显示链接，普通用户在重大变更后会看到同意对话框。│
│ 订阅不会因此中断。当前同意版本 v3                                      │
└──────────────────────────────────────────────────────────────────────┘
┌ 服务条款 ───────────────────────────────────── [版本历史] [编辑] ┐
│ 已发布  [zh-CN v4 · 9月30日] [en-US v2 · 9月12日]                  │
├ 隐私政策 ───────────────────────────────────── [版本历史] [编辑] ┤
│ 已发布  [zh-CN v3 · 9月30日]   en-US 尚未发布（回落到 zh-CN）       │
└──────────────────────────────────────────────────────────────────┘
┌ 我们收集什么（自动生成，用户看到的就是这些）────────────────────┐
│ 订阅访问日志（含 IP）              永久保留                        │
│ 登录记录                           90 天                           │
│ 连接历史（含 IP）                  30 天                           │
│ 设备标识（摘要）                   已开启                          │
│ 风控标记记录（不含 IP）            90 天                           │
│ 风险判定（不含 IP）                每小时覆盖                      │
│ 管理员复核记录（不含 IP）          删号后一小时内清除              │
│ 访问规则命中                       4 台节点 · 30 天                │
│ 白名单试运行（只记分组和主域名）    7 天                            │
│ 按网站用量                         1 台节点 · 7 天                 │
└──────────────────────────────────────────────────────────────────┘

编辑（全屏 Dialog）：
┌ ✕  编辑隐私政策   语言 [zh-CN ▾]   当前已发布 v3（9月30日）    [放弃] [发布…] ┐
│ [插入「我们收集什么」]                               12,340 / 60,000 字节    │
├─────────────────────────────────────┬───────────────────────────────────────┤
│ # 隐私政策                           │ 隐私政策                               │
│ 本服务……                             │ 本服务……                               │
│ [[data-collection]]                  │ ┌ 我们收集什么 ────────────────────┐   │
│ ## 你的权利                          │ │ 订阅访问日志（含 IP）· 永久保留   │   │
│ （CodeEditor，Markdown，等宽）        │ └──────────────────────────────────┘   │
└─────────────────────────────────────┴───────────────────────────────────────┘
发布：「发布隐私政策（zh-CN）第 4 版？」  比上一版多 120 字、少 30 字
      ☐ 重大变更：要求用户重新同意
        勾选后，约 1,240 名普通用户下次登录时会看到同意对话框；订阅不会中断。只改错别字时不要勾选。
      [取消] [发布]
```

**状态**：
- 已启用但没有任何已发布文档：Alert warning「已启用，但还没有发布任何文档：公开页会显示「暂无内容」」+ [去编辑]（前提缺失）。
- 编辑器：
  - 超过 60 000 字节：计数变红，[发布…] 禁用；
  - 正文里没有 `[[data-collection]]`：预览上方 `FieldHint tone="amber"`：summary「正文里没有「我们收集什么」，建议插入」，detail「「我们收集什么」由当前实际设置自动生成，设置变了它会跟着变。不插入的话，用户看不到本服务实际记录了哪些数据、保留多久。」；
  - 预览渲染失败只影响右栏。
- 发布 409（版本冲突）：「其他管理员刚发布了新版本，已重新载入」，保留草稿。
- 版本历史抽屉：读取中 Skeleton，空状态「还没有发布过」。

**交互**：
- 启用开关跟随设置页现有的保存栏提交。`legal.consent_version` **只由发布接口写入**，系统设置 PUT 保留它（§8.3）。
- 文档卡与编辑器是独立写入，不进设置页的草稿。切换语言时有未保存内容，先确认。
- 编辑器的语言选项：zh-CN、en-US，加上已上传的语言包（`SUPPORTED_LANGUAGES` 在 `i18n.init` 之前已追加上传的语言，CLAUDE.md「Runtime language packs」）。
- 编辑器用 `CodeEditor language="markdown"`（UI-0；首版没加 markdown 依赖时用 `plain`），关闭经 `useDirtyClose`。
- 「插入『我们收集什么』」在光标处插入单独一行 `[[data-collection]]`；正文里已有时按钮禁用。
- 预览与公开页用同一个渲染组件，输入停 300ms 后刷新。
- 发布对话框：勾选「重大变更」时，正文实时显示受影响的普通用户数。

**文案**：tab「隐私与协议」；其余见线框。保留期一律从设置插值，订阅日志保留为 0 时写「永久保留」。

**组件与文件**：`views/admin/legal/LegalTab.tsx`、`LegalEditorDialog.tsx`、`LegalPublishDialog.tsx`、`LegalHistorySheet.tsx`；`components/LegalDocument.tsx`（复用 `components/ReleaseNotes.tsx` 的安全 Markdown 配置：`skipHtml`、`urlTransform` 只允许 http/https、图片降级为 alt 文本；在 `[[data-collection]]` 处切开正文，中间插入清单卡）；`components/DataCollectionCard.tsx`。

**375px**：编辑器改为「编辑 / 预览」两个 tab，不并排；顶栏只留 ✕、标题、[发布…]，语言选择和插入按钮放到第二行；文档卡单列。

---

#### S22　隐私与协议：公开页 `/legal/terms`、`/legal/privacy`

```
                 [Logo] 站点名称                                 [中文 ▾]
          ┌──────────────── 阅读栏 max-width 720 ─────────────────┐
          │ 隐私政策                                               │  H1 28/600
          │ 第 3 版 · 2026年9月30日发布                             │  caption
          │ 本服务……（正文 15/1.75）                                │
          │ ┌ 我们收集什么 ────────────────────────────────────┐   │  md.surfaceContainer
          │ │ 订阅访问日志（含 IP）              永久保留        │   │
          │ │ 访问规则命中（4 台节点）            保留 30 天      │   │
          │ └─────────────────────────────────────────────────┘   │
          │ ────────────────────────────────────────────────────  │
          │ ← 返回 · 服务条款                                      │
          └────────────────────────────────────────────────────────┘
```

**状态**：
- 未启用或没有任何版本（公开接口返回 404）：居中卡片「此页面未启用」+ [返回]。
- 发生语言回落：标题下用安静色写「此语言暂无译本，显示的是 {{lang}} 版」。
- 加载：标题与 6 行文字的 Skeleton。读取失败：「暂时无法载入，请稍后再试」+ [重试]。
- 没有任何节点在记录：清单里不出现访问控制那几行。深色模式只用 md token。

**交互**：
- 语言切换复用 `LanguageMenu`，写 `?lang=`。
- 返回：有历史记录就 `navigate(-1)`，否则回登录页。两份文档在页脚互相链接；正文链接只允许 http/https，在新窗口打开。

**组件与文件**：`views/LegalView.tsx`（路由在 `RequireAuth` 之外）、`LegalDocument`、`DataCollectionCard`、`BrandLogo`、`LanguageMenu`。文案用 `auth:legal.*`。

**375px**：左右边距 16；H1 24/600；清单每项两行（项目 / 保留期）；页脚链接换行。

---

#### S23　隐私与协议：用户侧

```
同意对话框（UserLayout，role=user 且 legal_pending=true）：
┌ 服务条款和隐私政策已更新 ───────────────────────────────┐
│ 我们更新了服务条款和隐私政策，其中写明本服务记录哪些     │
│ 数据、保留多久。你的订阅不受影响。                       │
│ 《服务条款》↗   《隐私政策》↗                            │
│                                     [稍后]   [同意]      │
└─────────────────────────────────────────────────────────┘
注册页：☐ 我已阅读并同意《服务条款》和《隐私政策》   [注册]（未勾选时禁用）
页脚（登录页 LoginView.tsx:615、注册页、用户中心 UserLayout.tsx:105 的 footerText 那一行）：
  {{footerText}} · 服务条款 · 隐私政策        （footerText 为空时只有后两项）
（§1.2 Q2，默认不实现）用户中心首页中性提示条：你所在的分组只能访问指定的网站。打不开某个网站时，请联系管理员。
```

**状态**：
- 只对 role=user 弹，staff 永远不弹；`legal.enabled` 为 false 时链接、对话框、复选框都不出现。
- **页脚**：现在两处都只在 `site.footerText` 非空时才渲染页脚（`LoginView.tsx:615`、`UserLayout.tsx:105`），footerText 为空的站点会连法律链接一起丢掉。改为「`footerText` 非空**或** `legal.enabled`」时渲染，链接放在 footerText 之后。页脚字号是 11–12px，手机上链接的上下内边距补到可点区域 44px 高（§7.5）。
- 对话框点背景不关闭，Esc 等同「稍后」。「稍后」：本浏览器会话内不再提示（`sessionStorage`，读写都包 try/catch，读不到就照常弹），下次登录再问。
- 同意失败：框内一行 `md.error`「提交失败，请重试」；409（又发布了新版本）：「条款刚刚又更新了，请重新阅读」，链接刷新为最新版。
- 注册 409 `legal_consent_outdated`：表单顶部提示「条款刚刚更新，请重新阅读后再提交」，复选框取消勾选，并刷新版本号。

**交互**：同意调用 `POST /api/user/me/legal/accept {consent_version}`，成功后关闭并刷新 profile，snack「已记录你的同意」。注册复选框里的链接点了不会勾选复选框；`accepted_consent_version` 随注册请求一起发送。

**组件与文件**：`components/ConsentDialog.tsx`（Dialog `maxWidth="sm"`）、`RegisterView.tsx`、`LoginView.tsx`、`UserLayout.tsx`。文案：注册与公开页用 `auth:legal.*`，用户中心用 `user:legal.*`。

**375px**：对话框 fullScreen，两个按钮全宽、上下堆叠，「同意」在下；复选框文字可换行，链接的点击区域至少 44px 高。

---

#### 帮助文案（zh-CN 定稿）

四个 tab 右端的 `(?)` 各一段（`HelpTip`），en-US 按术语表逐句对应。`{{…}}` 一律从设置或常量插值（R14）。§10 的每一条已知限制都落在这里或下面注明的屏上，实现者**不得自己改写或删减**。

- **策略**：
  > 访问控制由节点强制执行，命中拦截的连接会被断开。「规则库」只决定客户端怎么分流，用户可以自己改掉。只有 PSP 原生节点执行这些策略；3X-UI 和 S-UI 的路由由运营者自己管理。
  > 规则从上到下判断，命中第一条就停止。「仅观察」命中后，后面的规则不再对这条连接生效。同一动作内的先后只决定命中记在哪条策略名下，建议把「计入风险」的拦截策略放在上面。
  > 节点只看得到目的地（域名或 IP），看不到网址路径。可被绕过：ECH、先用 DoH 解析再直连 IP、客户端自带的加密隧道。
  > IP 段只对以 IP 发起的连接生效，域名解析出的 IP 不参与匹配。BT 只识别明文握手与 uTP，加密 BT 和 DHT 识别不到。
  > 正则要对每个连接逐条匹配，很耗资源，最多 {{max_regexps}} 条。
  > 每次下发，节点会重启代理程序一次，正在使用的连接断开一次；最后一次修改后等 {{seconds}} 秒再下发，连续修改合并为一次。

  （§10 第 1、2、3、4、6、7 条）
- **列表**：
  > 支持的写法：纯域名、`domain:` / `full:` / `keyword:` / `regexp:`、`*.x`、hosts 行、AdGuard `||x^`、Clash 规则、IP 与 IP 段；`#` 开头是注释。保存时统一转小写、去尾点、中文域名转 punycode、IP 段对齐到网络号。
  > 远程与分类列表每 {{hours}} 小时刷新一次，只在内容变化时才下发；刷新失败时继续用旧内容。
  > 过宽的条目（几乎匹配所有域名的正则、少于 4 个字符的关键词、公共后缀本身、太大的 IP 段）在自定义列表和社区分类里会被剔除，并显示解析报告；远程 URL 列表出现它们时，整次刷新失败并继续用旧内容。社区分类过滤后为空时也保留旧内容，首次下载的列表仍未就绪。剔除的条目不会下发。
- **白名单分组**：
  > 白名单分组的成员只能访问名单里的目的地。全局拦截策略照常对他们生效；豁免的账号不受白名单限制。
  > 先试运行：试运行期间不拦截，只记录本应被拒绝的目的地。试运行只记到「分组 × 主域名」，看不到是谁，也看不到完整主机名。
  > 名单只写主域名时，网页的字体、脚本、第三方登录可能来自别的域名；看试运行报告再补。
  > 从试运行开始，本组成员就不再获得不执行白名单的节点（3X-UI、S-UI、版本过旧、规则太多，或仍在执行上一版的原生节点）。这些面板离线时，组员在上面的账号要等它恢复在线、下一次同步之后才会被删除。
  > 新节点第一次执行白名单时如果拒绝了策略，从发现到组员账号被移走大约要两个同步周期，这期间组员在这台节点上不受限。

  （§9.2 第 5 条、§9.5、§10 第 11 条）
- **记录**：
  > 每行是一个账号在一小时内对同一目的地的命中次数。记录的是节点识别出的域名，看不到网址路径。sing-box 节点只执行、不记录。白名单试运行只记到分组和主域名，默认不显示。命中记录保留 {{days}} 天。

  （§10 第 1、8、11 条）

另外两处不在 tab 帮助里：S14 `rejected` 的说明行带上 §10 第 10 条那一句；S16 用量表头的 HelpTip 写 §10 第 5 条。

### 7.5 无障碍清单（每个前端 PR 逐条勾选）

- [ ] 每个状态都有图标 + 文字 + 颜色，不只靠颜色。
- [ ] 可点的数字卡带 `aria-pressed`，再点一次撤销。
- [ ] 排序可以完全用键盘完成（⋯ 菜单里的上移、下移）；没有拖拽。
- [ ] 抽屉与对话框关闭后，焦点回到触发它的那一行或按钮；Esc 关闭（有未保存的修改时先确认）。
- [ ] 所有图标按钮都有 `aria-label`；对话框标题挂在 `aria-labelledby` 上（MUI 默认）。
- [ ] 表格有表头单元格；手机上折叠成块之后，每个值前面有视觉隐藏的标签。
- [ ] live region 只播报状态变化，不播报计时：结论条只有主句在 `role="status"` 里，倒计时与读取时间放在 `aria-hidden` 的副行（S1）。
- [ ] 深色与浅色下，徽章文字对比度 ≥ 4.5:1，由 `ToneBadge.contrast.test.ts` 自动检查（UI-0）；人工再看一眼 `md.error` 压在 `surfaceContainerHighest` 上的「拦截」徽章。
- [ ] 手机上可点区域 ≥ 44px。
- [ ] 日期与数字用 `Intl`，跟随界面语言。

### 7.6 UI 验收（合并前的必要条件）

1. **截图**（贴进 PR 描述）：本 PR 触及的每个视图、对话框、抽屉，各截以下几张：
   - 浅色与深色 × 1440×900 与 375×812（zh-CN）；
   - en-US 375×812 一张；
   - 空状态一张、错误状态一张。

   数据用新建的种子文件 `src/test/accessControlFixtures.ts`：
   - ≥ 8 条策略，三种动作都有，其中 1 条已停用、1 条引用未就绪的列表；
   - 5 个列表，其中 1 个刷新失败、1 个等待首次下载；
   - 2 个白名单分组，一个试运行、一个执行中；
   - 300 条命中，含已删除的策略和超长域名；
   - 12 台节点，覆盖 S14 表里的全部状态。

   **种子怎么进到页面**：仓库没有 MSW 一类的请求拦截层（F56），本地 PSP 也没有原生 agent，造不出 `/dest/status` 的各种状态。所以新增**只在开发环境生效**的 `src/dev/accessControlMock.ts`：一个 axios adapter，在 `import.meta.env.DEV` 且 `localStorage.psp_dev_fixtures === 'access'`（读写都包 try/catch）时，用 `src/test/accessControlFixtures.ts` 回答 `/api/admin/dest/*`、`/api/admin/risk-center/users/*` 与 `/api/admin/groups`；其余请求照常发往本地 PSP（`./psp` + `npm run dev`）。它只能经 `if (import.meta.env.DEV)` 里的动态 `import()` 引入，让生产构建把它整个摇掉；`smoke:dist` 加一条断言：生产包里没有 `accessControlFixtures` 字样。
   截图时在浏览器控制台执行 `localStorage.psp_dev_fixtures = 'access'`，然后刷新。不要拿生产数据截图。
2. **检查**：
   - 375 宽下页面没有横向滚动（截图时用浏览器开发者工具确认 `document.documentElement.scrollWidth <= innerWidth`）；
   - 长域名省略后可以悬停查看全文；
   - §7.5 全部勾选；
   - 新增的测试 `accessControlStyle.test.ts` 通过：扫描 `views/admin/accessControl` 与新增的 `components/*`，不得出现 `#[0-9a-fA-F]{3,8}\b`、`rgba?\(`、`<Chip[^>]*\bcolor=`。
3. **签字**：所有者看过截图并在 PR 上批准之后才能合并。实现者不得以「先合并、后调整」为由跳过。

### 7.7 前端测试清单

- 照 `views/admin/risk/policy/PolicyTab.test.tsx` 写（`createMemoryRouter` + 真实 zh-CN 字典 + mock ConfirmHost）的场景：带离开拦截的对话框（S3、S7、S9、S18、S21）、需要断言文案的测试、URL 往返测试。`useBlocker` 需要 data router，`adminSaveHarness` 用的是 `MemoryRouter`（`src/test/adminSaveHarness.tsx:78`），这些场景不能用它。
- 列表类、只读类测试用 `src/test/adminSaveHarness.tsx`。
- 每个 tab 至少一例：在 `phone` 为 true（mock `useMediaQuery`）时，断言折叠后的那一行存在。
- 具体文件见 §13。

---

## 8. 隐私与协议页（行为与后端；界面见 §7 S21–S23）

### 8.1 行为

- 设置 `legal.enabled`，**默认 false**。关闭时一切照旧，公开接口返回 404。
- 两份文档：服务条款、隐私政策。管理员用 Markdown 编辑，每种语言一份。
  **语言回落链**：请求语言 → zh-TW 回落 zh-CN → en-US → zh-CN → 404。
- **版本**：
  - `version` 按 `(kind, locale)` 自增，每次发布生成一行不可变的新记录；`(kind, locale, version)` 建唯一索引，撞版本号时重试一次。
  - `consent_version` 是**全局单调整数**（两种文档、所有语言共用），存在设置 `legal.consent_version` 里；任一文档发布时勾选「重大变更，需要用户重新同意」才 +1。改错别字不打扰用户。
  - **起点**：任一文档的**首次**发布，无论是否勾选重大变更，都把 `consent_version` 置为至少 1。否则第一次发布不勾选时它仍是 0，注册页的必勾项、`legal_pending` 和 `legal_consents` 的主键 `(user_id, 0)` 都没有定义。
  - `legal_pending = legal.enabled && consent_version > 0 && 不存在 consent_version ≥ 当前值的同意记录`。
  - **写入方**：`legal.enabled` 由系统设置的 PUT 写入（S21 的开关随设置页保存栏提交）；`consent_version` **只能**由发布接口写入（§8.3）。
- **「我们收集什么」自动生成**：Markdown 里单独一行写 `[[data-collection]]`，前端按这一行把正文切成两段，中间渲染由当前实际设置生成的清单：
  - 订阅访问日志（含 IP）：`sub.sub_log_retention_days`，**0 渲染为「永久保留」**
  - 登录记录：`security.auth_event_retention_days`
  - 连接历史（含 IP）：`risk.connection_retention_days`
  - 设备标识（HWID 摘要）：`risk.hwid_capture_off` 为 false 时列出
  - 风控标记记录（不含 IP）：`risk.flag_record_retention_days`
  - 风险判定（不含 IP，每小时覆盖）；管理员复核记录（不含 IP，无保留期，删号后一小时内清除）
  - 访问控制：哪些节点记录「规则命中」（`dest.hit_retention_days`）；白名单试运行（只记分组和主域名，`dest.trial_retention_days`）；哪些节点记录「按网站用量」（`dest.usage_retention_days`）

  清单文字走前端 i18n，数据由后端以结构化 JSON 给出。
- **大小**：`content` ≤ 60 000 字节（MySQL TEXT 上限 64 KiB，F36），超出返回 400 `legal_content_too_large`。

### 8.2 同意

- **只对 `role = user` 生效**；staff 不弹。
- **注册**：`legal.enabled` 且存在已发布版本时，注册表单出现必勾项。`registerRequest`（`handler/auth_register.go:25-34`）加 `accepted_consent_version`，与当前 `consent_version` 不符时返回 409 `legal_consent_outdated`（前端刷新后重试）。
- **其他来源的用户**（管理员创建、SSO 首次登录 `user.Service.EnsureSSO`、已有用户）：登录后 profile 返回 `legal_pending: true`，SPA 弹出同意对话框。
  **不做 403 门禁、不阻断订阅**：拒绝只是反复提示（所有者偏好少设准入门槛；阻断订阅会让用户莫名断网）。
- 同意记录 `legal_consents`：主键 `(user_id, consent_version)`、`accepted_at`、`method`（`register`/`prompt`）。**不记 IP。** 删除用户后由 P5 的孤儿清除处理。

### 8.3 实现清单

| 层 | 内容 |
|---|---|
| 表 | `legal_documents`（`id`、`kind` varchar(16)、`locale` varchar(16)、`version` int、`content` text、`consent_bump` bool、`published_at`、`published_by`；唯一索引 `(kind, locale, version)`）；`legal_consents` |
| 设置 | `legal.enabled`（默认 false）、`legal.consent_version`（默认 0）。**单一写入方**：`consent_version` 只由发布接口写；系统设置 PUT 保留它，加 `TestSettingsPut_PreservesLegalConsentVersion`（照 `admin_settings_policy_preserve_test.go:55`） |
| 公开 API | `GET /api/legal/:kind?lang=` → `{version, consent_version, locale, fallback_from?, content, published_at, data_collection:{…}}`，带 ETag（照 `handler/i18n_public.go:78`）；`GET /api/auth/methods`（`handler/auth_local.go:55-129`）加 `legal:{enabled, consent_version}` |
| 用户 API | `POST /api/user/me/legal/accept {consent_version}`；profile 加 `legal_pending` |
| 管理 API | `GET /api/admin/legal/:kind`（各语言的版本列表）；`POST /api/admin/legal/:kind`（发布新版本 `{locale, content, consent_bump}`）；`GET /api/admin/legal/affected-users`（重大变更会影响的普通用户数）。审计中间件会把单个字符串值截到 8192 字符（`middleware/audit.go:250`），审计行里的 `content` 只有开头，这是预期 |
| 前端 | §7 S21–S23 |

本页只解决「告知」。条款内容是否符合节点所在地的法律，需要所有者自行确认；本计划不提供法律意见。

---

## 9. 白名单模式（按分组：只能访问名单内的目的地，其余一律拒绝）

「拒绝」本身只是 §3.2 第 4c 条的一条规则。真正要设计的是下面五件事。界面见 §7 S8–S11、S17。

### 9.1 规则

见 §3.2 第 4 条与 §4.1 的 ID 表：名单是 `g<gid>x1`；基础放行和补充是 `g<gid>x2`；兜底是 `g<gid>`，试运行时 `observe`、执行时 `block`。
每个白名单分组自带两个 custom 列表（`owner_group_id = gid`）：「<分组> · 基础放行」（§9.3）和「<分组> · 补充」（试运行报告「加入白名单」写入这里）。

### 9.2 执行保证：不能执行的节点上不给组员开账号（关键）

白名单只在「用户认证所在的那台原生节点、且策略已生效」时成立。3X-UI / S-UI、旧版原生节点、超额度、被拒或嗅探不足的节点都做不到。
**只在订阅里隐藏这些节点不够**：组员在那些面板上的 client 仍然存在，旧订阅照样能连。必须在成员同步这一层排除。

1. **一个判断，全仓共用**：新增 `group.Service.Eligible(ctx, node, group) (bool, error)`。
   - 分组不是白名单模式 → 等同今天的 `Matches(node, group.TagFilter)`。
   - 是白名单模式（试运行和执行**两个阶段都适用**）→ 还要同时满足：
     - 面板 `Kind == psp`；
     - agent 本次的 `observed_capabilities` 含 `policy.destination.v1`；
      - `dest_agent_policy.fallback_reason` 为空且 fallback_exhausted=false（不在超额度、被拒、嗅探不足或回退终点中）。
   - **失败语义**：任何一次读失败都返回 error，**绝不**当作 false 或 true。
     - `NodesFor` 把 error 原样返回；
     - `ResyncMembership` 拿到 error 时整体中止、不做任何删除，交给同步任务按常规节奏重试；
     - render 沿用现有的错误路径（`/sub` 失败，客户端保留上次的订阅）；
     - **「添加」方向的三处调用点**（`node.go:597,1362`、`reconcile.go:429`，这些函数没有返回值）：出错就**跳过该分组**（这一轮不添加），记一条只含 `group_id` 的 Warn。不添加是安全的方向，下一轮或 heal 会补上。
   - **缓存**：`group.Service` 内加一份按面板的合格性缓存，TTL 30 秒。以下事件主动失效：三个相关能力的增减、`fallback_reason` 变化、分组模式或阶段变化。失效通过 `group.InvalidateEligibility(panelID)`（模式与阶段变化时失效全部）。`/sub` 热路径（`render.go:108`）不额外查库。
   - `group.Service` 因此要注入面板 repo、agent repo 和 `dest_agent_policy` 读接口（一个窄接口）。`NodesFor` 对 `TagFilter.All` 的捷径（`group.go:104-106`）之后，仍要过滤掉不合格的节点。
   - **依赖为 nil 时的降级**：没有注入 dest 读接口时，`Eligible` 退化为 `Matches`，因为此时也编不出任何策略（`nodesync.Options.Policies` 同样是 nil）。接线测试 `TestBuildWiresTheDestinationRepos` 必须断言生产环境已经注入，否则「白名单」会静默失效。
2. **F34 的 10 处**：
   - `NodesFor` 改调 `Eligible` 之后，经它的 7 处自动生效，handler 不用改；
   - 只有 `reconcile.go:429` 与 `node.go:597,1362` 要改为注入 `group.Service` 并调 `Eligible`。
3. **守卫测试**：用 `go/ast` 扫 `internal/service`、`internal/transport`、`internal/app`，除 `internal/service/group` 外不得出现对 `group.Matches` 的调用（防止以后新增第 11 处时绕过）。
4. **触发重新同步**：
   - 分组切换模式、白名单阶段变化 → 先 `group.InvalidateEligibility`，再 `user.ResyncGroupMembersInBackground(groupID)`（`service/user/user.go:2126`）。
   - 在 `nodesync.ingestReport` 写能力处（`nodesync.go:479`）比较新旧能力集，**只看 `policy.destination.v1` 是否出现或消失**。#73 之后任务能力会逐次变化（F24），所以不能比较整个集合。出现或消失时，对所有白名单分组调用同一个函数，否则新装或刚升级的节点永远不会给组员开 client。
   - `dest_agent_policy.fallback_reason` 由空变非空或反过来时，同样调用。
   - **接线**：新增 `nodesync.SetAllowlistResyncer(func(ctx context.Context, panelID int64))`，destpolicy（`fallback_reason` 变化）与 nodesync（能力变化）共用它。它**先**调用 `group.InvalidateEligibility(panelID)`，**再**对所有白名单分组异步调用 `ResyncGroupMembersInBackground`。顺序是必须的：反过来，重同步会在 30 秒缓存里读到旧的合格性。setter 与 `group.Service` 的新依赖都在 `app.go` 接线，并纳入 `TestBuildWiresTheDestinationRepos`；setter 为 nil 时不触发重同步，由 heal 兜底（写进注释）。
5. **兜底**（依赖 PSP #268，F43）。重试来自 **user_resync 任务与 heal**，不是删除本身入队（`deleteRetiredSharedClients` 失败只打日志，F54）：
   - `ResyncMembership` 返回错误时，`ResyncMembershipOrEnqueue` 把它记为 user_resync 任务；原生节点离线时按 1 分钟节奏重试（#268），3X-UI / S-UI 离线同样重试，最多 100 次（约 1.5 小时），之后任务被取消；第 6 条保证删除失败会让 `ResyncMembership` 返回错误；
   - 再由 `HealSharedClients` 兜底收敛（迁移完成后每第 4 个 reconcile 周期对每个用户重跑一次 `ResyncMembership`）；保留的空 attachment 行每次都会再次被列为 retired（`clientprov.go:69-78`），所以重跑一定会再试删除。
   - 所以「组员在不执行节点上残留 client」的最长时间 = 面板恢复在线之后的下一次重试或兜底周期。这一点写进白名单 tab 的帮助（§7.4「帮助文案」）。
6. **整面板移出时，删除不依赖其他面板**（F54）。现在 `ResyncMembership` 只在 `provisioned && lifeErr == nil` 时才删除 retired client，而 provision 只要组员**任意另一台**面板失败（比如某台原生节点离线，#268 的注释写明离线会让 provision 报错）就不成立：3X-UI 上的旧 client 于是不删，白名单可以靠旧订阅绕过，直到那台节点恢复。`ReconcileOrphans` 也救不了，它只扫描用户还有期望 client 的面板。改法：
   - `clientprov.Sync` 区分两类 retired：**整面板移出**（该面板的期望为空，`clientprov.go:69-78` 那一支）与**同面板内的重组**（合并、改键之后被替换的 client）；
   - 整面板移出的那一类，删除**不依赖** `provisioned` 与 `lifeErr`：它只收紧权限，理由与 `ReconcileOrphans` 的注释相同（`user.go:2284-2292`）。同面板重组的那一类保持现有前提不变：替换它的 client 还没上线时删掉旧的，会让用户断网；
   - 删除失败要**返回错误**（并入 `firstErr`），让 user_resync 按 #268 的节奏重试，而不是像现在一样只打日志。
   - 测试：`allowlist_resync_test.go` 加一例「组员所在的另一台原生节点离线时，分组切到白名单 → 3X-UI 上的 client 仍在同一次 resync 里被删除」；再加一例「同面板重组时 provision 失败 → 旧 client 不删」，锁住第二类的现有行为。

### 9.3 DNS 与基础放行

白名单组员的客户端如果有 DNS 查询**经由代理**发出而没被放行，表现是「所有网站都打不开」，而不是「只有名单外的打不开」。

1. 每个白名单分组自带一份「基础放行」列表，创建时预填**实际经代理发出的 DoH 主机**。判断规则如下。实现者开工时按此规则从**当前**模板重新读，不要照抄本文的示例：模板是运营者可改的文件，`internal/seed` 只在文件不存在时写入。
   - mihomo：`nameserver-policy` 里带 `#<代理组>` 后缀的条目（经所选节点发出）；`default-nameserver`、`proxy-server-nameserver` 是直连引导，不算。
   - sing-box：被 `dns.rules` 引用、且未设 `detour: direct` 的 https 服务器；定义了但没被任何规则引用的不算。
   - 以 2026-09-28 的默认模板为例，只有 `l9f26nnn5d.cloudflare-gateway.com` 一项（`internal/seed/files/templates/default-mihomo.yaml` 的 `nameserver-policy`）。
2. **不放开任意 53 端口**：那等于留一条 DNS 隧道。
3. 「测试目的地」对白名单组员要能回答「放行还是拒绝、命中哪条」（§7 S13）。

### 9.4 网站依赖：先试运行，再执行

网页的字体、脚本、图片、第三方登录往往来自别的域名，只写主域名会让页面残缺。白名单分两个阶段，**新建时强制从试运行开始**：

| 阶段 | 兜底动作 | 组员体验 | 管理员看到 |
|---|---|---|---|
| 试运行 | `observe`（`psp-watch-g<gid>`） | 照常上网；**但从试运行开始就不再获得不执行白名单的节点**（R5：这些节点上的流量试运行报告看不见，留着它们会漏报） | 试运行报告：过去 N 天**本应被拒绝**的主域名，按次数排序，每行一键「加入白名单」（写进本组的「补充」列表） |
| 执行 | `block`（`psp-deny-g<gid>`） | 名单外拒绝 | 被拒记录（命中表，`action = block`、`source = g<gid>`） |

- 报告数据就是 A 档命中（兜底规则的 observe 命中），不需要 B 档。**节点在聚合前**就折叠为「分组 × 主域名」，不带账号、端口与完整主机名，放进独立的试运行聚合器（R5、R15，§4.2、§5 N4）；PSP 只校验后入库，保留期为 `dest.trial_retention_days`。
- **切换到执行**是一次显式操作。服务端门槛只有一条：名单（含补充）里有未就绪的列表时返回 409 `dest_list_not_ready`。界面再加就绪清单（§7 S11）。列表首次拉取成功之后，刷新失败一律保留旧条目（P2），所以执行中的名单不会因刷新失败变空。
- 兜底命中量很大（每个后台 App 的心跳都算）：**永不计入风险信号**；受 trial 独立的聚合、pending、队列、发送机会与入库预算约束，不能消耗 block 的预留配额。共享 CPU/数据库故障、block 自身超额仍会丢失；报告按 P4 的 losses 显示「相关节点有记录未统计；统计可能不完整」，不能承诺绝对零丢失。
- **数据量估算**（按每个白名单分组每小时最多「节点 × 主域名」行计算，而不是「人 × 主机名」；节点侧与 PSP 侧都是这个量级，因为折叠在节点上做）：

  | 场景 | 每小时行数 | 7 天行数 | SQLite 粗估 |
  |---|---|---|---|
  | 1 个分组，200 人，每小时 300 个不同主域名，10 台节点 | ≤ 3 000（节点 × 主域名） | ≤ 50 万 | 约 60 MB |
  | 同上，若按「人 × 主机名 × 端口」聚合（第二版做法；第三版初稿只在 PSP 入库时折叠，节点侧仍是这个量级） | 可达 60 万 | 可达 1 亿 | 不可接受；节点聚合器 20 000 键、单次 4 096 行也会被它占满 |

  验收要按 §12 第 40 条实测，结果回填这张表。

### 9.5 切换空窗

- **新用户加入白名单分组**：与 roster 变化在同一轮生效（P3 第 1 条：成员不进快照），core 只重启一次，**没有空窗**。
- **新节点第一次声明能力**：组员的 client 与本组规则在同一次部署里到达。若这台节点随后拒绝了策略，从回退被发现到组员 client 被删除（§9.2 第 4 条）之间约两个同步周期，组员在这台节点上不受限。本计划接受这个空窗，写进白名单 tab 的帮助。
- **「不限 → 白名单」**：定义修改走尾随去抖，最晚约 `dest.policy_apply_min_seconds` 加一个同步周期后生效，core 重启一次。这段时间里试运行规则还没下发，组员不受限；试运行本来就不拦截，所以无害。
- **「试运行 → 执行」**：同样有去抖窗口，窗口内仍是试运行（只记录）。确认框写的「约 N 分钟内生效」就是指这个。
- 反方向（切回不限、切回试运行、用户移出分组）：空窗期里仍然受限，方向是安全的。
- 每台节点的生效情况见 `/dest/status` 与 §7 S14。

---

## 10. 已知限制（写进界面帮助，不要让管理员以为是保证）

| # | 限制 | 界面上写在哪 |
|---|---|---|
| 1 | **看不到 URL 路径**：HTTPS 只能看到 SNI 或域名；界面一律叫「目的地」，不叫「网址」 | S13 输入说明、S12 帮助 |
| 2 | **可绕过**：ECH、DoH 之后直连 IP、客户端自带的加密隧道 | 策略 tab 帮助（§7.4「帮助文案」） |
| 3 | **IP/CIDR 规则只对「以 IP 发起、且未被嗅探改写」的连接生效**；域名解析出的 IP 不参与匹配（Xray `AsIs`；sing-box 未加 `resolve`）。所以 Xray 上访问 `169.254.169.254.nip.io` 会被 freedom 的默认规则挡住（F9），但**没有命中记录** | S3 的 IP 段提示、S13 结果注释 |
| 4 | **BT 识别**只覆盖明文握手与 uTP；加密 BT（MSE/PE）、DHT 识别不到 | S3 BT 选项、S4 模板卡 |
| 5 | **B 档用量里的目的地是客户端请求的地址**：客户端以 IP 发起时只计为 `(ip)`（F6）。A 档命中不受此限（webhook 带嗅探域名） | S16 用量表头悬停 |
| 6 | **每次策略变更，节点 core 重启一次**（F13），现有连接会断开；尾随去抖与摘要比对把次数压到最低 | S1 下发条、S20 |
| 7 | **正则很贵**：每个连接都要逐条跑，上限 256 条 | 策略 tab 帮助；S2、S3 的额度 |
| 8 | **sing-box 节点首版只执行、不记录** | S14 |
| 9 | **入站嗅探配置不足时，策略在那台节点上不下发**（PSP 预检），节点继续执行上一版，直到运营者改好入站 | S14、S15 |
| 10 | **策略被拒时尝试修剪后的上一版；上一版也不可用则不执行访问策略**（§6 P3 第 7 条）。删除已停用的 allow 可能暴露后续拦截；回退中新增作用分组成员不受旧版该分组拦截，白名单成员会从不合格节点移除 | S14 的回退说明行 |
| 11 | **白名单试运行只记到「分组 × 主域名」**，看不到是谁、看不到完整主机名 | 白名单 tab 帮助、S10 表头、记录 tab 帮助 |
| 12 | **PSP 降级会撤去策略与白名单保证**：在线节点在收到并成功部署旧 PSP 配置后停止执行，离线节点可能继续运行旧策略；旧 PSP 的成员重同步也会重新开放其他面板（§11.2） | 发布说明 |
| 13 | **尽力交付**：节点/PSP 内存积压可能在重启时全部丢失；批次最长重试 48h，队列满、超额或部分入库失败也会丢数据。没有「最多一个同步周期」或全量审计保证 | S10、S12、S16 帮助，Node README，Protocol godoc |
| 14 | **用量包含正常放行与仅观察连接**；拦截尝试只记命中、不记用量；显式开启 B 档后，白名单试运行连接也按账号统计用量 | S14、S16，隐私页采集清单 |

---

## 11. 分阶段、PR、发版顺序与升级降级

### 11.1 阶段

| 阶段 | 内容 | 仓库 | 依赖 | 发版 |
|---|---|---|---|---|
| **0** | N0 热修 | Node | 无 | Node `v4.0.1.9`（预发布，§5 N0） |
| **UI-0** | §7.2 的共用部件抽取与扩展（ToneBadge、StatusLine、KpiTile 可点、FieldHint amber、CodeEditor、PolicyField、useLeaveGuard、useDirtyClose、useDrawerParam）；除徽章对比度修正外行为不变 | PSP | 无 | 随下一个 PSP 版本 |
| **1a** | §4.1：`ConfigBody.Policy`、`PolicyStatus`、`ValidateDestinationPolicy`、`ValidatePolicyStatus`、`PolicyDigest`、`MatchDestination`、conformance 向量、两个 fuzz 目标 | Protocol | 无 | tag `v0.3.0` |
| **1b** | N1（含 `PolicyError` 与空闲短路）、N1-X、N1-S、N3（含「被拒」的归因、`process.ErrCandidateRejected`、`RestoreStatus`）、N7 的 `policy.destination.v1`；`cmd/contract-agent`（不动 `protocol/alias.go`，§4.3 第 7 条） | Node | 1a、0 | Node 下一个第四段号（预发布） |
| **1c** | P1（定义表、`dest_policy_state`、快照、`dest_agent_policy`、`xui_panels.audit_collect`）、P2、P3（含一致快照与原子发布、ObserveStatus、预检、实际候选事务、LKG/exhausted 与修剪、空规则、paused、缓存）、P4 第 1 条中 PolicyStatus 的隔离解码、P5 的 `dest.*` 与 `/dest/settings`、P7 中除 groups、hits、usage 以外的全部接口（列表含 `entries`、geosite、策略、豁免、`exceptions` 的全局一支、test、status、publish、pause、retry、settings、`users/:id` 的分组与豁免部分、servers 的 `audit_collect`）、预览接口免审计、访问日志去查询串、P8、P9 中 1c 的 family、`adminOnlyAuditTargets`；界面 S1–S7、S13、S14（不含记录方式）、S15（状态行与对话框，不含记录方式）、S16 的「分组 / 豁免」两行、S18、S20；页外深链：`NodeIssuesView ?agent=`、`NodesView ?inbound=`、`ServersView` 搜索框回填；`docs/access-control.md` 初版。`contract_source` 不动（§4.3 第 8 条） | PSP | 1a、UI-0 | PSP 预发布 |
| **1c′** | `docs/compat/verification-v1.json` 的 `contract_source` 切到 1b 的 tag 与 commit，contract-agent 声明 `policy.destination.v1`，覆盖 PolicyStatus 的往返 | PSP | 1b **已发布**、1c | 随下一个 PSP 版本 |
| **2a** | §4.2 Audit（Kind/Hour/CollectRevision、仅计数批次、时间常量）、ValidateAuditObservation、fuzz 目标 | Protocol | 1a | tag `v0.4.0` |
| **2b** | N4（block/observe/trial 独立聚合、鉴权、sink lifecycle）、N5 的逐行校验入口、N6（分类 pending、七槽调度、冻结、过期与 off 清空）、N7 的 audit.hits.v1 | Node | 2a、1b | Node 预发布 |
| **2c** | P1 的 hits/batches/loss/budget 表、P4（隔离解码、分类队列、分块调度、持久去重/预算、档位闸门）、P5 的保留与匿名孤儿清理、P6、P7 的 hits、losses 与 users/:id 命中接口、P9 中 2c family；S12、S14/S15 的记录方式、S16 命中、S19 与完整性提示。此时 usage 协议/队列/预算可以就绪，但不开放 B 档、不创建逐人用量；空 usage 表可以按 P1 提前迁移，实际写入与查询在 4 才接通 | PSP | 2a、1c | PSP 预发布 |
| **3** | §8 隐私与协议页；界面 S21–S23 | PSP | 无 | 可与 1、2 任意并行 |
| **4** | B 档：N5（watch 也计用量、deny 不计用量、过滤器故障退出）、audit.usage.v1、P1 usage 表、usage 档位闸门与 loss 清理、P7 usage 与读取审计、S14 第三项、S16 用量、隐私页更新。Usage 的协议字段已在 2a，这一阶段不动 Protocol | Node + PSP | 2、3（B 档上线前隐私页已可用） | Node 先、PSP 后，各自预发布 |
| **5** | §9 白名单模式（含 §9.2 第 6 条对 `ResyncMembership` 的修改）；P7 的 groups 接口、`exceptions` 的分组一支、`GET /groups` 的 `dest_mode`；界面 S8–S11、S17 | PSP（Node 只加测试） | 1（执行）、2（试运行报告要看命中） | 可与 4 并行 |
| **6（后续研究，不属首轮交付）** | sing-box 采集；先验证事件来源，再单独提交设计，不按本计划直接实现 | Node | 2 | 独立计划后决定 |
| **N0-S（条件热修）** | sing-box 私有地址拦截 | Node | 按 §1.2 Q1 默认先完成 VM 验证；不阻塞 N0 与 1–5 | 验证后随 Node 下一修复版 |

- 每个阶段在各自的仓库里开一个或多个 PR，分支从最新 origin/main 切，命名 `Kazuha/access-control-<阶段>`（N0 用 `Kazuha/xray-access-log-off`）。版本号都是基线下的目标值，开工与打 tag 前核对是否已占用；不覆盖已有 tag。
  开发中允许基于尚未合并的前置 PR 临时叠加分支，但必须在 PR 写明 base 和依赖，检查 diff 只包含本包改动。按依赖顺序合并；前置包合并后重新对齐最新 main、更新 PR base，并重新验证受影响的组合，不能把叠加分支的绿灯当成最终合并结果。
- 1b 与 1c 可以在 1a 打 tag 之前用伪版本并行开发（§4.3 第 6 条）；合并前换成正式 tag。
- 每个 PR 的描述里贴对应小节完成判据的勾选情况；**前端 PR 附 §7.6 的截图，并取得所有者批准**。
- **发版方式**：
  - **Node**：等 main 上该提交的 `test.yml` 跑完全绿，再推注解 tag；预发布不需要审批（#74）。改到 `internal/upgrade/docker_*` 时，还要等非必需检查 `docker-updater.yml` 也绿。升为正式版走 `promote.yml`，由所有者决定。
  - **PSP**：等 `test.yml` 跑完全绿，用仓库的分配器拿号，再推注解 tag；没有审批环境。
  - **Protocol**：只推模块 tag。

  三个仓库的非正式发布都不需要人工确认，正式版仍由所有者决定。
- **远程升级**：所有 Node 版本都不改 state schema（仍为 9），PSP 远程升级照常可用（F14）。Docker 节点的 updater 早于 4.0.1.7 的，要先手动重建一次，之后才能远程升级（F27）。

### 11.2 升级与降级

| 组合 | 结果 | 要做的事 |
|---|---|---|
| 升级 PSP，节点全是旧版 | config ETag 不变，core 不重启（§12 第 3 条） | 无 |
| 升级节点，PSP 还没有任何策略 | 只有 N0 那一次重启 | 发布说明写明 |
| 第一次启用任何策略或白名单 | 健康在线节点通常在一个去抖窗口加一个同步周期内各部署一次新策略，core 因此各重启一次；节点按各自同步时刻生效，离线或部署失败节点没有这个时限，也不存在全机队原子切换 | S20 第一次下发的确认框写出节点数，状态页逐节点确认 |
| **PSP 降级**到不含本功能的版本 | 旧 PSP mint 出的 ConfigBody 不带 Policy，在线节点在收到并成功部署后停止执行策略并重启 core；离线或部署失败节点可能继续执行旧 artifact，不能视作已撤销。旧 PSP 也没有 `Eligible`，白名单组员会在下一次重同步后重新拿到 3X-UI 等节点，白名单保证已撤去。`dest_*` 表保持原样，再次升级后恢复 | 写进 `docs/UPGRADE-v4.md` 和该版本的发布说明（警告级）；降级演练记录逐节点实际生效时间 |
| **节点回滚**（远程升级回退到旧版） | 能力消失 → PSP 不再下发 Policy → 该节点重启一次、失去策略；能力变化触发白名单重同步，该节点被排除 | 无（自动） |
| 新节点 + 旧 PSP | 多出来的 Audit、PolicyStatus 被旧 PSP 静默忽略（F30：非严格解码，旧版的 base 校验不认识它们） | 无 |
| 新 PSP + 旧节点 | 节点没有能力，PSP 不下发策略；状态显示「需升级」 | 无 |

---

### 11.3 执行入口与阶段完成门槛

首轮范围为 **0、UI-0、1a–1c′、2a–2c、3、4、5**。N0-S 按验证结果单独修复，阶段 6 另立计划。实际阶段状态见 §11.5；不得将文档定稿、基础表迁移或单个 PR 的 CI 通过计作首轮功能完成。

1. **先执行 N0**：只关闭 Xray 默认访问日志，先写失败测试、再修编译器；测试与 main CI、真实二进制及 systemd/Docker 验收完成后发布 Node 修复预发布版。它没有 Protocol、PSP 或 UI 前置依赖。
2. **建立执行能力**：1a 发布 Protocol v0.3.0，随后 1b（Node）与 1c（PSP）开发，UI-0 先于 1c 前端；对外发布遵守 Node → PSP，1c′ 在已发布 Node 上锁住契约。
3. **建立 A 档**：2a 发布 Protocol v0.4.0，随后 2b（Node）与 2c（PSP）；先完成七项修订相关回归和三方言入库验证，再发布。全旧节点、全新节点、混合机队都要覆盖。
4. **交付剩余功能**：阶段 3 可独立推进；阶段 4 在 2、3 交付后开始，阶段 5 在 1、2 交付后开始。B 档上线要有已可用的隐私页，但仍尊重所有者的默认关闭决定；白名单试运行无需开启 B 档。
5. **每个工作包收尾**：对应失败测试已证明能捕获问题；实现后检查全绿；所需真实运行证据、三方言结果、UI 截图、文档和兼容矩阵齐全；PR CI 与合并后 main CI 都完成后才发布。无法运行的真实环境验收明确记「未验证」，不能仅因单元测试通过宣称阶段完成。

执行记录放在各阶段 PR 描述，包含基线 SHA、Protocol tag、实现 SHA、测试命令/结果、真实验收证据与版本号。本文是唯一行为基准；变更默认或发现新约束时先修订对应正文，再继续相关实现。

### 11.4 开工准备与证据口径

- 开工前核对三个仓库的远端 HEAD、当前分支和依赖版本；安装或定位各仓库钉住的工具链。文档中记录的版本是复核基线，不能代替开工时检查。
- N0 开工先确认可用的 Linux 验收环境、systemd 与 Docker 两种部署、PSP 远程升级通路及三个 catalog Xray 二进制；后续阶段再准备 sing-box、三方言数据库与浏览器截图环境。`psp-node` Lima VM 是验收环境的历史名称，不能假定每台开发机都已具备；可使用能完成同等场景的 Linux VM/测试主机，并在 PR 记录实际环境。
- 证据分为「文档检查」「单元/集成测试」「PR CI」「合并后 main CI」「真实部署验收」。跳过的测试、未接通的 VM 或未运行的方言都记为未验证；真实内核 `-test` 只证明配置可接受，不能替代代理连接、采集、升级和日志验收。
- 首轮完成以 §11.3 的全部范围和 §12 对应场景为准；N0 的单独交付只能标记阶段 0 完成。阶段 6 与条件热修 N0-S 分别记录，不能混入首轮已完成清单。

### 11.5 已有工作与未完成项（2026-10-04 核对）

以下为本版定稿时的证据快照，PR 的后续提交、合并与发布状态以实际记录更新；三个仓库 main 仍是首页的复核基线。**当前没有任何阶段被本计划认定为已完成或已发布。**

| 工作包 | 已有证据 | 下一完成门槛 |
|---|---|---|
| 0 / N0 | [Node #77](https://github.com/KazuhaHub/Passwall-Node/pull/77)，draft，`48b52a1`，本次核对全部检查成功 | systemd/Docker 实际代理连接、首次同步失败窗口与远程升级日志验收；合并后 main CI 与预发布 |
| UI-0 | [PSP #272](https://github.com/KazuhaHub/Passwall-Sub-Panel/pull/272)，draft，`d8722e89`，必需检查成功；第三方面板真实检查为 skipped，不计已验证；附有部分浏览器截图 | 补齐 §7.6 全部截图和交互矩阵、§7.5 无障碍清单，再由所有者审阅实际界面；当前截图不等于批准 |
| 1a | [Protocol #4](https://github.com/KazuhaHub/Passwall-Protocol/pull/4)，draft，`0175987`，两项 PR 检查成功 | 完成评审、合并后的 consumer toolchains 等门禁，再发布模块 tag v0.3.0；伪版本不等于正式模块发布 |
| 1b | [Node #78](https://github.com/KazuhaHub/Passwall-Node/pull/78)，draft，`8cda96e`，本次核对全部检查成功，临时依赖前置包 | 前置包合并、依赖正式模块 tag、完整 N8 与 §12 执行/状态实测，再按 Node 发布门槛交付 |
| 1c | [PSP #273](https://github.com/KazuhaHub/Passwall-Sub-Panel/pull/273)，draft，临时以 UI-0 #272 为 base；定义事务 `9b475324` 已通过完整 CI。解析、缓存与报告 `54ec95b6` 已通过 [完整 Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37191810887)，含三方言、Linux race 和固定真实分类数据。列表服务、循环和空远程保护 `e7697f51` 的完整本地 destlist/SQL-store/domain/safehttp 套件通过；先前 Windows 拦截未在新程序出现，未修改安全设置。文档提交 `00e86780` 的 [PR CI](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37192773685) 在运行，之前两个被新提交取代而取消的 workflow 不算完整成功 | 继续 §11.6；应用、持久设置、API、编译/下发、页面和联调尚未接通。所有后续提交各自验证，空表、缓存和草稿 PR 都不表示阶段完成 |
| 1c′、2a–2c、3、4、5 | 尚无本次核对可确认的交付证据 | 按 §11.1 依赖和对应验收启动；不因前置表存在跳过工作包 |
| N0-S | Linux 实际环境验证尚未完成 | 按 Q1 先验证，再决定修复提交；保持与首轮主路径分开 |

### 11.6 接下来的实施工作包与交接

1c 可拆为以下连续 PR；拆分仅用于控制评审规模，不减少 §11.1 的范围。每个子包都先有失败测试，再提交实现和文档；某一子包完成只更新子包状态，直到全部条件满足才标记 1c 完成。

| 顺序 | 子包 | 验收与后续依赖 |
|---|---|---|
| C1 | 具体 repo、定义写入口、列表/策略/豁免 CRUD、排序、generation、一致读、快照发布 CAS | SQLite/MySQL/PG 的并发交错与故障回滚；同毫秒编辑、旧发布失败、缺失快照均有测试。schema 基础归入此包，但不能单独关闭 C1 |
| C2 | 列表解析、规范化、safehttp、分类下载校验、缓存与刷新循环 | 格式/大小/过宽/来源版本测试；分类剔除与持久报告、真实 release 和空结果保护（15b）；相同摘要不重启；旧刷新结果不覆盖新定义；无网络 I/O 持锁 |
| C3 | 编译、预检、ObserveStatus、原子 candidate mint、LKG 修剪/耗尽、paused、缓存与接线 | 真内核和 Node 1b 联调；策略故障不阻断 roster；实际候选与 stream 一起回滚；nil/旧节点字节及 ETag 不变 |
| C4 | settings、全部 1c API、权限/审计/日志边界、清理、指标、架构与升级文档 | 路由与接线守卫，预览免审计且 test 留审计，查询串不泄漏，诊断目录和三方言通过 |
| C5 | UI-0 后的 1c 页面、抽屉、编辑器、深链、访问状态与设置 | §7.5、§7.6 和 1c 屏幕矩阵全部满足；附真实截图和中英文/主题/移动端证据 |
| C6 | 整包联调与兼容、升级降级演练、发布准备 | 对应 §12 实测记录逐条关联到实现 SHA；全部子包及前置依赖完成后才能进入 1c 发布门槛，随后执行 1c′ |

每个 PR 的交接记录统一包含：工作包及对应正文小节、base/依赖 PR、实现 SHA 与工具链/模块版本、失败测试与最终测试结果、真实环境/截图证据、未验证项和下一依赖。CI 结果绑定被测 SHA；代码再变时补跑受影响的检查。最终发布记录另附合并后 main CI、实际 tag 和升级降级结果。

没有可用 Linux 真实验收环境时，继续不依赖它的实现和 CI，把运行验收保留为未验证；不默认满足、也不因此删除该门槛。界面批准发生在可审阅的完整截图和交互证据准备好之后。

---

## 12. 验收场景（`psp-node` Lima VM + 本地 PSP，逐条记录结果）

**A. 热修与兼容**

1. **N0**：分别在健康同步的 systemd 和 Docker 部署上升级节点，确认首次成功 converge 后实际产物含 `access:"none"`；**升级完成 1 分钟之后**，用真实代理连接确认 `journalctl -u passwall-node` 与 `docker logs` 都不再出现新的 `accepted … email:` 行。另让首次同步失败，记录旧 artifact 仍可能写日志的窗口，恢复后确认切换与停止；既有历史日志的清理按第 2 条，不把历史行误判为新泄漏。
2. **N0 远程升级**：经 PSP 远程升级一台 Docker 节点，旧容器被删除；手动部署的节点按发布说明操作之后，旧日志消失。
3. **旧节点兼容**：PSP 升级后，未升级的原生节点 config ETag 不变、core 不重启；节点覆盖显示「需升级」。
4. **无策略零扰动**：已升级的节点、PSP 没有任何策略 → config ETag 与升级前相同（只有 N0 那一次重启）。开了 B 档的节点即使没有策略 ETag 也会变，这是预期。

**B. 执行**

5. **禁止 BT**：客户端跑明文 BT 下载 → 连接被断；命中记录出现，dest 是 tracker 或对端。
6. **禁止发信**：经代理 `nc smtp.gmail.com 25` 失败；命中记录正确。
7. **内网与元数据**：
   - 7a：启用「禁止访问内网与云元数据」模板后，IP 字面量 `curl http://169.254.169.254/`：Xray 失败且有命中；sing-box 失败（sing-box 不记录命中，§10 第 8 条）；
   - 7b：Xray 上 `curl http://169.254.169.254.nip.io/` 失败（freedom 默认规则）、**没有**命中（§10 第 3 条）；
   - 7c：sing-box 上同样的 nip.io 请求，结果按 §1.2 Q1 的决定记录；
   - 7d：IPv4 映射的 IPv6 `::ffff:169.254.169.254` 在两种内核上的结果都要记录。
8. **放行优先**：同一域名同时在 allow 与 block 列表中 → 放行，没有命中记录。
9. **豁免**：被豁免的用户访问 block 列表里的域名 → 放行、没有命中；豁免到期后（设 24 小时并手动调时钟，或把到期时间设在近处）重新受限。
10. **分组作用域**：只对分组 A 生效的策略不影响分组 B 的用户。
11. **仅观察**：连接成功，命中记录出现，动作显示「仅观察」。
12. **测试目的地**：对第 5–11 条的每个目的地，S13 的结论与实际结果一致；以域名测试时不发生任何 DNS 查询（在 VM 上抓包确认）。

**C. 下发、回退与状态**

13. **去抖**：一分钟内连改三条策略 → 每台节点只重启一次；改完立即点「立即下发」→ 不等窗口直接生效。
14. **去抖窗口内新成员**：管理员改一条策略之后马上把一个用户加入白名单分组 → 该用户在下一轮就受限。
15. **远程列表**：内容不变的刷新不触发任何节点重启（看 core 部署记录与节点日志）；内容变化只触发一次。
    - 15a　**刷新并发**：阻塞一次旧来源下载，修改 URL/分类或删除列表，再释放下载；旧内容和旧失败均不覆盖新状态、不复活已删除行、不推进错误 generation。相同摘要只更新元数据，完整测试在三方言重复。
    - 15b　**分类过滤与真实数据**：固定 release `20261004053124` 的 YAML 及校验文件，记录 sha256 `c0f7da9a7f95c86b354002650e8268b9d6bb0b638229274d3afa5651a7cf74b8`；离线回归验证 category-finance 可得到非空有效结果、`domain:hsbc` 被剔除、其余合法域名保留，报告列出原因，任何 action 的下发内容均无过宽条目。另用小 fixture 覆盖四种过宽判定、多个属性（含 !cn 与两种分隔兼容）、属性筛选后原始序号、去重与报告样本上限；全部被剔除时不覆盖旧内容或标就绪。后台刷新与重启后 S6/S7 可读同一成功报告；摘要未变只改报告时 generation 与节点部署次数不变；旧刷新报告不能覆盖新版本。checksum、YAML 或原子缓存写入失败时旧缓存与列表继续可用。远程 URL 同一过宽输入仍整份失败；缓存解析与分类过滤的测试必跑，真实 release fixture 固定并在 CI 离线运行，不能仅用本机可选环境变量跳过。
16. **未就绪列表**：新建一个从未拉取成功的远程列表并在策略中引用它 → 其他策略照常下发；白名单引用它时不能切到执行（409）。
    - 16a　**空远程内容保护**：有已成功列表时，远程返回空文件、只有注释、空 payload 或全部无法识别的行；预览及刷新均返回 dest_list_empty，旧 entries、摘要、成功报告与 last_fetched_at 保留，generation 不变。首次输入不标就绪、不允许作为有效内容保存。自定义空草稿仍按 P2 第 5 条处理。
17. **超额度**：导入 60 000 条域名 → 保存接口直接返回 400 `dest_policy_over_limit`；绕过接口（直接改远程列表的内容）→ 刷新判为失败、保留旧条目；构造两次各自合法、叠加后超额的并发写入 → **发布被拒**：`published_generation` 不推进，各节点继续执行当前版本，结论条显示「改动没有下发：…超出额度」，没有任何节点进入回退。
    - 17a　**定义与发布事务**：同一毫秒用同一个 updated_at 保存两次，第二次必须冲突；并发创建 priority 不碰撞；排序缺失/重复 ID 拒绝，排序后旧表单冲突。定义写入或快照插入故障时 generation/定义/发布状态全部回滚；读 G 后插入新定义，旧候选不能标成新 G；旧失败晚于新发布完成，不能重写 publish_error。已发布快照缺失/损坏返回存储错误，不 mint 空策略。以上在 SQLite/MySQL/PG 验证。
18. **嗅探不足时名单照常下发**：把一个入站改成 metadataOnly → PSP 同时预检 desired 与 LKG；LKG 同样需要域名嗅探时直接转 empty，界面显示「上一版也无法执行，当前没有执行访问策略」，不反复下发 LKG；停用一个用户，2 分钟内他在该节点无法连接。另用仅端口 LKG 验证回退仍可成功，此时显示「仍在执行上一版」。
19. **内核拒绝**：人为构造一个内核不接受、但能通过校验的策略 → `PolicyStatus.rejected`，界面显示「节点拒绝了新策略」，节点继续跑上一版；点「重新下发」会再试一次。
    - 19a　**不是策略的错不算被拒**：让某个 listener 的配置被核心拒绝（与策略无关），或让 `-test` 超时 → PolicyStatus 不变、不进入回退、白名单组员的 client 不被删除；只出现 `core_convergence_failed`。
    - 19b　**LKG 本身被拒**：节点在回退中，再把 core 换成不接受 LKG 写法的版本 → PSP 把回退目标改为「无策略」，节点在下一个同步周期以无策略状态恢复部署，roster 照常生效（停用一个用户，2 分钟内他在该节点上无法连接）；出现 issue `destination_policy_lkg_rejected`。
    - 19c　**回退期间的修剪与暂停**：节点在 `sniffing` 回退中 → 把某个白名单分组切回「不限」，该组的 deny-all 在这台节点上消失；再点「暂停全部执行」，这台回退中的节点同样停止执行。
    - 19d　**修剪后的候选被拒**：先部署 LKG，再停用一条规则或移除一名主体，让 fallback 的 minted_sha256 与 applied_sha256 不同；令新核心拒绝这个候选 → PSP 仍转 empty，保存 exhausted；重启 PSP、改变 roster 都不重发它，停用用户照常生效。改核心、修嗅探或手动 retry 后可以再试新版。
    - 19e　**回退候选的额度与来源**：构造当前有效豁免更新使 LKG 候选超额 → mint 前转 empty；取消/到期的旧豁免从回退候选移除；空/暂停 applied 不覆盖 LKG；旧摘要报告不清除新版失败；候选状态与 config stream 写入故障一起回滚。界面分别覆盖等待回退、已确认回退、回退耗尽。
20. **重启**：重启 agent 后，第一次上报的状态直接是「已生效」，不经过「下发中」；重启后的命中能正确解析到用户。
21. **暂停**：「暂停全部执行」→ 健康在线节点在下一轮成功部署后不再执行；「恢复执行」→ 原样恢复。另让一台节点离线，确认状态页不把它展示为已停止执行；恢复连接后再逐节点确认。暂停不是全机队同步撤销保证。
22. **B 档能力降级**：把「命中和按网站用量」下发给缺 `audit.usage.v1` 的节点 → 实际下发为 hits；`journalctl` / `docker logs` 中没有 `accepted … email:` 行。

**D. 记录**

23. **非持久边界**：健康同步下命中后重启，记录未确认数据的实际损失；再断网累计多个周期后重启，确认整个内存积压可丢失，文档/UI 不承诺一个周期。另用注入时钟推进超过 48h，确认整批过期而非删行或改 ID 重发；键数与 pending 字节始终有界。
24. **重发去重**：PSP 已入库但响应丢失 → 同类 pending 的 BatchID、Hour、Kind、全部行与计数原样重发，不重复计数；容量、TTL 与预算以内的新事件进入该类下一批。其他类别不会因这个 pending 停止发送。
    - 24a　**去重窗口**：已入库批次丢响应后离线 26h，执行 PSP 清理并重启 PSP；在 48h 接收窗口内重发仍只计一次。覆盖未来 1h 小时桶与 72h 保留边界；超过重试窗口的数据不入库、不消耗新配额。
    - 24b　**线上批次确认**：冻结的 Audit 因总报告过大被 deferred，控制响应仍成功 → pending 保留；本轮发送 trial 只确认该 trial BatchID，不清 block/observe/usage。全空全零不发，仅 Dropped/Unmatched 非零可发计数批次且幂等入库。
25. **入库失败**：在 worker 的第二个事务注入错误 → 第一个事务（去重行与第一块）保留、其余不落库，同步应答早已是 200，指标 `ingest_error` +1；把入库队列塞满 → 新批次计 `queue_full`，同步应答仍是 200。
    - 25a　**坏数据不拖垮同步**：让节点产生一行非法 Audit（测试构建里绕过入口校验）→ 节点照常同步、roster 照常应用，Audit 被丢弃并计数；向 PSP 发 `audit.hits[0].port = 70000` 或 count 为字符串的报告 → 控制响应仍是 200。
    - 25b　**四类隔离**：填满 trial/usage 的聚合器、pending、队列、字节与每小时预算，block 保持在自己的预算内 → block 不因低优先级共享配额被丢弃；四类在七槽调度下都获得发送/写库机会。in-flight 批次不能被 recent LRU 淘汰导致重复投递。
    - 25c　**预算与损失持久化**：超额、首块/后块失败、PSP 重启后预算预留不回退或重置，重复批次不再扣预算；loss 行分别记录行/事件单位，重启后查询历史仍可用，按账号或分组筛选不把面板损失当成准确个人损失；损失缓冲满也不阻塞控制。
26. **webhook 压测**：每秒 1000 个被拦截的连接，持续 5 分钟；另做 50 条带 webhook 的规则同时命中。Xray 的 goroutine 数与文件描述符不持续增长，agent CPU 记录在案。
27. **webhook 鉴权**：本机另一个进程向 socket 发伪造请求（不带或带错令牌）→ 401，不入库。
28. **三方言**：`Example.COM`、`example.com.`、`例子.cn` 的命中各自落到唯一一行；PostgreSQL 上 `q=EXAMPLE` 能搜到。
29. **B 档**：只有开启的节点上有用量；关闭后节点不再发送；用量接口不带 `user_id` 返回 400；每次读取写一条审计行，10 分钟内不重复。
    - 29a　**用量口径**：direct、全局 observe、白名单 trial 三种正常放行连接各贡献一次 Usage；后两种同时有一次对应 Hits，Hits 不重复。deny 无 Usage；A 档 trial 无账号数据，显式 B 档才有逐人用量。过滤器 panic 退出 agent 且原始访问行不转发。
    - 29b　**关闭采集的入库边界**：构造旧配置、未确认 pending、已投递队列和正在入库分块；保存 off 成功后这四条路径不再写。快速 off→开启且 Node 没收到中间 off，再重启 PSP，旧 revision 的 Hits/Usage 仍被拒。B→A 清空旧 revision 全部待发数据、新 A 档 Hits 正常；关闭不删除已有历史。同值保存不递增，普通 Save 不回滚 revision。
    - 29c　**事件版本归属**：revision 更新后送达旧 core 的 webhook 与旧进程 stdout 行，确认不会被贴上新 revision；新的事件正常统计。仅 B 档无执行规则、artifact digest 未变时 revision 改变仍重启并重新绑定 Writer；重启恢复同一个 revision 时不额外重启 core。
30. **保留期**：把命中保留设为 1 天，次日整点后旧行被清理；试运行行按 `dest.trial_retention_days` 清理；删除用户后，它的命中、豁免、同意记录在下一次清理时消失。
    - 30a　**匿名孤儿**：user_id=0 的合法 trial 行在用户孤儿清理后仍存在；分组切回 open 仍保留，删除分组或面板后清除；非法匿名行被清除。loss 与 budget 表按各自口径清理，不能提前删仍有效的去重行。
31. **风险信号**：阈值边界（恰好等于阈值、阈值减一、阈值的一半）；没有采集节点时为「无法判断」；信任不隐藏该信号；证据与标记记录中都没有目的地或 IP；`flagged` 进入「待处理」和铃铛。
32. **运行诊断**：每个新 metric family 都出现在「节点」卡上，中英文说明齐全（`diagnosticsCatalog.test.ts` 通过）。

**E. 白名单**

33. **试运行**：组员访问名单外的网站成功，试运行报告出现该主域名（没有账号信息）；一键加入后，它在报告里消失，下一次下发后变为放行。
34. **试运行即排除**：白名单试运行开始时，组员在 3X-UI 节点上的 client 被删除，订阅里也随之消失。
35. **执行**：名单外网站被拒；名单内网站正常；境外域名解析正常（证明基础放行生效）。
36. **执行保证**：组员在 3X-UI 节点上的 client 已不存在；用旧订阅连接失败。
37. **执行保证 · 离线**：节点离线期间切到白名单，节点恢复后，组员在它上面的 client 被删除（依赖 #268）。
    - 37a　**执行保证 · 另一台面板离线**：组员所在的另一台原生节点离线时，把分组切到白名单 → 3X-UI 上的 client 仍在一个同步周期内被删除（§9.2 第 6 条）。
38. **执行保证 · 读失败**：注入一次 `Eligible` 读库失败 → 白名单成员的 client 一个都不删；恢复后行为正常。
39. **白名单与全局规则**：组员跑 BT 仍被「禁止 BT」拦截；组员访问全局 observe 列表里、但不在白名单里的站点 → **被拒绝**（回归 §3.2）；被豁免的组员不受限制。
40. **数据量压测**：200 个用户 × 每小时 300 个目的地 × 24 小时试运行 → 记录节点试运行聚合器的键数与 `Dropped`、同期拦截命中是否有丢失、SQLite 入库耗时、**入库期间流量轮询循环的写延迟**（P4 第 3 条把入库移出 Sync 的效果）、`dest_hits` 行数与体积、`/dest/hits` 首页 p95、`/dest/policies` 首屏 p95；结果回填 §9.4 的表。另记录 5 万条域名下空闲节点每轮的 CPU 与 `state.db` 写入字节数（§5 N1 第 5 条）。
    - 40a　**混合压力**：block、observe、trial、usage 同时超额，记录各类排队槽数、规范 JSON 字节、实际堆峰值、各分块写库等待、控制 Sync 与流量轮询延迟；确认 256 批/64 MiB 队列上限、四个 pending 上限与配额隔离。解释所有丢失原因，不能只测试平均流量便宣称绝对不丢。
41. **能力变化**：在白名单分组覆盖的面板上新装一台新版原生节点 → 组员在它上面自动获得 client；节点任务能力的变化不触发白名单重同步。
42. **守卫测试**：在 `internal/service` 里新增一处直接调用 `group.Matches` → 测试失败。

**F. 隐私页**

43. 关闭时返回 404；启用并发布后，注册必须勾选；「重大变更」发布后，普通用户登录时弹窗、订阅不中断、staff 不弹；「我们收集什么」随设置实时变化；订阅日志保留为 0 时显示「永久保留」；系统设置保存不会回滚 `consent_version`。

**G. 权限与审计**

44. 运维员访问全部 `/api/admin/dest/*` 返回 403（路由测试 + 实测）；运维员读审计日志时看不到 `/api/admin/dest/*` 的写入行和用量读取行；运维员在导航里看不到访问控制，直接输入 `/admin/access-control` 会被弹回；运维员在分组列表上能看到「白名单」徽章，但看不到名单内容。

**H. 升级降级**

45. **PSP 降级演练**：降级到上一个 PSP 版本 → 健康在线节点成功部署旧 PSP 配置后策略消失并重启 core；离线节点继续旧策略，恢复后才撤去。另验证旧 PSP 成员重同步会重新开放不执行白名单的面板。再升级回来 → 成功部署后恢复，记录各节点实际切换时间与重启次数。结果写进发布说明。

**I. 界面**

46. §7.6 的截图矩阵齐全，已附在 PR 里，所有者已批准；§7.5 全部勾选。

**J. CI**

47. 全部仓库 CI 全绿：
    - **PSP**：`go test ./...` 加三方言（postgres、mysql job）、`gofmt`、`go vet`、staticcheck v0.8.1（`-tags node_reinstall_acceptance`）、`npm run lint`（exhaustive-deps 警告数不增加）、`npm run test`、`npm run build`、`smoke:dist`，以及「node compatibility」与「node contract」两个 job；
    - **Node**：真二进制 acceptance（N8）；
    - **Protocol**：`go test ./...`、apidiff、stdlib-only 门禁，main 上的 consumer toolchains。

---

## 13. 测试文件清单

| 仓库 | 文件 | 覆盖 |
|---|---|---|
| Protocol | `protocol/policy_test.go` | 校验规则全集、ID 正则、规范化字符集、排序去重、`PolicyDigest`、nil Policy 逐字节不变 |
| Protocol | `protocol/policystatus_test.go` | `ValidatePolicyStatus`（含 `MaxPolicyStatusListeners`）、与能力绑定、partialReport 含新字段、不进 Base 校验 |
| Protocol | `protocol/match_test.go` | `MatchDestination` 跑完 `conformance.DestinationMatchVectors`（含 `regexp:` 不锚定的向量） |
| Protocol | `protocol/audit_test.go` | 四种 Kind 与单小时批次；trial 的匿名形状；全空全零非法、仅计数批次合法；行数/大小上限、usage 计数批次能力绑定、partialReport、48h/1h/72h 常量关系 |
| Protocol | `protocol/conformance/destination.go`（新增，导出向量） | 匹配与嗅探向量 |
| Protocol | fuzz：`FuzzValidateDestinationPolicy`、`FuzzValidatePolicyStatus`、`FuzzValidateAuditObservation` | 照 `host_test.go:602` 与 `protocol_test.go:237` 的写法 |
| Node | `internal/core/xray/compiler_test.go`（追加，N0） | `access:"none"` |
| Node | `internal/core/xray/policy_test.go` | N8 列出的 Xray 编译用例、令牌稳定性、`AccessLogFiltered` |
| Node | `internal/core/singbox/policy_test.go` | sing-box 编译用例；N0-S（如果做） |
| Node | `internal/core/sniff_test.go` | 跑完 `conformance.SniffingVectors` |
| Node | `internal/core/runtime/runtime_test.go`（追加） | 只有策略变化时触发部署；空闲短路（含到期集合与只更新 `applied_at_ms`）；PolicyStatus 迁移；「被拒」的三例归因；`RestoreStatus` |
| Node | `internal/core/process/supervisor_test.go`（追加） | `-test` 非 0 退出包 `ErrCandidateRejected`；超时与 exec 失败不包 |
| Node | `internal/agent/auditsink/sink_test.go` | webhook 接收、令牌、丢弃 source、SplitHostPort、逐行校验入口、未知规则或用户、上限、试运行聚合器的折叠与独立上限、`Serve` 出错时返回错误、`runtime/` 目录不存在时也能监听 |
| Node | `internal/agent/auditlog/filter_test.go` | 真实行 fixture、背压、watch/trial 同时贡献命中与用量而 deny 不计用量、故障退出且不泄漏访问行、逐行校验 |
| Node | `internal/agent/report_test.go`（追加） | 分类 pending 与七槽公平性、冻结重发逐字节相同、仅确认 AuditBatchIDSent；deferred 后控制成功不清 pending；仅计数批次、过期、Count 饱和、off/revision 清空与降档后新 Hits；非法数据不阻断 SyncOnce |
| Node | `internal/agent/capability_gate_test.go`、`cmd/node/main_test.go`（追加） | 新能力在静态切片里，不在 CapabilitySource 里；sink 监听失败时不声明 `audit.hits.v1` |
| PSP | `internal/service/destlist/parse_test.go`、`normalize_test.go`、`broad_test.go`、`geosite_test.go`、`geosite_cache_test.go`、`refresh_test.go` | 各种格式、规范化、过宽条目、超限、摘要；分类剔除与持久报告、固定真实 release、属性组合、空结果与原子缓存保护（15b）；来源编辑/删除与慢刷新交错、旧失败迟到不覆盖新状态 |
| PSP | `internal/service/destpolicy/compile_test.go`、`publish_test.go`、`sniff_test.go`、`fallback_test.go` | P3 全部判据：一致读与原子 CAS、CatchAll 占位校验、修剪后摘要不同仍能转 empty、exhausted 持久化与上下文重试、已撤销豁免、候选超额、paused 不覆盖 LKG、Subjects 不振荡 |
| PSP | `internal/adapters/sqlstore/dest_*_repo_test.go` | 三方言定义/generation 原子写、同毫秒版本与 priority/排序并发、一致读、发布和错误 CAS、缺失/损坏快照、注入故障回滚；upsert、2 MiB entries、AuditCollect 保留、72h 去重与 budget/loss 清理；合法 trial 不被用户孤儿清理。PG 的 block 批次中 p12x1/p12x2 映射同一主键须归并；直接向 repo 注入两条同一 trial 主键的行也归并（这是 repo 防御测试，线上重复聚合键仍被拒）。重复批次 RowsAffected=0 且不扣预算；预算与首块原子提交、重启不重置 |
| PSP | `internal/adapters/sqlstore/user_repo_test.go`（追加） | `GroupIDsByIDs` 三方言 |
| PSP | `internal/adapters/sqlstore/node_agent_repo_test.go`（追加） | MintStream 等值路径不读 desired_body；MintConfigWithPolicyCandidate 在一个事务提交 stream 与实际候选，来源变化但 ETag 相同仍更新元数据；故障全回滚 |
| PSP | `internal/service/nodesync/*_test.go`（追加） | ObserveStatus 先于 Compile；分类投递/in-flight 不被 LRU 淘汰；七槽分块公平性与字节上限；26h 后跨清理/重启去重；持久独立预算；off/降档、快速关闭再开启与 revision 拒旧批次、入库屏障交错；loss 的面板口径；能力与回退变化在重同步前失效合格性缓存 |
| PSP | `internal/transport/http/handler/node_sync_audit_test.go` | 影子结构隔离解码；`audit.hits[0].port = 70000`、count 为字符串、PolicyStatus 非法时控制响应仍是 200 |
| PSP | `internal/transport/http/middleware/audit_test.go`（追加） | `TestDestPreviewEndpointsAreNotAudited`（同时断言 `/dest/test` 仍写审计） |
| PSP | `internal/transport/http/access_log_test.go`（追加） | `/api/admin/dest/` 的查询串不进访问日志 |
| PSP | `internal/service/group/eligible_test.go`、`matches_guard_test.go` | §9.2 的判断（含失败语义、缓存、`InvalidateEligibility`、依赖为 nil 时退化）、go/ast 守卫 |
| PSP | `internal/service/user/allowlist_resync_test.go` | 读失败时不删除；试运行开始即排除；另一台原生节点离线时 3X-UI 上的 client 仍被删除；同面板重组时 provision 失败则旧 client 不删；删除失败时 `ResyncMembership` 返回错误 |
| PSP | `internal/service/risk/destblock_test.go` | 六态（永不返回 `exempt`）、代码完备性、证据里没有地址、`no_collector` 按 R17 推导 |
| PSP | `internal/ports/risk_center_policy_test.go`、`risk_center_policy_spa_test.go`、`policy_settings_test.go`（修改） | 48 → 50、可覆盖键数 |
| PSP | `internal/ports/access_control_settings_test.go` | `dest_` 前缀全集 |
| PSP | `internal/transport/http/handler/admin_settings_policy_preserve_test.go`（追加） | 保留 `dest_*` 与 `legal.consent_version`；`TestSettingsPut_NeverReadsDestFieldsFromTheRequest` |
| PSP | `internal/domain/riskpolicy_test.go`（追加） | `RiskPolicyFromSettings` 夹取 `DestBlockThreshold`，0 取 20 |
| PSP | `internal/app/cleanup_test.go`（追加）、`dest_wiring_test.go` | 五个 prune（含合法匿名 trial、分组/面板孤儿、72h 批次与 budget、分类 loss 保留）、候选事务与采集闸门接线守卫 |
| PSP | `internal/transport/http/dest_route_test.go` | admin-only、错误码、`TestDestAuditRowsAreAdminOnly`、用量读取审计 |
| PSP | `internal/transport/http/legal_route_test.go` | 公开接口、回落链、409 |
| PSP | `web-react/src/utils/diagnosticsCatalog.test.ts`（自动覆盖） | 新 family 都已登记 |
| PSP | `web-react/src/views/admin/risk/policy/policyLayout.test.ts`（修改） | 50 键、七张卡 |
| PSP | `web-react/src/i18n/riskKeys.test.ts`、`tunableText.test.ts`（修改目录表） | 新目录的键与可调数值 |
| PSP | `web-react/src/views/admin/accessControl/**/*.test.tsx` | 每屏一份：回退等待/生效/耗尽三种条件、losses 行/事件单位与面板范围、off/revision 与 B 档文案；S1–S14、S18、S20（confirmCopy 与 `needsFirstPublishConfirm` 的四个入口）、`accessParams`/`recordsParams`（URL 里没有目的地；参数文法往返）、`accessVerdict`（含 `publish_error` 与离线）、`accessTone`（三元组两两不同）、`matchSummary`（四例）、页面级抽屉里打开的对话框层级高于抽屉、`accessControlStyle.test.ts` |
| PSP | `web-react/src/components/ToneBadge.contrast.test.ts`、`KpiTile.test.tsx`、`src/hooks/useDirtyClose.test.tsx`（UI-0） | 两种主题下每个 tone 的对比度 ≥ 4.5；可点砖的 `aria-pressed` 与再点撤销；✕/Esc/背景关闭时确认 |
| PSP | `web-react/src/views/admin/risk/evidence/evidence.test.tsx`（修改，2c） | `DetectorStateChip` 改用 ToneBadge 后按 `data-state` 断言；不完整的 clean 不画绿 |
| PSP | `web-react/src/views/admin/NodeIssuesView.test.tsx`、`NodesView.test.tsx`（追加，1c） | `?agent=` 筛选；`?inbound=` 打开入站编辑框后参数被 replace 掉 |
| PSP | `web-react/scripts/smoke-dist.mjs`（`smoke:dist`，追加） | 生产包里没有 `accessControlFixtures` |
| PSP | `web-react/src/views/admin/risk/drawer/RiskUserDrawer.test.tsx`（追加） | S16 |
| PSP | `web-react/src/views/admin/GroupsView.test.tsx`、`ServersView.test.tsx`（追加） | S17、S15；`ServersView` 带 `?q=` 打开时搜索框显示当前筛选 |
| PSP | `web-react/src/views/admin/legal/*.test.tsx`、`views/LegalView.test.tsx`、`components/ConsentDialog.test.tsx` | S21–S23 |
| PSP | `web-react/src/router/home.test.ts`、`prefetch.test.ts`、`utils/permissions.test.ts`、`utils/nodeIssueGroups.test.ts`（追加） | 三道门、新 issue code |
| PSP | `web-react/src/hooks/useDrawerParam.test.tsx`（由 `drawerParam.test.tsx` 迁来并扩充） | 字符串值、互斥；sheet → user（replace）→ Back 回到页面且 sheet 不复活；prefill 在 Back 之后不复活 |

**文档交付**（每个阶段的完成判据都包括这些；PSP 仓库文档用中文）：

| 文档 | 内容 | 阶段 |
|---|---|---|
| PSP `docs/access-control.md`（新建） | 照 `connection-limits.md` §14 的写法：行为、匹配顺序、表结构、接口一览、存储与隐私、升级与降级、指标 | 1c 建立，每个阶段追加 |
| PSP `docs/ARCHITECTURE.md` | destlist、destpolicy 两个服务，新表；在 §3「整体架构」下新增一小节「后台循环」，登记 `dest-list-refresh`（1c）与 `dest-audit-ingest`（2c），写明间隔来源与是否可热改（ARCHITECTURE.md 目前没有循环表，CLAUDE.md 的那张表被 `.gitignore:80` 忽略、不随 PR 提交，由所有者本地自行同步） | 1c、2c |
| PSP `docs/observability.md` | P9 的 family | 1c、2c |
| PSP `docs/connection-limits.md` | §13.x「访问拦截」、§14.3 来源、§14.4 第五个 tab 与第六行检测器、§14.11 改为 50 个键 | 2c |
| PSP `docs/UPGRADE-v4.md` | §11.2 的降级警告 | 1c |
| PSP `docs/psp-node-agent.md` | 新的线上字段与能力 | 1c、2c、4 |
| Protocol README「Provenance」与 godoc | 新类型、非持久语义、harness 分歧 | 1a、2a |
| Node README | 访问日志默认关闭与升级残留、audit.sock/鉴权令牌、四类容量与非持久积压、48h 重试/72h PSP 去重、off/降档语义、B 档含观察/试运行但不含拦截 | 0、2b、4 |
| 三个仓库的发布说明 | 按 §5 N0 与 §11.2 写 | 每次发版 |
