# 访问控制：原生节点的目的地策略

> **范围（第一批）。** 只对 **原生节点（Passwall Node，`kind=psp`）** 生效；3X-UI、S-UI 节点不执行任何策略。
> 本批提供：自定义列表、远程列表、v2fly geosite 分类、内置规则组目录、按分组 / 按节点限定范围的拦截与放行策略、
> 豁免账号、按入站的来源国家 / 来源 IP 段拒绝、测试目的地、发布与回退，以及状态页所需的全部接口。
> 策略状态只有 **关闭 / 开启**。**仅报告（report）、命中记录、用量、白名单分组、sing-box 采集** 在后续批次，
> 本批对它们的请求一律 400 `dest_policy_state_unavailable`，不留占位。
>
> 线上协议见 Passwall-Protocol `v0.3.0`（`protocol/policy.go`、`match.go`、`sniff.go`）；字段说明见
> [psp-node-agent.md](psp-node-agent.md) §11。

## 1. 行为一句话

管理员在 PSP 里定义「哪些目的地不许去」（以及例外）；PSP 把定义展开成一份目的地策略，放进每台原生节点的 **config**
里下发；节点把它编译成 Xray / sing-box 的路由规则。**没有任何策略时，下发给节点的 config 与升级前逐字节相同**，
config ETag 不变，core 不会因为升级面板而重启。

## 2. 概念

| 概念 | 说明 |
|---|---|
| **列表** | 一组匹配条目。`custom`：管理员粘贴的文本（沿用 Clash / Mihomo 行式写法，见 [routing-rule-dsl.md](routing-rule-dsl.md) §4.1）；`remote`：https URL；`geosite`：v2fly `domain-list-community` 的一个分类（可带属性过滤）。`side` 分 `dest`（目的地）与 `source`（来源，只能给来源规则用） |
| **策略** | 一条 `block` 或 `allow`：引用若干列表，外加自带的内置匹配（CIDR、端口、网络、协议 `bittorrent`、私有地址）。有 `state`（`off`/`on`）与 `scope`（`all`/`groups`/`panels`）。同一动作内的顺序由服务端的 `priority` 决定，只能用 `PUT /policies/order` 调整 |
| **豁免** | 让一个账号不受拦截策略约束，可带到期时间。**不豁免来源规则和全局放行之前的步骤**（见 §3） |
| **来源规则** | 按入站（节点）拒绝来源国家或来源 IP 段，只对原生节点，状态 `off`/`on`。它回答「这个入站对谁开放」，不是「用户能去哪」，所以排在最前，豁免也绕不过 |
| **规则组目录** | 随 PSP 发版的内置映射：把「BT 下载」「金融」「广告与追踪」等映射到 geosite 分类、协议、端口或私有地址。所有内置规则组都不默认启用；添加时管理员自选状态；名称、说明、状态、范围可改，**匹配内容只读**（要改就复制成自定义策略）。**没有通用的「教育」「支付」「博彩」分类**，目录里把它们列为 `unavailable` 并写明原因，不会创建任何东西 |

## 3. 匹配顺序

编译器严格照下表输出，协议校验要求规则按阶段非递减出现（Protocol `v0.3.0` 线上规格 §2、§11）：

| 阶段 | 规则 | 说明 |
|---|---|---|
| 0 | 来源规则（`s<节点号>`，`block`） | 按入站与来源 CIDR。同一国家 + CIDR 集合相同的入站合成一条，ID 取该组在本节点内的最小入站号 |
| 1 | 放行策略（`p<id>`，`allow`，按 priority） | 先于豁免：全局放行对所有人生效 |
| 2 | 豁免（`Exempt` 字段，不是规则） | 命中即放行 |
| 2.5 | 节点内置：尾随点域名拦截 | 策略里**有拦截规则带域名**时，节点自己加一条规则拦截以 `.` 结尾的主机名（Xray：`regexp:\.$` → blocked，ruleTag `psp-trailing-dot`）。PSP 不下发它 |
| 3 | 拦截策略（`p<id>`，`block`，按 priority） | |
| 4 | 白名单分组（`g<gid>…`） | 后续批次 |
| 5 | 仅报告（`observe`） | 后续批次；仅报告永远不改变判定 |
| — | 无命中 | 放行（核心默认出站） |

要点：

- 一条规则内各字段是「与」；`Domains`、`CIDRs`、`Private` 是「或」的三选一。一条策略同时有协议（`bittorrent`）和
  域名 / CIDR / 私有地址时，拆成 `p<id>x1`（域名、CIDR、私有地址部分）和 `p<id>x2`（协议部分），端口与网络两边各带一份。
- 「测试目的地」用同一张表逐步给出结果：`hit`、`miss`、`untestable`（输入没给的字段，如来源地址）、`shadowed`、`skipped`。

## 4. 发布、编译与回退

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
7. **嗅探预检**（仅 Xray，且策略里有域名或协议规则）：用 `protocol.CheckXraySniffing` 检查每个启用的入站。嗅探关闭可以（节点会注入
   `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":false}`）；开着但配置得无法提供所需信息（`metadataOnly`、`routeOnly`、
   缺 `destOverride`、有排除项、JSON 损坏）则这台节点**回退**，并记 PSP 侧 issue `destination_policy_sniffing_insufficient`。sing-box 不预检。
8. **每节点超额**：该节点的 Subjects + Exempt 与序列化大小超限 → 回退，issue `destination_policy_over_limit`。**绝不截断**：截断一份拦截名单等于悄悄放行。

### 4.3 回退到最近已知良好策略（LKG）

- **记录**：节点上报 `PolicyStatus{state:applied}` 且 `digest == PSP 铸出的摘要` 时，把这一版写入 `dest_agent_policy.applied_*`。
  `paused` 期间不记录。
- **被拒**：节点上报 `rejected` 且摘要等于铸出的摘要时，置 `fallback_reason=rejected`、记下当时的 `published_generation` 与三个投递能力，
  并产生 issue `destination_policy_rejected`。节点报的是「嗅探不足」时，状态页显示 `sniffing` 并列出入站。
- **回退内容** 是修剪过的 LKG，修剪**只放宽、不收紧**：已删除、已停用、范围已不含本面板的策略的规则去掉；来源规则只保留仍有来源规则的入站；
  动作已改变的策略的规则去掉；Subjects 只保留当前 roster 里的账号，对分组范围的策略再限于该分组的**当前**成员（原来对所有人的规则收窄到成员），空了的规则去掉；Exempt 取 LKG 与当前豁免的并集（都限于 roster）；组规则（后续批次）全部去掉。没有 LKG 或修剪后为空 → nil。**修剪处理不了「列表条目被删」**：LKG 是展开后的明文条目，策略仍在、只是列表里去掉了几条时，回退版仍会继续拦截已被删除的条目，直到新版部署成功。读取 LKG 失败（数据库瞬时错误）时本次同步失败，不会把「无策略」当结果下发或缓存；**连续失败 3 次（或自第一次失败起满 5 分钟）后**回退为 nil（总能部署，roster 照常流动），记 issue `destination_policy_lkg_unreadable`，该结果不缓存，库恢复后下一次同步会重新读取。
- **被拒的是回退本身**（回退用的是修剪后的 LKG，其摘要不同于记录的 `applied_sha256`；或换了 core 版本、LKG 里有新核心不再接受的写法）：只要被拒摘要等于「作为回退铸出」的摘要（铸出的与期望的不同；包括嗅探或超额时铸出的回退），就清空 LKG，回退目标为 nil，issue `destination_policy_lkg_rejected`，不再重铸。
  否则 PSP 会继续铸同一份被拒的 LKG，整份 config 永远部署不下去、roster 冻结。
- 回退版本也要过同一个嗅探预检（LKG、修剪后的 LKG 都一样）：过不了就发 nil、保留 LKG 记录并记嗅探 issue，入站修好后再用——这不是节点拒绝，只是此刻无法应用。被拒的上下文还包括节点上报的 core 版本。
- **被拒记录只在被拒的正是期望策略时才建立**；回退被拒只清 LKG，不会拖住期望策略，嗅探修好后期望策略照常下发。
- **被拒记录**独立保存（`rejected_sha256` 加 `rejected_generation`），不受嗅探或超额回退、无策略、暂停的影响；`rejected_generation` 是被拒策略**被铸出时**的 generation（`minted_generation`），不是上报到达时已发布的那一个，所以在上报到达前又发布了新一版，新一版仍会被尝试。
- **何时重试新版**：被拒时的**上下文**（引擎、core 版本、各入站的嗅探配置的哈希，`rejected_ctx`）变化；`published_generation` 变化且期望摘要与被拒摘要不同（只是 generation 变化而字节相同则不重试，只推进记录的 generation）；`policy.destination.v1` / `audit.hits.v1` / `audit.usage.v1` 三项中任一项增减；
  管理员点「重新下发」（`POST /dest/agents/:id/retry`）。成员变化不触发，避免每次 roster 变动都重走一遍「被拒 → 回退」。
- **何时清除回退**：重试的新版被报告 `applied` 且摘要等于期望摘要；嗅探预检通过；回到额度以内。

### 4.4 缓存与空闲开销

- Compile 的缓存键是 `(已发布 generation, 能力, 引擎, 各入站嗅探与启用状态, roster 与分组归属, 回退状态, LKG 摘要)`。命中缓存时只读一行发布状态，
  不查别的表、不重新序列化。
- 带策略的 ConfigBody 的规范字节另外缓存（键是「去掉 Policy 的 config 摘要」加策略摘要），不变的策略不会移动 ETag。
- `MintStream` 的等值路径只读 `id / desired_version / desired_etag`，不再整行读 `desired_body`（它可能是数 MiB）。

## 5. 表结构

| 表 | 作用 |
|---|---|
| `dest_lists` | 列表。`entries`（BLOB/BYTEA/LONGBLOB）是规范条目，每行一条、排序去重；`source_text` 是管理员粘贴的原文；`content_sha256` 是条目摘要（摘要不变不推进 generation） |
| `dest_policies` | 策略：`action`、`state`（`off`/`on`）、`list_ids`、`inline`（内置匹配 JSON）、`scope`、`group_ids`、`panel_ids`、`priority`、`template_key` |
| `dest_exemptions` | 豁免：`user_id` 主键、`reason`、`created_by`、`expires_at` |
| `dest_source_rules` | 每入站一行：`node_id` 主键、`countries`、`cidrs`、`state` |
| `dest_policy_state` | 单行（id 1）：`generation`、`published_generation`、`first_unpublished_at`、`last_write_at`、`paused`、`geoip_sha256`、`geosite_release`、`publish_error` |
| `dest_policy_snapshots` | 已发布快照的规范序列化，只保留最新一份 |
| `dest_agent_policy` | 每个 agent 一行：期望 / 铸出 / 上报的摘要、`fallback_reason`、`rejected_generation`、`rejected_caps`、`over_limit`、`precheck_listeners`、LKG（`applied_sha256`、`applied_body`…） |

三方言（SQLite、PostgreSQL、MySQL）都有测试；大列一律 `[]byte`，JSON 小列用 `text`，没有 `text` 列带 `DEFAULT`。
节点删除时同一事务带走它的 `dest_source_rules`；账号删除时带走它的 `dest_exemptions`；原生面板删除时带走它的 `dest_agent_policy`。

## 6. 接口

全部在 `adminGroup`（仅管理员）下，前缀 `/api/admin/dest`。时间一律 UTC 毫秒。400 是 `{error, code, field?, bad?}`。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET / PUT | `/settings` | `dest_policy_apply_min_seconds`、`dest_list_refresh_hours`、`dest_rules_update_mode`；只提交改过的键 |
| GET | `/lists` | `{items:[…], refresh_hours, budget}`；`state` 为 `ready`/`refreshing`/`failed`/`pending` |
| POST | `/lists/preview` | 只读：解析报告与前 50 条规范条目（远程列表会真的拉取一次）。**不写库、不写审计** |
| POST / PUT / DELETE | `/lists[/:id]` | PUT 带 `updated_at`，冲突 409 `dest_list_stale`；删除被引用的列表 409 `dest_list_in_use` 带 `used_by` |
| GET | `/lists/:id` | 统计 + 前 200 条样本；`?text=1` 另返回自定义列表的 `source_text` |
| POST | `/lists/:id/entries` | `{add:[…], remove?:[…]}`，只对自定义列表，一个事务、generation 只 +1 |
| POST | `/lists/:id/refresh` | 202；仅远程列表 |
| GET | `/geosite/status` `/geosite/categories` | 规则数据版本面板；无数据时分类 503 `dest_geosite_unavailable` |
| POST | `/geosite/update[?dry_run=1]` `/geosite/rollback[?dry_run=1]` | dry-run 返回每分类差异与判定；被拒 409 `dest_geosite_update_rejected`；忙 409 `dest_geosite_busy` |
| GET / POST | `/catalog`、`/catalog/:key/instantiate` | 目录；实例化 `{state:"off"|"on", locale?}`；不可用的组 400 `dest_template_unavailable`；已添加 409 `dest_template_exists`，名称与现有策略重复 409 `dest_name_taken` |
| GET / POST / PUT / DELETE | `/policies[/:id]`、`/policies/preview`、`/policies/order` | 额度形状 `budget:{rules,domains,regexps,cidrs,source_cidrs,subjects,bytes}`，各为 `{used,limit}`。`order` 的 `ids` 必须恰好等于该动作当前的全部策略，否则 409 `dest_policy_order_stale`。预览**不写库、不写审计** |
| GET / POST / PUT / DELETE | `/exemptions[/:user_id]` | 重复添加 409 `dest_exemption_exists`；响应带 `created_by_upn`、`expired` |
| GET | `/source-rules`、`/source-rules/:node_id` | 全部 / 单个（无规则返回 `state:"off"`）；带 `geoip_available` |
| PUT | `/source-rules/:node_id` | `{countries, cidrs, state, updated_at?}`；非原生节点 409 `dest_source_rule_not_native`；冲突 409 `dest_source_rule_stale` |
| POST | `/exceptions` | 全局例外：`{target, match:"site"|"host", scope:"global"}`；第一次使用时在同一事务里建「放行例外」列表和排在放行第一位的 allow 策略 |
| POST | `/test` | `{target, port?, network?, user_id?, panel_id?, inbound_node_id?, source_ip?}`。**写审计**（谁替谁测试过什么）。永不做 DNS |
| GET | `/users/:id` | 账号的分组与豁免（命中与用量部分在后续批次，为 null） |
| GET | `/status` | 全局 `{generation, published_generation, last_write_at, next_publish_at, apply_eta_ms, paused, publish_error, geosite_release, geoip_unavailable, totals, nodes:[…]}`；每节点含 `state`、`fallback_reason`、`over_limit`、`sniffing_insufficient`、`rules_data_version`、`geoip_unavailable` |
| POST / PUT | `/publish`、`/pause` | 立即发布（被校验拒绝 409 `dest_policy_over_limit` 带 `publish_error`）；紧急开关 `{paused}` |
| POST | `/agents/:agent_id/retry` | 清除 `rejected`，下一轮重试新版 |

节点 `state`：`none`、`paused`、`unsupported_kind`、`unsupported_version`、`pending`、`applied`、`rejected`、`over_limit`、`sniffing`、`offline`
（agent 超过 3 个同步周期未上报）。`rules_data_version` 由节点上报的摘要反查得到（PSP 记录每份已下发策略对应的 geosite 发布号）；
进程重启后、或摘要对应的是重启前的旧策略时为 null。

## 7. 存储与隐私

- 本批**不记录任何用户访问了哪个目的地**：没有命中记录、没有用量。表里只有定义，节点上报的只有 `PolicyStatus`（摘要、状态、问题码、入站列表）。
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

**每次策略变更，节点 core 重启一次**（Xray、sing-box 都没有热重载），现有连接会断开；去抖与摘要比对把次数压到最低。

## 9. 指标

在 `/api/admin/diagnostics/metrics`（见 [observability.md](observability.md) §4.7）：

| family | 含义 |
|---|---|
| `psp_dest_policy_compile_total{result}` | 每次同步的编译结果：`cache_hit`、`compiled`、`fallback_rejected`、`fallback_sniffing`、`fallback_over_limit`、`fallback_nil`、`invalid` |
| `psp_dest_policy_compile_ms` | 缓存未命中的编译耗时 |
| `psp_dest_policy_publish_total{trigger}` | 发布次数：`debounced`、`max_wait`、`manual`、`pause` |
| `psp_dest_policy_publish_rejected_total` | 发布前校验拒绝的次数 |
| `psp_node_policy_status_dropped_total` | 整体丢弃的节点策略状态（解码失败、校验失败、缺能力） |
| `psp_dest_list_refresh_total{result}` | 列表与 geosite 刷新：`unchanged`、`updated`、`failed`、`broad` |

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
12. **入站嗅探配置不足时，策略在那台节点上不下发**（PSP 预检），节点继续执行上一版，直到运营者改好入站。
13. **策略被内核拒绝时，那台节点执行上一版**（修剪后的 LKG）；在此期间，新加入作用分组的成员在这台节点上不受该分组的拦截策略约束。
14. **本批没有命中记录**：管理员看不到「谁被拦了」。这是后续批次的内容。

## 11. 设置

| 键 | 默认 | 范围 | 说明 |
|---|---|---|---|
| `dest_policy_apply_min_seconds` | 60 | 30–3600 | 尾随去抖窗口；最长等待是它的 5 倍 |
| `dest_list_refresh_hours` | 24 | 6–168 | 远程列表的刷新间隔，不能关闭（过期的列表是悄悄变旧的规则） |
| `dest_rules_update_mode` | `manual` | `manual` / `auto` | `auto`：检查到新 geosite 版本就下载、校验并应用；`manual`：只提示，管理员点更新。两种模式下，只有**正在使用的分类**内容变化时才让 generation +1。让已启用的策略超额或出现过宽条目的更新会失败并保留旧数据 |

后台循环 `dest-list-refresh` 见 [ARCHITECTURE.md](ARCHITECTURE.md) §3.3。
