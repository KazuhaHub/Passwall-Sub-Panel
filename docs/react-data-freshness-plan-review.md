# 前端数据新鲜度：源码评审与完整修订方案

- 评审日期：2026-09-18。
- 评审对象：[原计划](react-data-freshness-plan.md)。原稿保留，本文件是独立的修订建议与实施说明。
- 源码基线：`72e777c71ab3f1a1f1b913fa1513f25a020823ae`，评审时已 fetch `origin/main`。
- 状态：**建议方案，尚未实施；不表示依赖选型或后端扩展已经获得正式决策。**
- 核查方式：阅读现有前后端代码、路由、测试与 CI，并核对相关库的官方文档。**没有连接实际部署复现“解封仍显示封禁”，没有采集生产性能数据。** 下文明确区分已确认事实、待验证假设与建议参数。

## 1. 评审结论

推荐保留“引入 TanStack Query、按资源渐进迁移”的方向，但原计划不能直接交付实施。主要问题不是选错库，而是把四个不同问题合成了一个承诺：

1. 本次操作完成后，本标签页中的列表、抽屉、详情保持一致。
2. 其他标签页、其他管理员或后台任务改变数据后，当前页面发现变化。
3. PSP 数据库已经保存，但上游代理面板尚未同步完成。
4. 后端已经正确返回状态，前端仍使用旧对象、错误字段或误导文案。

这四类分别需要**共享查询与失效、外部变化检测、可观测的同步协议、正确的数据呈现**。一个 `QueryClientProvider` 不能包办它们。

建议最终拆成三个工作包：

| 工作包 | 内容 | 是否改后端 | 完成标准 |
|---|---|---|---|
| A：基础与用户试点 | 查询基础设施、用户列表及关联抽屉、通知铃铛、明确的失效关系与会话隔离 | 否 | 本页操作能刷新；切回页面能校验；不清空草稿/选择；不泄露旧会话缓存 |
| B：持续可见的新鲜度与迁移 | 对经过预算验证的只读查询启用可见时轮询；迁移服务器及其他资源 | 默认否 | 用户页持续打开时也能在约定时间内发现外部变化；已有任务轮询保持语义 |
| C：同步进度增强 | 如确有产品需求，补充资源同步状态，或进一步设计真正的 operation 契约 | 是，单独评审 | 能区分待处理、失败、未知与已观察到的任务结果，不能把没有 header 当成功 |

**A、B 是本次前端新鲜度改造的主线；C 不能伪装成“现有协议接一下即可”。** 如果只交付 A，应明确它没有解决页面始终可见时的全部外部变化。

## 2. 必须修改的问题与证据

### 2.1 高优先级：`X-Sync-Pending` 不是 operation 协议

原计划 §1.8、Phase 1 的“对该资源轮询直至 pending 清除”没有可用的查询契约。

源码证据：

- `internal/transport/http/handler/admin_user.go` 的六处 header 都位于写操作响应；`List`、`Get` 不返回用户同步状态。
- `internal/service/user/user.go:570` 的 `HasPendingSync` 仅检查该用户是否存在几类活跃 sync task；可能命中**前一次操作**的任务，查询出错还会返回 `false`。
- `internal/transport/http/handler/admin_sync_tasks.go:22` 的列表只消费分页、`status`、`type`，没有消费 `target_id` 或 operation ID。不能给现有请求凭空加参数就当后端已经支持。
- `internal/transport/http/router.go` 没有用户写操作对应的 `GetOperation` 路由。
- `web-react/src/api/users.ts` 多个写方法直接丢弃 Axios 响应，连 header 元数据也没有交给调用者。
- 全仓并非只有六处 header：`internal/transport/http/handler/user_me.go:362` 还有一处自助操作的响应。它同样不是可查询的 operation。

因此：

- GET 没返回这个 header，不代表 pending 已清除。
- 轮询用户的 `service_status` 只能看到 PSP 的有效服务状态，不能证明上游代理面板已应用配置。
- 扫描 sync task 列表第一页没有找到任务，不代表不存在任务；历史失败、取消、分页、并发操作和任务清理都使这种判断不可靠。
- 不能通过重复 POST“探测状态”，因为这会再次执行写操作。
- 即使 header 不存在，也只能说“本次响应未报告待同步”，不能反推“已逐个验证全部上游”。

**修改要求：从前端第一阶段删除 operation 轮询。保留并结构化 pending 提示，操作后立即刷新 PSP 数据；后端扩展放入 §12。** AIP-151 可以作为未来协议设计参考，不能用来证明现有 PSP 已经实现该协议。[AIP-151](https://google.aip.dev/151)

### 2.2 高优先级：失效范围被放大成了跨标签页、跨管理员广播

`invalidateQueries` 作用于当前 `QueryClient`。常规 SPA 中，它意味着**同一标签页内、同一 Provider 下**的查询。

它不会默认通知：另一个标签页、另一个浏览器、另一个管理员或仍用 `useState` 保存副本的旧组件。不同 query key 的同一用户也不会自动归一化合并，必须显式失效相应列表和详情。

应把“所有显示该用户的视图自动更新”改为有边界的验收：

| 场景 | 采用的机制 |
|---|---|
| 同一 QueryClient 内的活动列表/详情 | mutation 后定向失效并重取 |
| 当前不活动的已迁移页面 | 标记失效，下次挂载时校验 |
| 切走再回来，数据已 stale | 可见性恢复时重取 |
| 其他管理员操作，当前页面持续可见 | 该资源的定时只读轮询，或未来服务端推送 |
| 同浏览器的其他标签页 | 第一版依赖切回校验/资源轮询；即时跨页通知另列可选增强 |
| 尚未迁移的旧组件 | 继续保留它自己的刷新入口，或者随资源一起迁移 |

TanStack 的跨标签页同步插件是独立的实验性能力，不是默认语义；第一阶段不引入它。[Query Invalidation](https://tanstack.com/query/latest/docs/framework/react/guides/query-invalidation)、[broadcastQueryClient](https://tanstack.com/query/latest/docs/framework/react/plugins/broadcastQueryClient)

### 2.3 高优先级：聚焦重取和 `staleTime` 不会解决“始终停留页面不更新”

`staleTime` 到期只是数据变陈旧，**不是一个到期就发请求的计时器**。没有挂载、可见性恢复、重连、手动失效或轮询事件，就不会自动发现外部变化。

“切回来自动刷新”的默认行为也有条件：查询已启用、处于可用观察状态、数据 stale。设了非零 `staleTime` 后，短时间切回可能不会发请求。v5 默认使用 `visibilitychange`；不能承诺每次操作系统窗口焦点变化都等同于这个事件。[Important Defaults](https://tanstack.com/query/latest/docs/framework/react/guides/important-defaults)、[Window Focus Refetching](https://tanstack.com/query/latest/docs/framework/react/guides/window-focus-refetching)

**修改要求：分别定义“操作后刷新”“返回页面校验”“持续可见时更新”三个指标；至少在用户状态页面验证一种有界的外部变化检测机制。**

### 2.4 高优先级：解封根因不是二选一，原复现判据也不成立

已确认：

- `UsersView.tsx:974` 的 `actionResumeService` 调用了 `await load()`。
- 但 `UsersView.tsx:547` 的 `load()` 返回 `void`；`usePaged.ts` 的 `refresh()` 只是递增 React state。因此 **`await load()` 没有等待 GET 完成**。这不能单独证明永久陈旧的根因，但会使“操作已经刷新完”的时序判断失真。
- `UsersView.tsx:225` 的恢复入口主要对 `blocked_client`、`manual_suspended` 等状态开放。仅创建一个 `traffic_exceeded` 用户，未必能点击到原计划所说的按钮。
- `utils/userAccess.ts` 优先读取 `user.access`，其次读取平铺状态。单改 `enabled` 或 `service_disabled_reason` 不能保证实际渲染值改变。
- `domain/access.go` 会在清除人工暂停后继续计算账号禁用、过期、配额等事实；不需要等下一次 traffic poll 才重新显示这些限制。
- `UsersView.tsx` 同时保存列表、编辑对象、菜单对象、安全抽屉对象等副本。抽屉只在用户还存在于当前页时跟随列表更新，离开当前页后会保留旧快照。
- `UsersView.test.tsx` 已有“保存后重新打开时采用 update 响应，不回到旧列表值”的回归测试。迁移不能为了方便加一次全表 refetch，就删除这个行为保护。

“reason 清空、UI 仍封禁”不能直接推出产品语义问题；也可能是旧对象、未完成请求、失败响应或字段读取错误。必须比较**同一用户、同一次新 GET 的 `access.service_state` 与最终 UI**。

另外，`ResumeServiceAndSync` 先保存本地状态，再推送；`internal/service/user/unqueued.go` 明确存在“本地已保存、上游失败且重试入队失败，最终返回错误”的路径。不是所有失败响应都意味着本地没有变化。

**修改要求：按 §3 的矩阵排查；任何库选型结论都不能代替缺陷复现。**

### 2.5 高优先级：自动刷新会清掉批量勾选，可能覆盖表单草稿

`UsersView.tsx:391` 的 effect 以 `[items]` 为依赖清空选择。接入后台刷新后，任何行字段变化都可能清空正在准备执行的批量选择。

只把 `staleTime` 调大，或者只关闭 `refetchOnWindowFocus`，都不能可靠保护表单：mutation 失效、手动刷新和轮询仍然可能更新数据。

**修改要求：选择状态按查询范围/可操作 ID 管理；表单草稿与查询快照分离。具体规则见 §8、§9。**

### 2.6 高优先级：认证与角色变化后的缓存隔离缺失

现有 `stores/auth.ts` 支持同浏览器跨标签页同步登录状态；`api/client.ts` 还有独立的 refresh 失败退出路径。新增长寿命缓存后，如果只清 token、不清数据，账号 B 可能短暂看到账号 A 的旧列表，operator 可能看到之前 admin 缓存中的敏感字段。

这里尤其重要：用户 DTO 的字段会按调用者身份脱敏，同一 URL 不一定对不同身份返回同样的数据。

**修改要求：把会话切换、角色变更、退出、旧请求晚到纳入第一阶段，不能归入“不动 auth store”的豁免。无需重写认证业务，但必须接缓存生命周期。**

### 2.7 高优先级：只计算列表 QPS 会漏掉重查询和上游探测

`UsersView.tsx:407` 附近每次 `items` 变化都会调用 `topTraffic`。`admin_traffic.go:74` 的 `Top` 实际遍历全体用户，收集报告、排序，然后才按 `limit` 截断。调小 `limit` 不会消除全体用户扫描。

它也不是当前分页的逐用户用量接口：返回的是全局 Top-N。当前页里未进入 Top-N 的用户在 `UsersView.tsx:619`、`:1204` 被 `?? 0` 显示成 0，这是**已确认的语义错误风险**，与是否引入缓存无关。

`ServersView.tsx` 则会按页面 ID 集合自动探测服务器。普通列表刷新不应转变成每轮全页上游探测或重复写操作。

**修改要求：把查询之间的触发链列入预算；用户列表与排行榜解耦；编辑用量时读单用户接口；服务器列表与主动探测分开。**

### 2.8 中优先级：源码盘点漏掉已有轮询与真实改造单位

评审时重新统计：

| 指标 | 核查结果 | 应如何表述 |
|---|---|---|
| admin 顶层非测试 `.tsx` 文件 | 30 | 包含 Dialog、Drawer、Tab、辅助组件，不是 30 个页面 |
| 其中以 `View.tsx` 结尾 | 17 | 包含 Placeholder |
| 含字面量 `useEffect(() =>` 的文件 | 26 | 只是文本统计，不等于 26 个数据获取点，更不是 26 次替换 |
| API 非测试 `.ts` 文件 | 31 | 含类型/客户端等模块，不是全部都在 view 内直接调用 |
| `src` 下测试文件 | 54 | 与原统计一致；实施时重新统计，不把数字写成长期约束 |

除 NotificationBell 的 60 秒轮询外，至少还有：

- `NativeAgentUpgradeDialog.tsx`：根据任务终态停止的升级状态轮询。
- `ServersView.tsx:2047` 附近：安装状态每 4 秒轮询。
- `NodeMetricsDialog.tsx`：等待采样结果的延时查询循环。

上述行为需要按各自任务协议迁移，不能简单删掉定时器统一套用户 pending 逻辑。已有原生节点升级接口可以作为“真正拥有 task ID 与终态”的内部对照，但它不等于旧用户 sync task 队列。

### 2.9 中优先级：备选方案比较有事实错误与过度推断

- **SWR 有按 key 过滤函数批量 mutate/revalidate 的能力**，“无 predicate 式失效”不正确。体积更小也应在相同构建条件下测量，而非作为已确认事实。[SWR Mutation](https://swr.vercel.app/docs/mutation)
- React Router 有 action/fetcher revalidation、请求取消与常见竞态管理；它缺少的是 TanStack 风格的通用资源缓存体系，不能概括成“没有任何请求协调”。[React Router Race Conditions](https://reactrouter.com/explanation/race-conditions)、[Actions](https://reactrouter.com/start/data/actions)
- SSE/WS 是变化通知渠道，可以与 Query 配合，不应当作替换缓存层的同类选项。广播给 N 个客户端仍有 O(N) 发送成本；事件触发查询还会产生 N 次重取，不能声称扇出降到 O(1)。
- 节点数据面推/拉测量不能直接作为管理员浏览器刷新负载的证据。资源数量、消费者、触发频率、认证和查询成本不同。
- 仓库目前没装某类库，不等于仓库有“禁止该类依赖”的架构约束。
- React 文档支持考虑客户端缓存，但没有宣布所有 Effect 取数都是错误，也没有把所有候选库严格排成名次。PSP 是客户端 SPA，SSR 缺点不是本次选型的核心收益。[React useEffect](https://react.dev/reference/react/useEffect)

### 2.10 中优先级：禁止项与 ADR 的说法需要收紧

- Zustand 技术上可以承载异步数据，也可以实现订阅/失效；问题是项目不应维护两套互相竞争的权威服务器副本。应写成**团队职责约定**，不是库能力结论。
- 现有 `stores/site.ts` 也缓存服务端品牌/时区配置，不只有 auth store。必须登记迁移或兼容责任。
- “迁移一个页面就删掉它的 useEffect”不安全：URL 同步、订阅监听、草稿初始化、定时失效和浏览器副作用仍需要保留。只删掉被 Query 替代的**取数及其状态同步代码**。
- ADR README 建议记录重要决策，但没有构成一个需要额外审批的强制流程。本方案建议补 ADR，用于记住边界与不变量。
- ADR 目录中 `0024`、`0025` 已各有两个文件；补索引时要处理标题/完整文件名，不能只登记编号。新增编号在实际合入前复核；现在可以暂定 0034，不应硬编码为永久可用。

## 3. Phase 0：缺陷定性与请求基线

本阶段可以先做，不依赖最终选择 TanStack 还是 Router。允许补针对性复现测试；不要求为了“零生产代码”而推迟一个已明确的独立 bug 修复。

### 3.1 建立诊断记录

在开发环境或隔离测试数据上执行，记录部署版本、用户 ID、操作入口、浏览器标签页、操作前后时间。不要保存 token、UUID、订阅 URL 或恢复码到调试文档。

每次操作至少采集：

1. 点击前列表 GET 的该行：`enabled`、`auto_disabled_reason`、`service_disabled_reason`、`account_status`、`service_status`、`access`。
2. mutation 请求是否真正发送、响应状态、错误正文、`X-Sync-Pending`。
3. mutation 完成后是否发出新的列表/详情 GET；请求参数是否仍是正确页码和分组。
4. 新 GET 是否成功，是否晚于操作，响应是否来自缓存；检查部署实际的 `Cache-Control`、代理缓存头和 Service Worker，而不是预设“都是 React 的错”。当前代码已有 `NoStoreAPI` 中间件。
5. 新 GET 的 `access` 与 UI 使用的对象是否一致；区分列表行、已打开抽屉、编辑表单和菜单快照。
6. 后续再次受限时，记录是账号限制、配额/过期仍存在、新的违规请求，还是后端后台任务重新写入。不能只按时间接近 traffic poll 就归因给它。

### 3.2 复现矩阵

| 编号 | 构造方式 | 操作与正确判据 |
|---|---|---|
| R1 | 账号有效、未到期、未超额，服务为 `service_manual` | 恢复后新 GET 的 `access.service_state=active`；列表/只读抽屉跟随 |
| R2 | 同上，服务为 `blocked_client` | 恢复后校验服务状态及违规计数；避免测试客户端立即再次触发封禁 |
| R3 | 先人工暂停，再使配额超限或到期 | 点击恢复，暂停原因清除，但 `access` 仍表示超额/到期；文案不得声称代理已经可用 |
| R4 | 账号禁用，且有独立服务限制 | 分别测试“启用账号”和“恢复服务”；两者不得互相替代 |
| R5 | 上游不可达，但任务可持久化 | 本地状态刷新，显示“上游待同步”；不把本地可用当作上游已确认 |
| R6 | 上游不可达且入队失败（fixture） | 请求错误但本地可能已变，仍重取本地事实；不得自动重复非幂等操作 |
| R7 | 慢 GET 与新 GET 乱序、切筛选、开着抽屉翻页 | 老响应不能覆盖新查询；详情不能永久保留开抽屉时的用户副本 |
| R8 | 第二标签页/另一管理员执行操作 | 分别验证切回重取与持续可见时轮询，不能把同页失效当作跨用户事件 |
| R9 | 编辑为多个顺序 API，第二或第三步失败 | 已成功的字段仍显示最新；错误明确是部分保存，草稿保留 |

分类依据：

- 新 GET 没发出/失败：请求触发或错误处理问题。
- 新 GET 的 `access` 已正确、UI 不正确：前端呈现/对象副本问题。
- 新 GET 与数据库预期不同：后端状态计算或业务语义问题。
- 本地事实正确、上游尚未应用：同步可观测性问题。
- 此前已经恢复、之后又有明确新事件使其受限：后续业务事件，不能归类为首次刷新失败。

### 3.3 性能测量

对用户列表、单用户详情、流量 Top、通知、服务器列表/探测分别记录：请求数、响应体大小、p50/p95、错误率、数据库工作量、是否会请求上游。使用相同数据集、页面大小与浏览器场景比较改造前后。

建议覆盖 100 / 1,000 / 10,000 用户，25 / 100 行页面；如果实际部署规模不同，用实际规模与预期峰值替换。至少做 30 次稳定读取，另外记录冷启动，不能把缓存热路径当作全部成本。

轮询基础估算：

```text
QPS_poll = Σ(该资源可见的浏览器实例数 × 每轮实际请求数 / 周期秒数)
QPS_total ≈ QPS_poll + 挂载/切回请求 + 操作后失效请求 + 重试请求
后端工作量 ≈ Σ(各接口 QPS × 单次数据库/上游成本)
```

一个 SPA 当前不一定挂载所有路由，但可能同时挂载布局通知、多个面板、抽屉和保活 Tab；按实际活动查询统计，不按文件数量。相同 key 的并发请求可合并，不等于任意时刻发生的所有重取都会被压成一次。

产出一份短记录：复现归因、资源/调用者清单、关键接口成本、建议刷新周期与可接受负载。没有生产在线管理员数量时，给出假设与压力测试结果；不要因此阻塞基础设施、取消传播和同页失效这些不需要持续轮询的工作。

## 4. 修订后的选型与交付范围

### 4.1 推荐 TanStack Query 的实际理由

PSP 的查询不只属于路由：同一个用户同时出现在列表、抽屉、流量选择器、权限提示和用户门户；还有通知和按任务 ID 的轮询。使用按资源键管理的缓存，可以在保留现有 Router 和 Axios 的基础上逐步替换这些读模型。

建议采用实施时验证兼容的 **TanStack Query v5 稳定版本**，以 package-lock 固定实际安装版本。不要在方案中绑定未经验证的“最新小版本”。当前仓库 `package.json` 与旧文档的工具版本存在差异，应以源码与 lockfile 为准。

| 方案 | 修订后的评价 |
|---|---|
| TanStack Query | 适合本项目按资源、抽屉、任务查询渐进迁移；新增依赖与缓存生命周期是实际代价 |
| SWR | 同样可用，也支持跨 key 重新校验；不因错误的能力比较而排除。最终偏好 TanStack 是工程取舍 |
| React Router loader/action/fetcher | 零新增库，具备路由数据重校验和并发协调；需要重构现有取数及 mutation 入口，且独立组件仍需设计共享数据边界 |
| 小型自建刷新 hook | 可以修复具体切回刷新问题；如继续加共享缓存、取消、重试、会话边界和广播，维护成本需重新估算 |
| SSE/WS | 后续外部变化通知机制，可与任一数据层组合，不纳入当前依赖选型投票 |

无需实现两套完整原型。若没有“必须零依赖”的明确约束，直接以用户资源试点验证 TanStack；只有出现无法接受的包体、兼容或迁移成本证据时再重评。

### 4.2 第一轮具体试点

先做 **NotificationBell + UsersView + AccountSecurityDrawer/AdminPasskeysDialog**。

- NotificationBell 验证现有 60 秒轮询、隐藏暂停、失败保留旧数据。
- UsersView 验证分页、筛选、CRUD、批量部分成功、复杂编辑、两轴状态和关联查询。
- 已有抽屉/通行密钥对话框就是详情场景，不额外新增一个业务页面。

ServersView 放到下一独立提交组：它除了 `usePaged`，还有外部探测、原生安装、升级、迁移、短期命令、幂等键等，改造面远大于“也用了 usePaged”。

### 4.3 可验证的新鲜度目标

以下是建议验收目标，不是已测得的生产 SLA：

| 情况 | 目标与边界 |
|---|---|
| 本地写操作已完成 | 不等定时器，立刻使相关缓存失效并启动活动查询重取；完成后显示服务器事实 |
| 写成功、重取失败 | 明确“已保存，最新数据暂未读取”，保留旧数据显示陈旧状态；不能误报写失败 |
| 隐藏后重新可见 | stale 的活动查询立即校验；新鲜缓存可在其窗口内复用 |
| 用户列表持续可见 | 预算通过后启用 60 秒只读轮询；可见数据延迟目标约为一个周期加请求耗时 |
| 页面持续不可见 | 不因间隔计时器发起新轮询；已经发出的请求可能完成，不能据此断言暂停失效 |
| 上游流量/健康数据 | 前端刷新只读到后端当前快照；总延迟还包括后端采集周期，不声称前端轮询让上游实时化 |
| 网络离线/请求错误 | 延迟目标暂停成立；展示离线或陈旧提示，恢复连接后校验 |

如果用户列表轮询未通过负载门槛，降低频率、优化查询或重新协商延迟目标；此时不能宣布“持续停留页面不更新”已经解决。

## 5. 代码组织与文件级改造清单

建议新增一个小型 `query/` 目录，不做通用“万能数据仓库”：

```text
web-react/src/
  query/
    client.ts                 # QueryClient 工厂、默认错误/重试策略
    keys.ts                   # query key factory；只定义真实已迁移资源
    policies.ts               # 新鲜度、轮询、gc 策略及调整依据
    session.ts                # 会话生命周期与缓存隔离，避免 auth/client 循环依赖
    QuerySessionProvider.tsx   # 当前会话对应的稳定 Provider 边界
    invalidation.ts           # 用户等业务操作的关联失效规则
    users.ts                  # 用户 queryOptions/hooks 与 mutation 编排
    alerts.ts                 # 通知查询
    servers.ts                # 后续阶段添加
  hooks/
    usePageState.ts            # 从 usePaged 提取的分页/URL/UI 状态
    usePaged.ts                # 迁移期间保留旧调用者兼容实现
  api/
    requestOptions.ts         # signal、silent 等调用选项（可复用现有类型）
    mutationResult.ts          # 已保存结果及 sync header 元数据
  test/
    queryTestUtils.tsx         # 每测试独立 client + Provider + 清理
```

需要修改的既有文件：

| 文件/目录 | 具体任务 |
|---|---|
| `package.json`、`package-lock.json` | 添加 Query 依赖；提交 lockfile；不引入调试插件作为生产必需项 |
| `main.tsx` 或 `App.tsx` | 选择一个位置挂 Provider；确保 Router、抽屉、通知共享当前会话 client |
| `stores/auth.ts`、`api/client.ts` | 接缓存生命周期，保留 single-flight refresh；审查旧请求重放与会话变更 |
| `api/users.ts` | GET 支持取消/静默错误；试点 mutation 返回结构化响应元数据 |
| `api/alerts.ts`、`api/traffic.ts`、`api/groups.ts`、`api/limitEnforcement.ts` | 给实际迁移的查询补 options，其他 API 按需改 |
| `hooks/usePaged.ts`、现有测试 | 提取分页状态；兼容旧调用者，不在第一提交把所有页面一并改变 |
| `UsersView.tsx` | 替换取数、刷新、局部快照、选择重置及用量读取；保留草稿与交互状态 |
| `AccountSecurityDrawer.tsx`、`AdminPasskeysDialog.tsx` | 以用户 ID 对接详情；明确关闭/换用户时清理临时秘密 |
| `NotificationBell.tsx` | 删除自己的 fetch effect/timer，接统一通知查询 |
| `locales/zh-CN`、`locales/en-US` | 新增保存/待同步/陈旧/用量未知等文案；按项目流程生成繁体资源 |
| 测试与浏览器脚本 | 见 §13；所有直接 render 迁移组件的测试补 Provider |
| ADR 和原计划 | 实施决策确认后记录理由，更新原计划状态与范围 |

## 6. 查询基础设施：必须先固定的约定

### 6.1 认证与缓存生命周期

推荐为每个已认证会话提供独立的 client 生命周期，并把会话范围放入私有 query key；public 查询另有明确命名空间。

1. 会话身份由面板 API 基址、`userId`、`role` 与 `authEpoch` 标识。`authEpoch` 是不含秘密的会话代次，在新登录、退出后重新登录、有效身份/角色切换时变化；普通 access-token refresh 不改变代次。
2. `authEpoch` 可作为 `psp_user` 持久化身份元数据的一部分，由所有完成登录的入口统一设置，并通过现有 storage 监听同步。兼容旧存储时定义一次初始化逻辑，不要每次 render 生成新代次。
3. Provider 内的 client 对一次会话稳定；不能在每次 render `new QueryClient()`。会话变化时隔离新的 Provider，旧页面与对话框立即退出旧身份上下文。
4. 旧 client 取消 GET 查询并清空 query/mutation cache；旧异步回调比较启动时的代次，过期回调不写新缓存、不弹旧用户操作 toast、不恢复旧 dialog。
5. Axios 的退出路径与 Zustand logout 都调用同一个无 UI 依赖的会话失效入口，避免 `client → auth → api → client` 新循环。
6. 审查 `performRefresh`：旧会话启动的 refresh 响应不得在切换到新账号后覆盖新 token；等待 refresh 的旧 GET 也不得使用新账号 token 重放。用捕获的会话代次检查解决，而不是把原始 token 放入 query key。
7. 跨标签页 storage 事件按**身份/会话变化**处理，不因每次 token 刷新都清空数据。`storage.clear()` 的 `key=null` 也要重新核查认证状态。
8. 不添加 Query 持久化插件，不把用户 DTO、密码、订阅凭证写到 localStorage/IndexedDB。

私有查询的 `enabled` 必须包含认证与权限条件；只靠隐藏按钮不够。`enabled:false` 的查询不会正常参与自动失效重取，因此弹窗打开/权限恢复后的重新启用行为也要测试。[Disabling Queries](https://tanstack.com/query/latest/docs/framework/react/guides/disabling-queries)

### 6.2 默认策略

建议把这些值集中在 `policies.ts`，附资源负责人、测量日期和修改理由：

| 资源 | 初始 `staleTime` | `gcTime` | interval | 说明 |
|---|---:|---:|---:|---|
| 用户列表/只读详情 | 15 秒 | 5 分钟 | 用户列表预算通过后 60 秒；详情默认无 | 本地 mutation 显式失效，不受 15 秒限制 |
| 通知 | 30 秒 | 5 分钟 | 60 秒 | 保留现有节奏，隐藏时停；打开菜单允许主动校验 |
| 分组等下拉字典 | 5 分钟 | 15 分钟 | 无 | 写入分组后失效；过滤条件不同要分 key |
| 用户页附加 Top 用量 | 5 分钟 | 5 分钟 | 无 | 不随列表轮询；先限制重查询放大，见 §9.4 |
| 弹窗内单用户用量/限制事实 | 15 秒或按用途显式刷新 | 5 分钟 | 无 | 以 user ID 为 key；编辑用量要求打开时获取真实值 |
| 服务器列表 | 30 秒 | 5 分钟 | 初期无 | 先审查探测联动，再评估 60 秒只读刷新 |
| 设置/模板/规则只读快照 | 5 分钟 | 5 分钟 | 无 | 草稿独立；保存后定向失效 |
| 已有原生任务状态 | 按任务协议 | 短期 | 保留原任务节奏/终态条件 | 不套全局用户列表策略 |

这些是**试点起始配置**，不是测量结论。新鲜度窗口来自业务容忍度，轮询周期同时受负载约束；不能声称 `staleTime` 能仅由 QPS 数据自动推导出来。

全局约定：`refetchOnMount/refetchOnWindowFocus/refetchOnReconnect=true`，`refetchInterval=false`，`refetchIntervalInBackground=false`。第一阶段不使用 `staleTime:'static'`，以免与显式失效预期冲突。

重试规则：

- mutation：`retry:false`；断网重连不能自动重放创建用户、重置凭证、生成命令等操作。
- Query：仅经确认安全的只读请求，可对网络错误和可恢复 5xx 自动重试最多 1 次；明确的“能力未配置”503不重试。使用约 1 秒延迟。
- 401 由原 Axios single-flight 机制处理；最终 401、403、404、验证错误、取消请求不让 Query 再试。
- 429 第一版不自动重试；未来若实现，必须尊重 Retry-After 并有上限。
- 昂贵查询与任务观察查询可以显式 `retry:false`，避免“库重试 + 自己的下一轮轮询”叠加。

### 6.3 错误、取消和 loading

所有迁移的 queryFn 必须：

1. 把 Query 提供的 `signal` 传到底层 Axios；一项查询内多个 GET 时，每个都传。
2. 保持错误抛出，不用 `catch(() => [])` 或 `catch(() => undefined)` 把失败伪装成成功空数据。
3. 使用 `_skipErrorToast:true`，由统一的查询呈现规则处理错误；保留 Axios 的认证与 2FA 强制跳转逻辑。
4. 初次加载失败显示局部错误和重试入口；已有数据后刷新失败保留数据，并标“刷新失败/数据可能过期”。
5. 区分首屏 `isPending`/无数据与后台 `isFetching`；后台刷新只显示轻量指示，不把整个表格替换为 spinner。
6. 未获取到数据的 disabled query 不应让关闭的弹窗永远显示 loading。
7. 取消属于正常控制流，不弹错误、不重试；消费 signal 才能取消实际 HTTP。

现有 Axios 错误 toast 在 Query 的每次重试前就可能弹出，而其 1.5 秒去重窗口覆盖不了整个重试链。这就是需要查询侧静默、最终状态统一呈现的原因。

## 7. Query key 与 API 契约

### 7.1 Key 命名

示意结构，最终类型按项目实际 DTO 落地：

```ts
type QueryScope = {
  apiBase: string
  userId: number
  role: string
  authEpoch: string
}

const privateRoot = (s: QueryScope) => ['private', s] as const

export const userKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'users'] as const,
  lists: (s: QueryScope) => [...userKeys.all(s), 'list'] as const,
  list: (s: QueryScope, p: NormalizedUserListParams) =>
    [...userKeys.lists(s), p] as const,
  details: (s: QueryScope) => [...userKeys.all(s), 'detail'] as const,
  detail: (s: QueryScope, id: number) =>
    [...userKeys.details(s), id] as const,
  passkeys: (s: QueryScope, id: number) =>
    [...userKeys.all(s), 'passkeys', id] as const,
  limits: (s: QueryScope, id: number) =>
    [...userKeys.all(s), 'limits', id] as const,
}
```

约束：

- `NormalizedUserListParams` 必须包含 page、page_size、keyword、sort_by、sort_dir、group_id、enabled 等所有影响响应的参数；空字符串、缺省值与 legacy search 别名统一规范化。
- 不在 queryFn 闭包里偷偷依赖 key 没有表达的 groupFilter、用户、时区或语言。
- 图表 key 包含用户/节点/范围、since/until、period、实际 timezone；默认时区应先解析，再构造 key 和请求。
- 相同 key 只能对应同一种原始响应形状。组件局部映射用 `select`，不要把“完整用户数组”和“只有 id/name 的选择器数组”写到同一 key。
- list、detail、passkeys、usage 使用不同分支；不同 page 的缓存不互相覆盖。
- 不放密码、token、恢复码、订阅链接到 key；日志和调试面板中的 key 也不应携带秘密。

### 7.2 API 层继续只负责 HTTP

`api/` 不导入 QueryClient，不猜业务失效关系，不新增第二套全局缓存。

查询逐步补调用选项，例如：

```ts
export interface ReadOptions {
  signal?: AbortSignal
  silent?: boolean
}

export async function getUser(id: number, opts: ReadOptions = {}) {
  const { data } = await client.get<User>(`/admin/users/${id}`, {
    signal: opts.signal,
    _skipErrorToast: opts.silent,
  })
  return data
}
```

`listUsers` 已有第二参数 `AbortSignal`。改签名时逐个更新旧调用者与 API 映射测试；不要偷偷改变参数含义导致现有取消失效。可以先保留旧签名、追加 options，再在统一迁移时收敛。

试点写方法需要向上暴露响应元数据：

```ts
export interface MutationResult<T> {
  data: T
  syncPending: 'reported' | 'not_reported'
}
```

使用枚举而非“synced: true”，防止把 header 缺失当作已确认同步。204 对应 `MutationResult<void>`；返回 User 的 mutation 保留 User；一次性密码仍只在相应结果 UI 中使用。

可以在同一资源迁移提交中调整其 mutation 返回类型并更新所有调用者、测试。未迁移 API 保持原样。全局 pending toast 若保留兼容，应增加一个明确的 per-request opt-out，让已迁移 mutation 自己展示结构化结果，避免“成功 toast + 全局 warning + 局部 warning”重复三次。

### 7.3 不缓存一次性秘密

创建用户的初始密码、重置密码、恢复码、安装令牌/命令等不进入 query cache，不做 query 自动重试或后台 refetch。

如果使用 `useMutation` 保存了这些返回值，关闭结果框/换目标/退出时调用 reset 并清除局部值；对该 mutation 设置适合的短期 GC。`gcTime:0` 只影响符合回收条件的缓存，并不代替清理仍被观察的结果。

## 8. 分页 hook：保留交互语义，拆掉手工服务器状态

### 8.1 提取 `usePageState`

从 `usePaged` 提取以下职责：页码、pageSize 本地偏好、已提交关键词、排序、URL 前后同步与 setter。

保留：

- `psp_page_size` localStorage 偏好。
- page/q/sort 的 URL 形式与默认值省略。
- `paramPrefix`；浏览器 Back/Forward 必须同时改变 UI 与请求。
- 搜索框草稿不发请求，提交时才更新 keyword。
- pageSize、关键词、排序、分组筛选改变时回到第 1 页。
- 更新 URL 不覆盖其他组件使用的参数。

需要明确修正：当前 groupFilter 是 fetcher 闭包，由额外 `refresh()` effect 补救。新实现把它纳入 key，并以同一状态更新回到第一页；删除这条补丁 effect。避免先请求“新分组的旧页码”，再请求第一页。

URL 与本地 state 不应两边无条件 effect 互写导致循环或中间态；可以把已提交分页参数以 URL 为权威来源，pageSize 继续使用本地偏好。沿用现有公开行为，不借此改变路由格式。

### 8.2 迁移期兼容

UsersView 直接组合 `usePageState + useQuery(userListOptions(...))`。ServersView 尚未迁移时继续用 `usePaged` 的兼容取数实现；共享分页状态提取必须跑原有 hook 测试。

等最后一个调用者迁移后，删除旧 hook 的 items/total/loading/error/fetch effect/refreshTick，而不是长期维持两个权威副本。

新 refresh 明确返回 Promise；需要等待读取完成的路径使用 `await query.refetch(...)` 或明确定义的 invalidation helper。需要显示刷新失败时检查结果或使用抛错选项，不能认为 `await invalidateQueries()` 默认一定把所有读取失败抛出来。

### 8.3 分页显示与操作安全

- 同一列表翻页可使用上一页作为 placeholder，前提是 `isPlaceholderData` 期间禁用行写操作、批量选择及依赖新页内容的动作，并明确正在翻页。
- 切 user ID 的详情不复用另一个用户的 placeholder；切会话不复用任何私有 placeholder。
- 删除最后一页最后一项后，成功拿到最新 total，再把 page 收敛到有效最大页，避免无限回跳。
- 首屏失败不能显示“没有用户”；后台失败不能把旧数据清空成空列表。
- 选择状态在**查询范围改变**时清空；同范围后台刷新时保留仍存在且仍可操作的选中 ID，移除已删除/不再允许操作的 ID。
- 不以 `[items]` 对象引用作为清空选择的条件；也不依赖结构共享碰巧保持引用来避免回归。
- 批量操作启动时冻结目标 ID；刷新不能把后续循环目标改成另一批行。执行前保留权限检查，服务端仍是最终权限裁决者。

## 9. 用户资源迁移的完整步骤

### 9.1 拆分读模型

1. 用户分页列表使用 `userKeys.list`。
2. 分组字典、服务器能力字典成为独立查询，按真实权限启用；不因用户列表更新而反复下载。
3. 安全抽屉与通行密钥对话框的目标保存 `userId`，打开时订阅 detail/passkeys。展示字段以当前查询为准。
4. 菜单也尽量保存目标 ID，从当前数据读取；目标消失时关闭或禁用动作。
5. 编辑表单保存“目标 ID + 打开时基线 + 可编辑草稿 + dirty 标记”。Query 数据更新不自动覆盖 dirty 草稿。
6. `limit-enforcement` 按 user ID 查询并传 signal；快速由 A 用户切到 B 用户时，A 的慢响应不能填到 B 的提示里。
7. 纯展示的 `useMemo`/map 可以保留，但不再把 query.data 复制进另一个 `useState` 形成长期镜像。

抽屉为关闭动画保留的 `shown` 快照可以存在，但只承担退场动画；打开状态必须跟随该 ID 的查询。404 显示“用户已不存在”并禁用操作；403清除敏感内容，而不是继续展示缓存快照。

### 9.2 Mutation 统一约定

每类写操作通过资源级 mutation hook/编排函数调用 API。**失效放在 hook 的稳定回调或编排层，不只放在一次 `mutate` 调用的临时回调里**，避免组件卸载后跳过关键清理。

一般流程：

```text
确认输入与目标 → 标记该目标操作中 → 调用写 API
  → 若有完整、仍属本会话的响应，可用于对应详情
  → 使受影响读模型失效，并刷新活动观察者
  → 单独呈现“写结果”“读取新状态结果”“上游 pending 提示”
```

注意：

- 已开始的旧 GET 可能在写成功后才返回。写操作开始时取消相关进行中查询；写结果落缓存前再次处理竞态，避免旧 GET 覆盖新结果。只在 mutation 结束时随便 `setQueryData`，但完全不消费 signal/不取消旧请求，并不安全。
- 对仅修改展示字段且不改变当前筛选命中、排序位置的更新，可以用完整响应更新当前已缓存行，让已保存值立即可见，再后台校验；可能改变成员关系、排序或 total 时以列表重取为准。任何补丁都限于同一会话、同一实体，且不得凭请求值合成派生的 `access`。
- 保留现有保存回显测试的目的，新增“保存前已发出的旧 GET 晚到”测试。测试中的新 GET 应返回 fixture 的当前服务端状态；如果真实部署在写响应后发起的新 GET 仍返回旧值，要排查代理缓存/读一致性，不能靠延长 staleTime 掩盖，也不能在没有版本号时承诺辨认所有远端旧快照。
- 不手工把 `enabled=true` 推导成 `access.proxy_enabled=true`。
- 不用 `getUser` 的 DTO 直接拼接到所有筛选列表中；一个更新可能改变筛选命中、排序、总数和页边界。第一版优先失效列表。
- 不把 204 包装成凭空构造的 User。
- 串行编辑 `updateUser → setUserTraffic → setEnabled` 在成功和失败结束时均按已尝试目标重取相关事实。后一步失败不能撤销已完成的前一步，更不能用第一个响应覆盖最终状态。
- 可能部分落库的错误路径也要失效。真正未发送的本地验证失败无需发刷新请求。
- 操作成功后的 refetch 失败不得让调用者重试写操作；两者在 UI 和错误类型中分开。
- 同一用户的冲突操作在 UI 中串行或禁用；不要通过轮询重发 mutation。

### 9.3 最小失效矩阵

表内“失效”默认表示当前会话中相关缓存标 stale，活动查询重取；尚未迁移的读者必须有显式兼容刷新，不会自动获益。

| 写操作 | 必须重新校验的读模型 |
|---|---|
| 创建用户 | 用户列表、已有 dashboard 汇总、相关分组计数、sync task 列表（若已接入） |
| 修改名称/邮箱/备注 | 用户列表、该用户详情；依赖名称的选择器 |
| 修改分组/配额/到期/角色 | 用户列表/详情、该用户用量/限制事实、相关汇总；自身角色变化走会话边界 |
| 启用/禁用账号、暂停/恢复服务 | 用户列表/详情、该用户有效 access、相关汇总；sync task 列表；有待同步提示时仍立即刷新本地事实 |
| 调整用量 | 该用户用量、列表/详情 access、Top、相关图表/汇总；不要等列表 items 引用变化才刷新 |
| 删除用户 | 用户列表、相关计数、汇总、sync task 列表；活动详情按后端实际存在/待删除状态更新 |
| 重置 UUID/订阅凭证 | 用户列表/详情、依赖凭据的只读区域；一次性结果局部展示 |
| 重置 2FA / 撤销 passkey | 用户详情/列表安全计数、该用户 passkeys；清理已展示的过期临时结果 |
| 解除 SSO | 用户列表/详情 |
| 重置紧急访问使用量 | 用户列表/详情和相关用量 |
| 修改个人规则 | 该用户规则查询；不要假定自动清除后端订阅渲染缓存 |
| reconcile / 手动流量采集 | 按实际操作结果失效用户、用量、节点/汇总；操作本身维持显式触发 |

不要按 URL 字符串自动猜关联关系，也不要每次写操作都 `invalidateQueries()` 全局扫一遍。

批量操作继续用现有 `allSettledLimited` 控制写并发。完成后合并失效：用户列表一次、成功/结果不确定的目标详情按需、关联汇总一次。避免 100 个用户每完成一次都刷新整表。保留部分失败目标供重试；成功目标不自动重复提交。

删除特别处理：若后端返回的是已落库待删除状态，不应先把实体永久从缓存抹掉；先遵循新的 GET。确定硬删除后，关闭正在显示该实体的详情，再移除其缓存。不能让活动 detail 立即重新抓取一个必然 404 的 ID 并反复 toast。

### 9.4 用量数据的第一阶段可行做法

在“不改后端接口”的边界下：

1. 保留全局 Top 数据作为独立、低频的辅助查询，key 包含 limit；去掉 `[items] → topTraffic()` 的自动触发链。普通列表轮询不刷新它。
2. Top 缺失某个用户时展示“— / 暂未读取”，**不能显示 0，也不能作为编辑初值**。
3. 编辑用户时调用现有 `userTraffic(userId)` 获取真实报告。用量输入在读取成功前禁用；读取失败仍允许编辑无关字段，但不得向用量接口提交推测值。
4. 用量输入记录打开时真实基线和是否主动修改。只在使用者改变该字段时提交，不能因后台计数变化自动生成用量重置。
5. 需要准确用量时可在详情显式读取；不要为了填满 100 行而默认并发发送 100 个 GET。
6. 后续若产品要求每行都持续显示准确用量，另立小型后端改造：列表 DTO 批量补充用量，或支持限定 ID 的批量报告。需复用现有批量报告能力、校验 operator 权限和查询规模，完成预算后再启用自动刷新。

Top 辅助值可以显式标读取时间；列表状态刷新成功不代表用量列也在同一时刻刷新。页面“最后更新”应对应具体查询，不能误导为整页所有数据同时最新。

### 9.5 表单、草稿与恢复服务文案

- 背景新快照可以更新只读状态、用量展示和“数据已变化”提示，但 dirty 表单不被 `setForm(query.data)` 重置。
- 提供显式“重新载入已保存值”操作，并在有未保存修改时确认丢弃草稿。
- 现有接口没有 ETag/版本化写入契约，第一阶段不能承诺防止多管理员同时保存造成覆盖；只能保护本地草稿并提示已观察到的变化。需要真正冲突检测时另改后端。
- 恢复操作先显示“已清除服务暂停限制”；新 GET 表明有效服务 active 时才可显示“服务状态已恢复”。
- 如果仍到期/超额/账号禁用，明确剩余原因；如果 `X-Sync-Pending` 报告 pending，追加“上游待同步”，不把代理连通性宣告为已验证。
- 若刷新失败，用“操作已保存，当前状态暂未读取”，避免继续断言“仍被封禁”就是最终结论。

## 10. 通知、服务器与剩余资源迁移

### 10.1 NotificationBell

1. `getAlerts` 接 signal，保留静默错误。
2. `useQuery` 保留 60 秒轮询，隐藏时暂停；只在有通知权限的认证区域挂载。
3. 排序用 `select` 或只读派生，不修改 cache 中的原数组。
4. 保留后台失败时的上次结果；无任何成功数据且请求失败时显示“暂时无法获取通知”，不能假装“暂无通知”。
5. 菜单打开时主动 refetch；加载指示不能抹掉已有通知。
6. 同 key 的其他观察者共享数据，禁止再保留一个独立 setInterval。

### 10.2 ServersView：第二轮单独处理

先把“数据库服务器列表”和“主动连接/版本探测”分清：

- 普通列表 query 是只读读取；mutation/探测行为不得塞进默认会聚焦重取的 queryFn。
- 审查现有 `pageIdsKey` 自动 probe。第一版最多保留原触发条件；不能改成 `[data]`，否则每次轮询都会打上游。
- `mutateItems` 的富化字段不能直接永久写进列表并假设下一次 GET 会返回同字段。把短期 probe 结果按 server ID 单独保存/查询，并规定与持久化 DTO 的优先级。
- release catalog 以请求条件为 key，保留明确的检测失败状态与用户手动检查入口。
- 原生安装状态、升级任务、metrics 采样各自保留状态机、授权、取消和停止条件；Query 只代替请求调度，不重写业务终态。
- `verified`、`failed`、`manual_attention`、`dispatch_closed` 不得一律映射成“成功”；保留现有任务证据语义与手动校验入口。
- 关闭 dialog 停观察并取消 GET；已提交的后端升级任务不会因此被取消。
- 安装命令、token 和幂等键由原显式 mutation 流程管理，不能 focus/重连自动生成新命令。
- 迁移/重新安装影响服务端写入与身份配置，完整跑现有 installation/updates/reinstall 浏览器测试后再扩大范围。

### 10.3 剩余迁移按资源集，而非“每次删一个页面的 effect”

| 批次 | 文件/读者 | 主要风险与规则 |
|---|---|---|
| 用户关联 | `UserActivity`、`UserNodeUsage`、`UserServerUsage`、`MeView` | ID/时间范围/时区入 key；管理员和自助接口分开；一次性安全流程维持显式操作 |
| 运营查询 | `DashboardView`、`TrafficView`、`LogsView`、`SyncTasksView`、`NodeIssuesView`、`GeoAnomaliesTab` | 大查询不全局轮询；批量选择保持；隐藏 UI Tab 按活动状态启用；过滤参数完整 |
| 节点/证书/分组 | `NodesView`、`GroupsView`、`CertificatesView`、`CertEventsTab`、扫描/metrics 对话框 | capability 与角色门控；写后关联用户/节点/通知失效；不自动重放扫描/签发 |
| 配置编辑 | `SettingsView`、`SubClientsView`、`TemplatesView`、`RuleSetsView`、scope 编辑器 | dirty 草稿独立；共享 settings 必须更新所有读者，避免不同页面互相覆盖 |
| 公共资料 | `LanguagePacksView`、`stores/site.ts`、相关布局/登录资料 | 品牌副作用与服务器快照区分；i18n 启动加载保留既有启动顺序 |
| 诊断/其余 | `DiagnosticsView` 等 | 明确只读快照与执行命令边界；按结果状态驱动刷新 |

每迁移一个资源集，需要记录：生产者、所有读者、query key、请求参数、权限、新鲜度、mutation 失效、旧实现移除、测试覆盖。

迁移纪律：

- 已登记迁移的资源，新增代码统一复用其 query/mutation 模块。
- 未迁移资源允许原方式，不为了通过规则批量改写所有 effect。
- 旧读者迁移前仍会保留本地副本；PR 的验收范围明确列出，不声称它们也获得广播。
- 不用“API 模块里全局发 DOM 事件”建立第二套长期失效机制。确需临时桥接，写明调用者和删除条件。
- `site` store 可以暂时作为品牌 DOM 副作用/兼容投影；不让它与 Query 独立加载同一资源而无更新规则。
- 不以 effect 数量归零作为完成指标；以服务器数据只有一个读取/失效责任方为完成指标。

## 11. 外部变化检测、负载门槛与上线

### 11.1 第一版采用的机制

保留 Query 默认可见性/重连重取；用户列表在测量通过后开启 60 秒可见轮询；通知保留原 60 秒。其他资源按登记策略单独启用，禁止全局 `refetchInterval: 5000`。

Query 的隐藏暂停主要约束**间隔轮询**，不是保证后台标签页永远没有任何请求。显式 invalidation、手动 refetch、进行中的请求需要独立理解和测试。

浏览器内部不可见但仍挂载的 MUI Tab/Drawer 也需要 `enabled`/组件挂载边界；document 可见不等于每个业务区域都可见。

多标签页即时通知不是第一版必要项。若之后确需增加，可用受控的 BroadcastChannel 只广播“某资源可能变化”的无敏感数据事件；接收端核对 API 基址、会话与权限后使本地缓存失效。不要广播整份用户 DTO、token、密码；注意自发事件回环、合并与事件丢失。它也不能覆盖另一管理员的浏览器，后者仍需要轮询或服务端事件。

### 11.2 建议负载与行为门槛

先在测试环境确认下列条件，再在真实规模下调整阈值：

- 无操作情况下，每个可见用户列表实例每分钟最多约一次周期读取；通知另计。
- 每次用户列表周期读取不附带 Top 全体扫描、逐行上游探测或自动 POST。
- 同一批 N 个用户修改不触发 N 次整表刷新；列表/汇总在批次结尾合并失效。
- 隐藏两个周期，不产生新的周期查询；恢复时按 stale 策略校验，不补发错过的每一轮。
- 相同测试数据下只读接口 p95 不应明显劣化；可用“比基线增加不超过 20%”作为试点告警线，但最终以部署资源和数据库负载共同判断。
- 网络请求数、错误率与活跃管理会话数一同观察。不要只看 QPS，忽略单次全表扫描成本。
- 构建记录加入依赖前后的入口/相关 lazy chunk gzip 大小，确认是否影响首次登录与用户页加载；不凭库宣传给出体积结论。

### 11.3 回退方式

每个资源迁移以独立提交/PR 为单位。负载异常优先在集中 policy 关闭新增 interval，保留操作后失效和手动刷新；功能回归可回退相应资源提交。

关闭轮询是负载止血，不等于功能完整；关闭后同步调整产品验收状态。没有后端 schema 变化的 A/B 工作包不需要数据库回滚。

不必先开发一套复杂运行时灰度平台。使用小批次发布、集中策略和可回退提交即可；若项目已有 feature flag，再复用它。

## 12. 后续同步状态：两档可选方案

**此节是新增接口设计建议，不是现有 API 描述，也不作为 A/B 的前置依赖。**

### 12.1 先做轻量资源状态，若只需要回答“这个用户现在有没有同步任务”

可以新增受权限约束的资源状态接口，例如 `GET /api/admin/users/:id/sync-status`，而不是让浏览器扫所有任务。

契约至少包含：目标 ID、观察时间、活跃任务、近期相关失败/取消任务，以及查询是否成功。任务字段限制到 UI 所需的 id/type/status/attempts/next_retry_at/可展示错误，不返回带秘密的任务 payload。

必须定义：

1. **含义**是“当前可观察到的相关任务状态”，不是“本次写操作百分百已经应用到所有上游”。没有活跃任务可叫 `no_active_tasks`，不能无证据命名 `synced`。
2. 查询存储出错返回错误/unknown，不能复用当前 `HasPendingSync` 的 `false on error` 语义。
3. 明确覆盖哪些 sync task 类型；既有 helper 不包含所有迁移/原生任务，不能靠名称相似混合不同队列。
4. 对 operator 沿用用户目标权限；self-service 另用 `GET /api/user/me/sync-status`，不能让普通用户访问 admin 总队列。
5. 对用户已删除、正在删除、任务已清理定义 404/目标不存在与未知的区别；删除后的任务观察若必要，需要独立目标授权依据。
6. 实现从 ports repo 到 sqlstore 的有界目标查询；如果需要索引，依据真实查询计划与迁移测试添加。

前端：只在显式展开同步状态，或响应刚报告 pending 的相关区域进行有限轮询；例如 15 秒一次、总墙钟预算 5 分钟、隐藏暂停。预算是 UX 起始值，不代表后端任务应该在 5 分钟内完成。

预算耗尽显示“仍未确认完成，可稍后查看”；请求失败显示“状态未知”；没有活跃任务显示“当前未发现待处理任务”，不能自动弹“上游已全部同步”。停止本地轮询不取消后端工作。

建议文件范围：`internal/ports/repos.go`、实际 sync task repo、`service/user` 的状态读取方法、`handler/admin_user.go` 或独立 handler、`handler/user_me.go`、`router.go`、对应前端 API/query 和三方数据库测试。沿用现有架构，service 不导入 adapter。

### 12.2 真正需要“这次操作何时完成”，才设计 operation

需要至少补全以下关系后才能交付：

- mutation 返回稳定 operation ID 与资源/目标代次；即使没有后台任务也有明确的同步完成结果。
- operation 关联本次操作实际产生或复用的任务，不能只查 user ID。任务合并、去重、重复提交需要定义关联规则。
- 保存目标版本或 generation；后来的禁用操作覆盖先前的启用操作时，不能把旧 operation 的完成解释成“现在仍然启用”。
- 终态区分 succeeded、failed、canceled/superseded、unknown 等实际可证明状态；`done` 只表示结束，不等于成功。
- 跨面板部分成功需要明确聚合规则；任务 purge 不能立即抹去用户仍可能查询的 operation 证据。
- GET operation 有目标级授权；保留时间、进程重启、重复操作的幂等键、删除用户后的查询策略写清楚。
- 前端根据 operation 终态停止轮询；关闭 dialog、离线、页面隐藏、预算耗尽和后端任务失败是不同事件。

这是后端同步可观测性项目，需要单独估工与 ADR。不要为了给一个 toast 加自动消失动画而引入完整 operation 数据模型。

## 13. 测试与验收清单

### 13.1 测试基础

- `queryTestUtils` 每个测试创建独立 client，关闭非被测重试；unmount/clear 并恢复 focusManager、onlineManager、fake timers，避免跨测试污染。
- DOM 测试继续使用当前项目的 jsdom 文件注解；仅使用 `QueryClient` 的纯缓存测试可以保持 node 环境。
- 真正验证生产 retry 时显式使用生产策略；不能因为测试 helper 全关 retry 就宣称重试策略已覆盖。
- 相同 key 去重测试用两个同时挂载的观察者。React StrictMode 与消费 signal 的取消后重新挂载可能产生额外的取消请求，不能把“所有开发场景严格一请求”写成错误断言。

### 13.2 必须自动化的用例

| 组别 | 用例与断言 |
|---|---|
| key | 分组、关键词、排序、页码、pageSize、enabled、时区变化均改变对应 key；相同规范化参数复用 |
| 同页一致性 | 同一用户列表/安全详情同时打开，mutation 后两者更新；inactive 列表只标 stale，返回后更新 |
| 取消与乱序 | A 请求慢、切 B、B 先完成；A 晚到不覆盖 B；取消不 toast、不 retry |
| 保存回显 | 保留已有 reopen-saved-value 场景；保存前发起的旧 GET 晚到不能把刚保存的字段改回去 |
| 恢复服务 | R1/R2 active；R3 reason 清空但 access 仍受限；不在浏览器重算业务真值 |
| 读取失败 | 写成功后 GET 失败，UI 不回报写失败、不自动重写；保留旧值并显示陈旧状态 |
| 部分落库 | 多步骤编辑后半段失败、或 enqueue 失败；仍重取并反映已保存事实 |
| pending | 有 header 显示待同步；后续 GET 无 header不自动当完成；不发不存在的 operation 请求 |
| 选择 | 同页后台数据更新保持合法勾选；删除/权限变化移除无效 ID；翻页/改筛选清空；批次目标固定 |
| 草稿 | dirty 表单在 focus/refetch/invalidate 后保留；只读状态可更新；换用户重建基线 |
| 分页 | URL Back/Forward、paramPrefix、pageSize 偏好、分组切换回第一页；删除末页回有效页 |
| placeholder | 翻页旧数据暂存期间不可误执行行操作；A 用户详情不成为 B 用户的占位 |
| 用量 | 非 Top 用户不显示 0；打开编辑后 GET 精确报告；报告失败不提交用量；后台变化不自动重置计量 |
| 批量 | N 次 mutation 后列表/汇总合并失效；部分失败保留可重试目标；并发仍受限 |
| 认证 | A 退出后 B 登录无 A 数据闪现；同账号新会话/角色降级隔离；旧 refresh/GET 晚到不覆盖新会话 |
| storage | 另一标签页 logout/login、`key=null` 清理；普通 token refresh 不清空当前正常缓存 |
| retry | 网络/允许的 5xx 至多一次；401/403/404/429/取消/确定性503不循环；mutation不自动重试 |
| 通知 | 可见轮询、隐藏暂停、回来校验；错误保留旧通知；首屏失败不显示空成功 |
| 服务器后续 | 只读轮询不触发 probe/POST；升级状态停止条件、安装 token/命令、幂等键和关闭取消保持 |

### 13.3 浏览器验收

基于构建产物做实际浏览器检查：

1. 用户页编辑/解封，打开安全抽屉观察变化；保持批量选择后切走再切回。
2. 输入尚未保存的表单，另一标签页改变同用户，再切回；草稿不丢，已保存事实正确提示。
3. 两个独立认证的浏览器上下文模拟两名管理员；B 持续可见，在配置的周期内发现 A 的变化。
4. Network 记录中每轮用户列表刷新没有附带 Top 全扫描或 server probe；隐藏后轮询停止。
5. 离线、恢复、500、403、最终401；没有 toast 风暴，没有持续无限重试。
6. admin 退出后 operator 登录，旧缓存敏感字段不可见；跨页退出同样成立。
7. 自定义 `panel_path` 下 API、登录跳转与 Query scope 工作正常。
8. 正常/无数据/权限不足/错误/后台刷新五种 UI 都可区分，搜索、排序、翻页、删除末页不回归。

### 13.4 执行命令与门禁

在实际代码实施时执行，而不是声称本次文档评审已经运行过：

```bash
cd web-react
npm ci
npm run test
npm run build
npm run smoke:dist
```

若修改服务器重装流程，复用 `.github/workflows/test.yml` 当前已有的 Chromium reinstall 测试步骤。当前 CI 已有具体脚本与固定依赖，不依据陈旧说明重复搭一套。

如果需要构建后端/发布二进制，先完成前端 build，再从仓库根目录 `go build -o /tmp/psp-freshness-check ./cmd/panel`；检查 `.gitkeep` 与 TypeScript 跟踪产物差异。

工作包 A/B 只有前端变化，不要求为了形式运行所有 Go 集成测试；工作包 C 涉及 handler/service/repo 时补针对性测试、`go vet ./...` 和必要的 race/三方数据库 CI。测试门槛以当时工作流为准。

## 14. 推荐提交顺序与逐步完成条件

| 顺序 | 提交范围 | 交付与停止检查点 |
|---|---|---|
| PR 0 | 诊断与决策记录 | 完成 R1–R9 的可执行 fixture/关键复现记录；记录性能基线；说明实际症状归属；ADR 仍准确标注状态 |
| PR 1 | Query 基础、session、API options、测试 Provider、NotificationBell | 会话隔离/取消/错误/通知测试通过；用户业务页面尚未被机械改写 |
| PR 2 | `usePageState`、UsersView、关联安全详情、mutation 失效与用量修正 | 同标签页一致性、分页、批量、草稿、部分保存全部通过；operation 轮询不在此提交 |
| PR 3 | 用户列表可见轮询及真实浏览器验收 | 请求放大消除、预算通过、持续可见外部变化验收；未通过则主需求仍有明确剩余项 |
| PR 4 | ServersView 及其专用 dialog | 主动探测与只读刷新分离，保留任务/安装语义，已有服务器浏览器回归通过 |
| PR 5+ | 按 §10.3 逐资源迁移 | 每批附 key/失效/权限/策略清单；移除对应旧取数状态与冗余请求 |
| 独立后续 | §12 的同步状态增强 | 先定协议和授权，再实现；不与前端库迁移合并冒充低成本改动 |

每个 PR 描述至少写：改了哪个资源、哪些读者已经迁移、什么操作触发哪些失效、是否新增持续请求、怎么验证、还剩什么未迁移读者。

工期不要沿用原稿“26 个 view / Phase 0 1–2 天”直接推算。先完成 PR 1/2，再用实际资源复杂度与测试成本估剩余批次；服务器任务流程和配置编辑通常不能按普通列表估工。

## 15. 原计划应如何逐段修改

可按以下清单更新原文件，再把本文件作为实施细则链接进去：

| 原章节 | 必须修改的内容 |
|---|---|
| §0 一句话 | 拆成本页缓存一致性、外部变化发现、同步状态三条；删除“已有 pending 接 operation” |
| §1.1 症状 | 保留真实用户症状，注明尚未现场复现，不预先指定缓存为根因 |
| §1.2 统计 | 30 组件/17 View/26 字面量 effect 分开；记录基线 commit；改为资源与读者清单 |
| §1.3 唯一轮询 | 删除“唯一”，补升级、安装与 metrics 观察；各自标状态机与停止条件 |
| §1.4–1.5 理由 | 缩减泛化的官方背书；聚焦 PSP 的列表/详情/通知复用与取消/失效成本 |
| §1.6 默认值 | 补 stale、enabled、visibility 条件；写明 staleTime 到期不会自行触发请求 |
| §1.7 失效广播 | 改称同 QueryClient 的定向失效；增加不同 key、旧读者、跨标签页的边界 |
| §1.8 异步收敛 | 重写为“现有 header 不足以观察收敛”；引入 §12 的后续协议路线 |
| §2.1 定性 | 用 R1–R9；纠正 void refresh、恢复按钮条件、access 字段判据与部分落库 |
| §2.2 预算 | 加请求触发链、Top 扫描、上游 probe、active query 数和产品延迟目标 |
| §2.3 多管理员 | 单管理员也有后台变更；多人规模影响频率预算，不决定要不要新鲜度 |
| §3 方案 | 修正 SWR/Router 能力；SSE 作为增强维度；去掉 O(1) 和未经证实的依赖禁令 |
| §4 分阶段 | 换成 §14；第一试点含用户详情与通知，服务器另批；加入持续可见验收 |
| §5 不做 | 改为职责规则；auth session 生命周期必须处理；保留合法 effect；后端同步观察另项 |
| §6 风险 | 加会话泄露、草稿、批量选择、取消/旧响应、重复 toast、部分成功与重查询放大 |
| §7 ADR | 编号合入前复核；索引处理重复编号；不要把提案写成已接受 |
| §8 评审问题 | 收敛为外部变化可接受延迟、实际并发规模、是否需要 operation 级证据；不再把已能源码核实的事留给所有者回答 |

本次建议最关键的验收原则是：**页面显示的是后端新读取的事实，已保存与已同步分开表达；背景更新不会改变使用者正在准备提交的内容，也不会跨会话复用私有数据。**

## 16. 参考来源与适用边界

以下官方资料于本次评审核查，具体库行为仍以实施时锁定的版本和测试为准：

- [React useEffect：数据获取的替代方案](https://react.dev/reference/react/useEffect)
- [TanStack Query 默认值](https://tanstack.com/query/latest/docs/framework/react/guides/important-defaults)
- [TanStack Query 可见性恢复重取](https://tanstack.com/query/latest/docs/framework/react/guides/window-focus-refetching)
- [TanStack Query 定向失效](https://tanstack.com/query/latest/docs/framework/react/guides/query-invalidation)
- [TanStack Query disabled 查询](https://tanstack.com/query/latest/docs/framework/react/guides/disabling-queries)
- [TanStack Query 分页与 placeholder](https://tanstack.com/query/latest/docs/framework/react/guides/paginated-queries)
- [TanStack Query 跨标签页实验插件](https://tanstack.com/query/latest/docs/framework/react/plugins/broadcastQueryClient)
- [SWR mutation 与多 key 过滤](https://swr.vercel.app/docs/mutation)
- [React Router useRevalidator](https://reactrouter.com/api/hooks/useRevalidator)
- [React Router action](https://reactrouter.com/start/data/actions)
- [React Router 竞态处理](https://reactrouter.com/explanation/race-conditions)
- [Google AIP-151](https://google.aip.dev/151)

源码结论优先于旧文档或源文件里的历史注释；文中标出的行号只对应上述评审基线，实施时应通过函数名再次定位。
