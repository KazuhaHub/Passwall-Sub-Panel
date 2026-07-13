# PSP × 3X-UI 兼容性

PSP 通过 `/panel/api/*` 对接 3X-UI 面板。本文档维护两件事：

1. **每个 PSP 版本对应的最低 / 已测试 3X-UI 版本范围**（升级前查这里）
2. **历史踩过的兼容性坑**（升级 3X-UI 之前看这里，避免重复踩）

## 当前兼容矩阵

| PSP 版本 | 最低 3X-UI | 已实测通过 | 备注 |
|---|---|---|---|
| **v3.9.1+** | **3.4.2** | 3.5.0 | 新增由所选节点执行的 REALITY 目标扫描；依赖 3.4.2 首次提供的 `/server/scanRealityTargets`，不做 PSP 本机回退 |
| **v3.9.0** | **3.3.0** | 3.5.0 | 共享 client 模型历史版本；保留其原始 3.3.0 floor，不因 3.9.1 新功能反向收紧 |
| **v3.6.2 – v3.8.x** | **3.2.0** | 3.5.0 | 每节点 client(一级 `/clients/*` API),**硬切 ≥ 3.2.0**;per-node 路径是 shared 路径子集 |
| v3.6.0 – v3.6.1 | 3.1.0 | 3.1.0 | 仍走 inbound-scoped 端点;别跑在 3.2.0 上,先升 PSP 到 v3.6.2 |
| v3.5.1 – v3.5.x | 3.1.0 | 3.1.0 | `/inbounds/list` 把 settings 等改成 nested object,见下文 |
| v3.5.0 | 3.0.x | 3.0.x | 跨 3.1.0 升级会破坏 traffic poll |
| v3.4.x | 3.0.x | 3.0.x | 同上 |
| ≤ v3.3.x | 2.x – 3.0.x | 3.0.x | 历史兼容性见 CHANGELOG |

> 这张表是人看的速查；运行时真相源是 `docs/compat/v3.json`。`min_xui` 和 `max_tested_xui` 两个字段**都已接入运行时**(PSP 按需拉取并据此判 too_old / untested)。

**规则**:
- "最低 3X-UI" = 该 PSP 版本能正常工作的最早 3X-UI 版本(低于这个会破)
- "已实测通过" = 在该版本上真实跑过 traffic poll / reconcile / render 全套
- 任何高于"已实测通过"的 3X-UI 版本都属于**未知风险**——升级前先在一台 panel 上小流量验证

## 历史兼容性事件

### 2026-07-13 / PSP 3.9.1 接入节点侧 REALITY 目标扫描 → floor 3.3.0 抬到 3.4.2

**结论**：3X-UI 的 `POST /panel/api/server/scanRealityTarget` 与 `/scanRealityTargets`、对应 `reality_scan.go` 服务实现均从 **v3.4.2** 开始存在；v3.4.1 及更早标签不存在这些文件/路由。因此 PSP 3.9.1 的最低 3X-UI 版本准确调整为 **3.4.2**。

**PSP 实现策略**：管理员在节点创建/编辑页选择 3X-UI 服务器后打开扫描器；PSP 只鉴权并代理 URL-encoded `targets`，TLS/证书/ALPN/X25519/延迟探测由该 3X-UI 节点执行。返回结果携带 `source_panel_id/source_panel_name`，界面明确展示扫描来源；点击“使用”只回填 `dest + serverNames`，不会自动保存。没有 PSP 本机扫描回退，避免中央面板与节点出口不同导致误判。

**安全与资源边界**：扫描 API 仅 admin 可用，PSP 侧按来源 IP 限制每分钟 6 次、输入文本上限 16 KiB；3X-UI 侧继续负责 SSRF 防护、最多 32 并发、CIDR 最多 256 IP、总任务最多 512。CIDR 最坏情况可能超过普通 API 的 30 秒，PSP 只为该请求单独放宽到 75 秒。

**兼容矩阵处理**：`version.MinXUI` 与 v3 compat 最新 entry 同步设为 3.4.2；另保留窄范围 `v3.9.0..v3.9.0` entry（min 3.3.0），避免篡改历史版本事实。

### 2026-07-13 / 3X-UI 3.5.0(xray-core 26.7.11)REALITY 认证回归 → minClientVer 显式设置修复

**背景**: 面板从 3.4.2(xray 26.6.27)自动升级到 3.5.0(xray 26.7.11)后,一条此前正常工作的 TCP+REALITY inbound(VLESS + xtls-rprx-vision,端口 443)所有客户端(用户的 mihomo/Clash Verge,以及排查时用官方最新 Xray-core 客户端复现)全部连接失败,同一面板上的 shadowsocks inbound 不受影响。

**排查过程(逐项排除)**:
- 网络/端口本身没问题:TCP 三次握手正常,拿一个未认证的探测者(裸 openssl s_client,SNI 打 dest 域名)去连,能正确回落到伪装网站的真实证书——说明 REALITY 监听、回落路径是健康的。
- 密钥没问题:用 `xray x25519 -i <privateKey>` 反推,确认面板存的 privateKey/publicKey 数学上配对正确。
- 不是 mihomo 专属问题:下载官方 Xray-core v26.7.11 二进制,拿面板真实的 publicKey/shortId/UUID 原样发起握手,同样失败(`uConn.Verified: false` → `REALITY: processed invalid connection`),排除客户端指纹(chrome/firefox 都试了,结果一致)和客户端版本问题。
- 不是进程该重启没重启:`restartXrayService` 重启过,现象不变。
- 用全新密钥、全新端口建一条临时 inbound(测完即删)复现,现象甚至更严重——连伪装回落都直接卡死无响应,证明问题不是这条 inbound 自己的历史包袱,是这台面板 3.5.0 + xray-core 26.7.11 组合本身的问题。

**根因**: Xray-core commit [`af7eb68`](https://github.com/XTLS/Xray-core/commit/af7eb68)(随 v26.7.11 发布)把 REALITY 服务端行为改成:`realitySettings.minClientVer` 留空(以前的含义是"不限制客户端版本")现在会被服务端默认成 `"26.3.27"`,任何自报 Xray-core 版本低于这个值的 REALITY 客户端一律被当成未授权连接、回落到伪装网站。这个改动的动机是 Chrome/uTLS 指纹新鲜度(见 [XTLS/Xray-core#6181](https://github.com/XTLS/Xray-core/pull/6181) 讨论,RPRX 提到俄罗斯 GFW 已经在针对陈旧 Chrome 指纹限流),**不是一个访问控制/安全机制**——REALITY 真正的准入门槛(privateKey 派生的 AuthKey)完全不受这个字段影响。当天社区已有两份独立报告命中同一个 bug:[XTLS/Xray-core#6482](https://github.com/XTLS/Xray-core/issues/6482)(v26.6.27→v26.7.11 回归,现象一致)、[#6477](https://github.com/XTLS/Xray-core/issues/6477)(mihomo v1.19.28 客户端,官方回复确认修复方式是显式设置 `minClientVer`)。

**修复**: 通过 `/panel/api/inbounds/update/{id}`(读-改-写,保留全部 22 个既有 client / 密钥 / shortId / dest 不动)把该 inbound 的 `minClientVer` 从 `""` 显式改成 `"1.0.0"`,拉 `/panel/api/server/getConfigJson` 确认改动已进入正在跑的 xray 进程。管理员的真实客户端随后确认恢复正常。面板与 xray-core 都保持最新版(3.5.0 / 26.7.11)不动,无需回退。

**PSP 侧结论**: 与 PSP 代码无关——PSP 从不读写 `minClientVer`、不创建 REALITY inbound,`render` 包(`streams.go` / `urilist.go`)对着这台面板实时配置核对过,pbk/sni/sid/fp/spx 全部解析正确,纯粹是 3X-UI/xray-core 服务端的上游回归。

**未解决 / 待观察项**:
1. 临时 inbound 复现时"伪装回落也卡死"这个更严重的现象没有深挖根因(为避免在生产事故现场继续制造噪音,测完即删)。以后在这台面板(或同版本组合)上新建 REALITY inbound,建议先小流量验证再批量发给用户,不要假设新建的一定能用。
2. 顺带发现 `realitySettings.dest` 在 3.5.0 里对已有(经历过迁移)的 inbound 返回时改名成了 `target`,但 `/inbounds/add` 新建的 inbound 读写时仍然认 `dest`——同一版本下字段名不统一,取决于 inbound 是老迁移的还是新建的。PSP 的 `xuiStreamSettings.RealitySettings.Dest`(streams.go:41)本身在 PSP 里全代码库零引用,不受影响,但提醒以后手工拼 inbound payload 时注意。

**给未来的建议**: 如果某条 REALITY inbound 在 3X-UI 升级后(尤其是跳到 xray-core 26.7.x 系列)突然所有客户端都连不上、且用官方最新 Xray-core 客户端从零复现也失败,先查 `minClientVer` 是不是被新版本默认收紧了,显式设一个低版本号(如 `"1.0.0"`)就是最小侵入的修复,不需要回退 xray-core 版本。

### 2026-07-01 / 3X-UI 3.4.2 实机复核 → 已测上限 3.4.1 抬到 3.4.2

**背景**: 上游发 3.4.2。拿一台真实 3.4.2 面板(`panelVersion 3.4.2`, xray `26.6.27`)用 API token 做实机复核。

**复核结论(LIVE-VERIFIED)**: PSP 触及的接口仍在,形状稳定;3.4.2 可以直接兼容 PSP 当前 v3 线。OpenAPI 中 PSP 调用的 `/inbounds/*`、`/clients/*`、`/server/*` 路由全部存在;`updatePanel` / `installXray` 仅确认路由存在,未执行。

实机 smoke 覆盖:
- server/status、getPanelUpdateInfo、getXrayVersion、getWebCertFiles
- inbounds list/listSlim/get/add/update/del/setEnable(临时禁用 inbound,测完清理)
- clients add(多 inbound)/get/update-by-email/del-by-email、attach/detach、bulkAttach/bulkDetach、bulkCreate/bulkDel

**响应形状**: `/inbounds/list` 和 `/list/slim` 仍返回 nested object,PSP `flexJSON` 可解析;`/clients/get` 返回 `{client, externalLinks, inboundIds, usedTraffic}`,其中 `externalLinks` / `usedTraffic` 对 PSP 是附加字段,Go 会忽略。`min_xui` 不变:v3.9.0+ 仍为 3.3.0,v3.6.2-v3.8.x 仍为 3.2.0。

**结论**: `max_tested_xui` 3.4.1 → 3.4.2(`v3.json` 两条 active entry 同步);无需 PSP 代码改动。

### 2026-06-26 / 3X-UI 3.4.1 源码核 → 已测上限 3.4.0 抬到 3.4.1

**背景**: 上游发 3.4.1(Rolling Dev 通道、日志查看器重做、内存优化、client 批量操作等)。3.4.0(下一条,2026-06-24 实机已核)的 patch 版,本次按完整 `v3.4.0...v3.4.1` commit 列表**源码核**(未实机)。

**复核结论(代码零改动,SOURCE/CHANGELOG-VERIFIED)**: 整个 delta **没有动到任何 PSP 调用的端点 / 响应形状 / 数据模型 struct / DB schema / 认证路径**。
- **client 改动是附加或利好**:
  - `feat(clients)` 批量启停 + 批量设 XTLS flow(#5524)—— commit 明说 **「no new endpoint, DB column, or migration」**,flow 走既有 inbound-JSON→SyncInbound 路径,是 `bulkAdjust` 的一个**可选字段**,PSP 现有调用不受影响。
  - `fix(web)` #5543「删除多 inbound client 时无视 shared email 从运行时移除」—— 正是 PSP「一个 shared client 跨多 inbound」模型,这个修复**让 `DelClientByEmail`/`BulkDelByEmail` 的运行时清理更正确**,对 PSP 是利好。
- **其余全在 PSP 不碰的子系统**:3X-UI 自家订阅引擎(`PROTOCOL/TRANSPORT/SECURITY` 变量、Incy、`{{TRAFFIC_USED}}`)、tgbot、原生多节点(node traffic history)、tunnel(`rewritePort` null)、outbound、日志查看器、dev-update 通道。
- **数据模型**:`model.Client`/`xray.ClientTraffic`/`model.Inbound` 未变,无相关 schema 迁移;无 auth/CSRF 改动。

**两个非阻塞观察项**(不影响现有部署,记录备查):
1. **新 VLESS 加密模式**(#5517):若管理员给某 VLESS inbound 配了新加密模式,PSP 渲染将来可能要补对应的 client `encryption` 字段 —— 属**后续渲染支持**,不是兼容性破坏。
2. **#5520 恢复 Vision flow + 一次性 `MigrationRestoreVisionFlow` 启动迁移**:3X-UI 现在会在 flow-eligible inbound 上自愈 client 的 Vision flow。对 PSP 良性(reconcile 会重新收敛,PSP 自己的 flow 推导本就在该补的地方补 Vision)。

**结论**: `max_tested_xui` 3.4.0 → 3.4.1(v3.json 两条 entry 同步);`min_xui` / `version.MinXUI` const / drift-guard 不动。**升级建议**:可以升 —— 若手头有一台已升 3.4.1 的面板,跑一遍 traffic poll + 加/删 client 实机确认更稳妥,我可以补做实机复核。

### 2026-06-13 / 3X-UI 3.3.1 实机复核 → 已测上限 3.3.0 抬到 3.3.1

**背景**: 上游发 3.3.1(自 3.3.0 起一次大重构 + 多节点/安全修复一批)。拿一台已升到 **3.3.1 的真实面板**(`panelVersion 3.3.1`、xray `26.6.1`)用 API token 端到端复核。

**复核结论(代码零改动,LIVE-VERIFIED)**: PSP 触及的端点在 3.3.1 全部仍在、形状未变。实机 smoke-test(临时 inbound + client,测完自清理):17 个非破坏端点全 `success:true`,2 个破坏性端点(`updatePanel`、`installXray`)在 OpenAPI 路由表里确认存在(不实打)。
- **读路径**: `/inbounds/list` + `/inbounds/list/slim` 仍把 `settings`/`streamSettings`/`sniffing` 返回为 nested object(`flexJSON` 原样吞下),`clientStats` 带齐 `id/inboundId/email/up/down/total/enable/expiryTime/reset/lastOnline`(slim 把 `settings.clients` 裁到 `{email,enable}`);`/inbounds/get`、`/clients/get/{email}` 回 `{client:{uuid,…},inboundIds,usedTraffic}`——`usedTraffic` 是新增的**附加同级键**,Go 忽略;ID 仍从 `uuid` 取、非 `id`。
- **写路径**: inbound `add`→`get`→`update`(**读-改-写**,改 remark 同时保住已有 client)→`setEnable`→`del` 全 `success:true`;client `add`→`get`→`update`(full-replace,totalGB 1GiB→2GiB 已生效)→`del`(删后 get 回 `(record not found)`,`isClientNotFoundMsg` 命中)→`bulkCreate`(`created:1`)→`bulkDel`(`deleted:1`,新增的附加 `skipped[]` 被忽略)。
- **server**: `/server/status`(panelVersion 3.3.1 + xray version)、`/server/getPanelUpdateInfo`(`{currentVersion:3.3.1,latestVersion:v3.3.1}`)、`/server/getXrayVersion`(tag 字符串数组)、`/server/getWebCertFiles`(`{webCertFile,webKeyFile}`)全正常。

**3.3.0→3.3.1 变更对 PSP 的影响 = 无**(80 commits / 425 文件,但对 PSP 纯**附加**,逐 tag 比对源码确认):大头是**源码树重构**(`web/controller/*`→`internal/web/controller/*`、`database/model/*`→`internal/database/model/*`,git rename 89-96%,HTTP 层无感);`model.Client` 与 `xray.ClientTraffic` **逐字节一致**(`tgId` 仍 int64/数字);`model.Inbound` 只**新增**字段(`SubSortIndex`/`ShareAddrStrategy`/`ShareAddr`);唯一响应形状变化是 `/clients/get` 新增附加键 `usedTraffic`;唯一新增 inbound 路由是 `POST /inbounds/pushClientTraffics`(多节点,PSP 不调);服务层「按 email 匹配 client / partial update 保 UUID」修复与 PSP 一贯按 email 驱动 update/del 的做法一致。随版带 **GHSA-jm48**(Xray 日志路径限定在面板日志目录)——升级理由之一。故 `min_xui` 维持 3.2.0、`version.MinXUI` const 与 drift-guard 不动。

**未复核项**: cookie(用户名/密码)登录这次仍没验(只有 API token)。token 模式是 PSP 验证过且推荐的路径(cookie 模式对不安全方法要 `X-CSRF-Token`,token 模式不受约束)。

### 2026-06-12 / 3X-UI 3.3.0 实机复核 → 已测上限 3.2.8 抬到 3.3.0

**背景**: 上游发 3.3.0(minor 版本,自 3.2.8 起新增多节点/客户端分组/自定义 geo/出站订阅等一批功能)。拿一台已升到 **3.3.0 的真实面板**(`panelVersion 3.3.0`、xray `26.6.1`)用 API token 端到端复核。

**复核结论(代码零改动,LIVE-VERIFIED)**: PSP 触及的端点在 3.3.0 全部仍在、形状未变。实机 smoke-test(临时 client,测完自清理,全 `success:true`):
- **读路径**: `/inbounds/list` + `/inbounds/list/slim` 仍把 `settings`/`streamSettings`/`sniffing` 返回为 nested object(`allocate` 为 `null`,`flexJSON` 原样吞下),`clientStats` 带齐 `email/up/down/total/enable/expiryTime/reset/lastOnline`(新增的 `uuid`/`subId` 及 inbound 级 `originNodeGuid`/`lastTrafficResetTime`/`trafficReset` 都被 struct 忽略)。`/clients/get/{email}` 回 `{client:{uuid,email,enable,flow,password,auth,expiryTime,totalGB,…},inboundIds}`(PSP 从 `uuid` 取 ID,非 `id`)。
- **写路径(全部按 panel 唯一 email)**: `add` → `get`(回读 uuid/email/enable/totalGB/inboundIds 全对)→ `update`(full-replace,totalGB 1GiB→2GiB 已生效)→ `del`(删后 get 回 `(record not found)`)→ `bulkCreate`(`created:1`)→ `bulkDel`(`deleted:1`)。
- **server**: `/server/status`(panelVersion 3.3.0 + xray version)、`/server/getPanelUpdateInfo`、`/server/getXrayVersion`(tag 字符串数组)、`/server/getWebCertFiles`(`{webCertFile,webKeyFile}`)全正常。

**3.2.8→3.3.0 变更对 PSP 的影响 = 无**: delta 纯**附加** —— 一批新端点(`clients/groups/*`、`clients/bulkAdjust|bulkAttach|bulkDetach|delDepleted`、`custom-geo/*`、`nodes/*`、`xray/outbound-subs/*`、`server/getNew{UUID,VlessEnc,EchCert,mldsa65,mlkem768}`)——**没有任何 PSP 调用的路由被删或改形状**,故 `min_xui` 维持 3.2.0、`version.MinXUI` const 与 drift-guard 不动。多节点注意点同 3.2.8:inbound 现在带 `originNodeGuid`,但 PSP 每个 `(panel,inbound)` 用唯一 email、一个 client 只落一个 inbound,3X-UI 自家多节点对 standalone PSP 仍是 no-op(部署仍建议对接单机 3X-UI,见下文)。

**未复核项**: cookie(用户名/密码)登录这次没在 3.3.0 上验(本次只有 API token)。token 模式是 PSP 验证过且推荐的路径(3.2.x 起 cookie 模式对不安全方法要 `X-CSRF-Token`,token 模式不受约束)。

### 2026-06-05 / 3X-UI 3.2.8 实机复核 → 已测上限 3.2.7 抬到 3.2.8

**背景**: 上游 2026-06-05 当天发 3.2.8(此前 3.2.7 是 source-verified 抬上来的)。拿一台已升到 **3.2.8 的真实面板**(`panelVersion 3.2.8`、xray `26.6.1`)端到端复核。

**复核结论(代码零改动,LIVE-VERIFIED)**: PSP 触及的端点在 3.2.8 全部仍在、形状未变。实机 smoke-test(临时 inbound + client,测完自清理,全 `success:true`):
- **inbound**: `add` → `get`(回读 `security=tls` / `certificates:[]` / `serverName` / `settings.fingerprint` 全对)→ `update`(内联证书)→ 回读字节级一致 → `del`。
- **client(全部按 panel 唯一 email)**: `add` → `get`(`obj.client.uuid` 在、`email/enable/flow/password/auth/expiryTime/totalGB` 在原位、`inboundIds` 在)→ `update`(改 enable,回读已生效)→ `del`(删后 get 回 not-found)→ `bulkCreate`(`created:2`)→ `bulkDel`(`deleted:2`)。
- **server**: `/server/status`(取到 panelVersion 3.2.8 + xray version)、`/server/getPanelUpdateInfo` 正常。

**3.2.7→3.2.8 变更对 PSP 的影响 = 无**: delta 主要是 multi-node(3X-UI 自家主从节点功能)、client 批量性能、订阅格式。唯一碰 client API 的是 **#4892「scope remote client update/delete to one inbound」**——这是 multi-node 专属;PSP 每个 `(panel,inbound)` 用唯一 email、一个 client 只落一个 inbound,所以该改动对 PSP 是 no-op,已被实机 update/del 正常生效证实。3.2.8 的 client obj 新增 `security/group/comment/createdAt/updatedAt/reverse` 字段是**附加**的,Go json 自动忽略,不影响解析。

### 2026-06-02 / 3X-UI 3.2.6 复核 → 已测上限 3.2.0 抬到 3.2.6

**背景**: 上游 3X-UI 在 3.2.0 之后又出了 3.2.5 / 3.2.6,拿到一台真实 3.2.6 面板复核 PSP 是否需要适配。

**复核结论(代码零改动)**: PSP 触及的整个 3X-UI 面 = `internal/adapters/xui/client.go` 里那 15 个端点
(`/login`、`/panel/api/inbounds/{list,get,add,update,del,setEnable}`、`/panel/api/clients/{add,update,del}`、
`/panel/api/server/{status,getPanelUpdateInfo,updatePanel,installXray,getXrayVersion}`),在 3.2.6 **全部仍在、形状未变**。
逐项实测/核对:

- **序列化**: `/inbounds/list` 仍把 `settings` / `streamSettings` / `sniffing` 返回为 nested object(`allocate` 直接省略),
  `flexJSON` 原样吞下,下游解析器无感。3.1.0 那次破坏不会重演。
- **clientStats**: 仍返回 `email/up/down/total/enable/expiryTime/reset/lastOnline/uuid/subId`,流量轮询所需字段齐全。
- **subId(3.2.5「enforced unique subId per client」)**: 对 PSP 是**非问题**。PSP 构造 `ClientSpec` 时从不设 `SubID`(`sync.go`),
  面板服务端自动生成唯一值——实测一台面板 24 个 client 的 subId 全部互不相同,且每 user 的多 client 共享同一 uuid、各自独立 subId。
- **CSRF(3.2.x 新增)**: Bearer(API token)模式不受 CSRF 约束(实测 Bearer POST = HTTP 200)。
  **注意**: cookie(用户名/密码)模式下 3.2.x 对不安全方法要求 `X-CSRF-Token`——PSP 的 username/password 回退模式**未在 3.2.x 上验证**,
  生产请优先用 API token 模式(PSP 本就 token 优先)。
- **tgId / keepTraffic**: `tgId` 早已按 int64 发(v3.6.2 修);`/clients/del?keepTraffic=0` 与文档「不传 keepTraffic=1 即清流量」一致。

**写路径已实测**: 在 3.2.6 实机上跑了 client add→get→update→del 与 bulkCreate/bulkDel 的一次性 smoke-test(临时 client,测完自清理),全部 `success:true`、删后 get 回 `(record not found)`。配合读路径(traffic poll 的 `/inbounds/list`),3.2.6 端到端验证通过。

**顺带采纳 3.2.x 更省端点(v3.6.3-beta.15)**: traffic poll 改用 `/inbounds/list/slim`(只要 clientStats,丢掉 settings.clients 大字段);按 email 取单 client 走 `/clients/get/{email}`(替代拉整 inbound 再扫);删节点/删用户走 `/clients/bulkDel`、挂节点批量加用户走 `/clients/bulkCreate`(N 次网络调用+N 次 xray 重启收成 1 次)。bulkCreate 的重复项由面板报在 `skipped`(reason 含 "already in use"),据此收养归属。

**这几个新端点对 `min_xui=3.2.0` 下限仍兼容 —— 已在真实 3.2.0 面板(panelVersion 3.2.0、xray 26.5.9)端到端实测确认(2026-06-02)**:
- `/inbounds/list/slim`:3.2.0 **HTTP 200**(存在),clientStats 字段集与 3.2.6 逐字节一致。
- `/clients/get/{email}`:存在,existing→`{client,inboundIds}`,缺失→`" (record not found)"`(与 3.2.6 同,`isClientNotFoundMsg` 命中)。
- `/clients/bulkCreate`:`[{client,inboundIds}]` → `{created,skipped:[{email,reason}]}`,重复项 reason 含 "already in use"(M5 收养命中)。
- `/clients/bulkDel`:裸数组被拒、`{emails,keepTraffic}` → `{deleted:N}`(与 3.2.6 同)。
- 单条 add→get→update→del 全通,subId 仍服务端自动生成。

故 `min_xui=3.2.0` 是诚实的:存在性 + 契约形状都在真实 3.2.0 上验过,不只是「假定一致」。slim 在 3.2.0 即 HTTP 200,故 `ListInboundsSlim` 不加版本兜底(沿用本项目硬切下限、不维护兼容 shim 的一贯做法)。

**另需留意 — 3X-UI 原生多节点(Nodes 功能)**: 3.2.x 起 3X-UI 自带「central panel + 子节点」聚合,会按 email 跨节点聚合客户端流量。
PSP 自己就做多面板聚合(Node = 单个 inbound),若把 PSP 指向一台已配子节点的 central panel,clientStats 会是跨节点聚合值 →
与 PSP 的「一 inbound 一 node」模型冲突。**部署建议**:PSP 对接的 3X-UI 保持单机,不要再套 3X-UI 自己的 node 聚合。

### 2026-05-23 / 3X-UI 3.1.0 → PSP v3.5.0 破坏

**症状**: 任何升级到 3X-UI 3.1.0 的 panel 一旦被 PSP 接入,traffic poll Phase 1 fetch 全失败,日志报
"cannot unmarshal object into Go struct field of type string"。表现为所有 user 流量数据停止更新。

**根因**: 3X-UI 3.1.0 改了 `/panel/api/inbounds/list` 响应:
- `settings` / `streamSettings` / `sniffing` 从 escaped string(`"settings": "{\"clients\":[]}"`) 改成 nested object(`"settings": {"clients":[]}`)
- `allocate` 从 escaped string 改成 `null`
- 写端仍接受 legacy escaped-string 写法,没破坏

PSP `rawInbound` 这四个字段定义为 Go `string`,`json.Unmarshal` 一个 object 进去直接报错。

**修复**: PSP v3.5.1 新增 `flexJSON` 类型(nested object/array 原样捕获,null → "")。**硬切只支持 3X-UI ≥ 3.1.0**——不再维护 3.0.x 兼容路径,因为自用项目可以控制对接版本。

**附带发现**:
- 3.1.0 `clientStats[*]` 多了 `uuid` / `subId` / `lastOnline` 字段——Go json 默认忽略未知字段,PSP 当前 `rawClientTraffic` 不受影响
- `lastOnline` 是个免费的"用户最近活跃时间"素材,未来可以做"在线徽章"
- 新增端点 `/inbounds/list/slim`、`/inbounds/options`、`/clients/list/paged`、`/clients/{add,update,attach,detach}`——PSP 当前不用,但 slim 是未来 traffic poll 优化候选

## 升级 3X-UI 时的检查清单

1. **查本文的兼容矩阵**——目标版本是否在当前 PSP 版本的"已实测通过"范围内?
2. 不在范围内的话: 先升级 PSP 到支持目标 3X-UI 版本的版本
3. 升级**单台** panel 先,观察 5-10 分钟:
   - PSP traffic poll 日志无错(看 `traffic poll panel` warn 行)
   - PSP reconcile axis A 日志无错
   - 一个 user 用真实客户端拉订阅看是否能连
4. 全部正常后再升级其它 panel
5. **不要批量升级**——3X-UI 任意小版本都可能像 3.1.0 这样改 schema

## 当 3X-UI 升级踩到新破坏怎么办

1. 立即记录到本文的"历史兼容性事件"
2. PSP 这边: 走 patch 版本(v3.5.x) 修复兼容性,**同时更新兼容矩阵的"最低 3X-UI"**
3. 更新 `reference_xui_v3_api_break` memory(项目 memory 系统),把"这次踩坑 + 修复方式"沉淀

## 维护 `docs/compat/v<MAJOR>.json` 的 SOP

每个 PSP major 一个 JSON 文件(v3.x 都拉 `docs/compat/v3.json`,v4.x 都拉 `v4.json`)。
这是 v3.6.0-beta.7 引入的 per-major 分文件设计,理由见 ARCHITECTURE.md。

### 何时改 / 改什么

- **新 3X-UI 出 patch 版本(无 API 改动)** ── 在当前 active major 的 JSON 里把
  覆盖你 PSP 版本那条 entry 的 `max_tested_xui` 改成新版本号,顺手更新 `updated_at`
  和 `notes`。commit + push 到 main → 所有该 major 的 PSP 部署 60 秒内自动感知。
- **PSP 发新 minor (比如 v3.6 → v3.7)** ── 在当前 major 的 JSON 加新 entry,
  `psp_min: "v3.7.0"`, `psp_max: "v3.7.99"`,把新 entry 放在 `entries` 数组**最前**
  (first-match-wins 让新版优先匹配)。
- **PSP 发新 major (比如 v3.x → v4.0)** ── 新建 `docs/compat/v4.json`,内部 `major: 4`,
  entries 从只覆盖 v4.0 的 baseline 开始。`v3.json` 保持不动 — 仍跑 v3.x 的部署继续从
  那个文件拿数据。
- **patch 级精度区间(罕见)** ── 比如 v3.6.5-v3.6.8 单独验过 3.2.0,其它 v3.6.x
  还是 3.1.0:在 entries 数组前面插入一条更窄的 entry(narrower 在前 = first-match
  生效),broader 的 baseline 在后面兜底。
- **抬高最低版本(硬切)** ── entry 的 `min_xui` **已接入运行时**(PSP 拉到后按它判
  too_old),但它和代码里的 `version.MinXUI` const **是同一个下限的两处表述,必须相等**:
  `TestMinXUIConstMatchesCompatJSON` 会读这个 JSON 断言"覆盖最新版本那条 entry 的
  `min_xui` == const",**drift 直接让 `go test` 红**——所以改下限时**两处一起改**,忘了
  哪处提交前测试就拦住你(这正是 v3.6.2 漏掉 const、3.1.0 面板没警告那个坑的防忘闸)。
  运行时 `ActiveMinXUI() = max(MinXUI, JSON min_xui)` 只是安全网:正常发版两者相等、max
  是空操作;万一某次 drift 漏过测试发了版,取较高值兜底,下限永远不会被错误降低。

### entries 数组语义

- 每个 entry 是 PSP 版本的**闭区间** `[psp_min, psp_max]`(含两端)
- `psp_min` / `psp_max` 端点**只写 stable semver** `vX.Y.Z`,**不带** pre-release suffix
  (PSP 比对时会丢自己 version 的 `-beta.x` 后缀,把 `v3.6.0-beta.7` 当成 `v3.6.0` 匹配)
- **first-match-wins** ── 数组顺序就是优先级,narrower / 更新的 entry 放前
- 重叠 OK,顺序决定胜出

### compat 范围的拉取时机(reactive)+ 拉不到的故障容错

PSP 启动时先读本地 cache(`<DataDir>/compat-cache.json`)装入上次成功 fetch 的范围,
之后**不主动周期性拉 GitHub**。compat JSON(`docs/compat/v3.json`)只在**探测到某面板版本
对不上当前缓存范围**时才拉(reactive,v3.7.0):

- 版本探测(boot / 每 10 分钟 tick / Servers 页 Test)算出 `CheckXUI(panelVersion) != Supported`
  时才 `RefreshRemoteCompat`(60s 节流——同一 tick 多台对不上合并成一次)再重判;
- upgrade-panel 闸门**强制**拉一次(force,跳节流),保证"是否支持"判定不吃旧缓存。

所以**全兼容的机群对 GitHub 零 compat 流量**;一台太新的面板,等维护者 bump 了 `max_tested`,
PSP 下一次 tick 会自动重拉、自动转 Supported(无需重启/翻页)。

> "有新版可升级"角标是另一条——`RefreshLatestXUI` 查 GitHub release tag,**仍主动** + 30 分钟节流,
> 因为本地没有任何信号能告诉你"上游出了新版本",天生没法 reactive。

任何一步失败(网络挂 / JSON 解析错 / major 不匹配 / 没匹配 entry)只是让 CheckXUI 返回 Unknown,
admin 仍可通过 upgrade-panel 的 `force` 按钮强制升级 — 不是 hard wall。(Unknown 本身也算"对不上",
所以下次探测会自动再尝试拉。)

## v3.6.0 路线图: PSP 自动感知 3X-UI 版本 ── 已完成

| beta | 内容 |
|---|---|
| beta.1 | xui_panels 加 panel_version / xray_version / version_checked_at 三列;adapter GetServerStatus;app.go boot probe + traffic-loop piggyback |
| beta.2 | Servers 页 Version 列 + compat banner + Test 按钮顺手刷版本 |
| beta.3 | 远程升级 3X-UI / Xray 按钮 + smoke probe + 跨大版本 migrate 政策修订 |
| beta.4 | lastOnline 集成到 admin 用户列表"最近活跃"列 |
| beta.5 | dynamic compat (GitHub raw 单文件) + admin force override |
| beta.6 | 5 个 audit 发现 bug 修复 + local compat cache 兜底 |
| beta.7 | dynamic compat schema v2: per-major 分文件 + entries 数组 + psp_min/psp_max 范围 |

这样下次类似 3.1.0 这种破坏可以在 admin UI 提前看到,而不是 traffic poll 静默失败才察觉。
