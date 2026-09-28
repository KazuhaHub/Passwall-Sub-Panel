# 服务器看板整改：任务规划书

- **状态**：规划已定稿（2026-09-28），待派工
- **依据**：[psp-node-rootless-observability-plan.md](psp-node-rootless-observability-plan.md) §12（前端规格，已冻结）、
  [react-data-freshness-plan-review.md](react-data-freshness-plan-review.md) §6、§7.1、§10.2（查询缓存迁移规则）
- **触发**：生产截图（加拿大节点「主机指标」弹窗 → 性能 tab）里，图例压住 X 轴标签，X 轴是原始 UTC ISO 串，
  而弹窗顶部「Collected」又是浏览器本地时间；host 节点画出一条无意义的 0.1%「Container capacity」平线；
  服务器列表每行 6–7 个操作控件
- **范围**：只动 PSP 仓库（后端 handler 两处 + `web-react`）。**不动 Passwall-Node，不改线上协议。**

> 冻结规格 §12.2 写的本来就是「服务器详情」四个 tab。现在的 `NodeMetricsDialog` 是一个过渡形态，
> 本计划把它落成规格原意的详情页。凡是和 §12 不一致的地方，本文逐条写明理由（见 §6）；
> 按那份规格的规矩，偏离要改规格、不能只改实现。

---

## 1. 问题清单（每条都已对照源码核实）

路径相对 `web-react/src`，除非另写。

### 1.1 图表组件 `components/NodeMetricsChart.tsx`

| # | 问题 | 位置 | 根因 |
|---|---|---|---|
| C1 | 图例压住 X 轴标签 | `:126` | `legend` 没给位置，ECharts 6.1.0 默认 `bottom: 15`（`node_modules/echarts/lib/component/legend/LegendModel.js:242-250`），落在 `grid.bottom: 40` 里面。对照：`TrafficChart.tsx:58-62` 显式写了 `legend.top: 4`，所以它没这个问题 |
| C2 | X 轴是原始 ISO UTC 串 | `:127-132` | `xAxis.type: 'category'`，`data: times` 直接是 `2026-09-28T00:06:16.265Z` |
| C3 | 时间间距不按真实时间 | 同上 | 分类轴每个点等距；缺桶时两侧的点被画成相邻 |
| C4 | Y 轴没单位 | `:133-137` | 单位格式化只用在 tooltip（`valueFormatter`），`yAxis.axisLabel` 没有 `formatter` |
| C5 | tooltip 硬编码英文、时间是原始串 | `:112-124` | `` `coverage: ${coverage[index]}s` ``，表头是 `times[index]` |
| C6 | 只监听 `window` resize | `:83-93` | 详情页里侧栏折叠、tab 切换时容器变宽不会触发 |
| C7 | 无可访问名称 | `:155` | `<div role="img">` 没有 `aria-label` |

### 1.2 弹窗 `views/admin/NodeMetricsDialog.tsx`

| # | 问题 | 位置 |
|---|---|---|
| D1 | host scope 节点也画「Container capacity」 | `:303`，无条件加入 `cgroup_cpu_capacity_percent` 序列 |
| D2 | 时间范围 chip 在所有 tab 都显示，只有「性能」用它 | `:139-157` 在 tab 条件之外 |
| D3 | 「网络」tab 没有任何历史图 | `:331-347`；而 history 接口**已经**返回 `rx_bps`/`tx_bps`/`link_utilization_percent`/`tcp_retrans_percent`（`internal/transport/http/handler/admin_node_metrics.go:246-283`），`getNodeInterfaceHistory`（`api/nodeMetrics.ts:133`）全仓零调用 |
| D4 | 「诊断」tab 写着「远程诊断将在第二阶段提供」，但远程诊断早已有独立弹窗 | `:391` 的 `diagnosticsPhaseTwo`；`NodeDiagnosticsDialog.tsx` 已完整实现 |
| D5 | 概览「带宽监控」只显示出站 | `:251` 只用 `tx_bps` |
| D6 | 诊断 tab 显示未翻译的原始值（`healthy`） | `:376-377` |
| D7 | 「Collected」用浏览器时区，不用面板时区 | `:209` `toLocaleString()` |
| D8 | 刷新轮询关不掉 | `refresh()` `:83-101` 的 30 秒循环没有 abort，关掉弹窗仍在跑 |
| D9 | 弹窗没有 `key`，切换服务器会带着上一台的 tab/range 状态 | `ServersView.tsx:1974` |

### 1.3 后端

| # | 问题 | 位置 |
|---|---|---|
| B1 | 「Deployment」chip 实际显示的是 resource scope | `admin_node_metrics.go:120-121`：`deployment` 和 `resource_scope` **都**取自 `snapshot.Observation.ResourceScope`。协议里 `Deployment`（systemd/docker/manual/unknown）是独立字段（`github.com/KazuhaHub/passwall-protocol@v0.2.0/protocol/host.go:69`，PSP `go.mod` 钉的就是这个模块） |
| B2 | 没有单台服务器的读取接口 | `router.go:712-718` 只有 `GET /servers`（列表）和 `PUT`/`DELETE /servers/:id` |
| B3 | 前端无从知道有哪些网卡 | `GET …/node-metrics/interfaces` 要求 `interface` 参数，而没有任何接口列出名字；repo 里已有 `InterfaceNames`（`internal/ports/repos.go:733`），只在 service 内部用 |
| B4 | interfaces 接口忽略 `resolution` | `admin_node_metrics.go:303` 只把 `from/to` 传给 service，却把请求的 resolution 原样回显 |

### 1.4 服务器列表 `views/admin/ServersView.tsx`

- 行操作（`:1561-1623`）：测试（文字按钮）、编辑、删除、指标、诊断、更多，最多 6 个控件，删除夹在中间。
- `:1580-1586` 那段「kebab 菜单」注释放在了指标按钮上方，位置错了。
- 指标按钮的 tooltip 用 `'\n'` 拼多行（`:2653-2665`），但 Tooltip 没设 `whiteSpace: 'pre-line'`，实际显示成一行。
- `NodeMetricsDialog` 静态 import（`:3`）→ 打开服务器列表就加载 ECharts chunk。

---

## 2. 目标与非目标

**目标**

1. 原生节点有一个真正的详情页 `/admin/servers/:id`，四个 tab，对齐冻结规格 §12.2。
2. 所有时间轴按**面板时区**显示，和页面其他地方一致。
3. 图表不再重叠、有单位、缺数据处断线、tooltip 可翻译。
4. 列表行操作精简到「一眼能看完」。
5. 节点指标的读取迁入查询缓存（按 data-freshness 规则）。

**非目标（本次不做）**

- 规格 §12.2 里 API 目前还没有的字段：iowait/steal、load 1/5/15、swap、packets/errors/dropped、
  TCP established、conntrack、congestion control 列表、Agent/Core FD。这些要先扩 history/current 的
  DTO（数据已在快照里的先确认），单开任务。见 §7「后续」。
- 不改 Passwall-Node，不改协议。
- 不做 3X-UI / S-UI 的主机指标（它们没有 agent）。
- 不做自动每秒刷新（规格 §12.1 明确禁止）。

---

## 3. 已定的设计决定

| 决定 | 内容 | 理由 |
|---|---|---|
| 详情页而非弹窗 | 路由 `/admin/servers/:id`，React.lazy | 规格 §12.2 原意；弹窗装四个 tab + 三张 280px 图，必须滚动，且不能分享链接 |
| tab 和范围进 URL | `?tab=overview\|performance\|network\|diagnostics&range=1h\|24h\|7d\|30d\|90d` | 沿用 `RiskCenterView.tsx:48-70` 的 `useSearchParams` 模式：打开详情 push，切 tab/范围 replace |
| 所有服务器类型都能进详情页 | 3X-UI/S-UI 详情页只显示基本信息 + 「此类型面板不提供主机指标」 | 名字可点击对所有行一致；不做「有的能点有的不能点」 |
| 权限 | 整页 admin-only | 路由本来就在 `ADMIN_ONLY_ROUTES`（`router/home.ts:21-29`，`home.test.ts:21-26` 已断言 `/admin/servers/1` 是 admin-only），所有后端接口都在 `adminGroup` |
| 远程诊断并入「诊断」tab | 删除 `NodeDiagnosticsDialog` 的独立入口 | 同一件事两个入口、文案还互相矛盾（D4） |
| 行内只留三个控件 | 健康点（进详情）、测试、更多 | 见 WP-D6 |
| 图表时间轴 | `xAxis.type: 'time'`，数据用 `[epochMs, value]` 对 | 解决 C2、C3；和缺口断线规则配合，见 WP-D2 |
| 时区 | 面板时区（`useSiteStore(s => s.timezone)`，空则浏览器时区），图表下方用 `TzHint` 标注 | 图表轴放不下「面板时间（浏览器时间）」双显，`TzHint.tsx` 就是为此存在的（`DashboardView.tsx:314` 在用） |

---

## 4. 工作包

依赖顺序：**D1 → (D2 ∥ D3) → D4 → D5 → D6 → D7**。D2 与 D3 可以并行；D1 是纯后端，可以最先合。
每个 WP 都**先写失败的测试，再写实现**。

### WP-D1　后端：单台读取、网卡列表、两个小 bug

**文件**：`internal/transport/http/handler/admin_servers.go`、`admin_node_metrics.go`、`router.go`、
对应 `_test.go`；`internal/app/node_metrics_routes_test.go`

1. **`GET /api/admin/servers/:id`**（adminGroup）
   - 复用 `List` 的装配：`h.toServerDTOWithAgent(panel, agent, policy)` + `applyNodeMetrics(...)`
     （`admin_servers.go:315-383`）。把这段抽成一个 `h.decorate(ctx, panels)` 之类的函数，List 和 Get 共用，
     **不要复制第二份**。
   - 404 用 `domain.ErrNotFound` 包装，走 `respondError`。
   - 注意 gin v1.12 下 `/servers/node-releases`、`/servers/sui-release`、`/servers/compat-status` 这些静态段和
     `/servers/:id` 共存：加一个路由测试，断言三条静态路径仍命中原 handler，而不是被 `:id` 吃掉。
2. **`GET /api/admin/servers/:id/node-metrics/interface-names`**
   - 返回 `{"interfaces": [{"name": "eth0", "default_v4": true, "default_v6": false}, ...]}`。先确认
     快照里有没有「默认接口」标记；没有就只返回名字，不要编。
   - 用新路径，**不改** `…/interfaces` 的语义（它的「interface 必填、未知名字 404」是防自由文本进查询的
     安全边界，注释在 `admin_node_metrics.go` 的 `Interfaces` 上方）。
   - 非 PSP 服务器照 `resolveAgent` 返回 409 `node_metrics_unsupported`。
3. **B1 修复**：`scope.deployment` 改取 `snapshot.Observation.Deployment`。先查 PSP 存储的 observation
   结构有没有保存 Deployment：
   - 有 → 一行修复 + 测试（断言 docker 节点 `deployment=docker`、`resource_scope=container` 不相等）。
   - 没有 → 这是 WP4 入库时漏的字段，需要加列并在 nodesync 接收处写入；按规格文档的规矩先在
     `psp-node-rootless-observability-progress.md` 记一笔。
4. **B4 修复**：把 resolution 传进 `h.metrics.Interfaces(...)`；如果 service 本就不支持 minute/hour 选择，
   则让 handler 回显**实际使用**的 resolution，而不是请求值。二选一，测试锁住。

**完成判据**：`go test ./internal/transport/http/... ./internal/app/...` 通过；`gofmt`、`go vet`、staticcheck 通过。

### WP-D2　图表组件重写 `NodeMetricsChart`

**文件**：`components/NodeMetricsChart.tsx`、新增 `components/NodeMetricsChart.test.tsx`、
`utils/datetime.ts`（新增轴格式化 helper）

**输入形态改为**：

```ts
interface Props {
  points: { at: string; coverage_seconds: number }[]   // 时间桶，ISO
  series: MetricsSeries[]                               // 与 points 等长
  resolution: 'minute' | 'hour'                         // 用来判定缺口
  timeZone: string                                      // 面板时区，'' = 浏览器
  height?: number
  ariaLabel: string
  group?: string                                        // echarts.connect 联动组名
}
```

**做法**：

1. **时间轴**：`xAxis: { type: 'time' }`，每个序列的数据是 `[Date.parse(at), value]`。
2. **缺口必须断线（规格 §12.3）**。改成时间轴后，**缺失的桶不会自动断线**，ECharts 会把相邻两点直接连起来，
   正好画出规格禁止的「穿过故障的斜线」。因此在组件内做缺口补空：相邻两点间隔 >
   `1.5 × 桶宽`（minute = 60s，hour = 3600s）时，在中间插入一个 `[t, null]`。`connectNulls: false` 保留。
   **这一条要有单测**：输入 `00:00, 00:01, 00:05` 三个点，断言输出里 `00:01` 和 `00:05` 之间有 null。
3. **轴标签**：`axisLabel.formatter` 用 `Intl.DateTimeFormat(locale, { timeZone, ... })`：
   - 范围 ≤ 24h：`HH:mm`
   - 范围 > 24h：`MM-DD HH:mm`（跨天首个刻度带日期）
   - helper 放 `utils/datetime.ts`，名如 `formatAxisTime(ms, tz, spanMs, locale)`，单测覆盖 UTC、
     `America/Vancouver`、`Asia/Shanghai` 三个时区和跨天。
   - `hideOverlap: true`。
4. **图例**：`legend: { top: 0, left: 'right', type: 'scroll' }`；`grid: { top: 32, bottom: 8, left: 8, right: 16, containLabel: true }`。
   只有一个序列时**不画图例**（标题已经说明是什么）。
5. **Y 轴单位**：`yAxis.axisLabel.formatter = v => formatValue(v, unit)`；percent 类 `min: 0`，
   `max: v => Math.max(100, v.max)`**不要**用（CPU 1% 的节点会被压成一条贴底的线）。改为 `max` 不设，
   但加 `yAxis.name` 显示单位也不必，formatter 已带单位。
6. **tooltip**：表头 = 面板时区完整时间；每行 `序列名: 值`；末行 `t('admin:nodeMetrics.coverage', { seconds })`
   （新 key）。值为 null 时显示 `—`。
7. **resize**：换成 `ResizeObserver` 观察容器；卸载时 `disconnect()`。jsdom 没有 ResizeObserver，
   组件里做存在性判断，测试里提供一个桩。
8. **联动**：传入 `group` 时 `chart.group = group` 并 `echarts.connect(group)`，同一 tab 内几张图十字准线同步。
9. **可访问性**：`aria-label={ariaLabel}`。
10. **暗色**：保持现在从 `theme.palette.md` 取色、`md` 进依赖数组的做法。

**测试**（先写）：组件内部的纯函数（缺口补空、轴格式化、option 构建）抽出来导出后单测；
不要在 jsdom 里真渲染 canvas。`option` 构建函数的测试断言：legend 在顶部、`xAxis.type === 'time'`、
yAxis 有 formatter、单序列无 legend。

### WP-D3　节点指标迁入查询缓存

**文件**：`query/keys.ts`、`query/policies.ts`、新增 `query/nodeMetrics.ts`、`api/nodeMetrics.ts`、`api/servers.ts`

按 `react-data-freshness-plan-review.md` 的规则（§6.1 `enabled` 含权限；§6.3 传 `signal`、不许
`catch(() => undefined)`、区分 `isPending`/`isFetching`、刷新失败保留旧数据；§7.1 图表 key 含 id、范围、时区）：

| key | 内容 | policy 建议 | note 要写的理由 |
|---|---|---|---|
| `serverKeys.detail(scope, id)` | `GET /servers/:id` | stale 30s，与 `serversList` 一致 | 与列表同源数据，同节奏 |
| `nodeMetricsKeys.current(scope, id)` | current | stale 30s，**不轮询** | 规格 §12.1 禁止自动秒级刷新；样本每个同步周期（默认 30s）才变 |
| `nodeMetricsKeys.history(scope, id, range, resolution)` | history | stale 60s | 后端本身 `max-age=15`；1h 以上范围分钟内变化可忽略 |
| `nodeMetricsKeys.interfaceNames(scope, id)` | 网卡列表 | stale 5min | 网卡基本不变 |
| `nodeMetricsKeys.interfaceHistory(scope, id, name, range)` | 单网卡 | stale 60s | 同 history |
| `nodeMetricsKeys.health(scope, id)` | node-health | stale 30s | 同 current |

- 范围参数里的 `from/to` **不进 key**（每次渲染都在变），key 用 `range` 名；`queryFn` 里按当前时间算 from/to。
- 「立即刷新」按钮：POST refresh 后，用 `refetchInterval` 在 **30 秒窗口内**每 2s 查 current，直到 `received_at`
  变化或超时，然后停。窗口状态放组件 state，页面卸载即停（修 D8）。
- 服务器更新/删除的 mutation 成功后 invalidate `serverKeys.detail`。
- 远程诊断（`nodeDiagnostics`）**不迁**：它是 task 状态机轮询，按 §10.2「install、upgrade、诊断保留自己的状态机」。

### WP-D4　详情页 `/admin/servers/:id`

**文件**：新增 `views/admin/ServerDetailView.tsx` 与 `views/admin/serverDetail/*.tsx`（每个 tab 一个文件）、
`router/index.tsx`、`router/viewModules.ts`、`locales/{zh-CN,en-US}/admin.json`

**路由**：`/admin` 子路由加 `{ path: 'servers/:id', element: <ServerDetailView /> }`，lazy；
`viewModules.ts` 加 loader。`AdminLayout.tsx:307` 的 `startsWith(item.to + '/')` 已能让侧栏高亮「服务器」，无需改。

**页头**（所有 tab 共用）：

```
← 服务器   Canada BC Danika Home - Telus   [在线] [健康]      [测试] [立即刷新] [⋮]
           psp · Agent v0.0.1-beta10 · Xray 26.x · 最后同步 2 分钟前 · 最后指标 18:04
```

- ⋮ 菜单复用列表的菜单项（编辑、升级、重装、轮换凭据、删除），抽成共享组件 `ServerActionsMenu`，
  列表和详情页共用，**不要复制**。
- 非 PSP 服务器：页头照常，正文只显示一张「此类型面板不提供主机指标」的说明卡 + 基本信息。

**范围选择器**：放在 tab 条右侧，**只在「性能」「网络」tab 显示**（修 D2）。

**概览 tab**（规格 §12.2.1）：

- 四张主卡：CPU、内存、数据目录、网络。网络卡同时显示 ↓入站 / ↑出站（修 D5），副标题「最近 60 秒平均」。
  每张卡带一条 24h 迷你趋势线（复用 history，`range=24h`，一个请求四张卡共用）。
- scope 说明：`resource_scope` 为 `mixed` 时显示 `nodeMetrics.scopeNote` 这条 Alert（key 已存在但全仓未被引用）。
- 部署方式 / 资源范围 / cgroup 版本 / 采集时间 放进一行次要信息，时间用 `formatDualTz`（修 D7）。
- 当前活动告警：沿用 findings → Alert。

**性能 tab**（规格 §12.2.2，限于 API 现有字段）：

| 图 | 序列 | 条件 |
|---|---|---|
| CPU | `system_cpu_percent` | 总是 |
|  | `cgroup_cpu_capacity_percent` | **仅** `resource_scope !== 'host'`（修 D1） |
|  | `cgroup_cpu_throttled_period_percent` | 仅 container/mixed，且序列里至少一个非 null |
| 内存 | `memory_used_percent`，图例标 scope（主机可见 / 容器上限） | 总是 |
| 数据目录 | `disk_available_bytes` | 总是 |
| Core 进程 | `core_cpu_percent`（左）；`core_rss_bytes` 另一张图 | 一张图只放一种单位（组件已有此约束） |

所有图同一个 `group`，十字准线联动。

**网络 tab**（规格 §12.2.3）：

- 顶部网卡选择器（`interface-names`），默认选第一个默认路由接口；只有一个网卡时不显示选择器。
- 图 1：入站/出站 bps（来自主 history 的 `rx_bps`/`tx_bps`；选了具体网卡时改用 interface history）。
- 图 2：链路利用率 %（`link_utilization_percent`，全 null 时整张图不画，写一行「该网卡未报告链路速率」）。
- 图 3：TCP 重传率 %。
- 底部保留 `bbrReadOnly` 只读说明。

**诊断 tab**（规格 §12.2.4 + §13）：

- 上半部：数据新鲜度 / 资源健康 两张卡，值**翻译**后显示（`nodeMetrics.freshness.*`，修 D6）；findings 列表。
- 下半部：把 `NodeDiagnosticsDialog` 的正文搬进来（分区勾选 → 采集 → 轮询结果 → 检查表 / 状态 / 事件）。
  抽成 `NodeDiagnosticsPanel` 组件，弹窗和 tab 都用它；等 WP-D6 删掉弹窗入口后弹窗文件一并删除。
- 仅 `useCan('config.write')` 时显示采集区（与现状一致）。
- 删除 `diagnosticsPhaseTwo` 这条 key（中英两个文件都删，否则 parity 测试会报）。
- `doctorHint` 保留。

### WP-D5　弹窗退场

- `NodeMetricsDialog.tsx` 删除；其两个测试文件里**仍然有效的断言**迁到新 tab 组件的测试里：
  - em dash 而非 `0.0%`（`NodeMetricsDialog.test.tsx`）
  - mixed scope chip
  - 切换范围时保留旧数据、显示进度条、不发重叠请求（`NodeMetricsDialog.pending.test.tsx`）
  - 诊断 tab 加载中显示 `common:status.loading`
- `formatBits/formatBytes/formatValue` 的测试保持不动（函数仍在 `NodeMetricsChart.tsx`）。

### WP-D6　列表行操作精简

**行内保留**：

1. **名称**变成链接 → `/admin/servers/:id`。
2. **健康点**（原指标按钮，保留 `nodeMetricsColor` 配色）→ 同样进详情页；tooltip 修成多行
   （`slotProps={{ tooltip: { sx: { whiteSpace: 'pre-line' } } }}`）。仅 PSP 行显示。
3. **测试**按钮。
4. **⋮ 更多**（`ServerActionsMenu`）：编辑、诊断（跳详情页诊断 tab）、安装/重装、Agent 升级、
   面板升级、选择内核、轮换凭据、——分隔线——、删除（红色，放最后）。

**测试影响（必须同步改，不能删断言）**：

| 测试 | 现在的做法 | 改成 |
|---|---|---|
| `ServersView.pending.test.tsx:30` | 行内 delete 按钮 `aria-busy` | 打开 ⋮ → 删除菜单项；删除进行中菜单项显示 busy 且禁用（保留「慢链路不能二次删除」这条语义，见 `ServersView.tsx:1573-1576` 注释） |
| `ServersView.installation.test.tsx:74,157,165` | `within(row).getByRole('button', { name: 'admin:servers.action.edit' })` | 打开 ⋮ → 编辑 |
| `ServersView.updates.test.tsx:53-68` | 断言 ⋮ 菜单项与图标 | 追加断言：编辑、诊断、删除存在且删除在最后 |
| `ServersView.installationCommand.test.tsx:39-42` | 「恰好一个 install_reinstall」 | 不变，确认仍成立 |

- `NodeMetricsDialog`/`NodeDiagnosticsDialog` 的 import 从 `ServersView.tsx` 删除 → 列表页不再加载 ECharts（§1.4）。
- 挪正 `:1580-1586` 那段错位注释。
- `src/test/adminSaveHarness.tsx` 的 `installReads` 对未知 GET 会抛错；列表**不许**因此新增逐行请求
  （N+1，规格明令禁止，后端 List 注释也写了）。

### WP-D7　i18n、验收

**新增/修改 key**（中英同时加，zh-TW 由生成脚本处理，不手改）：

- `admin:serverDetail.*`：页头、返回、tab 名、非 PSP 说明、网卡选择器、「该网卡未报告链路速率」
- `admin:nodeMetrics.coverage`：「覆盖 {{seconds}} 秒」/ `covers {{seconds}}s`
- `admin:nodeMetrics.coreCPU`、`coreRSS`（图标题）
- `admin:nodeMetrics.deploymentValue.{systemd,docker,manual,unknown}`（修 chip 显示原值）
- 删除：`admin:nodeMetrics.diagnosticsPhaseTwo`

**文案规则**沿用规格 §12.4：不写「实时」、不用 0% 表示未知、container scope 不叫「主机内存」。

**验收**：

1. `cd web-react && npm run test`、`npm run lint`（不新增 exhaustive-deps 警告）、`npm run build`（`tsc -b` 严格）。
2. `go test ./...`、`gofmt -l .` 为空、`go vet`、staticcheck v0.8.1。
3. **真浏览器**（本地面板 + 一个真实 PSP 节点，或 `psp-node` Lima VM）逐项核对：
   - [ ] C1：图例在右上，与 X 轴无重叠（1h、7d、90d 三个范围各截一张）
   - [ ] C2/D7：轴、tooltip、采集时间三处时区一致，且等于面板时区
   - [ ] C3：人为停掉节点 5 分钟，1h 图上出现断口而非斜线
   - [ ] D1：host 节点 CPU 图只有一条线；Docker 节点有 container capacity
   - [ ] D3：网络 tab 有入/出站历史图；多网卡节点能切换
   - [ ] D4：诊断 tab 能发起并看到一次远程诊断结果；列表上不再有独立诊断按钮
   - [ ] B1：Docker 节点 chip 显示「部署：docker / 资源范围：container」
   - [ ] 暗色模式、移动端 375px 宽（图表允许横向滚动，规格 §12.3）
   - [ ] 列表页 Network 面板里不再请求 `vendor-echarts` chunk
4. `npm run smoke:dist`。

---

## 5. 派工建议

| 批次 | WP | 人 | 可并行 |
|---|---|---|---|
| 1 | D1 | 后端 | 与 D2 并行 |
| 1 | D2 | 前端 A | 与 D1、D3 并行 |
| 1 | D3 | 前端 B | 依赖 D1 的接口形状（先按本文 DTO 写 mock） |
| 2 | D4 + D5 | 前端 A | 依赖 D1–D3 |
| 3 | D6 + D7 | 前端 B | 依赖 D4 |

一个 PR 一个批次；分支从 `origin/main` 切，命名 `kazuha/server-detail-*`。

---

## 6. 与冻结规格 §12 的差异及理由

| 规格 | 本计划 | 理由 |
|---|---|---|
| §12.2「性能」要求 iowait/steal/load/swap/FD | 本次只画 API 已有字段 | 这些字段当前的 history/current DTO 都没有，要先扩后端；拆出去避免本次改动跨三层 |
| §12.2「诊断」写「第二阶段加入远程诊断任务」 | 远程诊断直接并入诊断 tab | 第二阶段（WP10）已完成，弹窗是当时的临时入口 |
| §12.1 列表「健康入口」 | 健康点保留，但点击进入详情页而非弹窗 | 与 §12.2 详情页一致 |

实施完成后，由负责 D7 的人把这三条回写进规格 §12，并在 progress 文档记一笔。

## 7. 后续（不在本次范围）

- 扩 history/current DTO：load、swap、iowait/steal、conntrack、packets/errors/dropped、congestion control。
- scope 或 boot ID 变化处的断点标记（规格 §12.3），需要 history 点带 boot id。
- 其他图表（`TrafficChart`）也迁到 `ResizeObserver` + 时间轴格式化 helper。
