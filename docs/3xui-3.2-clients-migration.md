# PSP × 3X-UI 3.2.0 客户端 API 迁移设计

> 状态:**P1 已实现(v3.6.2-beta.1)** —— adapter 迁到 `/clients/*`、删死方法、去 traffic
> fallback、compat 矩阵硬切 ≥3.2.0、Servers 页「批量升级 3X-UI」按钮,全后端测试绿。
> **P2 已完成(2026-05-28,真实 3.2.0 面板 live 验证)**:抓到并修复 `tgId` showstopper
> (`/clients/*` 要 int64、PSP 发 string,每次 add/update 都失败);§4.1(整行替换)、
> §4.3(反向推送 clobber)均**证伪**;P3 轴 A 反向推送经验证安全,已重新开启(`New()` 默认 push)。
> 决策:① 兼容策略 = **硬切 ≥3.2.0**(不做旧版兼容);② 面板 = **上游官方 3X-UI 3.2.0**。
> 关联:[3xui-compat.md](3xui-compat.md)(兼容矩阵 + 历史踩坑)、[ARCHITECTURE.md](ARCHITECTURE.md)、memory `reference_xui_v3_api_break`。

## 0. TL;DR

3X-UI 3.2.0 把客户端管理从 `/panel/api/inbounds/*` 整体搬到了一等公民命名空间
`/panel/api/clients/*`,**删除了 PSP 在用的 7 个 inbound 作用域 per-client 端点**。
PSP 当前 adapter 对接 3.2.0 会出现:**新建用户、删除用户直接 404 失败**;**启停 / 改 UUID /
改配额(走读改写)语义存疑**。

好消息:所有 3X-UI 写操作都收口在 [internal/service/sync/sync.go](../internal/service/sync/sync.go),
而新 API 的 update/del 都以 **email** 为键 —— PSP 的 email 本就是 `u{userID}-n{nodeID}@domain`
**每节点唯一**,刚好与"按 email 寻址"一一对应。所以**迁移几乎全部集中在
[internal/adapters/xui/](../internal/adapters/xui/) 内部,`ports.XUIClient` 接口签名和 `sync.go`
服务层基本不用动**。

订阅渲染**完全本地**(稳态零回源),不受任何影响。

## 1. 背景:3.2.0 改了什么(已用测试 token 实测)

实测面板 `panelVersion = 3.2.0`、`xray 26.5.9`。

| 现象 | 实测 | PSP 现状 |
|---|---|---|
| `settings`/`streamSettings` 返回为**嵌套对象** | ✅ | `flexJSON`(v3.5.1)已兼容,不破 |
| `clientStats[*]` 多 `uuid`/`subId`/`lastOnline` | ✅ | Go 忽略未知字段,不破 |
| `GET /inbounds/getClientTraffics/{email}` | **404** | PSP 在用 → 破 |
| `GET /inbounds/getClientTrafficsById/{id}` | **404** | PSP 在用 → 破 |
| `POST /inbounds/addClient` 等全部 per-client 端点 | openapi 全文无,实测 404 | PSP 在用 → 破 |
| `GET /clients/traffic/{email}`、`/clients/list/paged`、`/clients/get/{email}` | ✅ 200 | 新替代 |

openapi 全文搜 `addClient`/`delClient`/`copyClients`/`getClientTraffics`/`resetClientTraffic`/
`updateClient`(inbound 作用域)——**零命中**。注:`/clients/{add,update,attach,detach}` 早在
3.1.0 就已新增(见 [3xui-compat.md](3xui-compat.md) 历史事件),3.2.0 是**删掉了 inbound 侧的旧
入口**并补齐了 `del`/`traffic`/`resetTraffic`/`bulk*`/`groups*` 的完整面。

## 2. 影响面(逐方法)

`ports.XUIClient` 的 14 个方法,按 3.2.0 下的状态分三类:

### A. 不受影响(端点都在)

`ListInbounds` / `GetInbound` / `AddInbound` / `DelInbound` / `SetInboundEnable` /
`GetInboundClients`(走 `GetInbound` 解析 `settings.clients`)/ 全部 `/server/*`(版本、升级、xray)。

### B. 生产在用、会破(必须改)

| 方法 | 旧端点(404) | 唯一调用点 |
|---|---|---|
| `AddClient` | `POST /inbounds/addClient` | sync.go:95 `AddClientToInbound` |
| `DelClientByEmail` | `POST /inbounds/{id}/delClientByEmail/{email}` | sync.go:121(回滚)、sync.go:177 `DelOwnedClient` |
| `UpdateClient` / `UpdateClientWithInbound` | 走 `POST /inbounds/update/{id}` 读改写,端点在但**语义存疑** | sync.go:141/223/247/272 |
| `GetInboundTraffics` | `GET /inbounds/getClientTrafficsById/{id}` | traffic.go:417(仅 fallback) |

### C. 接口里有、生产零调用(死代码,直接删)

`DelClient`(`/inbounds/{id}/delClient/{uuid}`,sync.go 注释明说弃用,因为只匹配 VLESS/VMess
UUID、不匹配 SS/Hy2)、`CopyClients`、`GetClientTraffic`(全局聚合,无人用)、`ResetClientTraffic`
(PSP 自己管流量账,从不重置 3X-UI 计数器)。这 4 个只存在于
[ports/xui.go](../internal/ports/xui.go)、[adapters/xui/client.go](../internal/adapters/xui/client.go)
和 test fake 里,删除无任何调用方影响。

## 3. 新端点映射设计(adapter 内部改写)

核心策略:**保持 `ports.XUIClient` 接口签名不变**,只换 adapter 实现。这样 `sync.go` 一行不用改。

| 接口方法(签名不变) | 现在 | 改为 |
|---|---|---|
| `AddClient(ctx, inboundID, spec)` | `POST /inbounds/addClient` | `POST /clients/add`,body `{ client: <spec>, inboundIds: [inboundID] }` |
| `UpdateClient(ctx, inboundID, oldUUID, spec)` | GET inbound → 改 `settings.clients` → POST `/inbounds/update/{id}` | `POST /clients/update/{spec.Email}`,body = 完整 client 对象。**`inboundID`/`oldUUID` 形参退化为无用**(email 是稳定键,新 uuid 在 body),保留签名只为不动调用方 |
| `UpdateClientWithInbound(ctx, inb, uuid, spec)` | 同上(省一次 GetInbound) | 同 `UpdateClient`(新 API 本就无需 GetInbound,**该优化失去意义**)。实现里忽略 `inb`,直接委托 |
| `DelClientByEmail(ctx, inboundID, email)` | `POST /inbounds/{id}/delClientByEmail/{email}` | `POST /clients/del/{email}`(见 §4.2:PSP email 每节点唯一,全局删=单 inbound 删,且更干净不留孤儿) |
| `GetInboundTraffics(ctx, id)` | `GET /inbounds/getClientTrafficsById/{id}` | **删除该 fallback**,改为只靠 `ListInbounds().clientStats`(仍在,且更全)。见 §7 |

`UpdateClient` 的 body 由现有 `buildClientJSON`(client.go:739)直接复用即可——它产出的就是
3X-UI client 对象的字段集(`id`/`email`/`enable`/`flow`/`limitIp`/`totalGB`/`expiryTime`/
`subId`/`tgId`/`reset`/`password`/`method`/`auth`)。

附带简化:`RotateClientUUID`(sync.go:211)现在靠"旧 UUID 进 path + 新 id 进 body"这套别扭逻辑,
迁移后 **email 是稳定键、旧 UUID 不再需要**,轮换天然变干净(但接口签名先不动,`oldUUID` 形参留着)。

## 4. 三个语义陷阱 + 决策

### 4.1 `/clients/update` 是**整行替换**,不是合并 —— 必须补全字段

3.2.0 文档明说 `/clients/update/{email}` "server replaces the row, does not patch"。
而 PSP 当前 RMW 实现(`updateClientInSettings`,client.go:665)是**把 nextClient 合并进现有
client**(保留 `subId` 等 PSP 没显式设的字段)。

**风险**:整行替换时,`buildClientJSON` 没带的字段(典型是 `subId`)会被**清空**。

**决策**:确保 `UpdateClient` 走 `/clients/update` 时 body 携带**所有需要保留的字段**。
两个选项:
- **(推荐)** 在 spec/`buildClientJSON` 里补齐 `subId`(PSP 本来就持有/可派生用户的 subId);
  其余字段(`totalGB`/`expiryTime`)PSP 本就是真相源,正好该由 PSP 全量下发。
- (兜底)`UpdateClient` 内部先 `GET /clients/get/{email}` 取现状再 merge 后下发 —— 但这又
  退回到一次读 + 一次写,丢掉了新 API 的简化收益,不推荐。

> 注:3X-UI 的 `subId` 只影响**它自己**的订阅服务器分组;PSP 订阅是本地渲染、不读它。
> 所以即便 `subId` 被清空也不影响 PSP 出订阅,但会让"在 3X-UI 面板里直接看这个 client"
> 时丢失分组信息。按 PSP「3X-UI 状态尽量与 PSP 一致」的原则,仍建议补全。

### 4.2 `/clients/del` 是**全局删**;ownership 守卫位置澄清

`/clients/del/{email}` 删光该 email 在**所有** inbound 的存在;`/clients/{email}/detach`
才是按 `inboundIds` 删指定 inbound。

blast 分析担心"全局删丢掉 per-inbound 语义、绕过 ownership 守卫",**需澄清**:

- PSP 的 ownership 守卫(`ensureClientOwned` / `GetByMatch`,sync.go:147/214/238)是
  **PSP 进程内、HTTP 调用之前**就跑的,与用哪个 3X-UI 端点无关 → **不存在绕过**。
- PSP 的 email 是 `u{userID}-n{nodeID}`,**每节点唯一**,一个 email 只对应一个 inbound →
  "全局删" 实际只命中那一行,与 "per-inbound 删" 等价,且**不留孤儿 client 记录**
  (detach 会把 client 留在零 inbound 状态,反而脏)。

**决策**:`DelClientByEmail` → **`POST /clients/del/{email}`**(`keepTraffic=0`,PSP 自管账)。
`DelOwnedClient` 里的存在性预检(sync.go:163-169 `GetInboundClients`)可保留(仍走 `GetInbound`),
或简化为直接 del + "not found 当成功"。`DelAllOwnedForUser`/`DelAllOwnedForInbound` 不变
(仍逐 ownership 行调 `DelOwnedClient`)。

> 若未来 PSP 改成"一个 client 挂多 inbound"(v4 方向),再切回 `detach + inboundIds`。当前不需要。
>
> **→ 已立项为 v3.9.0,设计见 [v3.9.0-client-multi-inbound.md](v3.9.0-client-multi-inbound.md)**(凭据全对称 + 模型与 3X-UI 同构;attach/detach 已真机验证)。

### 4.3 RMW 退役 + axis-A inbound 配置回推(**需 live 验证**)

3.2.0 把 client 做成一等公民:"一条 client 行驱动它所属每个 inbound 的 `settings.clients`"。
即 **clients 表是真相源,`settings.clients` 是派生投影**。

- **client 写**:迁到 `/clients/*` 后,PSP 不再通过 `settings.clients` 写 client,这条风险消失。
- **inbound 配置回推**(reconcile axis-A,`reconcileInboundConfig` 调 `UpdateInbound`+`GetInbound`):
  `UpdateInbound` 现在会 `settingsWithCurrentClients` **重新注入 client 列表**再 POST
  `/inbounds/update/{id}`。在一等公民模型下,这次写可能(a)能落地、(b)被 clients 表二次投影**覆盖**、
  或(c)产生没有 clients 表行的孤儿 `settings.clients` 条目。openapi 没说死。

**决策**:axis-A 改成**只推 inbound 配置、不碰 client**。具体方案在 live 验证后定(见 §9):
- 若实测 `/inbounds/update` 会保留传入的 `settings.clients` 且不覆盖 → 维持现状,但把注入的
  client 用 "GET 到的原样" 回填(不增删)。
- 若实测会覆盖/产生孤儿 → axis-A 改为 "GET inbound 配置 → 只 diff streamSettings/sniffing/
  port 等非 client 字段 → 用不携带 clients 的方式回推"(或确认 `/inbounds/update` 接受
  "省略 clients = 不动 clients")。

## 5. `ports.XUIClient` 接口改动

**签名层面尽量零改动**(保护 `sync.go` / `reconcile.go` / `traffic.go` 调用方):

- 删除:`DelClient`、`CopyClients`、`GetClientTraffic`、`ResetClientTraffic`(§2.C 死代码)。
- 删除:`GetInboundTraffics`(§7),同步删 traffic.go 的 fallback 分支。
- 保留并改实现:`AddClient`、`UpdateClient`、`UpdateClientWithInbound`、`DelClientByEmail`。
- `UpdateClientWithInbound` 可后续删(优化已失效),本期先委托给 `UpdateClient`,降低 churn。

## 6. 死代码清理清单

- [ports/xui.go](../internal/ports/xui.go):删 `DelClient`/`CopyClients`/`GetClientTraffic`/
  `ResetClientTraffic`/`GetInboundTraffics` 五个方法签名 + 相关注释。
- [adapters/xui/client.go](../internal/adapters/xui/client.go):删对应实现(357/367/381/394/407 区段)。
- test fake(`sync_test.go`、`traffic_test.go`):删对应 stub 方法,避免接口不满足。

## 7. 流量轮询 fallback 处理

traffic.go:413-424 的 Phase 2 fallback(`ListInbounds` 没返回某 inbound 的 `clientStats` 时,
调 `GetInboundTraffics` 单独补)依赖的 `getClientTrafficsById` 已 404。

3.2.0 的 `ListInbounds().clientStats` **稳定返回**(实测每 inbound 都有,且带 uuid/subId/
lastOnline),fallback 触发条件本就极少。**决策**:删除 fallback,主路径 `clientStats` 兜全。
若日后发现某些 inbound 仍缺 clientStats,再用 `GET /clients/list/paged?...` 按 inbound 过滤补
(返回 slim 行含 `traffic{up,down}` + `inboundIds`)。

## 8. 版本门禁 / compat 矩阵

迁移后 PSP 调 `/clients/*`,而旧 inbound 端点在 3.2.0 已删 →
**迁移版与 ≤3.1.x 互斥**(沿用 v3.5.1「flexJSON 硬切 ≥3.1.0」的先例)。

- 新建一条 compat entry:`psp_min = <迁移版,如 v3.7.0>`,`min_xui = "3.2.0"`,
  `max_tested_xui = "3.2.0"`(放 [docs/compat/v3.json](compat/v3.json) `entries` 数组**最前**,
  first-match-wins)。
- 旧 entry(`v3.6.x → min 3.1.0`)保留不动,跑旧 PSP 的部署继续从它取数。
- **前置条件**:迁移版上线前,**所有面板先升到 3.2.0**。若存在混合舰队(部分 3.1.0 + 部分
  3.2.0),要么先统一升级,要么 adapter 做 version-detect 双路(成本高,不推荐,见 §12)。

## 9. live 验证清单(写操作,需在测试 client 上做,勿动真实用户)

用测试 token 在一个**临时 client / 临时 inbound** 上验证以下不确定项,再定 §4.3 / §4.1 细节:

1. `POST /clients/add {client:{email:"psp-probe@test"}, inboundIds:[X]}` → 看是否建一等公民
   client + 回填 `settings.clients`;`GET /clients/get/{email}` 看自动生成的字段(uuid/subId)。
2. `POST /clients/update/{email}` 只发部分字段 → 验证是否**整行替换清空** `subId`(确认 §4.1)。
3. `POST /inbounds/update/{X}` 带"原样回填的 `settings.clients`" → 再 `GET /clients/list` 看
   client 是否被覆盖 / 产生孤儿(确认 §4.3 走哪条分支)。
4. `POST /clients/del/{email}` → 确认从该 inbound 消失、`xray_client_traffic` 行随 `keepTraffic` 行为。
5. 收尾:把探测产生的临时 client/inbound 全部删掉。

## 10. 实施步骤(分期)

1. **P1 — adapter 改写 + 死代码清理**(本设计 §3/§5/§6/§7):接口签名不变,改 client.go 实现,
   删 5 个方法 + traffic fallback,修 test fake。`go test ./internal/adapters/xui/` 先绿。
2. **P2 — live 验证**(§9)→ 据结果定 §4.1(subId 补全)与 §4.3(axis-A)实现。
3. **P3 — axis-A inbound 配置回推适配**(reconcile.go `reconcileInboundConfig`)。
4. **P4 — compat 矩阵 + 文档**(§8;更新 [3xui-compat.md](3xui-compat.md) 历史事件、memory)。
5. **P5(可选优化)— 接入 bulk 端点**:`bulkDel`/`bulkCreate`/`bulkResetTraffic`/`bulkAttach`
   把 PSP 现有"批量启停 / 批量删 / 删用户全部 client"从 N 次 HTTP 降到 1 次。属增量优化,
   与兼容性修复解耦。

## 11. 测试计划(TDD)

- 改行为前先跑 `go test ./internal/adapters/xui/ ./internal/service/sync/ ./internal/service/traffic/`,
  记录现状。
- adapter 改写:为 `AddClient`/`UpdateClient`/`DelClientByEmail` 各补单测,断言**请求 URL +
  body 形状**(用 httptest mock 一个返回 `{success:true}` 的 3.2.0 桩),覆盖 happy + 1 failure
  (如 `/clients/update` 返回 `{success:false,msg:"client not found"}`)。
- 删方法后跑 `go build ./... && go vet ./...` 确认无悬挂引用。

## 12. 决策记录(已定)

1. **兼容策略 = 硬切 ≥3.2.0**(不做旧版兼容)。理由:3.0.x / 3.1.0 / 3.2.0 之间没有共同 API 面
   (3.2.0 删了 inbound 作用域端点),双路 = 维护两套完整 client 管理 + version-detect,与
   v3.5.1「硬切 ≥3.1.0」先例和自用项目简单优先冲突。compat 矩阵 `min_xui` 已置 3.2.0。
2. **面板 = 上游官方 3X-UI 3.2.0**(非 fork)→ "面板侧加旧端点别名"方案不成立,PSP 必须适配。
3. **`subId` 不额外补全**(§4.1)。`buildClientJSON` 已带 `subId`(以及 enable/totalGB/
   expiryTime/tgId/reset/协议密钥),整行替换下行为与迁移前的 RMW-merge **对 PSP 设置的字段
   完全一致**;唯一差异是面板上手工加的 `comment` 等 PSP 不设字段不再保留 —— PSP 托管 client
   不预期有手工字段,可接受。

## 13. 待办(P2 / P3,需 live 验证)

- ✅ **P2 — live 写验证(2026-05-28,完成)**:在一个隔离的禁用临时 inbound 上实测,测完连
  client 带 inbound 一起删干净,没碰任何真实 inbound/用户。发现:
  (a) **tgId showstopper** —— `/clients/add`·`update` 要 `tgId` 为 int64,PSP 的
  `buildClientJSON` 发的是 string(`s.TgID`),报 `cannot unmarshal string into ... tgId`,
  **每次 add/update 都失败**;已修(string→`strconv.Atoi`→int,空=0)。
  (b) §4.1 **证伪** —— `/clients/update` 是 **merge**:改 `totalGB` 时省略 `subId`,`subId`
  仍保留;PSP 省略字段安全,**不用补 subId/comment**。
  (c) §4.3 **证伪** —— 复刻 `UpdateInbound` 把带 client 的 `settings`(字符串)回推
  `/inbounds/update`,first-class client **完好**(`/clients/list` 精确 1 条、无孤儿无重复、
  uuid 不变、仍在 `settings.clients`)。
  (附:PSP 发 `id`=uuid 字符串被面板正确存进 `uuid` 列、另给数字主键,接受。)
- ✅ **P3 — reconcile 轴 A(完成)**:P2 既证 §4.3 安全,`axisAReversePush` 由 `New()` 置回
  `true`、反向推送恢复;`false`(采纳)保留为 kill-switch + 测试覆盖。
- **P5(可选)— bulk client 端点**:把批量启停 / 批量删 / 删用户全部 client 接 `/clients/bulk*`,
  N 次 HTTP 降到 1 次。增量优化,与兼容性修复解耦。
- **文档同步**:`docs/ARCHITECTURE.md` 的 adapter API 清单(§3X-UI client)仍列着旧的
  `DelClient` / `CopyClients` / `GetClientTraffic` / `GetInboundTraffics` / `ResetClientTraffic`,
  待一次 docs pass 同步到 `/clients/*`。
