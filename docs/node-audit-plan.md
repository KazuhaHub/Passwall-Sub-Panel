# 节点访问控制（目的地策略、命中记录、白名单分组、隐私页）：任务规划书

- **状态**：规划已定稿（2026-09-28，第二版：按实现视角逐条复核后重写），待派工。所有者决定已全部给出（§1）。
- **涉及仓库**：Passwall-Protocol（线上类型）、Passwall-Node（执行与采集）、Passwall-Sub-Panel（策略、存储、界面）
- **核对基线**：Passwall-Node `origin/main` `753d3a6`；Passwall-Protocol `origin/main`（PSP 与 Node 都钉 `v0.2.0`）；
  PSP `origin/main` `fb12a6c1`；Xray-core `v26.6.27`（catalog 另有 26.7.28、26.9.9）；sing-box `v1.14.0`。
  **本地 Passwall-Node 检出曾落后 origin/main 33 个提交——开工前 `git fetch`，不要照旧检出读代码。**
- **相关文档**：[psp-node-agent.md](psp-node-agent.md)（协议形状）、
  [connection-limits.md](connection-limits.md) §13.7 / §14.3（风控存储与保留的既有规矩）、
  [routing-rule-dsl.md](routing-rule-dsl.md)（规则名称）、
  [server-dashboard-rework-plan.md](server-dashboard-rework-plan.md)（服务器详情页；本功能的节点入口挂在它的 WP-D4 上）

**读本文的方式**：§2 是事实（设计约束从这里来，每条带出处）；§3 是行为模型（匹配顺序一旦写错就是安全漏洞，先读它）；
§4–§9 是各层做法；§11 是 PR 切分与发版顺序；§12 是验收；§13 是测试文件清单。
**每个工作包先写失败的测试，再写实现。**

---

## 0. 一句话

PSP 定义「哪些目的地拦截 / 只观察 / 放行」，以及「哪些分组只能访问白名单」，编进原生节点的代理内核路由；
节点通过内核的 webhook 回传**命中了哪条规则**（默认开启），管理员可以按节点再打开**按主域名聚合的用量**；
原始访问日志永远不离开节点。

## 1. 所有者已定的决定

| # | 问题 | 决定 | 日期 |
|---|---|---|---|
| 1 | 动机 | **防滥用**（BT、SMTP、扫描导致 VPS 被投诉）、**屏蔽高风险金融网站**、**白名单** | 2026-09-27 |
| 2 | 适用范围 | **只做 PSP 原生节点**。3X-UI / S-UI 的 Xray 路由模板归运营者，PSP 不写；它们显示「不支持」 | 2026-09-27 |
| 3 | 记录粒度 | **默认 A 档**（只记规则命中）；**管理员可按节点开启 B 档**（每用户每小时按主域名聚合）；C 档（原始日志）不回传 | 2026-09-27 |
| 4 | 对用户披露 | 做**隐私与协议页面**，**默认关闭** | 2026-09-27 |
| 5 | 白名单的含义 | **按分组的白名单模式：该分组的用户只能访问名单内的目的地，其他一律拒绝**（§9）。放行例外与用户豁免同样保留 | 2026-09-28 |
| 6 | 默认值 | 存在启用的策略时 A 档对所有原生节点默认开启；命中保留 30 天、用量保留 7 天 | 2026-09-28 |

---

## 2. 已核实的事实

实现者如发现与此不符，**先改本文再动代码**。Node 路径均指 `origin/main`。

### 2.1 代理内核（Xray / sing-box 上游）

| # | 事实 | 出处 | 影响 |
|---|---|---|---|
| F1 | Xray 路由与 sing-box 路由都是**第一条命中即终止**。命中「仅观察」规则（路由到一个放行出站）后，后面的规则不再评估 | Xray `app/router`；sing-box `route/rule.md` | §3 的顺序是安全关键 |
| F2 | Xray 规则的 `user` 为空数组时**不增加任何条件，等于匹配所有人** | `app/router/config.go:64` | 编译器遇到空 subject 集合必须**跳过整条规则** |
| F3 | Xray 一条规则内的各字段是「与」；sing-box 默认规则里 `domain*` 与 `ip_cidr` 是「或」，与端口/网络/协议是「与」 | Xray 路由文档；sing-box `route/rule.md` Default Fields | Xray 需把 domain 与 ip 拆成两条，sing-box 不拆 |
| F4 | 嗅探出的域名只有在 `destOverride` 含对应协议、`metadataOnly` 为 false、且域名不在 `domainsExcluded` 时才进入路由 | `app/dispatcher/default.go:232-260,295-316` | 嗅探配置不够时域名规则会**静默失效** |
| F5 | **Xray v26.6.27 起每条路由规则支持 `webhook`**：命中时向一个 HTTP 或 unix socket 地址 POST 一段 JSON，含 `email`、`destination`（**嗅探后的域名**，无域名时为 IP）、`outboundTag`、`source`、`ts` 等；可选按 email 去重；每个事件起一个 goroutine，5 秒超时。unix socket 的写法是 `"<绝对路径>:/<http 路径>"`。catalog 的三个 Xray 版本都有 | `infra/conf/router.go:126-152,264-268`；`app/router/webhook.go`；`common/utils/unixsocket.go:36-54` | **A 档命中记录用 webhook 采集**，不用解析日志（§5 N4） |
| F6 | Xray 访问日志一行：`2006/01/02 15:04:05.000000 from <src> accepted|rejected <tcp|udp>:<dest>:<port> [<in> -> <out>] email: <e>`。分隔符命中规则时是 ` -> `，走默认出站时是 ` >> `，强制出站时是 ` ==> `；`<dest>` 是**客户端请求的**目的地，不是嗅探域名；`rejected` 行带来源 IP、没有 email | `common/log/access.go`、`common/log/logger.go`、`app/dispatcher/default.go:489-499`、`proxy/vless/inbound/inbound.go:601-603` | B 档用量要解析日志（§5 N5）；日志不能原样转发 |
| F7 | Xray 在 `log.access` 为空时把访问日志写到 stdout，只有 `"none"` 关闭 | `infra/conf/log.go:31-41` | 见 F10 |
| F8 | `log.maskAddress` 会对**整行**做 IP 正则替换，目的 IP 也被抹掉 | `app/log/log.go`（`MaskedMsgWrapper`） | **不能用** maskAddress 来隐藏来源 IP |
| F9 | Xray `freedom` 出站对 vless/vmess/trojan/hysteria/wireguard/shadowsocks 入站，**默认在 DNS 解析后拦截私有与保留地址**（10/8、172.16/12、192.168/16、127/8、169.254/16、100.64/10、fc00::/7、fe80::/10 等）；sing-box 的 `direct` 没有这个默认 | `proxy/freedom/freedom.go:62-86,165-179` | 「内网与元数据」预设在 Xray 上主要起**记录**作用，在 sing-box 上才真正起拦截作用 |

### 2.2 节点侧（Passwall-Node）

| # | 事实 | 出处 | 影响 |
|---|---|---|---|
| F10 | **生产 Xray 节点正把每条连接写进 journald / Docker 日志**：编译器从不设置 `log.access`（生产 wiring 只给 `APIListen/CoreVersion/AllowRestrictedReality`，`cmd/node/main.go:200-203`），core 的 stdout 直接接 agent 的 stdout（`internal/core/process/supervisor.go:497-498`，`main.go:187`） | 同左 + F7 | **Phase 0 热修**，与本功能是否上线无关 |
| F11 | Xray 配置**没有 `routing` 段**；`blocked` 黑洞出站存在但无流量指向它 | `internal/core/xray/compiler.go:64-71,147-150` | 新增 routing 生成 |
| F12 | 节点**没有 geosite/geoip 数据文件** | `internal/core/install/install.go:186-190` | 分类由 PSP 展开成明文条目下发 |
| F13 | **任何 config/roster 变化都是 core 全量重启**，无热重载 | `supervisor.go:403-432`；`internal/core/runtime/runtime.go:277-351` | 策略变更要去抖；远程列表只在内容变化时下发 |
| F14 | `stream_documents.stream` 有 `CHECK (stream IN ('config','roster','directives'))`，state schema 为 9；**schema 一变，远程升级直接拒绝** | `internal/state/sqlite/store.go:23,454`；`internal/upgrade/helper_linux.go:138` | **不新增 stream**；策略作为 `ConfigBody` 可选字段下发 |
| F15 | outbox 表 `kind CHECK (kind IN ('issue','task_result'))` | `store.go:496` | 命中与用量不走 durable outbox，走 `NodeReport` 上的非持久可选字段（先例 `Host`） |
| F16 | 旧节点收到不认识的字段会静默丢弃，但仍把 PSP 的 ETag 报成已应用 | `internal/agent/receive.go:145-150` | 必须用 capability 把关 |
| F17 | 两个编译器解析 listener config 用 `DisallowUnknownFields` | `xray/compiler.go:311-325`、`singbox/compiler.go:458-472` | 策略不能塞进 listener config |
| F18 | 编译器输入 `core.Snapshot` 只有 `Listeners/Clients/Now`；`CompilerFactory` 只拿到 `CoreSelection` | `internal/core/core.go:16-20`；`runtime.go:32` | 要给 Snapshot 加 Policy |
| F19 | processor 对未变化的 listener **不调用 runtime 就标记 applied**；仅策略变化而内核拒绝时只产生 `core_convergence_failed` issue，节点继续跑**旧**策略（supervisor 回滚） | `internal/agent/processor.go:251-257`；`internal/agent/issues.go:25` | config ETag 不能证明策略生效，需要单独的 `PolicyStatus` |
| F20 | config 的 `ObjectStatus` key 必须是 listener key；新增 key 类型会让**旧 PSP 拒收整次上报** | Protocol `validate.go:188-195`（`validateReportedObject`） | `PolicyStatus` 做成独立字段，不借 ObjectStatus |
| F21 | sing-box 1.14 拒绝旧式 per-inbound sniffing；需在 `route.rules` 用 `action: "sniff"` | `singbox/compiler.go:184-186,493-500` | sing-box 编译方式不同 |
| F22 | 新上报字段必须同时加进手写的 `partialReport`；`fitReportToWire` 在 **Node**（`internal/agent/report.go:145`），不在 Protocol | Protocol `report.go:158-169` | 协议与 Node 各自的改动点 |
| F23 | supervisor 的 stdout 是可注入的 `Options.Stdout io.Writer` | `supervisor.go:38,147-151,497-498` | B 档的日志过滤器从这里接入 |

### 2.3 PSP 侧

| # | 事实 | 出处 | 影响 |
|---|---|---|---|
| F24 | 期望状态唯一生成点 `nodesync.Service.Sync`；`buildConfig` 是纯函数（无 ctx、无 repo），每轮每节点都跑 | `internal/service/nodesync/nodesync.go:127-257,346-401` | 策略编译要单独注入并缓存 |
| F25 | `Sync` 先剥离 `report.Host`，最后才入库，入库不返回错误 | `nodesync.go:128-139,229-240` | Audit 照此处理 |
| F26 | 节点上 client email 形如 `u{userID}{-k8hex}@{site.email_domain}`，**一人一面板可有多个**；roster 带 `Subject: usr_{userID}` | `domain/pspclient.go:218-224`；`pkg/clientplan/clientplan.go:195-204,481-526`；`nodesync.go:440` | 协议按 subject 指人，节点编译时展开成 email |
| F27 | `node_agent_streams` 有 `desired_etag/desired_body/applied_etag` | `internal/adapters/sqlstore/node_agent_repo.go:52-71` | 去抖时从 `desired_body` 取回旧策略 |
| F28 | 面板表是 **`xui_panels`**（`xuiPanelRow`，`schema.go:930`；domain `domain.Panel`，`types.go:1276`；`PanelKindPSP = "psp"`，`types.go:1239`） | 同左 | 采集档位加在这张表 |
| F29 | 分组选节点散在 **10 处**：`service/render/render.go:108`；`service/user/user.go:302,1475,2179,2231`（经 `NodesFor`）；`service/reconcile/reconcile.go:429`；`service/node/node.go:597,1362`；`transport/http/handler/admin_rules.go:239`；`transport/http/handler/user_me.go:250`（直接调 `group.Matches`，纯函数）。reconcile / node 只持有分组 repo，不持有 `group.Service` | 同左 | 白名单要把它们收拢成一个判断（§9.2） |
| F30 | 风控新表走「具体 repo + 使用方自带窄接口」模式，不进 `ports.Repos` | `sqlstore/flag_record_repo.go`、`risk_signal_repo.go`；`internal/app/app.go:582-589` | 本功能的表照此办理 |
| F31 | MySQL 的 `TEXT` 上限 64 KiB | MySQL | 大列表条目用 `[]byte`（longblob），先例 `node_agent_streams.desired_body` |
| F32 | 远程拉取先例：地理库更新（`safehttp`、大小上限、原子替换），**没有 ETag/摘要持久化先例**；它的读法遇超限会**静默截断**（`service/geo/update.go:318`） | `service/geo/update.go:108-181` | 列表刷新照它写，但超限要报错 |
| F33 | 已有设置键：`sub.sub_log_retention_days`（**0 = 永久**，`settings_kv_repo.go:365`）、`security.auth_event_retention_days`、`risk.hwid_capture_off`（`:500`）、`risk.connection_retention_days`（`:517`）；管理审计日志已占用 `audit` 这个词（`security.audit_retention_days`、`audit_log`、`runAuditCleanupLoop`） | 同左 | 本功能设置用前缀 **`dest.*`** |
| F34 | 没有「用户抽屉」；单个账号的聚合视图是风控中心 `?tab=user&id=N`（`web-react/src/views/admin/risk/UserLookupDetail.tsx`，admin-only）；风控中心当前 tab：`connections, geo, risk, flags, user`（`RiskCenterView.tsx:11`） | 同左 | 用户维度的界面放 `UserLookupDetail` |
| F35 | 协议已经通过 `ClientCounters.LiveIPs` 上报活跃客户端 IP | Protocol `report.go` | 本功能**不重复**上报来源 IP（数据最小化，是自定决定，不是被规格禁止） |

### 2.4 规则来源

| # | 事实 | 影响 |
|---|---|---|
| F36 | v2fly 每个 release 发布 `dlc.dat_plain.yml`（约 3.6 MB，已展开 `include:`）与 `.sha256sum`；条目只有 `domain:`/`full:`/`regexp:` 三种前缀，属性写作后缀 `:@attr` | PSP 下载校验后按分类取条目，无需解析 protobuf |
| F37 | `category-finance` 是**正规**银行券商；`category-cryptocurrency` 235 条；`category-porn` 6660 条（其中 140 条 `regexp:`）；**没有**博彩总分类 | 「高风险金融」无现成分类；成人内容预设占用 140 条正则额度 |

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
0. [sing-box] {"action":"sniff"}（有域名/协议规则时）
1. 全局 allow 策略（按 priority）                         → direct
2. 豁免用户  user:[豁免者全部 email]                       → direct
3. 全局 block 策略（按 priority）                          → psp-deny-p<id>
4. 各白名单分组（§9），每组依次：
     4a. 该组白名单列表   user:[组员]                      → direct
     4b. 该组基础放行     user:[组员]                      → direct
     4c. 该组兜底        user:[组员]（CatchAll）          → 试运行 psp-watch-g<gid> / 执行 psp-deny-g<gid>
5. 全局 observe 策略（按 priority）                        → psp-watch-p<id>
6. （无规则命中）                                          → direct（Xray 默认出站；sing-box route.final）
```

**为什么这样排**：

- 全局 `block` 在白名单之前：白名单里的网站也不许跑 BT、不许发信。
- 全局 `observe` 放在**白名单之后**：放在前面的话，白名单组员访问 observe 列表里的站点会被放行（F1），等于绕过白名单。
  代价：白名单组员命中全局 observe 列表时不会被记录（他们要么被白名单放行，要么被兜底拒绝），可以接受。
- 全局 `allow` 对白名单组员同样生效，等于给所有白名单分组追加放行项；allow 策略编辑框要提示这一点。
- 豁免用户不受 3、4、5 约束，也就不受白名单约束；界面写明「豁免 = 不受任何访问限制」。

### 3.3 总体结构

```
                     PSP                                              Node
┌──────────────────────────────────────────┐        ┌──────────────────────────────────────────┐
│ 列表（自定义 / 远程URL / v2fly 分类）        │        │ ConfigBody.Policy → core.Snapshot.Policy   │
│ 策略（动作 + 列表 + 作用域 + 优先级）        │ config │   compiler：routing.rules / route.rules    │
│ 豁免；白名单分组；节点采集档位               │ ─────▶ │   psp-deny/psp-watch 规则挂 webhook         │
│  └ destpolicy.Compile（缓存、去抖、超限保留）│        │ webhook 接收器（unix socket）→ 命中聚合器   │
│                                            │        │ [B 档] core stdout 过滤器 → 用量聚合器      │
│ 命中 / 用量入库（小时桶，BatchID 去重）      │ report │ PolicyStatus（内核真正在跑的策略摘要）       │
│ 策略生效状态 ← PolicyStatus                 │ ◀───── │ Audit（Take/Merge，按行裁剪）               │
│ 风险信号 dest_block；保留期清理             │        └──────────────────────────────────────────┘
└──────────────────────────────────────────┘
```

---

## 4. 协议（Passwall-Protocol）

加性改动：不升线上协议版本、不加端点、不加 stream（F14）。

### 4.1 下行：`ConfigBody.Policy`（模块版本 `v0.3.0`）

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
}

type DestinationRule struct {
    ID        string       `json:"id"`                  // ^[pg][0-9]{1,12}(x[0-9]{1,4})?$，用作出站 tag 后缀
    Action    RuleAction   `json:"action"`              // "block" | "observe" | "allow"
    Subjects  []SubjectKey `json:"subjects,omitempty"`  // 空 = 所有人（CatchAll 规则除外，见校验）
    Domains   []string     `json:"domains,omitempty"`   // "domain:x" | "full:x" | "keyword:x" | "regexp:x"
    CIDRs     []string     `json:"cidrs,omitempty"`
    Ports     string       `json:"ports,omitempty"`     // "25,465,587" / "6881-6889"
    Network   string       `json:"network,omitempty"`   // "tcp" | "udp" | ""
    Protocols []string     `json:"protocols,omitempty"` // 仅 "bittorrent"
    Private   bool         `json:"private,omitempty"`   // 私有/保留地址；sing-box 编为 ip_is_private，Xray 编为 CIDR 列表（§5 N1-X）
    // CatchAll marks a rule with no match fields that applies to every connection
    // of its Subjects — the default-deny tail of an allowlist group. It must be
    // explicit: a rule whose match lists merely came out empty (a list failed to
    // load) must be rejected, never silently widened into "match everything".
    CatchAll  bool         `json:"catch_all,omitempty"`
}
```

**`func ValidateDestinationPolicy(*DestinationPolicy) error`**（新增，PSP mint 前与 Node 接收时调用同一函数）：

- 规则数 ≤ 256；全部 `Domains` 合计 ≤ 50 000；`regexp:` 合计 ≤ 256 且都能被 Go `regexp` 编译；`CIDRs` 合计 ≤ 20 000；
  全部 `Subjects` + `Exempt` 合计 ≤ 50 000；Policy 规范序列化 ≤ 4 MiB。上限写进 `limits.go`（新增常量），是编译期常量。
- `ID` 唯一且匹配上面的正则。
- `Ports` 匹配 `^\d{1,5}(-\d{1,5})?(,\d{1,5}(-\d{1,5})?)*$`，每个值 1–65535，区间左 ≤ 右。
- 同一规则内：`Domains ∪ CIDRs ∪ Private` 之间是「或」，与 `Ports`/`Network`/`Protocols` 是「与」；
  `Protocols` 不得与 `Domains` 同时出现（BT 识别不产生域名）。
- **匹配字段（Domains、CIDRs、Ports、Network、Protocols、Private）全空的规则必须 `CatchAll: true` 且 `Subjects` 非空**；
  `CatchAll: true` 的规则不得带任何匹配字段。违反即整份 Policy 无效。
- 所有切片**排序去重**后才算合法（保证同一输入的 ETag 确定）。PSP 负责排序，校验只检查。
- Node 的 `validateConfig`（`internal/agent/validate.go:11`）调用它；**失败 = 整段 config 被拒**（连带 listener 变更），
  所以 PSP 必须在 mint 前先调用、失败就不下发（§6 P3）。

另新增 `func PolicyDigest(*DestinationPolicy) string`（规范序列化的 sha256；nil → `""`），PSP 与 Node 共用。

**能力**：`CapabilityDestinationPolicy = "policy.destination.v1"`。**不是**升级能力，不进 `AgentUpgradeCapabilities`
（同 `host.telemetry.v1`：缺了它不代表协议不兼容）。

**ETag 不变性**：`Policy == nil` 时 `ConfigBody` 的规范序列化必须与 v0.2.0 **逐字节相同**，加 conformance 测试锁住。

### 4.2 上行：`NodeReport.Audit` 与 `NodeReport.PolicyStatus`（模块版本 `v0.4.0`）

```go
type NodeReport struct {
    // ...existing fields...
    Audit        *AuditObservation `json:"audit,omitempty"`
    PolicyStatus *PolicyStatus     `json:"policy_status,omitempty"`
}

// PolicyStatus is what the core is ACTUALLY running, updated only after a
// deployment succeeded. A config ETag cannot say this: the processor marks an
// unchanged listener applied without touching the runtime, and a core that
// rejects a policy-only change keeps running the previous one.
type PolicyStatus struct {
    Digest    string `json:"digest"`               // PolicyDigest(已部署策略)；无策略为 ""
    State     string `json:"state"`                // "applied" | "rejected"
    IssueCode string `json:"issue_code,omitempty"` // rejected 时
}

type AuditObservation struct {
    BatchID   string       `json:"batch_id"`   // 16 字节随机 hex，每个新批次一个；重发同一批次沿用
    Engine    string       `json:"engine"`     // "xray" | "sing-box"
    Coverage  string       `json:"coverage"`   // "complete" | "unsupported"
    Hits      []AuditHit   `json:"hits,omitempty"`
    Usage     []AuditUsage `json:"usage,omitempty"`
    Dropped   uint64       `json:"dropped"`    // 聚合器键满导致丢弃的事件数
    Unmatched uint64       `json:"unmatched"`  // 解析失败或无 email 的事件/行数
}

type AuditHit struct {
    Hour    int64      `json:"hour"`     // 整点 UTC 毫秒（按节点收到事件的时间）
    RuleID  string     `json:"rule_id"`
    Subject SubjectKey `json:"subject"`
    Dest    string     `json:"dest"`     // 去掉端口的主机名或 IP，≤ 253 字节
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

- **能力**：`CapabilityAuditHits = "audit.hits.v1"`、`CapabilityAuditUsage = "audit.usage.v1"`；`PolicyStatus` 随
  `policy.destination.v1` 一起。
- **新增 `ValidateAuditObservation`**，并在发送方 `ValidateNodeReport`（Protocol `validate.go:136-157`，照 Host 与能力绑定的写法）里：
  Audit 非空而缺 `audit.hits.v1` → 报错。上限：每次 `Hits` ≤ 4096 行、`Usage` ≤ 8192 行、Audit 序列化 ≤ 1 MiB（`MaxAuditObservationBytes`）。
- **两个字段都要加进 `partialReport`**（F22）。
- **非持久**（F15）：节点重启会丢掉未发出的数据，最多一个同步周期；换来不 bump schema、远程升级照常可用。写进协议注释。

### 4.3 发版与跨仓开发流程

1. **Protocol 分两次发**：1a（下行）合并后打 `v0.3.0`；2a（上行）合并后打 `v0.4.0`。不要等到 2a 一起发，否则阻塞 Node 1b 与 PSP 1c。
2. **打 tag 前**：清空 `.github/api-breaks.txt`（其头部注释说明了规矩）；`harness/equiv_test.go`（对比 beta11 的等价性 harness，
   非必需检查）会因 `ConfigBody` 变化失败——在同一 PR 里记录这是有意的分歧或退役该 harness（README「Provenance」一节的规矩）。
3. **跨仓开发**：本地用 `go.work`（`/Users/kazuha/Codes/go.work` 目前不含 Passwall-Protocol，开发时加上）；CI 是 `GOWORK=off`，
   所以 PR 里用 `go get github.com/KazuhaHub/passwall-protocol@<commit>` 的伪版本，或打 `v0.3.0-rc.N`。**禁止合并含 `replace` 的 go.mod。**
4. **Node 升级依赖后**：在 `protocol/alias.go` 补全所有新导出名（类型别名、常量、函数包装）；`cmd/contract-agent` 同 PR 适配签名变化；
   dependabot 对 protocol 的升级 PR 不用，手动升。
5. **PSP 升级依赖后**：更新 `docs/compat/verification-v1.json` 的 `contract_source` 指向新 Node 提交（`deploy/compat/contract-source.mjs` 读它）。
6. **发布顺序**：Protocol → Node（声明能力）→ PSP。PSP 必须在「所有节点都是旧版」时照常工作。

---

## 5. 节点实现（Passwall-Node）

### N0　Phase 0 热修：关掉泄漏的访问日志（最先做，单独发修复版）

- Xray 编译器：`c.AccessLog == ""` 时写 `Log["access"] = "none"`，**除非** `Policy != nil && Policy.Collect == "hits_and_usage"`（B 档要读日志，见 N5）。
  现有 `Compiler.AccessLog` 字段（`compiler.go:35,152-154`）非空时照旧使用。
- 测试（先写）：三种情况——无策略 → `"none"`；A 档 → `"none"`（A 档用 webhook，不需要日志）；B 档 → 不设（stdout）。
  真二进制 `xray run -test` 通过（见 N8 的环境变量）。
- 发布说明：
  - 节点升级后编译出的配置摘要变化，`runtime.converge` 会重新部署（`runtime.go:330-351`），**每个节点 core 重启一次**。
  - 旧版节点的日志里已有连接记录。systemd：`journalctl --vacuum-time=…`；Docker：`docker compose logs` 删不掉，
    需要 `docker compose down && docker compose up -d` 重建容器才会丢弃 json-file 日志（已有 3×10 MiB 上限，`compose.example.yaml`）。

### N1　策略进入编译器

1. `core.Snapshot` 加 `Policy *protocol.DestinationPolicy`；`runtime.converge`（`runtime.go:294` 附近装配 Snapshot 处）填入 config 流里的 Policy。
2. 测试：**仅 Policy 变化**必须产生新 digest 并触发 Deploy。
3. 编译失败的处理：返回 `ObjectError{Stream: config, Key: <字典序第一个 listener key>, Code: "destination_policy_rejected"}`，
   并把 `PolicyStatus` 置为 `rejected`（N3）。**绝不静默去掉 Policy 继续部署**——那等于把拦截和白名单全部放行。
4. 嗅探（F4），只在存在 Domains 或 Protocols 规则时处理：
   - **未启用**嗅探的 listener：注入 `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":true}`（`routeOnly` 保证不改变实际连接目标）。
   - **已启用**嗅探的 listener：若 `metadataOnly: true`、或 `destOverride` 不含 `http`/`tls`/`quic` 中任一、或存在 `domainsExcluded`，
     编译器对该 listener 返回 `ObjectError{…, Code: "destination_policy_sniffing_insufficient"}`，**不擅自改写**
     （`routeOnly:false` 时追加 destOverride 会改变真实连接目标）。PSP 策略页列出这些 listener。
   - PSP 新建入站默认已是 `enabled:true, destOverride:[http,tls,quic,fakedns], routeOnly:false`（`web-react/src/views/admin/NodesView.tsx:460-463`），满足条件。

### N1-X　Xray 编译（`internal/core/xray/compiler.go`，新文件 `policy.go`）

`xrayConfig` 加 `Routing map[string]any \`json:"routing,omitempty"\``，无策略时不出现。按 §3.2 顺序生成。每条规则的形状：

```json
{"type":"field","ruleTag":"psp-p12","user":["u5@psp.local","u5-k1a2b3c4d@psp.local"],
 "domain":["domain:example.com","full:a.example.org"],"port":"443","network":"tcp",
 "outboundTag":"psp-deny-p12",
 "webhook":{"url":"/opt/passwall-node/data/runtime/audit.sock:/hit"}}
```

- `Subjects` → `user`：从**本次部署的** roster 取这些 subject 的**全部** client username（F26）。展开后为空 → **跳过整条规则**（F2），
  并加测试断言编译结果里不存在 `"user":[]`。`Subjects` 为空（所有人）→ 不写 `user` 字段。
- `Exempt` → §3.2 第 2 条：`{"user":[豁免者 email],"outboundTag":"direct"}`；展开为空则不生成。
- 同一规则内既有 `Domains` 又有 `CIDRs`/`Private` → 拆成两条同出站、同 webhook 的规则（F3）；`ruleTag` 分别加后缀 `a`/`b`。
- `Private` → `ip` 字段写入私有与保留地址 CIDR 列表（0/8、10/8、100.64/10、127/8、169.254/16、172.16/12、192.168/16、198.18/15、224/3、::1/128、fc00::/7、fe80::/10、ff00::/8）。
- `Protocols: ["bittorrent"]` → `"protocol":["bittorrent"]`。
- `CatchAll` → 只有 `user` 字段。
- 出站：每条 block 规则一个 `{"tag":"psp-deny-<id>","protocol":"blackhole"}`，每条 observe 一个 `{"tag":"psp-watch-<id>","protocol":"freedom"}`；保留原 `direct`、`blocked`。
- **webhook**：只挂在 `psp-deny-*` 与 `psp-watch-*` 规则上，且仅当 `Collect != ""`；`deduplication` 不设（要计数，不去重）。
  socket 路径 = `<DataDir>/runtime/audit.sock`（绝对路径）；路径超过 100 字节时不挂 webhook，`Coverage = "unsupported"`。

### N1-S　sing-box 编译（`internal/core/singbox/compiler.go`，新文件 `policy.go`）

- 有 Domains 或 Protocols 规则时，`route.rules` 第一条 `{"action":"sniff"}`（F21）。
- 规则字段：`auth_user`（sing-box 按 inbound user `name` 匹配，编译器以 `Credentials.Username` 为 name，`:263-267`）；
  前缀映射 `domain:`→`domain_suffix`、`full:`→`domain`、`keyword:`→`domain_keyword`、`regexp:`→`domain_regex`；
  `ip_cidr`；`Private` → `ip_is_private: true`；`Ports` → `port: [int]` 与 `port_range: ["a:b"]`（**sing-box 用冒号**）；
  `network`；`protocol: ["bittorrent"]`。**不拆分** domain 与 ip（F3）。
- 动作按 §3.1；observe 出站为 `{"type":"direct","tag":"psp-watch-<id>"}`。
- sing-box 没有 webhook：**首版只执行、不采集**，`Coverage = "unsupported"`（Collect 非空时每次 full 报告附 `{engine:"sing-box",coverage:"unsupported"}`）。

### N3　PolicyStatus

- runtime 在 `saveDeployment`（`runtime.go:354`）成功后，把**已部署**策略的 `PolicyDigest` 写入内存状态：`{Digest, State:"applied"}`；
  编译或部署失败时 `{Digest: 期望摘要, State:"rejected", IssueCode}`。
- 每次上报（full 与 partial）都带 `PolicyStatus`（节点有 `policy.destination.v1` 能力时）。

### N4　A 档：webhook 接收器（`internal/agent/auditsink/`，新包）

1. agent 启动时在 `<DataDir>/runtime/audit.sock` 监听 unix socket（先删除残留文件；权限 0600；systemd 下 DataDir 是唯一可写目录，符合 rootless 约束）。
   **不用** Linux abstract socket（`@…`）：它绕过文件权限，本机任何进程都能伪造命中。
2. 极简 HTTP handler：只接受 `POST /hit`，请求体 ≤ 4 KiB，解析 F5 的 JSON，取 `email`、`destination`、`outboundTag`；
   **丢弃 `source` 字段**（来源 IP 不进任何结构）；立即回 204。handler 内只做一次 map 更新，**不做任何 I/O**，
   因为 Xray 每个事件都占一个 goroutine 等待响应（5 秒超时）。
3. `outboundTag` 去掉 `psp-deny-`/`psp-watch-` 前缀得到 rule ID；不在当前已部署策略里的 → `Unmatched`。
4. email → subject：读 runtime 在 `saveDeployment` 成功后推送的快照 `SetRoster(map[email]SubjectKey, ruleIDs)`（**取已部署的**，不是最新存储的）。查不到 → `Unmatched`。
5. 聚合键 `(hour, ruleID, subject, dest, port)`，上限 20 000 键，满了计入 `Dropped`，不扩容。
6. 压测（验收必做，§12 第 16 条）：在 VM 上制造每秒 1000 个被拦截的连接，记录 Xray goroutine 数与 agent CPU；
   若 goroutine 持续堆积，退路是给 webhook 设 `deduplication: 1`（每用户每秒最多一条，计数改为「至少」语义，界面标注）。

### N5　B 档：访问日志过滤器（`internal/agent/auditlog/`，新包；仅 `Collect == "hits_and_usage"` 且 engine = xray）

1. 过滤器作为 `process.Options.Stdout`（F23）传入。exec 会为非 `*os.File` 的 Writer 起一个拷贝 goroutine，**Write 绝不能阻塞**：
   - 按行切分（`bufio`，单行上限 8 KiB，超长截断）后**非阻塞**投递到容量 4096 的 channel，满则丢弃并计 `Dropped`；
   - 转发到 agent stdout 也走独立的非阻塞队列；
   - 解析 goroutine 带 panic 防护；panic 后降级为「只做形状判断：访问行丢弃，其余转发」，不再聚合。
2. 访问行判定：匹配 `^\d{4}/\d\d/\d\d \d\d:\d\d:\d\d\.\d{6} from \S+ (accepted|rejected) ` 的行**一律不转发**（它们带来源 IP）。
3. 解析正则：
   `^\d{4}/\d\d/\d\d \d\d:\d\d:\d\d\.\d{6} from (\S+) (accepted|rejected) (\S*)(?: \[(\S+) (?:->|>>|==>) (\S+)\])?(?: (.*?))?(?: email: (\S+))?$`
   - `rejected` 行、无 email 的行 → `Unmatched`，不聚合；
   - 出站 tag 以 `psp-deny-`/`psp-watch-` 开头的行 → 已由 webhook 计入命中，**这里跳过**，避免重复；
   - 其余 `accepted` 行 → 用量：目的地去掉 `tcp:`/`udp:` 前缀与端口（IPv6 形如 `[::1]:443`），用 `golang.org/x/net/publicsuffix` 取 eTLD+1，IP → `(ip)`；
   - 小时桶用节点收到这一行时的 UTC 时间，不解析日志时间前缀（那是节点本地时区）。
4. 聚合键 `(hour, subject, site)`，上限 50 000。email → subject 用 N4 同一份已部署快照。

### N6　上报组装（`internal/agent/report.go` 等）

1. 构建上报时从两个聚合器 `Take()` 出要发的行，连同一个新 `BatchID` 放进 pending；**响应校验通过后丢弃 pending**；失败则 `Merge` 回聚合器，
   下次重发时**沿用同一个 BatchID**（PSP 按它去重）。
2. `fitReportToWire`（`report.go:145`）的丢弃顺序改为：Host → Audit.Usage（按 hour 从新到旧**逐行**裁）→ Audit.Hits（同）→ 部分上报。
   被裁掉的行**留在聚合器**下轮再发；只有聚合器键满时才计 `Dropped`。`BuiltReport` 增加 `AuditUsageTrimmed/AuditHitsTrimmed` 供测试断言。
3. Audit 非空即随每次上报（含 partial）发送。

### N7　能力声明

- `policy.destination.v1`：两种 engine 都支持执行，始终声明。
- `audit.hits.v1`、`audit.usage.v1`：始终声明；sing-box 下用 `Coverage = "unsupported"` 表达（能力在启动时算，engine 可在运行中被 PSP 更换）。

### N8　测试与完成判据（Node）

- 表驱动、断言解码后的 JSON（沿用现有 `compiler_test.go` 风格，无 golden 文件）。必须覆盖：
  无策略 → 无 `routing`、`access:"none"`；§3.2 顺序（allow → exempt → block → 白名单 → observe）；一人多 email 展开；
  subject 不在 roster → 整条跳过且无 `"user":[]`；Xray domain+ip 拆分、sing-box 不拆；`Ports` 两种写法；Private 两种编译；
  CatchAll；嗅探注入、嗅探不足报错；webhook 只挂在 deny/watch 规则且仅 Collect 非空时。
- 真二进制：catalog 的**全部三个** Xray 版本（`PSP_TEST_XRAY_26627_BIN`、`PSP_TEST_XRAY_26728_BIN`、`PSP_TEST_XRAY_2699_BIN`；
  `PSP_TEST_XRAY_BIN` 只是 26.6.27 的回退，`xray/compiler_test.go:150-158`）跑 `xray run -test`；`PSP_TEST_SING_BOX_BIN` 跑 `sing-box check`；
  至少一份 50 000 条域名的配置，在 VM 上记录一次启动耗时。
- webhook 接收器：真 Xray 在 VM 上命中后，聚合器里出现正确的 (rule, subject, dest)；来源 IP 不出现在任何结构里。
- 日志过滤器 fixture：用真 Xray 在 VM 上抓一批行，覆盖 `->`、`>>`、`==>`、IPv6 `tcp:[::1]:443`、UDP、`rejected`、无 email、mux 子连接；
  背压测试：下游不读时 core 不阻塞。
- 上报：Take/Merge 语义（失败不丢、成功不重）；按行裁剪；BatchID 重发沿用。
- **完成判据**：`go test ./...`、`go vet`、仓库现有 lint；`deployment/coreacceptance` 全部叶子测试跑且通过。

---

## 6. PSP 实现

### P1　数据模型（`internal/domain` + `internal/adapters/sqlstore`，走 F30 的具体 repo 模式）

所有表进 `schemaModels`；`type:text` 列不带 DEFAULT（`TestSchemaNoDefaultOnTextColumns`）；JSON 切片字段实现
`GormDBDataType() = "text"`；**三方言测试都要跑**（`PSP_TEST_DB_KIND`，见 CLAUDE.md）。

| 表 | 列 | 说明 |
|---|---|---|
| `dest_lists` | `id`；`name` varchar(128)；`kind` varchar(16)（`custom`/`remote`/`geosite`）；`source_url` varchar(1024)；`geosite_category` varchar(128)；`geosite_attrs` varchar(128)；**`entries` `[]byte`**（规范化条目，一行一条；MySQL 映射 longblob，F31）；`entry_count` int；`regexp_count` int；`content_sha256` varchar(64)；`refresh_hours` int；`last_fetched_at`；`last_error` varchar(512)；`created_at`/`updated_at` | 三方言测试插入 2 MiB 的 entries |
| `dest_policies` | `id`；`name` varchar(128)；`action` varchar(16)；`list_ids` text(JSON)；`inline` text(JSON：cidrs、ports、network、protocols、private)；`scope` varchar(16)（`all`/`groups`）；`group_ids` text(JSON)；`priority` int；`enabled` bool；`counts_as_risk` bool；时间戳 | |
| `dest_exemptions` | `user_id` PK；`reason` varchar(255)；`created_by` bigint；`created_at` | |
| `dest_group_modes` | `group_id` PK；`mode` varchar(16)（`open`/`allowlist`）；`stage` varchar(16)（`trial`/`enforce`）；`list_ids` text(JSON)；`base_list_id` bigint；`stage_changed_at`；`updated_at` | §9。不往 `groups_` 加列（分组行被多处按列写） |
| `dest_agent_policy` | `agent_id` varchar(64) PK；`policy_sha256` varchar(64)；`changed_at`；`over_limit` varchar(512) NOT NULL DEFAULT ''；`reported_sha256` varchar(64)；`reported_state` varchar(16)；`reported_issue` varchar(64)；`reported_at`；`updated_at` | 去抖、超限、生效状态（P3、P4） |
| `dest_hits` | 主键 `(hour_ms bigint, panel_id, user_id, source varchar(24), dest varchar(253), port int)`；`count` bigint；`first_at`；`last_at`。索引 `(user_id, hour_ms)`、`(source, hour_ms)`、`(panel_id, hour_ms)` | `source` = `p<policyID>` 或 `g<groupID>`（规则 ID 的 `x<n>`、`a/b` 后缀入库前去掉）。**无来源 IP**（F35） |
| `dest_usage_hourly` | 主键 `(hour_ms, panel_id, user_id, site varchar(253))`；`count` bigint | B 档 |
| `dest_audit_batches` | 主键 `(agent_id, batch_id varchar(32))`；`received_at` | BatchID 去重，保留 1 天 |
| `xui_panels` 加列 | `audit_collect varchar(16) NOT NULL DEFAULT 'hits'`（模型 `xuiPanelRow`，domain `Panel.AuditCollect`） | 取值 `off`/`hits`/`hits_and_usage`；只对 `Kind == psp` 生效 |

### P2　列表服务（`internal/service/destlist`，新包）

1. **自定义**：管理员粘贴文本，逐行解析（`#` 注释），接受：
   - 纯域名 `example.com` → `domain:example.com`；`domain:`/`full:`/`keyword:`/`regexp:` 前缀原样；
   - Clash 行 `DOMAIN-SUFFIX,x` / `DOMAIN,x` / `DOMAIN-KEYWORD,x` / `DOMAIN-REGEX,x` / `IP-CIDR,x` / `IP-CIDR6,x`（第三字段策略名忽略；名称沿用 routing-rule-dsl §4.1）；
   - hosts 行 `0.0.0.0 x` / `127.0.0.1 x`；AdGuard `||x^`；CIDR `1.2.3.0/24`。
   - 返回解析报告：接受 N 条、忽略 M 条，附前 20 条被忽略行的行号与原因。
2. **远程 URL**：`safehttp` 客户端，超时 60s，非 2xx 为错误；读取 `limit+1` 字节判断，**超过 16 MiB 报错，不截断**（不要照抄 F32 的截断写法）；
   另支持 Clash rule-provider YAML（`payload:` 列表，`+.x` / `.x` 视为 `domain:x`）。
   算 `content_sha256`，**只有摘要变化才更新 `entries` 并使策略代次 +1**（F13：否则每次刷新都重启全部 core）。
3. **v2fly 分类**：下载 `https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat_plain.yml` 与 `.sha256sum`，
   **校验 sha256 后**解析（F36）；整份缓存到 `<DataDir>/destlists/dlc_plain.yml`，所有 geosite 列表共用一次下载。
   条目先剥掉 `:@attr` 后缀再下发；`geosite_attrs` 为空 = 全部条目，非空 = 条目须含全部所列属性。
4. **刷新循环** `dest-list-refresh`：`safego.GoTracked`；间隔读设置 `dest.list_refresh_hours`（每轮重读，live）；单飞；失败保留旧 `entries` 并写 `last_error`。
5. 管理端「立即刷新」与地理库更新同形（POST 触发、返回状态、前端轮询）。
6. **完成判据**：解析器对每种格式有表驱动测试；超限报错测试；摘要不变不 bump 代次的测试；`safehttp` 拒绝回环地址的测试沿用。

### P3　策略编译（`internal/service/destpolicy`，新包）

**接入点**：`nodesync.Options`（`nodesync.go:77`）加 `Policies DestPolicyCompiler`（可为 nil = 功能关闭）；`Sync` 在 `buildConfig` 之后调用
`Compile(ctx, panelID, agentID, report.Capabilities, snapshot) (*protocol.DestinationPolicy, error)`，结果挂到 `ConfigBody.Policy`。
**用本次上报的能力**，不用 agent 行上的旧值。

1. 节点没有 `policy.destination.v1` → nil（F16）。有能力但**没有任何启用的策略、没有白名单分组、档位不是 B** → nil（保证 ETag 不变）。
2. 输入：全部启用的策略/列表/豁免/白名单分组；该面板 roster 上的用户及其分组（一个用户只属于一个分组，`domain.User.GroupID`）。
3. 输出按 §3.2 排序；`scope=groups` → `Subjects` = 这些分组里在本面板有 client 的用户的 `usr_{id}`，为空则不下发该规则；
   豁免 → `Exempt`；`Collect` = 面板 `audit_collect`（`off` → `""`）。所有切片排序去重。
4. 调 `protocol.ValidateDestinationPolicy`；失败或超限 → 见第 6 条。
5. **缓存**：按「全局策略代次 + 面板 roster 摘要 + 面板档位」缓存编译结果（Sync 每轮每节点都跑，50 000 条的编译不能每轮重做）。
   策略/列表/豁免/分组模式的任何写入都使全局代次 +1。
6. **去抖与超限**（用 `dest_agent_policy`）：
   - 期望摘要 ≠ 表中 `policy_sha256` 且 `now - changed_at < dest.policy_apply_min_seconds` → 从 `node_agent_streams.desired_body`
     解出上一版 Policy 继续下发（F27）；否则采用新版并更新表。
   - 超限或校验失败 → 同样沿用上一版，写 `over_limit` 说明（如「域名 57 312 / 50 000」），并写一条 `node_agent_issues`
     （PSP 侧新常量 `destination_policy_over_limit`，写法照 nodesync 里 `IssueReportMissingObject` 的用法，`nodesync.go:573`）；
     无上一版 → Policy 为 nil。**不截断**：截断等于悄悄放行。
   - **成员变动**引起的 Subjects 变化**不去抖**：它与 roster 变化在同一轮生效，只重启一次；去抖反而会造成两次重启。
7. **完成判据**（测试先写，`internal/service/destpolicy/compile_test.go`）：无策略时 `ConfigBody` 规范序列化与改动前逐字节相同；
   无能力节点不带 Policy；§3.2 顺序；分组展开；豁免；去抖窗口内沿用旧版；超限沿用旧版并写 issue；同一输入两次编译 ETag 相同；缓存命中不查库。

### P4　上报入库

1. handler 里照 `sanitizeHost`（`internal/transport/http/handler/node_sync.go:151-179`，在 `:256` 调用）写 `sanitizeAudit`：
   先量原始 JSON 子树 ≤ `MaxAuditObservationBytes`，再 `ValidateAuditObservation`，失败置 nil 并计数，不影响本轮其余部分。
2. `Sync` 里 `audit := report.Audit; report.Audit = nil`，与 Host 同处剥离（F25）；在控制响应构建完成后、Host 入库旁边入库，不返回错误；
   缓存的 `cloneReport` 不含 Audit。
3. BatchID 去重：内存 LRU（最近 10 000 个）+ `dest_audit_batches`；已见过的批次整批跳过。
4. subject → user_id；rule_id → `source`；未知的丢弃并计数（debug 日志只记计数）。按主键 upsert 累加 `count`、取 min(first)/max(last)，
   三方言各写一份 upsert，测试覆盖。
5. 面板档位是 `off` 或 `hits` 时，送来的 `Usage` 直接丢弃（防旧配置残留）。
6. `PolicyStatus` → 写 `dest_agent_policy.reported_*`（列级更新，不整行 Save）。

### P5　设置与保留

照 #259 的模式（`UISettings` 字段 + `settingDescriptors` 一行 + 「0 = 默认」+ domain 层 `settingOr` 夹取 + 管理端 DTO + `RuntimeEffective`）：

| key | 默认 | 范围 | 说明 |
|---|---|---|---|
| `dest.hit_retention_days` | 30 | 1–365 | A 档 |
| `dest.usage_retention_days` | 7 | 1–30 | B 档，更敏感，上限更低 |
| `dest.list_refresh_hours` | 24 | 6–168 | 远程/分类列表刷新 |
| `dest.policy_apply_min_seconds` | 60 | 30–3600 | 策略下发最小间隔 |
| `risk.dest_block_threshold` | 20 | 1–10000 | 24h 内 `counts_as_risk` 策略命中数达到即出信号 |
| `risk.dest_block_off` | false | — | 进 `OverridableScopeKeys`，可按分组覆盖 |

保留期清理：`runAuditCleanupLoop`（`app.go:1260-1298`）在 `pruneFlagRecords` 之后加 `pruneDestHits`、`pruneDestUsage`、`pruneDestBatches`（1 天），
行为照 `pruneConnectionHistory`（`app.go:1500-1532`）：读不到设置就跳过按时间删并 Warn；已删用户/面板的行照样清。测试照 `app/cleanup_test.go` 里 flag records 那四个。

### P6　风险信号 `dest_block`（只观察）

- `domain/risk.go` 加 `RiskKind` `dest_block`、code `dest_block_repeated`；更新 `RiskKinds()`、`AllRiskCodes()`（它会与前端 locale 对照）。
- evaluator 读 `dest_hits`，只统计 `counts_as_risk = true` 的策略；白名单兜底（`g*`）**永不计入**。
  读接口作为窄接口放进 `risk.Deps`，不破坏 `TestRiskServiceCannotWriteServiceState`（`service/risk/readonly_test.go:38`）。
- `domain/riskpolicy.go` 加 `DestBlockOff`；`ports/policy_settings.go` 同步。
- 只观察，不驱动任何自动停用；进入现有的合并风险铃铛条目，不新增铃铛类型。
- 前端：`utils/riskSignals.ts` 与现有 `risk` tab 的信号列表加这一类。

### P7　管理 API（全部 `adminGroup`；写操作自动进审计中间件；每组路由一条「admin-only」路由测试，照 `risk_center_route_test.go`）

| 方法 | 路径 | 请求 / 响应 |
|---|---|---|
| GET | `/api/admin/dest/lists` | `{items:[{id,name,kind,entry_count,regexp_count,last_fetched_at,last_error,refresh_hours}]}` |
| POST / PUT / DELETE | `/api/admin/dest/lists[/:id]` | 请求 `{name,kind,source_url?,geosite_category?,geosite_attrs?,text?,refresh_hours?}`；响应含解析报告 `{accepted,ignored,samples:[{line,reason}]}`；被策略或白名单引用的列表删除 → 409 `dest_list_in_use` |
| GET | `/api/admin/dest/lists/:id` | 统计 + 前 200 条样本，不返回全量 |
| POST | `/api/admin/dest/lists/:id/refresh` | 202；状态随 GET 返回 |
| GET | `/api/admin/dest/geosite/categories` | `{categories:[{name,count,regexp_count}]}`；未下载过 → 503 `dest_geosite_unavailable` |
| GET | `/api/admin/dest/policies` | `{allow:[Policy],block:[Policy],observe:[Policy]}`（按实际匹配顺序分组） |
| POST / PUT / DELETE | `/api/admin/dest/policies[/:id]` | Policy 全字段 |
| PUT | `/api/admin/dest/policies/order` | `{ids:[int64]}`，只调同一动作内的顺序 |
| GET / POST / DELETE | `/api/admin/dest/exemptions[/:user_id]` | `{user_id,reason}` |
| GET / PUT | `/api/admin/dest/groups/:group_id` | `{mode,stage,list_ids,base_list_id}`（§9） |
| GET | `/api/admin/dest/groups/:group_id/trial-report?days=7` | `{items:[{site,count,sample_dest}]}`，按主域名折叠 |
| POST | `/api/admin/dest/test` | 请求 `{target:"example.com" 或 "1.2.3.4", port?, user_id?}`；响应 `{matched: null 或 {source, action, list_id?, entry}, evaluated_as:"domain"|"ip"}` |
| GET | `/api/admin/dest/hits` | 分页；筛选 `user_id`、`panel_id`、`source`、`from`、`to`、`q`（dest 关键词，LIKE 带 `ESCAPE '!'`，`sqlstore/pagination.go:124`） |
| GET | `/api/admin/dest/usage` | **必须带 `user_id`**，否则 400 `dest_usage_user_required`；可选 `panel_id`、时间 |
| GET | `/api/admin/dest/status` | 每节点 `{panel_id, supported, collect, state, over_limit, issue_code, sniffing_insufficient:[listener]}`；`state` ∈ `unsupported`（非 psp 或无能力）/`pending`（期望摘要 ≠ `reported_sha256`）/`applied`/`rejected` |
| PUT | `/api/admin/servers/:id`（已有） | 加 `audit_collect`，DTO 用 `*string`，省略时保留原值（同 `UpdateChannel` 的「省略不擦除」规矩），只对 psp 生效 |

错误码汇总：`dest_list_parse_failed`、`dest_list_in_use`、`dest_policy_over_limit`、`dest_usage_user_required`、`dest_geosite_unavailable`。
B 档接口只能按人查，是刻意的：它回答「这个被投诉的账号在干什么」，不提供「全站都在访问什么」的浏览面。

### P8　删除时的清理

- 删除分组 → 删 `dest_group_modes` 行（照 `group.Delete` 删 scope settings 的写法，`service/group/group.go:78-86`），策略代次 +1。
- 删除用户 → 删 `dest_exemptions` 行；`dest_hits`/`dest_usage_hourly` 由保留期清理的「孤儿清除」处理。
- 删除策略 → 历史命中保留，界面把找不到的 `source` 显示为「已删除的策略」。

---

## 7. 前端（`web-react`）

1. **新页面「访问控制」** `/admin/access-control`，三道门都要加：`ADMIN_ONLY_ROUTES`（`router/home.ts:21`）、导航项 `adminOnly`
   （放「基础设施」分组，`AdminLayout.tsx:104`，`nav:section.infrastructure`）、`utils/permissions.ts` 新能力 `access.view`（仅 admin）。
   导航 label 键 `nav:admin.access_control`，其余文案放 `admin:access_control.*`（沿用 `risk_center` 的蛇形命名）。
   只维护 zh-CN 与 en-US（zh-TW 构建时生成；parity 测试 `src/i18n/localeParity.test.ts`）。七个命名空间是闭集，不新增命名空间。
2. **五个 tab**：
   - **策略**：三个区块按实际匹配顺序展示——放行 / 拦截 / 仅观察（拦截与仅观察之间插一条说明「白名单分组在这里生效」，链接到白名单 tab）；
     同区块内拖拽调序；每条显示动作、列表、作用域、7 天命中数。编辑框字段：名称、动作、列表多选、行内规则（CIDR、端口、TCP/UDP、BT、私有地址）、
     作用域（所有人 / 指定分组）、「计入风险信号」。放行动作的编辑框提示「对白名单分组同样生效」。
   - **列表**：自定义 / 远程 / 分类三种新建方式；条目数、正则条数、最后刷新、错误；自定义列表显示解析报告。
   - **白名单分组**：见 §9.6。
   - **命中记录**：表格（时间、用户、节点、策略或「白名单：分组名」、目的地、次数）；点用户跳 `/admin/risk?tab=user&id=N`；顶部「测试目的地」工具。
   - **豁免**：用户选择器 + 原因。
3. **写入确认**：策略、列表、豁免、白名单的所有写入都弹确认：「保存后各节点将在约 2 分钟内重启代理内核，现有连接会断开一次。」（去抖 60s + 同步周期）不做影响预测。
4. **预设**（新建策略的一键模板）：

   | 预设 | 内容 | 默认动作 | 计入风险 | 说明 |
   |---|---|---|---|---|
   | 禁止 BT | `protocols: [bittorrent]` | 拦截 | 是 | 只识别明文握手与 uTP，加密 BT、DHT 识别不到 |
   | 禁止发信 | TCP 25、465、587 | 拦截 | 是 | |
   | 禁止访问内网与元数据 | `private: true` | 拦截 | 否 | Xray 节点本身已默认拦截这些地址（F9），此预设在 Xray 上主要产生记录；sing-box 上才起拦截作用 |
   | 加密货币交易所 | v2fly `category-cryptocurrency` | 仅观察 | 否 | |
   | 成人内容 | v2fly `category-porn` | 仅观察 | 否 | 占用 140/256 条正则额度 |

   「高风险金融」**不做预设**（F37），给一段说明引导用自定义或远程列表。分类预设默认「仅观察」，先看一周命中再决定是否拦截。
5. **服务器详情页**（依赖 [server-dashboard-rework-plan.md](server-dashboard-rework-plan.md) WP-D4 先落地；区块放概览 tab）：
   是否支持、采集档位下拉（关闭 / 仅命中 / 命中 + 用量）、策略状态（`/dest/status` 的四值）、嗅探不足的 listener 列表、最近 24h 命中数。
   选「命中 + 用量」而隐私页未启用时显示警告（**只警告，不阻止**）。
6. **用户维度**：在 `views/admin/risk/UserLookupDetail.tsx` 加「访问控制」段：最近命中；B 档节点上该用户的主域名用量 Top 20。
7. 不支持的节点（3X-UI、S-UI、旧版原生节点）在策略页顶部汇总提示「以下节点不会执行这些策略」。
8. 测试：每个 tab 一个 `*.test.tsx`（首行 `// @vitest-environment jsdom`，用 `src/test/adminSaveHarness.tsx`）；路由 admin-only 测试扩展 `router/home.test.ts`。

---

## 8. 隐私与协议页

### 8.1 行为

- 设置 `legal.enabled`，**默认 false**。关闭时一切照旧，公开接口返回 404。
- 两份文档：服务条款、隐私政策。管理员用 Markdown 编辑，每种语言一份；zh-TW 回落 zh-CN，其他语言回落 en-US。
- **版本**：`version` 按 `(kind, locale)` 自增，每次发布生成不可变的新行。`consent_version` 是**全局单调整数**（两种文档、所有语言共用），
  存设置 `legal.consent_version`；任一文档发布时勾选「重大变更，需要用户重新同意」才 +1。改错别字不打扰用户。
- **「我们收集什么」自动生成**：Markdown 里单独一行写 `[[data-collection]]`，前端按这一行把正文切成两段，中间渲染由当前实际设置生成的清单：
  - 订阅访问日志（含 IP）：`sub.sub_log_retention_days`，**0 渲染为「永久保留」**
  - 登录记录：`security.auth_event_retention_days`
  - 连接历史（含 IP）：`risk.connection_retention_days`
  - 设备标识（HWID 摘要）：`risk.hwid_capture_off` 为 false 时列出
  - 访问控制：哪些节点记录「规则命中」、哪些记录「主域名用量」，以及 `dest.hit_retention_days` / `dest.usage_retention_days`
  清单文字走前端 i18n，数据由后端以结构化 JSON 给出。

### 8.2 同意

- **只对 `role = user` 生效**；staff 不弹。
- **注册**：`legal.enabled` 且存在已发布版本 → 注册表单出现必勾项；`registerRequest`（`handler/auth_register.go:25-34`）加
  `accepted_consent_version`，与当前 `consent_version` 不符 → 409 `legal_consent_outdated`（前端刷新后重试）。
- **其他来源的用户**（管理员创建、SSO 首次登录 `user.Service.EnsureSSO`、已有用户）：登录后 profile 返回 `legal_pending: true`，SPA 弹出同意对话框。
  **不做 403 门禁、不阻断订阅**：拒绝只是反复提示（所有者偏好少设准入门槛；阻断订阅会让用户莫名断网）。
- 同意记录 `legal_consents`：主键 `(user_id, consent_version)`、`accepted_at`、`method`（`register`/`prompt`）。**不记 IP。**

### 8.3 实现清单

| 层 | 内容 |
|---|---|
| 表 | `legal_documents`（`id`、`kind` varchar(16)、`locale` varchar(16)、`version` int、`content` text、`consent_bump` bool、`published_at`、`published_by`）；`legal_consents` |
| 设置 | `legal.enabled`（默认 false）、`legal.consent_version`（默认 0） |
| 公开 API | `GET /api/legal/:kind?lang=` → `{version, consent_version, content, data_collection:{…}}`，带 ETag（照 `handler/i18n_public.go:78`）；`GET /api/auth/methods`（`handler/auth_local.go:55-129`）加 `legal:{enabled, consent_version}` |
| 用户 API | `POST /api/user/me/legal/accept {consent_version}`；profile 加 `legal_pending` |
| 管理 API | `GET /api/admin/legal/:kind`（版本列表）；`POST /api/admin/legal/:kind`（发布新版本 `{locale, content, consent_bump}`）。审计中间件会把单个字符串值截到 8192 字符（`middleware/audit.go:250`），审计行里 `content` 只有开头，这是预期 |
| 前端公开路由 | `/legal/terms`、`/legal/privacy`（在 panel 前缀下）；登录页、注册页、用户中心页脚放链接。渲染复用 `components/ReleaseNotes.tsx` 的安全配置：`skipHtml`、`urlTransform` 只允许 http/https、图片降级为 alt 文本 |
| 前端管理 | 设置页新增「隐私与协议」：启用开关、双栏编辑（左 Markdown、右预览）、版本历史、发布对话框（含「重大变更」勾选） |

本页只解决「告知」。条款内容是否符合节点所在地法律，需要所有者自行确认；本计划不提供法律意见。

---

## 9. 白名单模式（按分组：只能访问名单内的目的地，其余一律拒绝）

「拒绝」本身只是 §3.2 第 4c 条一条规则。真正要设计的是下面五件事。

### 9.1 规则

见 §3.2 第 4 条。兜底规则 ID 为 `g<groupID>`，试运行时动作 `observe`，执行时 `block`。白名单列表与基础放行都是 `allow`。

### 9.2 执行保证：不能执行的节点上不给组员开账号（关键）

白名单只在「用户认证所在的那台原生节点、且策略已生效」时成立。3X-UI / S-UI、旧版原生节点、策略超限的节点都做不到。
**只在订阅里隐藏这些节点不够**——组员在那些面板上的 client 仍然存在，旧订阅照样能连。必须在成员同步这一层排除。

1. **一个判断，全仓共用**：新增 `group.Service.Eligible(ctx, node, group) bool`：
   - 分组不是白名单模式 → 等同今天的 `Matches(node, group.TagFilter)`；
   - 是白名单模式 → 另外要求：面板 `Kind == psp`、agent 的 `observed_capabilities` 含 `policy.destination.v1`、`dest_agent_policy.over_limit` 为空。
     （超限只在列表变化时才变，是稳定状态，不会引起 client 反复创建删除。）
   `group.Service` 因此需要注入面板 repo、agent repo、`dest_agent_policy` 读接口。`NodesFor` 改为调 `Eligible`。
2. **F29 的 10 处全部改走这个判断**：`NodesFor` 的 5 处自动生效；`reconcile.go:429`、`node.go:597,1362` 改为注入 `group.Service`；
   `admin_rules.go:239`、`user_me.go:250` 改调 `Eligible`。
3. **守卫测试**：用 `go/ast` 扫 `internal/service`、`internal/transport`、`internal/app`，除 `internal/service/group` 外不得出现对
   `group.Matches` 的调用（防止以后新增第 11 处时绕过）。
4. **触发重新同步**：
   - 分组切换模式、白名单阶段变化 → `user.ResyncGroupMembersInBackground(groupID)`（`service/user/user.go:2126`）；
   - 在 `nodesync.ingestReport`（`nodesync.go:479`，写 `observed_capabilities` 处）比较新旧能力集：`policy.destination.v1` 出现或消失时，对所有白名单分组调用同一函数
     （否则新装或刚升级的节点永远不会给组员开 client）；
   - `dest_agent_policy.over_limit` 由空变非空或反之时同样调用。
   删除 client 走现有同步任务队列，面板离线时排队重试。
5. 分组设置页列出「白名单模式下，以下 N 个节点将不再对本组用户开放」，保存前确认。

### 9.3 DNS 与基础放行

白名单组员的客户端如果有 DNS 查询**经由代理**发出而没被放行，表现是「所有网站都打不开」，而不是「只有名单外的打不开」。

1. 每个白名单分组自带一份「基础放行」列表（`dest_group_modes.base_list_id`，一个 `custom` 列表），创建时预填**实际经代理发出的 DoH 主机**。
   判断规则（实现者开工时按此从**当前**模板重新读，不要照抄本文示例——模板是运营者可改的文件，`internal/seed` 只在文件不存在时写入）：
   - mihomo：`nameserver-policy` 里带 `#<代理组>` 后缀的条目（经所选节点发出）；`default-nameserver`、`proxy-server-nameserver` 是直连引导，不算。
   - sing-box：被 `dns.rules` 引用、且未设 `detour: direct` 的 https 服务器；定义了但没被任何规则引用的不算。
   - 以 2026-09-28 的默认模板为例，只有 `l9f26nnn5d.cloudflare-gateway.com` 一项（`internal/seed/files/templates/default-mihomo.yaml` 的 `nameserver-policy`）。
2. **不放开任意 53 端口**：那等于留一条 DNS 隧道。
3. 「测试目的地」工具对白名单组员要能回答「放行还是拒绝、命中哪条」。

### 9.4 网站依赖：先试运行，再执行

网页的字体、脚本、图片、第三方登录往往来自别的域名，只写主域名会让页面残缺。白名单有两个阶段，**新建时强制从试运行开始**：

| 阶段 | 兜底动作 | 组员体验 | 管理员看到 |
|---|---|---|---|
| 试运行 | `observe`（`psp-watch-g<gid>`） | 一切照常 | 「试运行报告」：过去 N 天**本应被拒绝**的目的地，按主域名折叠、按次数排序，每行一键「加入白名单」 |
| 执行 | `block`（`psp-deny-g<gid>`） | 名单外拒绝 | 被拒记录（同一张命中表） |

- 报告数据就是 A 档命中（兜底规则的 observe 命中），不需要 B 档。
- 切换到执行是界面上的一次显式操作，确认框写明「切换后本组用户访问名单外目的地将被拒绝」。
- 兜底命中量很大（每个后台 App 的心跳都算）：**永不计入风险信号**，受 N4 的聚合上限约束；溢出计入 `Dropped`，报告页显示「本期有 X 条记录因上限未统计」。

### 9.5 切换空窗

- 「不限 → 白名单」或新用户加入白名单分组：最晚约 2 分钟后生效（去抖 60s + 同步周期），core 重启一次；这段时间内组员在已有 client 的节点上**不受限制**。
  本计划接受这个空窗，写进界面帮助，不为它新增门禁。
- 反方向（切回不限、用户移出分组）：空窗期里仍受限，方向安全。
- 服务器详情页显示每台节点「白名单策略：已生效 / 下发中 / 未支持 / 被拒绝」（`/dest/status`）。

### 9.6 界面

- 访问控制页「白名单分组」tab：所有白名单分组、阶段、列表、受影响节点数、试运行报告入口。
- 分组编辑页加「访问模式」段：不限 / 白名单；白名单时显示列表选择、基础放行、阶段、受影响节点。

---

## 10. 已知限制（写进界面帮助，不要让管理员以为是保证）

1. **看不到 URL 路径**：HTTPS 只能看到 SNI/域名；界面一律叫「目的地」，不叫「网址」。
2. **可绕过**：ECH、DoH 后直连 IP、客户端自带的加密隧道。
3. **IP/CIDR 规则只对「以 IP 发起、且未被嗅探改写」的连接生效**；域名解析出的 IP 不参与匹配（Xray `AsIs`；sing-box 未加 `resolve`）。
4. **BT 识别**只覆盖明文握手与 uTP；加密 BT（MSE/PE）、DHT 识别不到。
5. **B 档用量里的目的地是客户端请求的地址**：客户端以 IP 发起时只计到 `(ip)`（F6）。A 档命中不受此限（webhook 带嗅探域名）。
6. **每次策略变更，节点 core 重启一次**（F13），现有连接断开；去抖与摘要比对把次数压到最低。
7. **正则很贵**：每个连接都要逐条跑，上限 256 条。
8. **sing-box 节点首版只执行、不记录**。
9. 嗅探配置不足的 listener 会被节点拒绝部署策略（N1 第 4 条），需要运营者先改入站嗅探配置。

---

## 11. 分阶段、PR 与发版顺序

| 阶段 | 内容 | 仓库 | 依赖 | 发版 |
|---|---|---|---|---|
| **0** | N0 热修 | Node | 无 | Node 修复版（第四段版本号） |
| **1a** | §4.1 下行类型、`ValidateDestinationPolicy`、`PolicyDigest`、conformance | Protocol | 无 | `v0.3.0` |
| **1b** | N1、N1-X、N1-S、N3（Node 侧先在内存维护，上报等 2a）、N7 的 `policy.destination.v1`；`protocol/alias.go` | Node | 1a | — |
| **1c** | P1（除 hits/usage/batches 表）、P2、P3、P5 的 `dest.list_refresh_hours`/`dest.policy_apply_min_seconds`、P7 的列表/策略/豁免/test/status、P8、§7 的策略/列表/豁免 tab + 预设 | PSP | 1a | — |
| **2a** | §4.2 上行类型、`ValidateAuditObservation`、partialReport | Protocol | 1a | `v0.4.0` |
| **2b** | N3 上报、N4、N6、N7 的 `audit.hits.v1` | Node | 2a、1b | Node 版本 |
| **2c** | P1 的 hits/batches 表、P4、P5 的保留与风险设置、P6、P7 的 hits、§7 的命中 tab + 用户维度 + 服务器详情区块 | PSP | 2a | PSP 版本 |
| **3** | §8 隐私与协议页 | PSP | 无 | 可与 1、2 任意并行 |
| **4** | B 档：N5、`audit.usage.v1`、P1 usage 表、P7 usage、§7 用量视图 | 三仓 | 2、3（B 档上线前隐私页已可用） | |
| **5** | §9 白名单模式 | PSP（Node 只加测试） | 1（执行）、2（试运行报告要看命中） | 可与 4 并行 |
| **6** | sing-box 采集（先在真二进制上确认可用的事件来源，再设计） | Node | 2 | |

- 每个阶段在各自仓库一个或多个 PR，分支从各自的 `origin/main` 切，命名 `kazuha/access-control-<阶段>`。
- 1b 与 1c 在 1a 打 tag 前可以用伪版本并行开发（§4.3 第 3 条）；合并前换成正式 tag。
- 每个 PR 描述里贴对应小节的完成判据勾选情况。

---

## 12. 验收场景（`psp-node` Lima VM + 本地 PSP，逐条记录结果）

1. **P0**：升级节点后 `journalctl -u passwall-node` 不再出现 `accepted … email:` 行。
2. **旧节点兼容**：PSP 升级后，未升级的原生节点 config ETag 不变、core 不重启；策略页标注「不支持」。
3. **无策略零扰动**：已升级节点、PSP 无任何策略 → config ETag 与升级前相同（仅 P0 那一次重启）。开了 B 档的节点即使无策略 ETag 也会变，这是预期。
4. **禁止 BT**：客户端跑明文 BT 下载 → 连接被断；命中记录出现，dest 为 tracker 或对端。
5. **禁止发信**：经代理 `nc smtp.gmail.com 25` 失败；命中记录正确。
6. **内网与元数据**：在 **sing-box** 节点上经代理 `curl http://169.254.169.254/` 与 `curl http://169.254.169.254.nip.io/` 失败并有命中；在 Xray 节点上失败（freedom 默认规则，F9）且有命中。
7. **放行优先**：同一域名同时在 allow 与 block 列表 → 放行，无命中记录。
8. **豁免**：豁免用户访问 block 列表里的域名 → 放行，无命中。
9. **分组作用域**：只对分组 A 生效的策略不影响分组 B 的用户。
10. **仅观察**：连接成功，命中记录出现，动作显示「仅观察」。
11. **远程列表**：内容不变的刷新不触发任何节点重启（看 core 部署记录与节点日志）；内容变化只触发一次。
12. **超限**：导入 60 000 条域名 → 节点保留旧策略；状态显示超限；sync issue 出现。
13. **策略被内核拒绝**：人为构造一个内核不接受的配置 → `PolicyStatus.rejected`，界面显示「被拒绝」，节点继续跑旧策略。
14. **重启丢失有界**：命中后立即 `systemctl restart passwall-node` → 最多丢一个同步周期的命中。
15. **重发去重**：人为让 PSP 响应失败一次 → 节点重发同一 BatchID，PSP 计数不翻倍。
16. **webhook 压测**：每秒 1000 个被拦截连接持续 5 分钟，Xray goroutine 不持续增长，agent CPU 记录在案。
17. **B 档**：只在开启的节点上有用量；关闭后节点不再发送；用量接口不带 `user_id` 返回 400。
18. **隐私页**：关闭时 404；启用并发布后注册必须勾选；「重大变更」发布后普通用户登录弹窗、订阅不中断、staff 不弹；「我们收集什么」随设置实时变化；订阅日志保留为 0 时显示「永久保留」。
19. **保留期**：把命中保留设为 1 天，次日整点后旧行被清理。
20. **权限**：operator 访问全部 `/api/admin/dest/*` 返回 403（路由测试 + 实测）。
21. **白名单试运行**：组员访问名单外网站成功，试运行报告出现该主域名；一键加入后报告里消失。
22. **白名单执行**：名单外网站被拒；名单内网站正常；境外域名解析正常（证明基础放行生效）。
23. **白名单执行保证**：组员在 3X-UI 节点上的 client 被删除，订阅里也不再出现该节点；用旧订阅连接失败。
24. **白名单 + 全局拦截**：组员跑 BT 仍被「禁止 BT」拦截。
25. **白名单 + 全局观察（回归 §3.2）**：组员访问全局 observe 列表里、但不在白名单里的站点 → **被拒绝**。
26. **白名单 + 豁免**：豁免用户在白名单分组里不受限制。
27. **能力变化**：给白名单分组覆盖的面板新装一台新版原生节点 → 组员在其上自动获得 client。
28. **守卫测试**：在 `internal/service` 里新增一处直接 `group.Matches` 调用 → 测试失败。
29. 全部仓库 CI 绿：PSP `go test ./...` + 三方言、`npm run test/lint/build`、`smoke:dist`；Node 真二进制 acceptance（N8）；Protocol `go test ./...`、apidiff 步骤。

---

## 13. 测试文件清单

| 仓库 | 文件 | 覆盖 |
|---|---|---|
| Protocol | `protocol/policy_test.go` | 校验规则全集、排序去重、`PolicyDigest`、nil Policy 逐字节不变 |
| Protocol | `protocol/audit_test.go` | 上行校验、上限、与能力绑定、partialReport 含新字段 |
| Protocol | `protocol/conformance/`（新增向量） | Policy 与 Audit 的规范序列化 |
| Node | `internal/core/xray/policy_test.go` | N8 所列 Xray 编译用例 |
| Node | `internal/core/singbox/policy_test.go` | sing-box 编译用例 |
| Node | `internal/core/runtime/runtime_test.go`（追加） | 仅 Policy 变化触发部署；PolicyStatus 状态迁移 |
| Node | `internal/agent/auditsink/sink_test.go` | webhook 接收、丢弃 source、未知规则/用户、上限 |
| Node | `internal/agent/auditlog/filter_test.go` | 行 fixture、背压、降级 |
| Node | `internal/agent/report_test.go`（追加） | Take/Merge、按行裁剪、BatchID 沿用 |
| PSP | `internal/service/destlist/parse_test.go`、`refresh_test.go` | 各格式、超限、摘要 |
| PSP | `internal/service/destpolicy/compile_test.go` | P3 完成判据 |
| PSP | `internal/adapters/sqlstore/dest_*_repo_test.go` | 三方言 upsert、2 MiB entries、保留清理 |
| PSP | `internal/service/nodesync/*_test.go`（追加） | Audit 剥离与入库、BatchID 去重、能力变化触发重同步 |
| PSP | `internal/service/group/eligible_test.go` + `matches_guard_test.go` | §9.2 判断与 go/ast 守卫 |
| PSP | `internal/service/risk/destblock_test.go` | 信号阈值、白名单兜底不计入 |
| PSP | `internal/app/cleanup_test.go`（追加） | 三个 prune |
| PSP | `internal/transport/http/dest_route_test.go`、`legal_route_test.go` | admin-only、错误码、公开接口 |
| PSP | `web-react/src/views/admin/accessControl/*.test.tsx`、`views/legal/*.test.tsx` | 各 tab、隐私页渲染与同意流程 |
