# 节点审查（目的地策略与访问记录）：任务规划书

- **状态**：规划已定稿（2026-09-28），待派工。**§12 有一项仍待所有者确认**（白名单语义），不阻塞 Phase 0–2。
- **涉及仓库**：Passwall-Protocol（线上类型）、Passwall-Node（执行与采集）、Passwall-Sub-Panel（策略、存储、界面）
- **核对基线**：Passwall-Node `origin/main` `753d3a6`、Passwall-Protocol `v0.2.0`（PSP `go.mod` 钉的版本）、
  PSP `origin/main` `fb12a6c1`。**本地 Passwall-Node 检出落后 origin/main 33 个提交，开工前先 fetch，
  不要照着旧检出读代码。**
- **相关文档**：[psp-node-agent.md](psp-node-agent.md)（协议形状）、
  [psp-node-rootless-observability-plan.md](psp-node-rootless-observability-plan.md) §15（信息最小化）、
  [connection-limits.md](connection-limits.md) §13.7 / §14.3（风控存储与保留的既有规矩）、
  [routing-rule-dsl.md](routing-rule-dsl.md)（规则名称）、
  [server-dashboard-rework-plan.md](server-dashboard-rework-plan.md)（服务器详情页，本功能的节点侧入口挂在那里）

---

## 0. 一句话

PSP 定义「哪些目的地拦截 / 只观察 / 放行」，编进原生节点的 core 路由；节点只回传**命中了哪条规则**
（默认），管理员可以按节点再打开**按主域名聚合的用量**；原始访问日志永远不离开节点。

## 1. 所有者已定的决定（2026-09-27）

| # | 问题 | 决定 |
|---|---|---|
| 1 | 动机 | **防滥用**（BT、SMTP、扫描导致 VPS 被投诉）、**屏蔽高风险金融网站**、**白名单** |
| 2 | 适用范围 | **只做 PSP 原生节点**。3X-UI / S-UI 的 Xray 路由模板归运营者，PSP 不写；它们显示「不支持」 |
| 3 | 记录粒度 | **默认 A 档**（只记规则命中）；**管理员可按节点开启 B 档**（每用户每小时按主域名聚合）。C 档（原始日志）不回传 |
| 4 | 对用户披露 | 做一个**隐私与协议页面**，**默认关闭** |

---

## 2. 已核实的事实（设计的约束从这里来）

每条都对照了源码，写明位置。实现者如发现与此不符，先改本文再动代码。

### 2.1 节点侧（Passwall-Node `origin/main`）

| # | 事实 | 位置 | 对设计的影响 |
|---|---|---|---|
| F1 | Xray 配置**没有 `routing` 段**；`blocked` 黑洞出站已存在但没有任何流量指向它 | `internal/core/xray/compiler.go:64-71`、`:147-150` | 要新增 routing 生成 |
| F2 | **生产环境的 Xray 访问日志正在写到 stdout**：编译器从不设置 `log.access`（生产 wiring `cmd/node/main.go:200-203` 只给了 `APIListen/CoreVersion/AllowRestrictedReality`），而 Xray 在 `log` 对象存在、`access` 为空时把访问日志输出到控制台（`XTLS/Xray-core infra/conf/log.go:31-41`：默认 `AccessLogType: LogType_Console`，只有 `"none"` 关闭）。core 的 stdout/stderr 直接接到 agent 的 stdout（`internal/core/process/supervisor.go:497-498`）→ **journald / Docker 日志里正在积累每一条连接的用户 email 和目的地** | 同左 | **Phase 0 必须先修**，与本功能是否上线无关 |
| F3 | 节点**没有 geosite.dat / geoip.dat**：安装器只解出 core 二进制（`internal/core/install/install.go:186-190`），全仓无 `.dat` 引用 | 同左 | 编译出的配置**不能**用 `geosite:`/`geoip:`/`ext:`；分类要由 PSP 展开成明文列表下发 |
| F4 | **任何 config 或 roster 变化都是 core 全量重启**，没有热重载 | `supervisor.go:403-432`、`internal/core/runtime/runtime.go:277-351` | 策略每改一次，节点上所有连接断一次。策略变更必须去抖、远程列表只在内容摘要变化时才下发 |
| F5 | 流存储表 `stream_documents.stream` 有 `CHECK (stream IN ('config','roster','directives'))`；state schema 为 9；**schema 号一变，远程升级直接拒绝**（`internal/upgrade/helper_linux.go:138`） | `internal/state/sqlite/store.go:23`、`:454` | **不新增 stream**；策略作为 `ConfigBody` 的可选字段下发（body 是 JSON BLOB，加字段不改表） |
| F6 | 旧节点收到不认识的字段会**静默丢弃**，但仍然把 PSP 的 ETag 报成已应用 | `internal/agent/receive.go:145-150` | **必须用 capability 把关**：只对声明了能力的节点下发策略，否则 PSP 会以为「已生效」 |
| F7 | 两个编译器解析 listener config 用 `DisallowUnknownFields` | `xray/compiler.go:311-325`、`singbox/compiler.go:458-472` | 策略**不能**塞进 listener 的 config 里，旧节点会整体拒收 |
| F8 | outbox 表 `kind CHECK (kind IN ('issue','task_result'))` | `store.go:496` | 命中记录**不走 durable outbox**（否则又是 schema bump）；走 `NodeReport` 上的非持久可选字段，先例是 `Host` |
| F9 | 新增的上报字段必须**同时**加进手写的 `partialReport`，否则部分上报里会静默消失 | `passwall-protocol/protocol/report.go:158-169` 的注释 | 协议改动的检查项 |
| F10 | sing-box 1.14 **拒绝**旧式 per-inbound sniffing | `singbox/compiler.go:184-186`、`:493-500` | sing-box 要用 `route.rules` 里的 `action: "sniff"` |
| F11 | Xray 访问日志一行的形状是 `from <src> accepted <dest> [<inTag> -> <outTag>] email: <email>`（`common/log/access.go` 的 `String()`；`app/dispatcher/default.go:489-501` 写 Detour）。**`<dest>` 是客户端请求的目的地（`request.Destination()`，`proxy/vless/inbound/inbound.go:601-603`），不是嗅探出的域名** | Xray-core 上游 | 走 IP 请求的客户端，日志里只有 IP。**所以「命中了哪条规则」要靠出站 tag 表达**（每条规则一个出站），不能靠日志里的目的地去反推 |

### 2.2 PSP 侧

| # | 事实 | 位置 | 影响 |
|---|---|---|---|
| F12 | 期望状态唯一的生成点是 `nodesync.Service.Sync`，`buildConfig` 目前只产出 `Listeners + Core` | `internal/service/nodesync/nodesync.go:127-257`、`:346-401` | 策略在 `buildConfig` 里挂上去 |
| F13 | 节点上客户端的 email 形如 `u{userID}{-k8hex}@{site.email_domain}`，**一个用户在一个面板上可以有多个**（按凭据分区）；roster 同时带 `Subject: usr_{userID}` | `domain/pspclient.go:218-224`、`pkg/clientplan/clientplan.go:195-204,481-526`、`nodesync.go:443-445` | 协议里的规则按 **subject** 指人，节点编译时从 roster 展开成 email。email 不进策略 |
| F14 | PSP 里**没有** Xray 服务端路由生成器；现有规则解析器只服务订阅渲染 | `service/render/singbox.go:553-700` | 新写一个小的目的地匹配编译器，不复用订阅渲染的解析器（语义不同：那边的动作是客户端出站） |
| F15 | 能力字符串以 `node_agents.observed_capabilities` 保存，每次上报覆盖 | `sqlstore/node_agent_repo.go:284-300` | PSP 判断「节点是否支持」就读这里 |
| F16 | 地理库更新是远程拉取的先例（`safehttp`、大小上限、原子替换），**但没有 ETag/摘要持久化的先例** | `service/geo/update.go:108-181`、`app.go:1181-1219` | 列表刷新照它写，摘要比对是新代码 |
| F17 | 风控新表走「具体 repo + 使用方自带窄接口」模式，不进 `ports.Repos` | `sqlstore/flag_record_repo.go`、`risk_signal_repo.go`、`app.go:585-588` | 本功能的表照此办理 |
| F18 | 冻结规格 §15.3：节点**不上报 client live IP** | observability plan §15.3 | 命中记录**不带用户来源 IP**。滥用取证要的是「哪个账号、何时、访问了什么」，不是用户家里的 IP |

### 2.3 规则来源

| # | 事实 | 影响 |
|---|---|---|
| F19 | v2fly 每个 release 都发布 `dlc.dat_plain.yml`（约 3.6 MB，已展开 `include:`）与 `.sha256sum`。条目只有 `domain:`/`full:`/`regexp:` 三种前缀，属性写作后缀 `:@ads` | PSP 下载这一个文件、校验 sha256、按分类取条目即可，**不需要解析 protobuf** |
| F20 | `category-finance` 是**正规**银行与券商（HSBC、Schwab、IBKR…），不是高风险站点；`category-cryptocurrency`（235 条）、`category-bank-cn`、`category-porn`（6660 条）存在；**没有**博彩总分类（只有 `category-betting-ru`） | 「高风险金融」**没有现成分类**可用，主要靠自定义列表和远程列表；预设里不许把 `category-finance` 当「风险金融」 |

---

## 3. 总体结构

```
                 PSP                                          Node
┌───────────────────────────────────────┐        ┌────────────────────────────────────┐
│ 列表（自定义 / 远程URL / v2fly分类）      │        │ ConfigBody.Policy                   │
│   └─ 刷新：safehttp + sha256 摘要        │        │   └─ compiler：                      │
│ 策略（动作 + 列表 + 作用域 + 优先级）     │ config │       routing.rules（Xray）           │
│ 豁免（用户）                             │ ─────▶ │       route.rules（sing-box）         │
│ 节点采集档位（A / A+B）                  │        │       每条规则一个出站 tag             │
│   └─ nodesync.buildConfig 编出 Policy    │        │ supervisor：core stdout 经过滤器       │
│      （仅对声明 capability 的节点）       │        │   访问日志行 → 聚合器（内存）          │
│                                         │ report │   其他行 → 原样转发 agent stdout      │
│ 命中入库（小时桶） ◀─ NodeReport.Audit ◀─ │ ◀───── │ 聚合：命中（A）/ 主域名用量（B）       │
│ 用量入库（小时桶）                        │        │   上限、溢出计数、发送成功后清零        │
│ 风险信号 dest_block（只观察）             │        └────────────────────────────────────┘
│ 保留期清理（audit-cleanup）               │
└───────────────────────────────────────┘
```

**动作只有三个**：

| 动作 | 含义 | Xray 出站 | sing-box |
|---|---|---|---|
| `block` 拦截 | 断开，记一次命中 | `psp-deny-<ruleID>`（blackhole） | `action: "reject"` |
| `observe` 仅观察 | 放行，但记一次命中；新规则上线前先用它看误伤 | `psp-watch-<ruleID>`（freedom） | 路由到出站 `psp-watch-<ruleID>`（direct） |
| `allow` 放行 | 豁免后面所有规则，不记命中 | `direct` | `action: "route", outbound: "direct"` |

---

## 4. 协议（Passwall-Protocol，发 `v0.3.0`）

**加性改动，不升协议版本、不加端点、不加 stream**（F5）。

### 4.1 下行：`ConfigBody.Policy`

```go
type ConfigBody struct {
    Listeners []Listener        `json:"listeners"`
    Coverage  SegmentCounts     `json:"coverage"`
    Core      CoreSelection     `json:"core"`
    // Policy is present only for an agent that advertised CapabilityDestinationPolicy.
    // omitempty is load-bearing: a panel with no policy must mint byte-identical
    // config for every existing node, or upgrading the panel restarts every core.
    Policy *DestinationPolicy   `json:"policy,omitempty"`
}

type DestinationPolicy struct {
    Rules   []DestinationRule `json:"rules"`   // 顺序即优先级，第一条命中生效
    Collect CollectLevel      `json:"collect"` // "hits" | "hits_and_usage"
}

type DestinationRule struct {
    ID             string        `json:"id"`              // [a-z0-9]{1,16}，用作出站 tag 后缀
    Action         RuleAction    `json:"action"`          // "block" | "observe" | "allow"
    Subjects       []SubjectKey  `json:"subjects,omitempty"`        // 空 = 所有人
    ExceptSubjects []SubjectKey  `json:"except_subjects,omitempty"` // 豁免
    Domains        []string      `json:"domains,omitempty"`  // "domain:x" | "full:x" | "keyword:x" | "regexp:x"
    CIDRs          []string      `json:"cidrs,omitempty"`
    Ports          string        `json:"ports,omitempty"`    // "25,465,587" / "6881-6889"
    Network        string        `json:"network,omitempty"`  // "tcp" | "udp" | ""
    Protocols      []string      `json:"protocols,omitempty"` // 仅 "bittorrent"
}
```

- **能力**：`CapabilityDestinationPolicy = "policy.destination.v1"`。**不是**升级能力，不进
  `AgentUpgradeCapabilities`（同 `host.telemetry.v1` 的理由：缺了它不代表协议不兼容）。
- **校验（`validate.go`，发送方和接收方共用）**：规则数 ≤ 256；所有规则 `Domains` 合计 ≤ 50 000 条；
  `regexp:` 合计 ≤ 256 条且必须能被 Go `regexp` 编译；`CIDRs` 合计 ≤ 20 000；Policy 序列化后 ≤ 4 MiB；
  ID 唯一且符合字符集；`Subjects` 与 `ExceptSubjects` 都用现有 `SubjectKey` 校验。
  上限写进 `limits.go`，是**编译期常量**，不进设置（它们保护的是 16 MiB 的 sync 体）。
- **ETag**：沿用现有「规范序列化的 sha256」。`Policy == nil` 时序列化结果必须与 v0.2.0 **逐字节相同**，
  加一条 conformance 测试锁住。

### 4.2 上行：`NodeReport.Audit`

```go
type NodeReport struct {
    // ...existing fields...
    Audit *AuditObservation `json:"audit,omitempty"`
}

type AuditObservation struct {
    Engine     string       `json:"engine"`       // "xray" | "sing-box"
    Coverage   string       `json:"coverage"`     // "complete" | "unsupported"（sing-box 首版）
    Hits       []AuditHit   `json:"hits,omitempty"`
    Usage      []AuditUsage `json:"usage,omitempty"`
    Dropped    uint64       `json:"dropped"`      // 本窗口因上限丢弃的行数
    Unmatched  uint64       `json:"unmatched"`    // 解析失败的访问日志行
}

type AuditHit struct {
    Hour     int64      `json:"hour"`      // 整点 UTC 毫秒
    RuleID   string     `json:"rule_id"`
    Subject  SubjectKey `json:"subject"`   // 由 email 经 roster 反查；查不到的丢弃并计入 Dropped
    Dest     string     `json:"dest"`      // 客户端请求的目的地（域名或 IP），去掉端口，≤ 253 字节
    Port     uint16     `json:"port"`
    Count    uint32     `json:"count"`
    FirstMS  int64      `json:"first_ms"`
    LastMS   int64      `json:"last_ms"`
}

type AuditUsage struct {
    Hour    int64      `json:"hour"`
    Subject SubjectKey `json:"subject"`
    Site    string     `json:"site"`   // eTLD+1；目的地是 IP 时固定为 "(ip)"，不记 IP 本身
    Count   uint32     `json:"count"`
}
```

- **能力**：`CapabilityAuditHits = "audit.hits.v1"`、`CapabilityAuditUsage = "audit.usage.v1"`。
- **非持久**（F8）：与 `Host` 相同，节点重启会丢掉尚未发出的窗口，最多一个同步周期。换来的是不 bump schema、
  远程升级照常可用。**这个取舍要写进协议注释。**
- **上限**：每次上报 `Hits` ≤ 4096 行、`Usage` ≤ 8192 行、`Audit` 序列化 ≤ 512 KiB。
- 必做清单（F9）：字段加进 `partialReport`；新增 `ValidateAuditObservation`，从 `ValidateNodeReportBase`
  拆出去，坏的审计数据不能拖垮整次上报（照 `ValidateHostObservation` 的做法）；
  `fitReportToWire` 的丢弃顺序改为 **Host → Audit.Usage → Audit.Hits → 部分上报**。

---

## 5. 节点实现（Passwall-Node）

### N0　Phase 0 热修：关掉泄漏的访问日志（独立发版，最先做）

- Xray 编译器：`Log["access"] = "none"`，**除非** `Policy != nil && Policy.Collect != ""`。
- 测试（先写）：无策略时生成的配置 `log.access == "none"`；用 `PSP_TEST_XRAY_BIN` 的真二进制跑
  `xray run -test` 通过。
- 发布说明写清：旧版本节点的 journald / Docker 日志里存有连接记录，运营者可按需自行轮转或清理
  （`journalctl --vacuum-time=…`；Docker 已有 3×10 MiB 上限，`compose.example.yaml`）。
- PSP 下发的 config ETag 不变（它算的是 `ConfigBody`），但节点本地编译出的配置摘要变了，
  `runtime.converge` 会重新部署（`runtime.go:330-351`）→ 每个节点升级后 core 重启一次。发布说明注明。

### N1　编译器：路由规则

**Xray**（`internal/core/xray/compiler.go`）

1. `xrayConfig` 加 `Routing map[string]any \`json:"routing,omitempty"\``，无策略时不出现。
2. 每条规则生成：
   ```json
   {"type":"field","ruleTag":"psp-<id>","user":[…],"domain":[…],"ip":[…],"port":"…","network":"…",
    "protocol":["bittorrent"],"outboundTag":"psp-deny-<id>"}
   ```
   - `Subjects` → `user`：从 roster 取这些 subject 的**全部** client username（F13 一人多 email）。
     subject 在 roster 里一个 client 都没有 → 该规则对这台节点**跳过**（不能生成空 `user` 数组，
     空数组在 Xray 里的语义要实测，不能赌）。
   - `ExceptSubjects` → 在该规则**之前**插一条 `user: [...] → direct`。
   - 同一条规则里 `domain` 与 `ip` 同时存在时拆成两条同出站的规则（Xray 一条规则内字段是「与」关系，
     我们要的是「或」）。
3. 出站：每条 `block` 规则一个 `psp-deny-<id>`（blackhole），每条 `observe` 一个 `psp-watch-<id>`（freedom）。
   保留原 `direct`、`blocked`。
4. **嗅探**：只要有任何 `Domains` 或 `Protocols` 规则，所有 inbound 必须嗅探：
   - inbound 没配 sniffing 或 `enabled:false` → 设为
     `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":true}`；
   - 已经 `enabled:true` → 原样保留（运营者的 `destOverride` 语义不动）。
   - `routeOnly: true` 的理由：只让路由看见域名，不改写真实连接目标，不改变现有流量行为。
5. 私有地址预设用明文 CIDR（F3 没有 geoip.dat），由 PSP 下发，编译器不内置。

**sing-box**（`internal/core/singbox/compiler.go`）

1. `route.rules` 最前面：有域名/协议规则时插 `{"action":"sniff"}`（F10）。
2. 规则：`auth_user`（sing-box 按 inbound user `name` 匹配，编译器已用 `Credentials.Username` 作 name，
   `:263-267`）、`domain` / `domain_suffix` / `domain_keyword` / `domain_regex`、`ip_cidr`、`port` / `port_range`、
   `network`、`protocol: ["bittorrent"]`，动作按 §3 表。
3. 前缀映射：`domain:` → `domain_suffix`，`full:` → `domain`，`keyword:` → `domain_keyword`，`regexp:` → `domain_regex`。
4. 首版 sing-box **只拦截、不采集**：`AuditObservation.Coverage = "unsupported"`。采集留给 §10 Phase 5 的调研
  （sing-box 的连接日志格式要先在真二进制上确认，不凭印象写解析器）。

**测试**（先写，表驱动，断言解码后的 JSON，沿用现有 `compiler_test.go` 风格）：
- 无策略 → 无 `routing` 段、`access:"none"`；
- block/observe/allow 三种动作的出站与顺序；
- 一人多 email 展开；subject 不在 roster → 跳过；
- domain+ip 拆分；sniffing 注入与保留；
- `PSP_TEST_XRAY_BIN` / `PSP_TEST_SING_BOX_BIN` 真二进制 `-test` / `check` 通过，
  至少覆盖 50 000 条域名的配置（性能与启动时间在 VM 上记录一次）。

### N2　访问日志过滤与聚合（仅 Xray，`Collect != ""` 时）

1. 编译器：`Collect != ""` 时**不设** `log.access`（走 stdout，F2），并在 core 支持的前提下设
   `log.maskAddress: "full"`（把来源 IP 在 core 里就抹掉；catalog 当前是 Xray 26.6.27 / 26.7.28 / 26.9.9，
   实现前在真二进制上确认该字段被接受）。
2. Supervisor：core 的 stdout 不再直接接 agent stdout，改接一个**逐行过滤器**：
   - 符合访问日志形状（F11）的行 → 交给聚合器，**不转发**；
   - 其他行 → 原样写到 agent stdout（错误日志照旧进 journald）；
   - 行长上限 8 KiB，超长行截断后按「其他行」处理；
   - 过滤器 goroutine 必须有 panic 防护，挂了要退回「全部转发 + 丢弃访问行」，**不能**把 core 的 stdout 堵死
     （管道写满会阻塞 core）。
3. 解析：只关心 `[<inTag> -> <outTag>]` 的 outTag：
   - `psp-deny-<id>` / `psp-watch-<id>` → 命中（A 档），键 `(hour, id, subject, dest, port)`；
   - 其他出站 → 仅 `Collect == "hits_and_usage"` 时计入用量（B 档），键 `(hour, subject, eTLD+1)`，
     用 `golang.org/x/net/publicsuffix`（节点已依赖 `x/net`）；IP 目的地记为 `(ip)`。
   - email → subject：用当前 roster 反查；查不到的计入 `Dropped`。
4. 聚合器：内存 map，键数上限（命中 20 000、用量 50 000），超限计入 `Dropped`，**不**扩容。
   上报成功（收到合法响应）后清掉已发送的键，照 `Host.MarkSent` 的时序（`internal/agent/sync.go:144-154`）。
5. 测试：用真实 Xray 输出的日志行做 fixture（在 `psp-node` Lima VM 上用真二进制抓一批，含 IPv6、UDP、
   无 email、`rejected` 状态）；解析失败计数；上限溢出；管道背压下 core 不阻塞。

### N3　能力声明

- `policy.destination.v1`：编译器支持即声明（两种 engine 都支持拦截）。
- `audit.hits.v1`、`audit.usage.v1`：声明。sing-box 下在 `AuditObservation.Coverage` 里说 `unsupported`，
  不靠「不声明」表达（capability 在启动时算，engine 可以在运行中被 PSP 换掉）。

---

## 6. PSP 实现

### P1　数据模型（`internal/domain` + `sqlstore`，走 F17 的具体 repo 模式）

所有表进 `schemaModels`；`type:text` 列不带 DEFAULT（`TestSchemaNoDefaultOnTextColumns`）；
JSON 切片字段实现 `GormDBDataType() = "text"`；三方言测试都要跑。

| 表 | 列（要点） | 说明 |
|---|---|---|
| `dest_lists` | id、name、kind（`custom`/`remote`/`geosite`）、`source_url` varchar(1024)、`geosite_category` varchar(128)、`geosite_attrs` varchar(128)、`entries` text（规范化后的条目，一行一条）、`entry_count`、`content_sha256` varchar(64)、`refresh_hours`、`last_fetched_at`、`last_error` varchar(512)、`updated_at` | 远程与分类列表的 `entries` 是**缓存**，以 `content_sha256` 判断是否变化 |
| `dest_policies` | id、name、action、`list_ids` text(JSON)、`extra` text（行内规则：CIDR、端口、network、bittorrent）、scope（`all`/`groups`）、`group_ids` text(JSON)、priority、enabled、`counts_as_risk` bool、created/updated | priority 小的先匹配；`allow` 动作的策略**永远**排在所有 block/observe 之前（编译时保证，界面也这样展示） |
| `dest_exemptions` | user_id（PK）、reason varchar(255)、created_by、created_at | 豁免用户：所有 block/observe 对他不生效（`ExceptSubjects`） |
| `dest_hits` | 复合主键 (hour, panel_id, user_id, policy_id, dest varchar(253), port)、count、first_at、last_at | A 档。**无来源 IP**（F18）。全 varchar/整型，照 §14.3 的写法 |
| `dest_usage_hourly` | 复合主键 (hour, panel_id, user_id, site varchar(253))、count | B 档 |
| `panels` 加列 | `audit_collect` varchar(16) NOT NULL DEFAULT `'hits'` | 每节点档位：`off` / `hits` / `hits_and_usage`。默认 A 档（决定 #3） |

规则 ID 的映射：协议里的 `rule.id` = `p{policyID}` 或 `p{policyID}x{n}`（一条策略拆成多条规则时），
入库时反解回 policy_id。

### P2　列表服务 `internal/service/destlist`

1. **自定义**：管理员粘贴文本。接受的行格式（逐行，`#` 注释）：
   - 纯域名 `example.com` → `domain:example.com`
   - `domain:` / `full:` / `keyword:` / `regexp:` 前缀原样
   - Clash 风格 `DOMAIN-SUFFIX,x` / `DOMAIN,x` / `DOMAIN-KEYWORD,x` / `DOMAIN-REGEX,x` / `IP-CIDR,x` / `IP-CIDR6,x`
     （名称沿用 routing-rule-dsl §4.1）
   - hosts 风格 `0.0.0.0 x` / `127.0.0.1 x`
   - CIDR `1.2.3.0/24`
   - 解析报告：接受 N 条、忽略 M 条（带前 20 条被忽略行的行号与原因），界面展示。
2. **远程 URL**：`safehttp` 客户端，超时 60s，响应上限 16 MiB，非 2xx 为错误；解析同上；
   算 `content_sha256`，**只有摘要变化才更新 `entries` 并触发策略重编译**（F4：否则每次刷新都重启全部节点的 core）。
   `If-None-Match` / `If-Modified-Since` 可以加，但判断依据是摘要，不是 304。
3. **v2fly 分类**：下载 `https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat_plain.yml`
   与 `.sha256sum`，**校验 sha256 后**解析（F19）；缓存整份文件到 `<DataDir>`，所有 geosite 列表共用一次下载；
   按 `geosite_category` 取条目，`geosite_attrs`（如 `@ads`、`@!cn`）过滤后缀属性。
4. **刷新循环** `dest-list-refresh`：`safego.GoTracked`，间隔取设置 `audit.list_refresh_hours`（默认 24，下限 6），
   每轮重读设置（live）；单飞；失败保留旧 `entries` 并写 `last_error`。
5. 管理端「立即刷新」按钮，与地理库更新同形（`POST …/refresh`，返回状态，前端轮询）。

### P3　策略编译（`internal/service/destpolicy`，被 `nodesync.buildConfig` 调用）

输入：该面板、该节点 agent 的 `observed_capabilities`、全部启用的策略/列表/豁免、该面板 roster 上的用户及其分组。

1. 节点没有 `policy.destination.v1` → 返回 nil（F6）。**节点有能力但没有任何启用的策略**且档位不是 B → 也返回 nil，
   保证 ETag 不变。
2. `scope=groups` → `Subjects` = 这些分组里、在本面板有 client 的用户的 `usr_{id}`；一个都没有则这条规则不下发。
3. 豁免用户 → 每条 block/observe 规则的 `ExceptSubjects`。
4. 顺序：所有 `allow` 策略（按 priority），然后 block/observe（按 priority）。
5. 超过 §4.1 的任何上限 → 这台节点**不下发新策略**（保留上一版），记一条 sync issue，管理端策略页显示
   「策略超出节点上限：域名 57 312 / 50 000」。不能静默截断：截断等于悄悄放行。
6. **去抖**：策略/列表/豁免/分组成员变化只标记「待重编译」，由 nodesync 下一轮统一编；同一节点两次策略变更之间
   至少间隔 `audit.policy_apply_min_seconds`（默认 60）。理由见 F4。
7. **测试**（先写）：无策略时 `ConfigBody` 规范序列化与改动前逐字节相同；无能力节点不带 Policy；
   分组展开；豁免；allow 优先；超限保留旧版；同一输入两次编译 ETag 相同。

### P4　上报入库

1. `nodesync.ingestReport` 之后、host telemetry 之前处理 `report.Audit`；`ValidateAuditObservation` 失败只丢审计部分
   （同 host 的处理，`handler/node_sync.go:163-187`）。
2. subject → user_id；rule_id → policy_id；未知的丢弃并计数（debug 日志只记计数）。
3. 按主键 upsert 累加 count、取 min(first)/max(last)，三方言各写一份 upsert，测试覆盖。
4. 档位是 `off` 或 `hits` 的节点送来的 `Usage` 直接丢弃（防旧配置残留）。

### P5　设置与保留

新设置（照 #259 的模式：`UISettings` 字段 + descriptor + `0 = 默认` + domain 层 `settingOr` 夹取 + 管理端 DTO）：

| key | 默认 | 范围 | 说明 |
|---|---|---|---|
| `audit.hit_retention_days` | 30 | 1–365 | A 档命中保留 |
| `audit.usage_retention_days` | 7 | 1–30 | B 档用量保留（更敏感，上限更低） |
| `audit.list_refresh_hours` | 24 | 6–168 | 远程/分类列表刷新 |
| `audit.policy_apply_min_seconds` | 60 | 30–3600 | 策略下发最小间隔 |
| `risk.dest_block_threshold` | 20 | 1–10000 | 24h 内 `counts_as_risk` 策略命中数达到即出风险信号 |
| `risk.dest_block_off` | false | — | 可按分组覆盖（进 `OverridableScopeKeys`） |

保留期清理：`runAuditCleanupLoop` 在 `pruneFlagRecords` 之后加 `pruneDestHits`、`pruneDestUsage`，
行为照 `pruneConnectionHistory`（`app.go:1500-1532`）：读不到设置就跳过按时间删并 Warn；已删用户/面板的行照样清。

### P6　风险信号 `dest_block`（只观察）

- `domain/risk.go` 加 `RiskKind` `dest_block`，code `dest_block_repeated`；evaluator 读 `dest_hits`（窄接口放进
  `risk.Deps`，不破坏 `TestRiskServiceCannotWriteServiceState`）。
- 只统计 `counts_as_risk = true` 的策略（BT、SMTP 这种「在滥用」的命中才算；广告拦截命中不算）。
- 只观察，**不**驱动任何自动停用（与 v3 所有信号一致）。自动进入铃铛的合并风险条目，不新增铃铛类型。
- 风控中心正在按 2026-09-28 的决定改版（待处理 / 在线 / 记录 / 策略）。本信号只接入信号层，界面随改版走，
  **不要**在旧版风控页上另开 tab。

### P7　管理 API（全部 `adminGroup`，写操作走审计中间件，每组路由一条「admin-only」路由测试）

| 方法 | 路径 | 用途 |
|---|---|---|
| GET/POST/PUT/DELETE | `/api/admin/dest/lists[/:id]` | 列表 CRUD；GET 单条返回前 200 条样本 + 统计，不返回全量 |
| POST | `/api/admin/dest/lists/:id/refresh` | 立即刷新 |
| GET | `/api/admin/dest/geosite/categories` | 已缓存的 v2fly 分类名与条目数（供选择器） |
| GET/POST/PUT/DELETE | `/api/admin/dest/policies[/:id]` | 策略 CRUD；PUT 支持批量改 priority |
| GET/POST/DELETE | `/api/admin/dest/exemptions[/:user_id]` | 豁免 |
| POST | `/api/admin/dest/test` | 输入 `{domain 或 ip, port, user_id?, panel_id?}`，返回「会命中哪条策略、动作是什么」。误伤排查必备 |
| GET | `/api/admin/dest/hits` | 分页；筛选 user、panel、policy、时间、dest 关键词（LIKE 用 `ESCAPE '!'`） |
| GET | `/api/admin/dest/usage` | B 档，同上；**只按单个用户查**（必须带 user_id），不提供全站浏览 |
| GET | `/api/admin/dest/status` | 每个节点：能力、档位、策略是否已生效（比对 `node_agent_streams` 的 config ETag）、超限信息 |
| PUT | `/api/admin/servers/:id` 已有 | 加 `audit_collect` 字段 |

B 档接口只能按人查，是刻意的：它回答「这个被投诉的账号在干什么」，不提供「全站都在访问什么」的浏览面。

---

## 7. 前端（`web-react`）

1. **新页面「访问控制」** `/admin/access-control`（admin-only：加进 `ADMIN_ONLY_ROUTES` 与 `AdminLayout` 的
   `adminOnly` 导航项；放在「基础设施」分组），四个 tab：
   - **策略**：列表按实际匹配顺序展示（allow 永远在上方的独立区块）；拖拽调 priority；每条显示动作 chip、
     列表、作用域、7 天命中数。编辑对话框：名称、动作（拦截 / 仅观察 / 放行）、列表多选、行内规则
     （CIDR、端口、TCP/UDP、BT 协议）、作用域（所有人 / 指定分组）、「计入风险信号」开关。
     保存时如果会改变节点配置，确认框写明：**「保存后各节点将在 1 分钟内重启代理内核，现有连接会断开一次。」**
   - **列表**：自定义 / 远程 / 分类 三种新建方式；显示条目数、最后刷新、错误；自定义列表的解析报告。
   - **命中记录**：表格（时间、用户、节点、策略、目的地、次数），点用户进用户抽屉；顶部「测试目的地」工具。
   - **豁免**：用户选择器 + 原因。
2. **预设**（新建策略时的一键模板，放在「策略」tab 顶部）：
   | 预设 | 内容 | 默认动作 | 计入风险 |
   |---|---|---|---|
   | 禁止 BT | `protocol: bittorrent` | 拦截 | 是 |
   | 禁止发信 | TCP 25、465、587 | 拦截 | 是 |
   | 禁止访问内网与元数据 | 10/8、172.16/12、192.168/16、127/8、169.254/16、100.64/10、fc00::/7、fe80::/10、::1/128 | 拦截 | 否 |
   | 加密货币交易所 | v2fly `category-cryptocurrency` | 仅观察 | 否 |
   | 成人内容 | v2fly `category-porn` | 仅观察 | 否 |
   「高风险金融」**不做预设**（F20）：界面给一段说明，引导管理员用自定义列表或远程列表。
   分类预设默认「仅观察」，让管理员先看一周命中再决定是否拦截。
3. **服务器详情页**（见 server-dashboard-rework-plan.md）加「访问控制」区块：节点是否支持、采集档位下拉
   （关闭 / 仅命中 / 命中 + 用量）、策略是否已在该节点生效、最近 24h 命中数。
   选「命中 + 用量」时，如果隐私页未启用，显示警告（**只警告，不阻止**，见 §8）。
4. **用户抽屉**：加「访问控制」段：最近命中；B 档开启的节点上该用户的主域名用量 Top 20。
5. 不支持的节点（3X-UI、S-UI、旧版原生节点）在策略页顶部汇总提示「以下节点不会执行这些策略」。
6. i18n：新命名空间不能加（七个命名空间是闭集），全部放 `admin:accessControl.*`；中英同时加。

---

## 8. 隐私与协议页

### 8.1 行为

- 设置 `legal.enabled`，**默认 false**（决定 #4）。关闭时一切照旧，公开接口返回 404。
- 两份文档：**服务条款**、**隐私政策**。管理员用 Markdown 编辑，每种语言一份（至少 zh-CN、en-US，
  其他语言回落到 en-US）。
- **发布即成版本**：每次发布生成不可变的新版本（version 自增）。发布时勾选「重大变更，需要用户重新同意」
  才会提高 `consent_version`；改错别字不打扰用户。
- **「我们收集什么」自动生成**：Markdown 里写一行占位 `[[data-collection]]`，前端在该处渲染由**当前实际设置**
  生成的清单：
  - 订阅访问日志（含 IP）保留 `sub_log_retention_days` 天
  - 连接历史（含 IP）保留 `risk.connection_retention_days` 天
  - 设备标识（HWID 摘要）是否采集（`risk.hwid_capture_off`）
  - 登录记录（`auth_events`）
  - 访问控制：哪些节点记录「规则命中」、哪些节点记录「主域名用量」，各保留多少天
  这样政策正文不会和实际配置脱节。清单文字走前端 i18n，数据由后端给结构化 JSON。

### 8.2 同意

- **注册**：`legal.enabled` 且存在已发布版本 → 注册表单出现必勾的「我已阅读并同意《服务条款》《隐私政策》」，
  `registerRequest` 加 `accepted_consent_version`，与当前版本不符返回 409（页面刷新后重试）。
- **已有用户 / 管理员创建 / SSO 首次登录**：登录后 profile 返回 `legal_pending: true`，SPA 弹出同意对话框。
  **不做 403 门禁、不阻断订阅**：拒绝同意只是反复提示。理由：所有者偏好少设准入门槛；阻断订阅会让用户
  莫名断网，而且这不是强制执行合规的正确位置。
- 同意记录 `legal_consents`：(user_id, consent_version) 主键、accepted_at、method（`register`/`prompt`）。
  **不记 IP**。

### 8.3 实现清单

| 层 | 内容 |
|---|---|
| 表 | `legal_documents`（kind、locale、version、`content` text、`consent_version`、published_at、published_by）；`legal_consents` |
| 公开 API | `GET /api/legal/:kind?lang=` → `{version, consent_version, content, data_collection:{…}}`，带 ETag；`/api/auth/methods` 加 `legal: {enabled, consent_version}` |
| 用户 API | `POST /api/user/me/legal/accept {consent_version}`；profile 加 `legal_pending` |
| 管理 API | `GET/POST /api/admin/legal/:kind`（列出版本 / 发布新版本）。审计中间件会把请求体截断到 8192 字符，审计行里只看得到开头——在文档里写明，不改中间件 |
| 前端公开路由 | `/legal/terms`、`/legal/privacy`（在 panel 前缀下，登录页、注册页、用户中心页脚放链接）。渲染复用 `ReleaseNotes.tsx` 的安全配置：`skipHtml`、`urlTransform` 只允许 http/https、图片降级为 alt 文本 |
| 前端管理 | 设置页新增「隐私与协议」：启用开关、双栏编辑（左 Markdown、右预览）、版本历史、发布对话框 |

本页只解决「告知」。条款内容需要所有者自行确认是否符合节点所在地（加拿大等）的法律要求，本计划不提供法律意见。

---

## 9. 已知限制（写进界面帮助文字，不要让管理员以为是保证）

1. **看不到 URL 路径**：HTTPS 只能看到 SNI/域名；界面一律叫「目的地」，不叫「网址」。
2. **可绕过**：ECH、DoH 后直连 IP、客户端自带的加密隧道。IP 规则能兜一部分。
3. **命中记录里的目的地可能是 IP**：客户端以 IP 发起请求时，日志只有 IP，嗅探出的域名不进访问日志（F11）；
   但**命中哪条规则是准确的**（按出站 tag 判断）。
4. **每次策略变更，节点 core 重启一次**（F4），现有连接断开；已用去抖与摘要比对把次数压到最低。
5. **正则规则很贵**：每个连接都要跑一遍，所以上限是 256 条。
6. **sing-box 节点首版只拦截、不记录**。

---

## 10. 分阶段与派工

跨仓库的发布顺序固定为：**Protocol 发版 → Node 发版（声明能力）→ PSP 升级依赖**。
PSP 必须在「所有节点都是旧版」时照常工作（策略页显示「节点不支持」）。

| 阶段 | 内容 | 仓库 | 依赖 | 可并行 |
|---|---|---|---|---|
| **0** | N0：`log.access = "none"` 热修 | Node | 无 | 立刻做，单独发一个修复版（第四段版本号） |
| **1a** | §4.1 协议类型 + 校验 + conformance（下行部分） | Protocol | 无 | 与 1c 并行 |
| **1b** | N1 编译器（Xray + sing-box 拦截）+ N3 的 `policy.destination.v1` | Node | 1a | |
| **1c** | P1（除 hits/usage 表）、P2、P3、P5 的列表/策略设置、P7 的列表/策略/豁免/test/status、§7 的策略/列表/豁免 tab + 预设 | PSP | 1a 的类型（可先用 replace 指向本地） | 与 1b 并行 |
| **2a** | §4.2 上行类型 + 校验 + `fitReportToWire` | Protocol | 1a | |
| **2b** | N2 过滤器 + 聚合（仅命中）、N3 的 `audit.hits.v1` | Node | 2a、1b | |
| **2c** | P1 hits 表、P4、P5 保留、P6 风险信号、P7 hits、§7 命中 tab + 用户抽屉 + 服务器详情区块 | PSP | 2a | 与 2b 并行 |
| **3** | §8 隐私与协议页 | PSP | 无 | 可与 1、2 任意并行 |
| **4** | B 档：N2 用量聚合、`audit.usage.v1`、P1 usage 表、P7 usage、§7 用量视图 | 三仓 | 2、3（B 档上线前隐私页已可用） | |
| **5** | sing-box 采集调研 → 实现 | Node | 2 | |
| **6** | 白名单模式（见 §12） | 三仓 | 所有者确认 | |

每个阶段一个或多个 PR，分支从各自仓库的 `origin/main` 切，命名 `kazuha/access-control-<阶段>`；
**每个 WP 先写失败测试再实现**。

---

## 11. 验收场景（在 `psp-node` Lima VM + 本地 PSP 上逐条跑，记录结果）

1. **P0**：升级节点后 `journalctl -u passwall-node` 不再出现 `accepted … email:` 行。
2. **旧节点兼容**：PSP 升级后，未升级的原生节点 config ETag 不变、core 不重启；策略页标注「不支持」。
3. **无策略零扰动**：已升级节点、PSP 无任何策略 → config ETag 与升级前相同（仅 P0 那一次重启）。
4. **禁止 BT**：用户在客户端跑 BT 下载，连接被断；命中记录出现一条 `禁止 BT`，dest 为 tracker/peer 地址。
5. **禁止发信**：`nc smtp.gmail.com 25` 经代理失败；命中记录正确。
6. **内网与元数据**：经代理 `curl http://169.254.169.254/` 失败。
7. **放行优先**：同一域名同时在 allow 与 block 列表 → 放行，无命中记录。
8. **豁免**：豁免用户访问 block 列表里的域名 → 放行，无命中。
9. **分组作用域**：只对分组 A 生效的策略不影响分组 B 的用户。
10. **仅观察**：连接成功，命中记录出现，动作显示「仅观察」。
11. **远程列表**：内容不变的刷新不触发任何节点重启（看 `core_deployment` 与节点日志）；内容变化只触发一次。
12. **超限**：导入 60 000 条域名 → 节点保留旧策略，策略页显示超限，sync issue 出现。
13. **重启丢失有界**：命中后立即 `systemctl restart passwall-node` → 最多丢失一个同步周期的命中，`Dropped` 或缺口可解释。
14. **B 档**：只在开启的节点上有用量数据；关闭后节点不再发送；用量接口不带 user_id 返回 400。
15. **隐私页**：关闭时 404；启用并发布后注册必须勾选；「重大变更」发布后老用户登录弹窗、订阅不中断；
    「我们收集什么」随设置变化实时变化。
16. **保留期**：把保留设为 1 天，次日整点后旧行被清理。
17. **权限**：operator 访问全部 `/api/admin/dest/*` 返回 403（路由测试 + 实测）。
18. 全部仓库各自 CI 绿；PSP `go test ./...` + 三方言、`npm run test/lint/build`、`smoke:dist`；
    Node 真二进制 acceptance（`PSP_TEST_XRAY_BIN`、`PSP_TEST_SING_BOX_BIN`）。

---

## 12. 待所有者确认

**白名单指哪一种？**（本计划的 Phase 0–4 已包含 a 与 b；c 列为 Phase 6，等确认）

| | 含义 | 本计划中的位置 |
|---|---|---|
| a. 放行例外 | 某域名虽在拦截列表里，但放行（修误伤） | 已含：`allow` 动作，永远优先 |
| b. 用户豁免 | 某些用户不受拦截约束 | 已含：`dest_exemptions` |
| c. 白名单模式 | 某分组**只能**访问列表内目的地，其余全拒 | 未含。实现方式：该分组的规则末尾加一条 `user: [...] → psp-deny-default`；注意这会让该分组所有未列出的流量（含 DNS、系统更新）被拒，需要一套「基础放行」预设，设计另写 |

另外两处等所有者拍板时顺带确认：

- A 档默认对**所有**原生节点开启（`panels.audit_collect` 默认 `hits`），但前提是存在至少一条启用的策略；
  没有策略时节点上什么都不采。是否认可？
- 命中保留默认 30 天、用量默认 7 天，是否认可？
