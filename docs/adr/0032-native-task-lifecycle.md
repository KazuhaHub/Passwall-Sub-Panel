# ADR 0032：原生任务的到期、结果证据与恢复边界

- 状态：部分实现；§2 有界结果收存和管理员策略设置已实现；30/30/90 天默认已获所有者确认，其余生命周期机制与完整恢复验收未完成
- 日期：2026-09-11

## 背景

任务 transport 已有 durable journal、immutable result outbox 和整批事务接收，ID 铸造机制见
ADR 0031。这些机制不能单独回答到期后是否还能执行、何时能删除执行证据以及备份回退后如何
处理迟到结果。不能用“随机 ID 几乎不会冲突”代替这三个契约。

## 建议的不变量

### 1. 到期是执行授权结束，不是远端结果

共享协议增加不可变 `not_after_ms`（PSP UTC 毫秒，最晚开始执行授权）并由结果回显；保留现有
`InputSHA256(kind, args)` 的定义，deadline 单独持久化并参与精确身份比较。新 kind 必须同时
协商 durable execution、expiry 和 kind capability；旧 Node 不能因忽略新字段而继续获派任务。
每个真实 kind 显式确定 TTL，框架没有“零即无限期”。幂等重放复用已有 deadline，不重新延期。

PSP 到期只关闭 dispatch，不凭 queued/offered 状态制造执行终态。旧备份中的 queued 行可能在
备份之后已被 offer 甚至成功执行；`OfferCount=0` 不能证明历史上从未下发。关闭的 unresolved
任务仍受资源上限约束，不能借到期释放为无界积压。匹配的真实终态可以迟到。

Node 在接收、原子 claim 和进入 handler 前检查授权。无新鲜控制面时间锚点时不启动 received
任务；正常 heartbeat、terminal 重放及已有代理 core 服务不因此停止。时间锚点应计入完整 HTTP
RTT，以控制面时间的保守上界判断可否开始，并用本进程包含系统休眠的连续 elapsed 推进、防止回退。
不能把“上界已到截止”当作“确实已过期”：只有新鲜上界严格小于 deadline 才允许开始，只有
下界已经达到 deadline 才能证明到期；deadline 落在上下界之间时保持待执行，等待新锚点。
无锚点、过旧、时钟异常或算术溢出均不启动新任务。标准 Go monotonic 在部分系统休眠时会暂停，
且不能跨进程持久化，因此不能单凭 `time.Since` 或把 monotonic 存入备份实现这道授权门。
[Go time 契约](https://pkg.go.dev/time#hdr-Monotonic_Clocks)。
handler 自己还须有最长执行时间、取消和 Recover 契约；开始授权不是完成 deadline，也不承诺
外部系统会在精确毫秒边界前发生副作用。

本地未回滚的 received journal 能证明尚未 claim，因此可原子产生 expired-before-start 失败及
outbox。terminal 只重放原结果；running 按 Recover 契约核对。过期且没有 journal 时只发 bounded
replay-fenced Issue，不生成新 terminal：旧成功记录可能已经清理，伪造 failed/indeterminate
仍会与控制面的原成功冲突。

### 2. 确认收到证据，不等于宣称任务成功

PSP 恢复可能丢失任务行或 offer 标记。身份完整的 unknown 结果、以及身份匹配却仍是 queued 的
结果，先进入有数量/字节上限的 durable quarantine；后者同时关闭该任务的 dispatch，等待
恢复核对，不削弱 never-offered 结果不能直接完成任务的规则。跨 agent、已知身份不符或已知
terminal 冲突仍不能被当作成功。

正式结果更新与 quarantine 在同一个整批事务中提交。只有每条证据均已持久化，才可返回整批
2xx；失败仍整批重放。Node 的 delivered 表示证据已交付，不代表 PSP 将隔离结果用于任务终态。
当前无需为此增加逐结果 ACK；如果未来允许部分接收/选择性重试，必须重新设计确认协议。
quarantine 满额必须 backpressure，不得为保持 heartbeat 返回成功而静默丢弃结果。

**已实现的收存边界**：独立表以 `(agent_id, task_id)` 为主键，保存完整 canonical wire result、
SHA-256、`unknown_task` / `never_offered` 原因与首次/最近接收时间。每 agent compiled hard cap
为 256 行 / 16 MiB canonical JSON；满额时整批回滚、HTTP 429，精确重放不占新容量。
旧式缺少 kind/input digest 的结果不能冒充完整证据；已知跨 agent 身份仍拒绝，不能利用隔离
绕过所有权。隔离 ID 只 fence 同 agent 的新建任务，不能由一个 agent 预占别人的未来 TaskID。
已有隔离证据的冲突 payload 不得覆盖，也不得因任务行后来恢复而绕过冲突检查。
恢复的匹配 queued/offered 行仅关闭 dispatch，不自动将隔离证据晋升为任务终态。
Offer 同时检查同 agent 的隔离 ID；即使恢复行丢失了 dispatch 关闭标记，也不依赖 Node 重新
送一次已确认的结果才停止下发。

queued 收到匹配结果时以独立字段关闭 dispatch，仍保留 unresolved 状态及 active quota。
有隔离证据时 agent 删除 fail closed；尚无人工核对 API/UI、自动晋升任务终态或证据清理器。
这里不扩大现有“无隔离证据、已收敛且无 active task”的 agent 删除行为，也不表示 §3 历史
保留机制已完成。整个 report 后续失败仍返回非 2xx，下一轮重放已提交的结果证据。

### 3. 保留期不能短于安全期限

初期不删除 task identity。清理器只能处理 terminal、证据已确认交付、没有 pending outbox，且
已经超过原始执行授权及保留期限的记录；不能清理 running、未交付结果或仅关闭 dispatch 的
unresolved 任务。优先压缩大 args/result，精确的完整结果重放承诺与 compact identity tombstone
分开说明。agent 删除也不能绕过受保护历史的保留约束。

到期 fence、持久时间高水位和恢复流程通过验收前，不开启最终身份删除；尤其不能在原任务
deadline 前删除 Node terminal，否则 PSP 旧备份复活仍有效任务后可能重复执行。

## 已确认的默认策略（管理员可调整）

所有者确认以下值作为第一版默认，且允许管理员修改。这是已选择的策略，不是生产测量得出的
SLA，也不表示尚未实现的 expiry、清理或恢复流程已经通过验收：

- 离线后自动结果对账支持窗口：30 天。
- 单边 PSP 数据库备份的建议最大恢复年龄：30 天；缺失任务或 offer 证据仍需隔离核对，不承诺
  无人介入地重建所有任务意图。
- 完整终态结果/输入的默认保留期：90 天，并且不得早于原任务授权截止时间及安全余量。
- compact identity tombstone 初期不做自动物理删除；最终删除需要独立的 replay fence 验收。

这里的离线窗口只约束任务证据的自动对账保障，不改变现有代理 core 的离线执法策略。超过
窗口不自动重执行旧任务，进入人工恢复核对。任务 TTL 由 probe/upgrade 各自契约定义，不使用
这 30/90 天作为任务执行 TTL。改变保留设置不能追溯缩短已有记录的安全期限。

**管理员设置已实现**：`/api/admin/settings/ui` 的以下三个全局 KV 字段默认 30/30/90，管理页
“原生任务生命周期策略”可编辑：

- `node_task_offline_reconcile_days`
- `node_task_backup_restore_days`
- `node_task_result_retention_days`

各字段为 1–3650 天整数（一天是连续 24 小时）；0 不代表无限执行或立即删除。完整结果保留期
不得短于另两个窗口，前后端均校验。旧客户端 PUT 省略新字段或传 `null` 时保留已配置值；显式
0、非法类型或不一致的整组策略拒绝且不写入。数据库缺失字段才填默认，不覆盖合法自定义值。
旧内部 writer 的整组全零视为省略，只为缺失键 insert-only 播种默认，不覆盖管理员设置
（包括存在性读取之后出现的新键）；部分缺失的非零策略不能写入。默认策略来自全局设置或
固定产品默认，不因 settings reader 的调用方 fallback 而变化。

新设置选择未来新任务的策略，不追溯缩短或声称延长已承诺的旧窗口。接入真实 producer 前，
必须将完整策略与不可变原始 deadline 快照持久化；清理器使用任务自身的保护截止时间，不能
读取当前设置来重新计算旧任务。缺少快照或 deadline 的 legacy 证据保持保留，不猜测可删除时间。
当前设置更新只写 settings，不改已有 task/quarantine，也未启用清理；身份 tombstone 暂不自动
物理删除。策略设置完成不能替代下面的上线验收。

## 恢复与上线判据

单边 PSP 恢复保留旧任务 ID/deadline，新任务使用 fresh issuer；较新的 Node journal 承担去重，
迟到结果走正常接收或 quarantine。Node journal 丢失/回退、双端回退、运行中内存快照恢复或
超窗口恢复，必须先 fence writers/workers 并执行 restore-finalize，不自动换新 ID 再做副作用。

至少覆盖：延迟响应跨过 deadline、时间倒退、重启无锚点、claim 崩溃窗口、expired/no-journal、
备份复活 queued/offered、控制面丢失 task/tombstone 后迟到结果、quarantine 满额、legacy journal、
三库并发 expiry/complete/delete、Node/双端回滚演练。通过之前不开放 RealityProbe 或升级入口。

## 考虑过但未采用的方案

- **PSP 到期直接失败**：把没有结果误当没有执行，备份回退后尤其错误。
- **只加 UTC 字段、不做 capability 或本地检查**：旧/延迟 Node 仍可能开始执行。
- **unknown 结果静默 200 或永久 500**：分别丢失证据或毒化全部后续 heartbeat。
- **按数量强删未到期历史**：可以重新开放旧任务的副作用窗口；容量不足应 backpressure。
- **任意 2xx 即业务成功**：transport receipt 与任务 outcome 是两个不同事实。
