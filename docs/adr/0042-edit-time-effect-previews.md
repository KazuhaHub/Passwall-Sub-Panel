# ADR 0042：在编辑处显示匹配结果的预览

- 状态：已接受，已实现（2026-11-16，所有者批准实现）；自动化验证通过，已同步手测环境，见文末“实现与验证”
- 关联：[ADR 0041](0041-line-visibility-by-group-filter.md)（首个此类预览）、
  [ARCHITECTURE §6.3、§6.4](../ARCHITECTURE.md)、[后台编辑保存后的状态同步](../admin-edit-state.md)

## 背景

ADR 0041 在线路的 Region / Tags 旁显示“可见分组”：管理员修改的值要经过别处的规则匹配才产生效果，
编辑时看不到结果，于是由服务端按草稿实时计算并就地显示。项目中还有几处相同情形，其中修改分组的
`tag_filter` 会在保存后为全部成员重新开通与挂载，直接写入 3X-UI（`ResyncGroupMembersInBackground`），
而当前编辑器没有任何预览。

ADR 0041 的实现也暴露两个需要先统一的问题：预览接口是“带请求体的读”，审计豁免靠逐个手写路径匹配，
其中一个匹配函数曾因前后缀重叠越界；前端防抖、取消、“无”与“未知”的区分写在单一用途的 hook 里。

## 决策

### 0. 预览的共同约定

以下约定适用于本 ADR 的全部预览，也适用于 ADR 0041 已有的线路可见性预览：

1. **单一实现**：结果由服务端调用保存与渲染所用的同一函数计算（`group.Service.Eligible`、`NodesFor`、
   `SubscriptionNodesFor`、`SeparatorEntry.VisibleForNodes` 等），前端不复写匹配逻辑。
2. **按草稿计算**：请求携带编辑器中未保存的值，其余取已存记录；不写库、不写审计、不缓存结果。
3. **“无”不等于“未知”**：空列表只表示没有匹配；读取失败返回错误，前端显示“暂时无法计算”；
   响应缺少字段或行数不符视为读取失败，不得显示为“无”或“全部”。
4. **只提示，不阻止**：预览结果为空或有减少时以提示色显示，从不阻止保存。
5. **权限对等**：预览接口所在路由组与对应写接口相同；对写接口有目标限制的（运营人员只能编辑普通用户），
   预览施加同样的限制。
6. **不访问远端**：只读本地数据与既有缓存（节点列表缓存、按面板的 allowlist 资格缓存），不连接 3X-UI 或节点。

### 1. 分组：`tag_filter` 旁显示匹配节点

- 接口：`POST /api/admin/groups/node-preview`（新建）与 `POST /api/admin/groups/:id/node-preview`（编辑），
  请求为草稿 `tag_filter`；allowlist 配置取已存分组（新建分组视为未启用）。
- 返回：匹配且有资格的节点；匹配 `tag_filter` 但因 allowlist 资格被排除的节点及原因码；每个匹配节点按
  ADR 0041 可见的线路数；编辑已有分组时另返回相对已存条件新增与移出的节点，以及成员数。
- 编辑器显示匹配节点数量与名称；条件变化影响已有成员时，提示“保存后将为 N 名成员重新同步节点”，
  说明会写入上游面板。

### 2. 节点：自身 Region / Tags 旁显示可见分组

- 复用 `POST /api/admin/nodes/:id/line-visibility` 已返回的 `node_groups`；响应新增按已存节点值计算的
  `saved_node_groups`，用于显示“保存后将移出的分组”。
- 节点不属于任何分组时以提示色说明，它不会出现在任何订阅中，也不会为任何用户开通。

### 3. 用户：改分组时显示将获得的节点

- 接口：`POST /api/admin/users/:id/group-preview`（`staffGroup`），请求为目标 `group_id`；施加与
  `ensureOperatorAllowed` 相同的目标限制。
- 返回：目标分组与当前分组下 `SubscriptionNodesFor` 的差异（新增、移出的节点及各自可见线路数）。
- 用户编辑器在分组选择下方显示差异；只读，不预先开通。

### 4. 分隔标题：“按节点显示”时显示会出现的分组

- 接口：`POST /api/admin/nodes/separator/group-preview`（`adminGroup`），请求为草稿 `mode` 与 `node_ids`。
- 对每个分组以其 `NodesFor` 的节点 ID 调用 `SeparatorEntry.VisibleForNodes`，与渲染一致；全局模式返回全部分组。
- 编辑器在节点选择下方显示“会出现在：分组 A、分组 B”，无分组时提示。

### 5. 审计豁免改为路由级标注

- 新增路由中间件 `middleware.AuditReadOnly()`，在请求上下文中标记“只读预览”。`AuditWrites` 仍在处理前
  捕获请求体，但在 `c.Next()` 之后检查该标记，存在则不写审计。
- 预览路由统一通过一个注册辅助函数登记，辅助函数同时把方法与路由模板记入清单；测试以固定清单逐项点名，
  新增豁免必须修改该测试，保留“豁免需显式决定并在测试中点名”的原则。标注只允许用于 POST。
- 迁移现有豁免：`destPreviewPaths` 与 `isLineVisibilityPreviewPath` 改为路由标注后删除，
  `shouldAuditPath` 不再含任何预览特例。

### 6. 前端统一预览组件

- 抽出 `useDraftPreview(key, fetcher)`：输入暂停后请求、取消过期请求、不进查询缓存，返回
  `idle | loading | ready | error`，保留上一次结果直到新结果到达。
- 抽出 `PreviewCaption`：统一显示“列表 / 无（提示色）/ 暂时无法计算”，加载中不闪烁空状态。
- 各接口的 API 函数在边界规整响应，格式不符即抛错（沿用 ADR 0041 线路可见性的做法）。
- `useLineVisibility` 与 `LineLabelsFields` 迁移到上述组件，行为不变。

### 实施顺序

1. 共用基础：路由级审计标注与迁移、前端 `useDraftPreview` / `PreviewCaption` 与线路可见性迁移；
2. 节点可见分组（第 2 条）与分组匹配节点（第 1 条）；
3. 用户分组差异（第 3 条）与分隔标题分组（第 4 条）。

每步独立验证并同步手测环境；第 1 步不改变任何可见行为。

## 后果

- 编辑时按输入暂停发出只读请求，仅管理员与运营人员的编辑页面会触发；计算只读本地数据和既有缓存，
  成员数读取一次计数查询。
- 预览结果与保存后的实际结果使用同一函数，界面所见即保存后所得；保存前后其他管理员修改了节点或分组时，
  预览可能过时，以保存时为准。
- 审计中间件对预览路由仍会读取请求体再丢弃，开销与当前相同。
- 分组预览中“将重新同步 N 名成员”是对既有行为的说明，不改变保存流程。

不能破坏的不变量：

1. 预览不写库、不写审计、不访问远端；
2. 预览与保存、渲染使用同一实现，前端不复写匹配规则；
3. “未知”不得显示为“无”或“全部”；
4. 预览从不阻止保存；
5. 预览接口的权限与目标限制不宽于对应写接口。

## 考虑过但未采用

- **前端自行计算**：需要复写 `tag_filter` 与线路可见性规则，且 allowlist 资格依赖服务端的面板与节点状态，
  前端无法得知。
- **只在保存后报告结果**：分组条件保存即触发对上游的重新同步，事后报告来不及。
- **在列表页为每一行显示匹配结果**：请求量与列数增加，信息噪音大；需要时再单独评估。
- **用查询缓存保存预览结果**：草稿随输入变化，缓存命中率低且容易显示过时结果。
- **预览为空时阻止保存**：分组与节点常常分步配置，阻止会妨碍正常操作；与 ADR 0041 第 5 条一致。
- **继续逐个手写豁免路径**：每增加一个预览就多一个匹配函数，ADR 0041 中已出现越界缺陷。

## 实现与验证（2026-11-16）

实现位置：

- 审计：`middleware.AuditReadOnly`；`AuditWrites` 在 `c.Next()` 之后检查标记。`router.go` 的 `previewPOST` 是唯一挂载入口，
  `preview_routes_test.go` 读取源码，逐项点名全部 7 个预览路由，并禁止在 `previewPOST` 之外直接使用标记。
  `destPreviewPaths` 与 `isLineVisibilityPreviewPath` 已删除；原有的端到端测试（`dest_route_test.go`）继续证明访问控制预览不入审计。
- 服务端：`internal/service/group/previews.go`。
  - `GroupsForNode`
  - `PreviewGroupNodes`：`Excluded` 原因码为 `allowlist_unenforceable`。
  - `PreviewGroupChange`：线路数按各自分组计算。
  - `GroupsForSeparator`：按已启用计算。
- 接口：
  - `POST /api/admin/groups/node-preview`、`/api/admin/groups/:id/node-preview`（管理员）；
  - `POST /api/admin/users/:id/group-preview`（`staffGroup`，施加 `ensureOperatorAllowed`）；
  - `POST /api/admin/nodes/separator/group-preview`（管理员）；
  - `line-visibility` 响应新增 `saved_node_groups`。
  - 节点 handler 的依赖接口更名为 `NodeEditorPreviewer`。
- 前端：
  - 抽出 `hooks/useDraftPreview.ts` 与 `components/PreviewCaption.tsx`。`PreviewCaption` 新增 `noneTone`：用户换组“节点不变”属于中性信息，不用错误色。
  - 线路可见性迁移至共用组件。
  - 节点编辑器显示节点自身的可见分组与“保存后将移出”；分组编辑器显示匹配节点、allowlist 排除与“成员将新增 / 移出节点”；用户编辑器在所选分组与当前不同时显示节点增减；分隔标题在“按节点关联”时显示会出现的分组。
  - 各 API 函数在边界校验响应，格式不符按读取失败处理。

发现但未处理：分组编辑器每次保存都提交 `tag_filter`，`AdminGroupHandler.Update` 只要请求中带有该字段即视为条件已变，
从而在条件未改时也为全体成员触发 `ResyncGroupMembersInBackground`。分组预览中“成员将新增 / 移出”的提示只在节点集合
实际变化时显示，本 ADR 未改变保存流程，该行为另行评估。（2026-11-16 更新：已另行修复，`AdminGroupHandler.Update` 改用 `group.SameFilter` 按匹配语义比较新旧条件，只有条件或额度真正变化时才重新同步成员，见 ARCHITECTURE §7.5 附近说明。）

验证：
- Go：gofmt、`go vet`（含 acceptance tag）、staticcheck v0.8.2 通过；全量 94 个有测试包通过、9 个无测试文件；
  group、HTTP、handler、middleware 竞态检测通过。
- 前端：全量 2435 项通过、1 项既有跳过，类型检查与构建通过，lint 为 35 条既有告警、无新增。
- 手测环境：同步 `test-0dc13340-20261116-132432` 成功。
  - 只读调用五个预览均返回预期结果，审计中无预览记录。
  - 浏览器中，分组编辑器显示“匹配节点（1）：Manual VLESS”；改为 `region:JP` 后显示“没有匹配的节点”与
    “保存后 1 名成员将新增 0 个、移出 1 个节点”。随后取消，分组未改动。

未完成：
- 本机没有 Chrome/Chromium，`npm run smoke:dist` 与 390/768/1440 宽度检查未执行。
- 手测环境只有一个分组和一个用户，用户换组与分隔标题的界面仅由自动化测试验证。
- 未提交、未推送、未部署。

