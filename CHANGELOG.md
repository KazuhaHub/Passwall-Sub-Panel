# Changelog

Format inspired by [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
semver per `feedback_semver` (major = refactor, minor = feature, patch = fix +
small improvement).

## v3.6.3-beta.6 — 2026-06-01

修 beta.5 的认证日志接口 500;规则集编辑器换 CodeMirror,修编辑大规则集卡顿。

### Fixed

- **`/api/admin/auth-events` 一律 500（认证日志页 + 用户「最近登录」面板崩溃）** —— app.go 用字段逐个拷贝重新拼装 `ports.Repos`，漏拷了 beta.5 新加的 `AuthEvent` → 该仓储为 nil → handler 对 nil 接口调方法 panic（空 body 500）；同时登录发射因 nil-guard 静默不记（认证日志一直为空）。改为 `repos := mysqlRepos` 派生 + 仅覆盖两个 YAML 仓储（规则集/模板），从根上消除"漏拷新仓储"这类 drift；新增 `TestNewReposPopulatesEveryDBRepo` 反射守门（任何 DB 仓储漏接线即 `go test` 红）+ auth-events handler 端到端测试。

### Changed

- **规则集内容编辑器换成 CodeMirror（修卡顿）** —— 原 MUI `multiline` 自增高 textarea 每次按键都 O(内容长度) 重测高度，上千行规则集打字卡顿。换成 CodeMirror 6（`@uiw/react-codemirror` + `@codemirror/lang-yaml`：虚拟化、行号、YAML 高亮）。**懒加载**：重依赖独立成 ~142KB(gz) chunk，仅在打开规则集编辑弹窗时拉取，**首屏 bundle 零影响**。`proxy_group_order` 仍用普通字段（内容很小）。

## v3.6.3-beta.5 — 2026-06-01

新增「认证日志」（一等公民登录审计），并把 beta.4 的 Docker 跑 root 改回非 root（su-exec 降权）。无破坏性变更：新增一张 `auth_events` 表 + 一个 KV 设置，AutoMigrate 自动处理。

### Added

- **一等公民认证日志（`auth_events`）** —— 记录所有登录尝试（**本地 / SAML / OIDC，成功 + 失败**），含用户、方法、结果、失败原因码、IP、UA、时间；IP 地区在查看时离线解析（与审计/订阅日志一致）。
  - **闭合「SSO 登录不留痕」的硬缺口**：此前只有本地登录（经通用审计）留痕，SAML/OIDC 登录完全不记 —— 企业普遍用 SSO，这是合规盲区。现在三种方法在各自认证判定点统一记录（成功、`invalid_credentials`/`disabled`/`token_error`、SSO 的 `sso_no_account`/`sso_conflict`/断言或交换失败/`oidc_idp_error` 等）。
  - 后台 **Logs →「认证日志」** tab：按方法/结果/用户/时间/关键词筛，成功/失败高亮，带 IP+地区。用户编辑弹窗新增**「最近登录」面板**查看单用户登录历史。staff 可读 `GET /api/admin/auth-events`。
  - 登录从通用审计（`shouldAuditPath`）移除、改由 `auth_events` 唯一权威记录（token refresh 仍留审计），避免双记。
  - 独立保留期 `auth_event_retention_days`（默认 90，floor 90，同 `traffic_history_days`），每小时清理循环 prune。

### Changed

- **Docker 改回非 root（su-exec 降权 + PUID/PGID）** —— beta.4 为修绑定挂载写入而改成跑 root；按开源产品（小团队~企业）视角,非 root（k8s `runAsNonRoot` / 镜像扫描 / 合规）是正经诉求,故改用 postgres/redis/gitea 同款：容器以 root 启动 → entrypoint `chown` 挂载 → `su-exec` 降到非 root（默认 UID 10001，可经 `PUID`/`PGID` 对齐宿主机用户以免 sudo 编辑 `./config`）→ 长期进程非 root。fresh 与升级部署都自愈、无需手动 chown。`Dockerfile` 与 `Dockerfile.release`（发布的多架构镜像）同步修改；新增 `.gitattributes` 锁 `*.sh` 为 LF（防 CRLF shebang 破坏 entrypoint）。

## v3.6.3-beta.4 — 2026-06-01

beta.3 后的 geo 设置完善 + 设置页 UX 修复，外加一处 Docker 部署权限修复。无 schema 变更。

### Added

- **地区显示细化为「国家 · 州/省 · 城市」** —— 访问日志的 IP 地区由原来的「国旗 + 国家 + 城市/省其一」改为国家、州/省、城市三级（城市与州/省相同时去重）。数据后端早已抽取（subdivisions[0] + city），纯展示层改动。
- **自动更新间隔可配** —— 新增 `geo_ip_update_interval_hours`（默认 12，最小 1h），设置页可调；更新循环每轮重读，改动无需重启即生效（原为写死的 12h）。
- **IPinfo / MaxMind 可指定其他版本（付费）** —— MaxMind edition、IPinfo 数据库各为一个自由文本框：IPinfo 按填写的产品名拼 `ipinfo.io/data/<名>.mmdb`（免费默认 `ipinfo_lite`，付费可填如 `standard_location`，PathEscape 防注入），MaxMind 付费可填 `GeoIP2-City`。切换来源时自动清空版本字段，避免把一家的版本名带到另一家。

### Fixed

- **设置页「激活数据库」下拉选「自动」显示空白** —— 加 `displayEmpty`，现正常显示「（自动：按文件名第一个）」标签。
- **License Key / Token 字段改用 SMTP 密码同款「已保存（保持不变）」** —— 已存有凭据时显示只读条 + 「更改」，避免保存时误清空（空值=保持不变的语义后端早已支持）。
- **「立即更新」按钮现在先自动保存设置再下载** —— 不必再手动先点保存；校验失败或网络错会正确中断。
- **geoip 目录建不出来时不再静默** —— 启动若无法创建该目录会打 WARN（常见于 Docker 下配置目录权限问题）；手动更新失败的 mkdir 报错带上路径与可操作提示。

### Docker

- **容器改为以 root 运行（去掉 `USER psp`）** —— 容器原以非 root（UID 10001）运行，但 docker-compose 绑定挂载的 `./config` 被 Docker 自动创建为 root 属主（从旧 root 时代镜像升级也遗留 root 属主），非 root 进程无法写入：**全新部署首次生成 config.yaml 即崩溃重启**，升级后也无法创建新目录（如 geoip）。改为 root 后绑定挂载始终可写，与 3X-UI / Cloudreve 的做法一致；`Dockerfile` 与 `Dockerfile.release`（发布的多架构镜像）同步修改，命名卷数据目录不受影响。代价：宿主机看到的 `./config` 文件为 root 属主（手动编辑需 sudo），但面板设置基本走 UI——此举回退了 v3.6.3 引入的非 root 加固（审计 LOW 项），换取开箱即用的部署。

## v3.6.3-beta.3 — 2026-06-01

beta.2 后的修复:geo「立即更新」一直 502 的根因 + 该板块在英文界面没有翻译。无 schema 变更。

### Fixed

- **geo「立即更新」一直返回 502、且看不到真实原因** ── 端点原先在 HTTP 请求内**同步下载**整个数据库
  (MaxMind `.tar.gz` 最长 3 分钟),下载失败/超时就回 502;面板自身又复用 502 当「外部库失败」错误码,
  与前置反向代理的网关 502 撞车 —— 前端拿到的往往是反代的 HTML 错误页(没有 JSON `error` 体),只能显示
  通用的 `AxiosError ... 502`,真实原因(密钥错、下载源不可达等)被吞掉。改为:`POST .../update` 立即返回
  `202` 并在**后台**跑下载,把 `{updating,last_error,last_file,last_at}` 通过既有 `GET .../status` 暴露;
  前端触发后**轮询状态**直到完成,直接显示后端返回的真实成功/错误,反代再也无法吞掉错误。手动「立即更新」
  与 12h 自动更新现在共用同一单飞守卫(`StartUpdate`),消除了两者同时写 `.part` 临时文件的竞态。
- **「IP 地区显示(访问日志)」整块在英文界面仍是中文** ── 该板块全部用 `t(…, {defaultValue:'中文'})`,
  但两个语言包里**从未补 `settings.geo.*` 键**,英文模式只能回退到中文 defaultValue;另有 3 个「更新来源」
  下拉项压根没套 `t()`。补齐 en-US / zh-CN 的 `settings.geo.*`(26 键)与访问日志的 `logs.region_hint`,
  并把 3 个硬编码下拉项接入 i18n。

## v3.6.3-beta.2 — 2026-06-01

beta.1 发布后的逐行复查批次:1 个用户可见 bug + 流量图表精度/写入修复 + 两个 geo 健壮性小修。全部带回归 / drift 测试。无 schema 变更。

### Fixed

- **geo 自动更新选「DB-IP City Lite」保存即 400** ── 设置页下拉提供 `dbip`(免账号、城市级),
  下载器 `candidateURLs` 也一直支持它,但设置 PUT 的来源校验白名单**漏了 `dbip`**(只有
  `ipinfo`/`maxmind`/`custom`),选中保存被 400 拒绝 —— 一个纯校验侧的 drift。改为**单一真相源**
  `geo.IsValidUpdateSource`(handler 直接调它),API 接受的来源集合 = 下载器能下的集合,结构上不可能
  再分叉;新增 `TestUpdateSourcesNoDrift` 守门(任一侧漏改 `go test` 即红)。
- **流量图表每个小时边界系统性少算 ~一个轮询间隔(5-min 节奏 ≈8%)** ── hourly rollup 原先按桶内
  `MAX-MIN` 求 delta,**上一小时最后采样→下一小时首采样**那段跨边界流量两个桶都不计。改为
  `MAX-floor`,floor 取「桶内 MIN」与「相邻前一小时的 MAX」中较低者(进位),把跨边界增量归到后一个
  小时;跨边界计数器跳变(Xray 重启)时回退到桶内 MIN 防负;**缺口(中间整小时无数据)不进位**——
  无法判断缺口流量发生时刻,保守用桶内 MAX-MIN。仅影响图表柱形,不触及权威口径(lifetime/period/
  today 走独立 counter 路径)。
- **rollup 写放大** ── 轮询期原先每 5 分钟把整个 ~7 天原始窗口的所有桶全部 re-upsert。新增
  `RollupRecent`:轮询路径只重写最近几小时(开桶 + 重叠余量)的桶,旧的封口桶留给每小时清理循环的
  全量 `RollupOnce`。进位仍按完整原始集计算,近窗桶与全量结果一致,写量从每实体上百桶/轮询降到个位数。
- **DB-IP 上月回退 URL 在月末失效** ── DB-IP 是按月文件、无稳定 latest,故同时尝试本月+上月。
  上月原用 `now.AddDate(0,-1,0)`,在 31 号(如 2026-03-31→2026-03-03)会归一化回**本月**,
  回退 URL 与本月相同、形同虚设。改用「本月 1 号减一天」算上月(`prevMonthOf`),加 `TestPrevMonthOf`
  覆盖月末 / 跨年 / 闰月。
- **激活的 mmdb 文件损坏时日志刷屏** ── `ensureReader` 在 `Open` 失败时未记录已尝试的
  (path, mtime),导致每次 `Lookup` 都重试 `Open` 并打一条 WARN。改为记录失败的 (path, mtime)
  并丢弃陈旧 reader(避免拿另一个库的数据误导),后续 Lookup 直接短路,只在文件 mtime 变化或管理员
  改选时重试。

## v3.6.3-beta.1 — 2026-06-01

自 v3.6.2 以来的全部改动打包进这个 beta:① 新功能「访问日志 IP 地区显示」(离线 mmdb);
② 全面审计(19 维度 / 对抗式验证)后的修复批次 —— 1 个 HIGH(进程崩溃路径)+ 9 个 MEDIUM
+ 一组高性价比 LOW,全部补了回归 / drift 测试;③ 流量图表 / 存储管线的修复(下方小节)。
无 schema 破坏性变更。

### Added

- **访问日志 IP 地区显示(完全离线)** ── 订阅访问日志和审计日志的每条记录,在 IP 下方显示
  来源地区(国旗 + 国家/城市)。**纯离线**:用本地 `.mmdb` 库(放 `<ConfigDir>/geoip/`)做
  本地内存查询,**不外呼第三方、不缓存、不写数据库**——用户真实 IP 不离开服务器。reader 自动
  识别多种 schema(MaxMind GeoLite2 / GeoIP2、DB-IP Lite、IPinfo Lite),放哪个库就用哪种粒度。
  - **单一激活源,绝无冲突**:同时存在多个 `.mmdb` 时**不合并**,管理员在设置里选激活哪个
    (`geo_ip_db_file`,留空=按名取第一个),所以两个库永远不会"打架"。
  - **可选自动更新**(后台每 12h + 设置页「立即更新」按钮):来源 `maxmind`(GeoLite2-City
    `.tar.gz`,**默认**,需 license key,满足 30 天 EULA)/ `dbip`(月版 `.gz`,免账号匿名)/
    `ipinfo`(token,国家+ASN)/ `custom`(任意 `.mmdb`/`.gz`/`.tar.gz` URL)。下载→**校验确实
    是合法 mmdb**→原子替换→热重载;走 `safehttp`(SSRF 防护);只下载**公共库**,不涉及用户 IP。
  - **管理后台可配置**:设置页新增 geo 区块(启用开关、激活库下拉、自动更新开关、来源选择、
    token[**加密落盘 + GET 脱敏**]、MaxMind edition / 自定义 URL、库状态[类型/粒度/构建日期/
    当前激活]、立即更新);后端 `GET /api/admin/settings/geoip/status` + `POST .../update`。
  - **诚实标注精度**:经独立基准(arXiv:2605.21937 等)核实,免费库**国家级可靠(~88% 中国、
    ~99% 全球),城市级一般、亚洲最差**,且代理出口 IP 会解析到机房——UI 把城市标为"仅供参考",
    国家/省作权威。各数据源的署名要求在设置页注明。
  - 新增依赖 `oschwald/maxminddb-golang`(纯 Go,无 CGO)。

### 流量图表 / 存储管线（修好一个没做完的迁移）

- **流量历史图表超过 ~7 天就静默渲染成一长串平 0**(HIGH 修复)── beta.6 建好了
  raw 5-min → hourly rollup 这套机器(本意是 hourly 表存长周期、按 `TrafficHistoryDays`
  保留),但 `HistoryFor`/`NodeHistoryFor` 的读取**从没切过去**,仍读固定 7 天保留的原始
  5-min 表。于是管理端默认的 30 天视图、以及 UI 提供的 90/180/365 天范围,超过 7 天的桶
  全部返回 `{0,0,0}`(200 OK、无报错)。改:`HistoryFor`/`NodeHistoryFor` 改读
  hourly rollup —— 每个图表桶 = 落在其内的 UTC 小时 delta 之和(delta 可加,不再做
  累计计数器跨桶串接);新增 `ListHourlyByUser`/`ListHourlyByNode`(查询用 UTC 边界,
  规避 SQLite 时区字符串排序)。day/周/月图表现在真实覆盖到 `TrafficHistoryDays`。
  hourly 以 UTC 存储、按本地天求和 —— 整数偏移时区(如 +8)完全精确;半小时偏移时区
  在天边界至多错配 1 小时(hourly 粒度的固有取舍,rollup 一贯设计)。
- **hourly rollup 三表里 `client_*_hourly` 是只写不读的死存储**(存储修复)── 没有任何
  按客户端的历史图表读它(图表只有 user/node 两种),而它是最大的表。停止 roll up
  client 层(`rollupClient` 移除),只 roll up 被图表读的 user/node;已有的 client hourly
  行随保留期老化退场。这才是真正的存储节省 —— 被图表读的 user/node hourly 表本就很小,
  730 天保留也无压力,故默认保留期不变。
- **图表“今天”保持实时**:rollup 现在把当前未封口的 UTC 小时也纳入(幂等重跑),并在
  每次流量轮询后立即跑一次 `RollupOnce`(client 移除后开销很小),hourly 因此与原始
  5-min 轮询一样新鲜;每小时清理循环仍保留 rollup-先于-prune 的兜底。
- **文档订正**:`TrafficHistoryDays` 注释原称“0 keeps everything”,实际 `Load` 把 `<=0`
  统一强制成 730 天(无“永久保留”模式),prune 的 `> 0` 守卫永远见不到 0 —— 注释改为
  如实说明。前端两处“图表读原始表 / beta.7”的过时注释一并订正:Hour 粒度仍只在近 7 天
  可选(几千个小时点不可读),day/周/月已覆盖完整保留窗口。

### Fixed

- **3X-UI cookie 认证客户端的 401 重认证无界递归会拖垮整个进程**(HIGH)── cookie 模式下遇到
  401 会丢会话→重登→**直接重新调用 `doJSON`,无任何深度守卫**。当面板 `/login` 成功但受保护
  API 持续 401(反代路径级鉴权 / webBasePath 不匹配 / 会话密钥已轮换)时,非尾递归把 goroutine
  栈撑爆,以 fatal stack-overflow 终止进程 —— 这是 runtime-fatal 而非 panic,`safego` 的 recover
  拦不住,且 `doJSON` 是 `ListInbounds` 的咽喉(流量轮询 / reconcile 每周期对每个面板都跑),一个
  坏面板就能杀死整个 PSP。改为最多重认证一次:第二次连续 401 返回普通 auth 错误交由任务级
  退避处理。补 persistent-401 回归测试。
- **`SetPeriodUsage` 写入非单调快照,永久污染该小时流量 rollup 桶**(MEDIUM)── 管理员手动设
  本期用量时会写一条 total 低于当前小时已有快照的"base"行,成为桶 MIN,而 rollup 按 MAX-MIN
  计算,把整段周期用量算进这一小时;7 天后原始行被裁剪即无法纠正。改为只写当前值、本期用量
  仅靠 `PeriodBaselineBytes` 承载(`PeriodUsed()` 读它),不再写下界行。补回归测试。
- **inbound 上游已被删时 node-delete 任务永远循环、PSP 节点行永不删除**(MEDIUM)── 这是"管理员
  在 3X-UI 里直接删了 inbound"的常见孤儿场景:删除守卫返回的 `record not found` 不算永久错误 →
  1 分钟定速无限重试,`nodes.Delete` 永不可达。改为检测到 inbound 已不存在时跳过上游删除、清掉
  本地 ownership 行、删 PSP 节点(把 client-delete 的"已不存在=成功"幂等推广到 inbound)。
- **node 同步任务处理器缺最大尝试次数上限**(MEDIUM)── 非永久错误一律 1 分钟定速重试、无天花板。
  补 `maxNodeTaskAttempts=100`,镜像 user 处理器:超限即取消、保留末次错误供 Sync Tasks 查看,
  管理员手动 Retry 仍可覆盖。
- **`checkNodes`/`SetEnabled`/`DeleteAndSync` 用全行 Save,回退并发的 health/traffic/config 列写入**
  (MEDIUM)── 这三处对周期初读到的节点快照做全行 Save,会清掉并发循环刚写的列。新增列级写入器
  `UpdateEnabled`(只写 `enabled` 列),三处改用之,补 reconcile 断言测试。
- **两条"恢复缺失 client"路径对 VLESS flow 算法不一致,经 axis-B 恢复的 Reality 客户端 flow 为空
  且永不自愈**(MEDIUM)── axis B 用 `n.Flow` 直传,axis A 基于探测到的 `ce.flow` 再以 `n.Flow`
  覆盖。导入的 VLESS+Reality inbound 若 `Node.Flow` 留空,axis B 重建出 `flow=""` 的断连客户端,
  且 flow healer 因门控也永不纠正。抽出共享 `resolveFlow` helper 供三处复用,补单测。
- **SMTP 会话在 dial 之后无 I/O 截止时间,挂死的服务器会冻结整个串行提醒 / 公告循环**(MEDIUM)──
  `ctx` 只在 dial 的 select 里看,握手后整段对话无 deadline。dial 成功后对连接设
  `SetDeadline`(60s)覆盖 greeting→Quit。
- **拼装的 MySQL DSN 对 IPv6 主机字面量损坏**(MEDIUM)── 裸 `fmt.Sprintf` 无 IPv6 加括号,
  `::1` 被解析成 `[::1:3306]:3306` 连不上。改用驱动自带 `mysql.Config.FormatDSN`(经
  `net.JoinHostPort` 构造地址、对库名 PathEscape),与 Postgres 路径对齐,顺带修了库名含 `/`
  的转义问题。补 DSN round-trip 测试。
- **v3 迁移器写了已废弃的设置键名 `traffic_snapshot_retention_days`**(MEDIUM)── 该键在 v3
  已重命名为 `traffic_history_days`,迁移器仍写旧名,值被弃在死键上。改写为现行键名;新增
  `mysql.KnownSettingNames()` + 迁移器 drift-guard 测试(实跑 `copySettingsKV` against SQLite,
  断言写出的每个键都在现行 `settingDescriptors()` 集合内),给此前零覆盖的迁移器补上首个端到端测试。
- **sing-box DST-PORT 范围用了连字符格式被 sing-box 拒绝**(LOW)── Clash 的 `8000-9000` 直传成了
  sing-box 的 `port_range`,但后者要冒号语法 `8000:9000`。转换并校验两端为整数。补单测。
- **`oidc_settings.client_secret` 列是 `size:512` 却存加密 blob**(LOW)── 长 client secret 会截断
  (非严格 MySQL)/ 拒绝(PG / 严格 MySQL),损坏密文使下次 Load GCM 校验失败、SSO 启动中止。改
  `type:text`,与其他加密列一致,AutoMigrate 原地扩容。
- **`tryAdoptOrphan` 吞掉 `GetByPanelInbound` 的错误**(LOW)── 瞬时 DB 错误被当成"未被占用",可能
  误领已属他节点的 inbound。改为区分 `ErrNotFound` 与真实错误,后者上抛。
- **`Pool.Add` 直接存调用方指针,留下"别再动这个指针"的隐式契约**(LOW)── 改存防御性拷贝,
  消除与 `List()` 无锁字段读的潜在竞态。

### Security

- **审计中间件把非 JSON 请求体原样存档、不脱敏**(MEDIUM)── `/api/auth/local/login` 等审计路径上,
  form 编码的 `upn=x&password=...` 即便后续 400,明文密码也落进 operator 可读的审计表。改为:JSON 体
  照旧脱敏;form 体解析后跑同一套 key 脱敏;其余非 JSON 体只记形状(`{"unparsed_body":true,"len":N}`),
  绝不存原文。补三个回归测试。
- **SAML ACS 用未净化、可被攻击者控制的 `RelayState` 作登录后跳转目标**(LOW,防御纵深)── `RelayState`
  经 IdP 往返完全可控,ACS 此前未重新净化即拼进 `next=`。改为重跑 `sanitizeReturnTo` + `url.QueryEscape`;
  OIDC 回调同样补上 QueryEscape;`sanitizeReturnTo` 额外拒绝反斜杠。
- **空 `Value` 的 SSO 角色规则会空匹配**(LOW,提权脚枪)── 角色规则 `Value` 留空时会匹配 groups 属性里
  的空字符串元素,在授予 admin/operator 的规则上是提权隐患。`ruleMatches` 对空 `Value` 直接返回不匹配。补单测。
- **operator 提供的弱 / 耦合密钥材料无任何提示**(LOW)── 启动时对过短的 `jwt_secret`/`encryption_key`
  (< 16 字符)及"`encryption_key` 为空、复用 `jwt_secret` 作落盘密钥"两种情况打 WARN(`SecurityWarnings()`,
  纯函数、可单测),提醒耦合密钥下轮换 `jwt_secret` 会让落盘凭据全部解不开。

### Changed

- **容器以非 root 用户运行**(LOW,防御纵深)── 两个 Dockerfile 都新增 UID 10001 的 `psp` 用户、
  chown `/app` 后 `USER psp`(面板监听 8788 > 1024,不需特权端口)。绑定挂载的 config/data 卷需对该 UID 可写。
- **发布归档此前不含默认 rulesets / templates**── 打包步骤从 git-ignored 的 `config/{templates,rulesets}`
  拷贝(clean checkout 下不存在,失败被 `2>/dev/null||true` 吞),改从已提交的 `internal/seed/files/`
  拷贝(与二进制内嵌、首启释放的同源),并去掉 README / config 示例拷贝的静默吞错。
- **提交 `internal/web/dist/.gitkeep`**── 此前 `.gitignore` 的 `!internal/web/dist/.gitkeep` 否定规则指向
  一个并不存在的占位文件,导致全新 clone 下 `internal/web/dist/` 不存在、`go build ./cmd/panel` 因
  `go:embed all:dist` 直接失败。补上占位文件,前端未构建时后端也能编译。

## v3.6.2 — 2026-05-31

正式版。汇总 v3.6.2-beta.1 → beta.9 全部改动,beta.9 内容直发为正式版定稿。本次是
patch release:核心是适配 3X-UI 3.2.0(**硬切要求 ≥ 3.2.0**),叠加一个新功能(批量
升级 3X-UI)、compat 下限运行时化,以及多轮审计 / 回归修复,无 schema 破坏性变更。
完整逐项见下方各 pre-release 段落,下面只列核心叙事。

### 主要变化（叙事性总述）

- **适配 3X-UI 3.2.0,硬切要求 ≥ 3.2.0（beta.1 / beta.4）**:
  3.2.0 把客户端管理从 inbound 作用域端点整体迁到一等公民 `/panel/api/clients/*`,删掉了
  PSP 在用的 `addClient` / `delClientByEmail` / `getClientTraffics*` / `resetClientTraffic`
  等。xui adapter 迁到 `/clients/*`(按 email 寻址,而 PSP 的 `u{userID}-n{nodeID}@domain`
  本就每节点唯一、天然对上,`ports.XUIClient` 签名不变、服务层零改);traffic poll 去掉
  per-inbound fallback,Phase 2 全程零 3X-UI 网络调用。**在真实 3.2.0 面板的隔离临时
  inbound 上 live 实测**(add / update / del / inbound-update RMW 全跑通,测完删干净、未碰
  真实节点 / 用户),抓到并修掉 openapi 推导没暴露的 `tgId` string→int64 showstopper;
  reconcile 轴 A 反向推送经 live 验证(first-class client 完好、无孤儿 / 重复 / uuid 不变)
  后重新开启。逐项映射与实测见 `docs/3xui-3.2-clients-migration.md`。
- **Servers 页「批量升级 3X-UI」（beta.1）**:选中多台面板一键触发 3X-UI 自升级(镜像
  已有的批量升级 Xray)。**尊重版本门禁、不批量强制** —— 超出已测范围的面板被 gate 拦下
  并计入汇总,强制仍保留在单台 ⋮ 菜单逐台确认;已是最新版的面板短路返回、不再计为失败。
- **compat 下限运行时化 + 防忘 drift 闸（beta.8）**:硬切的最低 3X-UI 版本接入运行时 ——
  `ActiveMinXUI() = max(编译 const, JSON min_xui)`,平时只维护 `docs/compat/v3.json` 一处
  就能抬高下限;`TestMinXUIConstMatchesCompatJSON` 锁两处一致,drift 直接让 `go test` 红。
  修掉了 < 3.2.0 面板不报 too_old 警告的问题。
- **多轮审计 + 回归修复（贯穿 beta.2 → beta.9）**:每批改动后跑并行 review agent + 对抗
  验证,修引入的回归。关键项:refresh 端点不校验 TokenVersion → 会话撤销可被绕过(HIGH);
  关键词搜索跨方言 LIKE 转义最终统一用 `!`(三库通用),修掉中途以反斜杠 `ESCAPE '\'`
  引入、打挂全部 MySQL 部署关键词搜索的 1064 回归;应急配额已耗尽但时间窗未到仍发可用
  订阅(`/sub` 泄漏);被取消的同步任务仍执行 3X-UI 副作用、单任务记账出错会停掉整批;
  轮换 jwt_secret 让落盘密钥启动解不开且报错误导;编辑默认规则集冒出两条同 slug 重复行
  (Save 改按文档 slug 解析 + 前端 slug 重复校验,beta.9)。

## v3.6.2-beta.9 — 2026-05-28

### Fixed

- **编辑默认规则集会冒出两条同 slug 的重复行** ── 种子默认规则的文件名是 `default-rules.yaml`(连字符),
  但文件里的 `slug` 是 `default_rules`(下划线)。读取 / 删除按文件内容里的 `slug` 字段找文件(能命中
  种子文件),唯独 `RuleSetRepo.Save` 直接用 slug 拼文件名(`default_rules.yaml`),于是一编辑 / 保存默认
  规则,内容写进了一个**新文件**,种子文件原封不动 → 列表里出现两条一模一样的 `default_rules`。修:`Save`
  改成与读取 / 删除一致(按文档 slug 解析),一个 slug 永远只对应一个文件,编辑即干净覆盖、不再产生重复;
  新 slug 才回退到按文件名新建。前端"新建 / 复制"时另加一道 slug 重复校验(编辑态 slug 锁定不受影响),
  防止用已存在的 slug 静默覆盖原规则集。补 repo 单测锁定此行为。
  - 已经踩出重复行的部署需手动清理一次:`<ConfigDir>/rulesets/` 下保留一个文件即可(想留改动就把
    `default_rules.yaml` 的内容并回 `default-rules.yaml` 再删前者;想要纯净默认就删掉多出来的那个)。

## v3.6.2-beta.8 — 2026-05-28

### Fixed

- **MySQL 部署上所有关键词搜索报 1064 语法错误**(日志 / 用户 / 节点 / 分组 / 面板等全部失效)──
  keyword 搜索统一用的 `likeCols` 生成了 `LIKE ? ESCAPE '\'`,而反斜杠在 MySQL 字符串字面量里是
  转义符,`'\'` 把闭合引号转义掉 → 整条 SQL 语法错误;SQLite / Postgres 把反斜杠当字面量,所以
  本地默认 SQLite 后端从没暴露。把 LIKE 转义字符从 `\` 换成 `!`(三种方言里都是普通字面量),一处
  `likeEscapeChar` 跨所有后端通用,8 个搜索仓库自动跟修。补单测直接断言生成的 SQL 不含反斜杠、用
  `ESCAPE '!'`,挡住未来误改回去。
- **3X-UI < 3.2.0 的面板不报 too_old 警告** ── v3.6.2 把 client 适配硬切到 3.2.0 的 `/clients/*`
  API,但运行时最低版本其实从没抬到 3.2.0:`docs/compat/v3.json` 的 `min_xui` 改了却没接入运行时,
  代码里的 `MinXUI` const 也漏改(还停在 3.1.0),于是 3.1.0 面板被当 supported、无警告。修:`MinXUI`
  抬到 3.2.0,并把 JSON 的 `min_xui` 接入运行时(`ActiveMinXUI() = max(const, JSON)` 作安全网,只升
  不降);新增 `TestMinXUIConstMatchesCompatJSON` 一致性测试 —— const 与 JSON 的 `min_xui` 一旦 drift
  就让 `go test` 红,杜绝"改一处忘另一处"。
- **批量 / 单台远程升级 3X-UI 时,已是最新版的面板被算作"失败"** ── `UpgradePanel` 对
  `updateAvailable=false` 的面板仍调用 `updatePanel`,3X-UI 无更新可做 → 报错,前端 toast 把它计入
  "N failed"(一台已在 3.2.0 的面板就会触发)。`UpgradePanel` 在 `!info.UpdateAvailable` 时直接短路
  返回 `already_latest`;前端单台显示"已是最新版",批量把"已最新"单独计数,不再混进失败数。

## v3.6.2-beta.7 — 2026-05-28

审计 LOW 收尾(选定 3 项;其余 nit / 不可达的跳过)。

### Fixed

- **`POST /api/auth/refresh` 不进审计日志**(LOW,审计发现)── 登录有审计,但等价的"换发凭据"
  refresh 没有 —— 事后排查(如被盗 refresh token 持续续期)看不到这条。`shouldAuditPath` 加上该
  路径(body 里的 `refresh_token` 已被 `isSensitiveKey` 脱敏)。
- **`DelAllOwnedForInbound` 吞掉 per-client 删除错误 → 节点删除可能遗留孤儿 ownership 行**(LOW,
  审计发现)── 它原本 `_ =` 丢弃每个 `DelOwnedClient` 的错误、永远返回 nil,使 node-delete 任务里
  的错误检查形同虚设:某 client 删除瞬时失败时 ownership 行未移除,任务却继续 `DeleteInbound`(连带
  删掉 client)→ 留下指向已删 inbound 的孤儿行。改为返回 firstErr(同 `DelAllOwnedForUser`):失败
  即中止 `DeleteInbound`,任务下个 tick 重试收敛。
- **block-violation 计数读改写竞态(丢增量 / 重复 auto-disable)**(LOW,审计发现)── `/sub` 原来
  在请求加载的 stale user 快照上 `count++` 再以绝对值写回;两个并发 blocked 请求都读到 N、都写
  N+1(丢增量),且可能都跨阈值、各触发一次 auto-disable + 停用邮件。新增原子 `AdvanceBlockViolation`
  repo 方法:dedup 窗口放进 UPDATE 的 WHERE,自增 + 打时间戳一步完成,并发只有一个能推进
  (RowsAffected==1)→ 不丢增量、不双触发。替换原 `UpdateBlockViolation`,补 repo 单测(窗口门控)。

## v3.6.2-beta.6 — 2026-05-28

对审计 critic 标出的 4 个 MEDIUM 做对抗验证:Shutdown 派发竞态、rollup 全表读内存均**证伪**
(impact 前提不可达);secret-key 复用 jwt_secret **确认**(MEDIUM)、启动版本探测 single-flight
**确认**(nit),两个真的都修了。

### Fixed

- **轮换 jwt_secret 会让落盘密钥启动时解不开、且报错误导**(MEDIUM,审计发现)── legacy 配置
  (无独立 `encryption_key`)下 `jwt_secret` 兼作 at-rest 加密 key,而生成的 config 注释还鼓励轮换
  `jwt_secret`、没提这个副作用。轮换后 SAML/OIDC/SMTP/3X-UI 落盘密钥 GCM 解密失败 → 启动中止,报
  "decrypt database secret" 这种不指向恢复路径的错。修:`decryptSecret` 解密失败时报错明确给出
  恢复提示(轮换了就恢复旧 `jwt_secret` / 把 `encryption_key` 设成它);生成的 config.yaml 在
  `jwt_secret` 注释里警告 legacy 配置轮换会 brick。补单测断言报错含恢复提示。(consequence 2
  "空 key 静默明文" 未动 —— `validate()` 已硬拦空 `jwt_secret`,生产不可达。)
- **启动版本探测未纳入 single-flight 守卫**(nit,审计发现)── boot probe 直接调
  `probePanelVersionsOnce`、不设 `compatProbeInflight`;boot 探测慢(多个不可达面板 × 10s)还在跑
  时,第一个 traffic tick 的 reprobe 会并发再走一遍 per-panel walk。幂等(读 `/server/status` + 写
  相同版本)、无损,但与守卫"不重叠"的文档意图矛盾。boot probe 纳入同一 `CompareAndSwap` 守卫。

### Notes

- Shutdown 派发竞态、rollup 全表读内存经对抗验证均为**误报**:Shutdown 顺序保证 handler 的
  `bgWG.Add` happens-before `Wait`;rollup 的 OOM 需"prune 停摆但 rollup 继续"的非对称失败,而 DB
  挂则两者一起挂、raw 又硬上限 7 天。各遗留一个 LOW 优化点(rollup 三处 `Find` 可加 `WHERE
  captured_at < cutoff` / SQL 聚合省内存),本批不修。

## v3.6.2-beta.5 — 2026-05-28

### Fixed

- **应急配额已耗尽但时间窗未到的用户,`/sub` 仍发可用订阅**(MEDIUM,审计发现)──
  `/sub` 的 gating switch 只挡"应急窗口按时间过期",缺"窗口还在、但 per-window 配额已烧完"
  这一档。应急窗口只在下一个 traffic poll 周期(默认 5min)才被拆掉;在那之前 `/sub` 一直返回
  可用配置 + Subscription-Userinfo,面板侧授权闸漏掉了这个口子(3X-UI 自身 per-client floor 是
  兜底,但面板闸才是权威切断点)。新增 `domain.User.EmergencyQuotaExhausted(quotaBytes, now)`
  (仿 `IsExpired`,与 `emergencyFloor` / poll 拆窗口同一套 used = Lifetime - EmergencyBaseline
  算法,两层不会漂移),gate 用它在配额耗尽时返回 opaque 404。补 domain 纯单测 6 档(used==/>/<
  quota、无上限、时间过期、原因不符、无窗口)。

## v3.6.2-beta.4 — 2026-05-28

P2 live 写验证(在真实 3.2.0 面板的隔离临时 inbound 上实测,测完连 client 带 inbound 一起删
干净、未碰任何真实节点 / 用户)的修复与结论。

### Fixed

- **`tgId` 类型不匹配 → 对 3.2.0 的每次 client 新建 / 更新都失败**(showstopper)──
  `/clients/add`·`/clients/update` 要 `tgId` 为 int64,而 `buildClientJSON` 发的是 string
  (`s.TgID`),面板返回 `cannot unmarshal string into ... tgId of type int64` + `success:false`。
  这是 beta.1 迁移在真实 3.2.0 上的真正阻断点 —— openapi 推导没暴露、live 验证才抓到。PSP 不用
  3X-UI 的 Telegram 集成,`buildClientJSON` 改为发数字(`strconv.Atoi`,空 = 0);补单测断言
  `tgId` 序列化为数字而非字符串。

### Changed

- **reconcile 轴 A 反向推送重新开启** ── beta.2 把 `axisAReversePush` 默认关(漂移改"采纳"),
  是因为 `/inbounds/update` 重注入 `settings.clients` 在 3.2.0 一等公民模型下行为未验证。P2 实测
  复刻该 read-modify-write 后 first-class client 完好(`/clients/list` 精确 1 条、无孤儿、无重复、
  uuid 不变、仍在 `settings.clients`),证伪了 clobber 担心;`New()` 置回 `true` 恢复反向推送,
  `false`(采纳)保留为 kill-switch + 测试覆盖。

### Notes

- §4.1(`/clients/update` 整行替换会清 `subId` / `comment`)经 live 验证**证伪** —— 实为 merge:
  改 `totalGB` 时省略 `subId`,`subId` 仍保留。PSP 的 `UpdateClient` 省略字段是安全的,无需补。
- 迁移至此对真实 3.2.0 面板验证通过(add / update / del / inbound-update RMW 全跑通),可以打
  `v3.6.2-beta.*` tag。全部实测逐条见 `docs/3xui-3.2-clients-migration.md` §P2。

## v3.6.2-beta.3 — 2026-05-28

审计后续:对 3 个 critic 标出的 HIGH 做对抗验证 —— 2 个证伪、1 个确认(降为 MEDIUM)并修复。
证伪的两个:pool 凭据轮换 `Remove`+`Add` 非原子(窗口亚毫秒且良性,`xui.New()` 不做校验 /
网络 IO,坏凭据不会让 `Add` 失败,所谓"永久注销"前提不成立);`psp migrate` 不支持 Postgres
(不可达 —— migrator 只做 ≤v2.5.x→v3.0.0,而 PG 支持比 v3.0.0 晚两天落地,不存在 legacy PG 源库)。

### Fixed

- **被取消的同步任务仍会执行 3X-UI 副作用**(MEDIUM)── `MarkRunning` 用
  `.Updates(...).Error` 丢弃 `RowsAffected`,GORM 命中 0 行不报错。admin 在
  `ListDue`(选 Pending)→ `MarkRunning`(置 Running WHERE Pending)的窗口里点"取消",
  `MarkRunning` 实际 0 行却返回 nil,循环照样跑 `runUserTask` / `runNodeTask` /
  `processMailTask`,把 admin 刚取消的不可逆远程操作(删 inbound / 推配置 / 启停 / 发信)
  执行掉。修:`MarkRunning` 改返回 `(claimed bool, err)`(用 `RowsAffected`),user / node /
  mailer 三个 ProcessDueTasks 循环在 `!claimed` 时 `continue` 跳过副作用。补 repo 单测
  (Pending → claimed;Canceled → not-claimed 且不复活)。
- **单个任务记账 DB 出错会停掉整批 due task**(健壮性)── 三个循环里
  `MarkRunning` / `Cancel` / `MarkRetry` / `MarkSucceeded` 任一出错原本 `return` 整批退出,
  一次瞬时 DB 抖动就跳过本 tick 其余所有到期任务。改成 log + `continue`:出错的任务保持
  Pending、下个 tick(30s)重试,不连累同批其它任务。

## v3.6.2-beta.2 — 2026-05-28

多 agent 审计(覆盖迁移 + 鉴权 / `/sub` 热路径 / 数据层等)后的修复批。每条都经
对抗验证 + 补了单测。本批含 2 个与迁移无关的 HIGH(审计顺带挖出)。

### Fixed (security, high)

- **refresh 端点不校验 TokenVersion,会话撤销可被绕过** ──
  [auth_local.go](internal/transport/http/handler/auth_local.go) `Refresh` reload
  用户后只查 `!u.Enabled`,从不比对 `claims.TokenVersion`。而改密码 / 管理员重置
  密码 / 改角色都靠 bump `TokenVersion` 撤销会话(`RequireAuth` 对 access token
  有这道闸,refresh 路径没有)。后果:偷到的 refresh token 在受害者改密码后仍能
  换出全新有效 token,直到 refresh TTL(默认 7 天)耗尽 —— 打穿"改密码踢掉其它
  会话"的承诺。修:reload 后加一道 `u.TokenVersion != claims.TokenVersion → 401`,
  与 `middleware/auth.go` 的 access-token 闸对齐。
- **SQLite 上 LIKE 转义静默失效,含 `_`/`%`/`\` 的关键字搜不到** ──
  [pagination.go](internal/adapters/mysql/pagination.go) `keywordLike` 用反斜杠转义了
  LIKE 元字符,但 8 个搜索端点的 `LOWER(col) LIKE ?` 都没带 `ESCAPE '\'` 子句。
  SQLite(零配置默认后端)没有默认 LIKE 转义符 → 注入的反斜杠被当字面量、`%`/`_`
  仍是通配 → 搜 `u_5@x.org` 这类(下划线在邮箱 / 用户名里极常见)静默 0 命中或错配,
  影响用户 / 节点 / 分组 / 面板 / 分隔符 / 审计 / 订阅日志 / 邮件日志全部列表。
  MySQL / Postgres 默认反斜杠转义所以不受影响。修:抽出 `likeCols(exprs...)` 集中
  给每个谓词加 `ESCAPE '\'`(三库通用),8 个站点统一改用。补下划线字面匹配单测。

### Fixed (3.2.0 migration hardening)

- **`/clients/add` 重复 email、`/clients/update`·`del` 的 "client not found" 被当
  可重试错误,重试到上限** ── 3X-UI 把这类永久性 client 级拒绝以 HTTP 200 +
  `{success:false}` 返回,绕过了 `doJSON` 的 4xx→`ErrValidation` 包装,被 sync-task
  runner 当瞬时错误重试到 ~100 次(~1.5h)。修:`doJSON` 的 `success:false` 分支识别
  permanent 文案(duplicate / already exist / client not found)包成 `ErrValidation`
  快速失败,对齐 node.go "port already exists" 的处理;并修正 reconcile.go 一条
  "AddClient handles duplicates" 的假注释。
- **reconcile 轴 A 反向推送在 3.2.0 一等公民模型下未验证,默认关闭** ──
  `reconcileInboundConfig` 的 drift-push 走 `UpdateInbound` 读改写、会重注入
  `settings.clients[]`;在 3.2.0(clients 表为真相源、`settings.clients` 是派生投影)
  下这次写入行为未验证,可能孤儿 / 重复 PSP 托管的 client(见
  `docs/3xui-3.2-clients-migration.md` §4.3 / P2)。新增 `axisAReversePush` 开关,
  **默认关**:漂移时改为**采纳**(把 live 配置 capture 进快照、对 inbound 零写入),
  render 跟随 live 仍正确;代价是面板侧直接改动被跟随而非回退。P2 live 验证后置回
  `true` 即恢复反向推送(push 代码保留、仍有测试覆盖)。

### Fixed (frontend, low)

- **升级 3X-UI 的"已测范围外"409 网关会多弹一个原始红色 toast** ──
  `upgradePanel` / `upgradeXray`([servers.ts](web-react/src/api/servers.ts))未设
  `_skipErrorToast`,全局拦截器在**预期内**的 `untested_target` 409 上弹
  "Request failed with status code 409",与单条的强制确认弹窗、批量的"已拦截"汇总
  打架。修:两个调用都加 `_skipErrorToast`,由组件自己负责提示(单条 + 批量、
  含 submitXrayUpgrade 的重复 toast 一并消除)。
- **批量升级 3X-UI 后不清选中** ── 面板升级会重启面板(断连所有用户),完成后
  清空选中以防误触再次触发(与批量删除一致;批量测试 / 批量升级 Xray 因无破坏性
  保持选中)。

## v3.6.2-beta.1 — 2026-05-28

适配 3X-UI 3.2.0。3.2.0 把客户端管理从 inbound 作用域端点整体迁到一等公民
`/panel/api/clients/*`，删除了 PSP 在用的 `addClient` / `delClientByEmail` /
`getClientTraffics*` / `resetClientTraffic` / `delClient` / `copyClients`。
**硬切：v3.6.2 起要求 3X-UI ≥ 3.2.0**（沿用 v3.5.1「硬切 ≥3.1.0」先例，自用项目可控
对接版本）。设计与逐项映射见 `docs/3xui-3.2-clients-migration.md`。

### Changed

- **xui adapter 迁到 `/clients/*`**（`ports.XUIClient` 接口签名不变，服务层零改动 ——
  新 API 按 email 寻址，而 PSP 的 `u{userID}-n{nodeID}@domain` 本就每节点唯一，天然对得上）：
  - `AddClient` → `POST /clients/add`，body `{client, inboundIds:[id]}`。
  - `UpdateClient` / `UpdateClientWithInbound` → `POST /clients/update/{email}`
    （按 email、整行替换；不再读改写 inbound，`UpdateClientWithInbound` 的 GetInbound
    优化随之失效，降级为委托 `UpdateClient`）。
  - `DelClientByEmail` → `POST /clients/del/{email}?keepTraffic=0`。`/clients/del` 是
    全局删，但 PSP email 每节点唯一 → 实际等价单 inbound 删；ownership 守卫在 sync 层、
    调用前已生效，不受影响。
  - 删除生产零调用的死方法 `DelClient` / `CopyClients` / `GetClientTraffic` /
    `ResetClientTraffic` 及只剩 fallback 用途的 `GetInboundTraffics`，同步从
    `ports.XUIClient`、adapter、test fake 移除。
- **traffic poll 去掉 per-inbound fallback**：`getClientTrafficsById` 已被 3.2.0 删除；
  `ListInbounds().clientStats` 稳定返回每 inbound 的 per-client 计数，作为唯一来源，
  Phase 2 全程无 3X-UI 网络调用（顺手移除仅为 fallback 存在的 `pool.Get` 重解析块）。
- **compat 矩阵 `min_xui` / `max_tested_xui` 提到 3.2.0**（`docs/compat/v3.json` 新增
  `v3.6.2` 区间 entry，narrower-first；`v3.6.0`–`v3.6.1` 仍走 3.1.0 baseline，并标注
  不要拿旧 PSP 对接 3.2.0）。

### Added

- **Servers 页「批量升级 3X-UI / Upgrade 3X-UI on selected」**：选中多台面板一键触发
  3X-UI 自升级（镜像已有的「批量升级 Xray」，复用单端点 `POST /servers/:id/upgrade-panel`，
  后端零改动）。**尊重版本门禁、不批量强制** —— 目标版本超出已测范围的面板被 gate 拦下
  并计入汇总（`已发起 X / 拦截 Y / 失败 Z`），强制仍保留在单台 ⋮ 菜单逐台确认。配重启
  警告 confirm（每台面板重启、其上用户断连 ~30–60s，各自 smoke probe）。
  - 正确用法：先部署 v3.6.2（max_tested 提到 3.2.0）→ 全选 3.1.0 面板 → 一键升级，
    gate 放行无需 force。

### Notes

- `/clients/update` 是**整行替换**（非合并）：`buildClientJSON` 已带 PSP 管理的全部字段，
  但面板上手动加的 `comment` 等 PSP 不设的字段在更新时不再保留（PSP 托管的 client 不预期
  有手工字段）。
- reconcile 轴 A 的 inbound 配置回推仍走 `/inbounds/update`（读改写）；在一等公民模型下
  「写 `settings.clients` 是否被 clients 表二次投影覆盖」需在真实 3.2.0 面板 live 验证
  （设计文档 §4.3），本 beta 未改动该路径。

## v3.6.1 — 2026-05-28

正式版。汇总 v3.6.1-beta.1 → beta.10 全部改动，beta.10 内容直发为正式版定稿
（自 beta.10 起无追加修复）。本次是 patch release：v3.6.0「PSP 自动感知 3X-UI
版本」之上的统一分页骨架 + 两批全栈性能审计 + 安全加固 + 多轮回归修复，无
schema 破坏性变更。完整逐项见下方各 pre-release 段落，下面只列核心叙事。

### 主要变化（叙事性总述）

- **全部 admin 列表统一分页 + 列点击排序 + 关键字搜索（beta.1）**:
  Groups / Nodes / Servers / RuleSets / Templates 从「前端一把拉全集再 useMemo
  filter」拉到跟 Users / Audit / SubLogs 一样的分页骨架；新增 `usePaged` hook +
  `PagedTableFooter` / `SortableTableCell` 组件，分页 / 关键字 / 排序写
  querystring（刷新、分享链接保持视图），每页大小存 localStorage 跨列表共享。
  6 个 repo 补 `ListPaged` + 硬编码 sort 白名单防注入；`List(ctx)` 全集路径保留
  给 reconcile / traffic / render 内部调用。envelope 加 `page` / `page_size`
  字段，旧前端读 `items + total` 不破。
- **两批全栈性能审计（beta.4 + beta.6）**:
  热路径 settings 读放量是 `/sub` 公网端点最大单项成本 —— 加 `SettingsRepo`
  进程内缓存装饰器（invalidate-on-write，admin 存了立即生效无延迟窗口）、YAML
  template / ruleset 的 mtime-keyed 缓存、render 顶层一次性 `loadRenderSettings`
  穿透所有 helper。消 N+1：新增 `/admin/dashboard/summary` 聚合端点（不再下载
  全量 user / node 行）、admin list 的 settings.Load 提到循环外、group member 数
  改一次 `GROUP BY`、user ServerStatus 改 List 一次再内存索引、dashboard traffic
  Top 批量化（200+ round-trip → 2）。审计中间件改异步写（响应不等 fsync）、静态
  SPA 资源 init 时预读进 map、auth user cache TTL 5s → 60s。前端：vite vendor
  拆包（主 bundle 700KB → 165KB / gzip 48KB）、i18n lazy 加载、删 noto-sans-sc
  静态字体（392 文件 + 260KB CSS）、zustand per-field 订阅收紧重渲。traffic poll
  push 优先走 v3.5 本地快照（常态全 captured 时零 HTTP）。
- **安全姿态收紧（beta.2 + beta.3）**:
  `trusted_proxies` 默认从「信任全网」改成 loopback-only（fail-secure）—— 防公网
  直暴时匿名 client 伪造 `CF-Connecting-IP` / `X-Forwarded-For` 绕限流 / 污染
  audit IP；新增 `all` / `*` / `none` / cidr 四 token + boot WARN（**breaking**：
  反代 / CDN 后部署需显式填 proxy IP 段）。SSRF dialer guard 提到共享
  `internal/pkg/safehttp`，3X-UI 客户端 + GitHub compat 抓取共用，拒 loopback /
  link-local（含 `169.254.169.254` metadata）/ unspecified、保留私网段。
  `/sub/:token` 把 disabled / expired / blocked 等分支错误码统一收敛到 opaque
  `404 + 空 body`，杀掉匿名探针的 token 枚举 oracle（server 端仍 log 具体
  reason）；keyword LIKE 转义统一到 `keywordLike()`。
- **多轮回归审计修复（beta.2 / 3 / 5 / 7 / 8 / 9 / 10）**:
  每批改动后跑并行 review agent 修引入的回归。关键项：解封用户不清
  `BlockViolationCount` 导致账户被反复 auto-disable 困死（beta.10）；axios 拦截器
  把 `AbortController` cancel 误判成「网络异常」toast（beta.9）；CSP 默认放行
  Cloudflare Web Analytics beacon（beta.9）；dashboard summary 的
  `PageSize: 100000` 被 `applyPagination` 静默 clamp 到 200、250+ 用户实例 counter
  全错（beta.8）；`pollOwnedColumns` 漏 block-violation 三列致 admin 保存与 /sub
  计数互撞（beta.8）；beta.6 的本地快照 push fast-path 撞 `StripClients` 契约致每个
  captured node 推送全失败、彻底撤回（beta.7）；render prefetch goroutine panic
  导致 /sub 死锁（beta.7）；ServersView 版本探针无限循环（beta.2）；user 同步任务
  重试上限 100 防永久失败任务每分钟 hammer 3X-UI（beta.2）；前端分页 / 排序 /
  跨页选择一致性一组修复（beta.5）。

## v3.6.1-beta.10 — 2026-05-27

### Fixed

- **`SetEnabledAndSync` 解封时不清 BlockViolationCount,导致账户被困死**
  ── 终端用户被 `SubBlockAutoDisableCount`(默认 5)封了之后,DB 里
  `block_violation_count=5`。Admin 在 UI 点"启用",原代码只翻
  `enabled=true` + 改 `disable_detail`,计数原样保留。该用户下一次再用
  被禁客户端拉 /sub,count++ → 6 → 立刻 `>= 5` 阈值 → 再次
  auto-disable。账户陷入循环,admin 无法不打开数据库就让他逃出。
  修法:加 `UserRepo.ClearBlockViolation(userID)` 窄列写
  (count=0, last_block_violation_at=NULL, disable_detail=""),
  `SetEnabledAndSync` 在 `enabled=true` 分支调一次。必须走窄列写因为
  beta.8 起 pollOwnedColumns 把这三列从 `Update()` 的 Save 里 Omit 了
  (防 admin Save 跟 sub.go 并发增计数撞车)。失败只 log 不挡解封 ——
  解封比计数 reset 更重要。

## v3.6.1-beta.9 — 2026-05-26

### Fixed

- **axios 拦截器把 `AbortController` cancel 误判成 "Network error"** ──
  [client.ts:94-101](web-react/src/api/client.ts#L94-L101) 的 `categoriseError`
  只检查 `ECONNABORTED`(timeout)和 `!err.response`(network),但
  AbortController 抛的错误是 `err.code === 'ERR_CANCELED'` 且
  `err.response === undefined` —— 直接掉进 "network" 分支弹 toast。
  `usePaged` 的 effect cleanup `return () => ac.abort()` 在 deps 变化 /
  组件 unmount / StrictMode 双 mount 时都会 abort 老请求,这些 cancel
  全部错误地变成"网络异常"toast。表现:页面列表正常加载,底部却弹
  "网络异常"。修法:在 response error 拦截器最前面加
  `if (axios.isCancel(err) || err.code === 'ERR_CANCELED') return
  Promise.reject(err)`,跳过 toast。Cancel 是客户端主动信号,不该当成
  错误。beta.8 的 topTraffic silent 是治标(挡一个特定来源),这个
  isCancel 治本(挡所有 cancel 路径)。

### Changed

- **CSP 默认放行 Cloudflare Web Analytics beacon** ── CF 前置的部署里
  Web Analytics 会注入 `<script src=".../beacon.min.js">`
  + POST 到 `cloudflareinsights.com/cdn-cgi/rum`,被原 `script-src 'self'`
  + `connect-src 'self'` 全拦了。两个域名都是 Cloudflare 自家的(信任
  增量等于"用 Cloudflare 前置"本身),`script-src` 加
  `https://static.cloudflareinsights.com`,`connect-src` 加
  `https://cloudflareinsights.com`。非 CF 部署不受影响。

## v3.6.1-beta.8 — 2026-05-26

beta.7 之后再做一轮 2-agent 回归审计,找出 1 个 HIGH + 2 个 MED + 几个
LOW。HIGH 是 beta.6 引入的真 bug,MED 里有一项对应到 "click Users 报
Network error" toast 的间接来源。

### Fixed (real bugs)

- **Dashboard summary `PageSize: 100_000` 被 applyPagination 静默截到 200**
  ── beta.6 加的 `/admin/dashboard/summary` 端点用一次大 List 调用拉所
  有用户做 in-memory 聚合,但 [applyPagination](internal/adapters/mysql/pagination.go) 把
  PageSize > 200 的全部 clamp 到 200。装了 250+ 用户的实例上 UserTotal /
  UserEnabled / UserDisabled / UserEmergency 全错,排序 ID 在 200 之后的
  即将到期用户也永远不会出现在 dashboard 上。改成 200/页迭代,正确性
  恢复;长远改用 dedicated count 查询单独立项。
- **`pollOwnedColumns` 没包含 blocked-violation 列** ── beta.6 加了
  column-scoped `UpdateBlockViolation`(/sub 热路径只写 3 列),但
  `userRepo.Update`(Admin 编辑保存路径)用的 `Omit(pollOwnedColumns)`
  里这 3 列不在。admin 在 /sub 违规增长跟下一次 poll 之间点保存,
  block_violation_count 会被回退到 dialog 打开时的值 → auto-disable 阈值
  永远触发不了。把 `block_violation_count` / `last_block_violation_at` /
  `disable_detail` 三列补进 pollOwnedColumns。
- **`topTraffic` 失败弹全局 "Network error" toast** ── 打开 Users 页面、
  列表加载完全正常,底部却弹个 "Network error — please check your
  connection and try again" toast。根因是 UsersView / DashboardView 把
  `topTraffic` 当 best-effort 补充字段调用,JS 层用 `.catch()` 吞了
  promise rejection —— 但 axios 的全局 response 拦截器在 `.catch`
  **之前**就根据 ERR_NETWORK 弹了 toast。修法:给 `topTraffic` 加
  `opts: { silent?: boolean }`,silent=true 时通过 `_skipErrorToast: true`
  关掉全局 toast。Dashboard / UsersView 两处的 best-effort 调用都改成
  `topTraffic(N, { silent: true })`,辅助 console.warn 留 trail。

### Fixed (lower severity)

- **Dashboard node alerts 截断前没排序** ── 10 个不健康节点里,beta.6 是
  按 nodes.List 的 ID ASC 顺序取前 5 个 = 实际是 "ID 最小的 5 个" 而非
  "最近失败的 5 个"。改成按 HealthCheckedAt 倒序(最近失败排前),
  HealthCheckedAt 为 nil 的排末尾。
- **render.prefetchInboundsForRender 不响应 ctx 取消** ── shutdown 时,
  如果某条 ListInbounds 正卡在 30s 的 HTTP timeout(死的 3X-UI panel),
  接收循环用 counter-based `for ... <-resultsCh` 会傻等到那条 timeout
  超完。改成 `select { case <-ctx.Done(): break recv; case r := <-resultsCh: ... }`,
  shutdown 时立刻返回 partial map,后面的 flatten 步骤自然处理缺失 panel。
- **pushClientConfigToAll 在 panel 返回空 inbound 列表时清光 ownership**
  ── ListInbounds 成功但返回 `[]`(3X-UI 重启时偶尔会这样),Phase 2 会把每
  个 entry 都标 staleInbound → `ownership.RemoveByMatch` 全删。加防御:
  panel byInbound 长度为 0 + 无 err 时,这一轮 entry 全部 skip 不删,等
  reconcile 真确认了再清。
- **mailer maybeSend 已经在 beta.7 改过 render-first** —— 此版没动。
- **setLanguage 抛 promise rejection 不被处理** ── 部署新版本后老 tab 切
  语言时,如果 lazy chunk 拿不到(404),loadLanguageResources 的
  Promise.all 直接 reject;UserLayout / LoginView / LoginLocalView 都没
  `.catch`,UI 静默卡在原语言。setLanguage 内部加 try/catch:加载失败
  console.warn 后直接 return,不调 changeLanguage,语言状态保持一致。
- **Dashboard "0 / 0 健康" 文案** ── 零节点的全新面板会显示 "0 / 0 健康",
  读起来像坏了。`enabledNodeCount === 0` 时不渲染 subtitle。
- **删 `@fontsource/noto-sans-sc` 依赖** ── beta.6 已经把这个包的 import
  都删了,但 package.json 里 dep 还留着,clone + npm install 时还会
  下载 260KB 的字体文件到 node_modules。`npm uninstall` 干净。
- **`dashboardExpiringRow.expire_at` 类型对齐** ── 后端 always 写,前端
  类型却标了 optional,导致 DashboardView 里两处 `u.expire_at!` non-null
  assertion。后端去掉 `omitempty`,前端类型去 `?`,assertion 全部干掉。

### Verified clean (no action)

beta.7 几个 fix 都经独立 review 验证过没引入新问题:render.prefetch
panic-safe defer 顺序正确;mailer render-first 顺序正确;static
loadStaticAssets 的 sync.Once + log.Warn 行为正确;HIGH-2 dashboard
window 改 7 跟 UI 对齐没遗漏其他 14 引用;HIGH-3 font-family 去
"Noto Sans SC" 没有别处遗留;HIGH-1 D1 revert 干净,WithNodes / nodes
field / inboundcfg import 都清掉了。

## v3.6.1-beta.7 — 2026-05-26

beta.6 perf 批之后的回归 audit —— 2 个并行 review agent(后端 + 前端)找到
3 个 high + 2 个 med + 2 个 low。3 个 high 都是 perf 批引入的真 bug,必修。

### Fixed (real bugs from beta.6)

- **pushClientConfigToAll D1 路径完全不工作** ── beta.6 加的"本地快照
  fast path"撞上一个隐含契约:`inboundcfg.InboundFromNode(n)` 用的
  Settings JSON 是 `StripClients` 过的(v3.5 设计:本地快照只服务 render,
  client UUID 用 user.UUID 即时算出来不需要存)。当 push 路径把这个被
  剥的 inbound 喂给 `UpdateClient`/`UpdateClientWithInbound`,下游的
  `updateClientInSettings` 走 clients[] 找匹配 client → 数组是空的 →
  返回 `"client not found in inbound settings: email=… id=…"`。
  **每一个 captured node 的 push 全失败**:traffic poll 的 floor 下发、
  admin SetEnabled 的 3X-UI 通知、UpdateProfile 的 expire 改动全部断了,
  sync 任务还会 retry 到 100 次上限。彻底撤回 D1,回到 ListInbounds-per-
  panel 路径(D2 的 `UpdateClientWithInbound` 优化保留 —— ListInbounds
  返回的 inbound 是带 clients[] 的,从那里走的就没事)。
- **Dashboard "即将到期" 7/14 天不一致** ── beta.6 的
  `expiringWindowDays = 14` 跟前端硬编码的卡片标题 "即将到期(7 天内)"
  不对齐 —— 10 天后到期的用户会出现在 "7 天内" 的列表里,空状态文案
  "7 天内无到期用户" 也撒谎。后端改成 7 跟 UI 对齐。
- **theme font-family 还引用 "Noto Sans SC" 但 @font-face 删了** ── beta.6
  把 `@fontsource/noto-sans-sc/{400,500}.css` 删了(F3 字体优化),但
  [web-react/src/theme/index.ts](web-react/src/theme/index.ts) 的
  `fontFamily` 里那个名字还在。浏览器静默跳过这个找不到的 family,
  fallthrough 到下一个。Mac/Windows 有 PingFang SC / Microsoft YaHei 兜底
  没事,Linux 无系统 CJK 字体的桌面会渲成 tofu 框。把 "Noto Sans SC" 从
  font-family 去掉,顺手把注释更新成系统字体策略。

### Fixed (lower severity)

- **render.prefetchInbounds goroutine panic 会让 /sub 死锁(MED)** ──
  接收方是 counter-based(等 `len(panelInboundIDs)` 次 send),如果 goroutine
  内部某次 `pool.Get` 或 `ListInbounds` panic(罕见但不是不可能,比如
  xui_panel 配置导致 nil-deref),safego.Recover 吞掉 panic 但 resultsCh
  少一次 send → 接收方永远卡住 → /sub 整个请求 hang。改成 defer 里发结果,
  recover 也会把 result 设成 err 然后 send,保证每条 goroutine 一定有一次
  send。
- **UsersView usage map 在 pageSize 变化时双发请求(MED)** ── beta.6 把
  effect deps 写成 `[items, pageSize]`,但 usePaged 内部已经会在 pageSize
  变时刷 items,所以双 dep = 一次 pageSize 改动两次 fetch。`usageSeq`
  guard 保证显示对,但浪费一次 HTTP。去掉 dep 数组里的 pageSize,
  pageSize 在 effect fire 时直接读最新值。
- **mailer maybeSend ReserveSentSlot 在 render 之前(LOW)** ── beta.6
  把 HasSent+RecordSent 换成 ReserveSentSlot 之后,顺序变成
  reserve → render → send。SMTP 失败被 at-most-once 吃掉是设计意图,
  但模板**解析失败**(admin 把模板写坏了)同样被吃掉就不对 —— slot 已
  占,下个 cycle HasSent 仍是 true,这个 window 永远不发。换成
  render-first / reserve-second,跟 SendBlockedClientWarning 的顺序一致。
- **static.go WalkDir 错误静默(LOW)** ── `sync.Once` 把瞬态 ReadFile
  失败固化成进程生命周期的 404。加 log.Warn 给 admin 留 trail,顺手补
  "index.html 缺失" 的硬警告(SPA 没它就全 404)。

### Skipped (intentional)

- **i18n LanguageDetector 跟 explicit lng 并存**:harmless 冗余,
  resolveInitialLanguage 实际是 querystring → localStorage → navigator
  顺序对的复制,LanguageDetector 在 init 时被 lng 覆盖,只有 caches:
  ['localStorage'] 还在生效(语言切换会写 localStorage)。
- **setLanguage 不 await**:首次语言切换有 200ms flash(picker 已经更新
  但 t() 还是旧值),加 loading indicator 收益不值复杂度。
- **DropIndex idx_users_email 在 Postgres 上的命名差异**:已经注释 best-
  effort + WARN-only,该索引"留下也无害,只是浪费写"。
- **vendor 拆 axios/zustand/qrcode.react**:total ~50KB,主包已经从 700KB
  砍到 165KB,边际收益太小。
- **DashboardView t() 多写 admin: 前缀**:style,t() 行为不受影响。

## v3.6.1-beta.6 — 2026-05-26

第二轮全栈性能审计 —— 4 个并行 audit agent 覆盖 HTTP / DB / workers /
frontend,合并去重后 ~30 项。本批次按动手成本 vs 收益筛掉 ~6 项后实现剩
下的 ~24 项。两条 high 项主动 defer:rollup 增量化(需重设计避开 SQLite
TZ 坑)、/sub cheap-ETag 短路(需统一的 UpdatedAt change-tracking 跨
user/group/nodes/settings/template,跨度太大)。

### Performance — backend hot path

- **`paneltz.LocationOf` 加 `sync.Map` 缓存** ── `time.LoadLocation` Go
  stdlib 每次都重新解析 zoneinfo 表,没有内部缓存。每次 DTO mapping /
  history query 都付一次解析成本。`*time.Location` 一旦得到就不可变,直接
  用 `sync.Map[string]*time.Location` memoize。负路径(blank / 无法解析)
  也按字面 key 缓存,fast path 兼顾。
- **YAML template / ruleset repo 加 mtime cache** ── pre-fix 每次 `/sub`
  render 都对 `config/templates/*.yaml` + `config/rulesets/*.yaml` 全部
  `ReadDir + ReadFile + yaml.Unmarshal`(典型 5 templates + 3-5 rulesets
  ≈ 10 次 disk I/O + 10 次 YAML 解析)。改成 mtime-keyed `sync.Map`:Stat
  一次(syscall),如果文件 mtime 没变就直接返回缓存的 `*domain.Template`。
  Save / Delete 各自 Delete cache entry。Admin 手动改 YAML 文件后下次读
  自动失效(mtime 变了)。
- **render 服务 `Settings.Load` 一次性加载** ── 一次 mihomo render 调
  `Settings.Load` **6 次**,singbox / urilist 各再 2-3 次。即使 beta.4 加
  了 cache 装饰器,每次调用仍要 RWMutex.RLock + UISettings 结构体拷贝 +
  `applyUISettingsDefaults`。引入 `loadRenderSettings(ctx)` 在
  `RenderForUser` / `renderSingBox` / `renderURIList` 顶层 load 一次,
  穿透给所有 helper(profilePlaceholders / buildProxies / buildProfileName /
  resolveInbounds / prefetchInboundsForRender)。后续每次 render 只 Load
  一次 settings。
- **`prefetchInboundsForRender` 信号量先获取再起 goroutine** ── 原代码
  对每个 panel 无条件 `go func` 然后 goroutine 内部才 `sem <- struct{}{}`
  阻塞,大 fleet 下会一次性分配 N 个 goroutine,大部分立即阻塞在 channel
  send。改成 acquire 后再 spawn,goroutine 数量也跟着 cap。

### Performance — admin handler N+1

- **`/admin/dashboard/summary` 新增聚合端点(F6)** ── pre-fix dashboard
  开打 fetch `listUsers({page_size:500}) + listNodes({page_size:500}) +
  listGroups()` 只为算 4 个 counter tile + 5 行 expiring + 5 行 node
  alerts。新增 `AdminDashboardHandler.Summary` 在服务端聚合,
  return 只含 counters + 两个 5-row 列表的小 payload。前端 DashboardView
  改用 `dashboardSummary()`,不再下载全量 user / node 行。
- **`admin_user.List` 解一次,toDTO 用一次** ── pre-fix List 里每行调
  `h.toDTO` →`h.settings.Load`(EmergencyQuotaGB + Timezone)+
  `h.subURLFor` → 再 2 次 settings.Load(SubBaseURL + SubPath)。
  page_size=25 = 75 次 Load + 25 次 tz 解析。拆出 `toDTOWith(u, st, loc,
  subBase)` 纯映射版本,List 在循环外解析一次共享值。
- **`admin_group.List` 改一次 GROUP BY 取 member 数** ── 原来 List 拿到
  N 个 group 后,每个 group 单独 `CountMembers` (SELECT COUNT 1)。
  page_size=25 = 26 个 SELECT。新加
  `groupRepo.CountMembersByGroups([]int64) → map[int64]int64`,一次
  `GROUP BY group_id` 取全部。
- **`user_me.ServerStatus` 改 List 一次再 in-memory 索引** ── pre-fix
  按 ownership 每条 `nodes.GetByPanelInbound`,大组用户每次刷新一打
  SELECT。改成 `nodes.List(ctx)` 一次然后 `map[[2]int64]*Node` 索引。
  几百节点规模带宽成本远低于消掉的 round-trip。
- **`admin_traffic.Top` / `NodesTop` 批量化(B1 部分)** ── 加
  `TrafficRepo.LastBeforeForUsers` + `NodeTrafficRepo.LatestForNodes /
  LastBeforeForNodes`(MAX(id) GROUP BY 复用)+ `traffic.Service`
  上的 `ReportForUsers` / `NodeReportForNodes`。dashboard 一次 100 个
  user 从 200+ round-trip 收缩到 2(LatestForUsers + LastBeforeForUsers)。
  History 端点暂未批量化(需 ListByUser 批量版本,留待后续)。

### Performance — middleware + static

- **审计中间件改异步写入** ── 每个 admin write(POST/PUT/PATCH/DELETE)+
  每次 local-login + 每次 /api/user/me 写都阻塞在同步 `audit.Insert`
  fsync 上(SQLite ~5-50ms / 次)。引入 `middleware.AsyncDispatch` 函数
  类型,通过 router 把 `d.Async.Go` 注入。审计 INSERT 在请求线程外丢给
  panel-wide bgWG 跟踪的 goroutine,响应不再等 fsync。
- **静态资源 init 时预读 + 预算 Content-Type** ── 原 `StaticSPA` 每个请
  求 `fs.Sub + fs.ReadFile + mime.TypeByExtension + filepath.Ext`。SPA
  bundle 是 go:embed 不可变;改用 `sync.Once` 在第一次请求时
  WalkDir 全部读入 `map[string]staticAsset{body, contentType}`,后续
  请求纯 map 查。
- **auth user cache TTL 5s → 60s** ── pre-fix 任何 polling client >
  1 req/5s 都打穿 cache → 每请求一次 DB user lookup。bump 到 60s。
  TokenVersion 撤销的"撤销-到-生效"窗口同步扩到 60s 上限,自用面板可
  接受(admin disable / role-demote 都是罕见事件)。

### Performance — background workers

- **`pushClientConfigToAll` Phase 1 优先走本地快照(D1)** ── traffic
  poll 把每个 user-with-delta 都 push 配置,每次 push 都 `ListInbounds`
  per panel。pre-fix N=100 active users × 2 panels = 200 次冗余
  ListInbounds / 5 分钟 cycle。改成:有 inbound 本地快照(v3.5 captured)
  的 ownership 直接走 `inboundcfg.InboundFromNode(n)`(零 HTTP);只对
  未 capture 的 ownership 走 ListInbounds,且只对真有未 capture entry
  的 panel 发请求。常态(全部 captured)零 HTTP。
- **`UpdateClientWithInbound` 新增(D2)** ── pre-fix 每次 xui
  `UpdateClient` 内部都 `GetInbound`(read-modify-write 需要老 settings),
  即使 caller 刚 ListInbounds 完。新加 `ports.XUIClient.UpdateClientWithInbound`
  接受 caller 已经在手的 inbound,以及 sync.Service 的
  `SetOwnedClientEnableWithInbound` 变体。`pushClientConfigToAll` Phase 2
  push 用预取的 inbound,UpdateClient 内部不再 GetInbound。
- **mailer `maybeSend` 改用 `ReserveSentSlot`(D4)** ── pre-fix 是
  HasSent(Count) + send + RecordSent(Insert)两步并有 TOCTOU race
  (两个并发 cycle 都看到 HasSent=false → 双发)。`ReserveSentSlot` 已经
  存在(SendBlockedClientWarning 在用),是单 INSERT ... ON CONFLICT
  DO NOTHING 的原子操作。语义改成 at-most-once(SMTP 失败 = 这个 window
  不重试),跟 blocked-client warning 的策略一致。

### Performance — sub.go

- **blocked-client 计数改窄列写入(A5)** ── `/sub` 是公开端点最高 RPS
  写路径。pre-fix 每次违规计数都 `h.users.Update(u)` 全行 Save 重写 30+
  列 + 全部二级索引(upn / sub_token / sso / group_id)。新加
  `UserRepo.UpdateBlockViolation(ctx, id, count, lastAt, detail)` 只写
  3 列。

### Performance — frontend

- **vite `manualChunks` 拆 vendor**(F1) ── 主 bundle 从 ~700KB 降到
  165KB(gzip 48KB)。`vendor-react` 226KB / `vendor-mui` 348KB /
  `vendor-echarts` 507KB / `vendor-i18n` 58KB 各自独立缓存,小改动不会
  invalidate 所有 vendor 包。
- **i18n 改 lazy 加载**(F2) ── pre-fix 7 namespace × 2 语言全部静态
  `import` 进主 bundle(`admin.json` 一边 ~57KB,两语言双倍)。重写成
  `import.meta.glob` + 启动时只 load 当前语言 + namespace,语言切换时
  按需 fetch。
- **删 `@fontsource/noto-sans-sc` 静态 import**(F3) ── pre-fix 每个
  weight 拉 196 woff/woff2 unicode-range subset,total 392 字体文件 +
  260KB CSS(~400 个 @font-face 声明)。theme 的 font-family stack 已经
  覆盖 system CJK(PingFang SC / Microsoft YaHei / Hiragino Sans),
  Chinese-reading 平台都自带这些。可选自定义 subset 留待生产化部署。
- **UsersView topTraffic limit 按 pageSize cap**(F4) ── pre-fix 每次
  items 变化就 `topTraffic(1000)`,完全无视实际显示的行数。改成
  `Math.max(pageSize, 25)`,跟显示行数对齐。
- **AdminLayout zustand store per-field 订阅**(F5) ── pre-fix
  `useAuthStore()` / `useSiteStore()` / `useAppearanceStore()` 不带
  selector,任何字段变化都触发整 AdminLayout(含 nav drawer + AppBar)
  重渲。改成每个字段 `useStore(s => s.x)`,实际依赖收紧。site.load 的
  effect deps 也从 `[site]`(每次更新都是新引用)改成 `[siteLoad]`(action
  ref 稳定)。

### Schema (AutoMigrate)

- `mail_sent.sent_at` 加索引 `idx_mail_sent_at`:hourly retention
  DELETE WHERE sent_at < ? 不再全表扫(uk_mail_once user_id 在前不能用)。
- `sync_tasks.finished_at` 加索引 `idx_task_finished`:同上,
  DeleteSucceededBefore / DeleteFinished 由表扫降为 index scan。
- `users.email` 上的自动 `idx_users_email` 索引删除:实际 query 全部
  走 `LOWER(email) LIKE ?`,B-tree index 没法用。`cleanupLegacyState`
  里 DropIndex 老 install 跟着清。

### Deferred (intentional)

- **rollup 增量化(D3)** ── 当前每小时 `SELECT * FROM client_traffic_snapshots`
  无 WHERE 拉全表再 Go 端 filter cutoff。代码注释里已说明 SQLite zoneinfo-
  string 存储的 lexicographic vs semantic order 问题让 SQL 端
  `WHERE captured_at < ?` 在跨 TZ 数据上不可靠。增量化(watermark + 重叠
  buffer)需要独立设计 + 跨 dialect 测试,留待后续。
- **/sub cheap-ETag 短路(A4)** ── 需要 user / group / 各 node /
  settings / template 的统一 change-tracking signal(类似 max(UpdatedAt))。
  现有 schema 缺这层抽象。A1+A3 已经把 per-render 成本砍到很低,
  ETag 现状(body hash)的痛点已经不那么尖锐。

### Test fakes

- `fakeUserRepo` / `memoryUserRepo` 加 `UpdateBlockViolation`。
- `fakeTrafficRepo` 加 `LastBeforeForUsers`;`fakeNodeTrafficRepo` 加
  `LatestForNodes` / `LastBeforeForNodes`。
- `fakeXUIClient` 加 `UpdateClientWithInbound`。

## v3.6.1-beta.5 — 2026-05-26

beta.1-4 改动后的回归 audit 找到 8 项 —— 真 bug 4、行为不一致 4。全部一起修。

### Fixed (real bugs)

- **UsersView 切组下拉不刷新列表** ── beta.1 把 UsersView 接到 `usePaged`,
  fetcher 用 `useCallback([groupFilter])` 在 closure 里捕获 groupFilter,但
  `usePaged` 把 fetcher 存 ref 里、effect deps 只盯
  page/pageSize/keyword/sort —— 切换组下拉时 closure 更新了但不触发 refetch,
  admin 看到的是上一组的用户列表。加 `useEffect([groupFilter])` 调
  `paged.refresh()`。
- **Settings cache Save 并发可能让 cache 跟 DB 不同步** ── beta.4 装饰器在
  `inner.Save` 之后用 `mu.Lock` 写 cache,两步不原子:Save(A) + Save(B) 同时跑,
  DB 落在 (A,B) 顺序、cache 落在 (B,A) 顺序时 → DB=vB / cache=vA 一直撑到下次
  Save。改成 invalidate-on-success(`cached=nil`)而非 overwrite —— 下一次
  Load 强制走 inner 重读 DB,逻辑上保证 cache 永远是 DB truth 的子集或为空,
  不可能保留 stale 数据。代价是每次 Save 后多一次 inner.Load(rare event,影响
  可忽略)。
- **TemplatesView 全选抹掉所有 page 的选择** ── beta.1 的 toggleAll
  `setSelected(checked ? new Set(selectableSlugs) : new Set())` 直接替换整个
  Set,跟 ServersView / RuleSetsView / GroupsView 的 per-page 模型不一致。改成
  union/delete 当前页 IDs 的写法,跨页选择保持。
- **trusted_proxies 文档反向引导** ── beta.2 把默认从"信任所有"改成
  loopback-only,但 [internal/config/config.go](internal/config/config.go) 和
  [config/config.yaml.example](config/config.yaml.example) 的注释还写着
  *"Default (unset): zero-config — trust all upstreams"*。Cloudflare / nginx
  后面的部署者照着 example 留空,真实 client IP 会全部退化成 proxy host。两个
  文件的注释全部重写,加上 `all` / `*` / `none` / cidr list 四个 token 的说明
  + boot WARN 提示。

### Fixed (behavior consistency)

- **GroupsView header checkbox 跨页操作** ── beta.1 把 `selectableIds` /
  `allChecked` / `toggleAll` 接到 `filteredItems`(全 filtered 集合)而非
  `pagedItems`(当前页),admin 在 page 1 点"全选"会静默勾上其他 page 上的
  行(屏幕外看不到)。改 `selectableIds = pagedItems.filter(...)`,行为跟其他
  view 的 per-page 一致。
- **usePaged 浏览器 Back/Forward URL 不更新 state** ── 原 hook 只在 mount 时
  读一次 URL params,后续 history navigation 改了地址栏但 state 不跟,UI 显
  示和 URL 不一致(`?q=tw` 但表里没过滤)。新加反向 `useEffect([params])`
  把 URL → state 推回去,React 的 same-value setState bail-out 保证不会跟现
  有的 state → URL effect 形成循环。
- **render.prefetchInbounds 没读 admin 的 MaxPanelConcurrency** ── beta.4 加
  semaphore 时硬编码 `paneltz.ResolveMaxPanelConcurrency(0)`,忽略 admin 在
  Settings 里配的值 —— 跟 traffic.PollOnce / reconcile.RunOnce 不一致。借助
  beta.4 的 settings cache(读已经免费了),Load 一次取 `cfg.MaxPanelConcurrency`
  喂进去。
- **reconcile fallback 区分 batch-error vs empty-rows** ── beta.4 把
  `ownership.ListByUsers` batch 加进 RunOnce,但 fallback 条件用
  `entries, ok := ownershipByUser[u.ID]; if !ok { ... ListByUser }`,把"用户
  本来就没 ownership 行"也当成 batch miss → 每个零行用户每 cycle 一次额外
  SELECT,部分抵消了 N+1 修复的收益。引入 `batchOK bool`,只在 batch 失败
  时 fallback;成功 + 用户无行的正常状态直接跳过。

### Test fix

- `render_test.go`'s nil-Settings fixture would panic on the new
  `s.repos.Settings.Load(...)`. Nil-guard added in
  `prefetchInboundsForRender` (settings.Load failures already gracefully
  degrade to the default concurrency cap; nil repo behaves the same).

## v3.6.1-beta.4 — 2026-05-26

第三批审计后续 —— 性能优化 4 项。所有改动都在 hot path 上,目标是把订阅
端点 + traffic poll + reconcile 三条路径的 DB / 网络 round-trip 砍下来。

### Performance

- **`SettingsRepo` 加进程内缓存装饰器** ── 主推改动。`internal/service/render/render.go`
  每次 `/sub/:token` 请求会调 `Settings.Load` **4-6 次**(region-flag /
  profile placeholders / update-interval / buildProxies / buildProfileName /
  traffic snapshot 各一次),每次都是 `SELECT * FROM settings` + ~40 个 KV 行
  unmarshal。订阅端点是公网热路径(每个 proxy client 几分钟轮询一次),settings
  表读放量曾是这条路径上最大的单项成本。
  - 新文件 `internal/adapters/mysql/settings_cache.go`:`cachingSettingsRepo`
    用 `sync.RWMutex` 保护一个 `*ports.UISettings` 缓存
  - Load 走缓存命中路径:返回值的零字段用 caller-supplied defaults 兜底,
    保留 render/mailer 那种"传 SiteTitle=... 作为 fallback"的语义
  - Save 是 invalidate-on-write 而非 TTL:admin 在 Settings 页存了之后,
    下一次 `/sub` 立即看到新值,**无延迟窗口**
  - 模式跟 [router.go subPathCache](internal/transport/http/router.go) 一致
- **`render.prefetchInbounds` 加上 panel concurrency semaphore** ── 之前每渲
  染一次 `/sub`,一个组里覆盖 N 台 panel 就 spawn N 个 goroutine 同时打 3X-UI
  的 `ListInbounds`,没有上限。订阅端点是公网热点,一波 polling client 同时
  到达可以把 3X-UI 打哑。改成跟 traffic / reconcile 一样的
  `paneltz.ResolveMaxPanelConcurrency()` (默认 8) sem
- **`reconcile.RunOnce` 砍掉 N+1** ── 原 user loop 里:
  - 每个 user 调 `groups.GetByID(u.GroupID)` 一次 → 单次 reconcile N 个 SELECT
  - 每个 user 调 `ownership.ListByUser(u.ID)` **两次**(checkMissingOwnerships
    一次 + 主 scan 一次)→ 2N 个 SELECT

  现在:
  - 进 user loop 前 `groups.List()` 一次扔进 map 复用
  - 每页 user 进 loop 时先 `ownership.ListByUsers(userIDs)` batch 一次(同
    traffic poll 已有的 pattern),loop 内查 map
  - 总开销从 `3N+constant` 降到 `2+constant per page`
- **paged list 跳过 unneeded `COUNT(*)`** ── audit / sub_log / mail_sent 三个
  大表 list 时,前置 `COUNT(*) ... WHERE LIKE '%kw%'` 是单请求里最贵的查询
  (LIKE 不走索引,全表扫)。新 helper `inferTotalOrCount`:
  - admin 在 page 1 AND 返回行数 < page_size → 推断 total = len(rows),**跳过 COUNT**
  - 其他情况(page > 1 或 page 1 满) → 走 COUNT
  - 用 `q.Session(&gorm.Session{})` 拷贝 query 给 paginated Find,保证 Count
    跑在 unmodified WHERE 上

  实测对小数据集没什么变化(本来就快),但 30 万行 sub_logs + LIKE 关键字
  搜索从 ~300ms 降到 ~10ms

### Notes

- settings cache 不带 TTL,只走 Save invalidate。理由:UISettings 这种全局
  配置只有 admin 显式 Save 才变;TTL 会引入"admin 存了 settings 但 N 秒内
  /sub 还在用旧值"的怪行为。Save 直接灌新值绕开这个
- reconcile 的 `checkMissingOwnerships` 拆出 `checkMissingOwnershipsWithCtx`
  新签名带预加载入参,老签名删掉。如有 group 不在预加载 map 里(并发删除等
  race),依然降级到旧的 `groups.GetByID` on-demand 路径
- inferTotalOrCount 只对真正大表生效(audit / sub_log / mail_sent),其他
  小表(group / template / ruleset / xui_panel 等)还是 count-first,因为它们
  count 本来就 < 1ms,优化没意义

## v3.6.1-beta.3 — 2026-05-26

第二批审计后续修复 —— 行为 bug 4 项 + 安全加固 2 项。最贵的 render.Settings.Load
缓存(audit #8)留到 beta.4 单独 ship 配合测试。

### Security

- **SSRF dialer guard 拉到共享 `internal/pkg/safehttp` 包,xui adapter +
  GitHub 抓取共用** ── pre-beta.3 的 3X-UI 客户端 + `latest_xui` /
  `compat_remote` 都用 `http.DefaultClient`,没有 dialer 防御:
  - 3X-UI panel URL 是 admin-supplied DB content,被改成 `http://127.0.0.1:...`
    后,面板会被骗去代理打内网未授权 endpoint
  - GitHub fetch 的 `RefreshRemoteCompat(ctx, urlOverride)` 收 admin
    override,虽然今天只内部调,但日后挂 UI 就是 SSRF 入口

  共享 `safehttp.NewClient()` 装 `BlockNonPublicDial`:拒绝 loopback / link-local
  (含 `169.254.169.254` cloud metadata)/ unspecified 地址,**保留** 10/8 /
  172.16/12 / 192.168/16 私网(自部署 3X-UI 合法地址段)。auth 包里早期那份
  `ssrf.go` 暂留(SAML/OIDC 路径在跑,不在本 beta scope),后续合并
- **`/sub/:token` 错误码统一到 opaque 404 杀掉 enumeration oracle** ── pre-
  beta.3 unknown token 返 404,disabled / expired / emergency_expired / auto-
  disabled-by-blocked-client 全返 403 + 详细 body。匿名探针扫随机 token 只需
  看 status 就知道哪些是真实用户(`403 = valid token, account suspended`)。
  现在 4 个"valid token but blocked"分支都收敛到跟 unknown token 一样的
  `404 + 空 body`,server 端 `log.Info` 仍带具体 reason 供 admin 排查
  - 唯一保留 `403 + body` 的是 `client not allowed`(用户用了不在 whitelist
    的客户端,需要换 app —— 这条对用户必须可见)

### Bug fixes

- **compat re-probe 不再阻塞 traffic poll tick** ── 之前 `probePanelVersionsOnce`
  inline 跑在 `runTrafficLoop` 内,N 台不可达 panel × 10s timeout 就会把下一个
  traffic tick 推迟若干分钟。现在 fan 进独立 `safego.GoTracked` goroutine,
  外加 `atomic.Bool` single-flight guard 防止前一个 cycle 还在跑时下一个 tick
  再叠加(同 panel 并行 N 次 GetServerStatus)
- **`traffic.PollOnce` settings.Load 失败保留上次 cached pollCfg + WARN** ──
  原行为是失败就把 `pollCfg` 静默归零,导致 `EmergencyAccessQuotaGB`/自动禁用
  阈值等整个 cycle 失效,admin 没有任何 log 可 grep。改成 Service 持有
  `pollCfgCache`(RWMutex 保护),失败时回退到上次成功值 + Warn log
- **`/sub` 写 `sub_logs` 改成异步入队** ── 之前每次 sub 请求同步 INSERT,
  fsync-bound 写入挂在请求路径上;polling client 5 分钟一次 × N 用户的写率是
  公网端点最高的表。新加 `SubHandler.logSubAsync` helper 走 `h.async.Go`,请
  求当场 return,row 在后台落库
- **keyword search LIKE 转义统一到 `keywordLike()` helper** ── audit /
  sub_log / mail / user 4 个 repo 之前各自拼 LIKE pattern,user_repo 转义了
  `%`/`_`/`\` 三个 meta,其余 3 个没转义 → admin 在搜索框输入 `_` 会触发
  unexpected substring match + 全表扫描(非 SQL 注入,但 UX + 性能问题)。
  全部归一到 `keywordLike()` —— escape + lowercase + 加 `%...%` wrap 一步到位

### Notes

- 单 flight guard `compatProbeInflight` 用 `sync/atomic` 而非 mutex,因为只是
  布尔旗标 + CompareAndSwap fast path,锁本身的等待比 probe 慢得多
- traffic 的 `pollCfgCache` 在第一次 settings.Load 之前是零值;只有当第一次
  load 就失败时才会用零值跑一次。pre-beta.3 是每次失败都用零值,beta.3 后是
  只有第一次失败用零值
- safehttp 包未 export 一个 `metadataClient` 等 shorthand —— SAML/OIDC 那边
  自己有 15s timeout 偏好,xui 30s,version 30s,直接传 timeout 比 shorthand
  灵活

## v3.6.1-beta.2 — 2026-05-26

### Fixed

- **ServersView 探针无限循环（beta.1 引入）** ── 页面打开后 `useEffect([items])`
  里跑 `Promise.allSettled(items.map(probeServer))`，而 `probeServer` 拿到 Test
  返回后会 `mutateItems()` 把版本字段合并回行，新 items 引用 → effect 再次触发
  → 再 probe → 死循环。Network panel 累积 2000+ pending 请求，整页不可用。
  修：派生 `pageIdsKey = items.map(id).join('|')` 作为 effect 依赖，IDs 集合稳定
  时 effect 不触发，只在真正换页/搜索/排序时跑一次 probe。同样的依赖也修了
  选择状态被每次 mutate 清空的小 bug。
- **Servers 列表「可升级」红点改成 Version 列内联 chip** ── beta.8 加的 ⋮ 按钮
  上红点 Badge + tooltip 太弱（admin 必须 hover 才能知道目标版本是什么、是否
  值得点开）。换成 Version 列里直接显示 `可升级 → vX.Y.Z` 的 tertiary-container
  chip，target version 一眼可见，视觉也从「报警红」降级成「信息」级别。⋮ 按
  钮上的 Badge + Tooltip 同时移除，按钮回归纯粹"更多操作"语义。

### Security

- **`trusted_proxies` 默认值改为 loopback-only**（之前是 `0.0.0.0/0` + `::/0`,
  即"信任全网"）。原默认下,如果面板监听端口直暴在公网,任何匿名 client 都
  能伪造 `CF-Connecting-IP` / `X-Forwarded-For` 头 → `c.ClientIP()` 拿到伪造的
  IP → 绕过订阅限流、登录限流、audit log IP 追溯。新默认是 fail-secure
  (`127.0.0.1/32`, `::1/128`),只接受 loopback 来的代理头。
  - **配置 token 调整**:不再有"零配置信任所有"的入口
    - `""`(空 / 未设置)→ loopback only(新默认)
    - `"all"` / `"*"` → 信任全网(显式 opt-in,适用 Docker 内网监听等场景)
    - `"none"` → 完全禁用 trust list(用 raw TCP peer 作为 client IP)
    - `"<cidr>[,<cidr>]"` → 信任列出的网段(推荐生产值)
  - **boot 时会 WARN** 如果配置成 `all`,提醒 admin 这个模式只能在监听端口不
    可公网直达的拓扑下安全
  - **breaking**:跑在 reverse proxy / CDN 后面但没显式设置 `trusted_proxies`
    的部署,client IP 现在会显示成 proxy 的 IP。修复:把 proxy / CDN 的 IP 段
    填到 `http.trusted_proxies` 或 `PSP_TRUSTED_PROXIES`(Cloudflare 用户可直
    接列出 Cloudflare IP 段)

### Bug fixes

- **`userRepo.Update` 不再用 `Save()` 写整行**:加 `.Omit(pollOwnedColumns...)`
  排除 traffic poll 拥有的 6 列(lifetime_up/down/total_bytes,
  period_baseline_bytes, lifetime_baseline_at, traffic_period_start)+
  last_online_at。原行为下,admin 在 Users 页编辑某个用户(load → mutate →
  Save)的过程中如果 traffic poll 刚好跑完,admin 的 stale snapshot 会把
  lifetime 计数器回退几兆/几十兆。emergency 列暂保留在 Update 范围内,因为
  `UseEmergencyAccess` 也走 Update;那条 race 由 service 层 `emergencyMu` 收窄,
  窗口比 traffic poll 窄得多。
- **traffic.Service 的 floor-push + 邮件 fan-out goroutines 接入 bgWG 追踪**:
  原 `safego.Go("traffic.floor-push", ...)` / `traffic.disabled-email` /
  `traffic.enabled-email` 都不在 `App.bgWG` 里,`App.Shutdown` 返回时它们可能
  还在跑(违反"in-flight drain"契约 → 进程退出可能丢失最后一次配置 push 或
  邮件发送)。改成 `safego.GoTracked(s.bgWG, ...)`,新加 `SetBgWG` setter,
  Build() 里把 `&a.bgWG` 注入 traffic.Service。
- **user 同步任务重试上限**:`ProcessDueTasks` 在 task.Attempts ≥ 100 时改成
  Cancel 而非 MarkRetry,避免一个永久失败的任务(例:admin 删除了上游 inbound
  但本地 client config push 还在排队)每分钟一次永久 hammer 3X-UI。100 attempts
  × 1 min ≈ 1.5 小时,远超任何合理 transient outage;admin 仍可在 Sync Tasks
  里手动点 Retry。

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
