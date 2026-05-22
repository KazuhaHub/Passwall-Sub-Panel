# Changelog

Format inspired by [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
semver per `feedback_semver` (major = refactor, minor = feature, patch = fix +
small improvement).

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
