# 架构决策记录（ADR）

本目录记录那些**后来者容易误判为冗余、从而错误移除**的设计决策——尤其是安全属性。

代码注释解释"这段代码在做什么"，ADR 解释"为什么必须这样，以及不这样会怎样"。当一个决策的理由不写下来就会丢失、且丢失后会导致有人善意地把它删掉时，它就应该有一条 ADR。

## 索引

| 编号 | 标题 | 状态 |
|---|---|---|
| [0023](0023-saml-assertion-replay-protection.md) | SAML 断言重放防护必须持久化 | 已接受（v3.9.2） |
| [0024](0024-credential-derivation-byte-equality.md) | 渲染侧派生的凭据必须与共享客户端存储的凭据逐字节相等 | 已接受（追认既有决策；实现自 v3.9.0） |
| [0024](0024-psp-native-node-backend.md) | 自研节点后端作为一个 PanelKind，而不是一套新架构 | 搁置（2026-09-08 所有者已决定自研；仍等上游协议调研这一前置作业） |
| [0025](0025-push-pull-decision-rule.md) | 推还是拉：决策规则，以及 PSP 今天为什么没有选择权 | 已接受（规则）；Q2a 的结论随 ADR 0024 解冻 |
| [0025](0025-resync-membership-phase-order.md) | `ResyncMembership` 的阶段顺序是强制性的 | 已接受 |
| [0026](0026-shutdown-drain-before-cancel.md) | 关停时必须先排空请求再取消后台上下文 | 已接受 |
| [0027](0027-sub-opaque-404.md) | 订阅令牌无效时返回不可区分的 404 | 已接受 |
| [0028](0028-local-login-enumeration-and-timing.md) | 本地登录不得泄露账号是否存在（含时序） | 已接受 |
| [0029](0029-xray-reality-client-compatibility.md) | Xray core 升级按客户端兼容矩阵放行，不跟随 latest | 已接受（2026-09-10） |
| [0030](0030-separate-quic-and-udp-controls.md) | QUIC 与普通 UDP 独立、无地区假设地控制 | 已接受（2026-09-11） |
| [0031](0031-native-task-id-incarnations.md) | 原生任务 ID 使用新启动 incarnation，不跟随数据库回退 | 已接受（ID 机制；生命周期闸尚未完成） |
| [0032](0032-native-task-lifecycle.md) | 原生任务的到期、结果证据与恢复边界 | 提案（支持窗口待确认） |
| [0033](0033-native-node-compatibility-and-upgrade-admission.md) | Passwall Node 兼容信息与远程升级准入 | 已接受（2026-09-16） |
| [0034](0034-sync-status-contract.md) | 前端需要「同步状态」时，后端应给出什么 | 已接受（2026-09-18；选择资源任务状态，operation 与上游配置验证延后；实现待完成） |
| [0035](0035-backend-compatibility-and-release-evidence.md) | 后端兼容承诺与发布证据 | 提案（规范基线已整理，实施门禁待完成） |
| [0036](0036-saml-pre-migration-hardening.md) | SAML 加固线：先收紧本地校验与请求绑定，再考虑替换协议实现 | 已接受（2026-09-18；替代 ADR 0023 第 3、4 项；H1–H3 已实现，协议替换候选经实验否决，见 `docs/authcore-migration-measurement.md` §6） |
| [0037](0037-authcore-shareability-experiment.md) | authcore 共享收益实验的测量口径与退出判据 | 已接受（2026-09-18；判据在结果出现前冻结；三包结论见测量文档 §6、§8） |
| [0038](0038-refused-node-report-is-recorded.md) | 被拒绝的节点上报是一个要记录、要判定、要显示的事实 | 已接受（2026-09-22） |

> 编号从 0023 起始：更早的设计决策分散记录在 [ARCHITECTURE.md](../ARCHITECTURE.md)、各专题文档（如 [3xui-compat.md](../3xui-compat.md)、[panel-adapters.md](../panel-adapters.md)）以及提交历史中，尚未回溯整理为 ADR。新增决策请沿用此处的递增编号。

## 格式

每条 ADR 包含：**背景**（问题与约束）、**决策**（做了什么，以及每个关键约束的理由）、**后果**（代价、运维影响、以及未来修改时不能破坏的不变量）、**考虑过但未采用的方案**（含未采用的理由）。

最后一节尤其重要：它是防止同一个方案被反复重新提出的记录。
