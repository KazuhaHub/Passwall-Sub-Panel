# Passwall Node 远程升级

服务器菜单的“升级 Passwall Node”升级原生节点的 **Passwall Node 程序本体**；“选择内核”仍独立控制
Xray/sing-box。管理员选择已发布的精确版本并确认短暂连接中断，默认不自动追踪 latest/beta。

`POST /api/admin/servers/:id/upgrade-node-agent` 接收 `{version,expected_version}` 和必需的
`Idempotency-Key`；同键相同输入返回同一个原任务，不延长启动截止。更改同键输入返回冲突。
`GET /api/admin/servers/:id/node-agent-upgrades/:task_id` 只查询该服务器绑定 agent 的该任务，
不暴露原始任务参数、凭据或未经核验的结果。两接口仅管理员可用、响应禁止共享缓存。

使用现有 Passwall Node 主动同步的 durable task 通道，不使用 SSH、sync_tasks 或另一个公网节点接口。
启动授权默认十分钟；当轮报告必须同时声明 execution、expiry 与 `task.agent.upgrade.v1` 才下发。
任务创建前还会读取最近一次已验证的协议/能力快照；未知、不兼容或能力不全时不会创建任务。下发时再次
检查当轮报告，详细规则见 [ADR 0033](adr/0033-native-node-compatibility-and-upgrade-admission.md) 与
[机器兼容矩阵](compat/passwall-node-v4.json)。旧 beta2 没有升级能力，需先人工升级一次并保留身份和数据。

Linux/systemd 使用固定路径的独立 root helper。Docker 默认生成兼容 NAS 项目编辑器的单服务 Compose；
只有管理员在安装弹窗“高级”中显式开启远程升级时，才生成双服务 Compose：Agent 保持非 root 且不挂载
Docker socket，隔离 updater 无网络但独占 socket；升级以精确 digest 拉取目标镜像，
通过重启、重新同步与收敛检查后提交，失败自动恢复保留的旧容器。手动 Docker 配置、单容器旧安装以及其他
系统仍需宿主机/人工升级，不能为了方便把 socket 或特权交给 Agent。

Passwall Node 非 root daemon 接收请求；固定路径的独立 root systemd helper 自行下载并核验官方发行档。
只自动升级同数据库 schema/升级 contract 的版本。下载失败不停止旧进程；目标启动/重新同步/严格配置收敛
失败则恢复旧程序和版本元数据，不清空 config/data。升级会有连接中断；已持久化历史计数保持，
停机前未采样的流量仍有既有 core restart 计量窗口，不承诺逐字节无损。

界面显示排队、下发、截止关闭、失败、待人工检查、等待目标观察与核验成功。截止时间只关闭继续下发，
已经开始的任务仍可稍后返回真实结果，不能把截止自动伪装成失败或直接再次创建任务。
只有原任务具有严格匹配的重启/digest 收据，且新鲜认证上报的实际 Passwall Node 版本匹配目标，才显示成功。
本机 readiness 假设 daemon UID 可信，不是防已被攻陷节点的远程证明。

复用已有 queued+offered 硬配额与生命周期策略快照；随机新 TaskID 不是依赖可恢复数据库自增号。
未增加跨副本同类任务排他保证、通用 journal GC 或 DB/VM 恢复机制；Node 串行执行和预期旧版本 CAS
可拒绝已经改变的安装。终态 retention 设置不等于已实施自动清理，仍需普通备份与磁盘监控。

## 早于 4.0.1.3 的节点必须用安装脚本升级一次

版本地址在 **4.0.1.3** 从 `release/<version>` 改为 `v<version>`，而这套规则**在节点自己的二进制里**，
于是有一个绕不开的先有鸡还是先有蛋：

| 节点当前版本 | 它为目标版本拼出的下载地址 | 结果 |
| --- | --- | --- |
| ≤ 4.0.1.2 | `release/4.0.1.4`（旧规则） | 404。助手报 `official release download or verification failed`，面板显示“升级失败；保留了原发行版” |
| ≥ 4.0.1.3 | `v4.0.1.4`，失败时再试一次历史命名空间 | 正常 |

**不为新版本补 `release/<version>` 标签。** 历史命名空间是封闭的：那四个标签指向已发布的 commit，
不能移动，也不应该继续增长。所以换一次版本这件事只能由**新代码**代劳，而安装脚本正是那段新代码——
它由面板渲染，地址由面板从自己的发布目录读出并写进脚本，不经过节点自己的规则：

Servers → 该节点 → Install / Reinstall → 方法选 **linux** → 勾选**原地替换版本（升级）** → 在主机上执行。

它保留身份与状态，只替换发行版，失败自动回滚。**升级一次之后**该节点就回到上表第二行，此后可以正常
使用“升级 Passwall Node”。当机队里不再有 ≤ 4.0.1.2 的节点时，本节可以删除。
