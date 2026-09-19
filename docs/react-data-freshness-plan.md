# 前端数据新鲜度计划（修订稿）

- **状态**：**提案（v2，已按评审修订），尚未决策、尚未实施**
- **日期**：2026-09-18（v1 同日，v2 为修订稿）
- **v1**：初稿已按评审作实质性修订。**实施细则见 [`react-data-freshness-plan-review.md`](react-data-freshness-plan-review.md)**（16 节，含文件级改造清单、失效矩阵、R1–R9 复现矩阵、PR 顺序与验收清单）。本文只保留方向、边界与验收目标，不重复细则。
- **相关代码**：`web-react/src/hooks/usePaged.ts`、`web-react/src/views/admin/UsersView.tsx`、`web-react/src/components/NotificationBell.tsx`、`web-react/src/utils/userAccess.ts`、`web-react/src/api/client.ts`、`internal/transport/http/handler/admin_user.go`

> **v2 修订要点**：v1 把四个不同问题合成了一个承诺，并据此高估了方案能力。本稿按评审结论拆分，并删除三处不成立的论断（`X-Sync-Pending` 可直接接 operation 轮询、失效可跨标签页广播、聚焦重取可解决持续停留页面不更新）。

## 0. 一句话

把前端取数从「每个视图各写一份 `useEffect`」收敛为**按资源键管理的共享查询与定向失效**（建议 TanStack Query），分三件事交付——**不要把三者混为一谈**：

1. **本页缓存一致性**：本次操作完成后，本标签页内的列表、抽屉、详情保持一致。
2. **外部变化发现**：其他标签页、其他管理员或后台任务改变数据后，当前页面能发现变化。
3. **同步状态可见**：PSP 数据库已保存、上游代理面板尚未收敛时，能如实表达「待同步」，而不是宣称已同步。

一个 `QueryClientProvider` 只能包办第 1 项。第 2 项需要额外的有界轮询或推送；第 3 项是**后端契约工作**（见 §6），不属于本次前端选型。

## 1. 已确认的事实

### 1.1 症状

- 停留在某页面时，内容不会自动更新。
- **最影响日常的一条**：给用户解封后，当前界面持续显示「仍被封禁」，必须手动刷新。
- 其他多处状态同样不自动更新。

> ⚠️ **尚未在现场复现。** 上述为使用者报告的现象，**不预先认定缓存的缺失就是其根因**——1.1 第二、三条也可能来自旧对象副本、未完成的请求、失败响应或字段读取错误。定性方法见 §2.1。

### 1.2 基线统计（评审基线 `72e777c`）

| 指标 | 数值 |
|---|---|
| `views/admin/` 非测试 `.tsx` | 30（含 Dialog / Drawer / Tab / 辅助组件，**不是 30 个页面**） |
| 其中以 `View.tsx` 结尾 | 17（含 Placeholder） |
| 含字面量 `useEffect(() =>` 的文件 | 26（**文本统计，不等于 26 个取数点，更不是 26 次替换**） |
| API 非测试 `.ts` | 31（含类型/客户端模块） |
| `src` 下测试文件 | 54 |
| 已安装的数据获取库 | 0 |
| 后端 SSE / WebSocket 端点 | 0 |

**v2 更正**：v1 把「26 个含 effect 的文件」当作改造单位，这是错的。**改造单位是资源与读者，不是文件。** 实施时应重新统计，且不把这些数字写成长期约束。

### 1.3 已有的轮询点（v1 漏报）

v1 称 `NotificationBell` 是「全仓唯一的轮询」，**错误**。至少还有三处，且各自拥有**状态机与停止条件**，不能统一套用用户列表的 pending 逻辑：

| 位置 | 行为 | 停止条件 |
|---|---|---|
| `NotificationBell.tsx:85` | 60 秒轮询通知 | 组件卸载 |
| `NativeAgentUpgradeDialog.tsx:34` | 3 秒轮询升级任务 | 终态 `verified` / `failed` / `manual_attention` / `dispatch_closed` |
| `ServersView.tsx:2062` | 4 秒轮询安装状态 | 安装结束 |
| `NodeMetricsDialog.tsx:90` | 延时 2 秒等待采样 | 取样返回 |

**注意**：`NativeAgentUpgradeDialog` 是**真正拥有 task ID 与终态**的内部对照——它正是 §6 所设想契约的既有范例，但与旧的用户 sync task 队列不是同一套东西。

### 1.4 为什么值得引入数据层（限于 PSP 的实际理由）

v1 大段引用 React 官方文档作为背书，**属于过度推断**。官方文档支持「考虑使用客户端缓存」，但没有断言所有 Effect 取数都是错误，也没有把候选库排成名次；PSP 是客户端 SPA，官方列的 SSR 缺点也不是本次选型的核心收益。

本计划的理由应限定为 PSP 自身的事实：

- **同一个用户同时出现在多个读模型里**：用户列表、安全抽屉、通行密钥对话框、流量选择器、权限提示、自助门户。目前各持一份 `useState` 副本，互不知情。
- **需要共享的取消与失效**：A 处保存后，B 处持有的副本无任何机制被通知；快速切换筛选时旧响应可能覆盖新查询。
- **已有按任务 ID 的轮询与终态语义**需要统一调度，而不是继续新增定时器。

### 1.5 服务端状态是独立的一类

[TanStack Query 概览](https://tanstack.com/query/latest/docs/framework/react/overview) 把服务端状态的特征列为**异步、共享、可能过期、需要去重**，其中「shared ownership and can be changed by other people without your knowledge」正是本计划第 2 项的来源。

### 1.6 默认值与其边界

以下数值来自 [Important Defaults](https://tanstack.com/query/latest/docs/framework/react/guides/important-defaults)、[Window Focus Refetching](https://tanstack.com/query/latest/docs/framework/react/guides/window-focus-refetching)、[SWR Revalidation](https://swr.vercel.app/docs/revalidation)：

| 行为 | TanStack Query | SWR |
|---|---|---|
| 窗口重新聚焦重取 | `true` | `true` |
| 网络重连重取 | `true` | `true` |
| 挂载重取 | `true` | 视 stale |
| 定时轮询 | **关闭**，需 `refetchInterval` | **关闭**，需 `refreshInterval` |
| 隐藏时轮询 | **自动暂停** | 自动暂停 |
| 缓存回收 | 5 分钟 | — |
| 失败重试 | 3 次指数退避 | — |

**v2 必须写明的三条边界**（v1 含糊带过，是它高估方案能力的地方）：

1. **`staleTime` 到期只是「数据变陈旧」，不是一个到期就发请求的计时器。** 没有挂载、可见性恢复、重连、手动失效或轮询事件，就**不会**发现外部变化。因此它**解决不了「始终停留页面不更新」**。
2. **聚焦重取有条件**：查询已启用、处于可用观察状态、数据 stale。设了非零 `staleTime` 后，短时间切回可能不发请求。v5 监听 `visibilitychange`，**不能承诺每次 OS 窗口焦点变化都等同于该事件**。
3. **轮询默认在标签页隐藏时暂停**——这条默认值本身是好的，但「隐藏不轮询」也意味着**页面持续不可见期间不会发现外部变化**，这是设计取舍不是缺陷。

### 1.7 失效是**同 QueryClient 内**的定向失效（v1 严重高估）

v1 称 `invalidateQueries` 是「跨视图失效广播」，并把它当作治「A 处改 B 处不变」的通用解药。**范围被放大了。** 它作用于当前 `QueryClient`，即**同一标签页、同一 Provider 下**。

它**不会**默认通知：另一个标签页、另一个浏览器、另一个管理员、或仍用 `useState` 保存副本的未迁移组件。且**不同 query key 的同一用户不会自动归一化合并**，必须显式失效对应列表与详情。

修订后的边界：

| 场景 | 机制 |
|---|---|
| 同一 QueryClient 内的活动列表/详情 | mutation 后定向失效并重取 |
| 当前不活动的已迁移页面 | 标记失效，下次挂载时校验 |
| 切走再回来、数据已 stale | 可见性恢复时重取 |
| 其他管理员操作，当前页面持续可见 | 该资源的定时只读轮询，或未来服务端推送 |
| 同浏览器的其他标签页 | 第一版依赖切回校验/资源轮询；即时跨页通知另列可选增强 |
| 尚未迁移的旧组件 | 保留它自己的刷新入口，或随资源一起迁移 |

TanStack 的跨标签页插件是**独立的实验性能力**，不是默认语义，第一阶段不引入。

### 1.8 `X-Sync-Pending` 不足以观察收敛（v1 结论被推翻）

v1 称「协议两端都已在位，只差接通」，并计划「对该资源轮询直至 pending 清除」。**该契约不存在。** 源码证据：

- 该 header 共 **7 处**（v1 说 6 处，漏了 `handler/user_me.go:362`），**全部位于写操作响应**；`List`、`Get` 不返回用户同步状态。
- `service/user/user.go:570` 的 `HasPendingSync` 仅检查三类活跃 sync task；可能命中**前一次操作**的任务，且**查询出错返回 `false`**（`user.go:579`，失败被伪装成「无待同步」）。
- `handler/admin_sync_tasks.go` 的列表只消费分页、`status`、`type`，**没有 `target_id`**，不能给现有请求凭空加参数。
- `router.go` 没有用户写操作对应的 `GetOperation` 路由。
- `api/users.ts` 的 `setEnabled` / `setServiceStatus` **直接丢弃 Axios 响应**（`users.ts:70-76`），连 header 都到不了调用者。

因此：**GET 不返回该 header，不等于 pending 已清除**；轮询 `service_status` 只能看到 PSP 的有效服务状态，不能证明上游已应用；扫描 sync task 第一页没找到任务也不代表不存在。**不能通过重复 POST 探测状态**（那会再次执行写操作）。

**修订**：从前端第一阶段**删除 operation 轮询**。保留并结构化 pending 提示，操作后立即刷新 PSP 数据；后端扩展见 §6。AIP-151 可作未来协议设计参考，**不能用它论证 PSP 已实现该协议**。

## 2. 现在还不知道的

### 2.1 ★ 解封缺陷的定性（v1 判据不成立）

v1 把它二分为「入口遗漏 vs 双轴语义」，并称「已核查路径本身会刷新」。**该推论有误**：

- `UsersView.tsx:974` 的 `actionResumeService` 确实调用 `await load()`，但 `load()` → `refresh()` **返回 `void`**（`usePaged.ts:141` 只是递增 React state）。因此 **`await load()` 没有等待 GET 完成**——「操作已刷新完」的时序判断失真。
- `UsersView.tsx:225` 的恢复入口主要对 `blocked_client`、`manual_suspended` 等开放。**v1 的复现步骤（构造一个配额超限用户去点解封）不成立**——该用户未必能点到按钮。
- `utils/userAccess.ts` 优先读 `user.access`。**单改 `enabled` 或 `service_disabled_reason` 不能保证渲染值改变。**
- `domain/access.go` 在清除人工暂停后会**继续**计算账号禁用、过期、配额。不需要等下一次 traffic poll。
- `UsersView` 同时保存列表、编辑对象、菜单对象、安全抽屉对象等副本；抽屉只在用户仍在当前页时跟随列表，**离开当前页后保留旧快照**。

「reason 清空、UI 仍封禁」**不能直接推出产品语义问题**，也可能是旧对象、未完成请求、失败响应或字段读取错误。必须比较**同一用户、同一次新 GET 的 `access.service_state` 与最终 UI**。

**修订**：凡属已可源码核实的事，不应作为「留给所有者回答的问题」。按评审 R1–R9 复现矩阵执行；**任何库选型结论都不能代替缺陷复现**。详见评审 §3.1、§3.2。

#### 2.1.1 已定位并修复（2026-09-18）

按上述方法复现后，**报告症状的主因并非缓存层缺失，而是「快照对象未随列表刷新」**——这正是评审 §2.4 警告「不要预先指定缓存为根因」的价值所在。

- **缺陷**：编辑对话框的服务状态字段读 `serviceStatusText(editing)`（`UsersView.tsx:1843`），解封按钮传的也是 `editing`（`:1849`）。而 `editing` 是 `openEdit` 时写入的快照（`:618`），**没有同步 effect**。`actionResumeService` 会 `load()` 刷新列表，但不会更新 `editing`——对话框因此持续显示「服务暂停」，直到手动刷新页面。
- **旁证**：同文件的 `securityUser`（`:371-373`）**有**专用同步 effect，注释明写理由「its delegated actions refresh the table, and the drawer must reflect the fresh state, not the snapshot it opened with」。**同一处理漏给了 `editing`。** 全仓「从列表同步快照」的模式仅 3 处（`UsersView` 两处 + `ServersView:1859`），说明这是遗漏而非普遍模式。
- **修复**：补一条与 `securityUser` 同形的 effect，仅重新指向快照，**不触碰 `editForm` 草稿**——与评审 §9.5「dirty 表单不被重置」一致。
- **复现记录**：`web-react/src/views/admin/UsersView.test.tsx` 新增用例（先红后绿）：打开编辑框 → 恢复服务 → 断言对话框状态转为 active。
- **复核**：全量前端测试 541 通过 / 1 跳过；`tsc -b` 通过。

**这不能替代 R1–R9。** 其余条目（尤其 R3 的人工暂停叠加配额、R5/R6 的上游不可达与入队失败、R8 的跨标签页）仍未复现。

### 2.2 负载预算（v1 漏算重查询与上游探测）

v1 只算列表 QPS。实际还有：

- `UsersView` 每次 `items` 变化都调 `topTraffic`；而 `Top` 接口（`admin_traffic.go`）**遍历全体用户**后按 `limit` 截断——**调小 `limit` 不消除全体扫描**。
- `ServersView` 会按页面 ID 集合**自动探测上游**。列表刷新不应变成每轮全页探测。
- 多个面板、抽屉与保活 Tab 可能同时挂载；**document 可见不等于每个业务区域都可见**。

预算必须包含**请求触发链**，而非只数页面。

### 2.3 产品延迟容忍度

单管理员也有后台变更（traffic poll、reconcile、证书轮转），所以**「是否只有一名管理员」不决定要不要新鲜度**，只影响轮询频率预算。真正需要所有者回答的是：**外部变化在多长时间内被当前页面发现是可接受的。**

### 2.4 是否需要 operation 级证据

若产品只要求「这个用户现在有没有同步任务」，轻量资源状态接口足够；若要求「这次操作什么时候完成」，则需完整 operation 契约。**这是产品判断，决定 §6 走哪一档。**

## 3. 方案选项（修正版）

v1 的比较表有实质性错误，逐条修正：

| 方案 | 修正后的评价 |
|---|---|
| **TanStack Query** | 适合按资源、抽屉、任务查询渐进迁移。理由是本项目同一用户横跨多个读模型，且已有任务级轮询需要统一调度。代价是新增依赖与缓存生命周期管理 |
| **SWR** | **同样可用。** v1 称其「无 predicate 式失效」是**错的**——官方 `mutate` 接受 filter 函数（`mutate(key => ..., undefined, { revalidate: true })`，清空全部为 `mutate(() => true, undefined, { revalidate: false })`）。不因错误的能力比较而排除；最终偏好 TanStack 是工程取舍 |
| **React Router loader / action / fetcher** | **零新增库**，具备路由数据重校验、请求取消与竞态管理。v1 概括为「没有任何请求协调」是错的。它缺少的是 TanStack 风格的**通用资源缓存体系**；且需重构现有取数与 mutation 入口 |
| **小型自建 hook** | 可修复具体的「切回刷新」问题。若继续加共享缓存、取消、重试、会话边界与广播，**维护成本需重新估算** |
| **SSE / WebSocket** | **是变化通知渠道，不是缓存层的替代项**，可与任一数据层组合，不纳入当前选型投票。v1 称扇出降到 O(1) 是**错误的**——广播给 N 个客户端仍是 O(N) 发送，事件触发查询还会产生 N 次重取 |

**其他更正**：

- 节点数据面推/拉测量（[`data-plane-plan.md`](data-plane-plan.md)）**不能**直接作为管理员浏览器刷新负载的证据——资源数量、消费者、触发频率、认证与查询成本都不同。可作为方法论参考，不作论据。
- **「仓库目前没装某类库」不等于「仓库禁止该类依赖」**，v1 把它写成架构约束属过度推断。
- SWR「体积更小」应在**相同构建条件**下测量后再主张。

**不必实现两套原型。** 若无「必须零依赖」的明确约束，直接以用户资源试点验证 TanStack；只有出现不可接受的包体/兼容/迁移成本证据时再重评。

## 4. 交付范围与顺序

v1 的 Phase 划分作废，改用评审 §14 的 PR 顺序（PR 0 诊断 → PR 1 基础设施+通知 → PR 2 用户资源 → PR 3 可见轮询 → PR 4 服务器 → PR 5+ 其余资源 → 独立后续：同步状态增强）。

**第一轮试点**：`NotificationBell` + `UsersView` + 关联的安全抽屉/通行密钥对话框。

- 通知验证既有 60 秒轮询、隐藏暂停、失败保留旧数据。
- 用户页验证分页、筛选、CRUD、批量部分成功、复杂编辑、两轴状态与关联查询。
- 详情场景直接用**已有**抽屉，不额外造页面。

**`ServersView` 移到下一独立提交组**：它除 `usePaged` 外还有外部探测、原生安装、升级、迁移、短期命令与幂等键，改造面远大于「也用了 `usePaged`」。

### 4.1 可验证的新鲜度目标

以下是**建议验收目标，不是已测得的生产 SLA**：

| 情况 | 目标与边界 |
|---|---|
| 本地写操作已完成 | 不等定时器，立刻失效并使活动查询重取；完成后显示服务器事实 |
| 写成功、重取失败 | 明确「已保存，最新数据暂未读取」，保留旧值；**不能误报写失败** |
| 隐藏后重新可见 | stale 的活动查询立即校验；新鲜缓存可在窗口内复用 |
| 用户列表持续可见 | 预算通过后启用 60 秒只读轮询；延迟约一个周期加请求耗时 |
| 页面持续不可见 | 不因间隔计时器发起新轮询 |
| 上游流量/健康数据 | 前端刷新只读到后端当前快照；**不声称前端轮询让上游实时化** |
| 网络离线/请求错误 | 延迟目标暂停成立；提示离线或陈旧，恢复后校验 |

**若用户列表轮询未通过负载门槛，必须降低频率或重新协商延迟目标；此时不能宣布「持续停留页面不更新」已经解决。**

## 5. 职责规则（取代 v1 的「明确不做的事」）

v1 的禁止项有几条是**库能力结论**或**过度概括**，改为团队职责约定：

1. **服务端数据只保留一个权威读取责任方。** Zustand 技术上可以承载异步数据，问题不是库能力，而是**项目不应维护两套互相竞争的服务器副本**。这是职责约定。
2. **必须处理认证会话的缓存生命周期。** v1 把它归入「不动 auth store」的豁免是错的——用户 DTO 会按调用者身份脱敏，**同一 URL 对不同身份不保证同数据**；长寿命缓存若只清 token 不清数据，账号 B 可能短暂看到账号 A 的旧列表，operator 可能看到 admin 缓存中的敏感字段。**无需重写认证业务，但必须接缓存生命周期。**（细则见评审 §6.1）
3. **`stores/site.ts` 也缓存服务端品牌/时区配置**，必须登记迁移或兼容责任。
4. **迁移一个资源时，只删掉被 Query 替代的取数与其状态同步代码。** URL 同步、订阅监听、草稿初始化、定时失效与浏览器副作用**仍须保留**——v1 的「删掉它的 useEffect」不安全。
5. **不一次性迁移全部资源**；未迁移资源允许维持原方式，但**不得为其新增一套长期失效机制**（如 API 模块里全局发 DOM 事件）。
6. **第一阶段不引入 SSE/WS**，也不引入跨标签页广播插件。
7. **不以 effect 数量归零作为完成指标**，以「服务器数据只有一个读取/失效责任方」为完成指标。
8. **后端同步状态增强是独立工作包**，不与前端库迁移合并冒充低成本改动。

## 6. 后续同步状态（独立工作包，不是 A/B 前置）

两档可选，**由 §2.4 的产品判断决定走哪档**：

- **轻量资源状态**：新增受权限约束的 `GET /api/admin/users/:id/sync-status`（及自助版 `GET /api/user/me/sync-status`），回答「当前可观察到哪些相关任务」。必须定义：含义是「可观察到的任务状态」而非「已应用到所有上游」（无活跃任务叫 `no_active_tasks`，**不能叫 `synced`**）；查询出错返回 unknown，**不得复用 `HasPendingSync` 的 `false on error` 语义**；限定覆盖哪些 sync task 类型。
- **完整 operation 契约**：仅当产品确实需要「这次操作何时完成」时才做，需补全 operation ID、目标代次、终态区分（`done` 只表示结束，不等于成功）、部分成功聚合规则、目标级授权与幂等键。

细则与文件范围见评审 §12。**这是后端同步可观测性项目，需单独估工与 ADR。**

## 7. 风险（补入 v1 遗漏项）

| # | 风险 | 缓解 |
|---|---|---|
| 1 | **会话泄露**：登出/换号/角色变更后旧缓存被复用 | 会话作用域进私有 key；会话变化时清空 cache；旧回调比较代次 |
| 2 | **草稿被覆盖**：后台刷新重置正在编辑的表单 | 草稿与查询快照分离；`setForm(query.data)` 不得无条件执行 |
| 3 | **批量勾选被清空**：`[items]` 变化即清空（`UsersView.tsx:391`） | 选择按查询范围/可操作 ID 管理；同范围后台刷新保留合法选中 |
| 4 | **旧响应晚到覆盖新结果** | queryFn 消费 `signal`；写操作开始时取消在途查询 |
| 5 | **重复 toast**：库重试与 Axios 既有 toast 叠加 | 迁移的 queryFn 用静默错误，由统一呈现规则处理 |
| 6 | **部分落库**：多步编辑后半段失败 | 按已尝试目标重取；不撤销已完成步骤；错误明确为「部分保存」 |
| 7 | **重查询放大**：列表刷新连带 Top 全表扫描与上游探测 | 解耦触发链；Top 独立低频；服务器探测与列表刷新分离 |
| 8 | 引入依赖与仓库现状相悖 | 先出 ADR（§8）；React Router 零依赖方案始终在场 |
| 9 | 迁移期新旧并存 | 明文职责规则（§5）；PR 验收范围明确列出未迁移读者 |

## 8. 决策归档

- 按 [`docs/adr/README.md`](adr/README.md)，重要决策应记录；但 README **不构成需要额外审批的强制流程**。
- **ADR 目录已有重复编号**：`0024`、`0025` 各有两个文件（`0024-credential-derivation-byte-equality` / `0024-psp-native-node-backend`；`0025-push-pull-decision-rule` / `0025-resync-membership-phase-order`）。补索引时**必须处理标题与完整文件名，不能只登记编号**。
- 拟编号暂定 **0034**，**合入前复核**，不硬编码为永久可用。
- **不要把提案写成「已接受」**；本稿状态是提案。
- 待补索引的五条：`0024-credential-derivation-byte-equality`、`0025-resync-membership-phase-order`、`0026-shutdown-drain-before-cancel`、`0027-sub-opaque-404`、`0028-local-login-enumeration-and-timing`。

## 9. 给评审者的问题（已收敛）

v1 把已可源码核实的事也列为问题，属推卸。**收敛为三个真正需要人判断的问题**：

1. **外部变化的可接受延迟是多少？**（决定是否需要资源轮询，以及周期定多少）
2. **实际并发管理规模是多少？**（在线 admin/operator 数、同时挂载的业务区域数——决定频率预算）
3. **是否需要 operation 级证据？**（即「这次操作何时完成」是否为真实产品需求——决定 §6 走轻量还是完整档）

已由源码回答、无需再问的：swr/router 能力、`X-Sync-Pending` 可查询性、`refresh()` 时序、恢复按钮显示条件。

## 10. 本次修订的核实记录

评审的下列断言已独立对源码核实，**全部成立**（基线 `72e777c`）：

| 断言 | 证据 |
|---|---|
| `refresh()` 返回 void，`await load()` 未等待请求 | `usePaged.ts:141` |
| 渲染读 `access`，非平铺字段 | `utils/userAccess.ts` |
| `[items]` 变化清空勾选 | `UsersView.tsx:391` |
| 未进 Top-N 的用量显示为 0 | `UsersView.tsx:619, 1204` |
| `Top` 遍历全体用户，`limit` 不消除扫描 | `handler/admin_traffic.go` |
| `HasPendingSync` 出错返回 `false` | `service/user/user.go:579` |
| 另有 3 处轮询（v1 漏报） | `NativeAgentUpgradeDialog.tsx:34`(3s)、`ServersView.tsx:2062`(4s)、`NodeMetricsDialog.tsx:90`(2s) |
| header 共 7 处，非 6 处 | 增 `handler/user_me.go:362` |
| SWR 支持 filter 式批量失效（v1 写错） | [SWR Mutation](https://swr.vercel.app/docs/mutation) |
| 写方法丢弃响应，header 到不了调用者 | `api/users.ts:70-76` |
| ADR `0024`/`0025` 编号重复 | `docs/adr/` 目录 |

**未核实、留待执行**：线上复现（R1–R9）与性能测量（评审 §3.3）——本稿与评审均未连接实际部署。

## 附：引用来源

- [React useEffect：数据获取的替代方案](https://react.dev/reference/react/useEffect)
- [TanStack Query 默认值](https://tanstack.com/query/latest/docs/framework/react/guides/important-defaults)
- [TanStack Query 可见性恢复重取](https://tanstack.com/query/latest/docs/framework/react/guides/window-focus-refetching)
- [TanStack Query 定向失效](https://tanstack.com/query/latest/docs/framework/react/guides/query-invalidation)
- [TanStack Query 跨标签页实验插件](https://tanstack.com/query/latest/docs/framework/react/plugins/broadcastQueryClient)
- [SWR mutation 与多 key 过滤](https://swr.vercel.app/docs/mutation)
- [SWR 重新验证](https://swr.vercel.app/docs/revalidation)
- [React Router `useRevalidator`](https://reactrouter.com/api/hooks/useRevalidator)
- [React Router 竞态处理](https://reactrouter.com/explanation/race-conditions)
- [Google AIP-151](https://google.aip.dev/151)
