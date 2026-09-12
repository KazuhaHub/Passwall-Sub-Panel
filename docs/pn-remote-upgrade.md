# PN 远程升级

服务器菜单的“升级 PN 程序”升级原生节点的 **PN 程序本体**；“选择内核”仍独立控制 Xray/sing-box。
管理员选择已发布的精确 PN 版本并确认短暂连接中断，默认不自动追踪 latest/beta。

`POST /api/admin/servers/:id/upgrade-node-agent` 接收 `{version,expected_version}` 和必需的
`Idempotency-Key`；同键相同输入返回同一个原任务，不延长启动截止。更改同键输入返回冲突。
`GET /api/admin/servers/:id/node-agent-upgrades/:task_id` 只查询该服务器绑定 agent 的该任务，
不暴露原始任务参数、凭据或未经核验的结果。两接口仅管理员可用、响应禁止共享缓存。

使用现有 PN 主动同步的 durable task 通道，不使用 SSH、sync_tasks 或另一个公网节点接口。
启动授权默认十分钟；当轮报告必须同时声明 execution、expiry 与 `task.agent.upgrade.v1` 才下发。
旧 beta2 没有升级能力：首个支持版本发布后，需一次人工升级/启用 Linux/systemd 助手，保留身份和数据。
Docker 和其他系统使用宿主机/手动升级，不给容器添加特权。

PN 非 root daemon 接收请求；固定路径的独立 root systemd helper 自行下载并核验官方发行档。
只自动升级同数据库 schema/升级 contract 的版本。下载失败不停止旧进程；目标启动/重新同步/严格配置收敛
失败则恢复旧程序和版本元数据，不清空 config/data。升级会有连接中断；已持久化历史计数保持，
停机前未采样的流量仍有既有 core restart 计量窗口，不承诺逐字节无损。

界面显示排队、下发、截止关闭、失败、待人工检查、等待目标观察与核验成功。截止时间只关闭继续下发，
已经开始的任务仍可稍后返回真实结果，不能把截止自动伪装成失败或直接再次创建任务。
只有原任务具有严格匹配的重启/digest 收据，且新鲜认证上报的实际 PN 版本匹配目标，才显示成功。
本机 readiness 假设 daemon UID 可信，不是防已被攻陷节点的远程证明。

复用已有 queued+offered 硬配额与生命周期策略快照；随机新 TaskID 不是依赖可恢复数据库自增号。
未增加跨副本同类任务排他保证、通用 journal GC 或 DB/VM 恢复机制；Node 串行执行和预期旧版本 CAS
可拒绝已经改变的安装。终态 retention 设置不等于已实施自动清理，仍需普通备份与磁盘监控。
