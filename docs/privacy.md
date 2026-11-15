# 隐私与服务条款

> **范围。** 本文说明管理员如何发布「服务条款」和「隐私政策」，面板如何让用户同意，
> 以及「我们收集什么」这份清单是怎么生成的。**这一批只有后端和接口**，管理界面和公开页面随后续的界面批次上线；
> 在那之前，下文的接口都可以直接调用。
>
> **面板不提供任何条款正文。** 条款内容是否合法、是否符合节点所在地的法律，由所有者自行负责，本功能不构成法律意见。
> 编辑器里只会预置一行 `[[data-collection]]`（见 §3）。

## 1. 行为一句话

管理员用 Markdown 写两份文档（`terms`、`privacy`），每种语言各一份，每次发布生成一个不可变的新版本。
功能由设置 `legal_enabled` 控制，**默认关闭**：关闭时一切照旧，公开接口返回 404，没有任何用户被打扰。
打开后，注册表单必须勾选同意，已有用户登录后会被提示同意；**提示不阻断任何功能，也不影响订阅**。

## 2. 设置

| 键 | 默认 | 谁能写 | 说明 |
|---|---|---|---|
| `legal_enabled` | `false` | 系统设置 `PUT /api/admin/settings/ui` | 是否发布页面并要求同意 |
| `legal_consent_version` | `0` | **只有发布接口** | 全局的同意版本号，两份文档、所有语言共用一个。系统设置的 PUT 对它原样保留，请求里带什么都不会改动它 |
| `legal_disclose_usage` | `false` | 系统设置 `PUT /api/admin/settings/ui` | 是否在「我们收集什么」里列出「按网站用量」一项（见 §3）。默认不披露 |

`legal_consent_version` 的规则：

- 任一文档**第一次**发布，无论是否勾选「重大变更」，都把它置为 `1`。
- 之后只有勾选了「重大变更，需要用户重新同意」的发布才让它 `+1`。改错别字不会打扰用户。
- 它不会减小，也不会被发布以外的任何途径修改。

## 3. 「我们收集什么」

文档的 Markdown 里单独一行写 `[[data-collection]]`，前端会在这一行把正文切成两段，中间插入由**当前实际设置**生成的清单。
后端把这份清单作为结构化 JSON 放在公开接口的 `data_collection` 里，每项一个 key：

```json
{
  "sub_log":            {"enabled": true, "retention_days": 7,  "includes_ip": true},
  "auth_event":         {"enabled": true, "retention_days": 90, "includes_ip": true},
  "connection_history": {"enabled": true, "retention_days": 7,  "includes_ip": true},
  "hwid":               {"enabled": true, "includes_ip": false},
  "flag_records":       {"enabled": true, "retention_days": 90, "includes_ip": false},
  "risk_signals":       {"enabled": true, "includes_ip": false},
  "risk_reviews":       {"enabled": true, "includes_ip": false},
  "dest_hits":          {"enabled": true, "nodes": 2, "retention_days": 30, "includes_ip": false},
  "dest_trial":         {"enabled": false, "nodes": 2, "retention_days": 7, "includes_ip": false}
}
```

| key | 记录什么 | 保留天数来自 | 备注 |
|---|---|---|---|
| `sub_log` | 订阅访问日志（含 IP） | `sub_log_retention_days` | **`0` 表示永久保留** |
| `auth_event` | 登录记录（含 IP） | `auth_event_retention_days` | **`0` 表示永久保留** |
| `connection_history` | 连接历史（含 IP） | `risk_connection_retention_days` | 未设置时是清理任务实际使用的默认值 |
| `hwid` | 设备标识（摘要） | 无 | `enabled` 在 `risk_hwid_capture_off` 为真时是 `false` |
| `flag_records` | 风控标记记录（不含 IP） | `risk_flag_record_retention_days` | 未设置时是实际使用的默认值 |
| `risk_signals` | 风险判定（不含 IP，每小时覆盖） | 无 | 五类信号的开关都可以按分组重新打开。全局全部关闭、但有分组打开了其中任何一类时，`enabled` 仍为 `true`，并带 `enabled_groups`（打开的分组数，不含分组名）；全局全部关闭且没有分组打开时才是 `false` |
| `risk_reviews` | 管理员复核记录（不含 IP） | 无保留期 | 删除账号后一小时内清除 |
| `dest_hits` | 访问规则命中（不含来源 IP） | `dest_hit_retention_days` | `nodes` 是**正在记录**的原生节点数；没有节点在记录时 `enabled` 为 `false` |
| `dest_trial` | 白名单试运行（只记分组和主域名） | `dest_trial_retention_days` | 只有存在处于试运行的分组、且有节点在记录时 `enabled` 才为 `true` |

保留天数一律是面板**实际生效**的值，和清理任务读到的是同一份，不是页面上抄的一份默认值。
`retention_days` 为空表示这一项没有可以写的保留期；`nodes` 只出现在访问控制相关的项里。

### 按网站用量：默认不披露

「按网站用量」的记录功能不在这一批里，但清单已经为它留好了位置。**默认情况下，公开页面不会出现任何关于用量记录的内容**，
包括正在记录的节点数。管理员打开 `legal_disclose_usage` 之后，用量上线时才会在清单里多出一项 `dest_usage`
（正在记录的节点数和保留天数）。这是一个由管理员主动选择的披露，不是默认行为；本面板不提供逐个用户的提示。

清单是可扩展的：每个新增的记录类型注册一个条目（key、是否列出的开关、生成函数）即可，不用改已有的几项。
同一个 key 注册两次会在启动时直接 panic。

## 4. 同意

- **只对 `role = user` 的账号生效**，管理员、运维员和自定义角色的账号永远不会被提示。
- **注册**：`legal_enabled` 为真且已发布过版本时，注册请求必须带 `accepted_consent_version`，且等于当前的 `legal_consent_version`；
  缺失或不一致返回 `409`，`code` 为 `legal_consent_outdated`，响应里带当前的 `consent_version`，页面刷新后重试。
  这个检查在验证码之前，被拒的请求不会消耗验证码，也不会创建账号。成功后记录一条 `method = register` 的同意。
- **其他来源的账号**（管理员创建、SSO 首次登录、升级前已有的账号）：个人资料接口 `GET /api/user/me` 返回 `legal_pending: true`，
  界面据此弹出同意对话框。`legal_pending` 的条件是：功能已启用，`legal_consent_version > 0`，账号是普通用户，
  并且没有版本号**不低于**当前值的同意记录。
  另外，**必须有一份文档能被读到**：en-US 或 zh-CN 任一语言（回落链的两个终点）里至少发布过一份文档，
  否则不要求同意，注册页不出现必勾项（只在 ja-JP 之类的语言里发布，大多数访问者读不到，勾选框会指向一个 404）。
- `POST /api/user/me/legal/accept {"consent_version": N}`：`N` 必须等于当前版本，否则 `409 legal_consent_outdated`；
  功能关闭时 `404`。成功返回 `204`，记录 `method = prompt`。重复同意不报错。
- **不做 403 门禁，不阻断订阅**。拒绝同意只是反复被提示。
- 同意记录 `legal_consents` 的主键是 `(user_id, consent_version)`，只有 `accepted_at` 和 `method`。**不记 IP，不记浏览器信息。**
  删除账号后，每小时的清理任务会删掉它的同意记录。

## 5. 接口

### 公开

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/legal/:kind?lang=` | `kind` 为 `terms` 或 `privacy`。功能关闭、`kind` 不认识、或没有可用版本时 `404`。响应：`{kind, version, consent_version, locale, fallback_from?, content, published_at, data_collection}` |
| GET | `/api/auth/methods` | 多了 `legal: {enabled, consent_version}`，注册页和页脚据此渲染。`enabled` 为真的条件是功能已启用、已发布过版本、且有可读到的文档（见 §4） |

**语言回落链**：请求的语言 → （`zh-TW` 回落到 `zh-CN`）→ `en-US` → `zh-CN` → 404。返回的 `locale` 是实际给出的语言，
和请求的不同时 `fallback_from` 是请求的那个。不带 `lang`、或 `lang` 不是合法语言标签时，从 `en-US` 开始。

**ETag**：响应带 `ETag` 和 `Cache-Control: no-cache`，`If-None-Match` 命中时返回 `304`（304 同样带这两个头）。
ETag 是整个响应的摘要，包括 `data_collection`：管理员改了某个保留天数，同一份文档就是一个新的页面，缓存会自动失效。
`If-None-Match` 按 RFC 9110 的弱比较处理：可以是逗号分隔的列表，可以带 `W/` 前缀，`*` 匹配任何值。

公开接口不需要登录，所以它对数据库的开销是有界的：`data_collection` 里需要查数据库的数字（记录中的节点数、试运行分组数、
按分组重新打开风险信号的分组数）各缓存 **30 秒**；来自设置的数值（各保留天数、开关）不缓存，改完立即生效。因此节点数这类数字最多滞后 30 秒。

### 管理员（只有管理员，运维员返回 403）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/legal/:kind` | 每种语言的最新版本（含正文，给编辑器用），加全部历史版本（不含正文）。同时带 `enabled`、`consent_version` |
| POST | `/api/admin/legal/:kind` | 发布新版本 `{locale, content, consent_bump}`，返回 `{document, consent_version, consent_bumped}` |
| GET | `/api/admin/legal/affected-users` | `{consent_version, affected_users, accepted_current}`：一次重大变更会让多少普通用户重新被提示，以及其中已经同意了当前版本的有多少 |

发布的约束：

- `locale` 必须是合法的语言标签（如 `zh-CN`、`zh-TW`、`en-US`，大小写会被规整），否则 `400 legal_locale_invalid`。
- 正文不能为空，并且**不超过 60000 字节**（是字节不是字数，一个汉字三字节）。超出返回 `400`，`code` 为 `legal_content_too_large`。
- 版本号按 `(kind, locale)` 各自从 1 递增。两个人同时发布撞了号会自动重试一次，第二次还撞则返回 `409`。
- 版本是**不可变**的：没有编辑接口，改动就是发布下一版。
- 文档的写入和 `legal_consent_version` 的变动在**同一个数据库事务**里完成：要么都生效，要么都不生效，
  不会出现「文档已发布但版本号没动」的状态。发布时持有系统设置的写锁，并在提交后让设置缓存失效。

审计：这三个接口的写操作会像其他管理操作一样记入审计日志，**不属于仅管理员可见的审计行**
（发布的内容本来就是公开的）。审计中间件会把单个字符串值截到 8192 个字符，所以审计行里的 `content` 只有开头，这是预期的。

## 6. 数据与清理

| 表 | 内容 |
|---|---|
| `legal_documents` | 每个版本一行：`kind`、`locale`、`version`、`content`、`consent_bump`、`published_at`、`published_by`。`(kind, locale, version)` 唯一 |
| `legal_consents` | `(user_id, consent_version)` 主键，`accepted_at`，`method`（`register` / `prompt`） |

两张表在 SQLite、PostgreSQL、MySQL 上都由自动迁移创建，升级时无需手工操作。
没有外键。每小时的清理任务会删除 `legal_consents` 里账号已不存在的行（不设保留期：同意记录与账号同寿命）。
已发布的文档从不被删除（发布失败时事务回滚，也不会留下半个版本）。

## 7. 管理员注意事项

- 第一次发布之后，所有普通用户在下次登录时都会被提示同意（只要 `legal_enabled` 为真）。可以先发布、检查无误后再打开开关。
- 「重大变更」会让**所有**普通用户重新被提示，发布前先用 `affected-users` 看一下人数。
- 开启「访问规则命中」或其他记录功能之前，建议先启用本功能并发布隐私政策；该提醒是建议，不是限制。
- 反向代理自己的访问日志不在面板的控制范围内，如果它们记录了用户的请求，需要在政策正文里自行说明。
- 用户不能自行关闭管理员启用的记录；政策正文应如实写明，并告诉用户有疑问时联系管理员。
