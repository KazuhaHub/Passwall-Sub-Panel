# Changelog

Format inspired by [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
semver per `feedback_semver` (major = refactor, minor = feature, patch = fix +
small improvement).

## v3.6.1-beta.1 — 2026-05-26

### Added — 全部 admin 列表统一分页 + column-click 排序 + 关键字搜索

之前 admin 端的列表分散三套实现:Users/Audit/SubLogs/SyncTasks/EmailLogs 已经
后端分页;Groups/Nodes/Servers/RuleSets/Templates 后端一把返回前端 useMemo
filter;LogsView 自己长了一坨 Pagination + 手写 page 状态。本 beta 把所有
list endpoint 拉到同一根分页骨架上,UI 上每个表底部都有相同的 "1-25 of 3,421
< >" + 每页选择器(10/25/50/100),分页 / 关键字 / 排序状态写到 querystring,
admin 刷新或分享链接保持视图。每页大小存 localStorage(`psp_page_size`)在所有
列表间共享。

### Backend

- **`ports.Pagination`**:在原 Page/PageSize 基础上加 `Keyword` /
  `SortBy` / `SortDir`。每个 repo 维护自己的 sortAllowlist —— admin 传过来
  的 `sort_by` 必须命中允许列表才会落到 ORDER BY,否则 fallback 到 default
  排序,防 SQL 注入。
- **6 个之前没分页的 repo 增加 `ListPaged`**:Group / Node / Separator /
  RuleSet / Template / XUIPanel。原 `List(ctx)` 保留给内部 caller(reconcile /
  traffic poll / render 等需要全集的路径),`ListPaged` 仅供 admin API 用。
  mysql repos 共用 `applyPagination(q, p, allowlist, default)` 工具;yaml
  repos(rulesets / templates)走 in-memory `slicePage / sortBy / keywordMatch`,
  反正每个 yaml repo 文件个数 <10。
- **现有 4 个已分页 repo(User / Audit / SubLog / SyncTask + MailSent)** 接入
  `applyPagination`,同时支持 `SortBy`。SubLog / EmailLog 因为带 JOIN users,
  sort allowlist 用 `sub_logs.` / `mail_sent.` 前缀避免歧义。
- **handler 通用工具** `internal/transport/http/handler/pagination.go`:
  - `parsePagination(c)` 一把读 `?page=&page_size=&keyword=&sort_by=&sort_dir=`,
    clamp page>=1 / size 在 [1, 200],默认 size=25
  - `pagedEnvelope(items, total, p)` 统一返回 `{items, total, page, page_size}`
  - 所有 10 个 list handler 都改成这两个工具,保留旧 `?search=` 参数作为
    `keyword` 的 alias,老前端 URL bookmark 不破

### Frontend

- **新 hook `usePaged<T>(fetcher, opts?)`**(`src/hooks/usePaged.ts`):
  - 管 page / pageSize / keyword / sortBy / sortDir 状态
  - URL sync(`?page=&q=&sort=col-dir`),默认值省略保持 URL 简短
  - localStorage 持久化 page_size(全局 key `psp_page_size`)
  - AbortController 取消旧请求 —— admin 快速搜索时较老的慢响应不会
    覆盖较新的快响应
  - 暴露 `refresh()`(post-mutation reload)和 `mutateItems()`(无网
    络往返地 patch 已加载的某行,Servers 版本探针就是用这个)
- **新组件 `<PagedTableFooter />`**(`src/components/PagedTableFooter.tsx`):
  包 MUI `TablePagination` —— "1–25 of N" + 每页选择器 + 首页/末页按钮,
  样式跟所有 view 一致
- **新组件 `<SortableTableCell column activeColumn activeDir onSort />`**:
  包 MUI `TableSortLabel`,把表头变成可点击切换 asc/desc 的目标。usePaged
  的 `setSort(col, initialDir?)` 自动处理"同一列再点切方向、新列从默认
  方向开始"
- **每个 view 一致的迁移模式**:
  - **UsersView**(pilot)/ **ServersView**:走 `usePaged` 全套,
    column-click 排序覆盖大部分有意义的列
  - **NodesView**:走客户端切片分页(全集仍一次拉,因为拖拽重排序需要
    跨页计算位置)。表底 footer + page-size 选择器
  - **GroupsView / RuleSetsView / TemplatesView**:小列表,走客户端切片
    分页(后端已经支持 ListPaged 但前端默认请求 page_size=200,实际跑客户
    端切片以减少代码体积)
  - **LogsView**(sub / audit / email 三个 tab)/ **SyncTasksView**:
    每个 tab 自己维护 page + pageSize 状态,共用 `psp_page_size` localStorage
    key,统一用 PagedTableFooter
- **共用 i18n**:`common.json` 新增 `pagination.rows_per_page` /
  `pagination.range` —— 两个 locale 都加

### Notes

- API envelope `{items, total}` → `{items, total, page, page_size}`,新增字段
  老前端读 `items + total` 仍可用 —— **非破坏性**
- 旧 `?search=` 参数保留作为 `?keyword=` 的别名,handler 端 coalesce
- backend 的 sort 允许列表是硬编码白名单,不允许任意列 ORDER BY
- 分页节奏:list endpoint **默认 page_size=25**(单次 ~1KB JSON,网络压力可
  忽略);PageSize 0(或 <0)在 repo 层意味着"返回全部" —— 内部 caller(traffic
  poll / reconcile / render)走这条;admin API handler 永远 clamp 到正整数,
  禁止从 HTTP 触达"返回全部"

## v3.6.0-beta.11 — 2026-05-26

### Fixed

- **ServersView 升级对话框 fallback 文案残留 "PSP" + 历史事件引用**:beta.8 整理
  i18n 时只更新了 zh-CN / en-US JSON,源码里 `t(..., { defaultValue: ... })` 的
  fallback 字符串没改。一旦 i18n key 因为重构掉了或者 build 不齐,admin 会看到:
  - "PSP 将先检查目标版本是否在已测试范围内,在范围内才会触发 ... 自升级。面板会
    重启,约 60 秒后 PSP 跑 smoke probe 验证。"
  - "目标版本 X 状态为 Y(PSP 当前测试最高 Z)。强制升级可能因 schema 变更导致
    **PSP traffic poll 失败 —— PSP v3.5.1 修复的 v3.1.0 break 就是这类问题**。"
  - "已发起 3X-UI 升级到 X,约 60 秒后 PSP 跑 smoke probe ..."

  beta.8 commit 描述说"PSP → Passwall Panel"已经做了,但其实只做了一半。本 beta
  把这三处源码 defaultValue 同步成 i18n JSON 里的正式版文案(全角括号 + 全称 +
  去掉历史 reference)。

### Audit (no-op)

- 跑 `go vet ./...` + `go test ./...` + `tsc --noEmit` 全过
- 静态 grep "PSP / v3.5.1 / v3.1.0 break" 用户可见路径,只剩源码注释里的 reference
  (不会暴露给 admin)
- 检查 dropped fields (`xui_panels.latest_xui_version` / `update_available`) 的
  orphan 引用:无残留,DB 列照 self-use no-migration 原则保留为 dead weight
- 检查 `LatestXUIRefreshAt` / `LatestXUIRefreshError` 是 exported 但未使用的
  accessor,跟 `compat_remote.go` 里 `LastRefreshAt` / `LastRefreshError` 同样
  pattern。保留作为未来 admin 状态页面的钩子,不视为 dead code 删除

## v3.6.0-beta.10 — 2026-05-26

### Fixed

- **CompatUnknown 出现在新装/刚升级后第一次打开 Servers 页**:根因有两层 ——
  1. boot probe 漏调 `RefreshRemoteCompat`(beta.9 加 `RefreshLatestXUI` 时只补
     了一半)。结果 boot 完成时 compat JSON 还没拉到,UI 上每台 panel 都显示
     `Unknown` + 提示 "compat data not loaded yet"直到 admin 手动 Test。修复:
     `probePanelVersionsOnce` 进 panel 循环前 RefreshRemoteCompat / RefreshLatestXUI
     两个一起跑(各自 10s 超时,失败不阻塞)。
  2. `LoadCompatCache` 严格要求缓存文件的 `psp_version` 跟当前一致,否则整个
     丢弃 → 每次升一个 beta 都会让 cache 失效,boot 网络不通时 admin 看到的就是
     `Unknown`。修复:放宽该检查,旧 PSP 版本写的 cache 当 stale-but-usable 启动
     值用(boot 的 RefreshRemoteCompat 会在几秒内覆盖)。最坏情况是 admin 看到
     几秒钟旧的 compat 范围,而不是空白。

### Added

- **Servers 页批量选中后,工具栏新增「批量升级 Xray」按钮**:固定升 latest,不
  允许指定版本。理由:不同 panel 的 3X-UI 看到的可装 xray-core 版本列表可能不
  一致(取决于 3X-UI 自己的 GitHub 缓存),"全部锁同一个 tag"未必每台都能满足。
  共同的高频诉求是"把所有节点的 xray-core 都升新",这条单一语义直接做。Admin
  真要 pin 具体 tag 还是走单台的"升级 Xray"对话框。
  - 行为:`Promise.allSettled` 并行 fanout 到选中的 N 台 → 每台 `installXray("latest")`
    → 完成后并行 probe 拉回新版本号刷 UI → 整体结果 toast(全部成功 / 部分失败,
    带计数)
  - 风险:Xray 重启会让该节点的代理流量短暂断开(秒级),面板自身不重启;批量同时
    打 N 台等于让 N 台节点同时短暂不可用。admin 自行选时间窗

### Internal

- **触发时机最终汇总**(beta.10 之后,跟 README/docs 对齐):
  - PSP 直查 GitHub 的两类(compat JSON + release-latest):**boot probe 开始 +
    Servers 页 Test 点击**,两个时机各自做(节流自带去重,N 台 panel 同时打也只
    一次外网)
  - 不存在 background tick —— admin 不动手就没必要去问 GitHub 有没有新东西
- `internal/transport/http/handler/admin_servers.go` 没改;批量逻辑全在前端用现成
  的 `upgradeXray(id)` 单台 API fanout 完成,backend 无需新增 endpoint。

## v3.6.0-beta.9 — 2026-05-25

### Changed — "latest 3X-UI 版本" 检测改成 Passwall Panel 自己一次性查

beta.8 的"⋮ 红点 badge"用的是 N-fanout 模型:每台 panel 各自调 `GetPanelUpdateInfo`,
3X-UI 内部各自查一次 GitHub。本 beta 改成 Passwall Panel 直接一次性查 GitHub release-
latest,所有 panel 共用同一份结果。理由:

- "最新 3X-UI tag 是什么"是**全局事实**,跟具体 panel 无关 → 一次查询足以驱动所有
  panel 的 badge
- N-fanout 把 GitHub 查询成本均摊到每台 panel,扩到几十台机房就开始没意义,且各
  panel 缓存周期不同步,UI 上同一时刻不同 row 的 badge 状态可能矛盾
- 真正触发升级时(admin 点"升级 3X-UI 面板"),仍走 3X-UI 自己的 `/getPanelUpdateInfo`
  做权威 pre-check —— 升级目标必须由 3X-UI 自己定(`/updatePanel` endpoint 也是
  3X-UI 自己从 GitHub 拉,Passwall Panel 没有 inject 能力)

### Added

- **`internal/version/latest_xui.go`**:`RefreshLatestXUI(ctx)` 直查
  `https://api.github.com/repos/MHSanaei/3x-ui/releases/latest`,30 分钟节流 +
  single-flight,确保 N 台 panel 的 Test 风暴只会触发一次 GitHub 查询。`LatestXUI()`
  accessor 返回当前缓存 tag;`IsXUIUpdateAvailable(panelVersion)` 做 semver 比较。
- **本地缓存 `<DataDir>/latest-xui-cache.json`**:跟 compat-cache.json 同目录,boot
  时 `LoadLatestXUICache()` 先恢复上次拿到的 tag,冷启动无网络时 badge 仍能渲染。
  原子 temp+rename 写入,避免并发读到半成品。

### Changed

- **Boot probe 改一次性触发**:`probePanelVersionsOnce` 进入 panel 列表前先调
  `RefreshLatestXUI(ctx)` 一次,然后循环里只关注 `GetServerStatus` 探当前版本 —
  去掉了 per-panel 的 `GetPanelUpdateInfo` 调用。
- **Test handler 同样一次性触发**:`Test()` 在 `RefreshRemoteCompat` 之后追加一次
  `RefreshLatestXUI`,返回的 `latest_xui_version` / `update_available` 改成现场
  derive(`version.LatestXUI()` vs `status.PanelVersion`),不再从 DB 读 per-panel 列。
- **`toServerDTO` 现场 derive badge 状态**:`update_available` 不再从 DB 拿,改成
  `version.IsXUIUpdateAvailable(p.PanelVersion)`;`latest_xui_version` 同样从
  `version.LatestXUI()` 拿。一份全局 snapshot 驱动所有 row,绝不会出现"一半 row 显示
  badge 一半不显示"的不一致状态。
- **Xray 升级文案去掉"最新"后缀** —— 既然下拉里可以选具体版本,菜单项
  `升级 Xray（最新）` 改成 `升级 Xray`;对话框标题 `升级 Xray（最新版）` 改成
  `升级 Xray`;对话框正文重写为"安装 xray-core。可在下方选择目标版本(默认最新)"。
  3X-UI 升级那条保留"（最新）"/`(Latest)`,因为 `/updatePanel` 真没有版本参数。

### Fixed

- **EN 模式下 Xray 对话框 `目标版本` / `latest（最新版）` 没有翻译**:根因不是缺
  翻译,是 i18n key 被错位放进了 `placeholder` 块,但 [ServersView.tsx:921](web-react/src/views/admin/ServersView.tsx#L921)
  读的是 `field.xray_version` → 找不到回退到 defaultValue 的中文。en-US.json 和
  zh-CN.json 都把 `xray_version` / `xray_version_latest` / `xray_version_loading`
  三个 key 从 `placeholder` 移回 `field`。中文模式下因为 defaultValue 恰好也是中文,
  bug 视觉上看不出来。

### Removed

- **`xui_panels.latest_xui_version` / `update_available` 两列退役**:DB 里残留
  不动(自用项目无迁移原则),domain.XUIPanel 删掉对应字段,GORM row + repo 接口
  `UpdateLatestXUIVersion` 一并删。新代码完全不再读写这两列。
- **`client.GetPanelUpdateInfo` 在 boot probe + Test handler 中的引用退役**(仅在
  `UpgradePanel` 升级 pre-check 路径保留 —— 那里需要 3X-UI 给出权威升级目标版本)。

### Internal

- **触发时机汇总**(beta.9 之后):
  - PSP 直查 GitHub release-latest:**boot probe 开始 + Servers 页 Test 点击**
    (两个时机都走 `RefreshLatestXUI`,30 分钟节流 + single-flight)
  - PSP 查 GitHub raw compat JSON:**Servers 页 Test 点击**(`RefreshRemoteCompat`,
    60 秒节流)
  - PSP 探每台 3X-UI 当前版本(`GetServerStatus`):**boot 一次 + admin Test 时**
  - 3X-UI 查 GitHub(`/getPanelUpdateInfo`):**admin 点"升级 3X-UI 面板"时**
    (一次,做 pre-flight check)
  - 3X-UI 列 xray 版本(`/getXrayVersion`):**admin 打开"升级 Xray"对话框时**
    (lazy load)
- traffic poll loop 每 5 分钟那一圈对 3X-UI **只调** `/inbounds/list` +
  `/clientTraffics/:email`(查流量),**不**碰任何 GitHub / 版本类 endpoint。

## v3.6.0-beta.8 — 2026-05-25

### Added

- **⋮ 按钮新增"有新版本可用"红点 badge** ── 3X-UI panel 自己已经在查 GitHub 看是否
  有新版,Passwall Panel 现在通过 `GetPanelUpdateInfo` adapter 顺手把这个信息
  存进 `xui_panels.latest_xui_version` + `update_available` 两列,前端 ⋮ 按钮
  外层用 MUI `Badge variant="dot"` 渲染红点,hover tooltip 显示"3X-UI 新版本
  vX.Y.Z 可用（当前 vA.B.C）"。admin 一眼看到哪台 panel 该升级了,不用进
  3X-UI 自查。
- **Xray 升级支持指定版本（下拉选择）** ── 之前升 Xray 固定 "latest";现在
  Servers 页 ⋮ 选"升级 Xray"弹专用 Dialog,内含版本下拉:
  - 打开时 lazy 拉 `GET /admin/servers/:id/xray-versions` (新增 backend route,
    封装 3X-UI 的 `/server/getXrayVersion`),列出 panel 已知的可装版本
  - 下拉永远包含 "latest（最新版）" pseudo-option,即使版本列表 fetch 失败也能
    照常升 latest (graceful degradation)
  - admin 可 pin 具体 tag(`v25.10.31` 等)避免被动跟最新
- **顶部 compat banner 按 kind 拆分** ── 原单个 banner 现在拆成两个独立段落,
  视觉 severity 也分级:
  - **`too_old`(red banner)**: panel 跑的 3X-UI 低于 Passwall Panel 最低要求
    (违反 protocol floor),admin 必须升级 panel
  - **`untested`(amber banner)**: panel 跑的 3X-UI 超出 Passwall Panel 已测试
    范围,可能正常可能 silently 失败,建议升级 Passwall Panel 或仓库提 issue
    报告该 3X-UI 版本待验证
  - `unknown` 状态(从未探测 / probe 失败 transient) 仍排除在 banner 外避免噪音

### Changed

- **i18n 全面清理"PSP"缩写 → "Passwall Panel"全称**:11 处现有 i18n key
  (servers / nodes 两个段落)+ 本 beta 新加 keys 一并统一。理由:对 user-facing
  文案,缩写 admin 不一定立即识别,全称更专业 + 跟 README / docs 字面一致。
- **中文 i18n 用全角括号 `（）`**:之前 `升级 Xray (最新)` 这种半角括号在中文
  段落里不规范,统一改成 `升级 Xray（最新）` 系列。`{{count}}` 等 i18next 占位
  符的 `{{}}` 不动(模板语法)。
- **menu item 两个升级动作命名对称**:之前"升级 3X-UI 面板"+"升级 Xray（最新）"
  不对称(panel 那条没标"最新"),实际两者底层都是升 latest(3X-UI `/updatePanel`
  无版本参数,Xray 默认 latest)。改成两个都加"（最新）":"升级 3X-UI 面板（最新）"
  + "升级 Xray（最新）"。
- **升级 force-confirm 文案重写为正式版**:去掉"参见 PSP v3.5.1 修复 v3.1.0
  兼容性事件"这种历史 reference(admin 不需要懂这段历史),改成事实导向:
  *"即将升级到 X。当前 Passwall Panel 已验证的最高 3X-UI 版本为 Y。强制升级
  可能因协议或字段变更导致 traffic poll、reconcile 等关键流程失败。建议先升级
  Passwall Panel 至支持该 3X-UI 版本的发行,再升级面板。"*

### Internal

- **触发时机决策**: `GetPanelUpdateInfo` 在 boot probe + admin Test 两个时机
  piggyback,**不**在 traffic loop 每 5min 跑(避免 3X-UI 内部每 5min hit
  一次 GitHub API)。boot 给一个 baseline;admin 主动 Test 时拿"现在最新"。
  最久数据滞后 = admin 上次打开 Servers 页之后的时间窗。
- Schema: `xui_panels.latest_xui_version` (size:32) + `update_available` (bool)
  两列;`UpdateLatestXUIVersion` repo 方法 column-scoped,跟 `UpdateVersion`
  写入字段完全 disjoint,概念上两个 writer 不会互相覆盖。
- Adapter: `XUIClient.GetXrayVersionList(ctx) []string` 新方法;
  `serverDTO` 加 `latest_xui_version` + `update_available`。
- Frontend: `MoreVertIcon` 用 MUI `Badge` 包裹 + Tooltip,`overlap="circular"`
  让红点贴在 IconButton 圆形角落而不是方形外延。Upgrade Xray 改用专用 Dialog
  组件(MUI Select 下拉,FormControl + InputLabel + MenuItem),取代之前的
  `confirm()`。banner 拆分用 `useMemo` 各算一组 panels,渲染两个独立 Box。
- 图标 `UpgradeIcon` → `SystemUpdateIcon` (Material `SystemUpdateAlt`),
  方块带下箭头,跟手机系统升级图标视觉一致,比之前的 ↑ 加号样更直观。

### Migration

- AutoMigrate 自动加 `xui_panels.latest_xui_version` + `update_available` 两列,
  跨方言安全(SQLite / MySQL / PostgreSQL)。
- 首次启动后 admin 打开 Servers 页触发 testServer batch → 每个 panel 走 Test
  handler → 顺手调 GetPanelUpdateInfo → badge 立即生效。无需手动操作。

## v3.6.0-beta.7 — 2026-05-25

### Changed (dynamic compat schema 升级到 v2 ── per-major 分文件 + 范围表达)

针对 v3.6.0-beta.5 的 dynamic compat 设计,采纳两条 maintenance-friendly 改进:

- **`docs/compat/xui-compat.json` 拆分成 per-major 文件** ── 旧的单文件随 PSP 版本
  线性增长(每个 minor 加一行,迟早 50+ 行);新设计每个 PSP major 一个独立 JSON
  文件(`docs/compat/v3.json` 服务所有 v3.x 部署,未来 `v4.json` 服务 v4.x,以此类推)。
  - **每文件自然封顶**:一个 major 内 minor 数量有上限(~10),文件永远不会膨胀
  - **maintainer 心智轻**:只 active 一个文件,其它 major 文件物理不动 / frozen
  - **大版本切换零混淆**:发 v4 时只新建 `v4.json`,`v3.json` 自动停留在 v3 收尾状态;
    跑 v3 的部署仍正常拉自己那个文件
  - **prune 自动化**:老 major 不再 EOL 后整个文件可以从仓库删,跑老 major 的部署
    fetch 失败 → CompatUnknown → admin force override 仍可用(graceful degradation)
  - PSP 启动时:从 `version.Version` 抽 major(`v3.6.0-beta.7` → `3`)→ 拼 URL
    `https://raw.githubusercontent.com/.../docs/compat/v3.json` → fetch
  - 自校验:JSON 内 `major` 字段必须等于 PSP 自身 major,防 admin 不小心 push
    错文件到错路径

- **range 用 `psp_min` / `psp_max` 两个独立字段表达,支持 patch 级精度** ── 旧的
  map key `"v3.6"` 隐式代表 "v3.6.x 全系列",语义靠 doc 解释;新设计 entries 数组,
  每条 entry 用 `psp_min: "v3.6.0"` + `psp_max: "v3.6.99"` 显式两端点,支持
  patch 级 / 跨 minor 区间(例:`v3.5.9-v3.6.1` 一个范围)。
  - **JSON-schema 友好**:每个字段一个值,可以 JSON Schema 校验,无 string parser
  - **无 `-beta.x` 歧义**:不需要切 `"v3.5.1-beta.5-v3.5.5"` 这种字符串(端点
    规约只写 stable semver `vX.Y.Z`)
  - **first-match-wins** 匹配语义:`entries` 数组顺序就是优先级,admin 把
    narrower / 更新的 entry 放前面;broader baseline 兜底
  - **闭区间**:`[psp_min, psp_max]` 含两端
  - **PSP pre-release 归一化**:`v3.6.0-beta.7` 比对时丢 `-beta.7` 当 `v3.6.0`,
    符合 admin 心智("beta 算属于那个 minor")

- **新 JSON schema (v2)**:
  ```json
  {
    "schema_version": 2,
    "major": 3,
    "updated_at": "2026-05-25",
    "entries": [
      { "psp_min": "v3.6.0", "psp_max": "v3.6.99",
        "min_xui": "3.1.0", "max_tested_xui": "3.1.0", "notes": "..." }
    ]
  }
  ```
  跟 v1 不兼容,但反正只发了 beta.5 / beta.6,JSON 在仓库还没被实际用上,无迁移成本
  ── 直接切。

### Internal

- `compat_remote.go` `remoteCompatPayload` map → slice;新 `pspMajor()` helper
  抽 PSP major;新 `defaultURLForCurrentVersion()` 拼 per-major URL;
  `lookupForPSPVersion` 改成遍历 entries + cmpSemver 区间比对(first-match-wins)。
  约 60 行净改动。
- `compat_test.go` `TestLookupForPSPVersion_RangeMatchAndFirstWins` 覆盖 11 条 case:
  narrower 在前胜出 + 跨 minor 区间 + 上下界包含 + range 之间空隙 + pre-release
  归一化 + dev/garbage edge。
- `docs/3xui-compat.md` 加 "维护 SOP" 段落:何时改 / 改什么 + entries 数组语义 +
  PSP 拉不到时的故障容错说明。

### Migration

- 删除 `docs/compat/xui-compat.json`,新建 `docs/compat/v3.json`
- maintainer 以后改 v3.x 兼容数据只编辑 `v3.json` 的 `entries` 数组
- 未来发 v4 时新建 `v4.json` 即可,无需再动 `v3.json`

## v3.6.0-beta.6 — 2026-05-25

### Fixed (v3.6 系列代码审计发现的 5 个 bug + 1 个 perf 优化)

- **#1 data loss: probe 失败时清空已存版本号导致 UI 误显"从未探测"** ──
  `app.go probePanelVersionsOnce` 在 GetServerStatus 失败时调
  `UpdateVersion(panelID, "", "", &now)`,把 panel_version / xray_version 写空字符串。
  panel 之前探到的 `3.1.0` 因为一次 30 秒网络抖动就被擦掉,admin UI 显示 `—`
  直到下一轮成功探测才恢复。**修复**:`ports.XUIPanelRepo` 新增 `UpdateVersionCheckedAt(panelID, t)`
  方法,只 column-scoped 写 `version_checked_at` 一列,保留 panel_version / xray_version
  原值;probe 失败路径(app.go boot probe + traffic poll piggyback + Test handler
  失败分支)三处都改用新方法。UI 现在能正确显示"3.1.0 (上次探测 12 分钟前)"
  而不是丢数据。

- **#2 RefreshRemoteCompat 失败也 advance lastAt → 60s 锁死重试** ──
  `compat_remote.go RefreshRemoteCompat` 之前在 throttle 窗口决策**之前**就写
  `refreshLastAt = time.Now()`,意图是"防 N 个并发 hammer GitHub";结果 fetch 失败
  也算 advance,接下来 60 秒内 admin 任何 Test 点击都被 throttle 短路。**修复**:
  分离两个机制 — `refreshInflight bool` 做 single-flight(防 N 并发同时 fire),
  `refreshLastAt` 只在**成功**时 advance(失败立即可重试)。N 并发现在只触发 1 次
  fetch,失败后下一次 Test 点击立即 retry。

- **#3 setUpgrading 时机晚 → 双击 ⋮ 弹两个 confirm dialog** ──
  ServersView `runUpgradePanel` / `runUpgradeXray` 之前在 `await confirm()` **之后**
  才 `setUpgrading(s.id)`,期间 ⋮ 按钮的 `disabled={upgrading === s.id}` 还是 false。
  admin 快速双击 → 两个 confirm dialog 堆叠 → 都点确认 → 两次 POST upgrade。
  **修复**:`setUpgrading(s.id)` 移到函数最开头,`await confirm()` 在 try 块内
  (cancel 时 finally 自动 setUpgrading(null))。两个 handler 同时修。

- **#4 Test handler 用 c.Request.Context() 调 RefreshRemoteCompat → admin
  导航走会取消 GitHub fetch + 配合 #2 把 throttle 锁死** ── admin 打开 Servers
  页 → 触发 testServer → 不耐烦切到别的 tab → 浏览器 cancel 请求 → ctx cancel →
  RefreshRemoteCompat 失败 → 加上 #2 60s 锁死。**修复**:改用 `context.Background()`,
  compat_remote.go 内部已有 8s timeout,不会泄露。#2 + #4 联手解决"compat 一直
  unknown"这个最难 debug 的状态。

- **#5 smoke probe ctx.Done 时不写"被中止"audit → upgrade inflight 时 PSP
  关闭后 audit trail 不完整** ── `runPostUpgradeSmoke` 在 PSP shutdown 时三个 ctx.Done
  路径(initial grace / retry loop entry / retry sleep)全部直接 return,audit log
  只剩 `panel_upgrade_initiated` 没有收尾行。admin 之后看 audit 以为升级还在进行。
  **修复**:三个路径各加一行 `panel_upgrade_aborted` audit,actor=`upgrade-smoke`,
  detail 写明在哪个阶段被中止 + 提示 admin 手动验证。audit context 用
  `context.Background()` 因为传入 ctx 已 cancel(就是这个 ctx 触发了 abort)。

### Performance / Robustness

- **Phase B: local compat cache 文件 → PSP 冷启动 + 离线立即有可用 compat 数据** ──
  之前 PSP 启动后到 admin 第一次打开 Servers 页期间, ActiveMaxTestedXUI = "",
  所有 panel 显示 Unknown。如果 GitHub 不可达 / 启动时机网络还没起,这段时间
  可能很长。**新增** `internal/version/compat_cache.go`:
  - PSP 启动时 `app.Build` 调 `LoadCompatCache()` 读 `<DataDir>/compat-cache.json`
    把上次成功 fetch 的 max_tested_xui 装回 active state
  - `RefreshRemoteCompat` 成功后调 `saveCompatCache()` 原子写(temp + rename
    防 corrupt)持久化到同一文件
  - PSP 版本不匹配(cache 是 v3.6.x 的, PSP 现在是 v3.7.x)→ 忽略 cache,等
    第一次 fetch 替换 — 防跨 major MinXUI 改变引入的不一致
  - 失败完全 best-effort:cache 读失败 / 写失败仅 log.Warn,不阻断启动流程

### Internal

- `XUIPanelRepo.UpdateVersionCheckedAt` 是新接口方法,fakes(traffic_test.go /
  user_test.go)不需要补 stub(没 traffic 测试直接持有 XUIPanelRepo)。
- compat_remote.go 注释更新解释 single-flight + throttle 分离的设计;
  compat_cache.go 注释解释 PSP-version-mismatch 跳过策略的理由。

## v3.6.0-beta.5 — 2026-05-25

### Added (dynamic compat ── Phase 2 第五刀,bug 防御的最后一公里)

- **3X-UI 兼容范围改为 GitHub raw 远程下发**:之前 `MaxTestedXUI` 是 hardcode
  在 `internal/version/compat.go` 的常量,意味着 3X-UI 出 patch 版本(API 不变)
  PSP 也得发新版才能放宽兼容范围 —— 没意义。改为:
  - **删除** `MaxTestedXUI` const(完全 dynamic)
  - **保留** `MinXUI` const(代码级硬要求:PSP 调的端点在 3.1.0 以下不存在,不是
    "fallback" 是 protocol floor)
  - 新增 [`docs/compat/xui-compat.json`](docs/compat/xui-compat.json) 作为 single
    source of truth,key 按 PSP major.minor 分行(`"v3.6": {min_xui, max_tested_xui,
    notes}`,future-proof additive schema_version=1)
  - PSP 通过 `version.RefreshRemoteCompat(ctx, url)` 拉 raw URL,从 JSON 找匹配自己
    major.minor 的行,通过 `SetActiveMaxTestedXUI` 写入 atomic.Value
  - `version.CheckXUI` / `CompatMessage` 改读 `ActiveMinXUI() / ActiveMaxTestedXUI()`
    函数访问器,运行时立即生效
  - 默认 URL `https://raw.githubusercontent.com/KazuhaHub/passwall-sub-panel/main/docs/compat/xui-compat.json`
- **触发策略 ── 按需 + lazy,零后台 ticker**(按用户定调):
  - **不**在 boot 时 refresh,**不**piggyback traffic poll
  - admin 打开 Servers 页时,前端 batch 发 N 个 `testServer` 调用,后端
    `AdminServersHandler.Test` **入口**先调 `RefreshRemoteCompat`(8s HTTP timeout)
  - 内置 **60s throttle** 防 N 个并发 testServer 触发 N 次 GitHub fetch(N → 1 次)
  - admin 手动点单个 "测试连接" 同样路径
  - PSP 启动后 admin 不去 Servers 页则永远不刷 compat(其它路径不依赖 compat 数据,
    没影响)
  - 失败静默 fallback 到"未加载",CheckXUI 对所有 panel 返回 Unknown,
    `CompatMessage` 给出"open Servers / click Test to refresh"提示
- **admin 手动 override(force flag)** ── compat 数据未加载/目标超 tested 范围
  也允许 admin 强行升级:
  - `POST /api/admin/servers/:id/upgrade-panel` body 接受 `{force?: bool}`(缺省
    false = 旧行为,pre-check + 拒绝)
  - 拒绝响应 409 body 加 `can_force: true` 字段提示"二次确认可强制"
  - 强制路径走 audit action `panel_upgrade_forced`(区别于普通 `panel_upgrade_initiated`),
    `after_json` 写明 "compat=<status> (out of active tested range), admin overrode the gate"

### Frontend

- `upgradePanel(id, {force?})` API client 接 force 参数;`UpgradePanelResult` 加
  `compat_status` / `can_force` 字段
- ServersView `runUpgradePanel(s, force=false)`:
  1. 第一次走普通 confirm + POST(无 force)
  2. 收到 409 + `reason: "untested_target"` + `can_force: true` → 弹**第二个**
     confirm modal(destructive 风格红色按钮),消息明确"强制升级可能因 schema 变更
     导致 PSP traffic poll 失败 —— PSP v3.5.1 修复的 v3.1.0 break 就是这类问题",
     confirm 后递归调用自身 `force=true` 重发
  3. admin 也可拒绝 → 无操作返回
- i18n 同步 zh-CN / en-US:`servers.action.force_upgrade` + `servers.confirm.upgrade_force_{title,
  message}` + `servers.compat.not_loaded`

### Internal / 测试

- 14 个新 unit test(compat_test.go 全重写):覆盖 `parseSemver` 4 条 + `CheckXUI`
  6 条(含 "no remote loaded → Unknown" / "override widens range" / "boundary
  exact match" 三条新关键路径)+ `ActiveMinXUI` 不变性 + `CompatMessage` 2 条
  (含 "no remote loaded" vs "garbage input" 的 Unknown message 区分)+
  `lookupForPSPVersion` 1 条(PSP version 提取 + JSON lookup,含 dev/garbage edge)
- `resetCompatForTest(t)` helper 让每个 test case 从干净 atomic state 起,避免
  override leak

### Why

PSP v3.5.1 那次 3X-UI 3.1.0 schema break 暴露的问题: hardcoded 兼容范围只能靠发
PSP 新版来同步,反应迟钝。这次彻底拆开:
- **协议下界**(MinXUI)留 const ── 这是 code-level 真实要求,改它要改调用代码
- **测试上界**(MaxTested)走 GitHub JSON ── 仓库 maintainer 验过新 3X-UI 后改一行
  JSON push 到 main,所有 PSP 部署 60s 后(打开 Servers 时)自动放宽,**零 PSP 发版**
- **admin 仍能 override** ── 即使远程 JSON 没拉到 / 目标版本未测试,二次确认即可
  强制升级,operator 真的知道自己在做什么时不被门槛挡住

## v3.6.0-beta.4 — 2026-05-25

### Added (`lastOnline` 集成 ── Phase 2 第四刀,免费红利落地)

- **admin 用户列表新增"最近活跃"列**:每个用户基于跨所有 owned 3X-UI client 的
  `max(clientStats.lastOnline)` 显示相对时间(`刚刚` / `5 分钟前` / `2 小时前` /
  `3 天前`),悬停 tooltip 显示绝对时间戳;超 30 天则显示 `YYYY-MM-DD` 防止
  "9999天前" 这种没意义的标签;**永未活跃**或对接的全是 3X-UI < 3.1.0 panel
  (没这个字段)的用户显示 `—`(static muted dash,不刷屏)。
- **traffic poll 顺手聚合**:在 Phase 2 处理 ClientStats 时,对每个 user 取
  `max(t.LastOnline)` 进 sink,end-of-cycle 通过新 `BatchUpdateLastOnline`
  一次 transaction 写完;**零额外网络/3X-UI 调用**(完全 piggyback 已有的
  clientStats fetch)。每个 panel 在线探测对 PSP 是免费红利。
- **数据建模**:
  - `xui/rawClientTraffic` + `ports.ClientTraffic` 加 `LastOnline int64`
    (3X-UI 的 wire 单位是 unix-MILLISECONDS,13 位时间戳;实测确认,见
    docs/3xui-compat.md "3.1.0 附带发现")
  - `users.last_online_at` 新增列(`*time.Time`,nil = 从未活跃);
    `domain.User.LastOnlineAt` 同步;GORM AutoMigrate 跨方言自动加列
  - `UserRepo.BatchUpdateLastOnline(map[int64]time.Time)` 新方法,column-scoped
    UPDATE wrapped in transaction(同 `BatchUpdateTrafficState` 的批写思路)
  - 转换在 traffic poll 落地时一次完成(`time.UnixMilli(ms)`),其它路径不
    需要知道 wire 单位
- **回滚保护**:lastOnline 为 0 不入 sink、不入 UPDATE → 对接 3X-UI < 3.1.0 panel
  的旧部署 `last_online_at` 字段一直保持 nil 也不会被错误地写成 epoch 0。

### Frontend

- `UsersView` 表头新增 `users.table.last_online` 列,放在 status 与 actions 之间。
- 新增 `formatRelativeTimeShort(diffMs, t)` helper:5 档分桶(刚刚 / N 分钟前 /
  N 小时前 / N 天前 / YYYY-MM-DD long-ago fallback),每档独立 i18n key 让翻译者
  完全控制单复数(EN: `5m ago` / `1h ago` / `2d ago`)。
- `User` interface(api/types.ts)加 `last_online_at?: string | null`,与后端
  `userDTO.LastOnlineAt` 对齐。
- i18n 同步 zh-CN / en-US:`users.table.last_online` + `users.relative_time.{
  just_now,minutes_ago,hours_ago,days_ago}`。

### Internal

- `pollSink` 加 `lastOnlineMs map[int64]int64`(per-user max,natural dedup);
  flush 阶段从 5 个 batch 升到 6 个(`mark("sink flush (6 batches)")`)。
- traffic + user service 各自的 `fake` repo 补 `BatchUpdateLastOnline` stub,
  保持 `ports.UserRepo` 实现完整。

## v3.6.0-beta.3 — 2026-05-25

### Added (远程升级 3X-UI / Xray ── Phase 2 第三刀,最危险的一刀)

- **Servers 页 ⋮ kebab 菜单新增"升级 3X-UI 面板" / "升级 Xray (最新)" 两项**:
  destructive 操作藏在 kebab 里(不放进常驻 Actions 按钮),避免误点;每项点击
  先弹 confirm dialog 二次确认,确认后才发请求。
- **`POST /api/admin/servers/:id/upgrade-panel`** ── 远程升级 3X-UI 面板。
  关键设计:**PSP 先 pre-check 目标版本**:
  - 调 `GetPanelUpdateInfo` 拿 3X-UI 准备升级到的 `latestVersion`(GitHub 上的最新)
  - 用 `version.CheckXUI(latestVersion)` 对照 PSP `MaxTestedXUI`
  - **超出范围直接拒绝**(409 + `reason: "untested_target"` + 详细 message)
  - 在范围内才调 `UpdatePanel` 触发自升级
  - 写 `panel_upgrade_initiated` / `panel_upgrade_blocked` audit 行
  这就是用户提的"升级到固定版本,而不是最新版"在当前 3X-UI API 限制下的落地——
  3X-UI 的 `/updatePanel` **没有版本参数**(只能 latest),所以 PSP 没法主动指定;
  但 PSP 可以**主动拒绝**升级到未测试版本,让 admin "先升 PSP 再升 3X-UI"。
- **后台 post-upgrade smoke probe** ── `UpdatePanel` 触发后,后端 fire 一个
  `safego.GoTracked` goroutine:
  1. 等 60s 让 3X-UI 完成自升级 + 重启
  2. 每 10s 调 `GetServerStatus`,最多重试 12 次(共 2 min 额外窗口)
  3. `/status` 一旦回 → 立刻调 `ListInbounds` 验证 schema 没崩(就是 2026-05-23
     的 3.1.0 schema-break 模式)
  4. 全部 ok → 写 `panel_upgrade_succeeded` audit + 刷新 `xui_panels` 版本快照
  5. 任何阶段失败 → 写 `panel_upgrade_failed` 或 `panel_upgrade_schema_break`
     audit(后者专门对应"panel 回来了但响应 schema 崩了"的情况,grep
     `schema_break` 立即定位)
  Admin 在响应里立即拿到 202 + "已发起",真正的成败 60-180s 后 audit 显形。
- **`POST /api/admin/servers/:id/upgrade-xray`** ── 远程升级 xray-core 二进制。
  body 可选 `{version: "latest" | "v25.x.x"}`,缺省 `"latest"`。**不做 pre-check**
  ── xray 跟 PSP 兼容性低耦合(PSP 只调 3X-UI panel,不直接调 xray API),admin
  自由升级。**不调 smoke probe** ── 3X-UI panel 自身不重启,只重启 xray 子进程,
  请求同步返回结果。完成后立即刷新 `xui_panels.xray_version` 让 UI 反映新版本。
- 全部 upgrade 路径写 audit:`actor` = admin upn(在线请求)/ `"upgrade-smoke"`
  (后台 smoke probe);`target` = `panel=<id> name=<name> target=<version>`;
  `after_json` 携带详细 message(成功/失败原因)。

### Frontend

- ServersView 每行 Actions 列新增 ⋮ `MoreVertIcon` 按钮,展开后是
  "升级 3X-UI 面板" / "升级 Xray (最新)" 两项,各自带 `UpgradeIcon` 指示。
- 升级面板拒绝时(409 + `untested_target`)弹 **warning** toast:"拒绝升级:
  目标版本 X 超出 PSP 支持范围 (max Y),请先升级 PSP",而不是 generic error。
- Xray 升级成功后顺手再 `probeServer(s)` 一次,刷新 Version 列里的 Xray
  版本号(后端虽然已 UpdateVersion,但前端 items 还要 merge 才能立即显示)。
- i18n 同步 zh-CN / en-US:`servers.action.{more,upgrade,upgrade_panel,upgrade_xray}`
  + `servers.toast.upgrade_panel_{started,blocked}` / `servers.toast.upgrade_xray_ok`
  + `servers.confirm.upgrade_{panel,xray}_{title,message}`。

### Docs (跨大版本升级政策修订)

- **ARCHITECTURE §16.4 改写**:之前"vN+1 发版时所有 vN.x cleanup 段直接删除"
  改成"**搬进 vN+1 的 `psp migrate` 子命令**作为必跑前序"。这样 admin 不再被迫
  先升到 vN 最新版才能升 vN+1 —— 从任意 vN.x(包括 vN.0 / vN.5 / vN.99)直接
  升 vN+1.0 都 OK,所有 vN 内 cleanup 由 vN+1 migrate 补跑。
- migrate 容积仍有上限:vN+1 migrate 只**链式兼容**上一个 major(vN.x),不追溯
  vN-1 及更早。admin 跨多 major(比如 v3.x → v5.x)必须分段升(v3 → v4 → v5),
  每步分别跑该版本的 migrate。
- §16.4.1 registry 表新增 "搬迁到" 列(原 "evict-by"),为 v4.0.0 实施者列了
  v3 当前 3 段 cleanupLegacyState 需要原样复制进 v4 migrate 流程。

### Internal

- `traffic_test.go` fake xui client 补 3 个新方法 stub(`GetPanelUpdateInfo` /
  `UpdatePanel` / `InstallXray`)以维持 `ports.XUIClient` 实现完整。
- `AdminServersHandler` 构造函数加 `audit ports.AuditRepo` + `async AsyncDispatcher`
  两个新依赖;router 同步更新 wiring。

### 已验证 / 未验证

- **已验证**:`GetPanelUpdateInfo` 端到端真实 3.1.0 panel 跑通(`current=3.1.0
  latest=v3.1.0 available=false`,compat=supported,会允许升级)。
- **未真实跑** `UpdatePanel` / `InstallXray` 是 destructive 操作,会真重启 3X-UI
  / Xray;按设计假定它们工作,发版后 admin 第一次触发时实地验证(失败有 audit
  痕迹 + 详细错误,可定位修复)。

## v3.6.0-beta.2 — 2026-05-25

### Added (admin 操作面 ──​ Phase 2 第二刀)

- **Servers 页"测试连接"现在顺手当版本刷新用**:admin 点 ⟳ "测试连接"
  按钮后,后端 `AdminServersHandler.Test` 在 `ListInbounds` 成功后再调一次
  `GetServerStatus`(beta.1 的 adapter 方法)→ 写回 `xui_panels.UpdateVersion`
  → 把 `panel_version` / `xray_version` / `compat_status` / `compat_message`
  / `version_checked_at` 一并回到响应里。前端 `probeServer` 把这些字段 merge
  回 `items`,Version 列 + 顶部 banner 立即刷新。**没有新加 endpoint**——
  "测试连接"按钮就是 manual refresh 入口,符合你定的"在 Server 页面测试的
  时候手动触发"。Best-effort:版本探测失败不影响"测试连接"本身的 ok/fail
  判定(版本是次要信号,可达性是主信号)。
- **`GET /api/admin/servers` 响应增加版本字段**:`serverDTO` 加
  `panel_version` / `xray_version` / `version_checked_at` /
  `compat_status` ("supported" / "too_old" / "untested" / "unknown") /
  `compat_message`(human-readable)。`compat_status` 只在 panel 已经被
  探测过(`panel_version != ""`)时填充,"从未探测"的 panel 字段全空——
  避免 UI 显示一个意义模糊的 "unknown" 徽章让 admin 误以为有问题。

### Frontend

- **Servers 页新增 "版本" 列**:展示 `3X-UI <ver>` + `Xray <ver>`(两行栈式
  布局),supported 状态无徽章(干净),其它三态各自带色块徽章(error /
  secondary / surface)+ tooltip 显示 compat_message。"从未探测" 显示 "—"。
- **顶部告警 banner**:任意 panel 处于 `too_old` 或 `untested` 状态时,
  Servers 页顶部出现红色 banner(`md.errorContainer`)+ ⚠ 图标 + panel 列表
  (name + version + status)。"unknown" 故意排除——通常意味着"刚启动还没
  探测完"/"网络瞬时失败",不算真实的兼容性问题,不刷红条。
- i18n key 同步:`servers.table.version` + `servers.compat.{banner_title,
  too_old, untested, unknown}` 都加进 zh-CN / en-US。

### Internal

- `web-react/src/api/servers.ts` 新增 `CompatStatus` 类型(`'supported' |
  'too_old' | 'untested' | 'unknown'`),跟 `internal/version.CompatStatus.String()`
  对齐 —— 任一边改了对方也要改。`Server` + `TestResult` interface 同步加版本
  字段。

## v3.6.0-beta.1 — 2026-05-25

### Added (PSP 主动感知 3X-UI 版本 —— Phase 2 第一刀)

- **PSP 启动时一次性探测每个 panel 的 3X-UI / Xray 版本,超出兼容范围即写 Warn**
  日志,后续 beta 在此基础上做 UI 红条 + Servers 页版本列 + 远程升级按钮。
  - 新增 `internal/version/compat.go`,声明本 PSP 构建支持的 3X-UI 版本范围:
    常量 `MinXUI = "3.1.0"`, `MaxTestedXUI = "3.1.0"`(hardcode 在源码,admin
    不能从 settings 表松绑——这是约束的本意:新 3X-UI 可能像 3.1.0 那样改
    schema,见 [docs/3xui-compat.md](docs/3xui-compat.md))。`CheckXUI(ver)`
    返回 `CompatSupported / CompatTooOld / CompatUntested / CompatUnknown`
    四态;`parseSemver` 容忍 `v` 前缀 / pre-release 后缀(3X-UI 的 status
    端点报 "3.1.0",getPanelUpdateInfo 报 "v3.1.0",同一发布两种写法)。
  - xui adapter 新增 `Client.GetServerStatus(ctx) -> *ports.ServerStatus`,
    一次调用拿全 `panelVersion / xrayVersion / xrayState`(实测 3.1.0 的
    `/panel/api/server/status` 直接返回这三个,**比** `/getPanelUpdateInfo`
    **更全面**——PDF 没明说这点,实测发现)。**只解版本子集**,cpu/mem 等
    rich payload 字段不进 ports 接口,保持跨进程契约窄。
  - `xui_panels` 表新增三列 `panel_version` / `xray_version` /
    `version_checked_at`(GORM AutoMigrate 跨方言自动加,SQLite / MySQL /
    PostgreSQL 同样安全);domain.XUIPanel 同步加字段。`XUIPanelRepo.UpdateVersion`
    新增,**列级写**(只 update 三个版本列),避免与 admin 同时编辑 credentials
    的 Save 互相覆盖——跟 `nodes.UpdateHealth` 一个思路。
  - **触发策略**:① `app.go` Run() 启动时 fire 一次 `boot-version-probe`
    goroutine(panic-shielded,WaitGroup 跟踪),让 first-impression 版本信息
    立即落日志,admin 不用等首轮 traffic poll 才看到;② 后续 re-probe **piggyback
    traffic poll 周期**——`runTrafficLoop` 每轮 PollOnce 之后顺手再跑一次
    `probePanelVersionsOnce`,PSP 反正已经在"打每个 panel" 的节奏里,加一个
    `/server/status`(~10ms/panel)零额外 ticker、零额外 cadence 设定。
    panel 升级后 PSP 在下一个 traffic poll(默认 5 min)内自然感知,完全符合
    自用规模的实时性需求。每个 probe 自带 10s timeout,unreachable panel
    不拖累 poll 主路径。
  - 日志策略:supported 走 Debug(steady-state,don't spam),non-supported /
    probe-failed 走 Warn 并带详细 message,运维 grep `compat warning` /
    `compat probe failed` 即可定位"哪台 panel 出问题"。
  - 零额外周期 loop,零干扰 health / reconcile / traffic poll 内部逻辑(继续
    v3.5 解耦原则)。`xui_panels.version_checked_at` 给后续 beta 的"上次探测于
    XX 分钟前"UI 提示用。beta.2 将加 admin Servers 页"测试连接"按钮顺手
    触发即时探测,作为 traffic poll 自然节奏之外的手动 refresh 通道。
  - 单测覆盖:`TestParseSemver_*` 四条(strip v 前缀 / 丢 pre-release 后缀 /
    两段版本 / 拒非法)、`TestCheckXUI_*` 四条(边界 supported / TooOld /
    Untested / Unknown)、`TestCompatMessage_*` 一条(message 含关键事实);
    端到端用真实 3.1.0 panel(`/server/status`)跑通 `GetServerStatus` →
    解码 → CheckXUI → CompatMessage 全链路验证。

### Internal / 性能

- **顺手清掉 v3.5.1-beta.2 升级 sub_logs 索引时残留的两个旧单列 idx**:
  GORM AutoMigrate 只 add 新索引、不 drop 旧索引,所以
  `idx_sub_logs_user_id` + `idx_sub_logs_accessed_at` 跟新建的复合
  `idx_sub_user_time` + 独立 `idx_sub_accessed` 并存,每次 sub_logs INSERT
  写 4 个索引(本该 2 个),拖累 `/sub/:token` 这条公开端点的写入吞吐。
  `cleanupLegacyState` 加幂等 drop 块,GORM Migrator `HasIndex` + `DropIndex`
  跨方言安全。Best-effort:DropIndex 失败 → log Warn 不 abort(redundant
  index 只是 perf wart,不是 correctness 问题)。

### Docs

- `docs/3xui-compat.md` 更新"远期规划"段落:把 v3.6.0 拆成 3 个 beta 渐进交付,
  各 beta 内容明确;同时修正之前那条"`nodes` 表加 xui_version" 的字段位置
  错误——3X-UI 版本是 panel 级实例属性,应该挂在 `xui_panels` 表上。

## v3.5.1-beta.2 — 2026-05-25

### Performance / Internal

- **订阅端点接入 HTTP ETag / 304 协议**:`/sub/:token` 在 render 后用 `sha256(body)`
  前 8 字节构造 weak ETag(`W/"xxxxxxxxxxxxxxxx"`),响应头同时写 ETag +
  `Cache-Control: private, no-cache`(强制每次 revalidate)。客户端带 `If-None-Match`
  且匹配 → 返回 304(约 100 字节)+ 流量/到期 header(`Subscription-Userinfo` 等
  仍每次重算,因为是动态数据)。**这是纯下行带宽优化**——render 本身仍然跑(给
  ETag 算 hash 用),但客户端轮询时 90%+ 的请求会返回 304 跳过 mihomo YAML / sing-box
  config 几十 KB 的下行流量。朋友圈 50 用户 × Clash 默认 10 分钟轮询 ≈ 300 次/小时
  的订阅 fetch 直接受益。`sub_logs` audit 行 304 仍然写入(304 是"客户端来过"
  的事实)。`If-None-Match` 比对支持 RFC 9110 §13.1.2 的关键场景:`*` 通配符 /
  逗号分隔的多 ETag / weak-strong 宽松比较(只发 weak,但接受客户端 strip 了 `W/`
  前缀重发回来)。回归覆盖:`TestComputeWeakETag_*` 三条 + `TestETagMatches_*` 六条。

- **`sub_logs` 索引升级为复合**:原来是 `user_id(index)` + `accessed_at(index)`
  两个独立单列 idx;主查询 `WHERE user_id = ? ORDER BY accessed_at DESC LIMIT N`
  没法在一个 idx 内同时完成等值过滤 + 排序。改成复合 `idx_sub_user_time(user_id,
  accessed_at)` 服务主查询 + 保留独立 `idx_sub_accessed(accessed_at)` 服务 retention
  DELETE(`WHERE accessed_at < cutoff`)——跟 [[traffic_snapshots]] 的设计原则一致
  (复合 idx 的 leading column 不能服务非 leading-column 的 range query)。AutoMigrate
  在升级时会自动建索引;sub_logs 表数据量大的话**首次启动会扫表建索引**,可能短暂
  锁表/慢启动,自用规模下可忽略。

### Docs

- **新增 [docs/3xui-compat.md](docs/3xui-compat.md)**:维护 PSP 版本 ↔ 3X-UI 版本
  兼容矩阵,沉淀 v3.5.1 那次"3X-UI 3.1.0 改 list 序列化把 PSP 打挂"的踩坑记录,以及
  升级 3X-UI 前应该走的检查流程。配合下一版 v3.6.0 计划的"PSP 自动版本探测 + Servers 页
  远程升级按钮",建立 PSP 对 3X-UI 升级的主动防御能力。

## v3.5.1-beta.1 — 2026-05-25

### Fixed

- **适配 3X-UI 3.1.0 的 `/inbounds/list` 响应格式变化**:3.1.0(2026-05-23 发布)把
  `settings` / `streamSettings` / `sniffing` / `allocate` 四个字段从 escaped string
  改成了 nested JSON object/array(`allocate` 改成 `null`)。PSP adapter 把这些字段
  声明为 Go `string`,直接 `json.Unmarshal` 一个 object 进去会报
  `cannot unmarshal object into Go struct field of type string` —— 任何升级到 3.1.0
  的 panel 一旦被 PSP 接入,traffic poll Phase 1 fetch 会**整轮失败 → 所有 user skip**。
  实测确认问题真实存在(用 PowerShell `Invoke-RestMethod` 探一台 3.1.0 panel 的
  list 响应,settings 三个字段都是 nested object)。修复:新增 `flexJSON` 类型,
  `UnmarshalJSON` 把 nested object/array 原样收下、`null` 归一化为空字符串;
  `rawInbound.Settings/StreamSettings/Sniffing/Allocate` 全部切到 `flexJSON`,
  下游 `rawToInbound` 转回 `string(...)` 喂给 `ports.Inbound`(外部接口零变化)。
  写端不动 —— 3.1.0 仍接受 escaped string 写法。**最低版本要求改为 3X-UI ≥ 3.1.0**
  (README §环境要求 已同步)。回归覆盖:`TestFlexJSON_*` 四条(nested object /
  nested array / null / 字段缺失);端到端用一台真实 3.1.0 panel 跑 `ListInbounds`
  → `json.Unmarshal(inb.Settings)` round-trip 全 ok。
  - 顺带观察(暂不动):3.1.0 `clientStats[*]` 多了 `uuid` / `subId` / `lastOnline`
    三个字段,Go `encoding/json` 默认忽略未知字段,我们 `rawClientTraffic` 不受影响。
    `lastOnline` 是免费的"用户最近活跃时间"素材,以后做"在线徽章"可以用上。

## v3.5.0 — 2026-05-25

正式版。汇总 v3.5.0-beta.1 → beta.16 全部改动，beta.16 内容直发为正式版定稿
（自 beta.16 起无追加修复）。完整逐项见下方各 pre-release 段落，下面只列本次
release 的核心叙事。

### 主要变化（叙事性总述）

- **inbound 配置本地化（架构主线，beta.1 起一路收尾到 beta.7）**:
  render / reconcile / 节点编辑对话框 / health 全部不再回源 3X-UI 拉 inbound
  协议参数，统一以 `nodes` 行里的本地快照为真相源。3X-UI 控制 API 短暂挂掉
  时，订阅生成、健康探测、漂移对账都照常跑;reconcile 轴 A 反向把本地配置
  下发到 3X-UI 保证最终一致;`ConfigSyncState` 新增 `pending` 状态把"PSP 想推
  但还没推上去"在 UI 上显出来。详见 `docs/inbound-ownership.md`。
- **traffic poll 性能,手动 "Poll Now" 从 ~10s 降到亚秒级**:
  Phase 2 串行 per-user / per-client 本地写改成"循环入 sink、末尾一次 batch
  flush"(beta.9);safety-net floor push 从热路径里移出去异步化(beta.12);
  `OwnershipRepo.ListByUsers` batched read 把 N+1 reads 收成一次(beta.15)。
  12 user × 5 panel × 9 inbound 实测从用户体感 6–10s 压到 234ms,瓶颈彻底回到
  Phase 1 跨区 `ListInbounds` 的网络物理下限。
- **默认安全姿态收紧**:
  `jwt_access_ttl_minutes` 默认 120 → 60(beta.8);
  `jwt_refresh_ttl_minutes` 默认 10080 → 1440(beta.11)。
  Sliding Refresh 保留,日常活跃用户不感知;只压"完全不活跃"的绝对窗口,事实上
  把 access/refresh 万一被偷的有效窗口从 2h/7d 压到 1h/1d。已有部署不受影响
  (settings 表里存过的值优先于默认值)。
- **日志分级三层入口**:
  `--debug` 启动 flag(beta.15) + `PSP_LOG_LEVEL` 环境变量(beta.14) +
  `log_level` config 字段(beta.16),优先级 **flag > env > config > 默认(info)**。
  其中 log_level 故意不进 settings 表——它得在 DB 加载之前生效,boot 早期诊断
  (比如 config load 失败)需要的就是"DB 起来之前可控"。
- **零散修复与健壮性**(节选,完整看下方):创建 inbound 丢响应产生的孤儿
  recovery(beta.4)、reconcile drift push 可能清空 3X-UI 全部 client 的两层
  防御(beta.2)、admin 编辑被 reconcile 静默撤销(beta.2)、`UpdateInboundConfig`
  4xx 无限重试(beta.3)、inbound 协议密钥(SS-2022 PSK / Reality privateKey)的
  AES-GCM at-rest 加密(beta.4)、新建/导入节点不再阻塞在批量 client 推送
  (beta.7)、Tags 输入框尺寸对齐(beta.7)、health 改 port-open 探测、render 取
  inbound 去重、`InSync` 不再碰 remark(beta.5)。

## v3.5.0-beta.16 — 2026-05-23

### Added
- **`log_level` 加进 `config.yaml`**:beta.14/15 已有 `PSP_LOG_LEVEL` env + `--debug` flag,这次补 config 文件这一层,让"长期开 debug"(比如 dev 部署)不用每次启动都靠 env/flag。完整优先级链:**`--debug` flag > `PSP_LOG_LEVEL` env > `log_level` config > 默认 (info)**。三个口子各管各的使用场景——flag 一次性、env 注入/临时、config 持久化部署默认。
  - 为什么 log_level 不能走 settings 表(跟 cron / JWT TTL 那批不同):它必须在 DB 加载完成前就生效,而 settings 表本身得 DB 起来才能读——boot 早期诊断 log(比如 config load 失败)需要的就是这种"在 DB 之前可控"的能力。
  - main.go 拆出 `parseLogLevel` + `applyEarlyLogLevel`:env/flag 在 config 加载之前 apply(保证 config-load 错误本身就受调级),config.LogLevel 在加载后作为兜底(仅在 env/flag 都没显式设过时生效)。
  - 默认 config 模板加 Logging 段注释(写在 Filesystem 前面,跟 listen/secrets 同属"全局基础"那一档),含完整优先级说明 + 注释掉的示例。已有 config.yaml **不受影响**——新字段空缺时模板默认行为(info)。

## v3.5.0-beta.15 — 2026-05-23

### Added
- **`--debug` 启动 flag**:跟 `PSP_LOG_LEVEL=debug` 等价但更顺手,直接 `./psp --debug` 起进程即可启用 debug 级日志(含 PollOnce 那 7 段 timing)。env 仍然支持;两者并存时 flag 优先(env 先生效、flag 后覆盖)。

### Changed
- **`PollOnce` 起步阶段的 ownership 读改为 batched**:新增 `OwnershipRepo.ListByUsers(ctx, userIDs)` 一次 SQL 把所有 user 的 ownership 行按 user_id 桶分类返回,替代原来 `for _, u := range users { ListByUser(u.ID) }` 的 N+1 read。**跨方言**:GORM `Where("user_id IN ?", ids)` 在 SQLite / MySQL / Postgres 三个 backend 都原生支持(本仓库 KV / Ownership 一直跨这三方言)。失败时 fallback 回原 per-user loop——一次 batched 读出问题不至于让整轮 poll 崩。
- 收益面:本地 DB 部署(sub-ms per query)~5–10ms 量级,几乎不可感知;远程 DB(每次 round-trip 5–30ms)就明显了,从 N 次降到 1 次。是 N+1 source 的纯代码工艺清理,跟 beta.9 的 `LatestForUsers` / `BatchUpdateXxx` 三件套保持一致风格——traffic poll 现在没有任何 per-user inline DB read/write 残留。

### Internal / 测试
- mysql: `TestOwnershipListByUsers` 覆盖 bucket-by-user_id 行为 + absent-user-omitted + empty-input-empty-map 三条不变量。
- fake repo(`internal/service/traffic/traffic_test.go` 镜像生产 batched 行为,`internal/service/sync/sync_test.go` 加 stub 保证编译)。

## v3.5.0-beta.14 — 2026-05-23

### Changed
- **traffic poll timing log 改为 Debug 级 + 引入 `PSP_LOG_LEVEL` 环境变量**:beta.13 临时加的 7 段 `log.Info("traffic poll timing", ...)` 现在改成 `log.Debug`,默认运行时不输出(零噪音),起进程时 `PSP_LOG_LEVEL=debug ./psp` 一键启用——下次 "Poll Now 感觉慢" 不用改代码 + 重发版,SSH 上去改启动参数重启即可看现场。`PSP_LOG_LEVEL` 接受 `debug / info / warn / error`(case-insensitive),空值或无效值保持默认 Info。

### Internal(beta.13 收尾确认)
- beta.13 timing log 实测数据:12 user / 5 panel / 9 inbound 部署下,Poll Now 总耗时 **234ms**(用户报"~10s")。各阶段分布:`listAllUsers 3ms / LatestForUsers prefetch 20ms / ownership.ListByUser×12 10ms / Phase1 parallel ListInbounds 154ms / Phase2 inbound 处理 29ms / user 循环 0ms(push 异步) / sink flush 16ms`。瓶颈是 Phase 1 跨区 ListInbounds 的网络往返(5 panel 并行,wall = 最慢那个 ≈ 150ms),已是物理下限。先前用户报的"beta.12 上了之后仍 6-10s"事后判断是 binary 没真换/进程没真重启所致(Docker 镜像未重 build,或 service 未 reload)。本轮 git pull + go build + 真正重启后,beta.9 batch flush(MySQL localhost 收益小)+ beta.12 push 异步化(主要功臣)+ Phase 1 并行(老优化)三者叠加把 wall-clock 压到了理论下限。问题闭环。

## v3.5.0-beta.13 — 2026-05-23

### Internal(临时 debug,不影响行为)
- **traffic poll 加 5 段 timing 日志**:beta.12 把 safety-net push 异步化后,用户实测 MySQL(localhost)+ 跨区 3X-UI 部署仍 ~6–10s,理论模型对不上(预期 1–2s)。在 `PollOnce` 头/尾 + 5 个关键阶段(`listAllUsers` / `LatestForUsers prefetch` / `ownership.ListByUser per-user loop` / `Phase 1 parallel ListInbounds` / `Phase 2 inbound processing` / `user loop` / `sink flush`)插入 `log.Info("traffic poll timing", "stage", ..., "ms", ...)` 行,加上 `TOTAL`。生产部署一次,Poll Now 触发后日志能直接看出热点(很可能是跨区 ListInbounds 单次本身就 > 2s 的"网络 + 3X-UI 序列化"问题)。**纯诊断,无行为变化**。下一个 beta 根据日志结论做对症优化并清理本节的 instrumentation。

## v3.5.0-beta.12 — 2026-05-23

### Changed
- **traffic poll safety-net floor push 移出热路径**:beta.9 把 SQLite per-row UPDATE 收敛成 batch flush 后,剩下的 wall-clock 大头是 `recordAndEnforceWith` 末尾每个 active-with-limit 用户串行的 `s.configPusher.PushClientConfig` ——每个 user 会做 `GetByID + ownership.ListByUser + 每个 panel 一次 ListInbounds + 每个 owned client 一次 3X-UI UpdateClient`(per-user 内部已并行,但 user 之间是串行的)。10 个 active-with-limit 用户 × ~300ms = 3+ 秒,完美对得上实测的"beta.10 后仍 6–10s"。两个互补优化叠加:
  - **delta == 0 直接跳过推送**:本周期 user-level 增量为 0 → `floor = limit − used` 跟上次推过的一样 → 3X-UI 那边的 floor 仍然有效,这次推是冗余的。过滤掉"active panel、本周期 idle user"(client 在 ListInbounds 里被匹配到但字节数没动)。
  - **剩下要推的全部异步 fire-and-forget**:`safego.Go("traffic.floor-push", ...)` 触发,用 `context.Background()`(防"Poll Now"handler 退出时 ctx 被取消、push 半路放弃),通过 service 级 `pushSem`(cap 8,与 `MaxPanelConcurrency` 默认对齐)节流,跨 cycle 共享——上一轮还没推完时下一轮起来,新任务排队等 sem 而不是直接打爆 3X-UI。PollOnce 不再阻塞等推送完成,管理员"Poll Now"立即返回。
  - 失败语义不变(`log.Warn` + 下一轮自然重推);floor push 本就是 best-effort 安全网。预计"Poll Now"从 6–10s 降到 1–2s 级别。

## v3.5.0-beta.11 — 2026-05-23

### Changed
- **`jwt_refresh_ttl_minutes` 默认 10080(7d) → 1440(1d)**:跟 beta.8 紧的 access TTL 一个思路——把"完全不活跃的会话"绝对窗口从 7 天压到 1 天。Sliding Refresh 保留不变,日常使用的用户每次 `/refresh` 都会同时拿到新 refresh token,事实上仍然永不登出;只有真的连续 24h 没动过的会话才会被这个上限干掉。effectively 把 refresh token 万一被偷的有效窗口从一周压到一天。**已有部署不受影响**——settings 表里已有的值是 UI 保存过的,默认变更只影响首次启动且字段从未被存过的部署;`app.go` 里 settings 加载失败的兜底默认同步收紧到 24h,与主默认对齐。

## v3.5.0-beta.10 — 2026-05-23

### Fixed
- **traffic poll rollover-reenable 路径在 beta.9 的 sink 化下产生 stale-read 回归(实测命中)**:beta.9 把 `recordAndEnforceWith` 主路径 + rollover 分支的两处 `UpdateTrafficState` 都改成"入 sink、末尾 batch flush"。审计发现 rollover 分支随即调用的 `SetEnabledAndSync(true)`(用户因周期复位而重新启用)会 `GetByID + 整行 Update + pushClientConfigToAll`——只要 sink 还没 flush,GetByID 读到 **OLD lifetime / OLD baseline / OLD periodStart**,`u.PeriodUsed()` 算成"OLD 周期接近用满",`floor = limit − used ≈ 0` 被推到 3X-UI,用户表面 re-enable、实际仍被 3X-UI 阻断到下一轮 poll(~5 min)才纠正。修复:`persistRollover` 改回 inline `UpdateTrafficState` + `delete(sink.userUpdates, u.ID)` 防末尾 batch 重复写;`ClearEmergencyAccess` 仍 inline 在 emergency lock 内不变。Rollover 通常每周期 0–1 用户(monthly typical),性能成本可忽略。
  - 回归测试 `TestPollOnceRolloverWritesSynchronouslyForDisablerReread` 注入一个 `capturingDisabler`,在 `SetEnabledAndSync(true)` 调用瞬间实读 fake repo 拿 `PeriodUsed()`,断言为本轮 delta(几千字节)而非 `limit`(10 GB)。stash 验证过 pre-fix 报 `PeriodUsed = 10737418240`,完美命中 stale-read 症状。
  - 常规 safety-net `PushClientConfig`(line ~789)仍走 `GetByID`,看到的 lifetime 比内存少 `this_cycle_delta`,floor 偏大同等数量,下一轮 poll 自纠。这是 sink 化的设计取舍——safety-net 语义是"面板长时间掉线时 3X-UI 自己兜底",5 min 级别的 floor 滞后不影响这个属性,接受并 `docs/poll-perf-optimization.md §8.5` 文档化。

### Internal / 清理
- traffic poll 末尾 flush 块原本用 `users` 作局部变量名,遮蔽 PollOnce 顶部加载的 `users` 列表,改名 `pending` 消除误读。

## v3.5.0-beta.9 — 2026-05-23

### Changed
- **traffic poll 末尾批量 flush,手动"Poll Now"从 ~10s 降到亚秒级**:`PollOnce` 的 Phase 1(拉 3X-UI 数据)早已并行,瓶颈一直在 Phase 2 串行的 per-user / per-client 本地 DB 写——尤其 SQLite WAL 每次 commit ~5–10ms。N 用户 × M client 一轮 poll 是 `N + N×M` 次自动提交;100×8 ≈ 900 次,刚好对得上用户实测的 ~10s。本轮把热路径 3 个动作改成"循环里只入 sink,循环结束统一一次 flush":
  - 新增 `OwnershipRepo.BatchUpdateCounters` / `UserRepo.BatchUpdateTrafficState`:GORM 事务包 N 条 UPDATE,SQLite 下 N 次 commit 合并为 1 次;MySQL/PG 也省掉 N − 1 次 round-trip。列范围、emergency-column 跳过、零 ID 拒绝等约束与原单行方法逐项一致(`TestBatchUpdateTrafficState` / `TestOwnershipBatchUpdateCounters` 覆盖了空输入 no-op + zero-ID 整批回滚 + emergency 不被覆盖三条不变量)。
  - 新增 `TrafficRepo.LatestForUsers(ids)`:子查询 + IN 一次拿全部用户的最新 snapshot,替代每用户一次的 `LatestForUser` SELECT。`MAX(id)` 作 tie-breaker,与单用户路径 `Order("id DESC").Limit(1)` 语义一致(`TestLatestForUsers` 显式比对单用户结果)。
  - `recordClientStats` / `recordAndEnforceWith` 改为 sink-aware:有 sink 入队,无 sink 走原 inline(非 poll 调用者 / 测试 / `recordAndEnforce` 回退路径不变)。rollover 分支的两次 user UPDATE 通过 sink `map[int64]*User` 去重,确保一个用户每轮只写一行;`ClearEmergencyAccess` 仍 inline 在 emergency lock 内,沿用 v3.3.0-beta.6 不让 stale 写吃掉 live grant 的约束。**注:rollover 分支的 sink 化在 beta.10 被回退——见上。**
  - PollOnce 末尾在现有 3 个 `InsertBatch` 之后追加 `BatchUpdateCounters` + `BatchUpdateTrafficState` 两次调用。一轮 N × M 写场景的预期总 DB 操作:`1 LatestForUsers + 3 InsertBatch + 1 BatchUpdateCounters + 1 BatchUpdateTrafficState = 6 次`,与用户体量基本解耦。`TestPollOnceBatchesPerCycleWrites`(3 用户 × 4 client)断言这三个 batch 调用各恰好一次。

### Internal / 测试
- `internal/service/sync/sync_test.go` + `internal/service/user/user_test.go` 的 fake repo 各补一个新方法 stub,保持 `ports` 接口实现完整(否则全量 `go vet` / 包级测试编译失败)。
- 计划文档:`docs/poll-perf-optimization.md` 是本轮实现前的方案稿(问题定位、热路径表、跨方言策略、落地清单),保留作为后续回看用。

## v3.5.0-beta.8 — 2026-05-23

### Changed
- **`jwt_access_ttl_minutes` 默认 120 → 60**:把 access token 万一泄漏的有效窗口从 2h 压到 1h(浏览器 XSS / 日志意外带出截图等场景),前端 `/refresh` 频率从每 2h 一次升到每 1h 一次——自用规模下网络成本可忽略。`jwt_refresh_ttl_minutes` 保持 10080(7d)。当前是 **Sliding Refresh** 模式:每次 `/refresh` 同时重发新 refresh 让活跃用户事实上永不登出,这是有意保留的——只把"绝对的"账号窗口压短了。**已有部署不受影响**:settings 表里已有的值是你 UI 保存过的,默认变更只影响首次启动且字段从未被存过的部署;`app.go` 里 settings 加载失败的兜底默认同步收紧到 60min,与主默认对齐。

## v3.5.0-beta.7 — 2026-05-23

### Changed
- **新建 / 导入节点不再阻塞在"逐个推 client 到 3X-UI"**:`CreateInbound` / `ImportExisting`(以及两条 task 重放路径)的 `syncExistingUsersToNode` 改为 `safego.Go` 后台跑——保存请求一返回就成功,N 个用户的 client 推送在 goroutine 里继续。一个 100 人的 group 之前要等 ~20s,现在秒回;goroutine 失败 / 进程重启没推完的,reconcile 轴 B 的 `checkMissingOwnerships`(15min 内)兜底重建。沿用 beta.7 早些时候 `user.ResyncGroupMembersInBackground` 的同款 immediate + reconcile-fallback 模式。
- **health 不再调 3X-UI**(承接 v3.5 本地化):port / protocol 直接读 `nodes` 行(write-through + reconcile 轴 A 维持),不再每 5 分钟每 panel 一次 `ListInbounds`。控制面 / 数据面彻底解耦——3X-UI 控制 API 挂掉时 health 仍照常跑。`panel_unreachable` / `inbound_missing` 两个旧状态在 health 内不再写入(数据面探测失败统一报 `unreachable`;inbound 存在性由 reconcile §9.4.3 #6 兜底)。`Service.pool` 字段一并去掉,`health.New` 签名收缩为 `(nodes ports.NodeRepo)`。

### Fixed
- **新增节点对话框的 Tags 输入框比 Region 高一截**:`TagsAutocomplete` 写死 `size="medium"`,而周围 TextField(含 Region)都是 `size="small"`,同一行视觉错位。改成 `size="small"` 后,创建 / 编辑 / 导入 / 分隔符四处共用的 Tags 输入都和邻居字段对齐(里面的 Chip 本就是 small)。
- **admin 编辑路径推送失败时 `ConfigSyncState` 正确置 `pending`**(节点管理审查发现):之前 `node.UpdateInboundConfig` 在 `c.UpdateInbound` 失败 / panel 不可达时只入异步重试队列、本地 state 仍写 `synced`(误报"已同步");现已和 reconcile axis A 对齐——置 `pending` 并落盘,`SyncTaskNodeUpdate` 重试成功后再复位 `synced`。UI / 监控现在能正确反映"PSP 想推但还没推上去"。

### Added
- **reconcile 轴 A 可观测性**(beta.1 inbound 本地化收尾):
  - 每条 inbound 级配置事件单独写一条 `audit_log`(`inbound_config_backfilled` / `_drift_pushed` / `_push_failed` / `_recapture_failed` / `_backfill_failed`),actor=`reconcile`、target=`node=N panel=P inbound=I`。原有的 cycle-aggregate 汇总行(`reconcile_full` / `reconcile_light`)仍写,这是其上的 per-inbound 流水。
  - **`ConfigSyncState` 新增 `"pending"` 状态**:reconcile 下发推送失败 / 推后回采失败时由 `markConfigSyncStatePending` 写入 `nodes.config_sync_state`,UI / 监控可区分"已同步" vs "PSP 想推但推不上"。下一轮成功推送 / 回采时由 `inboundcfg.Capture` 复位为 `"synced"`。

### Docs
- ARCHITECTURE.md 正文回写为 v3.5 现实:§3.2 / §9.3 / §9.4.3(#7 改写 + 新增 #8 轴 A 配置漂移) / §9.4.5 🚫 / §9.5.1 导入接管,撤销旧"3X-UI 是 inbound 协议参数单源真相"表述,均交叉引用 `docs/inbound-ownership.md`。
- 补 `CreateInbound` / `ImportExisting` write-through 集成测试,以及 `inboundcfg.HasLocalConfig` 单测;`UpdateInboundConfig` 落盘加密往返已有覆盖(beta.2)。

## v3.5.0-beta.6 — 2026-05-22

### Fixed
- **编辑 inbound 对话框改读本地快照,不再实时拉 3X-UI**(承接 beta.1 的 source-of-truth 一致性):`GetInboundConfig`(编辑框数据源)之前 live-fetch 3X-UI,而 render / reconcile 都以本地快照为真相源。若本地与 3X-UI 已漂移,编辑框会显示 3X-UI 的漂移值,管理员一保存就把 PSP 本该强制的配置悄悄丢了(被 live 预填→写回本地+3X-UI)。现在已捕获节点(`ConfigSyncedAt != nil`)编辑框读本地快照,与渲染 / 对账三者一致;仅未捕获节点(pre-v3.5 / 刚导入未回填)才回源。节点详情页的 client 列表仍走 `ListClientsOfInbound`(始终 live),不受影响。
- "节点是否有本地配置"的判断统一收进 `inboundcfg.HasLocalConfig`,render 与编辑框共用一份定义,杜绝两处判定漂移。

## v3.5.0-beta.5 — 2026-05-22

### Changed
- **承接 beta.1 inbound 配置本地化的去重 + 健壮性收尾**(除下述 remark 一项外,无对外行为变化):
  - **渲染取 inbound 配置去重**:三种订阅格式(mihomo / sing-box / URI-list)各自重复的"本地快照优先、未捕获节点按 panel 批量回源"逻辑收敛为单个 `resolveInbounds`;删除已无生产调用的 `inboundForNodeRender` 死代码。
  - **inbound `remark` 完全归运维,reconcile 不再碰它**(撤销 beta.3 的 "InSync covers remark"):remark 是展示用标签,强制它会让管理员在 3X-UI 直接改名后每轮被 reconcile 改回。现在 ① `InSync` 不因 remark 判漂移;② 即便因真实连接配置漂移触发下发,也用 live 的 remark 而非 PSP 存的(保住运维改名,推后 re-capture 再把运维值同步进快照)。仅 PSP 主动新建 / 编辑 inbound 时按表单写 remark。
  - **`jsonEqual` 空值等价**:`""` / `null` / `{}` / `[]` 视作等价,消除"存储侧归一成 `{}`、3X-UI 侧返回 `""`"被误判为永久漂移、反复死推的潜在脆弱点。
  - **reconcile 稳态零额外读库**:已捕获且 in-sync 的节点直接跳过,不再为每个节点做一次防陈旧重读(`GetByID`);只有待回填 / 漂移节点才付重读代价。
  - 本轮触及文件统一 `gofmt`。

## v3.5.0-beta.4 — 2026-05-22

### Fixed

- **创建 inbound 时丢失 3X-UI 响应会产生孤儿 inbound**:`CreateInbound` 调用
  `AddInbound` 成功但响应被掐(网络抖动 / 面板重启 / 超时),客户端只看到错误,
  任务入队重试。下一轮 `AddInbound` 会收到 "port already exists",旧代码直接
  归类为永久失败、任务被 cancel —— 结果 3X-UI 上有一个真实 inbound,PSP 这边
  却没有 node 行指向它,管理员需要手动到 3X-UI 清理。新增 `tryAdoptOrphan`:
  retry 看到 port-exists 错误时先 `ListInbounds`,找到一个与 spec 严格匹配
  (port + protocol + listen 全等)且**不被任何其它 PSP 节点拥有**的 inbound,
  当作上一次"丢响应"的产物吸收过来(`Capture` live 配置 + 创建本地 node 行)。
  严格匹配 + 排除已有 owner 双重保险,几乎不可能误吸非 PSP 的 inbound。真正
  的端口冲突(另一个 protocol 占用同端口、或同 inbound 已被别的 PSP node 占)
  仍然走原来的永久失败路径。

### Internal / 测试

- **reconcile / node 测试 fake 区分 `Update` 和 `UpdateInboundConfig` 调用**:
  之前 fake 把全行 `Save` 和 column-scoped 快照写入都记到同一个 slice,
  无法断言生产代码用了正确的 writer(snapshot 写应该走列级 `UpdateInboundConfig`,
  否则跟 `UpdateHealth` 列级竞争)。fake 拆成两个 counter,reconcile axis-A
  的 backfill / stale-read 用例现在显式断言 "用列级写入 + 不用全行 Save"
  —— 防 v3.5.0-beta.1 那个 bug 再回归。
- **新增节点 inbound_settings / stream_settings 加密 round-trip 集成测试**:
  `TestNodeRepo_InboundSecretsRoundTripEncrypted` 用 sqlite 端到端跑一遍,
  写一条带 SS-2022 server PSK 和 Reality privateKey 的节点 → 读回 verify
  解密 = 原文 + 直接读 raw 列 verify 有 `enc:v1:` 前缀 + 原文密钥**不出现**
  在 stored row 里。第二步用 `UpdateInboundConfig` 改 PSK 重测,保证 column-
  scoped writer 也走加密。再加 `..._LegacyPlaintextStillReads` 锁定"pre-v3.5
  明文行读回不变"的软迁移契约。

### Changed (documentation)

- **`inboundcfg.ApplySpec` / `InboundFromNode` / `StripClients` 注释补足**:
  把之前 review 提到的"接受的 trade-off"白纸黑字写进 godoc:partial-PATCH
  会把无条件字段(Listen/Remark/Sniffing/Allocate/ExpiryTime)零化、Port
  与 Protocol 的 zero-guard 是有意的不对称、`Enable` 由 PSP 独占跟 3X-UI
  可能分歧(健康探测 + reconcile 兜底)、`StripClients` 路径在有 clients[]
  时会重 marshal(语义对比即可)、`ports.Inbound.Tag` 不进 round-trip。

## v3.5.0-beta.3 — 2026-05-22

### Fixed

- **inbound `remark` 在 3X-UI 被改不会被 reconcile 拉回**:axis-A 的 `InSync`
  漏比 `Remark` 字段——`Capture` 落库、`SpecFromNode` 发回去都带它,但 drift
  判定却跳过。结果是操作员在 3X-UI 直接改 inbound 备注后,PSP 既不重写回 PSP
  版本、也不更新本地快照,处于"知道有但视而不见"的状态。`InSync` 补加 Remark
  对比,跟其它字段同等强制。
- **`UpdateInboundConfig` 推到 3X-UI 收到 4xx 后无限重试**:之前 retry 退避里
  的 `isPermanentNodeTaskError` 只识别 `ErrAlreadyExists` / `ErrValidation` /
  `ErrInboundHasUnmanagedClients` 三种,而 `xui.doJSON` 把所有 HTTP 错误都包成
  普通字符串错误,4xx(无效 spec / 找不到 inbound 等)永远命不中 permanent,
  每分钟一次推下去、每次都失败。改成在 `doJSON` 把 4xx(401 / 408 / 429 除外)
  统一 wrap 进 `domain.ErrValidation`,task 运行器现在能正确把它标记为永久失败、
  停止重试;401 走原有的 re-auth 路径,408 / 429 / 5xx / 网络错误仍归类为
  瞬时、继续退避重试。

### Changed

- **文档与代码注释统一去掉 v4 前缀,改用 v3.5**:本次"inbound 配置本地化"
  原计划在 v4.0.0 major 切版做,实际决定非破坏性、增量发布在 v3.5.0-beta.1,
  但代码注释 / docs 文件名仍带"v4"字样,容易跟"下一个 major v4.0.0"混淆。
  改名:`docs/v4-inbound-ownership.md` → `docs/inbound-ownership.md`;所有
  架构相关的 v4 注释 → v3.5。UUID v4(协议层 UUID 版本)与 v4.0.0(指未来
  major)的引用保持不变。

## v3.5.0-beta.2 — 2026-05-22

### Fixed (v3.5 inbound 配置本地化的修补)

- **空 settings 的 inbound 在 reconcile drift push 时可能清空 3X-UI 全部 client**:
  `Capture`/`ApplySpec` 把 `settings==""` 落库为空字符串,后续 `InSync` 视作 drift 推空
  settings,而 xui client 的 RMW 兜底 `settingsWithCurrentClients` 又把空 `nextSettings`
  直接放行——3X-UI 收到 `settings=""` 可能持久化并清空 `clients[]`。两层都加防御:
  `Capture`/`ApplySpec` 把空规范化为 `{}`;`settingsWithCurrentClients` 也把空 next
  规范化为 `{}` 并强制走 RMW merge,确保 live clients 一定被注入。
- **admin 编辑可能被 reconcile 静默撤销**:reconcile cycle 顶部 `List()` 拿到的 node 行,
  如果在 `reconcileInboundConfig` 执行前被 admin 写过(`UpdateInboundConfig`),旧代码
  会拿 stale snapshot 当真相去 push,覆盖 admin 刚保存的配置。push 前重读节点行,
  对比 `ConfigSyncedAt` 时间戳作为乐观锁——不一致就跳过本轮,下一轮拿到 fresh 自动收敛。
- **post-push re-capture 失败 → 无限 drift 循环**:推完 3X-UI 后调 `GetInbound` 重抓
  snapshot 失败时,旧代码忽略错误继续 `nodes.Update`,但本地 snapshot 没变 → 下一轮
  reconcile 又判定 drift 又推,死循环。改成显式 emit Issue 并 return,**不** mark fixed,
  下一轮重试到成功为止。
- **inbound 配置 snapshot 列与 `UpdateHealth` / `UpdateTrafficCounters` 列级冲突**:
  reconcile 和 admin 写路径用 GORM `Save`(全行 UPDATE)写 snapshot,与 health/traffic
  的列级写法在并发时互相 clobber。新增 `NodeRepo.UpdateInboundConfig` 列级写法,所有
  snapshot 写路径(admin write-through、reconcile backfill、post-push convergence)都改
  走它,跟 `UpdateHealth` / `UpdateTrafficCounters` 同等并发安全。
- **Reality `privateKey` / 内联 TLS 证书私钥明文存数据库**:v3.5 把这些字段从 3X-UI
  迁到 PSP 本地后,新增的 `nodes.stream_settings` 和 `nodes.inbound_settings`(后者含
  SS-2022 server PSK)成了"无人看管"的 server-identity secret。两列加进 AES-GCM
  加密管道,跟 SAML 私钥 / OIDC client_secret / SMTP 密码同等保护。**老明文行无需迁移**——
  `encryptSecret` 在没配 `PSP_SECRET_KEY_MATERIAL` 时直接 passthrough,`decryptSecret`
  见到没 `enc:v1:` 前缀也 passthrough,下次写入时自动加密。`secrets-at-rest` 启动 audit
  也增加这两列的提醒。

### Changed

- **reconcile axis-A 异常更可观测**:之前的几条静默失败路径现在都 emit Issue,反映在
  reconcile 报告里:`inbound_config_backfill_failed`、`inbound_config_recapture_failed`、
  `panel_unreachable`(每个不可达 panel 一次,避免刷屏)。3X-UI 离线或本地写失败不再无声。
- **reconcile `checkNodes` 复用 `RunOnce` 预取的 cache**:axis-A 之前在 prefetch 之外又
  自己跑了一遍 ListInbounds,每个 panel 每轮 reconcile 多打一次 API。现在 checkNodes
  接收同一份 cache,axis-A / axis-B 共用,3X-UI API 调用减半。
- **sing-box / URI-list 渲染的 live fallback 也批量化**:beta.1 只让 mihomo `buildProxies`
  用 panel 分桶 + 并发 ListInbounds 预取,sing-box 和 URI-list 还是每个未捕获节点
  单独 `GetInbound`(N+1)。两者改用同一 `prefetchInboundsForRender`——过渡期一个 10
  节点 / 2 panel 的订阅,原本 10 次 `GetInbound`,现在 2 次 `ListInbounds`。
- **`SyncTaskNodeUpdate` 重试用本地 snapshot 而不是入队时的 spec**:rapid edits 收敛
  靠队列里最新的 spec,但旧实现把 enqueue 时的 spec 写进 `task.Payload`、运行时反序列
  化推过去,多次连续编辑可能让 3X-UI 短暂被推回老配置。改成运行时读
  `inboundcfg.SpecFromNode(n)`(本地真相源),同时统一开启 dedup:同节点的 NodeUpdate
  任务总只保留一条,本地 snapshot 谁后写谁赢。

### Detach 行为变化(behavioral change)

- **`Detach Node` 改为纯本地操作,不再联系 3X-UI**:之前 detach 会入队任务、调 3X-UI
  清掉 PSP 创建的 client(保留 inbound 和其它 client)。问题是 detach 的真实使用场景就是
  "服务器已经下线 / 面板不可达 → 我不想 PSP 再去那个面板上重试任何东西",旧实现会对
  一个死面板无限退避重试。改为:detach = 删本地 node 行 + 清本地 ownership 白名单,
  **不**调 3X-UI;之前在该 inbound 上由 PSP 创建的 client 留在 3X-UI 上,需要的话管理员
  自行去 3X-UI 清理。`SyncTaskNodeDetach` 任务类型移除。
- **Delete 语义不变**:仍走异步 sync 任务,先在 3X-UI 清 PSP 拥有的 client、再删 inbound、
  最后删本地 node 行,远端失败按 1min 退避重试。Delete 适合"服务器还在但我不再用了";
  Detach 适合"服务器没了 / 不可达"。两者前端都有确认对话框。

## v3.5.0-beta.1 — 2026-05-22

### Changed
- **订阅渲染不再实时回源 3X-UI:inbound 连接配置本地化,PSP 成为真相源**(详见
  [docs/inbound-ownership.md](docs/inbound-ownership.md))。之前每次拉订阅,render 都在请求
  热路径上调 3X-UI 的 `ListInbounds` 取端口 / stream / TLS / Reality 等连接参数——高频轮询把
  压力传导到上游,且面板一挂订阅就渲染失败。现把这些配置完整存进 `nodes` 表(全保真,镜像
  `InboundSpec`:listen / remark / settings(去 clients) / streamSettings / sniffing / allocate /
  expiryTime),render **只读本地、零回源**,3X-UI 临时不可达也能照常发订阅。
  - **写路径 write-through**:经面板新建 / 编辑 inbound 时,配置先存本地再下发(local-first,
    下发失败进异步重试队列,本地已生效);导入已有 inbound = 接管,把 live 配置吸进本地一次。
  - **reconcile 轴 A(配置层)**:无本地快照的老节点 → 从 live 回填(AutoMigrate 加列,无需迁移
    工具);有快照但 3X-UI 被手改 → 用 PSP 版本下发覆盖(持续强制),下发走 read-modify-write
    **保留全部 client**(PSP 管理的 + 手动建的私人 / 朋友 client 一个不动),推后再回采 live 收敛。
  - **client 级(到期 / 流量限制 / 启用 / uuid / 派生密码)完全不变**:仍由 sync / reconcile
    轴 B 维护,只管自己 email、绝不碰手动 client。本次只动 inbound 连接配置这一层。
  - 过渡期:升级后到首次 reconcile 回填之间,未回填节点 render 临时回源一次,回填后消失。

## v3.4.0-beta.12 — 2026-05-22

### Changed
- **节点健康检测改为「端口是否开放」(数据面可达性)**:之前只问 3X-UI「inbound 在不在 /
  启没启用」(控制面),节点机器挂了但面板还活着也显示 Up。改为直接探测代理端口:
  - TCP 协议(VLESS/VMess/Trojan/SS):TCP connect `ServerAddress:Port`,连上=Up、拒绝/
    超时=Down(并发探测,每个 5s 超时)。
  - Hysteria2(UDP):best-effort UDP 探测(open|filtered——只有收到 ICMP 端口不可达才判
    Down;UDP 无连接,精度只能到这)。不引入 QUIC 依赖。
  - 不再因 inbound 被停用单独判定——停用的端口本就不监听,探测自然失败=Down。
  - 新增 `Node.Port` 缓存(从 inbound 刷新,AutoMigrate 无需迁移):面板 admin API 临时
    挂掉时仍能用缓存端口探测,所以「面板 API 不可达」不等于「节点不可达」。
- **「最后检查」时间改为每轮都更新**:之前因「状态没变就跳过写库」的优化,时间戳只在状态
  变化时更新,显示的其实是「上次状态变化时间」。现每轮探测都刷新,名副其实。

## v3.4.0-beta.11 — 2026-05-22

### Fixed
- **快捷链接的 URL 图片图标不显示**:面板 CSP 的 `img-src` 只允许 `'self' data: blob:`,
  外部图片(如填了某站 favicon 的 URL)被浏览器按 CSP 拦掉、触发 `<img>` 的 onError 而
  隐藏。`img-src` 放行 `https:`,外部 HTTPS 图标即可加载。`script-src` 仍锁 `'self'`,只是
  放宽 `<img>` 的来源、不影响代码执行。(需重新部署二进制 + 浏览器刷新生效。)

## v3.4.0-beta.10 — 2026-05-22

### Fixed
- **快捷链接卡片:无描述时文字未垂直居中**:卡片行固定 `flex-start`,只有标签(无描述)
  的卡里单行文字顶对齐、与 32px 图标框不齐。改为无描述时整行居中,有描述时才顶对齐
  (让图标对齐第一行)。

## v3.4.0-beta.9 — 2026-05-22

### Changed
- **创建 inbound 对话框的 Tags 改为可搜索下拉**:之前是纯文本框,只有编辑 / 导入对话框
  用的是带搜索 + 选已有标签的 `TagsAutocomplete`。统一为同一个组件——创建节点时也能从
  现有标签(Trusted / Premium / Starter…)里搜索勾选,或敲回车新建,避免手抖打错把标签
  命名空间打散。

## v3.4.0-beta.8 — 2026-05-22

### Changed
- **改 Group filter 后保存:立即执行 + 后台**(在 beta.7 基础上调整为更符合预期的做法):
  Group 记录本身**同步存库、立即返回**;成员的 3X-UI 重同步改为**后台 goroutine 里立即
  执行**(每个成员先尝试同步、失败才入异步队列 `ResyncMembershipOrEnqueue`),不再阻塞
  保存请求、也不必干等 sync-task 周期。reconcile 兜底进程中断的残余。
- **节点编辑框移除「Flow」字段**:Flow 是 VLESS inbound 级设置,应在创建 / 导入 inbound
  时配,不属于节点元信息;之前所有节点(含 SS/VMess/Trojan)的 Edit 都显示它(旧节点
  `protocol` 为空触发了「未知则显示」的兜底)。现从节点 meta 编辑里去掉——既有值照常
  round-trip 保留,只是不在此处编辑。VLESS 的 Flow 仍在 inbound 配置 / 导入表单里。

## v3.4.0-beta.7 — 2026-05-22

### Fixed
- **改 Group 的 tag_filter 后保存很慢**:保存请求里**同步**地对该组每个成员逐一做完整
  3X-UI 重同步(每人多次面板往返),成员多 / 面板慢就一直卡到全部同步完才返回。改为
  **只入队** per-member 重同步任务后立即返回(几次快速 DB 操作,按目标去重),由后台
  sync-task 处理器 + reconcile 在后台对最新的组定义 propagate——与项目「写 3X-UI 走异步
  队列、不阻塞请求」的设计一致。代价:成员的节点变更不再即时生效,最多滞后一个 sync-task
  周期(~30s),reconcile 兜底。

## v3.4.0-beta.6 — 2026-05-22

### Fixed
- **后台快捷链接编辑器:图标改为单一控件**:原先「图标」自由文本框 + 「内置图标」下拉是
  两个控件编辑同一字段,易混淆/冲突。合并为一个图标框——文本框(emoji / 图片 URL /
  `mui:`)+ 框内「选内置图标」按钮(弹菜单选,写回同一字段),一个字段一个真相。
- **编辑器新增字段缺英文**:图标 / 分组 / 描述 / 突出 等只有中文兜底,补 `admin` 命名空间
  的 `link_table.*` 中英文。
- **门户快捷链接:纯标签时不再空荡靠左**:当所有链接都没有图标 / 描述 / 分组时,渲染回
  原来的紧凑按钮排(不再是文字贴左、右侧大片留白的宽卡片);有图标 / 描述 / 分组才用卡片
  网格。卡片模式下无图标的链接回退一个通用链接图标,保持整排对齐。

## v3.4.0-beta.5 — 2026-05-22

### Added
- **快捷链接增强自定义**:每条快捷链接新增 **图标 / 描述 / 分组 / 突出** 四项。
  - **图标**单字段自动判别来源:`http(s)://…` 当图片渲染、`mui:Name` 用内置精选图标库
    (~22 个快捷链接常用图标,后台下拉选)、其余当 emoji/文本。后台编辑器带实时预览。
  - **描述**:标签下可选副标题。
  - **分组**:同名分组在门户里归到一个分区(带分区标题);**无分组则平铺**,不强制。
  - **突出**:高亮某条(填充色卡片),适合重点推教程 / 续费。
  - 门户的快捷链接从「一排朴素按钮」变成 图标 + 标签 + 描述的卡片网格,按分组分区。
  - 全是 `QuickLink` 的新增字段(KV JSON,AutoMigrate 无需迁移),旧数据读出即带空值兼容。

## v3.4.0-beta.4 — 2026-05-22

### Changed
- **概览两列重新配平**:流量 / 客户端搬走后,概览左列只剩订阅链接、右列偏长,左右不齐。
  把「快捷链接」从右列移到左列(订阅链接之下),变成 左=订阅链接+快捷链接 / 右=用量+
  应急,高度大致配平。移动端顺序不变(`order` 保留)。
- **流量 / 客户端标签去掉折叠**:两者各自独占一个标签后,可折叠的 Accordion 没有意义,
  改成普通 Card 直接展示(无展开箭头)。客户端标签现在也常显教程链接(原先它挂在概览的
  推荐客户端卡上)。

## v3.4.0-beta.3 — 2026-05-21

### Changed
- **用户页拆成 4 个标签**:在「概览 / 服务器状态」基础上,把**流量曲线**和**其他客户端
  (一键导入)**各拆为独立标签——概览(订阅链接 + 用量摘要 + 推荐客户端 + 快捷链接 +
  应急)/ 流量 / 客户端 / 服务器状态。实现上用「就地按标签门控」:概览仍是原两栏响应式
  布局,非概览标签时右列不渲染、左列单卡自然撑满全宽,没有搬动那套精细排版。中英文标签
  齐全。

## v3.4.0-beta.2 — 2026-05-21

### Fixed
- **服务器状态 / 用户页标签的英文缺失**:新加的标签与状态文案只写了 `defaultValue`
  中文兜底、没在 i18n 语言文件里加 key,所以切到英文仍显示中文。补上 `user` 命名空间的
  `tabs.*` / `status.*` 的中英文翻译。

## v3.4.0-beta.1 — 2026-05-21

v3.4.0 的首个 beta。此前未发布稳定版的 `v3.3.0-beta.1 ~ beta.9` 开发线整体提升为
v3.4.0,逐条明细见下方各 beta.* 小节。相对上一个稳定版,本版主要内容:

### 重点
- **订阅客户端统一注册表**:检测规则 + 一键导入合并为「检测族 → 导入 App」两层注册表,
  含黑 / 白名单过滤模式;移除面板不产出格式的 Surge 系;Quantumult X UA 修正。
- **用户页改为标签布局,新增「服务器状态」标签**:用户可查看自己节点的可用性(脱敏:
  名称 / 地区 / 正常·离线·未知 / 最后检查时间,不含宿主机指标与失败位置)。
- **三个日志页(审计 / 订阅 / 邮件)模糊搜索**;**被禁客户端邮件提醒**(默认关、每日上限、
  与自动停用互斥);**全局友好错误页**。
- **流量耗尽 / 周期恢复邮件**(此前自动停用 / 恢复完全不发信);**SetPeriodUsage 周期口径
  修正**;**应急访问被并发 poll 误清的竞态修复**;**同一 inbound 并发写锁**;**rollup 历史
  小时桶随保留期回退修复**;**SS-2022 `aes-128-gcm` PSK 长度修正(16 字节)**。
- **前端列表 / 图表请求竞态守卫**(last-wins);编辑提交部分失败后刷新;**trusted_proxies
  安全配置建议**(推荐填反代 IP)。

### 复查留档
多轮多 agent + 人工核实:JWT / SSO / 审计(只记请求体、递归脱敏)/ 授权分组无越权或
泄密;加密 AES-GCM 无 nonce 复用;`crewjam/saml v0.4.14` 无已知 CVE;已据官方 v3.0.2
API 文档核实 **3X-UI v3.0.2 兼容**(client 操作仍在 `/panel/api/inbounds/*`)。

详细分条记录见下方 v3.3.0-beta.1 ~ beta.9。

## v3.3.0-beta.9 — 2026-05-21

### Added
- **用户页「服务器状态」标签**:用户能看到自己订阅里各节点的可用性——名称 + 地区 +
  粗状态(正常 / 离线 / 未知)+ 最后检查时间,方便自助判断「是节点挂了还是我的问题」。
  数据来自已有的节点健康探测,通过新的用户侧端点 `GET /api/user/me/server-status`
  下发,**严格脱敏**:只给该用户(按 ownership 解析)自己 group 的节点,且把内部
  `HealthState` 坍缩成三档(`ok`/`down`/`unknown`)——不暴露失败位置(panel 不可达 vs
  inbound 缺失)、不含 `HealthDetail` 错误串、不含面板宿主机 CPU/内存(那是 admin 专属)、
  不含 panel URL / inbound ID / 其他 group 的节点。跳过管理员停用的节点。有单测锁住
  「三种失败态都坍缩成 down」这条不变式。
- **用户页改为标签布局**:随着用户页内容变多,引入页内 Tab(复用 `useTabParam`,`?tab=`
  可深链,与后台风格一致)。当前分「概览」(原有全部内容)与「服务器状态」两个 tab;
  身份头部常驻 tab 之上。后续可继续把概览拆成更细的 tab。

## v3.3.0-beta.8 — 2026-05-21

前端复查后的一致性修复(后端经审查确认无授权/IDOR/审计泄密问题,无需改动)。

### Fixed
- **前端列表/图表请求竞态(stale-render)**:除 DashboardView 外,TrafficView /
  UsersView / LogsView / MeView / NodesView 的数据加载都没有 abort / 序号守卫——快速
  切 tab、翻页、改筛选、联动 snap 时,慢的旧请求后到会盖掉新结果,界面显示与当前
  选项对不上(下次交互自愈,不损坏数据)。给每个独立 loader 加 `useRef` 序号「last-
  wins」守卫(对 effect 与事件处理器都生效),只接受最新一次请求的结果。涉及
  TrafficView(rank/history)、UsersView(列表 + usageMap)、LogsView(sub/audit/email
  三个 tab)、MeView(trend)、NodesView(unmanaged 切面板)。
- **UsersView 编辑提交部分失败后不刷新**:保存是 `updateUser → setUserTraffic →
  setEnabled` 串行,中途失败则前面已落库、报错但表格不刷新,显示停留在编辑前的旧值。
  改为失败时也 `load()` 重新拉取,让行反映真实的「部分成功」状态,对话框保持打开可重试。

### 复查留档
- 后端授权分组(adminGroup vs staffGroup)、普通用户自助端点(无 IDOR,全用 JWT 的
  user id)、审计中间件(只记请求体不记响应体、递归脱敏覆盖 password/token/uuid/
  key_pem 等)、安全头、body-limit、group/config/seed/migrate 均经审查确认无问题。
- 已知小项(未改):LogsView 的保留天数保存是「读取全量 settings → 整体回写」,已在
  保存前 re-fetch 缓解,残留一个极小的并发写窗口;彻底消除需后端加一个仅更新该字段
  的局部端点。

## v3.3.0-beta.7 — 2026-05-21

### Fixed
- **流量历史小时桶随时间静默缩水**:raw 快照按 `now-7d`(非整点)删除,某小时桶横跨
  删除点时会先被删掉最早的几条原始行;而 rollup 每轮全表重扫 + 无条件 upsert
  覆盖(`MAX-MIN`),于是下一轮用残留行算出偏小的 delta 覆盖掉已正确的 hourly 值——
  超过 7 天的历史小时每个少算约「一个 5 分钟快照间隔」的量。改为把 raw 删除点
  **按 UTC 整点对齐**(`hourAlignedCutoff`),整小时删除、永不残缺,rollup 始终算完整
  小时,delta 稳定。只影响长周期流量图,不影响计费 / 配额(那些走 user 行 lifetime
  计数)。有单测覆盖。

### 复查留档(本版未改,经只读核实)
- **限流 IP 来源**:`http.trusted_proxies` 空配置默认信任所有代理(`0.0.0.0/0`)——
  **若面板端口直接对公网暴露**,`X-Forwarded-For` 可被伪造以绕过 per-IP 限流并污染
  审计 / 订阅日志的 IP。属注释写明的「假设端口不公开直达」的零配置默认。**部署建议**:
  把 `http.trusted_proxies` 设为反代 IP(无反代则设 `none`)。
- 前端认证刷新(单飞 + 防递归重放)、sync-task 重试(单 goroutine、崩溃靠
  `ResetRunning` 回收、每目标至多一个活跃任务)经核实无并发 / 风暴问题。

## v3.3.0-beta.6 — 2026-05-21

针对节点 / 邮件 / 流量三块「易出问题」子系统的一次深度复查后的修复(多 agent 审查
+ 逐条人工核实,纠正了 agent 的几处过度结论)。

### Fixed
- **流量自动停用 / 周期自动恢复不发邮件**:traffic poll 跑满配额自动停用、新周期
  自动恢复都只调 `SetEnabledAndSync`,而它从不发信——导致精心准备的
  `traffic_exhausted`(流量用完)和自动 `account_enabled`(周期恢复)两套模板形同
  虚设,用户全程收不到通知。把 mailer 以接口(`MailNotifier`,late-bound)接进
  traffic poll 的这两个转换点,异步发信(SMTP 不阻塞轮询),边沿触发(每次转换发
  一封,不会每轮重发)。
- **`SetPeriodUsage` 漏写 `PeriodBaselineBytes`**:管理员「设定本期已用流量=X」后,
  v3 的 `PeriodUsed()=Lifetime-PeriodBaselineBytes` 不等于 X,导致仪表盘显示和
  **下一轮 poll 的自动封禁判定**都用错值(会推翻它当场设的启用状态)。补设
  `PeriodBaselineBytes = Lifetime - X`(夹 ≥0)。
- **每个 poll 周期可能误清并发授予的应急访问**:`UpdateTrafficState` 之前连
  `emergency_until` / `emergency_baseline_bytes` 一起写,而 poll 用的是周期开头加载
  的旧用户快照——任意一轮普通 poll(非仅月度重置)都可能用旧值覆盖掉用户刚通过
  `UseEmergencyAccess` 并发授予的应急窗口(还白扣一次次数)。把这两列从
  `UpdateTrafficState` 移除(poll 不拥有它们),新增 `ClearEmergencyAccess` 由重置 /
  配额耗尽路径在应急锁内显式调用。有 SQLite 集成单测覆盖「旧 poll 写不再覆盖应急」
  与「显式清理生效」。

### Changed
- **同一 inbound 的并发 read-modify-write 加进程内写锁**:`UpdateClient` /
  `UpdateInbound` 是「GET 整个 inbound → 改 clients[] → 整体 POST 回」;traffic poll
  与 reconcile 直接并发调用时,两者基于同一快照写回会丢失对方的修改(lost update,
  下一轮自愈,但表现为「刚改的没立刻生效」)。给 `xui.Client` 加 per-inbound 写锁串
  行化(`AddClient` 走服务端 merge 端点,不受影响、无需加锁)。
- **`account_disabled` / `account_enabled` 邮件加分钟级去重**:这两类原先无任何幂等,
  双击 / 快速重试会重发。按 `(用户, 原因, 分钟)` 去重,既挡掉意外重复,又让真正的后续
  状态变更仍能通知;SMTP 失败的重试(分钟之后)照常发送。

### 复查纠正(留档)
- 子 agent 称「邮件正文 `text/template` 渲染 + `ClientName` 来自 User-Agent = HTML 注入
  面」——经核实 `ClientName` 是**管理员配置的检测族名**(或字面量 `other`),非原始
  UA,**无匿名注入面**;仅属「该用 html/template / 转义」的卫生项,本版未改。

一次借助多个子 agent + 外部规范核查的全量复查,只查出一条实锤功能 bug。

### Fixed
- **SS-2022 对 `2022-blake3-aes-128-gcm` 派生的 PSK 长度错误**:`DeriveProxyPassword`
  对所有 SS-2022 cipher 一律返回 `base64(SHA-256(uuid))` = 32 字节,但
  [SIP022](https://shadowsocks.org/doc/sip022.html) 要求 `aes-128-gcm` 的 PSK 必须
  是 **16 字节**(`aes-256-gcm` / `chacha20-poly1305` 才是 32)。结果:任何用
  `2022-blake3-aes-128-gcm` 的 inbound,写给 3X-UI 的凭证和渲染出的订阅两端都是错
  长度,Xray 以 "bad key length, required 16" 拒绝,该节点对所有用户连不上。
  修法:把 inbound 的 SS method 串进凭证派生链路(`DeriveProxyPassword` 新增
  `ssMethod` 参数,按 cipher 截断到 16/32;sync 写入、render、reconcile 三条产出
  credential 的路径同步传入),`aes-256-gcm` 行为不变。有单测覆盖三种 cipher 的
  PSK 字节长度 + buildClientSpec 端到端长度。

### 复查确认无问题(留档)
JWT(算法固定 / TokenVersion 撤销)、SAML/OIDC(签名 / replay / nonce / state /
零隐式 fallback)、AES-GCM 加密(随机 nonce、无复用)、流量 `monotonicDelta`、
3X-UI v2.x API 对接、三套订阅渲染对各协议(Reality/Hysteria2/SS-2022 URI/transport)
的字段名均符合官方 schema。`crewjam/saml v0.4.14` 恰为修复
[CVE-2023-45683](https://nvd.nist.gov/vuln/detail/CVE-2023-45683) 的版本,不受任何
已知 CVE 影响。

### 已知 / 待办(非本版改动)
- 默认客户端关键词里 V2RayN 族的裸 `v2ray` 子串偏宽(会顺带匹配 `v2rayA`/`v2rayU`
  等),低优,暂不动。

> 注:复查中一度记入的「3X-UI v3.x 重构 API 致本面板 404」为误报——生产环境本就
> 跑 v3.x 且 adapter 正常工作,已据官方 v3.0.2 API 文档核实端点一致,特此更正。

## v3.3.0-beta.4 — 2026-05-21

修复 + 加固一批,主要来自一次全量内部审查。

### Fixed
- **邮件模板 `blocked_client` 标签页点了没反应**:模板 tab 的 URL 白名单
  (`useTabParam` 的 `allowed`)漏列 `blocked_client`,点击后回退成默认 tab,永远
  切不过去。补上即可。
- **封禁计数被轮询客户端刷高导致误停用**:代理客户端按定时器轮询订阅,旧逻辑每次
  被禁拉取都 `BlockViolationCount++`,被动轮询就能堆到自动停用阈值、锁死一个从没
  动过手的用户。新增 `LastBlockViolationAt`,每用户每 10 分钟最多记一次违规
  (一次拉取突发 ≈ 一次违规);窗口内的被禁拉取不再写库,顺带减少热路径 DB 写。

### Changed
- **被禁客户端邮件提醒热路径优化**:`SendBlockedClientWarning` 改为由调用方
  (sub handler)用**已加载的** `SubBlockNotifyUser` 提前 gate——功能默认关时连
  goroutine 都不再 spawn,省掉原先「先查 mail_settings + 全 KV 表扫一遍才发现没开」
  的两次无谓 DB 读;UI 设置直接透传进去,函数内不再二次加载。
- **被禁邮件每日上限改为 insert-first 防并发超发**:并发被禁拉取原会都读到同一
  已发计数 → 都发 → 在同一 windowKey 上 `OnConflict DoNothing` 吞掉第二条(超发且
  计数不增)。新增 `MailRepo.ReserveSentSlot`:发信前先原子占位,只有抢到名额者才
  发(抢不到则静默,宁可少发不可多发),有单测覆盖。
- **日志三个标签页搜索翻页修正**:提交搜索时若不在第 1 页,旧逻辑会用旧页号先多发
  一次请求并闪一下错页;且翻页用的是输入框里**未提交**的词。改为 `appliedSearch`
  与输入分离——提交只重置页码 + 应用关键词,由单个 effect 统一驱动一次重载。

## v3.3.0-beta.3 — 2026-05-21

### Added
- 滥用保护新增「被禁客户端邮件提醒」:当用户用被禁 / 不受支持的客户端拉订阅、但
  尚未触发自动停用阈值时,给用户发一封提醒邮件(告知换用推荐客户端)。**默认关**;
  开启后受「每天最多 X 条 / 用户」上限约束(默认 1),避免客户端轮询导致邮件轰炸;
  与自动停用互斥(达到阈值时只发停用邮件)。新增可编辑 / 预览的 `blocked_client`
  邮件模板(`ListTemplates` 对缺失 kind 用内置默认兜底,已有部署无需迁移即可用);
  去重用 mail_sent 的 windowKey(`日期#序号`)+ 新增 `CountSentInWindow` 计数,
  有单测覆盖。

## v3.3.0-beta.2 — 2026-05-21

### Added
- 日志三个标签页(审计 / 订阅访问 / 邮件)各加一个模糊搜索框:
  - 审计原来的「操作者 / 动作」两个**精确匹配**框合并为一个关键词框,模糊匹配
    操作者 / 动作 / 对象;
  - 订阅访问搜 IP / UA / 客户端 / 用户(UPN / 显示名);
  - 邮件搜 收件人 / 类型 / 用户。
  后端 filter 加 `Search`(不区分大小写的跨列 `LOWER(...) LIKE`,join users 以搜
  UPN / 显示名,SQLite / PostgreSQL / MySQL 通用),audit repo 搜索有单测覆盖。

## v3.3.0-beta.1 — 2026-05-21

把订阅客户端配置重构为统一的「检测族 → 导入 App」两层注册表，并在其上补齐错误页、
滥用保护的黑/白名单与若干修复（汇总自前期 4 个内部迭代）。

### Added
- **订阅客户端统一注册表**:两张独立表（检测规则 `sub_client_rules` + 一键导入
  `sub_import_clients`）合并为「检测族 → 导入 App」两层注册表（KV `sub_clients`）。
  检测族持有 UA 关键词、渲染格式与启用开关;族下的 App 是门户一键导入项并继承族的
  格式。**门户是否展示某 App 由「App 启用 且 所属族启用」派生**——关闭一个族会同时
  拦截该族拉取并隐藏其全部导入项,杜绝「已禁用却仍展示」。后台两个编辑器合并为一个
  嵌套编辑器（族下折叠 App）。
- **Abuse protection 客户端过滤模式（黑/白名单）+ 问号说明**:黑名单（默认 = 原
  行为）只拦被「禁用」的族、未识别放行;白名单只放行「已知且启用」的族、未识别 /
  未启用一律拦截（计入异常次数、可能触发自动停用),「其他」自动被拦无需显式列。
  封禁判断抽成纯函数 `clientdetect.ClientBlocked(mode, result)` 并单测覆盖。
- **全局错误页**:页面级渲染崩溃不再落到 react-router 内置开发页;根路由挂
  `errorElement` 复用友好界面,带可折叠「查看详情」(完整 message + stack + 组件栈)
  与一键复制。App 级 React ErrorBoundary 保留兜非路由错误。

### Changed
- 导入 App 不再单独存渲染格式（冗余且前端未用）——格式只在族上设一次、服务端按 UA
  下发,二者天然一致。
- sing-box 族默认新增 `karing` 关键词。

### Fixed
- **Quantumult X 检测**:真实 UA 是 `Quantumult%20X/...`（空格是 `%20`），旧关键词
  `quantumult x` / `quantumultx` **都匹配不到**,QX 从未被正确识别;改为单个
  `quantumult`（已核对 subconverter 的 UA 匹配表）。注:此为默认值修正,线上已有
  配置需在后台把 QX 关键词手动改为 `quantumult`（或重置默认）。
- 客户端注册表编辑器在「仅检测、无 App」的族（Surge/Loon 等）上崩溃:`apps` 序列化
  为 JSON `null`（Go nil slice）,编辑器 `.length`/`.map` 抛错;载入设置时把 apps /
  keywords / platforms / recommended_for 一律规整为 `[]`。

### Removed
- 默认检测族移除 Surge / Loon / Surfboard:经核对官方文档,这三个 Surge 系客户端
  只认 Surge 专有 `.conf` 格式、面板不产该格式,**根本无法消费面板订阅**（一键导入
  或手动粘贴订阅链接都拿到无法解析的格式）;保留只会误导。未识别 UA 由 `Detect`
  兜底为 mihomo 且不拦截,移除无副作用。Quantumult X 保留（吃 Clash YAML、走
  mihomo）。

### Migration
- 升级时若仅存在旧的 `sub_client_rules` / `sub_import_clients`,首次加载会**自动**
  折叠为 `sub_clients`（检测规则建族、导入项按渲染格式 + 名字归到对应族），自定义
  配置不丢失。该一次性兼容代码隔离在 `internal/adapters/mysql/sub_clients_legacy.go`,
  连同两个 deprecated 字段计划在**下一个大版本（v4.0.0）删除**。

## v3.2.1-beta.6 — 2026-05-20

### Fixed
- 邮件设置「发送测试邮件」的按钮与输入框未对齐：按钮改为拉伸至输入框高度，兼容
  紧凑（small）与舒适（medium）两种密度，不再用固定高度只配其一；校验错误提示
  改为脱离文档流（绝对定位），让按钮只跟随输入框本身、不被错误文字撑高。

## v3.2.1-beta.5 — 2026-05-20

### Fixed
- SMTP 发送：以发件域名（而非 net/smtp 默认的 `localhost`）作为 EHLO/HELO
  名称——更严格的中继（尤其是 Google Workspace 的 smtp-relay.gmail.com）会对
  非 FQDN 的 HELO 直接断开连接，表现为测试邮件报「EOF」。同时给各阶段错误加上
  前缀（`smtp greeting/helo/starttls/auth/...`），不再只弹一个无信息量的 EOF，
  便于定位是握手、STARTTLS 还是认证环节失败。

## v3.2.1-beta.4 — 2026-05-20

beta.1 实测反馈的收尾修复（beta.2 的「账户状态」开关样式突兀，已由下拉替代；
beta.3 后又补入个人规则提示的 i18n 修复）。

### Added
- 用户编辑弹窗加入「账户状态」下拉（启用 / 停用）：可在编辑表单内直接切换账户状态
  （走既有 setEnabled 接口，仅在状态变化时下发），停用时显示自动停用原因。样式与
  分组 / 角色 / 重置周期下拉一致；为防自锁，禁止在此停用自己的账户。

### Fixed
- 邮件 Logo 在实际收件箱裂图（预览正常）：此前未配置 Logo 时回退为内嵌
  `data:` base64 图，而 Gmail 等网页邮箱**屏蔽 `data:` 图片**——预览（浏览器）
  能渲染、真实邮件却裂图。改为始终输出可被邮件客户端抓取的绝对 http(s) 链接：
  管理员配置的 Logo 仅在解析为 http(s) 绝对地址时采用（跳过 `data:` / 相对路径），
  否则回退到公开静态资源 `{SubBaseURL}/images/logo+title-circle-darkmode.png`；
  无 SubBaseURL 时返回空、模板跳过 `<img>` 而非裂图。
- SSO 停用 / 待审核错误页（/sso-error）正文混语言：后端对 `account_disabled` /
  `account_pending` 硬编码中文 `description` 覆盖了前端 i18n，英文界面下标题英文、
  正文中文。改为不再下发 description，由前端按语言渲染。
- 编辑弹窗里 SSO 徽章（SAML/OIDC）字体与角色 / 状态徽章不一致、整体偏上：
  统一字号 / 字重 / 垂直内边距并居中对齐。
- 用户门户保存个人规则失败时的提示混语言：管理员关闭自助编辑时后端返回硬编码
  英文串，被全局错误 toast 原样弹出。改为该请求跳过全局 toast，由前端按语言渲染
  （403 → 「管理员已关闭个人规则编辑」，其余 → 通用保存失败）。

### Added
- 用户编辑界面重做为 Cloudreve 风格的左右双栏：左栏聚合身份信息（头像 / 角色 /
  SSO / 状态徽章、ID/UUID、流量用量条 + 本周期已用 / 上限 / Lifetime 总量及上下行
  明细、创建时间、复制订阅），右栏为可编辑表单网格。用户列表 DTO 新增
  `lifetime_{up,down,total}_bytes`（只读，永不被周期重置清零）。

### Fixed
- 流量上限与紧急访问每窗口配额现在接受小数 GB（>=0，如 0.3）。此前后端
  `TrafficLimitGB` / `EmergencyAccessQuotaGB` 是整型，提交 0.3 之类的值会被
  JSON 解析直接拒掉；改为 `float64` 并在转字节时 `int64()` 截断，KV 设置用
  `floatField` 序列化，前端数字输入框 `step="any"` 且校验放宽为仅「>=0」。

## v3.2.0 — 2026-05-20

正式版。汇总 v3.2.0-beta.1 → rc.2 的全部改动（PostgreSQL 支持、到期日按面板
时区锚定、未纳管标签页按服务器查询、Shadowsocks 订阅 SIP022 修正、VLESS flow
统一、Hysteria2 多用户同步、依赖 CVE 升级等），并在 rc.2 之后补入下列收尾修复。
完整逐项见下方各 pre-release 段落。

### Security
- operator 角色的前端按钮门控收口：operator 能进的页面（节点 / 分组 / 规则库 /
  配置方案 / 日志 / 同步任务）上，admin-only 的写操作按钮（新建 / 编辑 / 删除 /
  导入 / 认领 / 清空 / 清理保留期 / 清理任务）以及对 admin/operator 账户的删除 /
  启停 / 重置 / 解绑等，现在对 operator **直接隐藏**，不再"有按钮、一点报错"。
  保留 operator 实际有权的操作（普通用户 CRUD、流量、节点启停、任务重试/取消）。
  新增前端能力层 `web-react/src/utils/permissions.ts`（`useCan(capability)`）作为
  自定义角色 / 细粒度权限的预留扩展点；详见 docs/ARCHITECTURE.md §6.3。

### Fixed
- TLS `allowInsecure` 现在会渲染进客户端配置：Clash 的 `skip-cert-verify`、
  sing-box 的 `insecure`、URI 的 `allowInsecure=1`，Hysteria2 也接上。此前面板
  能在创建节点时勾选 allowInsecure，但订阅完全不下发，自签证书的 TLS 节点客户端
  会因校验失败而连不上。
- 客户端删除统一走 `delClientByEmail`：3X-UI 的 `delClient/:id` 只认 UUID /
  password（VLESS/VMess 按 UUID、Trojan 按 password），不认 Shadowsocks(email)
  / Hysteria2(auth)，导致 SS 客户端按存储的 UUID 删除时报 “Client Not Found In
  Inbound For ID”、用户重同步的 DEL 任务无限重试。改为始终按 email 删除，对所有
  协议生效（取代 rc.1 的 by-id+回退方案）。
- 修正 `copyClients` 的请求字段：3X-UI 读 `clientEmails`，面板此前发的是
  `emails`（被忽略），会把"选择性复制"静默变成"复制全部"。

## v3.2.0-rc.2 — 2026-05-20

### Fixed
- Hysteria2 多用户同步打通：3X-UI 按 `auth` 字段识别 Hysteria2 客户端（auth 即
  其「client id」，为空会被拒「empty client ID」），但面板此前的 `ClientSpec` /
  `buildClientSpec` / `buildClientJSON` 完全没有 `auth`，同步出去的 Hysteria2
  客户端没有凭证、3X-UI 拒收或无法认证。现在 `ClientSpec` 新增 `Auth`，
  Hysteria2 客户端的 `auth` 设为用户 UUID（与订阅渲染用的 HY2 密码一致），
  序列化与回读都带上。VLESS/VMess/Trojan/SS 不受影响；删除走 email 路径已覆盖。

## v3.2.0-rc.1 — 2026-05-20

### Fixed
- 同步删除（Shadowsocks）：删 SS 客户端时按 settings 的 `id`(UUID) 调
  `delClient` 会被 3X-UI 拒为 “Client Not Found In Inbound For ID”，导致用户
  重同步的 DEL 任务无限重试（观察到 131 次）。现在 id 删失败后回退到
  `delClientByEmail`（3X-UI 跨协议稳定的删除键），删成功即移除归属、任务正常
  完成。VLESS / VMess / Trojan 的按-id 删除原本就有效，不受影响。
- 用户门户「本周期用量」旁的重置周期显示成原始 key `reset_period.monthly`：
  MeView 漏了 `profile.` 命名空间前缀，已修正。
- 创建 / 续期用户的到期口径与编辑统一：创建表单改为日期选择器（发
  `expire_date`，后端按面板时区 end-of-day 解析）；续期发的裸 `expire_at` 也
  锚定到面板时区当天结束。此前创建 / 续期按“现在 + N 天”的浏览器钟点存，跨
  时区会与编辑显示差一天、且在当天钟点而非当天结束过期。
- PostgreSQL 列表 / 图表排序补确定性 tiebreaker（`id`）：审计 / 订阅访问 /
  已发邮件分页此前按非唯一时间戳排序，PG 上同值行跨页可能重复或漏；流量图表
  分桶取“桶内最后一行”在 tie 时也不确定。
- SQLite 连接池上限改为 1（写串行化），消除高写竞争下的 “database is locked”；
  MySQL / Postgres 保持原有连接池。

### Security
- 升级存在可达 CVE 的依赖：`golang-jwt/jwt` v5.2.2（JWT header 解析内存放大
  DoS）、`golang.org/x/net` v0.53.0、`russellhaering/goxmldsig` v1.6.0。
  govulncheck 复查 0 命中。

### Internal
- 清理 staticcheck（U1000）标记的未使用代码：多处 `toDomain`、legacy 行类型、
  `settingsClients`、`panelName`、`isInboundNotFoundErr`、`activeLoginMode`。

## v3.2.0-beta.2 — 2026-05-19

### Fixed
- 节点「未纳管」标签页的服务器选择标签 / 占位、空状态提示、加载失败提示在
  英文界面下显示为中文：相关 i18n 键此前 zh-CN / en-US 都缺失，退回了中文
  兜底。已补齐两个 locale。

## v3.2.0-beta.1 — 2026-05-19

### Added
- **PostgreSQL 支持**：除默认 SQLite / MySQL 外新增 PostgreSQL（pgx 驱动）。
  用 `postgres:` 块的离散字段（host/port/user/password/database/sslmode）配置，
  或在 `mysql.dsn` 填 `postgres://` URL；环境变量 `PSP_POSTGRES_DSN` 可覆盖。
  建表仍由 GORM AutoMigrate 按方言生成，无需迁移脚本。
- 当浏览器时区 ≠ 面板时区时，管理端 / 用户端在到期日处显示小提示，说明该
  日期以哪个时区为准，消除歧义。

### Changed
- 节点「未纳管」标签页改为先选服务器再查询，仅请求所选面板的 inbound（一次
  `ListInbounds`），不再每次加载同步扫描全部 3X-UI 面板；某个面板慢或不可达
  时错误只限于所选服务器并提供重试，进入标签页前不再访问任何面板。

### Fixed
- 管理员用户搜索改为大小写不敏感（`LOWER(col) LIKE`）。此前 PostgreSQL 的
  `LIKE` 大小写敏感，搜 “john” 匹配不到 “John”；SQLite / MySQL 不受影响。
- 管理端设置 / 显示的到期日改为**按面板时区**解释与渲染。此前编辑弹窗用
  `new Date("YYYY-MM-DD")`（按 UTC 解析）再 setHours 本地小时，UTC 以西的
  时区会让到期早一天。用户端到期仍按浏览器本地时区显示（设计如此）。
- 审计 `before_json` / `after_json` 列由 `json` 改为 `text`：审计会写入空
  字符串（新建无 before 状态），而 PostgreSQL 的 json 列拒绝空串。

## v3.1.1-rc.3 — 2026-05-19

### Fixed
- 编辑节点弹窗的 `Flow` 字段现在仅对 VLESS 节点显示（此前对 SS / VMess /
  Trojan / Hysteria2 也显示）。为支持这点，节点新增缓存 `protocol`（schema
  加列，AutoMigrate 自动添加、无需 backfill）：import / create 时写入，编辑
  inbound 时回填，列表 / 详情 API 带出。已有旧节点 protocol 为空时按「未知」
  处理、仍显示 Flow，下次重新 import 或编辑 inbound 会自愈。

## v3.1.1-rc.2 — 2026-05-19

### Added
- 新建节点 / 导入 inbound 弹窗的 `Address` 字段现在按所属 3X-UI 服务器的
  URL 主机名预填（仅取 hostname，丢弃 scheme / 管理端口 / 路径）。这是
  可编辑的默认值——切换服务器时若地址未被手动改过会跟着更新，手动改过则
  保留。

### Fixed
- 订阅 URI 列表里 SS-2022（`2022-blake3-*`）的 `ss://` 链接拼接修正为
  SIP022 形式 `ss://method:serverPSK:userPSK@host:port`，PSK 内的 base64
  特殊字符（`+ / =`）走 percent-encoding；不再把整段 userinfo 用标准
  base64 包装。旧拼法会让 sing-box / shadowsocks-rust / Shadowrocket 无法
  解析 2022 节点。普通 SS 仍保持 SIP002 的 base64url userinfo。
- 统一 VLESS `flow` 的渲染：Clash / URI / sing-box 三种订阅一律按节点存储的
  flow 原样输出，空就留空。此前 Clash / URI 在 REALITY 且 flow 为空时会擅自
  补 `xtls-rprx-vision`，与显式选"无"、ws/grpc 传输或纯 reality 服务端冲突，
  且和 sing-box（一直按原值）行为不一致。

### Changed
- 导入 inbound 弹窗：`Flow` 选择器仅在源 inbound 为 VLESS 时显示，
  SS / VMess / Trojan / Hysteria2 不再出现该字段，提交时也不会为非 VLESS
  协议写入 flow。

### Maintenance
- `go mod tidy`：`coreos/go-oidc/v3`、`golang.org/x/oauth2` 从 `// indirect`
  归类为直接依赖（它们被 OIDC 登录代码直接 import），消除 go.mod 过期标记。

## v3.0.0 — 2026-05-18

正式版。基于一系列 V3 发布前的代码审查（后端 / 安全 / 前端 / DB / 构建 /
测试），完成了进程稳定性、HTTP 基础安全、数据完整性和发布卫生四个方向的硬化。

### Breaking
- 数据库 schema 重构（KV `settings` / `xui_clients` → `user_xui_clients` /
  retention 字段重组）。详见 [docs/UPGRADE-v3.0.0.md](docs/UPGRADE-v3.0.0.md)。
  老库由 `psp migrate` 子命令旁车迁移；主程序不会原地升级。
- 移除根目录历史构建残留（`migrate-db-v2.exe`、`reset-admin-password.exe`、
  `material-demo.html`、`user_*.yaml`），构建产物统一在 `local-build/`。

### Security
- HTTP 全局加入 HSTS / X-Frame-Options / X-Content-Type-Options /
  Referrer-Policy / 最小 CSP。
- `http.Server` 加 `ReadTimeout`、`WriteTimeout`、`IdleTimeout`、
  `MaxHeaderBytes`；请求体硬限 1 MiB，避免 audit 中间件的 `io.ReadAll` 被
  恶意大 body 打 OOM。
- SSO 注销时清 cookie 的 `secure` 标志与设置时一致 — 修复 HTTPS 部署下旧
  cookie 不被实际删除导致 access token 仍在 TTL 内可用的问题。
- JWT 加入 `tv` token_version claim；管理员停用 / 改角色 / 重置密码 /
  用户自助改密都会 bump 版本号，立即吊销旧 token。
- SAML Assertion 重放缓存：同一个 Assertion ID 在 `NotOnOrAfter` 窗口内
  只接受一次。
- SAML IdP metadata 获取加 15 s 超时 + 4 MiB body 上限，挡掉 SSRF/DoS。
- `isHTTPS()` 只在请求来自受信代理时才信任 `X-Forwarded-Proto` /
  `X-Forwarded-Ssl`，避免攻击者通过伪造头降级 cookie 安全标志。
- 启动时审计 `xui_panels`、`saml_settings`、`oidc_settings`、`mail_settings`
  里的密文列；存在明文行时 WARN 提示设 `PSP_SECRET_KEY_MATERIAL` 并在 UI
  重新保存。

### Reliability
- 新增 `internal/pkg/safego`：所有后台 goroutine（traffic / reconcile /
  health / mail / sync / audit cleanup / saml metadata refresh / 异步邮件 /
  扇出 worker）全部包 recover。任一 panic 不再打死整个面板。
- `App.Shutdown` 用 `sync.WaitGroup` 等后台 worker 退出，按 caller 设定的
  deadline 限时。
- mailer SMTP 拨号 ctx 超时时启动 reaper goroutine 收尾，不再泄漏 TCP/TLS
  连接。
- handler 异步邮件改用 `AsyncDispatcher` 接口，在面板 bg 上下文派生 —
  停服时干净退出，不再悬挂在 `context.Background()` 上。
- `render` 订阅渲染做 inbound 批量预取（每个 3X-UI 面板一次
  `ListInbounds`），消除原先每节点单独 `GetInbound` 的 N+1。
- `emergencyMu` 现在也覆盖 traffic poll 写入 `EmergencyUntil` /
  `EmergencyBaselineBytes` 的路径，封堵与 `UseEmergencyAccess` 的竞态。

### Data integrity
- 新增 `sub_logs` 自动 prune cron（接 audit cleanup loop），不再无界增长。
- `XUIPanelRepo.Delete` 加级联保护：仍有 nodes / user_xui_clients 引用时
  拒绝删除，避免幽灵外键。

### Frontend
- axios 401 拦截器接入 `/auth/refresh` 单飞重试，access TTL 过期不再粗暴
  踢回登录页。
- axios 错误差异化：网络异常 / 超时 / 5xx / 429 / 4xx 文案分离；同一错误
  在 1.5 s 内去重，避免 `Promise.allSettled` 批量失败刷屏 toast。
- 剪贴板新增 `document.execCommand` 回退路径，HTTP 部署也能复制订阅 URL；
  失败时 warning toast。
- `useAuthStore` 加 `hasToken` 字段 + `storage` 事件监听：多 tab 登出
  会同步触发其他 tab 的路由守卫重新评估。
- i18n：补齐 `nodes.field.{enabled,separator_*}` 等 4 个 zh-CN 缺失 key。
- 新增 `common.errors.{network,timeout,server,rate_limited,copy_*}` 文案。

### Operations
- 新增 `internal/version`；启动日志、`psp version` 子命令、`/api/version`
  端点共用 build identity。CI 用 `-ldflags="-X .../version.Version=..."`
  注入 release tag。
- systemd unit：`ReadWritePaths` 补 `/opt/psp/config`（修首启写不进
  config.yaml 的 P0），加 `StandardOutput=journal`、`MemoryMax`、
  `SystemCallFilter`、`RestrictAddressFamilies` 等硬化项；新增
  `deploy/systemd/env.example`。
- backend `POST /api/auth/refresh` 端点（与登录共用 PerIP 限流）。
