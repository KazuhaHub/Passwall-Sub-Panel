# PSP × 3X-UI 兼容性

PSP 通过 `/panel/api/*` 对接 3X-UI 面板。本文档维护两件事：

1. **每个 PSP 版本对应的最低 / 已测试 3X-UI 版本范围**（升级前查这里）
2. **历史踩过的兼容性坑**（升级 3X-UI 之前看这里，避免重复踩）

## 当前兼容矩阵

| PSP 版本 | 最低 3X-UI | 已实测通过 | 备注 |
|---|---|---|---|
| **v3.6.2+** | **3.2.0** | 3.2.8 | client 适配迁到一级 `/clients/*` API,**硬切 ≥ 3.2.0**;已测上限 2026-06-05 实机抬到 3.2.8,见下文 |
| v3.6.0 – v3.6.1 | 3.1.0 | 3.1.0 | 仍走 inbound-scoped 端点;别跑在 3.2.0 上,先升 PSP 到 v3.6.2 |
| v3.5.1 – v3.5.x | 3.1.0 | 3.1.0 | `/inbounds/list` 把 settings 等改成 nested object,见下文 |
| v3.5.0 | 3.0.x | 3.0.x | 跨 3.1.0 升级会破坏 traffic poll |
| v3.4.x | 3.0.x | 3.0.x | 同上 |
| ≤ v3.3.x | 2.x – 3.0.x | 3.0.x | 历史兼容性见 CHANGELOG |

> 这张表是人看的速查；运行时真相源是 `docs/compat/v3.json`。`min_xui` 和 `max_tested_xui` 两个字段**都已接入运行时**(PSP 按需拉取并据此判 too_old / untested)。

**规则**:
- "最低 3X-UI" = 该 PSP 版本能正常工作的最早 3X-UI 版本(低于这个会破)
- "已实测通过" = 在该版本上真实跑过 traffic poll / reconcile / render 全套
- 任何高于"已实测通过"的 3X-UI 版本都属于**未知风险**——升级前先在一台 panel 上小流量验证

## 历史兼容性事件

### 2026-06-05 / 3X-UI 3.2.8 实机复核 → 已测上限 3.2.7 抬到 3.2.8

**背景**: 上游 2026-06-05 当天发 3.2.8(此前 3.2.7 是 source-verified 抬上来的)。拿一台已升到 **3.2.8 的真实面板**(`panelVersion 3.2.8`、xray `26.6.1`)端到端复核。

**复核结论(代码零改动,LIVE-VERIFIED)**: PSP 触及的端点在 3.2.8 全部仍在、形状未变。实机 smoke-test(临时 inbound + client,测完自清理,全 `success:true`):
- **inbound**: `add` → `get`(回读 `security=tls` / `certificates:[]` / `serverName` / `settings.fingerprint` 全对)→ `update`(内联证书)→ 回读字节级一致 → `del`。
- **client(全部按 panel 唯一 email)**: `add` → `get`(`obj.client.uuid` 在、`email/enable/flow/password/auth/expiryTime/totalGB` 在原位、`inboundIds` 在)→ `update`(改 enable,回读已生效)→ `del`(删后 get 回 not-found)→ `bulkCreate`(`created:2`)→ `bulkDel`(`deleted:2`)。
- **server**: `/server/status`(取到 panelVersion 3.2.8 + xray version)、`/server/getPanelUpdateInfo` 正常。

**3.2.7→3.2.8 变更对 PSP 的影响 = 无**: delta 主要是 multi-node(3X-UI 自家主从节点功能)、client 批量性能、订阅格式。唯一碰 client API 的是 **#4892「scope remote client update/delete to one inbound」**——这是 multi-node 专属;PSP 每个 `(panel,inbound)` 用唯一 email、一个 client 只落一个 inbound,所以该改动对 PSP 是 no-op,已被实机 update/del 正常生效证实。3.2.8 的 client obj 新增 `security/group/comment/createdAt/updatedAt/reverse` 字段是**附加**的,Go json 自动忽略,不影响解析。

### 2026-06-02 / 3X-UI 3.2.6 复核 → 已测上限 3.2.0 抬到 3.2.6

**背景**: 上游 3X-UI 在 3.2.0 之后又出了 3.2.5 / 3.2.6,拿到一台真实 3.2.6 面板复核 PSP 是否需要适配。

**复核结论(代码零改动)**: PSP 触及的整个 3X-UI 面 = `internal/adapters/xui/client.go` 里那 15 个端点
(`/login`、`/panel/api/inbounds/{list,get,add,update,del,setEnable}`、`/panel/api/clients/{add,update,del}`、
`/panel/api/server/{status,getPanelUpdateInfo,updatePanel,installXray,getXrayVersion}`),在 3.2.6 **全部仍在、形状未变**。
逐项实测/核对:

- **序列化**: `/inbounds/list` 仍把 `settings` / `streamSettings` / `sniffing` 返回为 nested object(`allocate` 直接省略),
  `flexJSON` 原样吞下,下游解析器无感。3.1.0 那次破坏不会重演。
- **clientStats**: 仍返回 `email/up/down/total/enable/expiryTime/reset/lastOnline/uuid/subId`,流量轮询所需字段齐全。
- **subId(3.2.5「enforced unique subId per client」)**: 对 PSP 是**非问题**。PSP 构造 `ClientSpec` 时从不设 `SubID`(`sync.go`),
  面板服务端自动生成唯一值——实测一台面板 24 个 client 的 subId 全部互不相同,且每 user 的多 client 共享同一 uuid、各自独立 subId。
- **CSRF(3.2.x 新增)**: Bearer(API token)模式不受 CSRF 约束(实测 Bearer POST = HTTP 200)。
  **注意**: cookie(用户名/密码)模式下 3.2.x 对不安全方法要求 `X-CSRF-Token`——PSP 的 username/password 回退模式**未在 3.2.x 上验证**,
  生产请优先用 API token 模式(PSP 本就 token 优先)。
- **tgId / keepTraffic**: `tgId` 早已按 int64 发(v3.6.2 修);`/clients/del?keepTraffic=0` 与文档「不传 keepTraffic=1 即清流量」一致。

**写路径已实测**: 在 3.2.6 实机上跑了 client add→get→update→del 与 bulkCreate/bulkDel 的一次性 smoke-test(临时 client,测完自清理),全部 `success:true`、删后 get 回 `(record not found)`。配合读路径(traffic poll 的 `/inbounds/list`),3.2.6 端到端验证通过。

**顺带采纳 3.2.x 更省端点(v3.6.3-beta.15)**: traffic poll 改用 `/inbounds/list/slim`(只要 clientStats,丢掉 settings.clients 大字段);按 email 取单 client 走 `/clients/get/{email}`(替代拉整 inbound 再扫);删节点/删用户走 `/clients/bulkDel`、挂节点批量加用户走 `/clients/bulkCreate`(N 次网络调用+N 次 xray 重启收成 1 次)。bulkCreate 的重复项由面板报在 `skipped`(reason 含 "already in use"),据此收养归属。

**这几个新端点对 `min_xui=3.2.0` 下限仍兼容 —— 已在真实 3.2.0 面板(panelVersion 3.2.0、xray 26.5.9)端到端实测确认(2026-06-02)**:
- `/inbounds/list/slim`:3.2.0 **HTTP 200**(存在),clientStats 字段集与 3.2.6 逐字节一致。
- `/clients/get/{email}`:存在,existing→`{client,inboundIds}`,缺失→`" (record not found)"`(与 3.2.6 同,`isClientNotFoundMsg` 命中)。
- `/clients/bulkCreate`:`[{client,inboundIds}]` → `{created,skipped:[{email,reason}]}`,重复项 reason 含 "already in use"(M5 收养命中)。
- `/clients/bulkDel`:裸数组被拒、`{emails,keepTraffic}` → `{deleted:N}`(与 3.2.6 同)。
- 单条 add→get→update→del 全通,subId 仍服务端自动生成。

故 `min_xui=3.2.0` 是诚实的:存在性 + 契约形状都在真实 3.2.0 上验过,不只是「假定一致」。slim 在 3.2.0 即 HTTP 200,故 `ListInboundsSlim` 不加版本兜底(沿用本项目硬切下限、不维护兼容 shim 的一贯做法)。

**另需留意 — 3X-UI 原生多节点(Nodes 功能)**: 3.2.x 起 3X-UI 自带「central panel + 子节点」聚合,会按 email 跨节点聚合客户端流量。
PSP 自己就做多面板聚合(Node = 单个 inbound),若把 PSP 指向一台已配子节点的 central panel,clientStats 会是跨节点聚合值 →
与 PSP 的「一 inbound 一 node」模型冲突。**部署建议**:PSP 对接的 3X-UI 保持单机,不要再套 3X-UI 自己的 node 聚合。

### 2026-05-23 / 3X-UI 3.1.0 → PSP v3.5.0 破坏

**症状**: 任何升级到 3X-UI 3.1.0 的 panel 一旦被 PSP 接入,traffic poll Phase 1 fetch 全失败,日志报
"cannot unmarshal object into Go struct field of type string"。表现为所有 user 流量数据停止更新。

**根因**: 3X-UI 3.1.0 改了 `/panel/api/inbounds/list` 响应:
- `settings` / `streamSettings` / `sniffing` 从 escaped string(`"settings": "{\"clients\":[]}"`) 改成 nested object(`"settings": {"clients":[]}`)
- `allocate` 从 escaped string 改成 `null`
- 写端仍接受 legacy escaped-string 写法,没破坏

PSP `rawInbound` 这四个字段定义为 Go `string`,`json.Unmarshal` 一个 object 进去直接报错。

**修复**: PSP v3.5.1 新增 `flexJSON` 类型(nested object/array 原样捕获,null → "")。**硬切只支持 3X-UI ≥ 3.1.0**——不再维护 3.0.x 兼容路径,因为自用项目可以控制对接版本。

**附带发现**:
- 3.1.0 `clientStats[*]` 多了 `uuid` / `subId` / `lastOnline` 字段——Go json 默认忽略未知字段,PSP 当前 `rawClientTraffic` 不受影响
- `lastOnline` 是个免费的"用户最近活跃时间"素材,未来可以做"在线徽章"
- 新增端点 `/inbounds/list/slim`、`/inbounds/options`、`/clients/list/paged`、`/clients/{add,update,attach,detach}`——PSP 当前不用,但 slim 是未来 traffic poll 优化候选

## 升级 3X-UI 时的检查清单

1. **查本文的兼容矩阵**——目标版本是否在当前 PSP 版本的"已实测通过"范围内?
2. 不在范围内的话: 先升级 PSP 到支持目标 3X-UI 版本的版本
3. 升级**单台** panel 先,观察 5-10 分钟:
   - PSP traffic poll 日志无错(看 `traffic poll panel` warn 行)
   - PSP reconcile axis A 日志无错
   - 一个 user 用真实客户端拉订阅看是否能连
4. 全部正常后再升级其它 panel
5. **不要批量升级**——3X-UI 任意小版本都可能像 3.1.0 这样改 schema

## 当 3X-UI 升级踩到新破坏怎么办

1. 立即记录到本文的"历史兼容性事件"
2. PSP 这边: 走 patch 版本(v3.5.x) 修复兼容性,**同时更新兼容矩阵的"最低 3X-UI"**
3. 更新 `reference_xui_v3_api_break` memory(项目 memory 系统),把"这次踩坑 + 修复方式"沉淀

## 维护 `docs/compat/v<MAJOR>.json` 的 SOP

每个 PSP major 一个 JSON 文件(v3.x 都拉 `docs/compat/v3.json`,v4.x 都拉 `v4.json`)。
这是 v3.6.0-beta.7 引入的 per-major 分文件设计,理由见 ARCHITECTURE.md。

### 何时改 / 改什么

- **新 3X-UI 出 patch 版本(无 API 改动)** ── 在当前 active major 的 JSON 里把
  覆盖你 PSP 版本那条 entry 的 `max_tested_xui` 改成新版本号,顺手更新 `updated_at`
  和 `notes`。commit + push 到 main → 所有该 major 的 PSP 部署 60 秒内自动感知。
- **PSP 发新 minor (比如 v3.6 → v3.7)** ── 在当前 major 的 JSON 加新 entry,
  `psp_min: "v3.7.0"`, `psp_max: "v3.7.99"`,把新 entry 放在 `entries` 数组**最前**
  (first-match-wins 让新版优先匹配)。
- **PSP 发新 major (比如 v3.x → v4.0)** ── 新建 `docs/compat/v4.json`,内部 `major: 4`,
  entries 从只覆盖 v4.0 的 baseline 开始。`v3.json` 保持不动 — 仍跑 v3.x 的部署继续从
  那个文件拿数据。
- **patch 级精度区间(罕见)** ── 比如 v3.6.5-v3.6.8 单独验过 3.2.0,其它 v3.6.x
  还是 3.1.0:在 entries 数组前面插入一条更窄的 entry(narrower 在前 = first-match
  生效),broader 的 baseline 在后面兜底。
- **抬高最低版本(硬切)** ── entry 的 `min_xui` **已接入运行时**(PSP 拉到后按它判
  too_old),但它和代码里的 `version.MinXUI` const **是同一个下限的两处表述,必须相等**:
  `TestMinXUIConstMatchesCompatJSON` 会读这个 JSON 断言"覆盖最新版本那条 entry 的
  `min_xui` == const",**drift 直接让 `go test` 红**——所以改下限时**两处一起改**,忘了
  哪处提交前测试就拦住你(这正是 v3.6.2 漏掉 const、3.1.0 面板没警告那个坑的防忘闸)。
  运行时 `ActiveMinXUI() = max(MinXUI, JSON min_xui)` 只是安全网:正常发版两者相等、max
  是空操作;万一某次 drift 漏过测试发了版,取较高值兜底,下限永远不会被错误降低。

### entries 数组语义

- 每个 entry 是 PSP 版本的**闭区间** `[psp_min, psp_max]`(含两端)
- `psp_min` / `psp_max` 端点**只写 stable semver** `vX.Y.Z`,**不带** pre-release suffix
  (PSP 比对时会丢自己 version 的 `-beta.x` 后缀,把 `v3.6.0-beta.7` 当成 `v3.6.0` 匹配)
- **first-match-wins** ── 数组顺序就是优先级,narrower / 更新的 entry 放前
- 重叠 OK,顺序决定胜出

### compat 范围的拉取时机(reactive)+ 拉不到的故障容错

PSP 启动时先读本地 cache(`<DataDir>/compat-cache.json`)装入上次成功 fetch 的范围,
之后**不主动周期性拉 GitHub**。compat JSON(`docs/compat/v3.json`)只在**探测到某面板版本
对不上当前缓存范围**时才拉(reactive,v3.7.0):

- 版本探测(boot / 每 10 分钟 tick / Servers 页 Test)算出 `CheckXUI(panelVersion) != Supported`
  时才 `RefreshRemoteCompat`(60s 节流——同一 tick 多台对不上合并成一次)再重判;
- upgrade-panel 闸门**强制**拉一次(force,跳节流),保证"是否支持"判定不吃旧缓存。

所以**全兼容的机群对 GitHub 零 compat 流量**;一台太新的面板,等维护者 bump 了 `max_tested`,
PSP 下一次 tick 会自动重拉、自动转 Supported(无需重启/翻页)。

> "有新版可升级"角标是另一条——`RefreshLatestXUI` 查 GitHub release tag,**仍主动** + 30 分钟节流,
> 因为本地没有任何信号能告诉你"上游出了新版本",天生没法 reactive。

任何一步失败(网络挂 / JSON 解析错 / major 不匹配 / 没匹配 entry)只是让 CheckXUI 返回 Unknown,
admin 仍可通过 upgrade-panel 的 `force` 按钮强制升级 — 不是 hard wall。(Unknown 本身也算"对不上",
所以下次探测会自动再尝试拉。)

## v3.6.0 路线图: PSP 自动感知 3X-UI 版本 ── 已完成

| beta | 内容 |
|---|---|
| beta.1 | xui_panels 加 panel_version / xray_version / version_checked_at 三列;adapter GetServerStatus;app.go boot probe + traffic-loop piggyback |
| beta.2 | Servers 页 Version 列 + compat banner + Test 按钮顺手刷版本 |
| beta.3 | 远程升级 3X-UI / Xray 按钮 + smoke probe + 跨大版本 migrate 政策修订 |
| beta.4 | lastOnline 集成到 admin 用户列表"最近活跃"列 |
| beta.5 | dynamic compat (GitHub raw 单文件) + admin force override |
| beta.6 | 5 个 audit 发现 bug 修复 + local compat cache 兜底 |
| beta.7 | dynamic compat schema v2: per-major 分文件 + entries 数组 + psp_min/psp_max 范围 |

这样下次类似 3.1.0 这种破坏可以在 admin UI 提前看到,而不是 traffic poll 静默失败才察觉。
