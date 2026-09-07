# 后台编辑保存后的状态同步

> 状态：**已实现（2026-09-06）**。现有规则集、模板、分组、服务器、用户、节点元数据、节点分隔标题、Inbound、证书、DNS 凭据、ACME 账户与语言包编辑已按本文收口。
> 关联：[ARCHITECTURE.md](ARCHITECTURE.md) §6.4；分页列表状态见 [`usePaged`](../web-react/src/hooks/usePaged.ts)。
> 实现位置：[`web-react/src/views/admin`](../web-react/src/views/admin/)；回归测试与共用夹具位于同目录及 [`web-react/src/test/adminSaveHarness.tsx`](../web-react/src/test/adminSaveHarness.tsx)。
> 约定：docs 中文；代码注释英文；标识符沿用代码原名。新增后台编辑页面必须在评审中逐项核对 §7。

## 0. 一句话

编辑已有实体成功后，**用写接口返回的完整实体（或提交时的完整表单快照）替换当前本地条目，不得无条件立即重载同一列表**；创建、删除和需要校正分页总数的操作仍可重载。编辑器需要列表没有的字段时，打开时单独读取详情，并以详情响应为准。

---

## 1. 背景：写成功后，紧接着的读不一定是新快照

后台页面过去普遍采用同一流程：

```text
PUT /entity/:id 成功
  → 关闭对话框
  → GET /entities
  → 用列表响应整体覆盖 React state
```

这个流程把「写已经成功」错误地等同于「紧接着的列表读一定能证明它」。实际运行中，列表读取可能受缓存、并发中的旧请求、远端同步或其他最终一致性边界影响，短时间返回保存前快照。结果不是数据没写进去，而是前端主动拿旧列表把新状态盖掉：管理员保存后立即重开仍看到旧值，稍等或 F5 后才看到新值。

两个浏览器都能复现、且等待数秒后自行恢复，只能说明问题不属于某个浏览器引擎；它不构成给任意一层武断归因的证据。因此本约定不依赖「固定等待几秒」或某种浏览器缓存假设，而是直接消除错误的数据流：**成功写响应已经给出本次操作的权威结果时，不再用一次非必要的列表读覆盖它。**

---

## 2. 定稿：按操作类型选择同步方式

| 操作 | 保存成功后的页面状态 | 是否立即重载列表 | 理由 |
|---|---|---|---|
| 编辑，接口返回完整实体 | 按主键替换本地条目 | **否** | 写响应就是本次保存的权威结果 |
| 编辑，接口不返回实体但表单是完整对象 | 按主键写回提交快照 | **否** | 没有新增的服务端生成字段 |
| 编辑器依赖列表未携带的字段 | 打开时读取详情，以详情建表单 | 视列表展示需要决定 | 列表行不是详情真相源 |
| 创建 | 重新读取列表 | **是** | ID、默认值、服务端排序和分页总数可能改变 |
| 删除 | 分页列表重载；小型本地列表可直接移除 | 通常是 | 需要补齐当前页并校正总数 |
| 多步骤编辑 | 合并每一步的成功响应后写回 | **否**；部分失败时例外 | 不把已成功的步骤伪装成失败或旧值 |
| 只返回 `204` 且服务端会规范化字段 | 修改接口契约，或读取单条详情 | 必要时读取详情 | 表单快照不足以表达最终实体 |

这里的「完整实体」是列表和下一次编辑所需字段的完整表示，不等于数据库整行。私钥、密码等禁止回显的字段仍保持 write-only；列表只需获得 `has_secret`、配置状态等安全摘要。

---

## 3. 标准写法

### 3.1 普通本地列表：使用写响应替换

```tsx
const saved = await updateEntity(editing.id, request)
setItems(previous =>
  previous.map(item => item.id === saved.id ? saved : item)
)
setDialogOpen(false)
```

不要在后面补一次 `load()`：

```tsx
// 错误：这次读可能把 saved 覆盖回保存前快照。
const saved = await updateEntity(editing.id, request)
setItems(previous =>
  previous.map(item => item.id === saved.id ? saved : item)
)
await load()
```

### 3.2 `usePaged` 页面：只更新当前行

```tsx
const { mutateItems } = usePaged<Entity>(fetchEntities)

const saved = await updateEntity(editing.id, request)
mutateItems(previous =>
  previous.map(item => item.id === saved.id ? saved : item)
)
setDialogOpen(false)
```

`mutateItems` 有意不改 `total`，所以它只用于已有行内容更新。创建和删除仍走 `refresh()`，不能把新行随手 append 到分页结果后假装总数没有变化。

### 3.3 写接口没有实体响应：使用完整表单快照

规则集与模板按 `slug` 做整对象 upsert，写接口不返回实体；其表单已经包含完整文件模型，可以直接写回：

```tsx
await saveRuleSet(form)
setItems(previous =>
  previous.map(item => item.slug === form.slug ? form : item)
)
setDialogOpen(false)
```

只有满足以下条件才能采用这一写法：

- 表单覆盖列表和再次编辑需要的全部字段；
- 后端不会在保存时生成或规范化页面需要显示的字段；
- 表单更新遵守 React 不可变数据约定，写回后不会被原地修改。

新接口若不满足这些条件，应优先让 `PUT/PATCH` 返回安全的完整实体，而不是再加一次列表读取。

### 3.4 创建与编辑必须分开

```tsx
if (editing) {
  const saved = await updateEntity(editing.id, request)
  setItems(previous =>
    previous.map(item => item.id === saved.id ? saved : item)
  )
} else {
  await createEntity(request)
  refresh()
}
setDialogOpen(false)
```

创建接口即使返回实体，也不能在分页页面直接 append，除非同时维护 `total`、当前排序、筛选命中与页面容量。为省一次 GET 而复制半套分页器不值得。

---

## 4. 详情读取：请求了详情，就必须使用详情

Inbound 编辑器需要 Flow、证书来源和完整连接配置，列表节点只是摘要。打开编辑器时应先放入可用的占位值，再以详情响应替换编辑上下文和表单：

```tsx
const detail = await getNode(row.id)
setEditingInboundNode(detail.node)
setEditInboundForm(parseInboundForEdit(detail.node, detail.inbound))
```

下面这种写法看似读取了详情，实际仍把旧列表行当作权威数据：

```tsx
const detail = await getNode(row.id)
setEditInboundForm(parseInboundForEdit(row, detail.inbound))
```

凡是提交阶段还要比较旧值、补齐稀疏请求或决定是否调用另一条写接口，保存逻辑也必须引用 `detail` 建立的编辑上下文，不能回头闭包捕获列表行。

---

## 5. 多步骤保存、派生字段与失败

### 5.1 多步骤保存

用户编辑会依次修改资料、周期用量和启用状态。每一步成功响应只更新自己拥有的字段，全部完成后再关闭对话框：

```tsx
let saved = await updateUser(editing.id, request)

if (trafficChanged) {
  const usage = await setUserTraffic(editing.id, periodUsedGB)
  setUsageMap(previous => mergeUsage(previous, editing.id, usage))
}

if (enabledChanged) {
  await setEnabled(editing.id, enabled)
  saved = { ...saved, enabled }
}

mutateItems(previous =>
  previous.map(user => user.id === saved.id ? saved : user)
)
setDialogOpen(false)
```

若后一步失败，前一步已经持久化，不能提示成「整次保存没有发生」。对话框保持打开，页面可以重新读取真实状态供管理员核对；重试读取不得重复执行已经成功的写操作。

### 5.2 确实需要后台重载时

Inbound 保存会影响表格里的名称、Flow、证书绑定与同步状态，仍需要后台重载节点列表。此时必须保证再次打开编辑器走单条详情接口，不能再从可能被旧列表响应覆盖的行建立表单。

若一个页面没有详情接口、后台重载又不可省，应采用明确的合并保护，例如保留本次保存字段、比较实体版本，或只接受晚于写入版本的响应。不要用 `setTimeout(load, 5000)` 猜测一致性窗口；固定时间既拖慢正常路径，也无法构成正确性保证。

### 5.3 错误责任

- 写失败：不更新本地条目，不关闭编辑器；沿用 Axios 统一错误提示。
- 写成功、本地更新成功：显示原成功提示并关闭编辑器。
- 创建/删除后的列表读取失败：写操作仍已成功，错误文案不得暗示可以安全地重复写。
- 可选派生信息读取失败：保留已保存实体；不要因此回滚成旧列表对象。

---

## 6. 可见代价（有意接受）

本地替换不会重新执行服务端排序和筛选。若管理员修改了当前排序或搜索使用的字段，该行会立即显示新值，但可能暂时留在原位置，或暂时继续出现在当前筛选结果中。换页、改变筛选、重新进入页面或手动刷新后，服务端重新决定位置和可见性。

这是有意取舍：本次操作首先保证「保存成功的值不被旧快照覆盖」。若产品要求编辑后立即重排，应在本地用与服务端完全相同的排序/筛选规则重算，或为读取结果增加版本保护；不能退回无保护的立即列表重载。

---

## 7. 新增页面评审清单

- [ ] `PUT/PATCH` 是否返回列表与再次编辑所需的安全完整实体？
- [ ] 保存成功后是否按 `id` / `slug` 替换本地条目？
- [ ] 编辑路径是否避免无条件 `load()` / `refresh()`？
- [ ] 创建、删除是否正确维护分页 `total`、排序和当前页？
- [ ] 表单是否依赖列表没有的字段？若是，打开时是否读取并实际使用详情？
- [ ] 多步骤保存是否逐项合并成功结果，并正确处理部分失败？
- [ ] 后台刷新是否可能覆盖刚保存的字段？若可能，是否有详情读取或版本/合并保护？
- [ ] write-only 字段是否继续只写不回显？
- [ ] 回归测试是否让列表保持旧值，并验证保存后立即重开仍显示新值？

最低回归用例不是「刷新接口也返回新值」；那只证明理想路径。测试必须让列表读取继续返回旧行，同时让写接口返回新行：

```tsx
installReads({
  '/admin/entities': list([{ id: 1, name: 'old-name' }]),
})
api.put.mockResolvedValueOnce({
  data: { id: 1, name: 'new-name' },
})

await saveAndReopen()
expect(screen.getByDisplayValue('new-name')).toBeTruthy()
```

---

## 8. 考虑过但未采用

| 方案 | 未采用理由 |
|---|---|
| 保存后固定等待数秒再读取 | 延迟是猜测，不是正确性；正常路径也被强制拖慢 |
| 所有编辑器打开时一律 GET 详情 | 增加每次打开延迟，且规则集/模板等无需第二个真相源 |
| Axios 拦截器自动改 React state | 请求层不知道页面归属、实体主键、分页/筛选状态及不同响应包络 |
| 为本问题引入 React Query / SWR | 能统一实体缓存，但迁移成本远大于当前约十处显式更新 |
| 公共 `replaceById` 工具 | 现有主键同时有 `id`/`slug`，还跨本地 state 与 `usePaged`；新增文件和 import 没有净减少复杂度 |
| 统一保存/刷新状态机 | 适合必须等待读取、支持重试/取消的大规模场景；现有写响应已足以更新页面，引入整套状态机会把简单写路径复杂化 |

未来若多数后台资源统一进入一个实体缓存层，再把本约定收敛为公共 mutation API；在那之前，显式的一行本地替换是最短、最容易审查的实现。
