# Native destination traffic acceptance

The `Native destination traffic acceptance` workflow runs a real Node daemon
against PSP's production `Build` router, TLS sync endpoint, administrator HTTP
routes, SQLite stores, and destination audit worker. It never fabricates a Node
report or an applied receipt, and it never rewrites the emitted core artifact.

The Node candidate is pinned to
`KazuhaHub/Passwall-Node@63c60f6d5e8cd093bbaa8385f5b514dbe2f40390`.
This is a reviewed development candidate, not a published stable release. Its
own preferred Go compiler is checked separately from PSP's preferred compiler.
The additional guarded sing-box job pins Node to
`a6163c8b5c10a9c8129cf660020fe2c8fa2b85db`; the four baseline jobs retain their
original immutable candidate. Each checkout and compiler is checked independently.
No local module replacement is used. The daemon installs and runs each exact
Xray version: `26.6.27`, `26.7.28`, and `26.9.9`.
The isolated fixture explicitly acknowledges the reviewed restricted `26.9.9`
selection; the production catalog gate remains enforced.

A separate job selects the reviewed sing-box `1.14.0` through the same PSP
catalog and launches the candidate Node with its normal sing-box API. Its
thirteen required cases are empty policy, watch, block, pause/resume, sniffed
domain blocking, allowlist trial, enforce, supplemental CIDR, unchanged sync, report-only refresh, daemon restart,
collection settings, and the pre-hotfix private-address baseline. Administrator
mutations require a current applied policy receipt and actual listener/roster
convergence before traffic is checked. Trial allows the connection; enforce
denies it; adding the target CIDR allows it. The first report after restart must
restore the confirmed policy. Collection is clamped to the empty wire level
even when the panel requests B; the status API must disclose no hit/usage
support or active collection. Traffic over multiple sync cycles must leave
both audit tables empty.

Changing an unsupported collection setting leaves sing-box's effective policy
unchanged. That case compares the exact candidate/body/context and historical
LKG, requires its applied receipt and an accepted sync after the saved setting,
and verifies the saved panel level through status. It does not demand a new mint
for identical effective state. Policy-changing mutations still require a mint
after the administrator mutation.

The N0-S baseline uses a disposable loopback HTTP target. It sends distinct
VLESS address types for the private IPv4 literal and the `localhost` domain,
checks the target nonce, and expects the pinned pre-hotfix Node to reach both.
This is prerequisite evidence for a separate conditional Node hotfix, not a
claim that a private-address guard already exists or that cloud metadata and
DNS-resolved private destinations are protected. No cloud endpoint is queried.
The private-address guard is unchanged by this PSP acceptance change.

The guarded job repeats the same twelve policy, allowlist, sync, refresh, restart and collection
cases against the new Node, then replaces `private_baseline` with `private_guard`.
It must reject a real VLESS private IPv4 literal with no target request, while a
`localhost` domain still reaches the same controlled server and returns its nonce.
It repeats these checks after PSP publishes a global allow matching that exact
private target port and while destination policy is paused. An owned documentation
IP target must remain reachable each time, ruling out a stopped proxy. The
global allow is removed and policy is resumed through administrator HTTP, with
actual applied receipts required at each transition. Audit tables must stay empty.
This records the guard's visible-IP boundary and the remaining DNS-domain bypass;
it does not claim protection for all private-resolving domains or cloud metadata.
The guarded job has its own complete execution gate and cannot borrow passes
from either the baseline sing-box job or Xray jobs. This guarded job does not
change Node production code.

Every Xray job must execute all twenty-eight ordered cases: empty policy, watch,
block, pause/resume, anonymous allowlist trial, enforce, supplemental entry,
unchanged sync, report-only list refresh, unrelated listener rejection and repair, actual core-check timeout and repair,
collection off, detailed usage, daemon restart, policy rejection and retry,
original-LKG rejection, pruned-fallback rejection, port-only fallback under
insufficient sniffing, pause/resume during fallback, disabled-rule pruning,
sniffing fallback exhaustion, account revocation while exhausted, and a PSP app
restart with the exhausted state persisted, returning an allowlist group to open
during sniffing fallback, pause/resume of that pruned fallback, pausing an offline
node, restarting that node with unacknowledged audit backlog, and committed-batch
replay after response loss.
The restarted daemon's very
first real TLS report must already carry the confirmed applied policy, and a new
traffic hit must map to its original user. A bounded in-memory probe copies only
policy status from that first request; the production handler receives its
unchanged bytes and validates it normally. Allow/deny is checked against an actual
VLESS HTTP request and the destination's nonce and request count. Audit and
usage counts come from the real ingestion worker. The usage HTTP read must
also create its declared administrator audit action.

After supplemental allowlist admission, each engine holds a pidfd for the unique
live core belonging to the real Node. Parent, exact engine arguments, owned
`current.json` path and installed executable inode are checked before and after
three separate successful real TLS sync requests. Each cycle must retain the
published generation, policy mint/context/body/digest and confirmed LKG; config
and roster bodies, ETags, versions and epoch remain identical with fresh accepted
acknowledgements. The listener stays synced. The actual current core config stays
byte-identical with the same SHA-256, traffic still reaches the nonce target, and
the original pidfd must remain alive. Xray verifies both real users. No signals
are sent or artifacts rewritten. This proves no successful core replacement over
these idle cycles, not all possible sources of restart or a load-duration test.

The report-only refresh case seeds a migrated remote list with usable canonical
content and a missing parse report, then publishes a real global watch policy
that uses that list. The source is this repository's non-sensitive two-line
fixture on raw.githubusercontent.com at the exact PR head commit (or the exact
push/manual commit); only a lowercase forty-character commit is admitted. The
driver never grants private network access or replaces the production fetcher.
Administrator HTTP queues a normal asynchronous HTTPS download, duplicate
parsing and production refresh commit. The duplicate report must appear both
in storage and the administrator detail read, with a new fetched time and row
edit version. Source, normalized entries, counts, digest, definition generation
and published generation must stay unchanged. The pidfd/config/roster/policy
checks span that actual refresh and three later accepted syncs, with successful
traffic throughout. The temporary watch is then removed through HTTP. No Node
report, receipt, core artifact or refresh result is fabricated. This covers
the legacy-missing-report refresh path; the full category-download, SQL-dialect,
cache/CAS and old-node upgrade requirements still use their separate evidence.

Fault cases mutate sniffing and policies through production administrator HTTP.
They check canonical candidate bytes, historical LKG retention, the acknowledged
current digest, actual allow/deny traffic, and the status API's current rule count
and fault state. A confirmed port-only fallback continues blocking when a new
domain policy cannot be sniffed; pausing stops it and disabling its source prunes
it. A confirmed domain-dependent LKG exhausts under metadata-only sniffing.
The same HTTP Host is denied with full sniffing and allowed after exhaustion;
route-only sniffing preserves the local IP destination and requires no DNS.
Account revocation must then reach the acknowledged roster without rearming that
fallback. The revoked user must fail while a separate enabled canary still reaches
the same target, proving the core is serving traffic.
The listener rejection case runs while the group enforces its allowlist.
Administrator HTTP stores syntactically valid TLS settings with a numeric ALPN;
the real Xray configuration check rejects that listener. A fresh real
`core_convergence_failed` report must reach the operator inbox, while the policy
receipt and LKG remain unchanged, the invalid config stays unapplied, and both
original client keys remain attached. Both users still reach the target through
the running previous artifact. Restoring the original settings then requires a
fresh accepted report, current config/roster convergence and a synced production
listener intent. Stream convergence alone acknowledges a received document;
the listener's pending intent proves the invalid config was not deployed.

The timeout case binds to the installed executable of the running Node's core.
It pauses only that Node's exact `run -test -config .candidate-*.json` children,
using pidfds and checking parent, executable inode, argument shape and owned
state directory. It never pauses the live proxy process or rewrites any binary,
artifact, report or receipt. The normal production timeout must terminate the
first stopped check and yield a fresh `core_convergence_failed` deadline report.
Checks remain paused until repair so a quick successful retry cannot hide the
failure. While the check is stopped and after timeout, both existing users must
still reach the actual target through the previous core. Policy body, digest,
LKG, candidate kind and member keys remain unchanged; listener intent stays
pending and the status page does not classify this as a policy rejection.
Restoring the original listener requires a fresh applied/converged report and
synced listener intent. Cleanup resumes only still-existing held children and
closes all pidfds. These cases cover a completed unrelated rejection and a real
deadline, independently of a policy-specific rejection.

The separate `policy_rejected` case publishes a canonical documentation prefix,
`::ffff:192.0.2.0/120`, through administrator HTTP. PSP and Protocol validators
remain unchanged: Xray maps the address to IPv4 and rejects the 120-bit prefix.
The real Node must complete its policy-free check before attributing the failure.
A bounded observer retains only the first valid rejected PolicyStatus and the
original HTTP acceptance; earlier applied reports cannot satisfy this proof.
The rejection must identify the exact expected desired digest, and a fresh core
issue must establish the prefix-width error. PSP must acknowledge the unchanged
port-watch LKG, preserve the original live core pidfd and configuration bytes,
display rejected-with-fallback, and continue traffic and real watch ingestion.
Administrator retry must produce another actual accepted rejection followed by
the same LKG confirmation. Deleting the incompatible rule must recover normal
desired application. This tests a real engine compatibility boundary without
rewriting a binary, candidate artifact or Node report; it does not cover an LKG
or pruned fallback being rejected.

The independent `lkg_rejected` and `pruned_rejected` cases apply a mapped-prefix
policy with the reviewed sing-box `1.14.0`, then switch through administrator
HTTP to that job's exact reviewed Xray version. Collection is explicitly off
and legacy per-inbound Xray sniffing is cleared through administrator HTTP
before selecting sing-box; both settings are restored for subsequent cases.
This preserves sing-box's normal listener compatibility checks. With collection
off, changing engines alone cannot change the policy digest. Real sync traffic
is partitioned while the new mapped-prefix desired rule and core selection are
published, keeping the sing-box-confirmed LKG authoritative. The pruned case
also deletes an independently confirmed source: its fallback digest must differ
from the historical applied digest, while the original-LKG case must retain it.
The observer waits for the exact fallback digest, excluding the first desired
rejection. Both cases require the actual accepted fallback rejection, durable
exhaustion, empty policy application, current core and roster convergence, and
the persisted `destination_policy_lkg_rejected` issue. A revoked account must
fail real VLESS traffic while a separate canary succeeds. The pruned case also
reopens PSP's actual SQL pool, routes and caches; neither reopening nor roster
changes may rearm the exhausted candidate. Manual retry must provoke a fresh
desired rejection. Repaired definitions and the original collection setting are
applied through administrator HTTP before subsequent cases run.

PSP derives the LKG issue only from a matching, committed fallback rejection.
Its key is the exact candidate digest and its detail is fixed, with no address
or user data. Issue-write failures stop that sync before replacement minting;
a matching replay retries persistence without rearming the exhausted policy.
Stale, invalid, unsupported, desired-policy and rolled-back observations cannot
produce the issue. Durable issue deduplication preserves operator review state.

PSP is then shut down and rebuilt with the same configuration and database.
A drained fixture TLS gateway holds the endpoint stable while the actual SQL
pool, router, services and caches are replaced. The continuing real daemon must
sync with the reopened app; the exhausted candidate and historical LKG must
survive, and the revoked user must remain denied while the canary succeeds.

After the reopen, a group catch-all and a global port watch are genuinely applied:
canary traffic is denied and a real group hit is ingested. A new BitTorrent rule
then fails the metadata-only sniffing precheck. The confirmed fallback retains
the group source. Returning the group to open must prune that source, retain only
the exact port watch, and acknowledge the original canary's enabled attachment.
Before that administrative mutation, the driver captures the prior fallback.
The prune must advance the published generation, mint after that prior mint,
change the canonical body and digest, confirm that exact pruned body, and receive
a real sync after the mutation. Mint timestamps use a sync cycle's clock captured
before policy compilation, so a concurrent write can be visible to compilation
after that clock. This transition proof avoids assuming that the mint's clock
must follow the administrator request's wall time. The exact rule/source, current
generation, LKG, listener, roster and actual traffic assertions remain required.
The previously revoked user must remain disabled. Actual canary traffic must
reach the target and create a watch hit. Pausing this fallback acknowledges zero
rules and allows canary traffic without adding watch hits after prior batches
have drained. Resuming restores the confirmed watch and ingests a newly counted
hit. These checks cover both policy pruning and asynchronous membership resync.

Offline cases partition only real Node TLS sync requests with HTTP 503; the
production administrator routes remain available. With a confirmed port deny,
the administrator pauses execution while multiple actual polls fail. The status
API must still show the node as offline with its historical active rules, and
real canary traffic remains denied. Reconnection must then acknowledge the pause
before canary traffic succeeds; resuming restores the deny without re-enabling
the previously revoked user.

The next partition observes real frozen block batches over several failed polls.
A bounded fixture probe retains only counts and the first audit subtree's hash;
new blocked connections must not change that batch's ID, hour, rows or counters.
No partitioned hit reaches PSP storage. Node is then stopped and restarted while
sync remains unavailable. Its first real report must already restore the applied
policy, but multiple new failed reports must contain none of the old in-memory
backlog. The private sink token remains identical with mode 0600. New denied
traffic must produce a new batch through the restored sink. Reconnection admits
that new event and another fresh event, with exact counts excluding pre-restart
backlog. This records actual volatile-data loss across multiple cycles; it does
not bound restart loss to one poll interval or establish durable audit delivery.

The response-loss case lets the same production sync handler consume every
original report and enqueue its audit batch, then closes the real TLS connection
without delivering the handler's response. PSP must commit the first block hit.
At least three reports replay its unchanged audit-subtree hash, row count and
event count while three new denied connections accumulate for the next batch.
Every processed sync must actually have produced a successful handler response;
the fixture synthesizes no acknowledgement. Storage remains at exactly one new
event throughout the lost responses. After normal responses resume, the three
new events and one fresh connection must be ingested with exact counts and no
duplicate committed hit. This covers short live retries, not the 26-hour
cleanup/reopen and 48-hour/72-hour boundary matrix.

Current candidate readiness requires its published generation, a mint after
the administrator mutation, a matching applied report, and an accepted sync
after that mint. Desired candidates additionally require the matching confirmed
LKG. Empty and paused candidates may retain a different historical LKG;
unchanged report timestamps are not treated as a new status transition.

The driver only runs as root on a disposable GitHub-hosted Linux VM. It adds
one random run-owned `2001:db8::/128` address to loopback, verifies its exact
local route, and removes that exact address after stopping the daemon. TLS
trust is scoped to a temporary CA file; host trust stores and core private
address protections are unchanged. Credentials and process output stay in
temporary private files or memory and are never uploaded. The retained artifact
contains Go test execution results and count-only assertions. The result parser
rejects missing, duplicate, failed, or skipped cases. Xray and sing-box have
separate required sets; evidence from one engine cannot satisfy the other.

This gate covers one native node with one VLESS TCP listener and two users on
SQLite. The fixture provisions those records directly, obtains a normal admin
JWT, then uses production HTTP routes for every tested policy mutation. It
starts `Build` and the audit worker, not all `App.Run` background services. It
does not replace full service installation/reinstall acceptance, SQL matrix
tests, the complete sing-box matching/upgrade matrix,
other-engine timeout scenarios, multi-node
offline and mixed-version cases, long-window response-loss deduplication, load testing, the
complete UI approval matrix, or staged release verification.

Use the workflow run and PR validation record for execution evidence. All three
Xray jobs and both separate sing-box jobs must pass on the exact current PR head;
an earlier or partial matrix does
not satisfy this gate.

独立的 legacy-upgrade 作业使用已发布 PSP v4.0.1.20 的源提交
`0ae05e1e40170c030acd23cdf637310b740b50a6` 与 Node v4.0.1.8 的源提交
`8e5e3db72219a8db9081a372722abb1529d22ecd`，各自使用自身首选工具链。
旧 PSP 只加入已审阅的测试桥接；生产 Build、路由、迁移和同步服务均保持发布版本。
旧 Build 创建原始数据库，在同一夹具 TLS 入口后运行原生产处理器，并提供真实 VLESS
入站和账号。候选 Build 运行前，必须只读查询确认数据库中没有目的地策略表。

`old_panel`、`panel_upgrade` 和 `legacy_publication` 使用独立必需执行门禁。
真实旧节点收敛后，驱动以 pidfd 持有原 Xray 进程，关闭旧 PSP 进程，随后使用候选
Build 迁移同一数据库，Node 在全程持续运行。每阶段至少三个实际成功同步周期必须
保持 config/roster 的 ETag 与版本、实际核心配置字节、原核心进程及 nonce VLESS
连接。通过正常管理员 HTTP 发布端口拒绝策略后，旧节点仍保持原样，空策略元数据
不得含策略正文、执行摘要或规则数量，覆盖状态必须为 `unsupported_version`。
元数据记录本身不是已下发或执行策略的证据。有界被动探针原样转发请求及响应，仅
保留验证器和收敛事实；凭据、进程输出和私有数据库内容不上传。本验收覆盖这一固定旧
版本组合与 Xray 26.6.27，不能代替完整节点升级、降级或负载验收。

`mixed_fleet` 与 `mixed_offline_pause` 使用同一 PSP、同一 SQL pool 和同一账号，
另启动固定候选 Node `63c60f6d5e8cd093bbaa8385f5b514dbe2f40390`。每个 Node
独占自己的真实 TLS 同步网关、入站、核心进程、凭据和数据目录。端口拒绝在已确认
的新节点上执行；独立未被拒绝的 nonce 连接排除核心失效，旧节点继续放行并保留原
进程、配置字节及 ETag。覆盖必须分别显示旧节点需升级与新节点已执行，不能借用
另一节点的能力或回执。

只隔离新节点的实际同步请求时，旧节点仍同步。管理员正常保存暂停后，新节点覆盖
必须保持离线及历史执行数量，原核心继续拦截；不能提前声称已停止。恢复同步后必须
收到当前空候选的实际应用回执才放行，再恢复执行后重新拦截。全过程重复核对旧节点
原进程及连接，末尾确认旧节点的空元数据仍不含策略。本场景覆盖两个版本、两个
VLESS TCP 节点及 SQLite；不是全机队、跨方言或压力矩阵的完整证明。

随后 `node_upgrade`、`node_downgrade`、`node_reupgrade` 在原旧节点上依次运行
固定候选、v4.0.1.8 和固定候选二进制。每次只正常停止和启动 agent，保留同一 TLS
入口、凭据、入站端口、账号与数据目录，不改写 state.db、同步请求、回执或核心配置。
每阶段至少三个成功同步周期后，必须有 config/roster 当前完整应用回执、新鲜能力
及运行状态；候选还必须确认当前发布代际的精确策略摘要。两个独立 nonce 目标
验证策略目标拒绝/放行与控制目标持续可达，避免将核心失效误判为策略生效。

state.db 必须保持原文件身份和 schema 9，Agent epoch、凭据摘要和用户 UUID 保持
不变。降级后不得在当前候选中下发策略；原核心配置字节和 config ETag 恢复，
roster 的 `min_config_version` 随当前 config 版本更新，必须确认当前 roster ETag，
覆盖显示需升级且不表示正在执行历史策略。旧 Node 没有空策略 PolicyStatus，
接口保留历史 applied_rules 及未确认的空候选；界面不得把它显示为执行规则数，
也不得显示历史 fallback 为“仍在执行”。真实停止由当前完整流、配置字节和代理
连接证明。再次升级必须重新应用当前已发布策略。
历史 LKG 可以保留，不能把它当成当前执行事实。此演练只验证上述固定版本的原地
二进制切换；PSP 远程升级 helper、systemd/Docker updater、白名单能力变更及
离线升级窗口仍有各自验收门槛。

`panel_downgrade_offline`、`panel_downgrade`、`panel_reupgrade` 继续复用同一
数据库及原节点。候选 Node 全程运行，旧 PSP 的测试桥接只读取和核验已存在的
夹具身份，不重新创建账号或改写策略表。另一个 mixed 节点在这三个阶段被隔离，
末尾才恢复；此处只验证原节点的 PSP 降级链路，不宣称机队同时撤销策略。

驱动正常关闭候选 PSP 后，以只读事务记录全部 12 个 `dest_*` 表的结构、列和
全部行摘要。真实旧 PSP 再次打开同一数据库时，断联节点必须至少出现三个失败
同步请求，仍使用原核心进程和配置字节，持续拦截策略目标且放行独立控制目标。
恢复同步后，必须观察到旧 PSP 的实际无策略配置、三个成功收敛周期及候选 Node
的空摘要应用报告，原核心必须实际退出。此时策略目标放行，完整配置必须与原始
无策略配置一致，仅允许已审阅的 N0 编译器差异：旧 Node 未写 `log.access`，
候选 Node 显式写 `"none"`。入站、路由、出站及其它日志字段必须完整相同；
不改写任何实际配置文件。全部目的地表和行摘要保持不变。

再次正常关闭旧 PSP 并打开候选 Build 后，定义及发布快照必须保持原样，当前
发布代际、精确策略摘要及覆盖状态必须恢复。真实策略目标再次被拒绝，控制目标
仍可达；原数据库文件身份、Agent epoch、凭据摘要和账号 UUID 不变。验收保留
每阶段执行耗时及当前节点确认应用后的毫秒数，不上传私有表内容、地址或凭据。
本场景验证固定旧 PSP v4.0.1.20、候选 Node 与 Xray 26.6.27 的 SQLite 路径；
降级期间部署失败、白名单资格重新同步、其它数据库及全机队仍需独立演练。

`allowlist_capability_setup`、`allowlist_capability_removed`、
`allowlist_capability_restored` 在再次升级后的原账号上通过正常 HTTP 启用试运行，
加入独立控制目标的精确 CIDR，然后切到执行。使用完整 CAS 输入和发布接口；
两个阶段均须实际确认当前策略、完整流、账号附件与真实控制连接。另设独立且
不受原全局端口规则限制的目标：试运行时可达，执行时因未在白名单中而拒绝。
已应用策略正文必须含该分组、原主体对应的 trial 或 deny-all 规则；原全局拦截
规则不能替代该分组规则的确认。随后正常停止和
启动固定旧、新 Node 二进制，继续复用原数据、凭据及 TLS 入口。能力、客户端、
roster 和回执不得由驱动改写。

旧 Node 上能力消失后，生产异步成员重同步必须撤去原账号的该面板入站绑定。
SQL 和原生 roster 的稳定客户端身份保留，以保存累计计数；不能把删除历史行
当作撤销访问的条件。验收读取事务一致的实际成员快照及当前已确认 roster 正文，
要求两者的该账号绑定均为空，至少三个成功同步周期后原入站上的控制连接也失败，
但真实核心仍运行且完整流已确认。稳定客户端 ID、创建时间、UUID 保持，累计
计数不能下降。恢复候选 Node 后，同一客户端和账号 UUID 的附件自动恢复，当前发布策略的精确摘要得到
确认，控制目标重新可达而策略目标继续拒绝。分组 HTTP 覆盖逐节点核对资格，
公开分组 HTTP 使用节点总数、合格数及逐节点 `ineligible` 原因：旧节点须新增自身
的 `capability` 排除，恢复后须移除该排除，其他节点的资格保持。内部
`EligiblePanelIDs` 不属于 JSON 响应，不能拿它充当 HTTP 证据。不能借用另一候选
节点的能力；数据库文件、Agent epoch、凭据及账号身份保持不变。
此处验证两个固定版本的原生节点能力变更，不代替远程 helper/updater 或旧 PSP
重新开放第三方面板的成员重同步演练。

Xray 的采集档位切换在产生验证流量前，还必须核对已应用策略正文中的当前
`collect`、revision 和精确摘要。发布代际不变或旧候选重复报告不能提前确认新的
采集设置；原用量入库及关闭采集后不新增记录的门禁继续执行。

`group_scope`、`exemption`、`exemption_expired`、`allow_priority` 在三个 Xray
版本与 sing-box 基线/守卫作业中复用同一生产 PSP、TLS Node 和真实核心。
正常管理 HTTP 新建另一分组，并迁移既有控制账号或创建新账号；账号 UUID
保持，两个入站绑定必须实际确认。限定原分组的端口策略只拒绝该组账号，另一
分组账号可达且不新增该策略的 block 命中。临时豁免则只放行原账号，另一
账号继续受全局规则限制；Xray 对受限账号入库，豁免账号不新增 block 命中。

到期场景等待真实 UTC 时钟越过一分钟的 expires_at，然后显式调用生产小时
维护回调，等待自动发布及精确策略应用，两个账号再次被拒绝。它验证到期清理
至节点应用的运行路径，不宣称测试等待了小时定时器或跨 26/48/72 小时重启。
优先级场景使同一域名同时存在于放行和拦截名单：两个账号均可达且不新增
block 命中，只有拦截名单包含的另一个域名仍被拒绝。每组流量前后验证独立
未受该端口规则限制的控制连接及真实核心存活，最后恢复原分组和端口规则，
继续执行既有回退、重启与断联场景。节点输出及上传证据不包含密码或原始流量。
sing-box 继续不采集目的地记录；阶段 6 的采集实现仍不属于本计划。

作用域场景把原有矩阵扩展为 144 个必需执行阶段；它已在 `11408ac3` 的六份
执行证据中通过。代码和编译不证明当前提交已经运行，仍须按精确 HEAD 核验。

新增独立的 `published-remote` 作业及 `check-remote-upgrade`，要求
`candidate_policy`、`published_rollback`、`restart_recovery`、`idempotent_replay`
四个阶段各执行一次并通过，使当前严格矩阵要求共 148 项、七份运行证据。
此作业使用固定 `63c60f6d` 源码编译的候选，明确标识未发布的 `4.0.1.9`；
它不是官方发布包，也不作为已发布版本的证据。候选通过生产 PSP Build、TLS
同步和真实 Xray 执行非空端口策略，受限目标拒绝、独立目标可达，累计流量非零。
Build 夹具没有启动完整 cron 循环，因此显式调用原生产流量轮询回调：第一波
真实控制流量确立非零原始计数基线，第二波才折入累计计数。测试不直接写计数，
也不把首次样本当作累计增量；此处不声称覆盖 cron 的定时调度。

管理员经正常 HTTP 请求精确回滚到已发布的 `v4.0.1.8`，不直接插入任务或回执。
非 root Node 写入请求，原样生产 `passwall-node-upgrade.path` 唤起原样 helper
服务；helper 自行从官方 GitHub 下载真实包并执行生产签名、摘要、版本及同
schema 校验。驱动不替换 fetch、下载来源、信任公钥、服务沙箱或系统信任库。
初始账号服务也直接取自固定源码的安装模板；仅其私有 environment 为本次
运行配置 PSP TLS CA，helper 使用正常官方 HTTPS 信任。

回滚后，真实旧版本通过生产同步恢复原始任务并确认结果。验收核对管理 API
当前版本状态、helper InvocationID、精确任务输入/授权期限、真实安装摘要、
activation nonce/当前 PID、Node 持久执行及结果已确认；原核心须实际退出。
非空 PSP 定义和历史已应用正文保持，旧节点能力消失后当前候选撤去，实际
目标重新可达。账号/Agent epoch/凭据/客户端 UUID/创建时间、数据库 inode、
不可变 environment 与服务定义保持；累计流量不下降，新进程的 counter epoch
必须按生产约定推进。重启旧 Node 后这些检查继续成立，同一幂等请求不创建
新任务、不延长授权、不再次运行 helper，也不重启已有核心。

该作业只允许无既有安装的 GitHub 托管 Ubuntu 24.04 一次性 VM。清理前核对
本次 nonce、凭据、账号、精确单元正文和文件所有者，逐个删除有界的自有条目；
外来单元、链接、特殊文件或改变的身份会阻止清理。仅上传严格脱敏执行事件。
候选执行期间检查目的地与账号不出现在 journal；旧版本回滚后检查凭据保密，
不把旧版本的访问日志行为宣称为候选的隐私保证。

此路径不代替「已发布新功能版本」的正向远程升级与失败后自动回退，也不
代替 Docker updater、完整机队/数据库矩阵或长窗口。当前提交的四个新增阶段
仍须等待真实作业及严格证据检查，不能用之前的 144 项绿灯计为完成。
