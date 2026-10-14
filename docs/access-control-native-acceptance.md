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
Four further jobs pin the integrated Node stack to
`e2c9dfbfe934d4a61fda84b0a80b4f1cb5dbc719`: all three Xray versions and
guarded sing-box `1.14.0`. This candidate includes the report-outbox fix, private-IP
guard, state URI compatibility and Windows checkout changes. It is also an
unpublished development candidate. These jobs use the same complete traffic and
ingestion gates, with separate candidate-labelled artifacts; the earlier fixed
candidates remain independent checkpoints.
Four additional jobs pin the full-module Node stack to
`184f4e23f0f3d72f8492432184685fea4ec4967c`, including native Windows ACL/state
protection, document persistence, platform-boundary regressions and opt-in
numeric collector/agent-memory observation. Its five complete Node workflows
passed before this repin; collector/634 Windows roots/30 startup and both real-core
architecture reports were retained and independently checked. The older
`cef54ddba3ee96f9794aaacdc6ac1ed89cc318f9` execution remains a preserved historical
checkpoint. They exercise
all three Xray versions and guarded sing-box `1.14.0` with the same complete
runtime gates. The integrated `e2c9dfb` candidate remains a separate immutable
checkpoint; success on it does not replace execution of the full-module stack.
No local module replacement is used. The daemon installs and runs each exact
Xray version: `26.6.27`, `26.7.28`, and `26.9.9`.
The isolated fixture explicitly acknowledges the reviewed restricted `26.9.9`
selection; the production catalog gate remains enforced.

A separate job selects the reviewed sing-box `1.14.0` through the same PSP
catalog and launches the candidate Node with its normal sing-box API. Its
seventeen required cases are empty policy, watch, block, group scoping, exemption
and expiry, allow priority, pause/resume, sniffed
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

The guarded jobs repeat the same sixteen policy, allowlist, sync, refresh, restart and collection
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

Every Xray job must execute all thirty-two ordered cases: empty policy, watch,
block, group scoping, exemption and expiry, allow priority, pause/resume, anonymous allowlist trial, enforce, supplemental entry,
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

Current candidate readiness requires its published generation, a matching
applied report, and an accepted sync after the mint. A fault mint must follow
the administrator mutation, or prove a newer publication and mint than the
actual pre-mutation candidate, with an accepted sync after the mutation. This
handles a sync clock sampled before the write but compilation after the new
publication. The exact fault kind, applied digest, LKG and precheck still must
match; the exception cannot confirm a same-generation pause edit.
Desired candidates additionally require the matching confirmed
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
baseline Xray jobs, both checkpoint sing-box jobs, all four integrated Node
jobs and all four full-module Node jobs must pass on the exact current PR head;
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
`candidate_policy`、`health_failure_restore`、`published_rollback`、`restart_recovery`、
`idempotent_replay` 五个阶段各执行一次并通过，使当前严格矩阵要求共 149 项、
七份运行证据。此前 `da33a496` 的 148 项已实际通过，新阶段须独立确认。
此作业使用固定 `c51d145ae0e2db47eb69a640fe8f751318370f59` 源码编译的候选，
明确标识未发布的 `4.0.1.9 (c51d145)`；
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

健康失败场景先授权同一官方目标包，待生产 helper 实际激活目标、新非 root
进程发出同步且原核心退出后，只对目标版本的同步返回 503；其他请求保留原始
正文、头部和生产响应。helper 的生产 120 秒健康确认期限、下载/签名和信任均
不变。目标不得写出 authenticated readiness。随后必须由 helper 自动恢复原
候选二进制，原候选经正常同步上报确定失败，持久任务的 claim、输入和原始
期限保持，结果确认后不再执行。核对激活 nonce、失败回执、原始安装摘要、
当前非空策略/正文/精确摘要、真实拒绝/控制流量、保留身份/累计计数和推进后的
counter epoch。测试不直接恢复文件、启动恢复服务或改写回执；恢复后才发起
独立成功回滚授权，并继续原有重启/幂等阶段。

故障验收发现，不同核心观察周期会产生内部去重键不同、协议内容完全相同的
issue，导致报告校验失败并连带阻止有效失败任务结果发送。候选在报告边界
按 code/key/detail 合并完全相同的 issue，保留不同详情及全部持久队列 ID；
只有有效同步确认后才清理。完整/部分报告、丢响应重试和失败结果确认均有
真实 SQLite 回归测试。恢复验收要求 LastSeen 晚于观察到自动恢复完成的时间，
再验证当前策略和真实流量；恢复前的已接受快照不能证明恢复后的同步成功。

此路径不代替「已发布新功能版本」的正向远程升级，也不代替 Docker updater、
完整机队/数据库矩阵或长窗口。自动恢复场景仍须等待当前真实作业及严格证据
检查，不能用之前的 148 项绿灯计为完成。

独立 `published-docker` 作业新增 amd64/arm64 两个平台，各要求相同的五个阶段，
使原固定候选矩阵要求九份证据、159 项。它通过正常管理 HTTP 在一次性夹具中创建可恢复
的注册凭据，再下载 PSP 正常生成的双服务 Compose。固定 Node `c51d145ae0e2db47eb69a640fe8f751318370f59`
通过原发行 Dockerfile 构建明确未发布的本地候选；初始 Compose 不拉取该候选。
只增加夹具 TLS CA 的独立只读挂载，原入口、权限、网络与生产挂载保持。

候选须实际应用非空策略并通过真实拒绝/控制流量和两波计费。随后生产 updater
经真实 Docker Engine 从官方 GHCR 拉取 `4.0.1.8`，验证生产 image/schema/contract。
目标同步健康失败时须自动恢复原容器并确认原始失败任务；成功回滚须绑定实际
镜像 ID/RepoDigest、生产 transaction、nonce、容器 namespace PID 1、当前 host PID
与已确认的二进制摘要。原核心退出通过持有的 pidfd 核对，不直接改写任务或回执。
重启与幂等检查保留原始执行、私有文件和 SQL inode、稳定客户端/累计计数及推进
后的 counter epoch。候选检查两路 Docker 日志的隐私；官方旧目标只检查凭据保密。
清理仅移除本次项目 nonce、服务标签和控制挂载均匹配的有界容器与自有目录。
面板接收任务结果后，验收还须等待 Node 消费正常同步响应并持久确认；两者之间
不能用一次采样判为执行身份损坏。失败诊断仅保留固定类别，隐藏任务、凭据与日志。
异常退出清理容器后，只允许额外删除本次数据目录内 UID 10001 的生产 audit.sock；
其余链接或特殊文件仍阻止清理。

新增 Docker 驱动仍须通过实际作业和严格执行检查；代码、编译和合成 checker 测试
不证明其运行成功。它覆盖已发布旧目标的回滚及健康失败恢复，不代替已发布新功能
版本的正向升级，也不代替 Node updater 的全部交接故障矩阵。

集成 Node `e2c9dfbfe934d4a61fda84b0a80b4f1cb5dbc719` 另增加三份 Xray
证据（每份 32 项）和一份 guarded sing-box 证据（17 项），组成十三份独立证据、
272 项的固定检查点。全模块 Node `cef54ddba3ee96f9794aaacdc6ac1ed89cc318f9`
再增加同样四份证据、113 项，因此当前完整矩阵要求十七份独立证据、385 项。
原十三份证据仍须通过，不能用旧候选绿灯代替最新组合的实际执行，也不能据此
宣称已发布新版本的正向升级或全部阶段已经完成。

### 三方言真实核心矩阵

`traffic-sql` 在一次性托管 VM 的独立数据库服务上复用完整流量夹具，而不是
仅运行 SQL repository 测试。最新全模块 Node `cef54dd` 分别在 MySQL 8 与
PostgreSQL 16 上执行全部三个 Xray 版本和 guarded sing-box 1.14.0，新增八份
独立报告、226 个必需阶段。原十七份/385 项固定检查点保留；当前总要求为
二十五份/611 项，每份均须由原完整 verifier 验证。

`complete-matrix` 还要求五类运行作业全部成功，然后下载本次工作流的原始
脱敏报告，交给 `deploy/destination-matrix.mjs` 统一核验。目录必须恰好包含
当前 merge SHA 的二十五份报告，不能混用旧提交或缺少任一数据库/核心组合；
每份报告仍使用原完整 verifier 要求唯一 RUN/PASS 和包通过事件。额外字段、
原始 Output、未知诊断、失败、跳过、重复执行或链接文件都不能通过。合成
checker 单元测试只验证拒绝边界，不作为真实流量或矩阵完成的证据。

夹具只接受封闭的 `PSP_NATIVE_DB_KIND` 选择，不接收外部 DSN。服务数据库由
本次工作流创建，固定为回环地址的 `psp_native_acceptance`；MySQL 业务连接
使用仅获本数据库权限的专用用户。Build 后通过实际 SQL 读取引擎版本及当前
数据库，拒绝配置回退；Xray 的 PSP 关闭/重开阶段再次核对实际连接。读计数
断言在 PostgreSQL 上使用编号参数，参数值仍独立传入，策略、应用回执、真实
流量和采集检查不变。只上传原有脱敏事件，连接字符串和查询错误不进入证据。

这八份报告仍需实际运行并严格核验。通过该矩阵可证明这些单节点完整场景
使用三种实际数据库；它不代替多节点、全旧/全新/第三方混合机队、完整 App.Run
定时循环、四类饱和负载或 26/48/72 小时真实窗口。

### 三方言迁移、回滚及固定混合节点

`legacy-upgrade` 保留原固定 Node 的 SQLite 十四阶段，并扩展其 MySQL 8、
PostgreSQL 16 组合；全模块 Node `cef54dd` 再独立运行三种数据库的同样十四
阶段。每个组合都由固定旧 PSP 的真实 Build 创建初始数据库，确认不存在目的地
表后才交给候选 PSP 迁移。同一个主体和真实代理连接经历面板升级、旧/新节点
混合、单节点断联、Node 原地升降级、面板断联降级/在线降级/再升级，以及白名单
能力移除和恢复。原固定源码、完整十四项 verifier 和 SQLite artifact 名保持。

SQLite 仍核对原文件身份和全部十二张目的地表的 schema、列与完整行多重集。
服务器数据库仅连接本作业的专用回环数据库，核对实际版本、数据库与服务器 UUID
或数据库 OID；使用可重复读只读事务提取全部目的地表，再对结构和完整行数据
生成私有摘要。MySQL 使用原 CREATE TABLE 定义，PostgreSQL 包含表 OID、列、
约束、索引及附属序列配置/计数。行数据保留二进制内容，仅摘要参与比较，私有
正文、查询错误与标识不进入报告。重开 PSP 后再次确认真实连接和原始身份。

服务器组合在完整运行后还实际修改一行并回滚，随后创建/移除本次专用索引，
验证快照能分别发现数据变化与纯结构变化，且恢复后完整摘要相同。外部表名、
引号、SQL 语法和未知方言被封闭标识符检查拒绝。原始 SQLite 快照实现保持。

新增五份独立十四阶段报告，使当前整体要求为三十份、681 个必需场景。
`complete-matrix` 必须验证全部当前 merge 报告；代码、编译与合成检查器单元
测试不能代替这些组合的实际运行。该范围仍是固定旧/新两个节点的迁移和隔离
检查，不证明全旧/全新/第三方机队、真实负载或长窗口已完成。

### 生产 App.Run 与真实第三方面板的机队组合

新增 `TestNativeDestinationFleet` 与 `fleet` 作业，在 SQLite/MySQL/PostgreSQL
分别运行两台全旧、两台全新、一旧一新三种原生节点组合，再向同一个 PSP 与 SQL
pool 加入真实固定 3X-UI 3.9.0/S-UI 1.6.3 容器。原生节点仍固定旧版源码
`8e5e3db` 和全模块 `cef54dd`，各用自己的首选编译器，当前原生核心为 Xray
26.6.27；没有用适配器烟测的宽松 HTTP 客户端替代生产适配器。

驱动实际启动完整 `App.Run`。两个 TLS 同步入口各持有独立凭据、入站、数据目录
和真实 Node 进程，检查各自当前 config/roster、能力、应用摘要、发布代次及策略
正文。全局端口拒绝、暂停/恢复、第三方面板准备、白名单试运行/执行、单节点断联、
白名单暂停和重新开放必须分别唯一运行/通过，共十项。公开状态与分组覆盖逐面板
核对，旧版及第三方面板不能借用新节点能力或回执。

初始时间边界取在两个守护进程启动之前，管理修改前分别绑定原候选；相同报告的
`ReportedAt` 可以保持不变，但必须有新的真实心跳及准确候选/回执，不能把开始
等待的时间误作首次铸造时间。恢复完整嗅探后还须确认当前期望策略的采集回执，
才核对恢复的域名规则与实际流量，旧的空候选不能证明恢复完成。

白名单成员变更通过真实管理 HTTP 和生产成员重同步完成。检查当前原生 roster
与真实第三方面板的客户端入站绑定，并在重新开放后确认原客户端 ID、UUID、创建
时间及累计计数保留。独立开放分组的控制账号每阶段仍能完成 nonce 连接；错误
UUID 必须拒绝，区分策略/账号移除和整个核心不可用。直接 VLESS 探针不会借用
订阅中的 direct 出站。断联期间另一个节点继续真实同步，原核心以 pidfd 绑定。

两个真实容器沿用已有启动器的令牌铸造、准备和固定 digest。新增主机网络配置只
接受指定仓库的一次性托管环境及专用 `10.254.x.y` 地址；默认桥接模式保持。
两个固定版本的订阅服务都默认使用 `2096`。主机网络夹具等待 S-UI 配置初始化后，
停止本次容器，将其订阅端口单项事务更新为 `2097`，再启动同一个容器；管理端口、
其他设置和令牌行保留。异常原端口拒绝更新，默认桥接部署不执行该操作。
仅本次精确地址加入/移除，生产 SSRF 守卫允许该私有部署地址，目标连接仍使用
已有 nonce 文档地址。清理前核对容器 ID、名称、固定镜像、主机网络及本次挂载，
只删除已绑定的容器 ID。私有令牌、完整行和进程输出不进入报告。

原九组 `xray-pair` 保留，新增九组 `mixed-engines`：每组八个独立原生进程，
Xray 26.6.27、26.7.28、26.9.9 和 sing-box 1.14.0 各两个，仍分别运行全旧、
全新和旧新各半，覆盖三种数据库。26.9.9 显式确认受限版本；每个进程独立绑定
核心选择、凭据、同步入口、监听端口和数据目录，再接入两个真实第三方面板，
共十个面板。公开状态逐节点核对引擎和 Agent ID；sing-box 不能声明采集能力。
准备阶段的诊断将当前心跳、epoch、能力、期望核心选择、实际引擎/版本、运行与
监听状态分别转为布尔值，进程错误也仅记录固定类别是否出现；不输出具体私有值。
舰队的流证明逐台使用其预期引擎和版本，仍要求至少四次接受报告、已应用监听及
当前 config/roster 的版本和 ETag 一致。旧迁移夹具固定 Xray 26.6.27 的检查保留，
不能将该固定核心助手用于其他舰队核心。

混合引擎断联阶段隔离最后一个 sing-box 同步入口，保持其实际核心运行，要求
其他七个节点各自继续产生新心跳；恢复后核对该节点自己的策略与成员回执。
原十个阶段全部执行，成员移除、nonce 流量、控制账号和重新开放后的原客户端
身份均逐面板验证，不能用同组其他节点的结果代替。

完整门禁要求当前 merge 的四十八份/861 项：原三十份/681 项、九份双节点报告
及九份混合引擎报告，十八组机队作业均须成功。合成门禁单元测试、编译和 vet
只证明检查器与代码成立，新增组合必须取得实际运行报告。既有三十九份门禁的
历史运行仍按其不可变源码验证，不能将其标为四十八份通过。该范围不代替四类
饱和负载、24 小时负载、26/48/72 小时窗口或已发布新功能前向升级。

sing-box 单节点驱动也在管理修改前绑定原候选，使用真实候选变更和新心跳确认
回执，并核对暂停状态与当前候选类型一致。同步周期可能在写入前采样时钟、写入
后才编译；仅要求铸造时间晚于 HTTP 请求会误拒绝这种已确认变更。失败报告只
增加封闭的布尔诊断，不输出策略正文、凭据或完整私有行。

### 持续 webhook 负载验收

独立 `destination-load.yml` 启动完整生产 `App.Run`、真实 SQLite 和固定全模块
Node `cef54dd`，核心固定 Xray 26.6.27。单规则和五十条规则分别运行五分钟，
五十条规则通过各自真实目标端口同时承受流量；目标仍是本次拥有的 nonce 文档
地址，使用实际 VLESS 连接，不能将预计调度量或 HTTP 伪造命中当作流量证据。

生成器为固定大小 worker/队列，每 100ms 安排 110 个请求。门禁检查五分钟中的
每个完整一秒窗口实际成功写入至少 1000 个 VLESS 请求，所有已送达连接必须
拒绝，目标收到请求数为零；队列满、发送失败或延迟至窗口之后不会被算作成功。
每秒独立控制连接必须成功，原核心由 pidfd 保持绑定，真实同步心跳持续更新。

约每十秒从本次核心的 StatsService 读取实际 goroutine 数，从原进程 `/proc`
读取 FD、agent/core CPU ticks，并保留实际 CLK_TCK。采样拒绝进程出生坐标
变化，记录高峰和停止流量后的三十秒恢复。门禁给并发负载峰值设置上界，并要求
最后一份恢复样本回到基线 goroutine +128、FD +32 以内；这些是验收阈值，
不将 JSON 字节或系统线程数量代替实际 goroutine/FD。

读取生产入库的每条规则命中数与分类损失。只导出规则数量、事件量、损失原因和
资源数字；节点 ID、规则 ID、账号、凭据、nonce 和原始进程输出不进入报告。
有明确事件计数的损失保留，未解释的命中差额使本次有界负载失败，不能由少量
命中、部分报告或预期调度次数宣称完整统计。每阶段损失取当前累计计数与阶段前
累计计数的差额，不能把上一阶段损失重复计入下一阶段。

门禁同时要求两个阶段及父测试唯一实际运行/通过，并检查独立数字报告；只有
数字或合成门禁测试通过不足以证明负载验收完成。原 48 份/861 项功能矩阵保持
独立完整。这个组件只覆盖两段 webhook 持续负载，200 用户试运行及同期拦截、
四类同时超额、实际 24 小时负载、26/48/72 小时窗口仍须各自取得真实证据。

报告清洗器只保留父负载测试及 `single_rule`、`fifty_rules` 三个固定执行名称，
与负载门禁共享同一个封闭集合。未知子测试名称和原始输出仍移除。回归必须将
完整输入先经过生产清洗器再交给完整负载门禁，直接传入合成的最终报告不能
证明这条集成路径；历史报告被删去的执行名称不能补写或据时长反推为通过。

### 首份完整 webhook 负载运行证据

提交 `1c2f4da95622bae444c4e993464a556d4c62187b` 的
[真实负载工作流](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/38200260497)
终态成功。两个原件文件绑定同一非过期 artifact、准确源提交与 merge
`3540a0411434dceec7f592593d005f2261fd320c`；不可变源码门禁和独立检查器
均严格通过父测试、两个阶段各唯一 RUN/PASS 及全部数字报告，包 PASS 也唯一。

| 实际指标 | 单规则 | 五十规则 |
|---|---:|---:|
| 持续负载时间 | 300 秒 | 300 秒 |
| 实际送达并拒绝 / 实际入库事件 | 330000 / 330000 | 330000 / 330000 |
| 300 个一秒窗口中的最低送达量 | 1100 | 1100 |
| 传输失败 / 窗口后送达 / 拒绝目标收到请求 | 0 / 0 / 0 | 0 / 0 / 0 |
| 控制连接成功 / 新心跳 | 300 / 147 | 300 / 146 |
| 实际命中规则数 / 分类损失 | 1 / 空 | 50 / 空 |
| 核心 goroutine 峰值 / 恢复值 | 15 / 9 | 211 / 9 |
| 核心 FD 峰值 / 恢复值 | 112 / 8 | 207 / 8 |
| agent CPU 累计时间增量 | 29.91 秒 | 21.68 秒 |
| 核心 CPU 累计时间增量 | 76.32 秒 | 60.53 秒 |

每段有 34 份资源样本，实际 CLK_TCK 为 100，采样窗口分别为 330.059 秒与
330.057 秒，包含停流后的三十秒恢复；CPU 增量按这整个采样窗口记录。
该记录覆盖固定 Node `cef54dd`、Xray 26.6.27 和一次性托管 Linux/SQLite
环境中的这两段负载。200 用户试运行、同期拦截和配额隔离、四类混合超额、
24 小时负载与真实长时间窗口继续按各自要求验收。

### 200 用户试运行与独立拦截负载门禁

负载工作流新增独立 `trial-load` 作业，原两个 webhook 阶段保持必需。
真实生产成员同步创建并启用 200 个试运行用户，必须在同一实际 roster 中
确认全部用户及独立开放组控制用户，再绑定原核心进程进行五分钟流量。
每秒至少 1000 次成功试运行 nonce 响应，同时至少 100 次独立策略拒绝；
200 位用户各自必须实际送达。所有目标都是本次创建的文档地址 /128，
routeOnly 嗅探虚拟 HTTP 站点，无外部域名解析或目标连接。

每次测量连接使用唯一虚拟站点及一个事件，因此本场景可严格核对试运行
入库事件与分类丢弃的行数/事件数；这种换算不用于一般聚合流量。必须实际
出现试运行超额行，按小时的试运行保留配额不超过 20000；同期拦截事件全部
入库，拦截行与事件损失均为零。试运行数据库行必须匿名且端口为零。
封闭数字报告不导出用户、站点、凭据或规则 ID；独立的固定测试身份必须
经过生产清洗器取得唯一 RUN/PASS，不能借用 webhook 测试身份或仅凭数字通过。
每十秒采样实际 CPU、FD、goroutine，停流后继续采样三十秒检查恢复。

本节描述待当前提交取得托管运行证据的门禁，不表示负载已实际通过。
四类队列、pending/字节上界、独立拦截超额及长时间运行仍须分别验证。

首个提交 `0f86e419` 的托管试运行场景在开始流量之前失败，保留的执行报告
为根测试及包 FAIL，数字报告持续时间和用户送达均为零；完整门禁正确拒绝。
原两个 webhook 阶段仍完整成功，不视为新增场景通过。初始夹具仅等待能力
上报，未等生产原生适配器观测到运行核心和已应用入站。后续修正要求实际
`GetServerStatus` 与 `GetInbound` 符合固定核心、版本和本次入站，并保留
13 种封闭布尔准备阶段诊断。原失败报告未包含准确失败阶段，因此根因仍需
下一次托管执行确认；不能将这一夹具修正或编译通过当作负载已通过。

提交 `39524d2b` 的复测通过核心/入站就绪边界及其单元回归，但封闭诊断明确
报告 `trial_setup_membership=false`，仍在流量开始之前失败。原生面板写操作
只记录期望，立即读回可仍为上一版 roster；逐用户同步返回失败后，正常生产
调用通过 `ResyncMembershipOrEnqueue` 保存重试任务。夹具改为使用同一入口，
随后仍要求 200 用户的准确已应用 roster 以及逐用户真实 nonce 流量，不以
任务排队作为已应用证明。这次修正仍须取得新提交的完整托管负载通过证据。

### 独立 block 入库超额门禁

另设 `block-overflow` 作业，保留原两个 webhook 阶段和 200 用户试运行作业。
固定 Node/Xray/SQLite 运行真实 App.Run，每次连接使用唯一虚拟 HTTP 目的地，
五分钟内每个一秒窗口至少 1000 次实际送达并拒绝。必须从真实入库预算表
读到某小时 block 配额已保留 20000 行，以及实际 `over_budget` 丢弃行。
入库事件与所有分类丢弃严格对账；仅该唯一键/单事件夹具将一行丢弃换算为
一个已知事件。原普通聚合负载仍仅按其事件计数对账，不使用这种换算。

独立控制连接每秒成功，真实新心跳持续推进，原核心进程保持；资源采样和
停流后三十秒恢复沿用同一真实采样器。拒绝目标不得收到请求，传输失败和
迟到均为零。报告只保留数字及固定 root/阶段执行身份，未知身份和私有输出
拒绝；只有数字、只有部分场景或其他负载身份均不能代替这次实际执行。
本节描述待提交并取得托管运行证据的入库超额门禁，不表示其已运行成功，
也不替代四类聚合键、pending/队列字节和长时间压力的分别验证。

### 首份完整 200 用户试运行与拦截隔离证据

提交 `d74e8c43cf2471e5a69b48e3f9b94bda8836b549` 的
[负载工作流](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/38212263996)
终态成功，原两个 webhook 作业和新增试运行作业均通过。全部四个报告文件
绑定准确源及 merge `6a98c5d53f76e6c49208f414c1d750cf4fe99639` 的两个非过期
artifact，已由不可变源码门禁与独立检查器完整复核；所有必需测试身份各自
唯一 RUN/PASS、包 PASS 唯一，没有借用身份或原始输出。

| 实际指标 | 结果 |
|---|---:|
| 实际试运行用户 / 负载持续时间 | 200 / 300 秒 |
| 成功试运行 nonce 响应 / 最低每秒量 | 330000 / 1100 |
| 独立拒绝并入库 / 最低每秒量 | 33000 / 110 |
| 匿名试运行入库行 / 事件 | 20000 / 20000 |
| 试运行小时保留配额最大值 | 20000 行 |
| 节点 trial 丢弃事件 / PSP trial 超额行 | 250368 / 59632 |
| 试运行带账号或非零端口的行 | 0 |
| block 损失行 / 事件 | 0 / 0 |
| 传输失败 / 迟到 | 0 / 0 |
| 控制连接成功 / 新心跳 | 300 / 120 |
| 核心 goroutine 峰值 / 恢复值 | 319 / 9 |
| 核心 FD 峰值 / 恢复值 | 311 / 8 |
| agent / 核心 CPU 累计时间增量 | 51.36 秒 / 127.83 秒 |

34 份实际资源样本覆盖 330.047 秒，包含停流后三十秒恢复，CPU clock 为 100。
本场景每个虚拟站点恰为一个事件，故 20000 入库事件 + 250368 节点丢弃事件
再加 59632 PSP 超额行，完整对账至 330000 次成功试运行连接。试运行遥测不完整，
同期 block 仍完整保留且真实控制同步继续；这些数字不能解释为试运行无丢失。
该证明仅覆盖固定 Node `cef54dd`、Xray 26.6.27、一次性 Linux/SQLite 与这一
五分钟组合。独立 block 超额、四类队列/字节/heap 和长时间窗口仍需分别验收。

### 四类队列与实际解码内存门禁

`queue-pressure` 作业在托管 Linux 上使用生产 `batchQueue.offer`，每个请求
独立复制并解码真实规范化 JSON。四条 lane 分别填满到下一批被拒绝，报告
各自批数、规范化字节、批大小及 `queue_full`，验证既定 128/32/32/64 批和
32/8/8/16 MiB 配额。因批大小有粒度，总计不要求恰好达到 256 批或 64 MiB，
但每类必须因自己的字节或批数上限饱和，不能借用其他类的余量。

同一次完整基准迭代测量 GC 后保留堆内存，以及每次入队后和最终 GC 后的
堆内存采样峰值。后者是采样最大值，不是连续时间峰值；规范化字节配额
也不等于实际 Go 堆内存。证据只含固定 lane 和数字，并要求该独立测试根
唯一 RUN/PASS 及包 PASS。此组件尚待当前源码取得实际 hosted 运行结果；
不替代运行中 Node 聚合/pending、SQL 写入等待、控制同步和流量轮询延迟。

提交 `1f97927f` 的拦截超额作业在约 120 秒准备阶段失败，流量尚未开始；
保留的完整执行报告和数字报告被对应不可变源码门禁严格拒绝。原 webhook
与 200 用户试运行作业均成功，不能替代新增超额场景。后续保留原回执、
规则、核心选择、收集 revision 和配置/roster 条件，并逐项输出封闭布尔
诊断以确定准备失败位置。该提交的 module sing-box 在重启后单次放行失败；
重启夹具随后要求新鲜的未变候选回执、实际运行核心/入站、已打开监听和
本次 Node 的真实核心子进程，再执行原有单次 nonce 与请求数断言。两项
后续改动都须取得新提交的完整托管证据，不能按编译通过宣称运行成功。

提交 `8c89411b` 的超额准备诊断显示，仅 `load_transition=false`，其他 11 项
准确条件均为 true。负载夹具把无变化的 publish 请求也作为一次新修改，
会以已 minted 但可能尚未确认的快照覆盖实际 collection 修改之前的因果
边界。后续与既有原生功能驱动保持一致：publish 不覆盖该边界，实际修改
时间使用数据库的毫秒精度；回归覆盖错误替换后无法证明变更、原边界仍
能证明变更的情况。原严格回执与全部负载指标保持，修正仍待托管复测。

同一提交 `8c89411bb2b02f83053f826a971881b0d8409415` 的
[独立队列作业](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/38218215790/job/114707863106)
已实际成功。两个原件来自准确 merge
`6bc4399656dcbb43a8806878592c9785313768d4` 的非过期 artifact，
不可变源码门禁和独立检查均确认唯一测试 RUN/PASS、唯一包 PASS、封闭
字段及四类各自饱和。此为独立组件结果，不表示该轮完整负载工作流通过。

| lane | 实际批数 | 规范化 JSON 字节 | 下一批结果 |
|---|---:|---:|---|
| block | 42 | 33055344 | queue_full |
| observe | 10 | 7910340 | queue_full |
| trial | 10 | 7770320 | queue_full |
| usage | 27 | 16281891 | queue_full |

合计 89 批、65017895 规范化字节；GC 后保留堆内存 59214408 字节，
采样最大堆内存 95769224 字节。四类均因下一批超过自己的字节配额而饱和，
不是达到各自最大批数；这些值不是长期运行或并发 SQL worker 的内存结论。

### 首份完整独立 block 配额耗尽证据

提交 `3e58266fdd414fd5af2b4ac91e8698a2b396a27e` 的
[四项负载工作流](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/38221081604)
全部成功，包括原 webhook、200 用户匿名试运行、独立 block 超额与队列内存。
四个非过期 artifact 的八个原件绑定准确源及 merge
`0780602c0f56b5e6d8334ce370bdb1a752cfcdec`，各自由不可变源码门禁和独立
检查器完整通过；执行身份、封闭字段、存储/损失对账、隐私与恢复条件均保留。
本次因果边界修正已取得实际负载结果，原先准备失败的报告继续保留。

| 独立 block 实际指标 | 结果 |
|---|---:|
| 实际负载时间 | 300 秒 |
| 尝试 / 送达 / 被拒绝连接 | 330000 / 330000 / 330000 |
| 全部 300 个窗口的最低每秒量 | 1100 |
| 入库规则行 / 事件 / 实际小时保留配额 | 1 / 20000 / 20000 |
| PSP over_budget 行 / 事件 / unmatched | 310000 / 0 / 0 |
| 控制连接成功 / 新心跳 | 300 / 143 |
| 传输失败 / 目标收到请求 / 迟到 | 0 / 0 / 0 |
| 核心 goroutine 峰值 / 恢复值 | 236 / 9 |
| 核心 FD 峰值 / 恢复值 | 108 / 8 |
| agent / 核心 CPU 累计增量 | 31.86 秒 / 73.29 秒 |

34 份真实样本覆盖 330.054 秒，包含停流后三十秒恢复。此夹具一个虚拟站点
对应一次事件，20000 入库事件加 310000 超额行完整对账至 330000 次拦截，
没有其他损失类别；超额并不等于执行失效，目标始终没有收到请求。这份证明
仅覆盖固定 Node `cef54dd`、Xray 26.6.27、真实 Linux/SQLite App 和五分钟
独立 block 负载，不替代四类同时压力、SQL 分块等待、控制/流量轮询延迟及
真实长时间清理窗口。原生功能矩阵须以对应工作流的全部原件另行验收。

当前 `3e58266f` 的原生矩阵随后也全部成功，49 个作业终态通过，全部 48 份
非过期原件绑定上述准确源与 merge。不可变源码与独立完整检查均通过 861 个
必需场景；该提交的完整 Test/类型检查/构建及 systemd 同样终态成功。

### 四类队列、小时配额与实际 SQL 分块压力门禁

新增 `sql-pressure` 独立作业在托管 Linux 上打开真实一次性文件 SQLite，
运行生产 schema、收集权限、`destaudit.Service` 和单 worker。四类请求
交错填满原生产队列，独立解码每个请求，再由真实 worker 完整排空。每类
都必须因自己的批数或字节配额拒绝下一批，并在实际 SQL 中耗尽原小时
配额：每 Agent/UTC 接收小时的 block/observe/trial 各 20000 逻辑行、
usage 40000 逻辑行。原全局
256 批/64 MiB、分块 1000 行及 4/1/1/1 调度保持。

测试只在 repository 外包一层计时器，记录每次真实 Begin/Write 调用的
完整耗时，包含 gate 和事务，不能解释为单独的数据库锁等待。实际入库
事件、预算、queue_full/over_budget 的持久化损失与全部输入须完整对账；
trial 必须保持匿名，未知损失、超大分块、未排空和错误调度均失败。报告
仅保留固定身份/类别、数量和时间，不输出 SQL、账户、站点或 batch ID。

本组件尚待新增源码的首次托管结果；它是同一个生产 SQL worker 下的四类
队列与小时配额压力，尚未把 Node 聚合/pending、并发 HTTP 控制 Sync 和
真实流量轮询串入同一次运行。MySQL/PostgreSQL 压力、并发控制延迟和完整
长时间窗口仍属于后续混合压力验收，不能用这个组件替代。

首轮 `60d62419` 的 SQL 根实际失败且没有数字报告。后续 `867185fa`
保留的封闭诊断确认失败在最终 ledger，队列排空及七槽调度已完成。源码
复核发现夹具只使用一个 Agent，先触发原每 Agent 的 4/1/1/1 待处理批
上限，实际尚未填满全局队列，也不能耗尽其 20000/40000 行小时预算。
这两个失败原件继续保留，不能把单 Agent 饱和当作全局容量通过。

修正夹具创建 129 组真实一次性 SQL panel/Agent 记录，保留一对一所有权、
唯一凭据摘要及原每 Agent 上限；每轮每类从一个所有者只提交一批，下一个
全新所有者也被拒绝时才证明全局容量饱和。每轮原生产 service 完整排空，
连续轮次通过实际 SQL 消耗各接收小时预算，不预填预算行，不改实际时钟。
最多 64 轮、真实八分钟 deadline；UTC 小时变化时按所有实际预算桶对账，
每类所有实际接收的 Agent 都必须确实耗尽至少一个原小时预算并有超额。

报告区分全部轮次累计入队字节与实际同轮保留峰值，保留每个预算桶的行
数量及全配额 Agent 数，移除其身份。SQL 调用次数须按实际预算桶推导的
1000 行分块机会精确一致，完整事件/损失与每桶原上限同时核验。此修正
尚待新提交的托管结果；它仍是直接 SQL/service 组件，不是实际 Node 网络
守护进程或并发 HTTP 控制路径的验收。

同一 `867185fa` 原生矩阵的 `postgres/old/xray-pair` 在 native_ready
失败：十项 setup 均完成，peer 0 已实际运行；peer 1 保持心跳/epoch/选择，
没有运行核心或入站，私有输出的封闭 bind 分类为 true。全部 48 份原件
保留，两套全矩阵门禁正确拒绝。bind 分类只说明输出含该固定类别，尚未
证明具体端口或错误文本，不据此宣布完整根因修复。

复核发现原辅助函数用 port 0 探测后关闭，在 Node 真正监听之前，后续
自动分配的 httptest server 或连接可能占用所选端口；仅记录已选号码的
map 无法阻止其他自动分配。夹具辅助函数改为读取实际 Linux 自动端口
区间，在其之外轮换探测可用的非特权 loopback 服务端口；不修改内核设置、
不占用冲突端口、不改变回执或流量判定。纯规划回归覆盖前后区间、边界、
无可用范围及非法输入，实际端口选择仍须新的托管运行验证。

当前 `a2d2289b` 的 postgres/mixed/mixed-engines 已通过 native_ready 和
前序七个场景，但 one_peer_offline 在 4.15 秒失败，封闭类别为公开状态
不匹配。原夹具只等待至少三个失败同步请求及其他节点心跳；失败重试次数
不能证明已超过生产状态的三倍 poll 窗口，本夹具的 poll 为两秒。
后续仍保留原 partition barrier、失败请求数、心跳不增长、其他节点新鲜和
核心存活条件，并在同一真实 45 秒等待内读取生产 HTTP 状态：准确 peer
须超过完整六秒窗口并已显示 offline；旧能力节点继续显示 unsupported_version。
原后续单次流量及状态断言不变，不改生产阈值、心跳时间或增加流量重试。
回归覆盖四秒假就绪、六秒严格边界、公开状态仍 applied、身份/心跳借用、
缺项/重复与旧能力节点。失败原件保留，修正尚待托管结果。

### 首份真实 SQL 全局队列与各 Agent 小时预算证据

源 `a2d2289b9e06d18e81918a2639009d4775f39dc4` 的
[独立 SQL 作业](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/38286406342/job/114935725785)
实际成功。两个原件绑定准确源及 merge
`ba23664704263fc41f178a3a04936971bac70cbd` 的非过期 artifact，已由不可变
源码门禁与独立检查器完整通过：唯一根 RUN/PASS、包 PASS、封闭字段、原
全局容量和每 Agent/UTC 小时预算、准确 SQL 分块机会及全部数字对账。

| lane | 同轮实际峰值批数 | 同轮规范化字节 | 耗尽原预算的 Agent 数 | 入库事件 | over_budget 行 | queue_full 行 | 实际 SQL 调用 |
|---|---:|---:|---:|---:|---:|---:|---:|
| block | 50 | 33023450 | 50 | 1000000 | 1001000 | 40020 | 1950 |
| observe | 12 | 7973676 | 12 | 240000 | 240240 | 40020 | 468 |
| trial | 20 | 8286920 | 20 | 400000 | 400400 | 40020 | 780 |
| usage | 64 | 15376640 | 64 | 2560000 | 1280 | 40020 | 3776 |

129 组 SQL panel/Agent 注册记录支持二十轮实际填充及完整排空，每轮峰值
146 批/64660686 规范化字节；usage 因自己的 64 批上限拒绝，其他三类因
自己的下一批字节配额拒绝。累计 2920 批的规范化字节为 1293213720，
它是累计量，不是同一时刻的队列保留内存。

6003000 个逻辑输入行完整对账为 4200000 入库事件与 1803000 持久化损失：
其中 queue_full 为 160080，over_budget 为 1642920；没有未知损失，trial
账号/非零端口行为零。每个实际接收 Agent 都确实用满其原 20000 或 40000
行接收小时预算；本轮共 146 个实际预算桶。6974 个 repository 调用与原
1000 行分块机会精确一致，各类最大分块均 1000 行；二十轮排空共
82.100605732 秒。block/observe/trial/usage 单次完整 repository 调用最大
耗时分别为 294.018103/49.880544/46.208717/31.189737 毫秒，包含 gate 与
SQL 事务，不能解释为单独的数据库锁等待。

此为真实文件 SQLite、原 service/worker 的组件证明；这些输入是直接
收集器请求夹具，不是 6003000 次实际 Node 网络连接。它不代替同次 Node
聚合/pending 与 HTTP 控制/流量轮询、MySQL/PostgreSQL 压力或长时间窗口。
完整原生矩阵仍按当前失败场景及全部 48 份原件另行验收。

### 同一 SQL 压力的三种实际数据库

SQL 压力 job 扩为 SQLite、MySQL 8、PostgreSQL 16 三个独立作业，保持原
SQLite artifact 名及其已有证明，两个服务端方言分别保存自己的固定身份
报告，不能借用 SQLite 成功。三种方言使用同一四类输入、129 个独立所有者、
原队列和小时预算、1000 行分块、真实时钟及八分钟 worker deadline。

服务端连接只由 job 的固定一次性 loopback 服务产生，使用专属
psp_native_pressure 数据库；拒绝任何操作者 DSN 覆盖。实际连接须验证
driver/server 主版本和数据库名称，服务库必须为空后才运行真实生产 schema。
本机不运行服务数据库；实际结果由以下托管运行提供。
报告的 database 必须等于作业明确要求的方言；错误/未知方言、另一方言的
成功原件以及缺失实际执行身份均被拒绝，容量和数字对账条件保持。

首轮三方言实际证明来自准确源
`24bdeef4e142b38213042b89d948914536418c91`、merge
`55eacee4f62e8e58e48441fcf76217de09f4bb57` 的
[三个独立 SQL 作业](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/38293432542)。
SQLite/MySQL/PostgreSQL 均实际成功；六个原件及其非过期、准确源 metadata
已分别保存，不可变源码与独立检查器按每个明确方言完整通过，没有借用身份。

三种数据库各自都完成二十轮、峰值 146 批/64660686 规范化字节，
6003000 逻辑输入 = 4200000 入库事件 + 1803000 持久化损失；原预算、
6974 个真实 repository 调用及 1000 行分块机会均完整对账，trial 保持匿名。

| 真实数据库 | 二十轮排空合计秒数 | block / observe / trial / usage 最大完整 repository 调用毫秒 |
|---|---:|---|
| SQLite | 81.095435032 | 48.552093 / 41.262887 / 43.756585 / 21.104381 |
| MySQL 8 | 160.203361279 | 98.558478 / 73.687162 / 67.729587 / 76.715005 |
| PostgreSQL 16 | 148.586709391 | 537.138855 / 199.718971 / 402.141919 / 409.295736 |

这是各自一次性托管作业的实际测量，包含 gate/事务的调用耗时，不能拆解为
单独锁等待或推断生产环境性能保证。三种方言的 SQL/service 容量组件现在
均有实际证明；同次 Node 聚合/pending、HTTP 控制/流量轮询及长期窗口仍待验收。

### 运行中协调器和写库耗时的固定指标

为后续同次混合压力读取，增加三项实际单调时钟直方图：
psp_node_control_sync_ms 记录生产 control coordinator 的每次 Sync，包含
失败调用；psp_dest_audit_begin_ms 记录预算/marker/首块的完整 repository
调用，psp_dest_audit_chunk_ms 记录剩余分块调用，两者按固定审计 kind 分类，
包括存储拒绝/错误尝试。没有账户、Agent、站点、SQL、错误文本或动态标签。

协调器指标不包含 HTTP 认证、解码及网络往返；写库指标包含 backend admission、
repository gate 和事务，不能解释为单独锁等待。现有 psp_poll_stage_ms 的
sink_flush 提供流量轮询整段批量写入耗时。指标注册和对应失败/分块计数回归
须先取得托管测试结果；后续还需在同一次 Node/队列/SQL 压力中按测量窗口
采集这些指标及 HTTP/轮询延迟，不能用指标存在代替混合压力已经完成。

### 同次试运行的控制与流量写入测量

现有 200 用户、五分钟 trial + 独立 block 负载增加单独的
`destination-trial-load-latency.json` 原件，原有投递、匿名、损失、资源及
预算报告和门禁保持。仅该一次性 trial fixture 将真实流量轮询设置为一分钟，
由 `App.Run` 的生产循环执行，不直接调用或模拟 PollOnce。

在投递开始前、五分钟投递结束后分别读取固定直方图的累计桶并作差；
不重置生产指标，也不借用启动以来的最大值或分位数。读取不具有全局原子性，
边界以各直方图完成观测为准；桶/count 不一致时只重读有界快照，重置或
不一致差值拒绝。导出的桶只能给出 p95 上界，不能声称精确 p95。

报告区分完整控制 HTTP handler（包含认证/解码/协调器，不含网络往返）、
生产 coordinator、完整 PollOnce、真实 `sink_flush` 批量写入、block/trial
首笔与后续分块。后续分块可以确实没有样本；不为其生成虚假分位数。
门禁要求控制两项各至少 100 次、轮询及写入各至少四次、两类首笔调用
均有样本，并重新核对原投递报告的实际执行身份与相同持续时长。

该测量补齐两类真实流量下的同步/轮询耗时，仍不
代表四类 Node 聚合与 pending、PSP 队列、SQL、实际堆内存的同次过载验收，
也不代替 24 小时与真实保留期检查。

首次实际窗口来自源 `a074dd87d166977d2dbe3c18c042ff9cb00447f0`、merge
`dbae9e6e25c31edfcc3fe74577623a81dbc2b59d` 的
[trial 作业](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/38300093629)。
三份原件绑定准确源/merge/run 并保存，完整源码门禁和独立 trial 组件门禁均通过。

| 同一 300000 ms 负载窗口 | 实际调用数 | p95 桶上界毫秒 |
|---|---:|---:|
| 完整控制 handler / coordinator | 128 / 128 | 200 / 200 |
| 生产 PollOnce / sink_flush | 5 / 5 | 200 / 50 |
| block / trial 首笔仓库调用 | 110 / 18 | 10 / 100 |
| block / trial 后续分块调用 | 0 / 19 | 无样本 / 50 |

200 用户产生 330000 次允许试运行，独立 33000 次拦截全部入库，拦截损失、
传输失败及迟交均为零。trial 20000 入库事件 + 246272 节点丢失事件 +
63728 接收端超预算行 = 330000，保持匿名并完整分类对账；这不是无损审计证明。

该版本完整 Test、原生矩阵和 systemd 成功，但整个 Load 未通过：双 webhook
的 fifty_rules 阶段有 300 次控制失败、330300 次命中，实际负载投递为 330000。
原件完整保留，整体门禁正确拒绝，不能用成功的 trial 组件代替整体验收。
夹具的规则只匹配端口，两个不同地址分别请求自动端口不能保证数值不同；
具体冲突端口未保留，故不声称已从原件读出该号码。修复将控制目标绑定到
现有独立显式端口分配器，并在发布前验证 50 个目标端口与控制端口不重叠。
新增回归将在两个 loopback 地址绑定同一个数值端口，验证不同地址不能作为
端口规则的隔离依据；三个负载作业均先跑该回归。原持续速率、损失对账、
控制成功和超时门槛保持。修复源
`2e2308a521c888d68a8494bd7a18b5ec5e1cb3eb` 的
[七项负载](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/38303329603)
已全部实际成功，15 份原件及完整准确源/merge/run metadata 保存。
single_rule 与 fifty_rules 各 330000 次实际拦截、300 次成功控制、零传输
失败，入库命中各 330000；全部七项源码门禁及独立双负载/同次延迟组合
门禁完整通过，未放宽或重解释原条件。

### 负载期间的记录首页与策略首屏 API 查询

现有 trial 作业增加第四份独立原件 `destination-trial-load-pages.json`。
在同一个五分钟真实负载窗口内，每两秒读取一次实际管理员 TLS API，最多
149 次读取记录第一页（本面板、include_trial=1、50 行）和策略 overview。
验证已发布 generation、自己的策略/试运行分组、页码、页大小、行数、
来源以及 trial 的匿名字段；200 HTML、未发布/其他夹具、标识泄露或读失败
不能计成成功样本。请求持续时长包含 TLS/网络、完整响应读取与校验，报告
仅保留单调窗口偏移、微秒耗时和行数，不保留请求凭据或私有响应内容。

门禁重新验证原 trial 执行身份与持续时长；每个 API 至少 100 个真实成功
样本，覆盖开始至最后二十秒内、间隔不超过五秒，全部在原负载窗口完成。
末次记录页总量须达到 20000 行，策略页仍为本次的一个 block 与一个 trial
分组。p95 使用实际请求时长样本的 nearest-rank 值，不借用旧窗口统计。
失败时保存已有数值样本，原投递/损失/延迟证据也继续形成，最终 Go 场景
明确失败；不以部分页面成功替代完整通过。

首次实际查询来自 `a787e88dcc28148634de6a31cebaffc8c69ddf02` 的
[trial 作业](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/38306623646)：
两个 API 各 149 个成功样本，记录第一页 p95 138.293 ms、末次总量 20001，
策略 overview p95 11.253 ms、末次两项对象。全部 16 份负载原件及准确源/
merge/run metadata 保存，独立双负载/延迟/页面完整组合门禁通过。该源
完整 Test/systemd/七负载成功；原生首次尝试编译器下载失败、无该报告，
保留 47 原件后仅定向重跑已终止失败作业，同源补齐第 48 份并通过 861 场景。
这些历史查询数值不冒充新 Node 观测候选的运行结果。

此处测量 API 首屏请求，不代表浏览器渲染耗时；五分钟、20000 行规模仍
不能替代 §40 的 24 小时数据规模，四类同次过载和 50k 域名空闲成本也仍需
各自的实际证明。

### 运行中队列的只读占用视图

`destaudit.Service.Pressure()` 在同一队列锁下返回四个固定 kind 的 waiting、
active、保留批数/规范字节、原批数/字节/单 owner 上限、owner 数和最大单
owner 占用。不导出面板、Agent、batch、账户或目的地，也不读取私有行或
存储。已取消但 worker 尚未退出的 active 仍保留原额度；读取不推进调度、
释放或重新解释占用。每类 waiting+active 与实际 reservations 一致，原合计
256 批/64 MiB 和 4/1/1/1 单 owner 限额保持。

该视图用于同次 Node/queue/SQL/heap 压力采样，本身不等于压力已执行。
`d178a30663e3d5450836973ee29efc65a027f26d` 的完整 Test/systemd、
[原生 48 项产物 / 861 场景](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/38315349673)
及[七项负载](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/38315349554)
均实际成功。全部 48/16 原件及完整 metadata 已保存，固定源矩阵、独立
负载/延迟/首屏组合、三种 SQL 和队列门禁通过；它们仍各自保留证明边界。

### 四类同次真实连接、冻结重试与恢复

新增独立 `mixed-pressure` 作业，固定同一 Node 候选、Xray 26.6.27、
SQLite 和实际 `App.Run`。复用原试运行的生产会员服务及完整已应用 roster/
config/policy 回执，保留原五分钟双类试运行、独立 block overflow、Webhook、
队列和三种 SQL 作业。新的数值报告及测试执行身份独立保存，不能借旧报告通过。

90 秒内并发发送每秒 800 个唯一 block、400 个 observe、1100 个匿名 trial、
1000 个无命中 usage 目标及一个独立控制连接。域名形式 VLESS 连接使用本次
nonce 的 `.test` 名称，映射到作业自己拥有的 documentation IPv6 /128 服务；
不查询公共名称。只在已限定的一次性 root GitHub runner 上追加 hosts，退出
恢复原文件。原因是 Xray accepted 日志记录原始请求目标，单独伪装 HTTP Host
不能证明 usage 的唯一键压力。Block 的唯一嗅探域名仍只路由到其自有数值目标。

开始 75 秒仅在本次 TLS 夹具返回真实 503，阻止 PSP 接收原报告；Node 按生产
退避形成并保留四类 pending。之后把同一原请求交给生产认证 handler，私下
比较每类第一次恢复成功的 canonical subtree SHA-256；报告只导出比较次数，
不导出 digest、batch、账户、目的地或私有 HTTP 内容。窗口长度覆盖生产退避
的一轮七个调度槽，不修改时钟、ACK、调度、额度或核心配置。

显式启用 `--audit-metrics`，严格解析固定数值 journal。门禁要求某一实际采样
时刻同时具有四类键上限、四个 pending 和各自非零 retained dropped；不能
拼接不同时间的峰值。采集实际整进程 Go heap、每 10 ms 的真实队列保留批数/
规范字节及每秒 PSP heap。完整校验原每类/单 owner/合计上限，并保存同窗口
control Sync、真实 PollOnce/sink flush、四类 Begin/Chunk 完整调用耗时的 CDF。
这些耗时包括等待和执行，不能称作单独的 SQL 锁等待时间。

每次目标唯一，hit 三类逐一核对真实投递与 `dest_hits` 已存事件加各原因
持久化丢失，试运行仍须匿名。三类 hit 每小时原预算均为 20000，usage 为
40000；旧 SQL 组件的 4000 条 observe 输入不是预算。

`2616824e` 首次实际混合运行完成四类投递、四类 canonical 重试和最终排空，
但 usage 225090 个实际允许连接仅有 39999 个存储事件、137852 个 Node dropped
及 18193 个 over_budget，共 196044，差 29046；因此作业失败，18 份 Load
原件全部保留。该源另外完整 Test/systemd/Native 成功，48 份 Native 原件
独立核对 861 必需场景通过；这些结果不能替代失败的混合门禁。

固定 Xray 26.6.27 的 `common/log/logger.go` 有一个 128 条、满时无计数丢弃
的上游队列。Node schema 2 增加只读数值过滤器入口及 usage/denied/unmatched/
dropped/stale 分类、解析队列和未刷损失数。修正 overflow 不把已识别 denied
记成 usage 丢失，畸形/截断输入以原接收小时计入独立 bounded unmatched。
这些计数测量 Node 实际收到的行，不能测量核心内部队列丢弃数。

新混合门禁保存已完整排空的 warmup 前值与至少三个真实、相隔两秒且稳定的
结束快照。先核对 private 输入等于各分类之和及无新增 stale，再将收到的
usage、有效允许 overflow、unmatched 与 usage 已存事件加持久化丢失精确
核对。实际允许连接减入口允许数的差额单独报告为核心日志未收到；实际
denied 与入口 denied 的差额也独立报告。禁止把这两项伪造为 Node dropped
或用它们填补入口之后无法解释的存储差额。原 29046 差额仍是失败旧源结果，
不能当作新源运行数字。结束须实际排空解析队列、未刷损失、Node 四类
retained/pending 和 PSP queue。错误 kind、缺失重试或未投递连接继续失败。
部分失败仍保存已有数值报告。PSP heap 包括同一测试进程的负载夹具，不能
解释为隔离 PSP 服务的独立内存成本。

该作业只有一个真实 native owner，最大保留批数为 4/1/1/1 合计七个；它不能
证明 256 批/64 MiB 的多 owner 同次全局饱和。现有多 owner SQL/队列组件证据
继续独立保留，完整 §40a 的多 owner 集成压力及 §40 的 24 小时规模仍未完成。
修正后的混合场景仍需其准确源的实际托管验证。

混合矩阵扩展至 SQLite、MySQL 8、PostgreSQL 16。每个作业有独立真实 Node、
生产 App.Run、原队列及同一作业内的真实 SQL 连接池；复用已有专用托管
数据库配置与实际版本/库名校验，禁止外部 DSN。只读断言使用对应驱动的
占位符，报告绑定实际后端，并由门禁校验与矩阵选择一致。SQLite 保留原
artifact 名，另两个后端各保存独立测试和数值原件；原七个 Load 作业保留。
这三组运行仍各只有一个 native owner，不能宣称多 owner 全局饱和。

`227722d1` 的 SQLite、PostgreSQL 混合原件已由 immutable 源码和独立门禁
核验通过：同次四类上限/pending/丢失采样分别 45、46 次，投递全完成、四类
canonical 重试及最终排空通过。过滤器前未收到的允许日志分别为 22272、
25177，denied 分别为 3749、3923；入口之后的持久化账精确一致。它们是
具体运行的日志链路差额，不是核心内部丢弃计数。

同源 MySQL 的服务、候选构建与所有投递完成，但数值账读取失败，十项 Load
九通过、一失败，22 份原件保留。只读损失查询中的未限定 `SUM(rows)` 与
MySQL 8 的保留字 ROWS 冲突；修正为表名限定列，参数仍独立绑定。后续
MySQL 混合作业须先在其实际专用数据库复现旧查询的 1064，再验证限定列
查询成功，报告仅保存数值回执，随后执行完整压力与对账。新源仍待验证，
不能用另外两后端的通过替代。

### Fifty-thousand-domain idle cost gate

新增独立 `idle-cost` 作业复用真实托管 App.Run、固定完整 Node 与 Xray
26.6.27。管理员通过实际 HTTP 创建 50000 条独立域名的 custom list、引用
它的 block 策略、关闭采集并发布；必须核对实际 list 数、已确认 applied
policy 数与正在运行的核心配置 domain 数均为 50000，并核对 config/roster
完整回执。控制流量在测量窗口外验证，窗口内没有负载请求。

先取五个不带跟踪的真实空闲同步周期，再取五个单独标明带跟踪的周期。
每轮只允许一次实际成功控制 Sync，读取原 Node 与核心的 `/proc` CPU ticks
及实际 CLK_TCK；验证进程 birth、原核心 pidfd、SQLite 文件身份、候选与
配置/roster 不变。该夹具保持已有 2 秒 Sync cadence，报告每轮耗时与 CPU，
不能解释为默认 cadence 的持续 CPU 比率或更长空闲窗口。

文件写入阶段仅在一次性自有 Linux job 中把 strace 附加到准确 Node PID，
实际验证 TracerPid 与 observer PID 一致；不修改安全设置、策略、数据或定时器。
`-s 0` 抑制 buffer 内容，原始 trace 保存在私有临时目录且不上传。严格
读取器按成功 syscall 返回字节、调用入口时间和线程身份，对准确 state.db、
其 WAL、journal 分别计数，排除其他文件和 SQLite SHM；短写按实际返回值，
错误、遗失/未完成调用、内容转储、共享可写映射或模糊 state 路径都失败。
只导出固定数值每轮报告。跟踪 CPU 单独呈现，不能替代前五轮未跟踪 CPU。
这些是逻辑 syscall 写入字节，不是文件增长、物理磁盘写入或全进程 I/O。

原 idle_sync 的三轮身份与回执验证保留，仅提取可供新五轮观测复用的内部
helper；原十项 Load 和 Native 矩阵保留。新增作业另存两份数值原件，Load
合计十一项、二十四份原件。当前只完成编译与解析器/门禁回归，实际五万
域名 idle 数字仍待准确新源托管执行，不计为已完成的成本证据。

首次 `6f576c33` idle 实际运行的 list/publish 已达到 50000，但夹具把管理
设置的 `off` 与协议的空 CollectLevel 混为一谈，等待条件不可能成立；180
秒后失败，尚未开始 CPU 或写入测量，报告 cycles 为空。失败的两份原件
保留，不能作为零成本证明。修正改为复用 nativeCollectionReceiptMatches
检查协议空值及实际关闭 revision、mint/applied/reported 摘要一致，不改变
关闭设置、策略规模或 Node 生产行为。新增回归已先验证旧条件失败；新源
仍须重新取得完整五万域名运行回执和测量。

`e2482a0f` 修正协议关闭值后的实际 idle 原件仍在 180 秒等待阶段失败，
尚未进入测量；旧报告无法区分余下哪个条件失败。后续数值报告增加
`receipt_checks`，逐轮重置，位 0–12 依次代表 definitions 可读且未暂停、
runtime 存在、agent 存在、观察引擎 Xray、epoch 一致、固定 desired core、
因果回执、关闭采集 revision/digest 回执、applied JSON、单条准确 block 规则、
50000 域名、config 收敛、roster 收敛。完整值必须为 8191 才进入测量，
applied 域名数按实际读取填写；不导出原始回执、摘要、身份或私有字段。
新增观察没有降低任何原条件，实际未满足项仍需该源运行结果定位。

`dbe63e10` 实际报告为 7999（8191 去除位 6、7），已确认 applied body 的
50000 域名、准确 block 规则、固定引擎/epoch 及 config/roster 均满足；
失败集中于因果候选及关闭采集回执。生产 compiler 在 Collect 为空时不写
CollectRevision，协议准确值为 0，不能与管理记录的 revision 混用。夹具还
在发布前捕获了已 mint 的 pending 候选，覆盖实际策略编辑基线。修正把
基线捕获前移到创建策略之前，并使用关闭协议值空字符串/revision 0，
同时独立确认管理设置确为 off；其余摘要、真实新回执及 8191 条件保留。
新回归先验证旧写法失败，完整实际 idle 测量仍待修正源重新执行。

`ba751db5` 实际报告为 8127，仅因果回执未满足；关闭采集的协议回执已
通过，测量仍未开始。夹具此前没有等待启动回执，第一次同步可以在编辑
前取时、编辑后才 mint，缺少有效的变更前候选。现在在任何管理员编辑前
等待真实初始候选的准确回执、已接受同步及 config/roster 收敛，再捕获
编辑基线；编辑时间与生产 destWriteTime 一样使用毫秒精度。启动等待在
测量窗口外，不改变生产时钟或原因果校验。此修正仍须新源托管运行验证，
不能把上一源已 applied 的五万域名计作 CPU 或写入成本通过。

`0abf2af2` 实际运行已达到 8191，published/applied/core 均为 50000，
原进程与单次接受同步绑定的 plain/traced 各五轮 CPU 采样均完成。之后
失败，第二次控制请求尚未进行，state_writes 全为 null；这只证明采样阶段，
不能作为完整空闲成本通过，也不能把 null 当成零写入。失败原件保留。
该失败原件的五轮均值为 plain Node 122ms/Xray 2ms、traced Node 198ms/
Xray 2ms（CLK_TCK=100，夹具 2 秒同步周期）；这些是失败原件中的 CPU
观察值，跟踪阶段有观测扰动，不能替代完整成本验收或默认周期的成本。

idle 数值报告升级为 schema 2，另加 trace_stage/trace_error，不上传私有
轨迹或 stderr。stage 1–6 分别为跟踪五轮完成、observer 已停止、已确认
detach 且窗口文件就绪、读取器成功、五轮数值已解码、最后控制请求通过；
完整门禁另要求 stage=6/error=0，原 8191、身份和五轮写入条件都保留。
错误 1–15 仅表示固定解析类别：输入、路径形状、窗口、时间戳、内容转储、
行格式、未完成/恢复调用、fd 身份、路径歧义、共享可写 mmap、msync、
返回值、未知 syscall、计数溢出、线程调用重叠。16–20 为读取器不存在、
读取器执行失败、数值形状不完整、observer 停止失败、未 detach。
任意运行时错误的原始文本仍私有，只有严格解码的固定数字类别可导出。

同源 `0abf2af2` 完整 Test 的 race 3/3 也在 App 整包四分钟预算到期时
失败，当前 TestBuildBaseDestinationAccessNeverCallsUsageRepository 仅运行
1 秒；原件及堆栈保留，未出现 race 报告。与前次同类整包超时一起说明
需要拆分迁移密集的 App 套件，不能通过延长超时或忽略失败来判定通过。
App 现在独立按真实 go test -race -list 的 Test/Fuzz/Example 根名称轮转到
三分片，原模块规划同时排除 SQLStore 和 App，并逐一校验排除名称存在。
原四分钟测试预算保留，SQLStore 原分片保留，SQLite 必需门禁同时检查
模块及 App 两组结果。每个 App 作业保存完整名单、选中名单及逐项结果；
回归实际执行排除规划和测试轮转，验证没有遗漏/重复，错误排除名会失败。
新 CI 分片的实际完整运行与原件核对仍待托管验证。

`503119b0` 实际 idle 报告 stage=1/error=19，十三项回执及 plain/traced
各五轮仍通过；失败明确发生在停止 observer，读取器尚未运行。运行器
使用 strace 6.8，其 [terminate 实现](https://github.com/strace/strace/blob/v6.8/src/strace.c#L3982-L4023)
先 detach/flush/close，再重发收到的信号；主动 SIGINT 会成为非零等待结果。
夹具现在只在确已成功请求 SIGINT 时接受准确 SIGINT/130 状态；失败请求、
其他信号、普通非零退出或未知 wait 错误仍失败。Linux 预检用六种真实
子进程退出及失败请求验证该边界；完整验收还要求 TracerPid=0、原核心
存活、完整私有轨迹解码及末次控制请求。observer stdout 必须为空，stderr
只准许 bounded 数字 attach/detach 消息，异常输出记 error=21 且不导出
原文，避免中断状态掩盖跟踪器的真实错误。修正后的实际成本仍待验证。

`503119b0` 的三个 App race 分片已全部成功，三份完整编译名单一致，
选中名单准确轮转、无遗漏/重复。九份原件独立核对的 211 个测试根均为
RUN1/PASS1，没有失败、跳过或缓存结果，原四分钟预算保留。该组件证据
不替代同源其余完整 Test、Native、压力及空闲成本结果。

`2583ac11` 完整 Test、systemd、49 项 Native 作业及十一项 Load 均成功。
完整 48 Native/24 Load 原件及 source/merge/run metadata 保存；861 个
Native 必需场景、三后端 mixed/SQL、queue、原 webhook/trial/首屏查询及
新 idle 成本已独立核验。三份 App 编译名单及九份原件另独立核对 211 个
测试根，每个仅在一个分片 RUN1/PASS1，没有失败、跳过或缓存结果。

首份完整五万域名成本原件为 schema 2、receipt_checks=8191、stage=6/
error=0、control_successes=2；published/applied/core 均为 50000，plain/
traced 各五轮均仅包含一次真实接受同步，原进程及 SQLite 身份保留。
2 秒夹具、CLK_TCK=100 下 plain Node/Core CPU 均值为 88/2ms，traced
Node CPU 均值 172ms。每轮 DB/journal 写入为 0，WAL 五轮分别为
8240、16480、16456、16480、24720 字节，均值 16475.2 字节；对应写调用
为 4/8/7/8/12、sync 调用 1/3/3/3/5。这是确切 state.db/WAL/journal 的
成功逻辑 syscall 字节，测量周期及跟踪扰动均保留，原始轨迹不上传。
它完成五万域名成本组件；同次多 owner 全局压力、真实 24 小时数据规模、
物理表体积及实际保留期/重启仍按原范围待验收。

多 owner 全局压力的下一阶段先补齐 fixture 边界，尚未计作全局压力通过。
owner 数量从实际 Pressure 的 batch_limit/owner_limit 推导；四个原上限
128/32/32/64 与 per-owner 4/1/1/1 需要 65 owner 才能覆盖批数上限及拒绝。
启动持有只包装原 destinationAuditRunner.Run，原生产 Offer、权限通知、
队列、最终 SQL worker 保留；回归通过实际 App/SQLite/认证请求核对持有期
控制响应与入队、关闭/重开撤销旧 revision、释放后只持久化一次。
公共 Xray 缓存复制仅允许 xray/install.json 两个正规不可 group/other 写的
文件，验证 metadata、SHA-256 与路径；拒绝 symlink、未知字段、摘要变化、
私有文件及已有目标。每个后续 Node 仍正常执行 catalog/摘要/version 校验，
状态、runtime、凭据与日志不复制；文件回归使用明确的合成摘要向量，
不执行合成核心。独立 Linux fixture 预检接入必需 SQLite 总门禁；它只是
夹具隔离与生命周期测试，实际 65 Node/Xray 压力及三 SQL 运行仍待完成。

`7a69afc6` 的四项完整工作流及两项 Linux fixture 预检已实际成功；完整
48 Native/24 Load/9 App 原件另独立验证，原压力与空闲成本门禁保留。

后续 `global-bootstrap` 三后端作业只验证全局压力的真实进程前提：完整
不可变 Node `184f4e23` 启动 65 个独立认证 daemon，每个具有独立新建的
SQLite state 和实际 Xray 26.6.27 子进程。首个实际安装、验证并运行的
公共核心缓存复制到其余 64 个新目录；不复制状态、runtime、凭据或日志。
生产 App.Run 和成员同步部署同一个实际用户，等待每个 owner 的新 config/
roster 回执，再通过每个真实 VLESS inbound 验证控制 nonce。成员部署可以
合法替换空 roster 核心，因此最终 pidfd/进程/配置绑定在真实控制成功后
建立，最后检查 65 个原父进程和核心仍存活、state 文件身份各不相同。

产物只有闭合的测试事件及十五字段数值报告；完整前提要求 stage=9，
65 parent/core/state/stream/control、64 次公共复制、shared_users=1 且
pressure_complete=0。缺失项、缓存/跳过、额外私有字段或错误来源均失败。
该前提不证明 200 用户、多 lane 同次过载、全局 64MiB 队列及重试损失对账；
这些压力项及长期验收仍须分别完成。启动前提的结论须绑定同源实际运行
和三个后端的原件；编译或合成报告不能替代这些证据。

全局压力另需在预热入库完成后重新持有 SQL 入口；仅持有 worker 首次启动
会让预热的小批次占住每个 owner 的单一槽位，不能证明后续字节上限。
包内装配接口允许测试装饰已受 operation gate 管理的原 audit store；生产
Build 固定传 nil，不暴露配置或另一 collector。测试只在原 Begin 调用前
等待可释放的阶段信号，其他 repo 方法、权限 watcher、时钟、配额和实际
事务均转交原存储，不伪造成功或预留结果，也不持有后台迁移的读许可。

Linux 集成预检用实际 App/SQLite/认证协议向量验证两个可重复释放的阶段：
worker 的真实 active reservation 保持、控制流仍返回、重传只入队一次、
释放后原 SQL Begin 仅调用一次且队列排空。等待期间真实关闭/重开记录后，
原 SQL 必须拒绝旧 revision；取消等待不进入事务、不返回合成成功。它是
存储与装配边界验证，不代替实际 65 Node 的全局过载和损失账本验收。

后续压力运行共用原 65 Node 启动路径，返回前仍须完成原 stage=9 的全部
检查；默认 bootstrap 不启用额外观测。压力夹具可以在实际进程启动前
选择只读 audit-metrics 和作用于原认证 handler 的测试传输包装，复用原
私有输出解析器，不在运行中更换 handler、不复制私有状态或合成指标。

多 owner 传输按已验证的实际 agent/probe 配对选择各自的观察包装，进入
原认证路由前恢复原始 bytes 与 headers；不重新序列化报告。未知身份、
畸形或超限报告仍转交生产拒绝路径，原读取错误也保留。App/SQLite 预检
以两套实际注册凭据和协议向量检查正确认证、错误凭据、foreign input、
重复/未绑定 observer 及原 bytes，未启动定时分区，不产生重试通过记录。
该预检不证明 65 个实际进程的四类 canonical retry，后者仍须实际运行。

新增 global-batch 三后端作业复用实际 65 Node/Xray 夹具与 200 人试运行
部署路径，逐 owner 等待当前 collection revision、实际 policy digest 和
完整成员/listener roster，再绑定部署后的核心。小规模域名映射共用同一
nonce namespace，仍使用 domain-form VLESS；不复制状态或修改节点时钟。
原件只有启动数值报告、闭合测试事件和批数压力数值报告。

每个 owner 四类各投递 1200 次连接、120 次独立控制；每名试运行用户
实际服务 390 次。所有节点都经过原 75 秒 HTTP 分区与四类首个 canonical
retry 验证。SQL 只在实际预热入库排空后暂停原 Begin，120 秒流量期间
不得提前委托；10ms 实际队列采样须同次到达 128/32/32/64 共 256 批，
每 lane 都有真实入库 queue_full 行损失。释放后要求所有 owner 三轮稳定
入口读数、队列/四类 pending 排空、各自存储/损失账本精确对账和原进程
存活；PSP 全堆采样及十二个真实控制/poll/Begin/Chunk CDF 一并保存。
低行数批次可以没有后续 Chunk 事务，零样本原样保留，不能伪造延迟。

此组件要求 global_batch_complete=1，但 byte_saturation_complete、
key_saturation_complete 与 pressure_complete 必须仍为 0。它不能替代
真实字节拒绝、键上限/Node retained dropped 过载或 §40 的 24 小时运行。
代码及数值拒绝回归已接入；真实三个后端的运行结论仍待托管验收，合成
数值测试和编译成功均不计作该项通过。

独立验证源 `2f3b4b65` 的 SQLite bootstrap 原件在 stage4/started3 失败，
固定诊断为 idle_core_restarted；它没有执行流量，不能记为压力失败或通过。
此前缓存复制把首个空 roster 核心的 PID 绑定跨越了后续 provisioning，
而生产成员同步此时可合法替换核心。复制现在逐次读取当前核心的父进程、
准确运行命令、版本缓存路径及实际 executable inode，结合真实版本/流
状态后再复制验证过的公共文件；最终部署后的 pidfd 稳定性检查保持不变。
修正的真实运行仍需新源验证，失败原件保留。

诊断源 ff3 的 MySQL mixed 根测试完成，原门禁仍因实际每秒完成数拒绝：
总数 72000/36000/99000/90000、零失败/迟到和完整账本保留，但 trial/usage
各一个秒窗口为 920/824，低于原 1000/900。生产服务未改，原失败产物保留。
测试 producer 改为每 10ms 的 8/4/11/10，替代每 100ms 的 80/40/110/100，
保持各 lane 速率、总请求身份、90 秒和 90 次控制不变，减少 FIFO 大突发
让后两类完成跨秒。实际秒窗口、四类同次上限和账本通过门禁保持原值，
改动后的托管结果尚待确认。

同源 ff3 的三个 global-batch 根测试均在 20 分钟套件预算内终止，实际
完成成员同步数为 SQLite 47、PostgreSQL 68、MySQL 80；各有 65 个原生
owner，仍在 stage1，流量与 SQL 暂停入口尚未开始。固定诊断同时标记
套件超时、setup 和生产 ResyncMembership 调用栈；这些失败原件保留。
全局夹具现在最多并发同步 8 名真实用户，每人仍走完整生产成员同步与
持久重试路径，主线程在调用完成后写入数值进度。普通单节点夹具保持原
逐用户创建、同步顺序；全局场景仍须完成全部 200 人、65 份实际名单、
真实流量及原账本门禁。没有增加超时或降低任何完成门槛，运行结果待验收。

源 56e8b291 的 MySQL 小分片实测四类每秒最低完成数为
784/380/1043/949，原吞吐门槛通过；总数、控制和账本完成，但实际末尾
42 次响应晚于90秒，根测试仍失败。原来的100ms分片最后发包在89.9秒，
10ms分片错误地延后到89.99秒。现在将相同9000个小片均匀铺到原89.9秒
最后发包点，恢复原100ms收尾余量；90秒观测、全部请求身份/总数和零
迟到门槛不变。该修正仍需新的同源实测，未将失败记录改成通过。

源56的MySQL/PostgreSQL全局场景已完成全部200人同步，继续在stage2等待
当前策略/名单回执时失败。检查发现，夹具在64个节点的collection编辑
之后才读取因果基线，可能把已生成但尚未确认的新候选当成编辑前状态；
随后无改动publish不会再次生成候选，原严格回执判断因此无法确认转移。
现在在collection编辑前读取各节点原候选，最终仍要求新鲜心跳、当前
generation、准确collection revision/digest、收敛名单及全部200人。
既有因果回执回归明确拒绝错误的pending基线，未放宽回执判断；新的
全局运行结果仍待验收，旧stage2失败保留。

源59的50k idle实际观测在trace_stage3/error13失败，CPU的五轮plain/
traced观测已完成，写入字节未解析成功，不能填零或宣称该项通过。受控
回归复现解析器拒绝restart_syscall的同一类别；Linux内核可能在停止/
恢复后重启futex、poll或sleep等待。解析现在只接受完整、已知的非写入
等待重启，并保留线程配对检查；写入重启、未知调用、状态路径混入和
未闭合调用仍拒绝，不产生SQLite写入计数。该回归证明一个解析缺口，
尚不能证明旧失败的具体调用就是它，真实trace验收与旧失败原件保留。

源d507的PostgreSQL mixed根测试通过，但第2–3秒的实际吞吐门禁失败：
四类最低631/292/793/730；总数、零迟到、90次控制和账本仍完成。控制
连接原来在发包循环里同步等待，可能暂停所有lane投递。现在将相同90次
真实控制放入现有768-worker/2048-job有界池，按实际90秒截止时间确认；
控制不进入四类投递计数，不合格响应仍计失败。原吞吐、总量、零迟到、
控制、配额和账本门禁保持，具体实测结果仍须新的同源运行确认。

手动workflow_dispatch可选all、mixed-pressure、idle-cost、global-batch，
默认或空输入为all；PR/main事件始终运行完整矩阵。独立选择校验拒绝
未知输入，选择值只经环境变量传入固定case，不插入shell代码。针对性
运行的原件仅证明选中的场景，其通过不充当完整17份产物/全范围验收。

源d507的MySQL/PostgreSQL全局场景仍在stage2失败，各已完成全部200人，
尚无压力流量。因果基线修正不能被当作完整修复证明。失败清理现在仅
输出固定就绪类别：state/store/capability/streams/receipt/collection/
shape/subjects/roster/complete，定位实际失败门禁；不输出节点身份、
名单、候选内容或凭据。诊断不改变原通过条件，也不能让失败根测试通过。

源e9三后端均在stage2失败，固定类别streams_inbound；配置/名单流收敛
检查已通过，但真实状态或inbound读取/检查未通过。继续细分时保留所有
原检查，按typed error区分快照missing/offline/stale、其他read错误，
以及对象absent、核心runtime字段不符或inbound shape不符。只输出这些
固定类别，不输出错误详情、状态原件、目的地或身份；未增加超时或改变
实际快照新鲜度要求。真实根因仍须下一次同源原件确认。

Windows前端子进程池曾在Node24/26及不同worker数下发生原生访问违规，
分别落在QueueTab、AccessControlView和SettingsView。Node26线程池对照
运行相同完整套件，2505项通过、保留原1项条件跳过且没有未处理错误。
随后Node24线程对照仍发生原生退出，Node26默认命令对照又以另一个
原生异常码退出。因此单次线程池通过不能证明可靠修复；未提交的测试池
试验已撤销，原配置与全部失败日志保留。没有缩减测试或忽略异常，Windows
故障仍未解决，必须进一步取得原生诊断；Linux完整前端继续单独记录。

源57的三后端原件均在stage2、200人已建立且traffic_ms=0时失败：
MySQL/PostgreSQL为streams_inbound_offline，SQLite为streams_inbound_stale。
因此后续仅在失败清理时输出等待节点的真实同步请求年龄、完成年龄、
最近耗时/HTTP状态、在途数及父进程存活值；没有请求或完成记录的字段
保持缺失。输出经过字段与数值上限白名单，不输出节点身份或原始正文。
传输观察不会满足就绪条件，完整压力及账本门禁仍保持。

Windows在受控ProcDump线程池对照中未复现原生异常，也未生成dump；
该轮2497项通过、8项在原5000ms门限超时、1项既有条件跳过。
调试器命令退出0不能替代Vitest失败结果，本轮不能作为完整测试通过。

完整报告仍记录真实心跳、流回执及最新FULL观察；附件已经applied、
版本和三项确认凭据均相同且无失败时钟时，省去重复附件事务。
非applied、版本/凭据变化、失败时钟清理仍写入；陈旧epoch/ETag和
已经变化的desired客户端仍由原匹配门禁拒绝。201人回归覆盖这些边界，
客户端查找改为单次索引，避免每个对象再次扫描全名单。该优化是否恢复
65节点压力场景的新鲜度，须新的真实运行确认。

源f597三后端仍为stage2离线，等待节点的父进程存活、最近HTTP状态200，
最近同步耗时分别约15.0/16.4/18.1秒。源0016的PostgreSQL最新原件仍
在stage2快照过期，最近耗时约6.6秒；不能据此声明全局场景已恢复。
配额构建另将逐账户GetByID改为现有ListByIDs的当前、去重批量读取，
保留其500-ID分片和计划/分组解析。缺失、空行、外部或重复身份及
读取失败不返回部分配额；空名单不进行全局账户读取。601账户边界
回归及既有临时访问/流量配额回归必须共同通过。

源8ba完整Test 18项通过，但全局原件仍失败：SQLite在stage1完成153人；
MySQL/PostgreSQL在stage2完成200人后离线，最近同步分别约7.1/8.7秒，
父进程仍在、HTTP200且有请求在途。账户批量化不能被当作全局修复证明。
继续定位时，控制协调器按固定desired/ingest/policy/panel/config/roster/
directives/tasks/finish记录实际阶段耗时，包括失败返回；没有身份标签。
准备阶段每完成50人及stage2失败时输出已有直方图的count/mean/p95/max，
只允许固定字段与数值范围；即使全套超时，先前阶段观测仍留在原测试流。
直方图分位数是累计、分桶估计，不能替代通过原件、损失账本或压力门禁。

源c9的阶段原件确认三后端报告持久化最慢，累计平均约2.8/3.1/5.0秒，
p95分桶估计约11.9/9.5/24.8秒。SQLite在准备阶段完成135人，另外两种
后端完成200人但仍无压力流量。前端遗漏的新指标目录/文案已补齐，
源e2完整Test 18项与278项诊断相关测试通过；旧失败继续保留。
后续将同一报告的客户端附件观察交给可选批量仓库端口：每250个精确
client/node对读取一次、500个参数以内，同一事务按原输入顺序应用。
重复的四种状态均避免无变化写入；pending/rejected保持原失败时钟，
只有applied可更新确认版本/凭据，blocked清除时钟并保留确认凭据。
缺失附件不创建，晚期写入失败整批回滚，旧单条端口保留为兼容路径。
该修复仍须实际三后端运行和原压力门禁确认。

配置准备的失败观测补充：`psp_node_control_config_error_total` 按固定阶段
catalog/build/compile/repository/capability/encode/mint 和类型
canceled/deadline/conflict/validation/unavailable/other 计数。类型按错误链
判定，不读取原始错误字符串，身份、候选正文和数据库参数不进入标签。
现有返回错误、重试、回执、时钟和完成门禁保持；这个计数不覆盖认证或
其他控制阶段，也不证明节点就绪。诊断页面同步提供中英文指标及 42 个
固定组合的文案。全局准备进度和失败清理仅输出该已有计数的固定名称
及 1–1000000 数值；缺失字段不能填成零或解释为没有失败。

源 `bdff6006` 的全局三后端仍全部套件超时：SQLite 200 人、stage2；
MySQL 200 人、总体 stage1 但已进入 receipt；PostgreSQL 198 人、membership。
三者流量与四类完成标志均为零，没有压力 profile 或根测试 PASS。
配置阶段与名单阶段次数不同只提示存在早期返回，不能据此指定根因；
新的分类计数仍须同源真实运行确认。旧失败原件继续保留。
