# 访问控制：原生节点的目的地策略

> **范围（第一批）。** 只对 **原生节点（Passwall Node，`kind=psp`）** 生效；3X-UI、S-UI 节点不执行任何策略。
> 本批提供：自定义列表、远程列表、v2fly geosite 分类、内置规则组目录、按分组 / 按节点限定范围的拦截与放行策略、
> 豁免账号、按入站的来源国家 / 来源 IP 段拒绝、测试目的地、发布与回退，以及状态页所需的全部接口。
> 策略状态有 **关闭 / 仅报告 / 开启** 三态，管理员自由选择，没有「必须先仅报告」的门槛。**命中记录**（A 档）随第二批上线：
> 节点上报命中，PSP 入库并在记录接口、策略列表、账号视图和风险信号里使用。**用量（B 档）、白名单分组的接口与界面、sing-box 采集** 在后续批次（白名单分组的数据模型与编译见 §3.2）。
>
> 线上协议见 Passwall-Protocol `v0.4.1`（`protocol/policy.go`、`match.go`、`sniff.go`）；字段说明见
> [psp-node-agent.md](psp-node-agent.md) §11。

## 1. 行为一句话

管理员在 PSP 里定义「哪些目的地不许去」（以及例外）；PSP 把定义展开成一份目的地策略，放进每台原生节点的 **config**
里下发；节点把它编译成 Xray / sing-box 的路由规则。**没有任何策略时，下发给节点的 config 与升级前逐字节相同**，
config ETag 不变，core 不会因为升级面板而重启。

## 2. 概念

| 概念 | 说明 |
|---|---|
| **列表** | 一组匹配条目。`custom`：管理员粘贴的文本（沿用 Clash / Mihomo 行式写法，见 [routing-rule-dsl.md](routing-rule-dsl.md) §4.1）；`remote`：https URL；`geosite`：v2fly `domain-list-community` 的一个分类（可带属性过滤）。`side` 分 `dest`（目的地）与 `source`（来源，只能给来源规则用） |
| **策略** | 一条 `block` 或 `allow`：引用若干列表，外加自带的内置匹配（CIDR、端口、网络、协议 `bittorrent`、私有地址）。有 `state`（`off`/`report`/`on`；**放行策略只有 `off`/`on`**，对它的 `report` 请求仍是 400 `dest_policy_state_unavailable`）与 `scope`（`all`/`groups`/`panels`）。同一动作内的顺序由服务端的 `priority` 决定，只能用 `PUT /policies/order` 调整 |
| **豁免** | 让一个账号不受拦截策略约束，可带到期时间。**不豁免来源规则和全局放行之前的步骤**（见 §3） |
| **来源规则** | 按入站（节点）拒绝来源国家或来源 IP 段，只对原生节点，状态 `off`/`report`/`on`。它回答「这个入站对谁开放」，不是「用户能去哪」，所以排在最前，豁免也绕不过 |
| **规则组目录** | 随 PSP 发版的内置映射：把「BT 下载」「金融」「广告与追踪」等映射到 geosite 分类、协议、端口或私有地址。所有内置规则组都不默认启用；添加时管理员自选状态；名称、说明、状态、范围可改，**匹配内容只读**（要改就复制成自定义策略）。**没有通用的「教育」「支付」「博彩」分类**，目录里把它们列为 `unavailable` 并写明原因，不会创建任何东西 |

## 3. 匹配顺序

编译器严格照下表输出，协议校验要求规则按阶段非递减出现（Protocol `v0.3.0` 线上规格 §2、§11）：

| 阶段 | 规则 | 说明 |
|---|---|---|
| 0 | 来源规则（`s<节点号>`，`block`，状态 `on`） | 按入站与来源 CIDR。同一国家 + CIDR 集合**且状态相同**的入站合成一条，ID 取该组在本节点内的最小入站号 |
| 1 | 放行策略（`p<id>`，`allow`，按 priority） | 先于豁免：全局放行对所有人生效 |
| 2 | 豁免（`Exempt` 字段，不是规则） | 命中即放行 |
| 2.5 | 节点内置：尾随点域名拦截 | 策略里**有拦截规则带域名**时，节点自己加一条规则拦截以 `.` 结尾的主机名（Xray：`regexp:\.$` → blocked，ruleTag `psp-trailing-dot`）。PSP 不下发它 |
| 3 | 拦截策略（`p<id>`，`block`，按 priority） | |
| 4 | 白名单分组（`g<gid>x1`、`g<gid>x2`、`g<gid>`；状态 `report` / `on`） | 见 §3.2。排在拦截策略之后、仅报告之前 |
| 5 | 仅报告（`observe`，`p*` 与 `s*`；先来源规则，再策略） | 状态为 `report` 的拦截策略与来源规则。**永远不改变判定**：它排在所有开启的规则之后，只会命中「本来就会放行」的连接 |
| — | 无命中 | 放行（核心默认出站） |

要点：

- 一条规则内各字段是「与」；`Domains`、`CIDRs`、`Private` 是「或」的三选一。一条策略同时有协议（`bittorrent`）和
  域名 / CIDR / 私有地址时，拆成 `p<id>x1`（域名、CIDR、私有地址部分）和 `p<id>x2`（协议部分），端口与网络两边各带一份。
- 「测试目的地」用同一张表逐步给出结果：`hit`、`miss`、`untestable`（输入没给的字段，如来源地址）、`shadowed`、`skipped`。

### 3.1 仅报告（report）

- `report` 状态的拦截策略编译成线上动作 `observe`，放在阶段 5（`psp-watch-<id>`）；来源规则同理（`s<n>` + `observe`）。命中后**放行并记录**，且不终止之后的评估，因为它之后没有开启的规则。
  如果放在拦截段，命中它的连接会就此终止匹配，排在后面的开启拦截就不再生效，等于悄悄放宽了限制；放在阶段 5，加上或去掉一条仅报告的策略，**任何连接的判定都不变**（`TestReport_NeverChangesAVerdict` 用 `MatchDestination` 逐条验证）。
- 代价：已经被前面「开启」的规则拦掉、或被「放行」放过的连接，不会出现在仅报告的统计里。这是对的：它们的结果与这条策略无关。
- `observe` 行在记录里表示「**本应拦截**」：连接被放行了。它不计入风险信号 `dest_block`（只数真正的 `block`）。
- 仅报告的策略与开启的策略一样，带域名或协议时需要入站嗅探，在嗅探不足的入站上同样按第 4.2 节第 7 条限定入站；回退到 LKG 时，状态改变了的规则（开启 ↔ 仅报告）视为不同的规则，被修剪掉（只放宽、不收紧）。

### 3.2 白名单分组（编译，数据模型、编译与成员同步已就绪，接口与界面在后续批次）

- 状态存在分组自己的行上（`groups_` 表的可空列，见 §5），`off` / `report` / `on`，NULL 等于 `off`；没有任何分组设置时，快照字节、下发给节点的策略字节与升级前逐字节相同（`TestCompile_NoGroupsIsByteIdentical`）。
- 每个面板上，状态不是 `off` 且本面板 roster 里有组员 M 的分组，按分组号升序输出三条规则，Subjects 都是 M：`g<gid>x1`（allow：`list_ids` 里已就绪列表的域名与 CIDR）、`g<gid>x2`（allow：基础放行列表加补充列表）、`g<gid>`（catch-all：`on` 为 block，`report` 为 observe）。x1 或 x2 没有内容就不输出；M 为空整组不输出。未就绪的列表不贡献内容。
- **只给声明了 `policy.destination.groups.v1` 的节点输出**（旧的 sing-box 节点会让「全局 BT 拦截」被分组放行绕过）。能力的增减是新的策略（编译键含它）。
- `report` 永不改变判定：x1、x2 只放行本来就放行的连接，catch-all 只记录（`TestReport_GroupReportNeverChangesAVerdict`）。代价（A10）：组员的流量命中分组规则后终止，不会再落到阶段 5 的仅报告策略上，那部分的记录会偏少。
- 一个组员在三条规则里各占一个 subject，所以每台节点每个分组约 3×成员数；`MaxPolicySubjects = 50 000`，单面板白名单组员上限约 16 000，超出走 `over_limit`。每节点超额按「该面板 tags 选中的组员数 × 该组规则条数」计，不按 roster 计，否则组员因不合格被移出 roster 后会在额度内外振荡。
- 上界（发布前校验）用每组一个合成 subject 把所有分组的规则与列表算进去；超额则发布被拒（`publish_error`）。
- 仅报告的剪枝（`shedObserve` / `enforcedOnly`）把分组的规则当执行类，**永不剪掉**：只剪掉 catch-all 会留下没有 catch-all 的 member 规则，整份无效。试运行组需要的嗅探某入站不足时，该组在本节点按组整体去掉（见 §4.2 第 7 条），不再走回退。
- 记录 LKG 时 `applied_groups` 写入 body 里出现 catch-all 的分组号；状态页 `allowlist_groups` 是 `[{id,name}]`。

### 3.3 白名单分组的成员同步（R5：不能执行的节点上不给组员开账号）

白名单只在「组员认证所在的那台原生节点、且策略已生效」时成立。3X-UI / S-UI、旧版节点、超额度、被拒或嗅探不足的节点都做不到，所以**只在订阅里隐藏不够**：组员在那些面板上的 client 还在，旧订阅照样能连。成员同步这一层必须排除它们。

- **一个判断，全仓共用**：`group.Service.Eligible(ctx, node, group)`。状态 `off`（含 NULL）等同今天的 `Matches(node, tag_filter)`，行为与升级前完全相同。状态 `report` 或 `on`（两个都适用）还要同时满足：面板是原生（`psp`）；agent 本次上报的能力同时含 `policy.destination.v1` 与 `policy.destination.groups.v1`；`dest_agent_policy.fallback_reason` 不是 `rejected` 或 `over_limit`；引擎是 Xray 时，本面板**已启用的入站**的嗅探能满足域名规则（`protocol.CheckXraySniffing`）。需求恒按「域名」判，不去读分组的列表：白名单本来就是域名白名单（基础放行里是 DoH 主机名），且这样合格性只取决于面板、不取决于名单内容。能执行但不记录的节点（sing-box）算合格。
- **嗅探直接在入站上判，不看 `fallback_reason=sniffing`**：后者依赖 roster，而把组员移出 roster 会让它消失，于是「不合格 → 移出 → 规则没了 → 预检通过 → 合格 → 加回 → 预检失败」会无限振荡。`rejected` 只在 generation 变化时重试，不会振荡，但每次重新发布可能有一轮「加回 → 被拒 → 删除」。
- **失败语义**：任何一次读失败都返回 error，绝不当作 true 或 false。`NodesFor` 原样返回（`/sub` 失败，客户端保留上次订阅）；三处「添加」方向的调用点（`node.go` 的新节点入组与重建入站、`reconcile.go` 的缺失 client 修复）出错就**跳过该分组**，只打一条带 `group_id` 的 Warn，下一轮或 heal 会补上。
- **缓存**：按面板的合格性缓存，TTL 30 秒；`NodesFor` 对 `TagFilter.All` 的捷径之后也过滤。缓存命中时 `/sub` 热路径不额外查库；未命中时每个面板读一次面板行、agent 行和策略状态行。
- **触发重新同步**（先失效缓存，再对每个状态不为 `off` 的分组调 `ResyncGroupMembersInBackground`，顺序不能反）：agent 上报的两个能力出现或消失（只看这两个，任务能力会逐次变化）；`fallback_reason` 进入或离开 `rejected` / `over_limit`；psp 面板的入站嗅探、启用状态变化，以及入站的新建、导入、删除、分离；分组保存。缓存的组员数在有人加入、离开分组或被删除时失效。没有接线时只失效缓存，由 heal 兜底。
- **因白名单被排除的面板上，删除不依赖其他面板**：`clientprov` 把退役的 client 分成「用户在该面板上已没有任何节点」与「同面板内被另一个 client 取代」。只有**面板因白名单不合格而被排除**（分组的 tags 仍选中该面板的节点，分组是白名单，面板却不能执行：`group.Service.AllowlistExcludedPanels`）时，`ResyncMembership` 才不看 provision 与生命周期是否成功就删，失败（面板离线、拒绝）并入返回的错误，由 `user_resync` 任务和 heal 重试。其余情况，包括分组的 tags 改去选别的面板，仍是先建后拆：等替代者上线才删，否则目标面板离线时用户会断网；这些删除失败只记日志，不使重同步失败，否则一行留在长期离线或已删除面板上的空 client 会让该用户每次重同步、每次 heal 都报错。面板已不在池里（已删除）或面板上已没有该 client（「not found」，只认 3X-UI 的几种固定措辞）都算完成。
- 每节点超额按 `TagMatchedMembers`（该面板 tags 选中的组员数，不论是否合格）计，由 `group.Service` 实现，见 §3.2。对**每个**状态不为 `off` 且 tags 选中该面板的分组都计，不要求 roster 里有该组的人：过额使面板不合格、组员全部离开 roster 后，额度不能因此看起来够用而把他们放回来；roster 里没有任何组员但按这个数会超额时，照样走 `over_limit`。
- **守卫**：`TestNoGroupMatchesOutsideGroupPackage` 扫描 `internal/service`、`internal/transport`、`internal/app`，`group` 包以外不得调用 `group.Matches`。

## 4. 发布、编译与回退

### 4.0 记录档位（`Policy.Collect`）

- 面板（原生服务器）有 `audit_collect`：`off` / `hits`（默认）/ `hits_and_usage`，只能通过服务器编辑接口（`PUT /servers/:id`）改，整行保存不会碰它。
- 下发给节点的有效档位是**面板档位与节点能力中较低的一个**：`hits` 需要节点声明 `audit.hits.v1`；`hits_and_usage`（B 档）尚未提供，没有节点声明 `audit.usage.v1`，按 `hits` 处理（`usageTierAvailable`）。
- **只在需要的地方编译采集**：有效档位非空，**并且**策略里至少有一条 `block` 或 `observe` 规则时才写 `Collect`，让节点给这些规则挂 webhook。没有规则可记录（只有放行策略）、面板关闭记录、节点没有能力时都不写：写了会改变摘要、让 core 白白重启一次。档位变化是新策略（摘要变），不变则 ETag 不动。回退（LKG）时的档位跟随面板**当前**的选择，但**只降不升**：取 LKG 铸出时的档位与面板当前档位中较低的一个。升到比 LKG 更高的档位会铸出节点从没应用过的字节（另一个摘要）；节点若连它也拒绝，LKG 就被清空、节点停止执行。
- 实际下发的档位写进 `dest_agent_policy.collect_effective`。「**在记录**」（`collecting`）由 PSP 推导，不靠节点上报（安静的节点不发 audit，区分不了「在记录但没命中」和「没在记录」）：节点是 Xray、声明了 `audit.hits.v1`、`collect_effective` 非空，且最近一次 `PolicyStatus` 是对**当前铸出的摘要**的 `applied`。sing-box 节点只执行、不记录。 节点最近应用的那一版的档位记在 `dest_agent_policy.applied_collect`，风险信号靠它在发布与节点应用之间不抖动（见 `/policies` 一行）。

### 4.1 发布（去抖）

- 任何定义写入（策略、列表内容变化、豁免、来源规则、节点删除带走来源规则）都在**同一个事务**里让 `generation + 1`。
- 每台节点每次同步时，`Compile` 先检查是否该发布：有未发布的改动，并且
  - 距最近一次写入已满 `dest_policy_apply_min_seconds`（默认 60，范围 30–3600）——**尾随去抖**；或
  - 距最早一次未发布的写入已满该值的 **5 倍**——**最长等待**，连续修改不至于永远不发布；或
  - 管理员点「立即下发」（`POST /dest/publish`）；或
  - 紧急开关变化（`PUT /dest/pause`），此时一并发布等待中的改动。
- 发布读快照的顺序是**先读 generation，再读全部定义**，在 PostgreSQL / MySQL 上放进同一个 `REPEATABLE READ` 只读事务；
  完成发布用比较并交换（CAS）。两次读之间提交的写入只会多引起一次无害的重复发布，不会丢。
- **发布前校验**：用快照编译一份「不含 Subjects 与 Exempt 的上界策略」，调用 `protocol.ValidateDestinationPolicy`。
  规则数、域名、正则、CIDR、序列化大小对每台节点都一样，所以在这里一次判定。超额或无效：**不推进** `published_generation`，
  各节点继续执行当前版本，`dest_policy_state.publish_error` 写入 `{kind,used,limit}` 或 `{kind:"invalid",field}`，
  由 `/dest/status` 返回。同一 generation 不会被每次同步重复尝试，新写入或手动发布才会再试。
- 保存策略 / 来源规则的 API 在写入前用同一套额度检查（400 `dest_policy_over_limit`）。
- 成员、分组、roster 的变化**不进快照**，所以不经过去抖：新用户加入分组，下一轮同步就进入该分组策略的 Subjects。
- 到期的豁免由 `dest-list-refresh` 循环每次醒来时清理并让 generation +1；发布时已经把已过期的豁免排除在外，所以豁免最迟在
  一个循环间隔（至多 1 小时）加一个去抖窗口之后失效，行在清理前仍显示为「已过期」。

### 4.2 每台节点的编译

`Compile(panel, agent, 本次上报的能力, 期望快照, config)`，顺序是：

1. 没有 `policy.destination.v1` 能力 → 不带 Policy（nil）。用**本次上报**的能力，不用 agent 行上的旧值。
2. `paused` → nil，且不改写最近已知良好策略（LKG）。
3. 已发布的快照里没有启用的策略也没有来源规则 → nil。
4. 否则展开：`Subjects`（`scope=groups` 时是本节点 roster 里属于这些分组的账号；为空则整条规则不下发）、`panels` 范围只给列出的面板、
   `Exempt` 只保留本节点 roster 里有 client 的账号（所以给一个不在本节点的账号加豁免，本节点 config 不变，不会让全机队重启）。
5. **未就绪的列表或国家，跳过规则中对应的那部分匹配；匹配全空的规则整条跳过。** 一个例外：如果规则本来依赖列表而列表都不可用，
   端口、网络这些「收窄条件」**不会**单独顶上去（否则「某列表 + 端口 25」会变成「所有人的 25 端口」）。没有地址侧的策略（如发信端口）不受影响。
6. 所有切片排序去重，`Rules` 规范成非 nil，`ValidateDestinationPolicy` 通过后才下发。
7. **按入站限定需要嗅探的规则**（仅 Xray；`sniffrestrict.go`）：「给每台节点它能执行的部分」。用 `protocol.CheckXraySniffing` 逐个检查启用的入站，
   **按规则自己的需要**（域名规则要 `destOverride` 含 http/tls/quic、无排除项、`routeOnly` 关；BT 协议规则只要不是 `metadataOnly`）。嗅探关闭可以
   （节点会注入 `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":false}`）；开着但配置得无法提供所需信息（`metadataOnly`、`routeOnly`、
   缺 `destOverride`、有排除项、JSON 损坏）的入站是「嗅探不足」。对这台节点：
   - 带域名或协议（含 BT）的规则，`Inbounds` 限定为（规则原有的入站，或本节点全部启用入站）∩ 能提供所需嗅探的入站；交集为空则这条规则不下发。
     仅报告规则同样处理。端口、IP 段、来源国家/网段、网络、私网等**不需要嗅探的规则不变，仍在所有入站生效**。
   - 一条规则同时含域名和地址（列表里既有域名又有 IP 段）时拆成两条：地址那一半不变，域名那一半（取同一策略下一个空闲的 `x<k>` 后缀）限定入站。
   - 组规则（`g*`）不能带 `Inbounds`：某个分组（按组 id）里只要有规则需要嗅探而某入站提供不了，就把本节点上**该分组**的全部规则去掉（该白名单不在这台节点执行，只放宽）；其他分组不动。
   - 结果会再按节点自己的逐入站检查复核一遍（断言，不过则视为面板缺陷，走「回退」）。引擎按 agent 的**期望内核**（与 Compile 用的 config 一致）判定，sing-box 不限定（它自己嗅探）。
   - **不是只放宽，有两处收紧（已知、接受）。** ① 开启的**放行**规则（阶段 1）如果带域名，也按上面限定：在嗅探不足的入站上它不生效，排在后面、不需要嗅探的拦截（端口、IP 段、私网、来源）就会拦下本来被它放行的连接（例如放行 `smtp.corp` + 拦 25 端口：嗅探够用的入站放行，嗅探不足的入站拦截）。状态里在有这样的放行规则时，对被标注的入站多显示一行说明（`sniffing_allow_exempt_lost`）。② Node 的「尾点」保护（拦截所有以 `.` 结尾的主机名，只要策略里还有带域名的拦截规则就生成）对所有入站生效，包括嗅探不足的入站。
   - **丢哪类规则取决于原因：** 只有 `metadata_only`（或嗅探 JSON 损坏）会让协议规则（BT）也不生效；`route_only`、`dest_override_missing:*`、`domains_excluded`、`ips_excluded` 只让域名规则不生效，BT 照常。状态按入站列出原因。
   - 限定会给规则加上显式 `Inbounds`，占用入站额度；若原本在额度内、限定后超出，回退并记 `destination_policy_over_limit`，detail 注明是限定所致（`… after restricting rules to the inbounds that can sniff`）。回退用的 LKG 限定后同样复核校验与额度，任一不过则发 nil。
   这**不是回退**：不查 LKG，铸出的就是限定后的期望策略，节点报告 `applied` 后照常记为 LKG。状态里仍列出嗅探不足的入站（`sniffing_insufficient`，按每个入站上适用规则的需要判定，逐入站给原因），
   并记 PSP 侧 issue `destination_policy_sniffing_insufficient`（每个入站一条），页面据此标注意；节点状态仍是 `applied`。所有入站都不足时，域名与协议规则整条去掉，其余照常下发（不再是 nil）。
8. **每节点超额**：该节点的 Subjects + Exempt 与序列化大小超限 → 回退，issue `destination_policy_over_limit`。**绝不截断**：截断一份拦截名单等于悄悄放行。
9. **仅报告不干扰执行**：仅报告（`observe`）规则不改变判定，所以也不能间接改变判定。嗅探不足的入站上，它们按第 7 条限定入站（或在没有可用入站时去掉）；若只有 `observe` 规则让额度（超额）过不了，就把这些 `observe` 规则从铸出的策略里去掉，保留开启的规则，节点照常执行。任一情形都在状态里记 issue `destination_policy_report_only_not_deployed`（`report-only rules not deployed on this node: sniffing` 或 `… : limit`），`report_only_not_deployed` 为 `"sniffing"` 或 `"limit"`。只剩 `observe` 规则且都去掉时本节点没有策略。开启的规则自己过不了额度，仍然是回退（第 8 条）。回退用的 LKG 按第 7 条同样限定，所以回退也不会因为嗅探不足而整份发 nil。

### 4.3 回退到最近已知良好策略（LKG）

- **记录**：节点上报 `PolicyStatus{state:applied}` 且 `digest == PSP 铸出的摘要` 时，把这一版写入 `dest_agent_policy.applied_*`。
  `paused` 期间不记录。
- **被拒**：节点上报 `rejected` 且摘要等于铸出的摘要时，置 `fallback_reason=rejected`、记下当时的 `published_generation` 与三个投递能力，
  并产生 issue `destination_policy_rejected`。节点自己报「嗅探不足」而拒绝时（面板的预检与节点的检查不一致），状态页显示 `sniffing` 并列出入站；面板自己判定的嗅探不足不是拒绝，见第 4.2 节第 7 条。
- **回退内容** 是修剪过的 LKG，修剪**只放宽、不收紧**：已删除、已停用、范围已不含本面板的策略的规则去掉；来源规则只保留仍有来源规则的入站；
  动作已改变的策略的规则去掉；Subjects 只保留当前 roster 里的账号，对分组范围的策略再限于该分组的**当前**成员（原来对所有人的规则收窄到成员），空了的规则去掉；Exempt 取 LKG 与当前豁免的并集（都限于 roster）；分组规则只保留「节点声明了分组能力、该分组仍在生效且状态与 LKG 相同（catch-all 的线上动作相同：开启 ↔ 仅报告视为不同规则）」的，Subjects 取 LKG ∩ 当前组员 ∩ roster，空了整组删掉，所以修剪后每个分组仍满足「member 与 catch-all 的 subjects 相同」。LKG 是展开后的明文，名单条目的增删修剪不了（与策略相同）。没有 LKG 或修剪后为空 → nil。**修剪处理不了「列表条目被删」**：LKG 是展开后的明文条目，策略仍在、只是列表里去掉了几条时，回退版仍会继续拦截已被删除的条目，直到新版部署成功。读取 LKG 失败（数据库瞬时错误）时本次同步失败，不会把「无策略」当结果下发或缓存；**连续失败 3 次（或自第一次失败起满 5 分钟）后**回退为 nil（总能部署，roster 照常流动），记 issue `destination_policy_lkg_unreadable`，该结果不缓存，库恢复后下一次同步会重新读取。
- **被拒的是回退本身**（回退用的是修剪后的 LKG，其摘要不同于记录的 `applied_sha256`；或换了 core 版本、LKG 里有新核心不再接受的写法）：只要被拒摘要等于「作为回退铸出」的摘要（铸出的与期望的不同；包括超额时铸出的回退），就清空 LKG，回退目标为 nil，issue `destination_policy_lkg_rejected`，不再重铸。
  否则 PSP 会继续铸同一份被拒的 LKG，整份 config 永远部署不下去、roster 冻结。
- 回退版本也按第 4.2 节第 7 条限定需要嗅探的规则（LKG、修剪后的 LKG 都一样），而不是在嗅探不足时发 nil：嗅探不足不会让节点失去其余规则的执行。被拒的上下文还包括节点上报的 core 版本。
- **被拒记录只在被拒的正是期望策略时才建立**；回退被拒只清 LKG，不会拖住期望策略，期望策略照常下发。
- **被拒记录**独立保存（`rejected_sha256` 加 `rejected_generation`），不受超额回退、无策略、暂停的影响；`rejected_generation` 是被拒策略**被铸出时**的 generation（`minted_generation`），不是上报到达时已发布的那一个，所以在上报到达前又发布了新一版，新一版仍会被尝试。
- **何时重试新版**：被拒时的**上下文**（引擎、core 版本、各入站的嗅探配置的哈希，`rejected_ctx`）变化；`published_generation` 变化且期望摘要与被拒摘要不同（只是 generation 变化而字节相同则不重试，只推进记录的 generation）；`policy.destination.v1` / `audit.hits.v1` / `audit.usage.v1` 三项中任一项增减；
  管理员点「重新下发」（`POST /dest/agents/:id/retry`）。成员变化不触发，避免每次 roster 变动都重走一遍「被拒 → 回退」。
- **何时清除回退**：重试的新版被报告 `applied` 且摘要等于期望摘要；回到额度以内。

### 4.4 缓存与空闲开销

- Compile 的缓存键是 `(已发布 generation, 能力, 引擎, 各入站嗅探与启用状态, roster 与分组归属, 回退状态, LKG 摘要)`。命中缓存时只读一行发布状态，
  不查别的表、不重新序列化。
- 带策略的 ConfigBody 的规范字节另外缓存（键是「去掉 Policy 的 config 摘要」加策略摘要），不变的策略不会移动 ETag。
- `MintStream` 的等值路径只读 `id / desired_version / desired_etag`，不再整行读 `desired_body`（它可能是数 MiB）。

## 5. 表结构

| 表 | 作用 |
|---|---|
| `dest_lists` | 列表。`entries`（BLOB/BYTEA/LONGBLOB）是规范条目，每行一条、排序去重；`source_text` 是管理员粘贴的原文；`content_sha256` 是条目摘要（摘要不变不推进 generation） |
| `dest_policies` | 策略：`action`、`state`（`off`/`report`/`on`）、`list_ids`、`inline`（内置匹配 JSON）、`scope`、`group_ids`、`panel_ids`、`priority`、`template_key`、`counts_as_risk`（默认 false） |
| `dest_exemptions` | 豁免：`user_id` 主键、`reason`、`created_by`、`expires_at` |
| `dest_source_rules` | 每入站一行：`node_id` 主键、`countries`、`cidrs`、`state`（`off`/`report`/`on`） |
| `dest_hits` | 命中的小时聚合。主键 `(hour_ms, panel_id, user_id, source, action, dest, port)`，`count`、`first_ms`、`last_ms`（毫秒，bigint：MySQL 的 datetime 没有毫秒，SQLite 把时间存成文本，MIN/MAX 会成字符串比较）。`source` 是 `p<策略号>`、`s<入站号>` 或 `g<分组号>`（`x<k>` 后缀已去掉）；`action` 是 `block` 或 `observe`（本应拦截）。白名单试运行行 `user_id = 0`、`port = 0`。索引 `(user_id, hour_ms)`、`(source, hour_ms)`、`(panel_id, hour_ms)`。**不存来源 IP** |
| `dest_audit_batches` | 主键 `(agent_id, batch_id)`、`received_at`：BatchID 去重，保留 72 小时（接受的历史窗口 48 小时加 24 小时余量：标记在节点还可能重发的期间内必须在） |
| `dest_audit_drops` | 主键 `(hour_ms, panel_id, kind)`、`count`：上报了但没有变成命中行的数量（节点聚合表满、超预算、队列满、写入失败），让「这段时间有 N 条未统计」说得出口。`kind`：`node_hits`、`node_trial`、`over_budget`、`queue_full`、`ingest_error` |
| `xui_panels.audit_collect` | `varchar(16) NOT NULL DEFAULT 'hits'`，只对 `kind=psp` 有意义；整行 `Save` 不写它（更新分支 `Omit`），只有 `UpdateNativeMetadata` 写 |
| `dest_policy_state` | 单行（id 1）：`generation`、`published_generation`、`first_unpublished_at`、`last_write_at`、`paused`、`geoip_sha256`、`geosite_release`、`publish_error` |
| `dest_policy_snapshots` | 已发布快照的规范序列化，只保留最新一份 |
| `groups_`（白名单列） | `dest_allowlist_state`（varchar(16)，NULL = off）、`dest_allowlist_list_ids`（text JSON）、`dest_allowlist_base_list_id`、`dest_allowlist_extra_list_id`、`dest_allowlist_state_changed_at`、`dest_allowlist_updated_at`（都可空，升级后为 NULL，text 列无 DEFAULT）。只有 dest 一侧的写入（`DestGroupAllowlistRepo`）改这几列，分组编辑不碰；写入带 `updated_at` 前置条件，并与 generation +1 同一事务。分组删除在同一事务里带走它自带的列表（`dest_lists.owner_group_id`），并在它有白名单或自带列表时推进 generation |
| `dest_agent_policy` | 每个 agent 一行：期望 / 铸出 / 上报的摘要、`fallback_reason`、`rejected_generation`、`rejected_caps`、`over_limit`、`precheck_listeners`、LKG（`applied_sha256`、`applied_body`…） |

三方言（SQLite、PostgreSQL、MySQL）都有测试；大列一律 `[]byte`，JSON 小列用 `text`，没有 `text` 列带 `DEFAULT`。
节点删除时同一事务带走它的 `dest_source_rules`；账号删除时带走它的 `dest_exemptions`；原生面板删除时带走它的 `dest_agent_policy`。

命中的 upsert 三方言各有写法：PostgreSQL / MySQL 用 `LEAST` / `GREATEST`，SQLite 用两参数的 `MIN` / `MAX`（`destHitConflict`，照 `service/rollup` 的 `onConflictClause`）；
PostgreSQL 的多行 `ON CONFLICT DO UPDATE` 遇到同一语句内的重复键会整句失败，所以写入前先按最终主键在内存里归并（`p12x1` 与 `p12x2` 都映射到 `p12`）。

## 6. 接口

全部在 `adminGroup`（仅管理员）下，前缀 `/api/admin/dest`。时间一律 UTC 毫秒。400 是 `{error, code, field?, bad?}`。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET / PUT | `/settings` | `dest_policy_apply_min_seconds`、`dest_list_refresh_hours`、`dest_rules_update_mode`、`dest_hit_retention_days`、`dest_trial_retention_days`；只提交改过的键 |
| GET | `/lists` | `{items:[…], refresh_hours, budget}`；`state` 为 `ready`/`refreshing`/`failed`/`pending` |
| POST | `/lists/preview` | 只读：解析报告与前 50 条规范条目（远程列表会真的拉取一次）。**不写库、不写审计** |
| POST / PUT / DELETE | `/lists[/:id]` | PUT 带 `updated_at`，冲突 409 `dest_list_stale`；删除被引用的列表 409 `dest_list_in_use` 带 `used_by` |
| GET | `/lists/:id` | 统计 + 前 200 条样本；`?text=1` 另返回自定义列表的 `source_text` |
| POST | `/lists/:id/entries` | `{add:[…], remove?:[…]}`，只对自定义列表，一个事务、generation 只 +1 |
| POST | `/lists/:id/refresh` | 202；仅远程列表 |
| GET | `/geosite/status` `/geosite/categories` | 规则数据版本面板；无数据时分类 503 `dest_geosite_unavailable` |
| POST | `/geosite/update[?dry_run=1]` `/geosite/rollback[?dry_run=1]` | dry-run 返回每分类差异与判定；被拒 409 `dest_geosite_update_rejected`；忙 409 `dest_geosite_busy` |
| GET / POST | `/catalog`、`/catalog/:key/instantiate` | 目录；实例化 `{state:"off"|"on", locale?}`；不可用的组 400 `dest_template_unavailable`；已添加 409 `dest_template_exists`，名称与现有策略重复 409 `dest_name_taken` |
| GET / POST / PUT / DELETE | `/policies[/:id]`、`/policies/preview`、`/policies/order` | 额度形状 `budget:{rules,domains,regexps,cidrs,source_cidrs,subjects,bytes}`，各为 `{used,limit}`。`order` 的 `ids` 必须恰好等于该动作当前的全部策略，否则 409 `dest_policy_order_stale`。预览**不写库、不写审计**，并返回 `{budget, policy}`（`policy` 是按保存同样的规则校验、规范化后的假设结果）。**`counts_as_risk`**（策略 DTO 与 POST / PUT / 预览请求都带，布尔）：这条策略**被执行的拦截**计入风险信号 `dest_block`。只有 `block` 策略能设为 true，新建 `allow` 策略、或保持为 `allow` 的策略带 `true` 是 400 `dest_policy_invalid`，`field:"counts_as_risk"`；`block` 在同一请求里改成 `allow`（客户端把整个 DTO 原样传回）则静默清为 false，不报错；仅报告（`report`）的 `block` 策略可以存着它，但仅报告的行（`observe`）永远不计，它在转为 `on` 之后才开始计数。请求里省略它：POST 为 false，PUT 保持原值（所以不认识这个字段的老客户端不会无意清掉它）；只改 `counts_as_risk` 的 PUT 不推进 generation（编译器不读它，不会触发重新发布）。目录模板的默认值：`bittorrent` 与 `smtp` 为 true（命中就是这个账号在做这件事），其余（分类列表、`private` 内网匹配，节点本来就会拦这类地址）为 false；模板对象带 `counts_as_risk`，实例化出的策略取该默认值，之后可改 |
| GET / POST / PUT / DELETE | `/exemptions[/:user_id]` | 重复添加 409 `dest_exemption_exists`；响应带 `created_by_upn`、`expired` |
| GET | `/source-rules`、`/source-rules/:node_id` | 全部 / 单个（无规则返回 `state:"off"`）；带 `geoip_available` |
| PUT | `/source-rules/:node_id` | `{countries, cidrs, state, updated_at?}`；非原生节点 409 `dest_source_rule_not_native`；冲突 409 `dest_source_rule_stale` |
| POST | `/exceptions` | 全局例外：`{target, match:"site"|"host", scope:"global"}`；第一次使用时在同一事务里建「放行例外」列表和排在放行第一位的 allow 策略 |
| POST | `/test` | `{target, port?, network?, user_id?, panel_id?, inbound_node_id?, source_ip?}`。**写审计**（谁替谁测试过什么）。永不做 DNS。`steps[].kind` 取 `source`、`allow`、`block`、`exempt`、`builtin`；每个 step 另带 `report_only: bool`：**规则处于 `report`（仅报告）状态时为 true，策略规则与来源规则都一样**，此时 `kind` 仍是规则本身的种类（`block` 或 `source`），并带 `policy_id` / `node_id` 与 `name`。命中 `report_only` 的 step 不拦截：它**会**终止求值，`verdict` 为 `observe`，`terminating_step` 是该规则的 id（界面应显示「仅记录，本应拦截」，不要显示「被拦截」）。取舍：用独立的布尔而不是新的 `kind`，是因为界面既要知道这是哪一种规则，也要知道它只是报告|
| GET | `/users/:id` | 账号的分组与豁免，加 `hits_available`（该账号有 client 的节点中至少一台在记录）与 `recent_hits:{days, items:[{source, source_name, action, count, top_dests(≤3), panels, last_at}]}`；用量部分仍为 null |
| GET | `/hits` | 命中记录，分页。筛选 `user_id`、`panel_id`、`source`、`source_kind`（`policy`/`source`/`group`）、`action`（`block`/`observe`）、`since`、`until`（UTC 毫秒，间隔最长 31 天，超出 400 `dest_hits_range`）、`include_trial`、`q`（目的地关键词）、`group_by`（`none`/`site`/`user`/`policy`）、`page`、`page_size`。响应另带 `summary:{block, deny, observe, users}`（时间/账号/节点选择，**带 `source` 时再收窄到这个来源**，不受 `action`、`source_kind` 筛选影响。取舍：计划 P7 只把 `action` 与 `source_kind` 列为不影响摘要的筛选，而「转为拦截」对话框（S4）要的是「这条策略涉及多少账号」，读的就是 `source=p<id>` 请求的 `summary.users`，所以按来源收窄比另设 `summary_for_source` 更贴合计划，前端也不用多发一个请求；不带 `source` 时仍是整个选择，指标卡不随 `action` 变化）、`sources:[{source, name|null}]`（`null` = 已删除）与 `dropped_in_range` |
| GET | `/policies` | 策略带 `hits_recent`（窗口内 `block` 命中数）、`last_hit_at`，**状态为 `report` 的另带 `would_block_recent`**（窗口内 `observe` 命中数，即本应拦截的次数）；`hit_window_days = min(7, 命中保留天数)`。没有任何节点在记录时 `hits_recent` / `would_block_recent` 为 null；放行策略不记录任何东西，三个字段恒为 null。`last_hit_at` 只在保留窗口内、只为列出的非放行策略读取（走 `(source, hour_ms)` 索引）。风险信号 `dest_block` 判断「节点是否在记录」用的是不随每次发布抖动的 `CollectingForRisk`（已应用过带记录级别的策略、且面板仍要求记录即算），`/status.collecting` 仍是「此刻确实在记录」的诚实答案（「0 次」对一条没人监视的策略是没有依据的说法）。仅报告的策略在 `block` 数组里，`observe` 数组恒为空 |
| GET / PUT | `/servers`、`/servers/:id`（已有） | 原生服务器的 DTO 与更新多了 `audit_collect`（省略保持原值；对非原生服务器 400；非法值 400） |
| GET | `/status` | 全局 `{generation, published_generation, last_write_at, next_publish_at, apply_eta_ms, paused, publish_error, geosite_release, geoip_unavailable, totals, nodes:[…]}`；每节点含 `state`、`fallback_reason`、`over_limit`、`sniffing_insufficient`、`rules_data_version`、`geoip_unavailable`、`collect`（面板档位）、`collect_effective`（下发给节点的档位）、`collecting`（推导的「在记录」，§4.0）、`hits_24h`（近 24 小时命中数，节点无记录能力时为 null）、`report_only_not_deployed`（`"sniffing"`、`"limit"` 或 null：这个节点收到的策略里被去掉了仅报告规则的原因，null 表示都已下发；开启的规则不受影响；每次编译重新写入，所以嗅探修好、额度够了就变回 null，与节点 issue `destination_policy_report_only_not_deployed` 同源，但 issue 行不会自己消失，这个字段会） |
| POST / PUT | `/publish`、`/pause` | 立即发布（被校验拒绝 409 `dest_policy_over_limit` 带 `publish_error`）；紧急开关 `{paused}` |
| POST | `/agents/:agent_id/retry` | 清除 `rejected`，下一轮重试新版 |

节点 `state`：`none`、`paused`、`unsupported_kind`、`not_enrolled`（原生面板上还没有节点注册）、`unsupported_version`、`pending`、`applied`、`rejected`、`over_limit`、`sniffing`、`offline`
（agent 超过 3 个同步周期未上报）。`rules_data_version` 由节点上报的摘要反查得到（PSP 记录每份已下发策略对应的 geosite 发布号）；
进程重启后、或摘要对应的是重启前的旧策略时为 null。

## 7. 存储与隐私

- **命中记录（A 档）默认开启**（面板 `audit_collect = hits`，且存在会记录的规则时）：记录**哪条规则对谁、去往哪个目的地与端口命中了几次**，按小时聚合。**不存客户端来源 IP**（协议里没有这个字段，来源规则的命中也一样），不存 URL 路径（看不到），不存用量（B 档未提供）。
  目的地是客户端请求的主机名（嗅探出的域名）或 IP，已由节点规范化。原始访问日志永远不离开节点。
- **保留期**：`dest.hit_retention_days`（默认 30，1–365；0 是默认，永远不是「永久」）；白名单试运行行 `dest.trial_retention_days`（默认 7，1–30，有效值不超过命中保留期）；批次标记 72 小时。每小时一次的清理（`runAuditCleanupLoop`，`pruneRiskReviews` 之后）按**整点对齐**的截止时间删除，并清理孤儿（已删账号、已删面板的行与计数，已不存在的 agent 的投递状态）。读不到设置时跳过按时间的删除并打 Warn，孤儿清理照常。
- **「尽力而为」，不是审计级保证**：①节点把批次放在内存里，重启丢最多一个同步周期的数据；②Xray 对失败的 webhook POST **不重试**，接收器不可用的那一刻命中就丢了；③PSP 在同步应答之后才入库（有界队列：每节点 4 批、全局 256 批，满了丢弃并计 `queue_full`；PSP 重启丢队列里的批次）；④每个 agent 每小时最多入库 20000 行，超出计 `over_budget`。
  这些损失都被计数（指标与 `dest_audit_drops`），`GET /hits` 的 `dropped_in_range` 告诉你这段时间有多少行未统计；节点自己报告的丢弃（聚合表满）与无法使用的事件也在其中。**不要把「没有命中」读成「没有发生」**：账号视图的 `hits_available` 与策略的 `null` 就是为此存在。
- **隔离解码**：同步处理器把 `audit` 子树当作原始字节单独量大小（≤ 1 MiB）、解码、`ValidateAuditObservation`、检查能力（缺 `audit.hits.v1` 丢整棵，缺 `audit.usage.v1` 丢用量行）；任何失败只丢这棵子树并计数，同步照常 200，roster 与 config 不受影响。子树在 `Sync` 里与 Host 同处剥离，不进最近上报缓存，**同步应答构建完成之后**才投递入队。
- **BatchID 去重**：进程内 LRU（最近 10000 个，投递时就记）；重启后靠 `dest_audit_batches` 的 `INSERT … ON CONFLICT DO NOTHING` 与 `RowsAffected` 兜底（不靠捕获主键冲突：PostgreSQL 事务里捕获错误会让事务进入 aborted）。每个事务不超过 1000 行、每条语句 200 行。
- 读取命中记录不写审计行（它在 `/api/admin/dest/` 下，只有管理员能调；查询串不进访问日志）。
- **日志规矩**：Info 及以上只记计数和 agent_id，**任何级别都不记目的地、账号、来源 IP**；入库失败的错误文本若含批次里的目的地则被隐去（`redactErr`）。指标的标签只有封闭集合。
- 审计：`/api/admin/dest/` 下所有写操作（含 `test`）进审计日志，请求体照例记录（列表原文、豁免理由、被测目的地都在里面），
  所以这些行**只有管理员能读**（`adminOnlyAuditTargets`）。两个预览接口按精确路径豁免，不写审计。
- PSP 自己的访问日志在 `/api/admin/dest/` 下**不写查询串**，避免搜索词与账号 id 进 journald，不受任何保留设置约束。
  **反向代理自己的访问日志 PSP 管不到**，部署时请同样处理。
- 日志规矩：Info 及以上只记计数和 agent_id；Debug 也不记 email、目的地、来源 IP。PSP 侧 issue 的内容只有摘要前缀、入站键和额度数字。
- geosite 数据只在 PSP 展开：PSP 下载并用 `.sha256sum` 校验 `dlc.dat_plain.yml`，保留当前与上一份（`<DataDir>/destlists/`），
  以 v2fly 的 release tag 为「规则数据版本」。下发的是明文条目，所有节点拿到同一份。远程列表只允许 https、走 SSRF 防护的客户端、
  超过 16 MiB 报错（不截断），内容摘要不变不推进 generation。

## 8. 升级与降级

| 组合 | 结果 | 要做的事 |
|---|---|---|
| 升级 PSP，节点全是旧版 | 节点没有能力，不下发策略；config ETag 不变，core 不重启 | 无 |
| 升级节点，PSP 还没有任何策略 | 只有节点升级自己那一次重启 | 发布说明写明 |
| 第一次启用任何策略 | 所有有能力的节点在一个去抖窗口加一个同步周期内**同时**重启一次 | 页面第一次下发前确认并写出节点数 |
| **PSP 降级**到不含本功能的版本 | 旧 PSP 铸出的 ConfigBody 不带 Policy，**所有策略立即失效**，每台节点重启一次。`dest_*` 表保持原样，再次升级后恢复 | 见 [UPGRADE-v4.md](UPGRADE-v4.md) 的降级警告 |
| 节点回滚到旧版 | 能力消失 → PSP 不再下发 → 该节点重启一次、失去策略 | 无（自动） |
| 新节点 + 旧 PSP | 多出来的 `policy_status` 被旧 PSP 静默忽略 | 无 |
| 新 PSP + 旧节点 | 节点没有能力，状态显示 `unsupported_version` | 升级节点 |
| 原生面板还没有节点注册 | 状态显示 `not_enrolled`，不计入执行节点，也不算需升级 | 在服务器页安装并注册节点 |
| 生产节点升级到带 `audit.hits.v1` 的 agent，且已有第一批拦截策略、`audit_collect` 为默认 `hits` | 策略里多了记录级别，摘要变化，**core 再重启一次** | 想避免：升级前把该服务器的 `audit_collect` 设为 `off`（`PUT /servers/:id`） |

**每次策略变更，节点 core 重启一次**（Xray、sing-box 都没有热重载），现有连接会断开；去抖与摘要比对把次数压到最低。

## 9. 指标

在 `/api/admin/diagnostics/metrics`（见 [observability.md](observability.md) §4.7）：

| family | 含义 |
|---|---|
| `psp_dest_policy_compile_total{result}` | 每次同步的编译结果：`cache_hit`、`compiled`、`fallback_rejected`、`fallback_over_limit`、`fallback_nil`、`invalid` |
| `psp_dest_policy_compile_ms` | 缓存未命中的编译耗时 |
| `psp_dest_policy_publish_total{trigger}` | 发布次数：`debounced`、`max_wait`、`manual`、`pause` |
| `psp_dest_policy_publish_rejected_total` | 发布前校验拒绝的次数 |
| `psp_node_policy_status_dropped_total` | 整体丢弃的节点策略状态（解码失败、校验失败、缺能力） |
| `psp_dest_list_refresh_total{result}` | 列表与 geosite 刷新：`unchanged`、`updated`、`failed`、`broad` |
| `psp_node_audit_report_total{outcome}` | 每份 audit 子树的去向：`accepted`、`oversized`、`invalid`、`no_capability`、`duplicate_batch`、`queue_full`、`ingest_error` |
| `psp_dest_audit_rows_total{outcome}` | 收到的行的去向（计数器只有一个标签，种类与结果合在一起）：`hits_stored`、`hits_unknown_subject`、`hits_out_of_range`、`hits_over_budget`、`usage_collect_off` |
| `psp_dest_audit_node_dropped_total`、`psp_dest_audit_node_unmatched_total` | 节点自己报告的丢弃（命中、试运行、用量合计）与无法使用的事件 |
| `psp_dest_pruned_rows_total{table}` | 保留期清理的行数：`hits`、`trial`、`batches`、`orphans` |

## 10. 已知限制（不是保证）

1. **看不到 URL 路径**：HTTPS 只能看到 SNI 或域名，所以叫「目的地」，不叫「网址」。ECH、DoH 之后直连 IP、客户端自带的加密隧道可以绕过。
2. **sing-box：以 IP 发起的请求配假 SNI。** sing-box 靠路由里的 `sniff` 动作把嗅探出的域名交给后面的规则匹配，Node 的 sing-box 编译器不依赖它改写连接去向。
   所以客户端对一个被拦截域名背后的 IP 直接发起连接、再带一个无关的 SNI 时，域名规则看到的是那个假域名，拦不住。
   Xray 一侧靠 `routeOnly:false` 的注入缩小了这个口子（见下一条），sing-box 一侧没有对等的手段；这是写进限制而不是承诺的原因。
3. **Xray `routeOnly:false` 会改写去向。** 节点为域名规则在没开嗅探的入站上注入 `routeOnly:false` 后，Xray 会连接**嗅探出的主机**而不是客户端请求的地址：
   对携带可识别主机的连接，去向会被改成 SNI 指向的地方。这正是「拦的就是到达的」，但对原本没开嗅探的入站是行为变化。`routeOnly:true` 的入站在 PSP 预检里判为「嗅探不足」，
   不会被改写。
4. **IP/CIDR 与私有地址规则只对以 IP 发起的连接生效。** 路由不做 DNS 解析（Xray `AsIs`，sing-box 未加 `resolve`），所以**主机名解析到被拦网段的连接不会被这些规则匹配**；
   「测试目的地」遇到这种情况会给出 `hostname_skips_ip_rules` 注释。Xray 的 freedom 出站对部分入站类型有默认的私有地址拦截，只是后备，不能依赖。
5. **尾随点拦截。** Xray 不会把 `example.com.` 当作 `example.com`，所以只要有拦截规则带域名，节点就多拦所有以 `.` 结尾的主机名（客户端没有理由那样请求）；
   大小写不需要处理，Xray 在匹配前统一小写。
6. **DNS 搜索列表。** 无点的主机名（如 `intranet`）可能被节点的 DNS 搜索列表（resolv.conf 的 `search`，Docker / Kubernetes 常见）展开成别的名字，
   域名规则看到的是客户端发来的短名。
7. **中转或 CDN 在前时，来源 IP 是中转的。** 节点前面有中转、CDN 或负载均衡时，来源规则看到的是它们的地址，不是用户的真实地址。
8. **国家以 PSP 的 GeoIP 库为准，与 Xray 自带的 `geoip:cn` 不保证逐条一致。** 国家展开用 PSP 已配置的 GeoIP mmdb（风控中心用的同一份）遍历取 `country.iso_code`
   并合并相邻网段；mmdb 不可用时，这条来源规则被跳过，状态页标 `geoip_unavailable`，绝不静默当作已编译。库更新会让 generation +1（仅当有规则用到国家）。
9. **BT 识别**只覆盖明文握手与 uTP；加密 BT（MSE/PE）、DHT 识别不到。
10. **`geoip:` 行依赖 GeoIP 库，库不可用时整张列表被跳过。** 列表里只要有一条 `GEOIP` 行（国家展开成目的地网段）而 PSP 没有可用的 GeoIP 库，**整张列表**（包括其中的域名条目）在这次编译里都不参与，状态页标 `geoip_unavailable`；不会只丢那一行。需要域名条目照常生效时，把国家行拆到另一张列表。
11. **正则很贵**：每个连接都要逐条跑，整份上限 256 条；保存时用 Go `regexp`（RE2）编译，编不过不让保存，没有字面量锚点或长于 256 字符的给警告。
12. **入站嗅探配置不足时，只有需要嗅探的规则在那个入站上不生效，其余规则照常。** 域名规则和协议规则（含 BT）只在嗅探够用的入站上执行（`Inbounds` 限定）；
    端口、IP 段、来源国家/网段、网络、私网等规则在所有入站生效。节点的状态仍是 `applied`，状态页列出嗅探不足的入站并标注意；仅报告规则同理，`report_only_not_deployed` 为 `"sniffing"`。
    有开启的域名放行规则时，被标注的入站上端口、IP 段等拦截可能拦下本应被放行的连接（放行规则在该入站不生效）；Node 的尾点保护在这些入站上同样生效。
    所有入站都不足时，节点只执行不需要嗅探的那部分（状态仍标注，`state` 可能是 `none`）。运营者把入站嗅探改好（`enabled`、`destOverride` 含 http/tls/quic、无排除项、`routeOnly` 关、非 `metadataOnly`）后自动恢复。
    组规则不能按入站限定，所以某个分组里一条需要嗅探而某入站不够时，该分组的白名单在这台节点不执行（按组，其他分组不受影响）。
13. **策略被内核拒绝时，那台节点执行上一版**（修剪后的 LKG）；在此期间，新加入作用分组的成员在这台节点上不受该分组的拦截策略约束。
14. **命中记录是尽力而为的**（见 §7）：节点重启、Xray 不重试失败的 webhook、PSP 队列满或重启都会丢数据，丢失被计数但补不回来。
15. **仅报告只反映「本来就会放行」的连接**：被前面开启的规则拦掉或放行的连接不进仅报告的统计（§3.1）。

## 11. 设置

| 键 | 默认 | 范围 | 说明 |
|---|---|---|---|
| `dest_policy_apply_min_seconds` | 60 | 30–3600 | 尾随去抖窗口；最长等待是它的 5 倍 |
| `dest_list_refresh_hours` | 24 | 6–168 | 远程列表的刷新间隔，不能关闭（过期的列表是悄悄变旧的规则） |
| `dest_hit_retention_days` | 30 | 1–365 | 命中记录的保留天数（0 = 默认，永远不是永久）；风险信号 `risk.dest_block_*` 见 [connection-limits.md](connection-limits.md) §13.12 |
| `dest_trial_retention_days` | 7 | 1–30 | 白名单试运行折叠行的保留天数，有效值不超过命中保留期 |
| `dest_rules_update_mode` | `manual` | `manual` / `auto` | `auto`：检查到新 geosite 版本就下载、校验并应用；`manual`：只提示，管理员点更新。两种模式下，只有**正在使用的分类**内容变化时才让 generation +1。让已启用的策略超额或出现过宽条目的更新会失败并保留旧数据 |

后台循环 `dest-list-refresh` 见 [ARCHITECTURE.md](ARCHITECTURE.md) §3.3。

## 12. 相对规划书的偏离

两处与 `node-audit-plan.md` 不同，都是有意的：

- **`counts_as_risk` 在 PUT 里省略时保持原值**（规划书 R18 说 PUT 带全字段）。原因：这个字段是后来加进 DTO 的，不认识它的客户端（旧前端、脚本）整份回写策略时，若按「省略 = false」就会悄悄关掉 BT、发信模板的风险计数，而且没有任何提示。显式写 `true` / `false` 照常生效；POST 省略为 false。
- **`GET /dest/hits` 的 `summary` 带 `source` 时收窄到该来源**（S12 的指标卡原本按整个选择计）。原因：「转为拦截」对话框（S4）要的「涉及账号」就是 `source=p<id>` 请求的 `summary.users`；另设一个 `summary_for_source` 要多一块响应和一套前端分支。不带 `source` 时仍是整个选择，`action`、`source_kind` 仍不影响它。
