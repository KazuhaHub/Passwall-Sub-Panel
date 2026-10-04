# 服务器看板整改：任务规划书

- **状态**：规划已定稿（2026-09-28，第二版：按实现视角复核后重写），待派工。
  **2026-10-02 复核（只改了 WP-D4 相关的几处）**：对照 PSP `origin/main` `aa8a1b4b` 复核过。
  - WP-D4 尚未实现：没有 `ServerDetailView.tsx`，也没有 `views/admin/serverDetail/`。
  - `ServersView.tsx`、`NodeMetricsChart.tsx`、`NodeMetricsDialog.tsx`、`NodeDiagnosticsDialog.tsx` 与 `internal/service/nodemetrics` 自 `fb12a6c1` 以来都没有改动，WP-D1–D6 的行号仍然有效。
  - 改动的是 D-2、D-4 的出处，§4.1 的时间写法，以及 §4.3 末尾为 [node-audit-plan.md](node-audit-plan.md) §7 S15 留出的「访问控制」区块。
- **依据**：[psp-node-rootless-observability-plan.md](psp-node-rootless-observability-plan.md) §12（前端规格，已冻结）、
  [react-data-freshness-plan-review.md](react-data-freshness-plan-review.md) §6、§7.1、§10.2（查询缓存迁移规则）
- **触发**：生产截图（加拿大节点「主机指标」弹窗 → 性能 tab）里，图例压住 X 轴标签，X 轴是原始 UTC ISO 串，
  弹窗顶部「Collected」却是浏览器本地时间；host 节点画出一条无意义的 0.1%「Container capacity」平线；
  服务器列表每行 6–7 个操作控件
- **范围**：只动 PSP 仓库——后端 `internal/service/nodemetrics`、`internal/transport/http`（及其测试替身），
  前端 `web-react`。**不动 Passwall-Node，不改线上协议，不加表、不加迁移。**

> 冻结规格 §12.2 写的本来就是「服务器详情」四个 tab。现在的 `NodeMetricsDialog` 是过渡形态，本计划把它落成
> 规格原意的详情页。与 §12 不一致之处见 §7，按那份规格的规矩，完成后要回写规格。

**读本文的方式**：§1 是现状（每条都对照源码核实过，路径可直接跳转）；§3 是已拍板、不需要再讨论的设计决定；
§4 是工作包，按顺序做，每个包都写了「先写哪些测试」和「完成判据」；§5 是 PR 切分。
**每个工作包都先写失败的测试，再写实现。**

---

## 1. 现状与问题（已核实）

前端路径相对 `web-react/src`，后端路径相对仓库根。

### 1.1 后端数据面（决定了前端能画什么）

| # | 事实 | 位置 | 后果 |
|---|---|---|---|
| S1 | **主机网络速率只在小时汇总里有值**。`Derive()` 没有网络这一步，`RXBps/TXBps/LinkUtilizationPercent` 只在读小时汇总时被填上 | `internal/service/nodemetrics/derive.go:104-124`（无网络步骤）；`read.go:186-188`（仅小时行赋值）；`rollup.go:301-350`（`accumulateNetwork` 汇总默认接口） | `current` 与 1h/24h 历史里的 `rx_bps/tx_bps/link_utilization_percent` **恒为 null**。生产上概览「带宽监控」卡片因此一直是 `—` |
| S2 | 「minute」分辨率**不是整分钟桶**，是按 `ReceivedAt` 存的原始样本。面板要求每 60s 上报（`DefaultNodeHostReportInterval`），且两行之间至少隔 60s（`NodeMetricRawWriteInterval`），59.9s 到的那次会被跳过 → 实际间隔约 60s 或约 120s | `internal/service/nodemetrics/ingest.go:26-36`、`isHistoryDue` `:228-243` | 断线阈值不能按 60s 算 |
| S3 | `resolution=auto` 在跨度 ≤ `MaxMinuteRange`（2500 分钟 ≈ 41h40m）且在原始保留期（7 天）内时解析为 `minute`，否则 `hour`；响应里的 `resolution` 是**解析后**的值 | `internal/service/nodemetrics/read.go:30,38`；`admin_node_metrics.go:172-211` | 1h、24h → minute；7d/30d/90d → hour |
| S4 | 缺口的表现：缺失的样本/小时**不出现**在序列里（小时行 `SampleCount==0` 被跳过，`read.go:163`）；算不出的速率（如重启后）是**存在的点、值为 null**；网卡序列**直接省略**缺口点（`read.go:259-263`） | 同左 | 图表组件必须自己插断点 |
| S5 | 网卡序列 `Interfaces()` **没有分辨率参数**，永远返回原始样本；没有网卡的小时表；原始只保留 7 天；没有点数上限 | `admin_node_metrics.go:31,303`；`read.go:233` | 网卡图只能做 1h/24h；handler 现在却把 `hour` 原样回显 |
| S6 | 「Deployment」chip 显示的其实是 resource scope：`deployment` 和 `resource_scope` **都**取自 `snapshot.Observation.ResourceScope` | `admin_node_metrics.go:120-121` | Docker 节点显示「部署：container」 |
| S7 | Deployment **已经存了**：最新快照行的 `SnapshotJSON` 里有 `scope.deployment`（`internal/adapters/sqlstore/node_host_schema.go:38`），原始样本有 `deployment` 列（`:78`，`domain.NodeHostMetricSample.Deployment` `internal/domain/nodehost.go:65`）。`domain.NodeHostObservation`（`nodehost.go:25-49`）**没有**这个字段 | 同左 | 修 S6 不需要加列 |
| S8 | 网卡默认标记也已经存了：每行 `IsDefaultIPv4/IsDefaultIPv6`（`node_host_schema.go:191-192`），快照里有 `network.default_ipv4_interface / default_ipv6_interface`；但 `InterfaceNames()` 只返回去重排序后的名字，覆盖 7 天，可能含已消失的 veth | `internal/ports/repos.go:733`；`read.go:234` | 新接口要合并这两处 |
| S9 | 没有单台服务器的读取接口：只有 `GET /servers`、`PUT/DELETE /servers/:id` | `internal/transport/http/router.go:712-718` | 详情页直达链接无从取数 |
| S10 | `Update` 的响应走 `toServerDTO`（`admin_servers.go` 约 `:764`），**没有** `applyNodeMetrics` → 编辑保存后列表行的健康点变灰 | `admin_servers.go:633-776` | 顺手修 |
| S11 | 小时粒度下 `disk_available_bytes` 是小时**最小值**、`core_rss_bytes` 是小时**峰值**，其余是平均值；`memory_scope` 在小时行里是 null | `read.go:176-198` | 规格 §12.3 要求 max/min 不能画成未标注的线 |

### 1.2 图表组件 `components/NodeMetricsChart.tsx`

| # | 问题 | 位置 |
|---|---|---|
| C1 | 图例压住 X 轴标签：`legend` 没给位置，ECharts 6.1.0 默认 `bottom: 15`（`node_modules/echarts/lib/component/legend/LegendModel.js:242-250`），落在 `grid.bottom: 40` 里。对照 `TrafficChart.tsx:58-62` 显式写了 `legend.top: 4` | `:126` |
| C2 | X 轴是分类轴，数据是原始 ISO UTC 串 | `:127-132` |
| C3 | 分类轴等距 → 缺桶两侧的点被画成相邻 | 同上 |
| C4 | Y 轴无单位：单位只用在 tooltip | `:133-137` |
| C5 | tooltip 硬编码英文 `` `coverage: ${coverage[index]}s` ``，表头是原始串 | `:112-124` |
| C6 | 只监听 `window` resize（全仓没有 `ResizeObserver`） | `:83-93` |
| C7 | `<div role="img">` 无可访问名称 | `:155` |

### 1.3 弹窗 `views/admin/NodeMetricsDialog.tsx`、`NodeDiagnosticsDialog.tsx`

| # | 问题 | 位置 |
|---|---|---|
| D1 | host scope 也画「Container capacity」 | `NodeMetricsDialog.tsx:303` |
| D2 | 范围 chip 在所有 tab 显示，只有性能 tab 用 | `:139-157` |
| D3 | 网络 tab 没有历史图；`getNodeInterfaceHistory`（`api/nodeMetrics.ts:133`）全仓零调用 | `:331-347` |
| D4 | 诊断 tab 写「远程诊断将在第二阶段提供」，而远程诊断早有独立弹窗 | `:391`（`diagnosticsPhaseTwo`） |
| D5 | 概览「带宽监控」只用 `tx_bps`（且因 S1 恒为 `—`） | `:251` |
| D6 | 诊断 tab 显示原始值 `healthy` | `:376-377` |
| D7 | 时间用 `toLocaleString()`（浏览器时区） | `NodeMetricsDialog.tsx:209`、`NodeDiagnosticsDialog.tsx:213` |
| D8 | 「立即刷新」的 30 秒轮询没有 abort，关窗后继续跑 | `:83-101` |
| D9 | 弹窗无 `key`，切服务器带着上一台的 tab/range | `ServersView.tsx:1974` |
| D10 | `NodeDiagnosticsDialog` 从不渲染 `result.host` | `NodeDiagnosticsDialog.tsx` 全文 |

### 1.4 列表 `views/admin/ServersView.tsx`

- 行操作（`:1561-1623`）：测试、编辑、删除、指标、诊断、⋮，删除夹在中间；`:1580-1586` 的「kebab」注释错位在指标按钮上方。
- 指标 tooltip 用 `'\n'` 拼多行（`:2653-2665`），Tooltip 没有 `whiteSpace: 'pre-line'`，实际显示成一行。
- 两个弹窗静态 import（`:3-4`）→ 打开列表就拉 `vendor-echarts` chunk（`vite.config.ts:47-53` 把 ECharts 单独分块）。
- 所有菜单动作的实现（`openEdit :702`、`openCoreDialog :550`、`rotateCredential :647`、`runUpgradePanel :397`、
  `confirmDelete :793`、编辑表单约 `:1800-1972`、以及 `ReinstallBackendDialog` / `NativeAgentUpgradeDialog` /
  `NodeMigrationPreviewDialog` / `NativeInstallationDialog`）都在这个 2665 行的组件里。
- 探测：`probeServer :330-380`，列表每次挂载按 `pageIdsKey` 对整页探测一次（`:309-312`）。

### 1.5 类型与 i18n

- `api/nodeMetrics.ts:75-80` 的 `NodeMetricsSeriesPoint` **缺** `core_cpu_percent`，而后端会发（`admin_node_metrics.go:254,269`）。
- `nodeMetrics.scopeNote` 这条 key 存在但全仓未引用；`nodeMetrics.coreRSS` 已存在（概览卡在用）。
- `nodeMetrics.bbrReadOnly` 的文案承诺「显示系统默认与可用算法」，但 API 并没有这些字段。

---

## 2. 目标与非目标

**目标**

1. 原生节点有一个真正的详情页 `/admin/servers/:id`，四个 tab，对齐冻结规格 §12.2。
2. 所有时间按**面板时区**显示（轴、tooltip、采集时间、诊断事件）。
3. 图表不再重叠、有单位、缺数据处断线、tooltip 可翻译、小时 max/min 有标注。
4. 网络速率在 1h/24h 与 `current` 里有真实数据（修 S1）。
5. 列表行内只剩三个控件。
6. 节点指标读取迁入查询缓存。

**非目标（见 §8「后续」）**

- API 里还没有的规格字段：iowait/steal、load、swap、packets/errors/dropped、TCP established、conntrack、
  congestion control 列表、Agent/Core FD、sync RTT、collector duration。
- scope/boot ID 变化处的断点标记（需要 history 点带 boot id）。
- 网卡的小时汇总（没有表；7 天以上只提供默认接口合计）。
- 把所有服务器动作的对话框从 `ServersView` 抽成可复用宿主（§3 用 URL 转交代替，见决定 D-6）。
- 不改 Passwall-Node、不做 3X-UI/S-UI 的主机指标、不做秒级自动刷新（规格 §12.1 禁止）。

---

## 3. 已定的设计决定（不需要再讨论）

| # | 决定 | 内容 | 理由 |
|---|---|---|---|
| D-1 | 详情页而非弹窗 | 路由 `/admin/servers/:id`，React.lazy | 规格 §12.2 原意；四 tab + 多张图放在弹窗里必须滚动，且不能分享链接 |
| D-2 | 状态进 URL | `?tab=overview\|performance\|network\|diagnostics&range=1h\|24h\|7d\|30d\|90d&iface=<name>`；打开详情 push，切 tab/范围/网卡 replace；非法值回退到 `overview`/`1h`，不改写 URL | 沿用风控中心的写法：切 tab 用 replace（`views/admin/risk/RiskCenterView.tsx:57-64`），打开详情用 push（`views/admin/risk/drawerParam.ts`；node-audit-plan §7.2 的 UI-0 会把它移到 `src/hooks/useDrawerParam.ts`，届时从新位置引用） |
| D-3 | 所有服务器类型都能进详情页 | 3X-UI/S-UI 只显示基本信息 + 「此类型面板不提供主机指标」 | 名称链接对每一行一致 |
| D-4 | 权限 | 整页 admin-only | 已在 `ADMIN_ONLY_ROUTES`（`router/home.ts:25-30`，#262/#267 之后还含 `/admin/risk`、`/admin/diagnostics`；`home.test.ts` 仍断言 `/admin/servers/1`）；后端所有相关接口都在 `adminGroup` |
| D-5 | 远程诊断并入诊断 tab | 删掉独立入口与弹窗 | D4 |
| D-6 | 详情页的服务器动作 | 删除与诊断在详情页本地执行；其余动作（编辑、重装、升级、选核心、轮换凭据）**跳回列表并自动打开对应对话框**：`/admin/servers?open=<action>&server=<id>` | 这些动作的对话框和状态全在 `ServersView` 里；把它们抽成可复用宿主是一个比本计划其余部分加起来还大的重构，列为后续 |
| D-7 | 图表时间轴 | `xAxis.type: 'time'`，数据点为 `[epochMs, value, coverageSeconds]` | 解决 C2、C3；coverage 随点走，不再靠 `dataIndex` 回查 |
| D-8 | 时区 | 面板时区（`useSiteStore(s => s.timezone)`，空则浏览器时区）；轴标签用它格式化，图下方放 `components/TzHint.tsx` 说明 | ECharts 没有时区选项（只有 `useUTC`），刻度按浏览器本地整点对齐，只能「按面板时区改写标签」 |
| D-9 | 查询 key 偏离 review §7.1 | 时区不进 key（接口返回 UTC 点，时区只影响渲染）；`from/to` 不进 key（窗口相对 now，由 range 名唯一确定，staleTime 内复用旧窗口可接受） | 显式记录偏离 |
| D-10 | 网络速率 | 后端补上分钟级与 `current` 的主机网络速率（WP-D1 第 5 步），**不**用网卡序列拼 | 概览卡与 1h/24h 图都要它；rollup 已有同一算法，抽出来共用即可 |
| D-11 | 行内控件 | 名称（链接）、健康点（链接，仅 PSP）、测试、⋮ | 见 WP-D6a |

---

## 4. 工作包

**顺序**：D1（后端）→ D2（图表组件）∥ D3（查询层）→ D6a（抽取共用件）→ D4 + D5 + D6b（详情页上线、弹窗退场）→ D7（收尾）。

### WP-D1　后端：单台读取、网卡清单、网络速率、三个 bug

**文件**：`internal/transport/http/handler/admin_servers.go`、`admin_node_metrics.go`、`router.go`；
`internal/service/nodemetrics/{read.go,derive.go,rollup.go}`；`ports` 中 `NodeMetricsService` 相关接口；
测试替身 `admin_node_metrics_test.go` 里的 `nodeMetricsServiceStub`（接口加方法后不改就编译不过）。

1. **`GET /api/admin/servers/:id`**（`adminGroup`，放在 `router.go` 的 `PUT /servers/:id` 旁边）
   - 抽出 `func (h *AdminServersHandler) decorate(ctx context.Context, panels []*domain.Panel) ([]serverDTO, error)`：
     包含 `refreshLatestSUI`（S-UI 行）、`agents.ListByPanelIDs`、`nodeMetrics.LatestBatchByPanelIDs`、`compatPolicy`、
     `toServerDTOWithAgent`、`applyNodeMetrics`。**`List`、新的 `Get`、`Update` 的响应都走它**（顺带修 S10）。
   - id 非法 → 400 `{"error":"Invalid id"}`；不存在 → 与 PUT/DELETE 一致用 `mapServerError(c, err)`（`admin_servers.go:1962`）。
   - 响应体即单个 `serverDTO`（与列表项同形）。
2. **`GET /api/admin/servers/:id/node-metrics/interface-names`**（注册在 `router.go` 的 `if d.NodeMetrics != nil` 块内，
   handler 放 `AdminNodeMetricsHandler`）
   - service 新增 `InterfaceCatalog(ctx, agentID) ([]InterfaceName, error)`：
     名字取 `repo.InterfaceNames(agentID)`（保证每个名字都能被 `…/interfaces` 查到）；
     `default_v4 / default_v6 / present` 取自最新快照 `SnapshotJSON.network`（`default_ipv4_interface`、
     `default_ipv6_interface`、`interfaces[].name`）。无快照时三者全 false。
   - 响应：`{"interfaces":[{"name":"eth0","default_v4":true,"default_v6":false,"present":true}]}`，
     排序 default_v4 > default_v6 > present > name。
   - 非 PSP → 409 `node_metrics_unsupported`（复用 `resolveAgent`）；无 agent 行 → 404。`Cache-Control: private, max-age=60`。
   - 用新路径，**不改** `…/interfaces` 的语义（「interface 必填、未知名字 404」是防自由文本进查询的边界）。
3. **S6 修复**：新增 `deploymentOf(snapshot)`：先解 `snapshot.Observation.SnapshotJSON` 的 `scope.deployment`，
   失败回退 `snapshot.Sample.Deployment`（`Sample` 可能为 nil，`read.go:89-93`），再回退 `"unknown"`。
   `Current` 的 `scope.deployment` 改用它。**不加列、不加迁移**（S7）。
4. **S5 修复**：`Interfaces` handler：`resolution=hour` → 422 `node_metric_resolution_unavailable`；
   `auto` 仅在 `parseWindow` 判定可用分钟级时接受，否则 422；响应 `resolution` 恒为 `"minute"`；
   点数 > `MaxHistoryPoints` → 422（与 `History` 一致）。
5. **S1 修复：分钟级与 current 的主机网络速率**
   - 把 `rollup.go:301-350` `accumulateNetwork` 里「默认接口 rx/tx 求和 + 利用率取峰值」抽成纯函数
     `hostNetworkRates(prevRows, curRows []domain.NodeInterfaceMetricSample, prevAt, curAt time.Time) (rx, tx, util *float64)`，
     rollup 与读路径共用，**不复制第二份算法**。
   - 分钟历史：按 `InterfaceNames` 逐个 `InterfaceRange`（最多 `MaxNetworkInterfaces` 次），按样本分组后对相邻样本调用。
   - `Current`：对 `Sample` 与前一样本的网卡行调用。
   - 相邻样本不可比（重启、计数器回绕）时返回 nil，不返回 0。

**先写的测试**（Go，表驱动）：

| 测试 | 断言 |
|---|---|
| `TestServerGetReturnsDecoratedDTO` | 带 `node_resource_health` 等字段；与 List 中同一项相等 |
| `TestServerGetUnknownIs404` / `TestServerGetInvalidIDIs400` | 状态码与错误体 |
| `TestServerUpdateResponseKeepsNodeMetrics` | S10 |
| `TestServerStaticPathsAreNotShadowedByID` | 扩展 `internal/transport/http/node_release_catalog_test.go` 中已带管理员 JWT 的路由测试：`/servers/node-releases`、`/servers/sui-release`、`/servers/compat-status` 命中原 handler（按响应形状判断），`/servers/1` 命中 Get。**不要**用 `internal/app/node_metrics_routes_test.go` 的「不是 404」判断，那里没 token，全是 401，分不出命中谁 |
| `TestNodeMetricsInterfaceNamesOrdersDefaultsFirst` | 排序与 present 标记；无快照时全 false |
| `TestNodeMetricsCurrentReportsDeploymentSeparatelyFromScope` | SnapshotJSON 含 deployment=docker、scope=container → 两个字段分别正确 |
| `TestNodeMetricsInterfacesRefusesHourResolution` / `…ReportsMinuteResolution` | S5 |
| `TestMinuteHistoryCarriesDefaultInterfaceThroughput` / `TestCurrentSummaryCarriesRxTx` | S1 |
| `TestRollupAndMinuteAgreeOnHostThroughput` | 同一组样本：小时平均 == 分钟点按 coverage 加权平均 |

并在 `internal/app/node_metrics_routes_test.go` 的路径清单里加上 `…/interface-names` 与 `/api/admin/servers/1`。

**完成判据**：`go test ./internal/transport/http/... ./internal/service/nodemetrics/... ./internal/app/...` 通过；
`test -z "$(gofmt -l .)"`、`go vet ./...`、`go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 -tags node_reinstall_acceptance ./...` 通过。

### WP-D2　图表组件重写 `components/NodeMetricsChart.tsx`

**新增文件**：`components/NodeMetricsChart.test.ts`（纯函数测试，不渲染 canvas）；`utils/datetime.ts` 增加
`formatAxisTime`，测试写进现有或新建的 `utils/datetime.test.ts`。

**Props**：

```ts
export interface ChartPoint { at: string; coverage_seconds: number }
interface Props {
  points: ChartPoint[]                 // 与每个 series.values 等长
  series: MetricsSeries[]              // 一张图只放一种 unit（组件已有此约束）
  resolution: 'minute' | 'hour'        // 取响应体的 history.resolution，不是请求值
  from: string; to: string             // 取响应体的 history.from / to
  timeZone: string                     // '' = 浏览器时区
  ariaLabel: string
  group?: string                       // echarts.connect 组名
  height?: number                      // 默认 240
}
```

**导出的纯函数**（全部要有单测）：`insertGaps`、`buildChartOption`、以及 `datetime.ts` 的 `formatAxisTime`。

**做法**：

1. **时间轴**：`xAxis: { type: 'time', min: Date.parse(from), max: Date.parse(to), axisLabel: { hideOverlap: true, formatter } }`。
   固定 min/max 是为了让「节点 20 分钟前停止上报」表现为右侧空白，而不是图表在最后一个点结束、看起来很新鲜。
2. **缺口断线**（规格 §12.3；S2、S4）：`insertGaps` 在相邻两点间隔超过阈值时，在两点正中插入 `[t, null, 0]`。
   阈值：`minute` = **180s**（连续丢两次上报才算断；正常间隔本就有约 120s 的情况），`hour` = **5400s**。首尾不补。
   单测：`00:00, 00:01, 00:05`（minute）→ `00:01` 与 `00:05` 之间有 null；`00:00, 00:02` → 无 null；hour 同理。
3. **孤立点可见**：`showSymbol: true`，`symbolSize` 回调对「前后都是 null 或缺口」的点返回 4，其余返回 0
   （一条线至少两个点，否则孤立样本不可见）。
4. **轴标签** `formatAxisTime(ms, tz, spanMs, locale)`：
   - 跨度 ≤ 24h：`HH:mm`；24h–7d：`MM-DD HH:mm`；≥ 7d：`MM-DD`。
   - 用 `Intl.DateTimeFormat(locale, { timeZone: tz || undefined, hourCycle: 'h23', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).formatToParts` 拼装（Intl 没有 `MM-DD` 模式；en-US 默认 12 小时制）。
   - 按 `(locale, tz, pattern)` 缓存 formatter；`tz` 非法时 catch 后回退浏览器时区（参照 `panelDayStr` 的 try/catch；
     **不要**照抄 `formatDualTz`，它对非法时区会抛 RangeError，且用的是浏览器 locale 而不是 `i18n.language`）。
   - 不做「跨天首个刻度带日期」：ECharts 刻度按浏览器本地时间对齐，无法可靠判断面板时区的跨天；完整日期由 tooltip 给出。
   - 单测覆盖 `UTC`、`America/Vancouver`、`Asia/Shanghai`、`Asia/Kolkata`（+05:30）、非法时区回退、zh-CN 与 en-US。
5. **图例与布局**：
   `legend: { top: 0, right: 0, type: 'scroll' }`；**只有一个序列时不画图例**；
   `grid: { top: 32, bottom: 8, left: 8, right: 16, outerBoundsMode: 'same', outerBoundsContain: 'axisLabel' }`
   （ECharts 6 里 `containLabel` 是旧写法，会在开发模式打警告 `Grid.js:175`；这两个字段是 v6 的等价写法）。
6. **Y 轴**：`axisLabel.formatter = v => formatValue(v, unit)`；percent/bps/bytes 设 `min: 0`；**不设 max**
   （强制 100 会让 1% 的节点贴底）；不设 `yAxis.name`。所有序列全为 null 时组件不渲染图表，由调用方显示空状态。
7. **tooltip**：`trigger: 'axis'`。表头 = `params[0].axisValue` 按面板时区格式化的完整日期时间；
   每行 `序列名: formatValue(item.value[1])`，null 显示 `—`；末行 `t('admin:nodeMetrics.coverage', { seconds: item.value[2] })`。
   **不要**再用 `dataIndex` 回查数组（插入断点后下标会错位）。
8. **resize**：`ResizeObserver` 观察容器，卸载时 `disconnect()`；jsdom 没有它，组件内做存在性判断。
9. **联动**：传入 `group` 时 init 后设 `chart.group = group` 再 `echarts.connect(group)`；组名由调用方给
   `` `server-${id}-${tab}` ``（`connect` 是全局的，组名必须每页唯一）。
10. **移动端**（规格 §12.3 允许横向滚动）：外层 `Box sx={{ overflowX: 'auto' }}`，内层 `minWidth: 560`，ResizeObserver 观察内层。
11. **模块注册不变**：`echarts.use([LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])`
    已覆盖时间轴、scroll 图例、axisPointer 与 `connect`，无需 DataZoom/Timeline。
12. **暗色**：保持从 `theme.palette.md` 取色、`md` 在依赖数组里的做法。

**迁移测试**：`NodeMetricsDialog.test.tsx:49-63` 的 `node metric units` 两个用例**原样移入** `NodeMetricsChart.test.ts`
（D5 会删掉原文件，这两个用例不能随之消失）。

**`buildChartOption` 的单测断言**：legend 在顶部；单序列无 legend；`xAxis.type === 'time'` 且 min/max 等于 from/to；
yAxis 有 formatter；数据点为三元组；插入断点后 null 位置正确。

**完成判据**：`NodeMetricsChart.test.ts`、`datetime.test.ts` 全绿；`npm run build`（`tsc -b`）通过；
旧 `NodeMetricsDialog` 在同一 PR 里改为按新 props 调用，仍能编译运行。

### WP-D3　节点指标迁入查询缓存

**文件**：`query/keys.ts`、`query/policies.ts`、`query/servers.ts`（加 `serverDetailQuery`）、新建 `query/nodeMetrics.ts`、
`api/nodeMetrics.ts`、`api/servers.ts`（加 `getServer(id)`）；测试 `query/nodeMetrics.test.ts`。

规则出处：review §6.1（`enabled` 含权限）、§6.3（传 `signal`；不许 `catch(() => undefined)`；错误走 `_skipErrorToast`；
区分 `isPending` 与 `isFetching`；刷新失败保留旧数据）。

1. **类型**：`NodeMetricsSeriesPoint` 加 `core_cpu_percent?: number | null`；所有 `get*` 函数加
   `opts?: { signal?: AbortSignal }` 并统一传 `_skipErrorToast: true`。
2. **key**（形状固定，避免前缀失效时误伤）：

   ```ts
   serverKeys.details = s => [...serverKeys.all(s), 'detail']
   serverKeys.detail  = (s, id) => [...serverKeys.details(s), id]

   nodeMetricsKeys.all              = s => [...privateRoot(s), 'node-metrics']
   nodeMetricsKeys.server           = (s, id) => [...nodeMetricsKeys.all(s), id]
   nodeMetricsKeys.current          = (s, id) => [...nodeMetricsKeys.server(s, id), 'current']
   nodeMetricsKeys.history          = (s, id, range) => [...nodeMetricsKeys.server(s, id), 'history', range]
   nodeMetricsKeys.health           = (s, id) => [...nodeMetricsKeys.server(s, id), 'health']
   nodeMetricsKeys.interfaceNames   = (s, id) => [...nodeMetricsKeys.server(s, id), 'interfaces']
   nodeMetricsKeys.interfaceHistory = (s, id, name, range) => [...nodeMetricsKeys.server(s, id), 'interface', name, range]
   ```

   分辨率由 range 唯一决定（1h→`minute`，24h→`auto`，7d/30d/90d→`hour`，沿用 `NodeMetricsDialog.tsx:27-33`），不进 key。
   `queryFn` 里按当前时间算 `from/to`。偏离 review §7.1 的理由见决定 D-9。
3. **policy**（`as const satisfies Record<string, ResourcePolicy>`，每条都要写 `note`）：

   | policy | staleTime | gcTime | refetchInterval | note 要点 |
   |---|---|---|---|---|
   | `serverDetail` | 30s | 5min | false | 与 `serversList` 同源同节奏 |
   | `nodeMetricsCurrent` | 30s | 5min | false（刷新窗口例外，见第 5 条） | 样本按 `DefaultNodeHostReportInterval`=60s 更新，原始写入下限 60s；规格 §12.1 禁止秒级自动刷新 |
   | `nodeMetricsHistory` | 60s | 5min | false | 后端 `max-age=15`；1h 以上范围分钟内变化可忽略 |
   | `nodeMetricsInterfaces` | 5min | 10min | false | 网卡清单几乎不变 |
   | `nodeMetricsHealth` | 30s | 5min | false | 同 current |

4. **`enabled`**：所有节点指标查询 `enabled = useCan('config.write') && isValidId && server?.panel_type === 'psp'`
   （依赖 `serverDetail` 的结果，避免对非 PSP 发请求吃 409）；history/interface 另需所在 tab 可见；
   `interfaceHistory` 只在 range ∈ {1h, 24h} 时 enabled（S5）。
5. **「立即刷新」窗口**（修 D8；模式参照已测试的 `query/syncStatus.ts` 中 `watchIntervalMs` / `useObservationWindow`）：
   - 纯函数 `refreshPollMs({ open, elapsedMs, baseline, receivedAt }): 2000 | false`：`open && elapsedMs < 30000 && receivedAt === baseline` 时 2000。
   - `useNodeMetricsRefresh(scope, id)`：`useMutation`（POST refresh，`retry: false`）；成功后记录 `{ windowStart, baseline: current.received_at }`；
     current 查询 `refetchInterval: q => refreshPollMs(...)`；`received_at` 变化或超时即关窗，并 invalidate health 与当前 range 的 history。
   - 按钮 `AsyncButton pending = mutation.isPending || windowOpen`。窗口状态放组件 state，卸载即停。
   - 标签页隐藏时轮询暂停（`refetchIntervalInBackground: false`），窗口按墙钟时间照常结束，可接受。
6. **缓存与写操作的衔接**（`ServersView` 没有 mutation，直接调 `updateServer`/`deleteServer` 再 `mutateItems` 改列表缓存，`:765`、`:793-804`）：
   - `updateServer` 成功后 `setQueryData(serverKeys.detail(scope, id), saved)`；
   - `deleteServer` 成功后 `removeQueries({ queryKey: serverKeys.detail(scope, id) })` 与 `removeQueries({ queryKey: nodeMetricsKeys.server(scope, id) })`。
7. **不迁移的**：远程诊断（任务状态机，review §10.2 明确保留）；「测试」探测（review §10.2：探测不得放进会聚焦重取的 queryFn）。

**先写的测试**（`query/nodeMetrics.test.ts`）：key 含 id 与 range；`refreshPollMs` 的四种停止条件
（未开窗、超时、`received_at` 已变、正常轮询）；policy 取值等于 `policies.*`。

**完成判据**：测试全绿；`tsc -b`；所有 queryFn 传 `signal`，没有吞错误的 catch。

### WP-D6a　抽出列表与详情页共用的部件（行为不变）

在详情页之前做，因为 D4 要用它们。**本包不改变列表的可见行为**（行内控件仍是现状），只做抽取，
这样本包的回归风险只在「抽取是否等价」，现有测试不用改。

| 新文件 | 内容 | 来源 |
|---|---|---|
| `views/admin/servers/ServerActionsMenu.tsx` | ⋮ 按钮 + 菜单，见下 | `ServersView.tsx:1615-1622`、`:1638-1676` |
| `hooks/useServerProbe.ts` | `probe(server, { notify }) → Promise<ProbeState>`，参数 `onPatch(patch)` 由调用方合并进列表缓存或详情缓存 | `ServersView.tsx:330-380` |
| `views/admin/servers/nodeHealthColor.ts` | `nodeMetricsColor`、`nodeMetricsTooltip` | `ServersView.tsx:2632-2665` |
| `views/admin/servers/ServerVersionCell.tsx` | 版本单元格 | `versionCell :1052` |

**`ServerActionsMenu` 接口**：

```ts
type ServerAction =
  | 'edit' | 'diagnostics' | 'install' | 'agentUpgrade'
  | 'panelUpgrade' | 'selectCore' | 'rotateCredential' | 'delete'

interface Props {
  server: Server
  canConfigure: boolean
  busy: boolean                  // 升级中或删除中：⋮ 显示进度并禁用
  hide?: ServerAction[]          // 本包里列表传 ['edit','diagnostics','delete']，行为不变
  onAction: (action: ServerAction, server: Server) => void
}
```

- 组件自带 ⋮ 按钮（`AsyncIconButton`，`pending={busy}`，顺带给现在的 upgrading 状态补上 `aria-busy`）与 `Menu`（每个实例自有 anchor）。
- 可见性规则照抄 `ServersView.tsx:1645-1675`，并补：`edit`、`delete`、`install`、`rotateCredential` 需 `canConfigure`；
  `diagnostics` 需 `panel_type === 'psp' && canConfigure`；`agentUpgrade` 需 psp；`panelUpgrade`/`selectCore` 按 capability；
  ⋮ 按钮始终渲染。`delete` 放最后、前面一条 `Divider`、`color: error`。
- 列表的 `onAction` 分派到现有 handler（`openEdit`、`setReinstallTarget`、`setNativeUpgradeTarget`、`runUpgradePanel`、`openCoreDialog`、`rotateCredential`、`confirmDelete`），不复制实现。

**完成判据**：现有所有 `ServersView.*.test.tsx`、`NodeMigrationPreviewDialog.test.tsx` 不改一行全绿；
新增 `ServerActionsMenu.test.tsx` 覆盖可见性矩阵（admin/operator × psp/3xui/sui × capability）；
`useServerProbe` 有单测。

### WP-D4　详情页 `/admin/servers/:id`

**新文件**：`views/admin/ServerDetailView.tsx`；`views/admin/serverDetail/{OverviewTab,PerformanceTab,NetworkTab,DiagnosticsTab,ServerHeader,Sparkline}.tsx`；
`views/admin/servers/NodeDiagnosticsPanel.tsx`（由 `NodeDiagnosticsDialog` 正文改造）。
**改**：`router/index.tsx`（`/admin` 子路由加 `{ path: 'servers/:id', element: <ServerDetailView /> }`）、
`router/viewModules.ts`（loader 键 `'/admin/servers/:id'`）。`AdminLayout.tsx:307` 的 `startsWith(item.to + '/')` 已能让侧栏高亮「服务器」，不用改。

**懒加载**：`PerformanceTab`、`NetworkTab` 用 `React.lazy`（只有它们 import `NodeMetricsChart`），概览/诊断 tab 不拉 `vendor-echarts`。
列表行**不做**悬停预取（否则列表页会拉 ECharts）。

#### 4.1 页头 `ServerHeader`（所有 tab 共用）

```
← 返回   Canada BC Danika Home - Telus                         [测试] [立即刷新] [⋮]
         PSP 原生节点 · [在线] · [健康]
         Agent v4.0.1.x · Xray 26.9.9 · 最后同步 2026-09-28 18:04 · 最后指标 2026-09-28 18:04
```

| 字段 | 来源 |
|---|---|
| 名称、类型、备注 | `serverDetail` |
| 在线 | 进入页面时**一次性**探测：非 PSP 用 `useServerProbe`；PSP 用 `GET …/node-agent-status` 的 `state`。按 id 触发一次（`useEffect`），不做成 query |
| 健康 | `server.node_resource_health`，配色用 `nodeHealthColor` |
| 版本 | `ServerVersionCell` |
| 最后同步 / 最后指标 | `node-agent-status.last_seen` / `current.received_at` |

- 页头的时间一律 `formatDualTz(value, panelTz)`，不做「N 分钟前」。原因是页头要给出确切时刻，并不是缺 helper：#262 之后仓库已有 `utils/relativeTime.ts:14` 的 `formatRelativeTimeShort`（风控中心的短格式）。页内列表若要写相对时间，就用它，并在悬停时显示 `formatMsDualTz`。
- 「立即刷新」只对 PSP 显示（WP-D3 第 5 条）。
- ⋮ 用 `ServerActionsMenu`，`onAction`：
  - `delete`：本地执行——确认框 → `deleteServer` → WP-D3 第 6 条的 `removeQueries` → `navigate('/admin/servers')` → toast；
  - `diagnostics`：切到诊断 tab；
  - 其余：`navigate(\`/admin/servers?open=${action}&server=${id}\`)`（决定 D-6）。
- **列表侧配套**：`ServersView` 挂载时读 `open` 与 `server` 两个参数，用 `getServer(id)` 取到 `Server` 后调用与 ⋮ 相同的 handler，
  然后 `replace` 删除**这两个**参数（不动 `usePageState` 管理的 page/keyword）。未知 action 或取不到服务器 → 忽略并删参数。
- **返回**：`location.key !== 'default'` 时 `navigate(-1)`（列表的分页、搜索都在 URL 里），否则 `Link` 到 `/admin/servers`。

#### 4.2 加载、错误与空状态

| 情况 | 显示 |
|---|---|
| `:id` 不是正整数 | 直接显示 NotFound 卡，不发请求 |
| `serverDetail` pending | 页头骨架 + `CircularProgress` |
| 404 | 「服务器不存在或已删除」+ 返回列表按钮 |
| 其他错误 | Alert + 重试（`refetch`） |
| 已有数据、刷新失败 | 保留数据 + 顶部 warning「刷新失败，数据可能已过期」（review §6.3） |
| 非 PSP | 只有页头 + 「基本信息」卡（类型、URL、备注、版本、兼容状态），不显示 tab |
| PSP 且 `current.available=false`，freshness=`unsupported` | `nodeMetrics.unsupported` |
| 同上，freshness=`missing` | 「等待节点首次上报」 |
| 图表 | 按规格 §12.3 区分 unsupported / unavailable / stale（保留旧数据 + 「数据陈旧」chip）/ noHistory；空时不渲染空图 |

#### 4.3 概览 tab（规格 §12.2.1）

- 四张主卡：CPU、内存、数据目录、网络（↓入站 / ↑出站，副标题「最近 60 秒平均」）。数据来自 `current.summary`。
- 每张卡一条 24h 迷你趋势线：`Sparkline` 用**纯 SVG polyline**（不引入 ECharts），数据来自一次 `history(range=24h)`，
  四张卡共用；断线规则与 WP-D2 相同（复用 `insertGaps`）。
- `resource_scope === 'mixed'` 时显示 `nodeMetrics.scopeNote` 这条 Alert（key 已存在）。
- 次要信息一行：部署方式（`deploymentValue.*` 翻译）/ 资源范围 / cgroup 版本 / 采集时间（`formatDualTz`）。
- 当前活动告警：findings → Alert（沿用）。
- **最后一个区块是「访问控制」**，规格见 [node-audit-plan.md](node-audit-plan.md) §7 S15：
  - 只对 `kind = psp` 显示，渲染共享组件 `<NodePolicyStatusRow variant="block">`（`components/NodePolicyStatusRow.tsx`）。这个组件在 node-audit 阶段 1c 先随「节点覆盖」抽屉上线，本包**只挂载、不另写**。
  - `GET /api/admin/dest/status` 读取失败或返回 503（未接线）时，整个区块不渲染，不影响其余区块。**不要写「404 = 访问控制尚未上线」这一支**：SPA 与 API 由同一个二进制发布，区块由后合并的一方挂上（见下），挂上时接口一定已经存在；而未注册的 `/api/...` 路径会落到 `NoRoute` → SPA，返回 200 加 index.html，根本不是 404（node-audit-plan F55）。
  - 本包与 node-audit 没有先后依赖，由**后合并的那一方**负责把区块挂上：
    - WP-D4 先合：本包不挂这个区块，由 node-audit 阶段 1c（或之后最先改到 `OverviewTab.tsx` 的那个阶段）的 PR 挂上；
    - 1c 先合：组件已经存在，本包的 PR5 直接挂上。
  - 测试 `serverDetail/OverviewTab.test.tsx` 加两例：`/dest/status` 读取失败时不出现「访问控制」区块，其余区块照常；有数据时区块出现。

#### 4.4 性能 tab（规格 §12.2.2，限 API 现有字段）

范围选择器放 tab 条右侧，**只在性能与网络 tab 显示**（修 D2）。每张图外包 `ChartBlock`（标题 + 可选副标题）。

| 图 | 序列 | 条件 |
|---|---|---|
| CPU | `system_cpu_percent` | 总是 |
|  | `cgroup_cpu_capacity_percent` | 仅 `resource_scope !== 'host'`（修 D1） |
|  | `cgroup_cpu_throttled_period_percent` | 仅 container/mixed，且序列里至少一个非 null |
| 内存 | `memory_used_percent`；scope 写在**副标题**（小时行的 `memory_scope` 是 null，取 `current.summary.memory_scope`） | 总是 |
| 数据目录 | `disk_available_bytes`；`resolution==='hour'` 时序列名加「（小时最小值）」 | 总是 |
| Core CPU | `core_cpu_percent` | 总是 |
| Core 内存 | `core_rss_bytes`；小时粒度加「（小时峰值）」 | 总是 |

所有图 `group = \`server-${id}-performance\``。

#### 4.5 网络 tab（规格 §12.2.3）

| range | 图 1 入站/出站 bps | 图 2 链路利用率 % | 图 3 TCP 重传率 % |
|---|---|---|---|
| 1h、24h | 选中网卡的 `interfaceHistory` | 同左 | 主 history（主机级，不随网卡变） |
| 7d、30d、90d | 主 history（小时汇总 = 默认接口合计）；网卡选择器禁用，提示「7 天以上只提供默认接口合计」 | 主 history | 主 history |

- 网卡选择器（`interface-names`）：默认 default_v4 → default_v6 → 第一个 present → 第一个名字；只有一个网卡时不显示；选择写进 `?iface=`。
- 「该网卡未报告链路速率」只在数据源为 `interfaceHistory` 且 `link_utilization_percent` 全 null 时显示。
- 底部保留只读说明，文案改为「TCP 拥塞控制（只读）：PSP 不会修改内核网络设置。」（原文承诺了 API 没有的数据，见 §1.5）。

#### 4.6 诊断 tab（规格 §12.2.4 + §13）

- 上半：数据新鲜度 / 资源健康两张卡，值用 `nodeMetrics.freshness.*` **翻译**（修 D6；该组 key 已覆盖两者全部取值）；findings 列表；`doctorHint`。
- 下半：`NodeDiagnosticsPanel({ serverId, diagnostic, onDiagnostic })`——
  - `diagnostic` 状态**提升到 `ServerDetailView`**，切 tab 不丢正在进行的任务；
  - 原弹窗的「open」语义改为「组件挂载」；Collect 按钮在面板底部；新增「重新采集」按钮（原来靠关弹窗重置）；
  - 事件时间改用 `formatDualTz(panelTz)`（修 D7）；
  - 轮询仍用 3s `setInterval`（review §10.2 保留任务状态机）；
  - 仅 `useCan('config.write')` 时显示采集区（与现状一致）；
  - `result.host` 仍不渲染（D10 记为后续，不在本次）。
- 删除 `nodeMetrics.diagnosticsPhaseTwo`（中英两个文件都删，否则 parity 测试失败）。

#### 4.7 移动端

`Tabs variant="scrollable" allowScrollButtonsMobile`；页头操作区 `flexWrap` 换到标题下方；范围 chip 换行；网卡 `Select` 在 xs 下 `fullWidth`。

#### 4.8 测试

视图测试里一律 mock 图表，不渲染 canvas：

```ts
vi.mock('@/components/NodeMetricsChart', async importOriginal => ({
  ...(await importOriginal()),
  default: (p: { ariaLabel: string }) => <div data-testid="chart" aria-label={p.ariaLabel} />,
}))
```

`adminSaveHarness` 的 `installReads` 会忽略查询参数，不同 range 返回同一 body，所以用**调用次数与 URL** 断言。

| 测试文件 | 覆盖 |
|---|---|
| `ServerDetailView.test.tsx` | 非法 id / 404 / 非 PSP / unsupported / missing 五种状态；tab 与 range 读写 URL；host 节点 CPU 图只有一个序列 |
| `ServerDetailView.pending.test.tsx` | 从 `NodeMetricsDialog.pending.test.tsx` 迁来的三例（见 WP-D5）；刷新窗口开关 |
| `serverDetail/OverviewTab.test.tsx` | em dash 而非 `0.0%`；mixed scope Alert；deployment 与 resource_scope 两个值不同 |
| `serverDetail/NetworkTab.test.tsx` | 1h 用 interface history、7d 用主 history 并禁用选择器；链路速率提示条件 |
| `servers/NodeDiagnosticsPanel.test.tsx`、`.pending.test.tsx` | 从 `NodeDiagnosticsDialog*.test.tsx` 迁来的七例；切 tab 后任务仍在 |
| `ServersView.openParam.test.tsx` | `?open=edit&server=3` 打开编辑对话框并清掉参数；未知 action 被忽略 |
| 路由测试 | `/admin/servers/7` 渲染详情页 |

**完成判据**：上表全绿；`npm run build`；手动打开一个真实 PSP 节点的四个 tab 无控制台报错。

### WP-D5　弹窗退场（与 D4 同一个 PR）

删除 `views/admin/NodeMetricsDialog.tsx`、`NodeDiagnosticsDialog.tsx` 及其四个测试文件。**删除前把全部有效断言迁走**：

| 原测试 | 迁往 |
|---|---|
| `NodeMetricsDialog.test.tsx:49-63` units 两例 | `components/NodeMetricsChart.test.ts`（WP-D2 已迁） |
| `NodeMetricsDialog.test.tsx:66` unsupported | `ServerDetailView.test.tsx` |
| `:75` scope chip | `OverviewTab.test.tsx`，改为断言 deployment 与 resource_scope 两个 chip 值不同 |
| `:94` em dash | `OverviewTab.test.tsx` |
| `:105` 无样本范围 | `ServerDetailView.test.tsx` |
| `NodeMetricsDialog.pending.test.tsx:76` 切范围保留旧数据、进度条、无重叠请求 | `ServerDetailView.pending.test.tsx` |
| `:114` 诊断 tab loading | 同上 |
| `:139` 手动刷新 spinner | 同上 |
| `NodeDiagnosticsDialog.test.tsx` 六例、`.pending.test.tsx` 一例 | `NodeDiagnosticsPanel.test.tsx` / `.pending.test.tsx`（mount 改为面板） |

原 pending 测试刻意不用 StrictMode 以便数请求次数（`NodeMetricsDialog.pending.test.tsx:15-30`）；迁到 TanStack Query 后
StrictMode 下请求会被去重，**改用 `adminSaveHarness` 的 `mount`（带 StrictMode）**，次数断言仍成立。

### WP-D6b　列表行操作精简（与 D4、D5 同一个 PR）

**行内**：名称（`MUI Link component={RouterLink}` → 详情页）、健康点（仅 PSP，`IconButton component={RouterLink}`，
aria-label 用新 key `servers.action.open_detail`「查看 {{name}} 详情」；tooltip 加
`slotProps={{ tooltip: { sx: { whiteSpace: 'pre-line' } } }}` 修多行）、测试按钮、⋮（`ServerActionsMenu`，不再 hide 任何项）。

**删除**：行内编辑、删除、指标、诊断按钮；两个弹窗的 import；`:1580-1586` 的错位注释。

**删除中的忙碌状态**：新增 `deletingId` state，⋮ 的 `busy = upgrading === s.id || deletingId === s.id`。
（菜单在确认框打开前就已关闭，所以忙碌状态放在 ⋮ 按钮上，不放在菜单项上。）

**测试改动（只改定位方式，不删断言）**：

| 测试 | 现在 | 改成 |
|---|---|---|
| `ServersView.pending.test.tsx:30` | 行内删除按钮 `aria-busy` | 打开 ⋮ → 点删除 → 确认；断言该行 ⋮（name `admin:servers.action.more`）`aria-busy="true"` 且禁用、出现 progressbar；再点 ⋮ 不打开菜单，`api.delete` 只调用一次；resolve 后 `aria-busy` 移除 |
| `ServersView.installation.test.tsx:74,157,165` | `within(row).getByRole('button', { name: 'admin:servers.action.edit' })` | 打开 ⋮ → 点「编辑」菜单项 |
| `ServersView.updates.test.tsx:53-68` | 断言 ⋮ 菜单项与图标 | 追加：编辑、诊断、删除存在，删除在最后 |
| `ServersView.installationCommand.test.tsx:39-42` | 恰好一个 install_reinstall | 不变 |
| `ServersView.installationCommand.test.tsx:74-82`（operator） | install_reinstall 不存在 | 追加：operator 下编辑、删除、诊断也不存在 |

**注意**：从详情页返回会重新挂载 `ServersView`，`pageIdsKey` 那段 effect（`:309-312`）会对整页重新探测、对上游发真实请求。
本次在 `useServerProbe` 里加一个按 id 的 30 秒去重（30 秒内探测过的行不重复探测），并在测试里断言返回列表不会重复探测。

`src/test/adminSaveHarness.tsx` 的 `installReads` 对未知 GET 会抛错；列表**不许**因此新增逐行请求（N+1）。

**完成判据**：上表所有测试全绿；`ServersView.tsx` 不再 import `NodeMetricsDialog`/`NodeDiagnosticsDialog`/任何 echarts 模块。

### WP-D7　i18n、规格回写与验收

**i18n**（zh-CN 与 en-US 同时加，parity 测试 `src/i18n/localeParity.test.ts`；zh-TW 由生成脚本处理，不手改）：

```
新增 admin:serverDetail.{back, notFound, loadFailed, refreshFailed, noMetricsForType, basicInfo,
       lastSync, lastMetric, agentVersion, coreVersion, range, interface, interfaceDefaultV4,
       interfaceDefaultV6, interfaceAggregateOnly, noLinkSpeed, waitingFirstReport, staleData,
       chartNetwork, chartLinkUtilization, chartRetrans, chartCoreCPU, chartCoreRSS, chartAria,
       remoteDiagnostics, recollect}
新增 admin:servers.action.{diagnostics, open_detail}
新增 admin:nodeMetrics.{coverage, coreCPU, hourlyMin, hourlyMax,
       deploymentValue.{systemd, docker, manual, unknown}}
修改 admin:nodeMetrics.bbrReadOnly（见 4.5）
删除 admin:nodeMetrics.diagnosticsPhaseTwo
复用 nodeMetrics.tab*、rxBps/txBps、coreRSS、freshness.*、scopeNote
```

`common` 命名空间没有「返回」，所以用 `serverDetail.back`。文案规则沿用规格 §12.4：不写「实时」，不用 0% 表示未知，
container scope 不叫「主机内存」。

**规格回写**：把 §7 的差异逐条写进 `psp-node-rootless-observability-plan.md` §12，并在
`psp-node-rootless-observability-progress.md` 记一笔。

**验收**：

1. 开工前记 lint 基线：`cd web-react && npx oxlint src 2>&1 | grep -c exhaustive-deps`；验收时数量不增加。
2. `npm run test`、`npm run lint`、`npm run build`、`npm run smoke:dist`。
3. `go test ./...`、`gofmt`、`go vet`、staticcheck v0.8.1（版本见 `.github/workflows/test.yml`）。
4. **真浏览器**（本地面板 + 真实 PSP 节点，或 `psp-node` Lima VM），逐项截图贴到 PR：
   - [ ] 图例在右上，与 X 轴无重叠（1h、7d、90d 各一张）
   - [ ] 轴、tooltip、采集时间、诊断事件时间四处时区一致，等于面板时区
   - [ ] 停掉节点 5 分钟后，1h 图出现断口而非斜线；节点仍停着时，图右侧留白
   - [ ] host 节点 CPU 图只有一条线；Docker 节点有 container capacity 与 throttled
   - [ ] 概览网络卡与 1h 网络图有数值（S1 已修）
   - [ ] 网络 tab 1h 能切网卡；7d 选择器禁用并有提示
   - [ ] Docker 节点显示「部署：docker / 资源范围：container」
   - [ ] 诊断 tab 能发起一次远程诊断；切到别的 tab 再切回，任务仍在
   - [ ] 详情页 ⋮ → 编辑：跳回列表并打开该服务器的编辑框
   - [ ] 暗色模式；375px 宽（图表可横向滚动）
   - [ ] **硬刷新**直接打开 `/admin/servers`（新标签页），Network 面板里没有 `vendor-echarts`（先访问过仪表盘的话会有缓存，所以要新标签页）

---

## 5. PR 切分与派工

| PR | 内容 | 依赖 | 建议分工 |
|---|---|---|---|
| PR1 | WP-D1 | 无 | 后端 |
| PR2 | WP-D2（旧弹窗同 PR 适配新 props） | 无 | 前端 A |
| PR3 | WP-D3 | PR1 合入（接口形状） | 前端 B |
| PR4 | WP-D6a | 无（与 PR2、PR3 并行） | 前端 B |
| PR5 | WP-D4 + WP-D5 + WP-D6b | PR1–PR4 | 前端 A |
| PR6 | WP-D7 | PR5 | 前端 B |

分支从 `origin/main` 切，命名 `kazuha/server-detail-<pr编号或主题>`。每个 PR 的描述里贴对应 WP 的完成判据勾选情况。

---

## 6. 风险

- **PR5 较大**：它同时上线详情页、删除弹窗、改列表行。拆开会出现「弹窗删了但入口还在」或「入口改了但详情页没有」的中间态，
  所以刻意合在一起；审阅时按 D4 / D5 / D6b 三段看。
- **`?open=` 转交**：如果以后给 `ServersView` 加新的菜单动作，必须同时加进 `ServerActionsMenu` 的 `ServerAction` 和 `open` 参数分派，
  否则详情页的菜单项会跳回列表却什么都不打开。`ServersView.openParam.test.tsx` 应遍历 `ServerAction` 的每个值来锁住这一点。

---

## 7. 与冻结规格 §12 的差异及理由（WP-D7 回写）

| 规格 | 本计划 | 理由 |
|---|---|---|
| §12.2.2 性能：iowait/steal/load/swap/FD | 只画 API 已有字段 | 这些字段不在 history/current DTO 里，要先扩后端 |
| §12.2.3 网络：packets/errors/dropped、established、conntrack、congestion control 列表、qdisc | 同上 | 同上 |
| §12.2.4 诊断：unavailable section、sync RTT、连续失败、请求响应大小、collector duration | 同上 | 同上 |
| §12.2.4「第二阶段加入远程诊断任务」 | 远程诊断直接并入诊断 tab | 第二阶段（WP10）已完成 |
| §12.3 scope/boot ID 变化处画断点标记 | 未做 | history 点不带 boot id |
| §12.1 列表颜色「红色：critical 或 offline」 | `nodeMetricsColor` 仍不看 offline | 在线状态来自一次性探测，不在列表数据里；后续由 agent 状态补 |
| §12.2.1 connectivity | 页头「在线」来自一次性探测与 agent-status | 没有持续的连接状态字段 |
| §12.1 健康入口 | 健康点点击进入详情页 | 与 §12.2 一致 |

---

## 8. 后续（不在本次范围）

- 扩 history/current DTO，补上 §7 表中缺的字段。
- history 点带 boot id，画 scope/boot 断点标记。
- 把所有服务器动作的对话框从 `ServersView` 抽成可复用宿主，取消 `?open=` 转交。
- 诊断结果渲染 `result.host`（D10）。
- 其他图表（`TrafficChart`）也迁到 `ResizeObserver` + `formatAxisTime`。
