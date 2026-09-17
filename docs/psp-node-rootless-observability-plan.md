# PSP Node 无特权主机可观测性与运维能力实施规格

- **状态**：设计规格，尚未实现
- **冻结日期**：2026-09-17
- **覆盖仓库**：Passwall-Node、Passwall-Sub-Panel
- **读者**：后续实现者、评审者、测试与发布负责人
- **相关设计**：[psp-node-agent.md](psp-node-agent.md)、[psp-node-plan.md](psp-node-plan.md)、
  [ADR 0024](adr/0024-psp-native-node-backend.md)、[ADR 0025](adr/0025-push-pull-decision-rule.md)、
  [ADR 0031](adr/0031-native-task-id-incarnations.md)、[ADR 0032](adr/0032-native-task-lifecycle.md)

> 这不是愿望清单。本文给出线上协议、字段语义、采集来源、持久化、计算公式、API、UI、
> 告警、测试、发布顺序和验收条件。接手者可以拆任务和写代码，但不得在实现里悄悄改变
> 本文已经冻结的边界；需要改变时，先修改本文并记录理由。

## 0. 一句话与硬边界

**把 PSP Node 扩展成一个无特权的节点观测与诊断 Agent，但不把它变成宿主机管理器。**

本项目新增的全部能力必须同时满足：

1. Passwall-Node 主进程继续使用当前非 root 用户运行。
2. 不新增 root helper。
3. 不新增 CAP_NET_ADMIN、CAP_SYS_ADMIN、CAP_SYS_MODULE、CAP_NET_RAW 等 Linux capability。
4. 不执行控制面传来的任意命令、脚本、路径或环境变量。
5. 不写 sysctl、qdisc、防火墙、systemd、包管理器或宿主机配置文件。
6. 不因为遥测采集或持久化失败而阻断 config、roster、directives、task receipt 或配额执法。
7. 所有指标都必须明确作用域；容器指标不得伪装成宿主机指标。
8. 读不到的值必须是缺失或 unavailable，绝不能用 0 代替。

现有远程 Agent 升级所使用的隔离特权 helper 不属于本项目。本项目既不删除它，也不依赖它，
更不借它扩展宿主机管理能力。关闭远程升级 helper 后，本文全部功能仍须正常工作。

## 1. 已冻结的产品决定

| 问题 | 决定 |
|---|---|
| BBR | 只读识别系统默认算法、可用算法和默认 qdisc；不配置、不持久化、不自动优化 |
| 总限速 | 不做强制执行；只做实时吞吐、峰值、持续高占用和周期总量告警 |
| root 权限 | 新功能完全不需要 |
| 首个完整采集平台 | Linux，包括 systemd、Docker host-network 和手工运行 |
| macOS / Windows | 协议与六平台编译保持兼容；第一阶段不注册 collector、不声明 capability，后续补齐 |
| 指标真相源 | Node 报累计计数器和当前 gauge；Panel 负责差分、历史、聚合和告警 |
| 默认上报周期 | 60 秒，由 Panel 在同步信封中下发；0 表示每轮都报 |
| 原始历史 | 默认 7 天，分钟级 |
| 小时汇总 | 默认 90 天 |
| UI 权限 | 仅管理员；不进入普通用户 API |
| 自动修复 | 第一阶段没有；只观测、告警、诊断 |
| 外部探测 | 第一阶段只测 Node 到 PSP 的既有同步链路；不实现原始 ICMP |

## 2. 为什么这样切边界

当前 Passwall-Node 的安全价值来自三个事实：

- Node 主动拨向 PSP，没有入站管理端口。
- 网络 Agent 非 root，只拥有运行代理 Core 所需的最小权限。
- PSP 下发的是类型化期望状态和 capability-gated task，不是 Shell。

BBR 修改、tc 限速、eBPF、宿主机防火墙和系统服务控制都会扩大信任边界。即使把它们放进
另一个 helper，也会产生安装、授权、回滚、Docker 宿主机穿透和供应链风险。它们不是可观测性
的必要条件，因此全部排除。

无特权 Agent 仍然可以完成大部分真正有运营价值的工作：

- 判断机器是否过载；
- 判断 Core 是否在消耗异常资源；
- 判断磁盘、FD、conntrack、内存是否接近故障；
- 看到网络吞吐、丢包、错误和 TCP 重传；
- 看到同步链路是否变慢或连续失败；
- 识别 BBR 和基础内核环境；
- 生成可交给运维人员的脱敏诊断；
- 对 Agent 自己拥有的 Core 做有限、类型化的运行操作。

## 3. 范围总表

### 3.1 第一阶段必须实现

| 能力 | Node | Panel | UI |
|---|---|---|---|
| 平台与作用域 | OS、架构、内核、部署形态、cgroup | 保存最新快照 | 环境卡片 |
| CPU / load | 系统累计 CPU 计数、cgroup CPU/限额/节流、核心数、load 1/5/15 | 差分与汇总 | 当前值和趋势 |
| 内存 / swap | 系统与 cgroup gauge | 保存与阈值判断 | 当前值和趋势 |
| 数据目录文件系统 | 容量、可用、inode | 保存、最低值汇总 | 容量卡片和告警 |
| 网络接口 | 累计字节、包、错误、丢包、MTU、状态 | 计算速率 | 主接口趋势和接口表 |
| TCP / socket | 连接、累计 segment/retrans、socket 概况 | 计算重传率 | 网络健康卡片 |
| conntrack | 当前值和上限，若可读 | 计算占用率 | 可用时展示 |
| Agent / Core 进程 | CPU 累计、RSS、FD、线程、启动时间 | 计算速率和重启 | 进程卡片 |
| 同步健康 | 最近成功、连续失败、RTT、请求响应大小 | 最新状态、告警 | 同步状态卡片 |
| BBR 只读 | 系统默认/可用 congestion control、默认 qdisc | 保存 | 网络调优只读卡片 |
| 历史与汇总 | 无 | 分钟历史、小时 rollup、清理 | 1h/24h/7d/30d/90d |
| 告警 | 上报事实 | 规则、迟滞和恢复 | 列表 badge 与详情 |
| 本地诊断 | passwall-node doctor [--json] | 无 | 无 |

### 3.2 第二阶段应实现

- 管理端按需刷新下一份主机报告；
- 脱敏远程诊断任务 diagnostics.collect.v1；
- Core 重启任务 core.restart.v1；
- Agent 自有事件环形缓冲区；
- SQLite quick_check、Core 二进制与已确认配置摘要核验；
- Linux PSI（CPU、memory、IO pressure）；
- data-dir 所在块设备的 IO bytes/ops/busy time，无法可靠映射时 unavailable；
- softnet drop、TCP listen overflow/drop 等更深网络内核计数；
- 节点维护模式和 drain 的独立设计；
- 节点流量周期预算告警，但仍不做强制限速；
- 结合节点流量与资源指标的容量趋势和迁移建议；
- macOS、Windows 的原生 collector。

### 3.3 明确不做

- 任意 Shell、脚本、SSH 或命令执行；
- BBR、sysctl、qdisc、tc、eBPF 的写入；
- 宿主机防火墙、路由、DNS 或网卡配置；
- 操作系统更新、软件包安装、reboot、shutdown；
- 读取其他进程的环境、命令行、内存或文件；
- 上报凭据、完整 Core 配置、客户端列表、客户端 IP 明细；
- ICMP raw socket 探测；
- 自动迁移用户、自动停用节点或自动修改订阅；
- 根据单个瞬时样本触发告警；
- 把缺失指标当成 0 或健康。

## 4. 术语与数据模型

### 4.1 四种值

本文把上报值分成四类，接手者不能混用：

| 类型 | 示例 | 线上语义 |
|---|---|---|
| 累计计数器 counter | CPU total、网卡 tx_bytes、TCP retrans | 只做同一 epoch 内的差分 |
| 当前值 gauge | MemAvailable、load1、FD 数、conntrack count | 可直接展示和聚合 |
| 身份 identity | boot_id、进程 started_at、sample_id | 判断计数器能否连续 |
| 能力 / 可用性 | capability、unavailable | 判断缺失是未实现还是暂不可读 |

### 4.2 三个时间

- collected_at_ms：Node 完成该样本采集时的墙钟时间；
- received_at：PSP 完成认证并收到报告的时间，PSP 自己写入；
- uptime_ms：Node 观察到的系统单调 uptime。

图表的横轴使用 received_at，详情同时显示 collected_at_ms。任何告警窗口以 PSP 的
received_at 为准，避免节点墙钟漂移改变告警时长。时钟漂移为：

~~~
clock_skew_ms = collected_at_ms - received_at_ms
~~~

### 4.3 作用域

每个 HostObservation 必须声明：

- deployment：systemd、docker、manual、unknown；
- resource_scope：host、container、mixed、unknown；
- cgroup_version：0、1 或 2；
- data_filesystem_scope：host_mount、container_mount、unknown。

resource_scope 的定义：

- host：CPU、内存和网络明确代表宿主机；
- container：代表容器限制或容器命名空间；
- mixed：例如 CPU 来自宿主机 proc，但内存同时有宿主机和 cgroup 两组；
- unknown：collector 无法证明。

UI 必须把 scope 展示出来。mixed 不能被简化成 host。

## 5. 线上协议扩展

协议类型的唯一真相源仍在 Passwall-Node 的 protocol 包；PSP 只通过 Go module 引用，不复制类型。
本次是 additive v1 扩展，不提高 ProtocolVersion1，也不新增同步端点。

本节的 Go 代码块是带 JSON 名的字段规格伪代码，不是第二份可编译类型定义。实现时以
Passwall-Node protocol 包中的真实 Go struct 和 struct tag 为唯一真相源；本文负责冻结字段名和语义。

### 5.1 Capability

新增：

~~~
host.telemetry.v1
~~~

只有实际注册 HostCollector 的构建才声明该 capability。声明 capability 不表示每一小项都可读；
字段级缺失由 optional section 和 unavailable 表达。
第一阶段只有 Linux production composition 注册；Darwin/Windows stub 只为保持编译，不得因
“有个空 collector”而广告能力。

未来任务分别使用现有规则自动生成：

~~~
task.diagnostics.collect.v1
task.core.restart.v1
~~~

不得把 host.telemetry.v1 加进 AgentUpgradeCapabilities。缺少遥测不能让一个原本兼容的 Node
变成协议不兼容，也不能影响 Agent 升级资格。

### 5.2 Envelope 调度字段

在 protocol.Envelope 添加：

~~~go
HostReportSeconds int  json:"host_report_seconds"
WantHostReport    bool json:"want_host_report"
~~~

语义：

- HostReportSeconds 为 0：每个报告都带 HostObservation，采用 fail-safe over-report；
- 正数：距离上次实际发送 HostObservation 达到该秒数后的第一轮携带；
- 进程启动后的第一份报告总是携带 HostObservation；
- 合法正数范围：5 到 3600 秒；
- WantHostReport：下一轮强制携带一次，不改变长期周期；
- 上述周期和 full_report_seconds 独立；
- HostObservation 可以出现在 partial 或 full report；
- PSP 默认下发 60 秒；
- 有管理员按需刷新时，PSP 对该 agent 的下一份 Envelope 设置 WantHostReport。

在共享 protocol 包增加 ShouldSendHost，PSP 和 Node 都调用同一实现，不复制判断。

### 5.3 NodeReport 字段

在 protocol.NodeReport 添加：

~~~go
Host *HostObservation json:"host,omitempty"
~~~

NodeReport.MarshalJSON 的 partial 专用结构必须显式携带 Host。遗漏这一处会造成全量报告有指标、
轻量报告静默丢指标，是必须有测试守住的回归点。

Host 为空有三种合法情况：

1. Node 没有 host.telemetry.v1；
2. 本轮尚未到 HostReportSeconds；
3. collector 整体失败，Node 通过稳定 Issue 上报失败。

PSP 不得把一次 Host 为空写成所有指标为零，也不得删除最后一个有效快照。

整体失败的稳定 code 为 host_telemetry_failed。连续失败 episode 只产生一个 durable Issue；
中间至少一次成功后再次整体失败才开启新 episode。单个 optional section 不可读只进入 unavailable，
不产生 Issue，避免容器中永久缺失的 conntrack 每分钟制造噪声。

### 5.4 HostObservation v1 结构

以下是规范形状。JSON 名、类型、可空性和语义已冻结；Go 标识符按本仓库命名惯例
实现，但公共 protocol package 必须是唯一类型真相源。

~~~go
type HostObservation struct {
    SampleID       string                 json:"sample_id"
    CollectedAtMS  int64                  json:"collected_at_ms"
    UptimeMS       int64                  json:"uptime_ms"
    BootID         string                 json:"boot_id,omitempty"
    Scope          HostScope              json:"scope"
    Platform       PlatformObservation    json:"platform"
    CPU            *CPUObservation        json:"cpu,omitempty"
    Load           *LoadObservation       json:"load,omitempty"
    Memory         *MemoryObservation     json:"memory,omitempty"
    Filesystem     *FilesystemObservation json:"filesystem,omitempty"
    Network        *NetworkObservation    json:"network,omitempty"
    TCP            *TCPObservation        json:"tcp,omitempty"
    Sockets        *SocketObservation     json:"sockets,omitempty"
    Processes      *ProcessObservation    json:"processes,omitempty"
    Runtime        *RuntimeObservation    json:"runtime,omitempty"
    Tuning         *TuningObservation     json:"tuning,omitempty"
    Unavailable    []string               json:"unavailable,omitempty"
}

type HostScope struct {
    Deployment          string json:"deployment"
    ResourceScope       string json:"resource_scope"
    CgroupVersion       int    json:"cgroup_version"
    DataFilesystemScope string json:"data_filesystem_scope"
}

type PlatformObservation struct {
    OS             string json:"os"
    Arch           string json:"arch"
    KernelRelease  string json:"kernel_release,omitempty"
    DistributionID string json:"distribution_id,omitempty"
    VersionID      string json:"version_id,omitempty"
    LogicalCPUs    int    json:"logical_cpus"
}
~~~

SampleID 是 16 个随机字节的小写十六进制，共 32 字符。每次真正采集生成一个新值；同一份
BuiltReport 重发时保持不变。Panel 以 agent_id + sample_id 幂等。

BootID 在 Linux 读取 proc boot_id；其他平台无法提供时可省略。BootID 变化、累计计数下降或
进程 started_at 变化都意味着对应 counter 断点，本次不计算速率。

### 5.5 CPU

~~~go
type CPUObservation struct {
    System       *SystemCPUObservation json:"system,omitempty"
    Cgroup       *CgroupCPUObservation json:"cgroup,omitempty"
}

type SystemCPUObservation struct {
    CounterEpoch string json:"counter_epoch"
    Total        uint64 json:"total"
    Idle         uint64 json:"idle"
    IOWait       uint64 json:"iowait"
    Steal        uint64 json:"steal"
}

type CgroupCPUObservation struct {
    CounterEpoch  string  json:"counter_epoch,omitempty"
    UsageUS       *uint64 json:"usage_us,omitempty"
    UserUS        *uint64 json:"user_us,omitempty"
    SystemUS      *uint64 json:"system_us,omitempty"
    NrPeriods     *uint64 json:"nr_periods,omitempty"
    NrThrottled   *uint64 json:"nr_throttled,omitempty"
    ThrottledUS   *uint64 json:"throttled_us,omitempty"
    QuotaUS       *uint64 json:"quota_us,omitempty"
    PeriodUS      *uint64 json:"period_us,omitempty"
    EffectiveCPUs *uint64 json:"effective_cpus,omitempty"
}

type LoadObservation struct {
    Load1        float64 json:"load_1"
    Load5        float64 json:"load_5"
    Load15       float64 json:"load_15"
}
~~~

CPUObservation 必须至少有 System 或 Cgroup 之一，允许 proc stat 不可读但 cgroup 仍可读。
System 的 Total、Idle、IOWait、Steal 是同一平台内的累计调度单位。Linux Total 等于
user + nice + system + idle + iowait + irq + softirq + steal；guest/guest_nice 已包在 user/nice 内，
不得再加一次。Idle 只是 idle，不包含单独上报的 iowait。单位故意不写成秒或 jiffy：
Panel 只使用相邻样本的比值，绝不展示或跨平台比较绝对值。

System CounterEpoch 首选 BootID；没有 BootID 时由 collector 为当前进程生命周期生成
随机 epoch。

Cgroup CPU 在 v2 读 cpu.stat/cpu.max/cpuset.cpus.effective，v1 读对应 cpuacct/cpu/cpuset
controller。QuotaUS 与 PeriodUS 必须同时存在；cpu.max 为 max 时两者都为 nil。
NrPeriods、NrThrottled、ThrottledUS 必须三者全有或全无。EffectiveCPUs 为解析后的
可用 CPU 数，不上报 cpuset 文本。cgroup epoch 由 BootID 与 cgroup 路径身份的本地
摘要构成；摘要不上报原始路径。

### 5.6 内存与 cgroup

~~~go
type MemoryObservation struct {
    System               *SystemMemoryObservation json:"system,omitempty"
    Cgroup               *CgroupMemoryObservation json:"cgroup,omitempty"
}

type SystemMemoryObservation struct {
    TotalBytes     uint64  json:"total_bytes"
    AvailableBytes *uint64 json:"available_bytes,omitempty"
    SwapTotalBytes uint64 json:"swap_total_bytes"
    SwapFreeBytes  uint64 json:"swap_free_bytes"
}

type CgroupMemoryObservation struct {
    CounterEpoch    string  json:"counter_epoch"
    CurrentBytes    uint64  json:"current_bytes"
    LimitBytes      *uint64 json:"limit_bytes,omitempty"
    SwapCurrentBytes *uint64 json:"swap_current_bytes,omitempty"
    SwapLimitBytes   *uint64 json:"swap_limit_bytes,omitempty"
    OOMEvents       *uint64 json:"oom_events,omitempty"
    OOMKillEvents   *uint64 json:"oom_kill_events,omitempty"
}
~~~

LimitBytes 为 nil 表示 unlimited 或不可知；不能把 unlimited 编码成 0。
MemAvailable 缺失时 AvailableBytes 为 nil 并加入 memory.system.available；v1 不估算，
避免把不同内核版本的近似公式变成线上协议。SwapTotalBytes 为 0 是合法的“未配置 swap”。
Cgroup CurrentBytes 可因采样竞态短暂大于 LimitBytes，validator 不因此拒绝整份报告；
UI 保留原值并可将比例 clamp 用于画图。
OOMEvents/OOMKillEvents 只在内核提供真实 counter 时成对上报；cgroup v1 的 memory.failcnt
不等于 OOM，不得冒充，两字段为 nil 并加入 memory.cgroup.oom。

### 5.7 数据目录文件系统

~~~go
type FilesystemObservation struct {
    TotalBytes      uint64  json:"total_bytes"
    AvailableBytes  uint64  json:"available_bytes"
    TotalInodes     *uint64 json:"total_inodes,omitempty"
    AvailableInodes *uint64 json:"available_inodes,omitempty"
    ReadOnly        bool    json:"read_only"
}
~~~

只对 Node 的 data-dir 所在文件系统做 statfs；不上报真实挂载路径、设备名或宿主机其他目录。
AvailableBytes 使用非 root 进程实际可用的 bavail，不使用包含 root 保留块的 bfree。
inode 两字段必须同时存在或同时为 nil；文件系统不报告有意义的 inode 时不得用 0
伪装，并加入 filesystem.inodes。

### 5.8 网络接口

~~~go
type NetworkObservation struct {
    CounterEpoch        string                        json:"counter_epoch"
    DefaultIPv4Interface string                       json:"default_ipv4_interface,omitempty"
    DefaultIPv6Interface string                       json:"default_ipv6_interface,omitempty"
    Interfaces          []NetworkInterfaceObservation json:"interfaces"
}

type NetworkInterfaceObservation struct {
    Name       string  json:"name"
    Index      int     json:"index"
    MTU        int     json:"mtu"
    Up         bool    json:"up"
    OperationalState string json:"operational_state,omitempty"
    LinkSpeedMbps *uint64 json:"link_speed_mbps,omitempty"
    RXBytes    uint64  json:"rx_bytes"
    RXPackets  uint64  json:"rx_packets"
    RXErrors   uint64  json:"rx_errors"
    RXDropped  uint64  json:"rx_dropped"
    TXBytes    uint64  json:"tx_bytes"
    TXPackets  uint64  json:"tx_packets"
    TXErrors   uint64  json:"tx_errors"
    TXDropped  uint64  json:"tx_dropped"
}
~~~

规则：

- 最多 32 个接口；
- 排除 loopback；
- 按 ifindex 升序；
- 同一报告内 name 和 index 都必须唯一；
- 不上报 MAC、IP 地址、路由表内容；
- 默认接口只报告接口名；
- LinkSpeedMbps 读不到时为 nil，不能写 0；
- Up 只表示内核 IFF_UP 管理状态；OperationalState 是 sysfs operstate 的受限枚举，不得混成一个字段；
- Docker host-network 仍必须按 Scope 标为 mixed 或 container，不能仅凭看到宿主机接口就声称 host。
接口计数器身份是 counter_epoch + index + name。任一项改变都形成断点，不计算跨断点速率。

默认接口选择不依赖外部 ip 命令。IPv4 只考虑 destination/mask 都为 0 且 route flag
包含 UP 的条目；IPv6 只考虑目标和 prefix 都为 0 且启用的条目。每个 family 选
最小 numeric metric；最小 metric 指向多个不同接口时视为歧义，该 family 默认接口
为空并加入 network.default_route。多条最小 route 若都指向同一接口，仍是唯一结果。

### 5.9 TCP、socket 与 conntrack

~~~go
type TCPObservation struct {
    CounterEpoch string json:"counter_epoch"
    ActiveOpens  uint64 json:"active_opens"
    PassiveOpens uint64 json:"passive_opens"
    AttemptFails uint64 json:"attempt_fails"
    EstabResets  uint64 json:"estab_resets"
    InSegments   uint64 json:"in_segments"
    OutSegments  uint64 json:"out_segments"
    RetransSegments uint64 json:"retrans_segments"
    CurrentEstablished uint64 json:"current_established"
}

type SocketObservation struct {
    TCPInUse     uint64  json:"tcp_in_use"
    TCPOrphan    uint64  json:"tcp_orphan"
    TCPTimeWait  uint64  json:"tcp_time_wait"
    UDPInUse     uint64  json:"udp_in_use"
    ConntrackCurrent *uint64 json:"conntrack_current,omitempty"
    ConntrackLimit   *uint64 json:"conntrack_limit,omitempty"
}
~~~

TCP cumulative fields 来自 proc net snmp；socket gauge 来自 proc net sockstat。conntrack 文件缺失或
无权读取时两个字段均 nil，并加入 unavailable。只出现一个 conntrack 字段视为非法报告。

### 5.10 Agent 与 Core 进程

~~~go
type ProcessObservation struct {
    Agent ProcessMetrics  json:"agent"
    Core  *ProcessMetrics json:"core,omitempty"
}

type ProcessMetrics struct {
    StartedAtMS           int64   json:"started_at_ms"
    CounterEpoch          string  json:"counter_epoch"
    CPUTime               *uint64 json:"cpu_time,omitempty"
    CPUTimeUnitsPerSecond *uint64 json:"cpu_time_units_per_second,omitempty"
    RSSBytes              uint64  json:"rss_bytes"
    OpenFDs               uint64  json:"open_fds"
    FDLimit               *uint64 json:"fd_limit,omitempty"
    Threads               uint64  json:"threads"
}
~~~

CPUTime 也是只做相邻样本差分的累计单位；CPUTime 与 CPUTimeUnitsPerSecond 必须同时存在或同时
为 nil。Linux 从固定的 proc self auxv AT_CLKTCK 读取每秒单位，不调用 getconf；读取失败时仍可
报告 RSS、FD 和 thread，但两项 CPU 字段为 nil。Agent 只能读取自己与自己启动的 Core，不扫描
其他进程。Core 不在运行时 Core 为 nil，运行状态仍以 NodeReport.CoreState 为准。
CounterEpoch 必须在同一进程实例中稳定，Linux 使用 BootID + proc starttime tick 的本地摘要；
不得每个样本生成随机值。StartedAtMS 由系统 boot time 与 proc starttime 推导，不使用
文件 mtime。

Core 采集必须由 Supervisor 返回受控的子进程 handle 与预期 starttime，collector 打开对应
proc 文件后再次比对 starttime。不相同时当作 PID reuse，丢弃 Core section；禁止只传一个
无身份的任意 PID 给 collector。

不得上报 PID、命令行、环境变量、打开文件路径或内存内容。

### 5.11 Agent 运行与同步

~~~go
type RuntimeObservation struct {
    AgentStartedAtMS       int64  json:"agent_started_at_ms"
    CoreRestartCount       uint64 json:"core_restart_count"
    LastSyncSuccessAtMS    int64  json:"last_sync_success_at_ms,omitempty"
    LastSyncFailureAtMS    int64  json:"last_sync_failure_at_ms,omitempty"
    ConsecutiveSyncFailures uint64 json:"consecutive_sync_failures"
    SyncSuccessCount       uint64 json:"sync_success_count"
    SyncFailureCount       uint64 json:"sync_failure_count"
    LastRoundTripMS        *uint64 json:"last_round_trip_ms,omitempty"
    LastRequestBytes       *uint64 json:"last_request_bytes,omitempty"
    LastResponseBytes      *uint64 json:"last_response_bytes,omitempty"
    CollectorDurationMS    uint64 json:"collector_duration_ms"
}
~~~

这些字段描述上一轮及更早的同步，因为当前报告尚未完成。发送成功后才推进 success 计数。
HTTP 连接、认证、编码、响应校验中的失败都计入 failure。任务 handler 失败不计为同步失败。

### 5.12 BBR 与只读网络调优状态

~~~go
type TuningObservation struct {
    TCPAvailableCongestionControls []string json:"tcp_available_congestion_controls,omitempty"
    TCPDefaultCongestionControl     string   json:"tcp_default_congestion_control,omitempty"
    DefaultQdisc                    string   json:"default_qdisc,omitempty"
}
~~~

字段来源只允许读取固定 proc sys 路径或无特权 netlink 查询。禁止调用 sysctl 或 tc 外部命令。
算法列表按字典序去重，名称必须是小写 token。

UI 从 available 列表是否包含 bbr 推导 BBR 可用性。Node 和 Panel 都不生成“建议自动开启”的动作。
TCPDefaultCongestionControl 只是新建 TCP socket 的系统默认值，不证明 Core 已建立连接
正在使用该算法。UI 不得把“available 含 bbr”显示为“BBR 已启用”。

### 5.13 unavailable

unavailable 按字典序去重，最多 32 项。第一版定义以下稳定 token：

~~~
cpu.system
cpu.cgroup
load
memory.system
memory.cgroup
memory.system.available
memory.cgroup.oom
filesystem.data
filesystem.inodes
network.interfaces
network.default_route
network.link_speed
tcp
sockets
conntrack
process.agent
process.core
runtime.sync
tuning.tcp_congestion_control
tuning.default_qdisc
platform.kernel
platform.distribution
~~~

它不携带原始错误字符串。诊断详情进入本地日志或 diagnostics.collect.v1 的有界、脱敏检查结果。
为保持 additive v1 兼容，validator 接受符合 protocol validToken 且不超过 64 字节的未知 token；
旧 Panel 必须保留但可以只显示为“其他指标不可用”，不能因为新 Node 增加一个 section 就拒绝同步。
已发布 token 不得改名或改变含义。

粒度规则：CoreState 明确为 stopped 时 Core=nil 是业务状态，不加 process.core；
只有 Core 应在运行却无法读取时才加。network.link_speed 只在存在唯一默认接口但
其 speed 不可读时加，不因为某个非默认 veth 没有 speed 而永久标记整机。

## 6. 协议校验与资源上限

protocol 包暴露 ValidateNodeReportBase、ValidateHostObservation 和 ValidateNodeReport；最后一个先
调用 Base，Host 非 nil 时再调用 Host validator。Node 发送前使用完整 ValidateNodeReport；
Panel 为了隔离非关键遥测，先验证 Base，再单独验证 Host。Host 的最低校验：

1. sample_id 是恰好 32 字符的小写十六进制；
2. collected_at_ms > 0，uptime_ms >= 0；
3. scope 枚举合法；
4. OS 与 arch 非空，logical_cpus 在 1..4096；
5. 所有 float 非 NaN、非 Inf 且非负；
6. 所有 uint64 在 MaxInt64 范围内，保证三种数据库 BIGINT 可存；
7. available 不大于 total；
8. CPU 至少有 System/Cgroup 之一；System idle + iowait 不得大于 total，collector
   必须从同一次 proc stat 读取全部字段；
9. 接口最多 32，name 最长 64 字节、合法 UTF-8，index/MTU 为正；
10. available congestion controls 最多 32 个，每个最长 32 字节；
11. unavailable 每项是 canonical validToken、最长 64 字节，且无重复；未知 token 合法；
12. Process CPUTime 与 CPUTimeUnitsPerSecond 必须成对，后者必须大于 0；
13. Memory 至少有 System 或 Cgroup 之一；System available 若有则不得超过 total，
    swap free 不得超过 swap total；Cgroup current 可因竞态短暂超过 limit；
14. Host 编码后最大 128 KiB；
15. partial report 可以携带 Host，但仍不得携带完整对象枚举；
16. 没有 host.telemetry.v1 capability 却携带 Host，视为非法；
17. 声明 capability 但本轮不携带 Host 是合法的。

另外，Cgroup CPU 至少有 usage、quota/period、effective CPUs 或 throttle 一组事实；
quota/period 必须成对且大于 0；throttle 三个计数必须全有或全无；
usage 或 throttle 存在时 CounterEpoch 必须非空；
NrThrottled 不得大于 NrPeriods。filesystem inode 两字段必须成对，available 不得大于
total。Cgroup OOM 两字段必须成对。网络接口 index/name 都不得重复，
OperationalState 只能是 up、down、dormant、lowerlayerdown、unknown、notpresent 或 testing。
所有 epoch 最长 128 字节，BootID 最长 64，OS/arch/distribution ID 最长 32，kernel release/
version ID 最长 128，且全部必须是合法 UTF-8。MTU 上限 1,048,576，link speed 必须
在 1..100,000,000 Mbps。结构中每个非零 Unix millisecond 时间都必须在
1..253402300799999（9999-12-31T23:59:59.999Z）；0 只能用于 optional “尚无”字段。

验证顺序先做固定上限，再分配 map 或排序，避免不可信 JSON 造成放大。

## 7. Node 侧采集设计

### 7.1 包边界

Passwall-Node 新增：

~~~
internal/host/
  collector.go
  collector_linux.go
  collector_darwin.go
  collector_windows.go
  procfs_linux.go
  cgroup_linux.go
  network_linux.go
  process_linux.go
  collector_test.go

internal/agent/
  host_reporter.go
  runtime_stats.go
~~~

host 包只负责采事实，不知道 HTTP、PSP、任务或数据库。agent.HostReporter 负责周期、缓存、
sample_id 与 collector 失败 episode；capability 由 composition 根据是否注册 HostReporter 加入。

接口定义为：

~~~go
type Collector interface {
    Collect(context.Context) (protocol.HostObservation, error)
}

type HostReporter interface {
    Due(protocol.Envelope, time.Time) bool
    Collect(context.Context) (*protocol.HostObservation, error)
    MarkSent(sampleID string, at time.Time)
}
~~~

Runner 保有 lastEnvelope，每轮用 HostReporter.Due 决定 includeHost，并把该 bool 与 partial
分开传入 Synchronizer.SyncOnce。Synchronizer 在 ReportBuilder.Build 之前获取 cached/新 Host，
以参数注入 report，确保 fitReportToWire 看到真实字节。Host 采集或本地校验失败时，
记录 Issue 并用 nil 继续构建核心 report，不向 SyncOnce 返错。

MarkSent 只能在 HTTP 返回且 ValidateSyncResponse 成功后推进周期；不需要等后续
outbox ack/Core convergence，因为 PSP 已经收到该 SampleID。构建了报告但 POST 失败时，下一轮重发同一个 cached
sample 和 SampleID，直到成功或超过两倍周期；超过后可采新样本，但旧样本丢失是显式 gap。

fitReportToWire 的固定降级顺序是：先带 Host 编码；若超过 MaxSyncBodyBytes，先移除
Host 重试；仍超限时才走现有的 durable outbox partial flush/错误逻辑。因 Host 被移除的
轮次不调用 MarkSent，下一轮可再试，并开启 detail 分类为 wire_limit 的
host_telemetry_failed episode。遥测永远不能把本来能发送的控制/记账报告顶过 16 MiB 后
导致整轮失败。

### 7.2 Linux 固定数据源

| 数据 | 首选来源 | 失败行为 |
|---|---|---|
| boot ID | /proc/sys/kernel/random/boot_id | BootID 省略，CPU 使用进程 epoch |
| uptime | /proc/uptime | 整份 Host 不发送并记录 Issue |
| system CPU | /proc/stat | cpu.system unavailable |
| cgroup CPU | /proc/self/cgroup + /sys/fs/cgroup cpu/cpuacct/cpuset 固定派生路径 | cpu.cgroup unavailable |
| load | /proc/loadavg | Load section 为空并加入 load unavailable；CPU counter 保留 |
| 内存 | /proc/meminfo | memory.system unavailable |
| cgroup memory | /proc/self/cgroup + /sys/fs/cgroup memory 固定派生路径 | memory.cgroup unavailable |
| data-dir FS | statfs(data-dir) | filesystem.data unavailable |
| 接口计数 | /proc/net/dev | network.interfaces unavailable |
| 接口元数据 | net.Interfaces + /sys/class/net 固定字段 | 单字段 nil，不丢整个接口 |
| 默认接口 | /proc/net/route、/proc/net/ipv6_route | network.default_route unavailable |
| TCP | /proc/net/snmp | tcp unavailable |
| socket | /proc/net/sockstat、sockstat6 | sockets unavailable |
| conntrack | 固定 proc sys netfilter count/max | conntrack unavailable |
| Agent 进程 | /proc/self + getrlimit | process.agent unavailable |
| 进程 CPU 单位 | /proc/self/auxv 的 AT_CLKTCK | CPUTime 两字段为 nil，其他进程 gauge 保留 |
| Core 进程 | Supervisor 提供受控 PID/启动身份，再读对应 proc | process.core unavailable |
| TCP CC | 固定 proc sys net ipv4 文件 | tuning token |
| 默认 qdisc | 固定 proc sys net core 文件 | tuning token |
| OS release | /etc/os-release，仅读固定文件 | platform.distribution unavailable |

collector 不接受控制面提供的文件路径。除了构造时注入的 data-dir，所有读取路径必须在代码中固定。
单元测试通过注入 procRoot、sysRoot、etcRoot 使用 fixture，不允许测试依赖 CI 主机实时数值。

### 7.3 禁止外部命令

采集器不得执行：

- sh、bash、powershell；
- sysctl、tc、ip、ss、netstat；
- docker、systemctl、journalctl；
- lsof、ps、top；
- 任意由 PATH 解析的程序。

理由不是性能，而是外部程序的存在、版本、locale、输出和权限不可控，同时会扩大命令注入面。

### 7.4 容器与 cgroup

Linux collector 必须同时处理：

- cgroup v2 unified；
- cgroup v1 memory/cpu controller；
- 没有 cgroup 文件；
- cgroup max；
- 容器看到宿主机 proc 但受 cgroup 限制；
- host-network 下看到宿主机接口；
- rootless container 无权读取部分 sysctl；
- NAS bind mount 与 overlayfs。

Cgroup 解析规则冻结如下：

- 先从 proc self cgroup 获得当前进程的 controller 相对路径，再与构造时注入的
  sysRoot 固定 controller root 组合；清理后必须仍位于该 root 下；
- v2 memory.max/cpu.max 中的 max 表示 nil limit；
- v1 memory.limit_in_bytes 等于 64-bit LONG_MAX 按 page size 向下对齐的 sentinel 时视为
  unlimited；必须用 fixture 覆盖，不得使用“比系统内存大”的模糊启发式；
- v1 memory.memsw.* 包含 memory + swap，对外 SwapCurrentBytes/SwapLimitBytes 必须减去
  对应 memory 值并在采样竞态时最低取 0；文件缺失时两字段为 nil；
- v2 memory.events 的 oom/oom_kill 映射 OOM counter；v1 memory.failcnt 不映射 OOM；
- v2 cpu.stat 的 usec 值直接使用；v1 cpuacct.usage 的 ns 整除 1000 转为 usec，
  cpuacct.stat 的 user/system 只在 AT_CLKTCK 可用时转换；
- v1 cpu.cfs_quota_us < 0 表示 unlimited，QuotaUS/PeriodUS 两者都为 nil；
- cpuset 解析支持逗号与范围，拒绝重叠、逆序、负数或超过 4096 CPU 的输入；
- 任一文件在同一采样中发生删除/更换时，只丢弃受影响的 cgroup 子字段。

内存 UI 的优先展示规则：

1. 有有限 cgroup limit 时，主卡片显示 cgroup current / limit；
2. 同时把 system available / total 放在“宿主机可见值”次级行；
3. 没有有限 limit 时显示 system；
4. 两组都没有时显示 unavailable。

CPU UI 的优先展示规则：

1. 有有限 cgroup quota/cpuset 时，主卡同时显示“容器可用 CPU 占用”和
   “可见系统 CPU”，并标出真正更紧的限制来自 quota 还是 cpuset；
2. 没有配额但有 effective cpuset 时，load normalization 优先使用 effective CPU 数；
3. cgroup CPU 不可读时只显示 system CPU，不把 host CPU 写成容器占用；
4. CPU throttle 只有在 throttle 三个 counter 成对可差分时展示。

### 7.5 采集预算

- 一次采集软预算 100 ms，硬超时 2 秒；
- 不得读取无界日志或遍历整个 proc；
- 不得为统计连接逐行解析全部 tcp socket；
- 不得阻塞同步主循环；超时返回已有 section 和 unavailable；
- collector panic 必须被 Recover，生成稳定 Issue host_telemetry_failed；
- Issue detail 只含 section 和归类错误，不含文件内容。

HostReporter 对 Collector 做 single-flight：同时最多一次采集。硬超时时本轮返回 nil Host
并继续 sync；底层本地文件 syscall 若尚未返回，不再启动第二个 collector goroutine，迟到结果
直接丢弃。超时的 caller 不释放 latch；只有 worker 真正返回或 panic 时由 defer 释放，
因此永久卡住最多损失后续遥测，不会每 30 秒泄漏一个 goroutine。阻塞 fixture 必须证明
这一点。

每份样本在开始时记录 monotonic start，所有 section 完成后取 wall-clock CollectedAtMS，
CollectorDurationMS 由 monotonic elapsed 生成。不允许把上一份样本的成功 section 与本次新
section 拼成一份 Host；只能缓存完整 HostObservation 供网络重试。

### 7.6 同步运行统计

HTTP Syncer 外层增加线程安全 RuntimeStats：

- 请求编码完成后记录 request bytes；
- 收到完整响应后记录 response bytes 和 RTT；
- 响应通过 ValidateSyncResponse 且 outbox acknowledgement 完成后才算 success；
- 任一路径失败推进 failure 和 consecutive failures；
- success 清零 consecutive failures；
- 所有累计值只增不减，进程重启通过 AgentStartedAtMS 形成新 epoch。

### 7.7 本地 doctor 子命令

当前 Node 尚无 doctor 命令。第一阶段新增：

~~~
passwall-node doctor
passwall-node doctor --json
~~~

cmd/node 必须在 daemon flag 解析之前识别 doctor 子命令，与现有 --upgrade-info 等
特殊入口保持隔离。doctor 的 --data-dir 是必填绝对路径，--credential-file 是可选绝对
路径；当前 daemon 没有这两项的默认值，doctor 不得悄悄发明一套。它不连接 PSP、不启动
Core、不修改 SQLite/文件权限，也不读出 credential 内容；只检查路径存在性、安全性
和当前用户的可读性。未给 credential 时 credential.permissions 为 unavailable。

JSON 形状：

~~~json
{
  "schema_version": 1,
  "collected_at_ms": 0,
  "host": {},
  "checks": [
    {
      "code": "installation.layout",
      "status": "ok",
      "summary": "managed layout is valid"
    }
  ]
}
~~~

status 只能是 ok、warning、failed、unavailable。summary 最长 512 字节，不含秘密。
JSON 输出不得包含 credential、endpoint query、环境文件内容、完整配置、客户端身份或 IP。
第一版稳定 check code 必须包含：installation.layout、credential.permissions、
data_dir.access、state.sqlite_open、state.sqlite_quick_check、core.selection、
core.binary_digest、core.confirmed_config_digest、collector.host、collector.process。不适用的项返回
unavailable，不得省略；每个 code 恰好出现一次，按 code 字典序输出。
data_dir.access 在 data-dir 内用 O_CREATE|O_EXCL 创建随机名 mode 0600 的空 probe，fsync、
close 并立即删除；任一步失败为 failed，遗留文件名不输出到 JSON。SQLite 使用只读连接
运行 PRAGMA quick_check，不跑 migration。文本与 JSON 从同一 CheckResult slice 生成；--json 时 stdout
只有一个 JSON document，诊断日志进 stderr。

退出码：

- 0：没有 failed；
- 1：至少一个 failed；
- 2：参数、输出编码或诊断框架自身失败。

## 8. Panel 接收与故障隔离

### 8.1 接收顺序

HTTP node_sync handler 与 nodesync.Service.Sync 都不再对整份 report 盲目调用完整 validator。
handler 保留 MaxBytesReader、单 JSON document 检查和 ValidateNodeReportBase；service 再做一次 Base
防御非 HTTP 调用者，并单独处理 Host。nodesync.Service.Sync 保持配置与配额路径优先：

Service 入口立即把 report.Host 移到局部 host 变量，并在传给既有 ingest/cache 路径的
controlReport 副本中置 nil。cloneReport 和 cloneObservationReport 再显式清空 Host 作为
第二道防线。Host 只能进 metrics repo，不得留在 latest-full 内存 cache，否则每个离线
agent 可额外驻留 128 KiB。

1. 解码整体请求并验证 NodeReport 控制面部分；
2. 单独验证 Host；失败则丢弃 Host、做限频日志和内部 counter，不拒绝请求；
3. 完成 task receipt、Issue、三流 applied 和记账；
4. 计算 config / roster / directives；
5. 在独立、最长 500 ms 的 DB context 中尝试写最新 Host 快照和历史样本；
6. Host 持久化失败只记录 PSP 日志和内部观测计数，不返回非 2xx；
7. 返回已计算的 SyncResponse；其余关键路径失败仍按现有语义返回错误。

第 2、5、6 条是硬要求：坏 Host payload 或磁盘指标表的一个错误不能让节点
拿不到新 roster 或 quota。500 ms 是上限而不是目标；超时后必须立即返回控制响应。
若本轮持久化了 refresh 热窗所等待的新 SampleID，在返回前清除热窗并将当前已构建
响应的 Envelope.WantHostReport 设为 false；写入失败则保留 true，让 Node 在热窗期内重试。

认证、整体 body 上限、JSON 语法或 NodeReport 控制面契约失败仍拒绝整个请求。仅 Host
子树的语义错误按上述隔离规则处理。collector 应在发送前调用完整 validator；失败时
本轮不放入 Host，并使用 host_telemetry_failed Issue episode 报告。

Host metrics 明确是 best-effort observation，不进入 Node durable outbox。PSP 在一次独立事务中写
latest 与应写入的 raw/interface sample；该事务失败就整批放弃并记日志，Node 收到成功同步后无需
重放这个样本。图表允许出现 gap，不能为了“指标一条不丢”反过来阻断数据面控制。

### 8.2 最新快照

新增 domain.NodeHostObservation 与 NodeHostObservationRepo。数据库表为：

~~~
node_host_observations
  agent_id                 VARCHAR(64) PRIMARY KEY
  sample_id                CHAR(32) NOT NULL
  payload_digest           CHAR(64) NOT NULL
  collected_at             TIMESTAMP NOT NULL
  received_at              TIMESTAMP NOT NULL
  boot_id                  VARCHAR(64) NOT NULL DEFAULT ''
  resource_scope           VARCHAR(16) NOT NULL
  snapshot_json            BLOB/TEXT NOT NULL
  created_at               TIMESTAMP NOT NULL
  updated_at               TIMESTAMP NOT NULL
~~~

snapshot_json 是通过 protocol validator 后重新编码的 canonical JSON，不保存原始请求切片。
上限 128 KiB。它用于详情和当前编译版本已知的 additive 字段，不作为图表查询源。
payload_digest 是 canonical JSON 的小写 SHA-256。

更新必须按 received_at 单调：迟到的旧请求不得覆盖更晚快照。相同 sample_id +
payload_digest 是幂等成功，received_at 保留第一次成功接收时间；相同 sample_id 但 digest
不同是 sample identity conflict，丢弃新 Host、限频记录，但核心 sync 仍成功。

### 8.3 分钟历史

固定列放在 node_host_metric_samples；接口明细放在 node_interface_metric_samples。

node_host_metric_samples 必须包含以下有值/可空列：

- agent_id、sample_id、collected_at、received_at、boot_id；
- logical_cpus、system CPU 四个累计计数与 epoch、load 1/5/15；
- cgroup CPU usage/user/system、quota/period/effective CPUs、throttle 计数与 epoch；
- system memory/swap、cgroup memory/limit/OOM；
- data-dir filesystem bytes/inodes/read-only；
- TCP 累计计数、current established；
- socket gauge、conntrack；
- Agent/Core CPU counter、RSS、FD、threads、started_at；
- Core restart count；
- sync success/failure count、consecutive failures、last RTT；
- scope 与 collector duration。

列命名字典如下，实现不得用一个不可查询的 JSON 代替历史列：

~~~
id, agent_id, sample_id, payload_digest, collected_at, received_at, boot_id,
deployment, resource_scope, cgroup_version, data_filesystem_scope,
logical_cpus,
system_cpu_epoch, system_cpu_total, system_cpu_idle,
system_cpu_iowait, system_cpu_steal,
cgroup_cpu_epoch, cgroup_cpu_usage_us, cgroup_cpu_user_us,
cgroup_cpu_system_us, cgroup_cpu_nr_periods, cgroup_cpu_nr_throttled,
cgroup_cpu_throttled_us, cgroup_cpu_quota_us, cgroup_cpu_period_us,
cgroup_cpu_effective_cpus,
load_1, load_5, load_15,
system_memory_total_bytes, system_memory_available_bytes,
system_swap_total_bytes, system_swap_free_bytes,
cgroup_memory_epoch, cgroup_memory_current_bytes, cgroup_memory_limit_bytes,
cgroup_swap_current_bytes, cgroup_swap_limit_bytes,
cgroup_oom_events, cgroup_oom_kill_events,
filesystem_total_bytes, filesystem_available_bytes,
filesystem_total_inodes, filesystem_available_inodes, filesystem_read_only,
tcp_epoch, tcp_active_opens, tcp_passive_opens, tcp_attempt_fails,
tcp_estab_resets, tcp_in_segments, tcp_out_segments,
tcp_retrans_segments, tcp_current_established,
socket_tcp_in_use, socket_tcp_orphan, socket_tcp_time_wait, socket_udp_in_use,
conntrack_current, conntrack_limit,
agent_process_epoch, agent_started_at, agent_cpu_time,
agent_cpu_units_per_second, agent_rss_bytes, agent_open_fds,
agent_fd_limit, agent_threads,
core_process_epoch, core_started_at, core_cpu_time,
core_cpu_units_per_second, core_rss_bytes, core_open_fds,
core_fd_limit, core_threads,
agent_runtime_started_at, core_restart_count,
last_sync_success_at, last_sync_failure_at, consecutive_sync_failures,
sync_success_count, sync_failure_count, last_round_trip_ms,
last_request_bytes, last_response_bytes, collector_duration_ms,
created_at
~~~

id 是本地自增主键；agent_id、sample_id、payload_digest、collected_at、received_at、scope、logical_cpus、
collector_duration_ms 和 created_at 非空，其余指标列根据 section/field 可用性为 nullable。
通过 protocol MaxInt64 验证的 uint64 以带符号 BIGINT/int64 存储；读回 domain 时负数
是数据损坏错误，不转换为超大 uint64。float 列用 GORM 的 float64 跨方言映射，禁止 NaN/Inf。

唯一键为 agent_id + sample_id；另建 agent_id + received_at 范围索引。

node_interface_metric_samples：

~~~
sample_id + agent_id + interface_index  UNIQUE
interface_name
is_default_ipv4
is_default_ipv6
mtu
up
link_speed_mbps nullable
rx/tx bytes, packets, errors, dropped
received_at
~~~

外键是否启用遵循本仓库现有跨方言策略；即使无 FK，repo 删除 agent 时也必须事务删除相关样本。

### 8.4 写入节流

- 最新 snapshot 每个 Host report 都更新；
- 原始历史默认至多每 60 秒一条；
- 距上一历史样本不足 60 秒时，只更新 latest；
- boot_id、scope 或进程 started_at 改变时允许立即插入，形成可见断点；
- Panel 重启后从 DB 查询最后一条，不依赖内存节流；
- 60 秒以后允许成为 setting，但第一版先用编译常量，避免多个周期旋钮同时上线。

第一版常量命名固定为 DefaultNodeHostReportInterval=60s、
NodeMetricRawRetention=7*24h、NodeMetricHourlyRetention=90*24h、
NodeMetricRawWriteInterval=60s、NodeMetricRollupSafetyLag=5m。它们放在 node metrics service，
不放在 HTTP handler。

### 8.5 原始保留和小时汇总

- 原始样本默认保留 7 天；
- 小时汇总默认保留 90 天；
- rollup 必须先于原始 prune，只处理 bucket_end <= now - NodeMetricRollupSafetyLag 的小时；
- cutoff 向下取整到 UTC 小时，不能删除正在汇总的部分小时；
- cleanup 进入现有 hourly maintenance worker，不新增常驻 goroutine；
- 一次批量删除必须有上限，避免大库长事务；
- settings 后续命名为 node_metric_raw_retention_days 与 node_metric_hourly_retention_days；
- 0 是否表示永久必须与现有 retention UI 语义一致；上线设置项时补全三方言测试。

小时表 node_host_metric_hourly 必须保存：

- bucket_start、agent_id、sample_count、coverage_seconds；
- system CPU average/max、iowait average/max、steal average/max；
- cgroup CPU capacity utilization average/max、throttled-period ratio average/max、throttled time sum；
- load normalized average/max；
- memory used average/max、available minimum；
- cgroup used average/max、OOM/OOM kill delta sum；
- filesystem available minimum、inode available minimum；
- network RX/TX bps average/max、link utilization average/max；
- RX/TX error/drop delta sum；
- TCP retrans ratio average/max、retrans delta sum；
- Agent/Core CPU average/max、RSS average/max、FD max；
- conntrack ratio average/max；
- sync RTT average/max、failure delta sum；
- Core restart delta sum。

不完整小时写 coverage_seconds。UI 不得把低 coverage 的小时画成完整健康区间。

Rollup 先对每对相邻 raw sample 运行第 9 节的断点规则，再聚合已经得到的 rate/gauge；
不得用小时首尾 raw counter 直接相减。average 按有效 interval 秒数加权，coverage_seconds
是有效 interval 并集的秒数且上限 3600，max 取有效 interval 最大值。跨 UTC 小时的
interval 必须按边界按时间比例分摊。

同一 bucket 中如果 resource_scope、cpu_scope 或 memory_scope 改变，对应 primary 序列的
average/max 写 null；明确的 system 和 cgroup 序列仍各自汇总。默认接口身份在 bucket
内改变时，服务器总带宽与 link utilization 写 null，但接口独立序列仍可汇总。
小时表只持久结束且越过 safety lag 的 UTC bucket。hour history API 对未持久的
完整/未完整 bucket 使用同一个 rollup 纯函数从 raw 即时计算并合并；禁止同一
bucket 既读小时表又读 raw 后双计。Rollup upsert 必须幂等，在 raw prune 前重跑同一
bucket 得到字节等价结果。

### 8.6 PSP 自身观测

接入现有进程内 metrics registry：

- psp_node_host_report_total{outcome=accepted|no_host|invalid|identity_conflict|storage_error}；
- psp_node_host_history_total{outcome=inserted|throttled|duplicate|storage_error}；
- psp_node_host_persist_ms；
- psp_node_host_snapshot_bytes；
- psp_node_host_rollup_total{outcome=written|empty|error}；
- psp_node_host_pruned_rows_total{table=raw|interface|hourly}。

标签值只能是上述固定集，禁止 agent ID/server name 等高基数标签。storage_error 必须
从这里可见，因为它按设计不会返回给 Node。

### 8.7 Port 契约

只在 ports.Repos 增加一个 NodeHostMetric 聚合 repo，不把 latest/raw/interface/hourly 拆成四个
无法共享事务的 port。最小契约：

~~~go
type NodeHostMetricRepo interface {
    Persist(context.Context, NodeHostPersistRequest) (NodeHostPersistResult, error)
    Latest(context.Context, string) (*NodeHostObservation, error)
    LatestBatchByPanelIDs(context.Context, []int64) (map[int64]NodeHostSummary, error)
    RawRange(context.Context, string, time.Time, time.Time, bool) ([]NodeHostMetricSample, error)
    InterfaceRange(context.Context, string, string, time.Time, time.Time) ([]NodeInterfaceMetricSample, error)
    HourlyRange(context.Context, string, time.Time, time.Time) ([]NodeHostMetricHourly, error)
    UpsertHourly(context.Context, []NodeHostMetricHourly) error
    DeleteByAgentID(context.Context, string) error
    Prune(context.Context, NodeHostPruneRequest) (NodeHostPruneResult, error)
}
~~~

RawRange 的最后一个 bool 名为 includePredecessor，为 true 时最多额外返回 from 前一条；
返回始终按 received_at,id 升序。Persist 是 latest + 可选 raw + interfaces 的唯一事务边界，
结果显式返回 LatestUpdated、HistoryInserted、Duplicate、IdentityConflict。repo 不接受 protocol
类型或 raw JSON，service 完成 validated protocol → domain 转换。每个 method 都必须有 SQLite、
PostgreSQL、MySQL 共享 contract test。

## 9. 派生指标与精确公式

Panel 只在以下条件全部满足时对两个样本 A、B 做差分：

- B.received_at > A.received_at；
- 对应 counter epoch 相同；
- BootID 相同，或两边都没有 BootID 且进程 epoch 未变；
- B counter >= A counter；
- 时间差在 5 秒到 15 分钟之间。

不满足时输出 null gap，不猜测、不把当前值当 delta。
任何以 from 为左边界的查询/汇总都必须额外读取 from 之前最新一条 raw sample 作为
差分基线；该基线只参与计算，不作为范围内数据点返回。缺少基线时第一点 rate 为 null。

### 9.1 CPU

~~~
d_total  = B.total  - A.total
d_idle   = B.idle   - A.idle
d_iowait = B.iowait - A.iowait
d_steal  = B.steal  - A.steal

cpu_percent    = 100 * (d_total - d_idle - d_iowait) / d_total
iowait_percent = 100 * d_iowait / d_total
steal_percent  = 100 * d_steal / d_total
load_normalized = load_15 / capacity_cores
~~~

d_total 为 0 时全部为 null。最终值 clamp 到 0..100 只用于防御读取竞态，同时记录内部诊断；
不能用 clamp 掩盖持续的 collector 错误。

Cgroup CPU：

~~~
elapsed_us = elapsed_seconds * 1,000,000
usage_cores_percent = 100 * delta(usage_us) / elapsed_us
quota_cores = quota_us / period_us
quota_percent = usage_cores_percent / quota_cores
effective_cpuset_percent = usage_cores_percent / effective_cpus
cgroup_capacity_cores = min(all_known_positive(quota_cores, effective_cpus))
cgroup_capacity_percent = usage_cores_percent / cgroup_capacity_cores
throttled_period_ratio = delta(nr_throttled) / max(delta(nr_periods), 1)
throttled_time_ratio = delta(throttled_us) / elapsed_us
~~~

usage_cores_percent 以单核 100% 表示，可超过 100%。quota_percent 只在 quota/period
成对且两个样本相同时计算；无限 quota 时为 null。quota 与 cpuset 同时存在时
必须取两者较小者作为 cgroup 真实 capacity，不得用固定优先级忽略更紧限制。
primary_cpu_percent 优先取 cgroup_capacity_percent，没有任何 cgroup capacity 才用 system
cpu_percent；同时返回 cpu_scope=cgroup_quota|cgroup_cpuset|system，若两者数值相等则用
cgroup_quota。load_normalized 和 process 机器占比使用同一 capacity_cores；cgroup capacity
不可知时 capacity_cores=logical_cpus。

### 9.2 内存

~~~
memory_used_bytes   = total - available
memory_used_percent = 100 * memory_used_bytes / total

cgroup_used_percent = current / limit * 100
~~~

available 为 nil、total 为 0 或 cgroup limit 为 nil 时对应百分比为 null。Cgroup 竞态导致
current > limit 时 API 保留原始百分比；只在进度条绘制层 clamp。

### 9.3 文件系统

~~~
filesystem_used_percent = 100 * (total - available) / total
inode_used_percent      = 100 * (total_inodes - available_inodes) / total_inodes
~~~

inode 字段为 nil 或 total_inodes 为 0 时 inode_used_percent 为 null。

### 9.4 网络

每个接口只在 A/B 的 network counter epoch、interface index 和 name 全部相同时差分。
同名新 ifindex、同 ifindex 新名字或 counter 回退都是 gap。

~~~
rx_bps = 8 * (B.rx_bytes - A.rx_bytes) / elapsed_seconds
tx_bps = 8 * (B.tx_bytes - A.tx_bytes) / elapsed_seconds

error_ratio =
  delta(errors + dropped) /
  max(delta(rx_packets + tx_packets), 1)

link_utilization_percent =
  100 * max(rx_bps, tx_bps) /
  (link_speed_mbps * 1,000,000)
~~~

服务器总吞吐默认取 default IPv4 与 default IPv6 接口的并集求和，同一接口只能计一次。
只有 A/B 两份样本的默认接口并集完全相同时才计算服务器总吞吐；路由切换该点为
gap，各接口自身速率仍可计算。
没有默认接口时不自动把所有接口相加，避免把 bridge、veth 与物理接口重复计数；UI 显示各接口，
总吞吐为 null。link speed 为 nil/0 或默认接口不唯一时 utilization 为 null，不评价饱和告警。
链路按全双工处理，取 RX/TX 较大者，不把两个方向相加后与单方向链路速率比较。

### 9.5 TCP 重传

~~~
retrans_ratio = delta(retrans_segments) / max(delta(out_segments), 1)
~~~

out_segments delta 小于 1000 时不评价告警，但仍可展示绝对 retrans delta。

### 9.6 进程 CPU

~~~
process_cpu_cores_percent =
  100 *
  (delta(process_cpu_time) / cpu_time_units_per_second) /
  elapsed_seconds
~~~

只有相邻样本的 CounterEpoch 和 CPUTimeUnitsPerSecond 都相同才计算。该值以单核为 100%，
多线程 Core 可以超过 100%。Node 可用 CPU capacity 占比为：

~~~
process_capacity_percent = process_cpu_cores_percent / capacity_cores
process_system_percent = process_cpu_cores_percent / logical_cpus
~~~

UI 主显示单核口径并明确标注，次级显示 capacity 口径；只有在 system 作用域
才把 process_system_percent 称为“整机占比”。

### 9.7 新鲜度

~~~
age = now_on_panel - latest.received_at
~~~

- age <= 2 × effective host period：fresh；
- 2 × period < age <= 5 × period：stale；
- age > 5 × period：missing；
- agent 本身 offline 时优先显示 offline，不再单独制造 metric missing 告警。

effective host period 使用共享调度函数计算，不能假设 HostReportSeconds 一定是 poll 的整数倍。

## 10. 健康状态与告警

告警在 PSP 计算，Node 只报告事实。Node 不读取阈值、不自行决定服务器是否健康，也不发送邮件。

### 10.1 状态层级

每个节点有两个互不覆盖的状态：

1. connectivity：online、offline、unknown；
2. resource_health：healthy、warming_up、warning、critical、stale、unavailable。

不能把它们压成一个布尔值。典型组合：

- online + critical：Agent 活着但磁盘即将耗尽；
- online + warming_up：已收到指标，但尚未覆盖最长持续阈值窗口；
- online + stale：心跳正常但遥测 collector 停止更新；
- offline + unavailable：从未收到遥测的旧 Node；
- online + unavailable：新协议未支持或全部 section 不可读。

服务器列表可以映射成一个最高优先级 badge，但 API 必须保留两个维度。
只有最近一次持久化 capability 包含 host.telemetry.v1 的 agent 才评价 node_metric_stale；
从未声明 capability 的旧 Node 是 unsupported，不产生 stale 告警。capability 从有变无时，最后快照
保留用于审计但 freshness 显示 unsupported，直到能力恢复。

整体 resource_health 决策顺序为：capability 缺失→unavailable；metrics stale/missing→stale；
有 critical finding→critical；有 warning finding→warning；历史未覆盖需要的最长窗口→
warming_up；所有可评价 section 均缺失→unavailable；其余→healthy。connectivity=offline
不改写 resource_health，但列表综合 badge 优先显示 offline。

### 10.2 稳定告警 code

新增告警 code：

~~~
node_metric_stale
node_cpu_saturated
node_cpu_iowait_high
node_cpu_steal_high
node_cpu_throttled
node_load_high
node_memory_pressure
node_swap_pressure
node_cgroup_oom
node_disk_space_low
node_inode_low
node_filesystem_read_only
node_bandwidth_saturated
node_network_errors
node_tcp_retrans_high
node_conntrack_pressure
node_fd_pressure
node_core_restart_loop
node_sync_degraded
node_clock_skew
node_collector_slow
~~~

code 是 API 和通知模板表面，发布后不得重命名。需要改语义时新增版本化 code。

### 10.3 第一版默认阈值

第一版先使用编译默认值，不进入设置 UI。完成生产观察后再开放设置，避免一开始制造几十个未经
验证的旋钮。

| Code | Warning | Critical | 恢复 |
|---|---|---|---|
| node_metric_stale | 超过 2 个有效周期 | 超过 5 个有效周期 | 连续 2 个 fresh 样本 |
| node_cpu_saturated | CPU >= 90%，持续 10 分钟 | >= 97%，持续 5 分钟 | < 80%，持续 10 分钟 |
| node_cpu_iowait_high | iowait >= 20%，10 分钟 | >= 40%，5 分钟 | < 10%，10 分钟 |
| node_cpu_steal_high | steal >= 10%，10 分钟 | >= 25%，5 分钟 | < 5%，10 分钟 |
| node_cpu_throttled | throttled period ratio >= 20%，10 分钟 | >= 50%，5 分钟 | < 10%，10 分钟 |
| node_load_high | load15 / CPU >= 1.5，15 分钟 | >= 3，10 分钟 | < 1，15 分钟 |
| node_memory_pressure | available <= 10%，5 分钟 | <= 5%，2 分钟 | > 15%，10 分钟 |
| node_swap_pressure | swap used >= 50% 且 10 分钟内持续增长 | >= 80%，持续 5 分钟 | < 30%，15 分钟 |
| node_cgroup_oom | OOM delta > 0 | OOM kill delta > 0 | 事件型；新事件生成新告警 |
| node_disk_space_low | available <= min(total × 15%, 5 GiB)，连续 2 个样本 | <= min(total × 5%, 1 GiB)，连续 2 个样本 | > min(total × 20%, 8 GiB)，10 分钟 |
| node_inode_low | available <= 15%，连续 2 个样本 | <= 5%，连续 2 个样本 | > 20%，10 分钟 |
| node_filesystem_read_only | 一次确认样本 | 同左 | 连续 2 个可写样本 |
| node_bandwidth_saturated | 默认接口 RX 或 TX >= 链路速率 80%，10 分钟 | >= 95%，5 分钟 | < 60%，10 分钟 |
| node_network_errors | error ratio >= 1%，5 分钟且包 delta >= 1000 | >= 5% | < 0.2%，10 分钟 |
| node_tcp_retrans_high | retrans ratio >= 5%，5 分钟且 out delta >= 1000 | >= 15% | < 2%，10 分钟 |
| node_conntrack_pressure | >= 80%，5 分钟 | >= 95%，2 分钟 | < 70%，10 分钟 |
| node_fd_pressure | Agent 或 Core >= 80%，5 分钟 | >= 95%，2 分钟 | < 70%，10 分钟 |
| node_core_restart_loop | 10 分钟内 >= 3 次 | 10 分钟内 >= 5 次 | 30 分钟无重启 |
| node_sync_degraded | 连续失败 >= 3 | 连续失败 >= 10 | 一次成功 |
| node_clock_skew | 绝对值 >= 2 分钟，3 个样本 | >= 10 分钟，2 个样本 | < 1 分钟，3 个样本 |
| node_collector_slow | >= 500 ms，5 个样本 | >= 1500 ms，2 个样本 | < 250 ms，10 个样本 |

百分比规则在分母不可知时不评价。unavailable 不等于健康，也不单独升级为 critical；它通过
resource_health=unavailable 和 UI 说明表达。
内存规则优先评价有限 cgroup current/limit，否则评价 system available/total；Swap 也优先
使用成对的有限 cgroup swap，否则使用 system swap。node_cpu_saturated 使用
primary_cpu_percent；node_cpu_iowait_high 和 node_cpu_steal_high 只使用 system 计数。

### 10.4 告警去重与事件

- 沿用 alert.Service 的 live-derived 模型，不新增 alerts/events 表；
- finding identity 为 agent_id + code；接口型 finding 再加 interface index；
- OOM 等事件在最近 24 小时窗口内从 raw/hourly counter delta 派生，窗口结束后自然消失；
- 恢复后条件不再产出 Alert，不维护独立生命周期；
- 新增独立 TypeNodeResource，不复用现有 TypeNodeHealth；后者是 listener/node 健康；
- TypeNodeResource 是 admin-only，deep-link 到服务器详情；
- 顶栏每个 server 只聚合成一条 Alert，key 为 node_resource:panel_id，severity 取最高值，
  count 为活动 finding 数，since 为最早 finding；细项只在 node-health API 展开；
- resource warning 映射 Alert SeverityWarning，critical 映射 SeverityError；
- 接入现有 alert.Service 顶栏 bell，并更新 dashboard drift test；
- 节点 offline 后暂停资源阈值告警，只保留 offline；
- 删除 agent 后没有样本来源，告警自然消失。

### 10.5 健康服务包

新增 internal/service/nodehealth：

~~~
Evaluator
  Evaluate(latest, previous, historyWindow, now) []HealthFinding

HealthFinding
  Code
  Severity
  StartedAt
  LastSeenAt
  CurrentValue
  Threshold
  Unit
  InterfaceName optional
~~~

Evaluator 必须是纯函数；repo 查询、Alert DTO 和 HTTP DTO 在外层。持续窗口与恢复迟滞都从已保存
历史重建，因此 Panel 重启不会重复开启或丢失一条活动条件，也不需要内存状态。每条规则用表驱动
测试覆盖触发、保持、迟滞恢复、缺数据、counter reset 和低样本量。

Evaluator 每次读取至少最近 45 分钟有序样本，从最早样本开始重放 trigger/recovery 状态机；
如果已有当前可用值但历史不足以证明最长持续窗口，整体状态为 warming_up，不是假定
healthy。当必要 section 全部不可读才是 unavailable。OOM 规则额外读取 24 小时 counter
delta。AlertService 可在单次请求中批量读取全部 agent 窗口，禁止 per-agent N+1。

## 11. 管理 API

所有端点要求 Administrator。响应不得含 Node credential、Agent 私有安装数据或原始请求。

### 11.1 当前快照

~~~
GET /api/admin/servers/:id/node-metrics/current
~~~

成功：

~~~json
{
  "available": true,
  "freshness": "fresh",
  "received_at": "2026-09-17T12:00:00Z",
  "collected_at": "2026-09-17T11:59:59Z",
  "scope": {
    "deployment": "docker",
    "resource_scope": "mixed",
    "cgroup_version": 2
  },
  "platform": {},
  "summary": {
    "cpu_percent": 21.4,
    "cpu_scope": "cgroup_quota",
    "system_cpu_percent": 11.2,
    "cgroup_cpu_cores_percent": 42.8,
    "cgroup_cpu_quota_percent": 21.4,
    "cgroup_cpu_capacity_percent": 21.4,
    "cgroup_cpu_throttled_period_percent": 0.0,
    "memory_used_percent": 62.1,
    "memory_scope": "cgroup",
    "disk_used_percent": 48.0,
    "rx_bps": 1200000,
    "tx_bps": 8300000,
    "tcp_retrans_percent": 0.2,
    "core_rss_bytes": 104857600
  },
  "host": {},
  "health": {
    "status": "warning",
    "findings": []
  }
}
~~~

规则：

- 非 PSP PanelKind 返回 HTTP 409，错误 code 为 node_metrics_unsupported；
- PSP 节点从未报告 capability 时 available=false，HTTP 200；
- capability 存在但没有有效快照时 available=true、freshness=missing；
- summary 派生值缺失时为 null，不省略，方便前端稳定渲染；
- cpu_percent 是 9.1 节的 primary_cpu_percent，memory_used_percent 按 7.4 节的优先级；
- cpu_scope 和 memory_scope 在对应百分比为 null 时也为 null；
- host 只返回环境、当前 gauge、可用性和已经在后端计算的派生值；
- API 不返回原始 uint64 counter，避免 JS 精度和前后端公式漂移；
- 原始 counter 只存在于 protocol、持久化和后端 rollup 内。

current 的 rate 用 latest snapshot 与它之前最新一条不同 sample_id 的 raw sample 差分。
若 latest 本身已是最新 raw，repo 必须再向前取一条；不得将样本与自己相减得出 0 bps/0%。

### 11.2 历史

~~~
GET /api/admin/servers/:id/node-metrics/history
    ?from=RFC3339
    &to=RFC3339
    &resolution=auto|minute|hour
~~~

限制：

- 最大范围 90 天；
- from < to；
- minute 最大 2500 分钟，且 from 不得早于 raw retention cutoff；
- hour 最大 90 天；
- 返回点最多 2500；auto 自动选择；
- 时间一律 UTC；
- 每个 series 用 null 表示 gap；
- 不进行向前填充；
- coverage 一并返回。

auto 在范围不超过 2500 分钟且全部位于 raw retention 内时选 minute，否则选 hour。
显式请求不可用的 minute 范围返回 HTTP 422，错误 code=node_metric_resolution_unavailable，
不静默换成 hour。

响应：

~~~json
{
  "resolution": "minute",
  "from": "...",
  "to": "...",
  "series": [
    {
      "at": "...",
      "coverage_seconds": 60,
      "cpu_percent": 20.1,
      "cpu_scope": "cgroup_quota",
      "system_cpu_percent": 10.0,
      "cgroup_cpu_cores_percent": 40.2,
      "cgroup_cpu_quota_percent": 20.1,
      "cgroup_cpu_capacity_percent": 20.1,
      "cgroup_cpu_throttled_period_percent": 0.0,
      "memory_used_percent": 61.0,
      "memory_scope": "cgroup",
      "disk_available_bytes": 123,
      "rx_bps": 456,
      "tx_bps": 789,
      "link_utilization_percent": null,
      "tcp_retrans_percent": null,
      "core_cpu_percent": 31.0,
      "core_rss_bytes": 456
    }
  ]
}
~~~

接口明细使用独立端点，避免主图响应随接口数倍增：

~~~
GET /api/admin/servers/:id/node-metrics/interfaces
    ?from=&to=&resolution=&interface=
~~~

interface 参数必填，且必须匹配该 agent 已观察到的 canonical name，不能进入文件路径或命令。

### 11.3 按需刷新

~~~
POST /api/admin/servers/:id/node-metrics/refresh
~~~

行为：

- 只设置内存中的 WantHostReport 热窗，不持久化；
- 不创建 durable task；
- 返回 202；
- 不等待 Node；
- 同一服务器 10 秒内合并；
- 下一次 Node sync 的 Envelope 设置 WantHostReport；
- 页面轮询 current，直到 sample_id 变化或 30 秒超时；
- Panel 重启丢掉 refresh request 是允许的，因为它没有副作用。

热窗记录 agent_id、请求时的 baseline sample_id 和 30 秒 expires_at。在收到不同 sample_id
的有效 Host 后删除；Host 缺失/无效时继续下发 WantHostReport，直到成功或过期。

### 11.4 健康详情

~~~
GET /api/admin/servers/:id/node-health
~~~

返回 connectivity、resource_health、活动 finding 和 unavailable section。该端点独立存在；
current 只带健康摘要，避免重复大 payload，也避免服务器列表为每一行加载大 snapshot。

### 11.5 列表摘要

现有服务器列表 DTO 只增加：

~~~
node_resource_health
node_metrics_freshness
node_cpu_percent nullable
node_memory_percent nullable
node_metric_received_at nullable
~~~

必须批量加载所有 PanelID 的摘要，禁止服务器列表 N+1 查询。

## 12. 前端规格

### 12.1 服务器列表

PSP 原生节点行增加一个紧凑健康入口：

- 绿色：online + healthy + fresh；
- 蓝/中性：warming_up；
- 黄色：warning 或 stale；
- 红色：critical 或 offline；
- 灰色：unsupported / unavailable；
- tooltip 分开写“连接状态”和“资源状态”；
- CPU/内存只在有值时显示；
- 旧 Node 不显示 0%，显示“此版本未提供主机指标”。

列表不展示所有资源列，不自动每秒刷新。沿用服务器列表现有刷新节奏。

### 12.2 服务器详情

原生节点详情增加四个 tab：

1. **概览**
   - connectivity、resource health、最后同步、最后指标；
   - CPU、内存、磁盘、网络四张主卡；
   - Agent/Core 版本和状态；
   - 当前活动告警。

2. **性能**
   - system CPU + iowait + steal；
   - cgroup CPU capacity/quota/cpuset 占用、throttled period/time；
   - load 1/5/15，另画 normalized load；
   - system/cgroup memory 与 swap；
   - Agent/Core CPU、RSS、FD；
   - 数据目录容量/inode；
   - 1h、24h、7d、30d、90d 范围。

3. **网络**
   - 默认 IPv4/IPv6 接口；
   - RX/TX bps；
   - packets、errors、dropped；
   - TCP established、retrans；
   - conntrack；
   - 系统默认 congestion control、available 列表、default qdisc；
   - 只读文案明确 PSP 不会修改 BBR。

4. **诊断**
   - unavailable section；
   - sync RTT、连续失败、请求响应大小；
   - collector duration；
   - passwall-node doctor 命令提示；
   - 第二阶段加入远程诊断任务。

### 12.3 图表规则

- 使用已有 ECharts 依赖；
- byte rate 自动显示 Kbps/Mbps/Gbps，但 API 仍传 bps；
- byte gauge 用 KiB/MiB/GiB/TiB；
- gap 必须断线，不做平滑补点；
- scope 或 boot ID 改变处画断点标记；
- tooltip 显示 coverage；
- 不把小时 max 与 average 画成同一条未标注线；
- 移动端允许横向滚动，不挤压标签；
- 所有颜色从主题取值，支持暗色；
- 图表为空时区分 unsupported、unavailable、stale、no history。

### 12.4 文案与 i18n

新增 key 必须先写英文与简体中文源文件；繁体中文继续由仓库既有生成流程处理，不能手工改生成目录。

关键文案必须避免：

- “BBR 未开启，点击优化”；
- “0% CPU”表示未知；
- “主机内存”描述 container scope；
- “实时”描述一分钟采样；
- “限速”描述仅告警功能。

推荐名称：

- “带宽监控”而不是“总限速”；
- “TCP 拥塞控制（只读）”而不是“BBR 设置”；
- “Node 可见资源”并展示 scope；
- “最近 60 秒平均”而不是“当前瞬时”。

## 13. 脱敏诊断任务

第二阶段新增 diagnostics.collect.v1。它是只读任务，不扩展权限。

### 13.1 Args

~~~json
{
  "schema_version": 1,
  "sections": ["host", "runtime", "state", "events"],
  "max_events": 100
}
~~~

约束：

- sections 是 allowlist，最多 8；
- max_events 为 0..200；
- 不接受路径、命令、URL、host、port 或正则；
- task 必须带现有 expiry capability 和 NotAfterMS；
- NotAfterMS 必须不晚于创建时间加 5 分钟；
- 同一 agent 同时最多一个活动诊断任务，由 producer 层幂等合并。

### 13.2 Result

~~~json
{
  "schema_version": 1,
  "collected_at_ms": 0,
  "recovered": false,
  "host": {},
  "runtime": {
    "core_state": "running",
    "core_config_digest": "...",
    "stream_state": {}
  },
  "state": {
    "sqlite_quick_check": "ok",
    "outbox_pending": 0,
    "tasks_queued": 0
  },
  "events": [],
  "checks": []
}
~~~

总结果上限 512 KiB，低于协议 1 MiB 上限留出 envelope 余量。超限时按固定优先级丢弃最旧 events，
并返回 truncated=true；不得切断 JSON。

### 13.3 绝不能进入结果

- credential 与其 hash；
- environment file 原文；
- PSP endpoint 中的 query 或 userinfo；
- client credential、email、UUID、password、live IP；
- 完整 config / roster / directives body；
- TLS 私钥或完整证书；
- 进程环境、命令行；
- 任意日志原文，除非未来另立脱敏规范；
- 宿主机用户名、hostname、MAC；
- 任意文件内容。

### 13.4 Crash recovery

诊断是只读的。handler 实现 TaskRecoverer，遇到 running journal 恢复时可以重新采集，并在结果写
recovered=true。不得把重新采集伪装成原时刻快照。相同 task ID 的终态仍遵循现有 immutable replay。

### 13.5 PSP 管理入口

- POST /api/admin/servers/:id/node-diagnostics 创建或取回幂等任务；
- GET /api/admin/servers/:id/node-diagnostics/:task_id 查询；
- UI 显示 queued、offered、succeeded、failed、indeterminate；
- result 只对 Administrator 返回；
- audit 记录发起人、server、task id，不记录结果正文；
- 结果保留遵循 Node task retention，不另造无限历史。

## 14. 其他无特权能力清单

本节用于防止未来再从零头脑风暴。每项仍需按顺序独立实现，不代表第一阶段一起上线。

### 14.1 Core 运行操作

1. **core.restart.v1**
   - 只重启 Agent 自己启动的 Core；
   - args 绑定 expected engine、version、config digest；
   - 重启后必须 read-back 同一 digest 且状态 running 才成功；
   - Agent 自身不重启；
   - crash recovery 需要用 restart epoch 判断是否已经完成；
   - 不接受 PID、命令或路径。

2. **配置重新校验**
   - 对已确认配置重新运行 Core 自带 check；
   - 只读，无切换；
   - 返回分类错误，不返回完整配置。

3. **Core 二进制完整性**
   - 对当前审计目录二进制计算 SHA-256；
   - 与 catalog 已确认 digest 比较；
   - 不扫描任意目录。

4. **Node 维护模式**
   - 必须是持久 desired state，不是一次性 task；
   - disabled、draining、active 三态；
   - draining 的连接语义需按 Xray/sing-box 分别验证；
   - 不在本规格内直接设计协议，另立 RFC。

### 14.2 运营观测

- 周期累计流量预算及 80/90/100% 告警，不做封禁；
- 峰值带宽、P95 带宽、持续饱和时长；
- Linux proc pressure 的 CPU/memory/IO some/full avg10/60/300 与累计 stall；
- data-dir mount 通过 mountinfo major:minor 映射 diskstats 后的读写 bytes/ops/busy time；
- 映射到 overlay、网络文件系统或多个底层设备时明确 unavailable，不猜设备；
- softnet dropped/time_squeeze、TCP ListenOverflows/ListenDrops；
- 每 GiB 流量的 CPU 成本；
- Core RSS 随客户端/监听器数量的趋势；
- 重启、配置收敛、遥测失败的时间线；
- Agent 版本、Core 版本和资源异常的相关性；
- 公网同步来源 IP 变化，由 PSP 在认证 HTTP 边界观测；
- IPv4/IPv6 同步路径分类；
- 数据目录增长速率与预计耗尽时间；
- 容量建议：健康、接近容量、建议扩容；只建议，不自动迁移。

### 14.3 网络质量

无特权允许：

- 既有 PSP HTTPS 同步 RTT、DNS、TCP/TLS 错误分类；
- 对 PSP endpoint 的解析地址族和连接结果；
- 管理员发起、严格类型化的 TCP connect / TLS handshake 任务。

第一阶段不做任意目标探测。未来若新增 network.probe.v1：

- 目标必须来自 PSP 管理员显式输入并审计；
- 限制端口、超时、并发、次数和结果大小；
- 明确 SSRF 风险；
- 不允许 file、unix、gopher 等 scheme；
- 不允许跟随重定向到未授权地址；
- 不使用 raw ICMP；
- 每个任务最多 10 个目标、总执行不超过 30 秒。

### 14.4 日志

不要直接做“远程 tail journal”。Node 当前日志可能进入 journald，而非 root Agent 未必能读；
扩大 journal 组权限也会读到其他服务。

可接受路线：

- Agent 内部维护仅包含自身结构化事件的有界环形缓冲；
- Core stdout/stderr 由 Supervisor tee 到有界、脱敏、本地私有 ring；
- 默认不上传；
- 诊断任务只返回稳定事件 code、severity、时间和有界 summary；
- 任何日志上传前必须有专门的秘密扫描与测试。

### 14.5 其他可后续评审的 rootless 候选

- cgroup pids.current/pids.max 与 PID 额度压力；
- Node state.db、WAL、outbox、task journal、Core 目录的分类体积，只遍历 Agent 自有 data-dir；
- 配置 apply/check/switch/rollback 耗时、最近成功时间与失败分类；
- Core crash-loop、退出类别和“已确认配置下连续存活时间”；
- 按 listener 聚合的当前连接数，不含客户端 IP/身份，且必须由 Core 受控 API 提供；
- UDP InErrors/NoPorts/RcvbufErrors/SndbufErrors 和 TCP Syncookies/ListenOverflows 等有界 proc 计数；
- ephemeral port 范围与已用压力，但不为此遍历无界 socket 明细；
- PSP endpoint 的 DNS、TCP、TLS 分段耗时，服务端证书 notAfter 与证书链错误分类；
- 通过 PSP 观测的来源 ASN/国家变化，只在已有 GeoIP 能力内处理，不让 Node 调外部 IP API；
- OS/kernel 版本与 Panel 端签名生命周期 catalog 的 EOL/已知风险提示，Node 不自行联网查 CVE；
- reboot-required 等发行版特定只读信号，必须标准化为“提示”而不是可执行操作；
- thermal zone 温度，只在 sysfs 明确可读且可标识作用域时上报，容器中默认 unavailable；
- macOS/Windows 的等价 process/filesystem/network collector，先定义平台语义再声明 capability。

以下不是 rootless 候选：SMART/NVMe 底层健康、包捕获、socket owner 遍历、远程
journalctl、宿主机服务/防火墙管理。它们需要更大权限或会越过信息最小化边界，不应
因为“也挺有用”被塞进本 Agent。

## 15. 安全、隐私和滥用防护

### 15.1 权限验收

systemd unit 必须继续：

- User=passwall-node；
- NoNewPrivileges=true；
- CapabilityBoundingSet 只有既有 CAP_NET_BIND_SERVICE；
- ProtectSystem=strict；
- ReadWritePaths 只有 Node data-dir；
- 新 collector 不要求放宽 ProtectProc 或 /sys 写权限。

Docker Agent 必须继续：

- 非 root 运行；
- cap_drop ALL；
- 仅 entrypoint 现有 CHOWN/SETGID/SETUID，exec 后不保留；
- read_only root filesystem；
- 不挂载 Docker socket、host proc 可写视图或 sysfs 可写视图；
- 不使用 privileged。

CI 增加静态基线测试，任何 capability 或 unit sandbox 变化都要显式更新快照。

### 15.2 输入边界

- HostObservation 来自已认证 Agent，但仍是不可信输入；
- 所有数组、字符串、数值和整体 JSON 都有上限；
- Panel 在 SQL 前验证；
- 不允许 raw map[string]any 直接进数据库；
- snapshot canonicalize 后存；
- API range、points、interface 参数有限制；
- diagnostics task args 逐字段解析，拒绝未知字段；
- 前端不渲染 Node 提供的 HTML。

### 15.3 信息最小化

不上报：

- hostname；
- 用户名；
- MAC 和接口 IP；
- 全路由表；
- 任意 mount path；
- 其他进程；
- client live IP；
- credential 或配置正文。

distribution ID、kernel release、interface name 属于运维必要信息，只对 Administrator 展示。

### 15.4 数据规模防护

- 每 agent 每分钟最多一个 raw host sample；
- 每 sample 最多 32 个接口；
- latest snapshot 最大 128 KiB；
- diagnostics result 最大 512 KiB；
- history API 最大 2500 点；
- retention cleanup 分批；
- 删除 agent 必须清理样本；
- schema 索引必须通过三方言查询计划与压力测试。

## 16. 跨仓兼容与发布顺序

### 16.1 Additive 兼容

- 旧 Node 不认识 Envelope 新字段，会忽略并继续工作；
- 旧 Node 不上报 Host，Panel 显示 unsupported；
- 新 Node 对旧 Panel 上报 Host 时，旧 JSON decoder 会忽略未知字段，核心同步继续；
- capability 是当前事实，Node 禁用 collector 后下一报告必须删除 capability；
- Panel 不把 capability sticky；
- protocol version 保持 v1。

必须用实际旧版本 fixture 证明“新 Node → 旧 Panel”和“旧 Node → 新 Panel”，不能只凭 Go 默认行为推断。

### 16.2 推荐发布顺序

1. 在 Passwall-Node protocol 包加入类型、validator、共享调度函数和测试；
2. 发布 Node module revision/tag；
3. PSP bump go.mod，先实现接收但 UI 可隐藏；
4. 部署新 PSP；
5. 发布带 collector 的 Node；
6. 小规模升级 1 台 systemd + 1 台 Docker；
7. 观察至少 7 天数据库增长、collector 耗时与误报；
8. 开启 UI 告警；
9. 再扩大升级。

即使顺序颠倒，核心同步也必须兼容；推荐顺序只是避免先产生无人消费的数据。

### 16.3 回滚

- PSP 回滚：新 Node 的 Host 字段被旧 PSP 忽略，不影响三流；
- Node 回滚：capability 消失，PSP 保留最后快照但标记 stale/unsupported，不写 0；
- DB schema 回滚遵循 PSP 既有数据库备份纪律；
- 删除 telemetry 表不是 Node 回滚的必要条件；
- 告警关闭不删除历史；
- 任何回滚都不能改变 traffic counter baseline。

## 17. 实现工作包与依赖

### WP0：协议骨架

**仓库**：Passwall-Node

内容：

- protocol/host.go；
- NodeReport.Host；
- Envelope host cadence；
- capability；
- validators 与 limits；
- partial MarshalJSON；
- ShouldSendHost；
- conformance fixtures。

完成判据：

- 新旧 JSON 互通；
- ValidateHostObservation 拒绝 malformed host；Panel 的隔离语义在 WP5 验证；
- partial/full 都可携带；
- wire 上限测试；
- PSP 尚未改时 Node protocol 包独立测试全绿。

### WP1：Linux collector

**仓库**：Passwall-Node

内容：

- 固定 proc/sys parser；
- cgroup v1/v2；
- statfs；
- network/TCP/socket/process；
- BBR 只读；
- fixture 测试；
- resource scope。

依赖：WP0。

完成判据：

- 无外部命令；
- 非 root unit 与 Docker 均采集；
- 每个文件缺失均有测试；
- 100 次 fixture benchmark 无泄漏和非线性遍历；
- go test -race 通过。

### WP2：调度和同步统计

**仓库**：Passwall-Node

内容：

- HostReporter cache；
- ShouldSendHost 接线；
- RuntimeStats；
- ReportBuilder；
- collector failure Issue；
- runner wake/partial 行为。

依赖：WP0、WP1。

完成判据：

- 60 秒周期在 30 秒 poll 下每两轮一次；
- WantHostReport 下一轮一次；
- POST 失败不错误推进 last sent；
- 重发 sample id 幂等；
- collector 超时不阻断 sync；
- capability 只在实现存在时声明。

### WP3：passwall-node doctor [--json]

**仓库**：Passwall-Node

复用 WP1，不能另写第二套采集器。

完成判据：

- 文本输出兼容；
- JSON schema 固定；
- secret golden test；
- 非 root systemd 安装与 Docker 容器内均可运行；
- exit code 测试。

### WP4：Panel domain、ports、SQL

**仓库**：Passwall-Sub-Panel

内容：

- NodeHostObservation；
- latest/sample/interface/hourly domain；
- repo interfaces；
- 三方言 schema；
- schemaModels / guard；
- idempotent writes；
- batch list summaries；
- delete cascade service logic。

依赖：WP0 发布 module。

完成判据：

- SQLite/Postgres/MySQL 全套 repo 测试；
- duplicate sample 幂等；
- late sample 不覆盖 latest；
- 每 agent 60 秒 throttle；
- 删除 agent 不留 orphan；
- 大整数边界一致。

### WP5：nodesync 接收

**仓库**：Passwall-Sub-Panel

内容：

- Host validator；
- latest/history ingest；
- best-effort persistence boundary；
- current capability observation；
- refresh request；
- cloneReport/cloneObservationReport 不保留 Host。

依赖：WP4。

完成判据：

- Host DB 写失败仍返回有效 SyncResponse；
- malformed Host 在 SQL 前丢弃，但仍返回有效 SyncResponse；
- partial/full 均接收；
- 旧 Node 无回归；
- refresh 合并和过期测试。

### WP6：派生与 rollup

**仓库**：Passwall-Sub-Panel

内容：

- sample differ；
- counter epoch/gap；
- minute readers；
- hourly rollup；
- raw/hourly prune；
- coverage。

依赖：WP4。

完成判据：

- 本文 §9 每个公式有 table test；
- reset、wrap、boot change、time reversal 都产生 gap；
- rollup-before-prune；
- UTC 小时边界；
- DST 不影响；
- 三方言结果一致。

### WP7：健康与 alert

**仓库**：Passwall-Sub-Panel

内容：

- nodehealth pure evaluator；
- 从 metric history live derive finding；
- alert.Service 接线；
- hysteresis；
- offline suppression；
- notification localization。

依赖：WP6。

完成判据：

- 本文阈值、持续窗口、恢复全部有测试；
- 缺数据不误报 0；
- 一次尖峰不告警；
- offline 不产生重复 stale spam；
- 告警 identity 稳定。

### WP8：管理 API

**仓库**：Passwall-Sub-Panel

内容：

- current/history/interfaces/refresh/health；
- admin route；
- range/point bounds；
- server list batch summary；
- audit。

依赖：WP5、WP6、WP7。

完成判据：

- RBAC；
- 404/unsupported/missing 状态固定；
- 2500 点上限；
- 无 N+1；
- uint64 不直接泄露给 JS；
- OpenAPI 或 handler contract test。

### WP9：前端

**仓库**：Passwall-Sub-Panel/web-react

内容：

- types/API；
- list badge；
- 四个 tab；
- ECharts；
- empty/stale/scope；
- refresh；
- i18n；
- mobile/dark。

依赖：WP8。

完成判据：

- Vitest 覆盖所有状态；
- TypeScript strict build；
- 不存在 unused import；
- 旧 Node UI；
- Docker mixed scope UI；
- null gap 断线；
- production build + smoke:dist。

### WP10：远程诊断

**两个仓库**。

依赖：WP0-WP5，且现有 task expiry/retention/restore 发布门保持满足。

完成判据：

- args/result shared types；
- capability gate；
- crash recovery；
- 512 KiB 截断；
- secret fixture；
- administrator + audit；
- task 全生命周期 UI。

### 依赖图

~~~
WP0 ──┬──> WP1 ──> WP2 ──> WP3
      │
      └──> WP4 ──┬──> WP5 ──┬──> WP8 ──> WP9
                 └──> WP6 ──> WP7 ──┘

WP0 + WP1 + WP4 + WP5 ──> WP10
~~~

WP0 与 UI 之间不得直接跳跃；没有持久化、公式和历史语义时先画图只会把临时 JSON 变成事实标准。

## 18. 测试矩阵

### 18.1 Protocol

- capability absent/present；
- Host absent/present；
- partial/full MarshalJSON；
- every limit boundary；
- duplicate interface/unavailable/capability；
- NaN/Inf；
- MaxInt64；
- JSON unknown additive field；
- 128 KiB Host；
- 16 MiB whole report；
- fuzz ValidateHostObservation；
- mutation test：故意把 nil 写成 0，测试必须失败。

### 18.2 Linux fixtures

必须包含：

- 普通 systemd host；
- Docker cgroup v2 CPU/memory；
- cgroup v1 CPU/cpuacct/cpuset/memory；
- unlimited cgroup；
- finite CPU quota、cpuset 与 throttling counter；
- 512 MiB memory limit；
- proc 缺字段；
- 老内核没有 MemAvailable；
- 没有 conntrack；
- 多默认路由；
- IPv6 only；
- veth/bridge/physical 混合；
- 网卡 counter reset；
- Core stopped / restarted；
- 32/64 位 auxv 与 AT_CLKTCK 缺失；
- data-dir read-only；
- overlayfs；
- BBR available/default；
- BBR unavailable；
- 非 UTF-8 os-release；
- 超大/负数文本输入被 parser 拒绝；
- context timeout。

### 18.3 Panel repos

每种 SQLite、PostgreSQL、MySQL：

- schema create；
- latest upsert；
- late write；
- duplicate sample；
- 60 秒 throttle；
- interface batch；
- range index；
- rollup；
- prune；
- agent delete；
- concurrent same sample；
- concurrent different samples；
- oversized snapshot；
- corrupt stored JSON fail closed。

### 18.4 服务

- Node online/offline + resource health matrix；
- 所有 formula；
- every alert trigger/recover；
- low traffic retrans denominator；
- counter reset；
- missing section；
- stale；
- boot change；
- clock skew；
- collector slow；
- sync failure recovery；
- Host storage error does not fail sync。

### 18.5 HTTP

- admin only；
- non-native server；
- unsupported old Node；
- supported but no sample；
- current fresh/stale/missing；
- invalid range；
- too many points；
- interface injection string；
- refresh coalescing；
- batch list query count；
- cache headers：敏感运维数据 no-store。

### 18.6 Frontend

- every empty state；
- null gap；
- scope labels；
- threshold colors；
- dark/light；
- mobile；
- refresh timeout；
- unsupported version；
- stale snapshot；
- API partial fields；
- locale fallback；
- chart large values；
- 90-day hourly selection。

### 18.7 跨仓 acceptance

1. 真 Node fixture 生成 Host report；
2. 真 PSP endpoint 认证、接收、返回 cadence；
3. Node 应用三流不受 Host 影响；
4. PSP current/history 可读；
5. 删除 Node 后样本清理；
6. 旧 Node 对新 PSP；
7. 新 Node 对旧 PSP；
8. systemd 非 root；
9. Docker cap_drop ALL；
10. 模拟 metrics DB 写失败仍可完成 roster/quota round trip。

## 19. 性能与容量预算

### 19.1 Wire

目标：

- 无接口异常时单份 HostObservation <= 16 KiB；
- 32 接口极限 <= 64 KiB；
- 绝对上限 128 KiB；
- 默认每 60 秒一份。

100 台节点按平均 16 KiB：

~~~
100 * 16 KiB / 60s ≈ 26.7 KiB/s 上行载荷
~~~

这远低于 16 MiB round-trip 上限，但仍应在真实样本中测量而不是只算结构体。

### 19.2 DB

100 台、每分钟一条：

~~~
100 * 1440 * 7 = 1,008,000 host raw rows
~~~

接口表按平均 3 个接口约 3,024,000 rows。必须有范围索引、批量 insert、分批 prune 和真实三方言
压力测试。若实际单行体积使 SQLite 自用部署不可接受，优先降低接口历史密度或只持久默认接口，
不能偷偷缩短 retention。

### 19.3 查询

- server list：固定 2 次查询以内完成 Panel + metric summaries；
- current：主键查询；
- minute history：agent_id + received_at 索引；
- hour history：agent_id + bucket_start；
- 90 天查询最多 2160 小时，低于 2500 点上限，不需要悄悄截断；
- 图表请求可 15 秒短缓存，但 current 和 health no-store。

## 20. 验收场景

实现完成前必须演练：

### 场景 A：旧 Node

- 服务器仍在线；
- 核心与流量正常；
- UI 显示“此 Node 版本未提供主机指标”；
- 无 0% 假数据；
- 不产生 metric stale 告警。

### 场景 B：systemd Linux

- passwall-node 用户运行；
- CPU/内存/磁盘/网络/Core 指标出现；
- BBR 只读出现；
- unit capability 未扩大；
- passwall-node doctor --json 无秘密。

### 场景 C：Docker 512 MiB limit

- scope=mixed/container；
- 主卡显示 cgroup 512 MiB，不显示宿主机总内存作为可用容量；
- host-network 接口不被宣称为完整宿主机管理能力；
- 无 privileged / Docker socket。

### 场景 D：Panel 暂时不可达

- Node 继续本地配额与 Core；
- sync failure count 增长；
- 恢复后指标出现 gap；
- 不把整个离线期间插值成连续线；
- cumulative network/TCP counter 恢复后可按最大 15 分钟规则决定是否差分。

### 场景 E：Node 重启

- Agent process counter 断点；
- BootID 不变；
- Host CPU/network counter 可继续；
- Core 若重启则 Core process counter 断点；
- 不出现负速率。

### 场景 F：宿主机重启

- BootID 变化；
- 所有 host cumulative counter 断点；
- uptime 回落可见；
- 不把当前 counter 当作重启期间用量；
- 图表画 gap/重启标记。

### 场景 G：磁盘耗尽

- 告警在持续窗口后触发；
- latest metrics 写失败时 Node 核心 sync 仍按能完成的部分继续；
- PSP 主 DB 真正不可写时沿用现有整体故障语义，不假装成功。

### 场景 H：网卡重命名或默认路由变化

- interface index/name identity变化形成断点；
- 不把旧 eth0 counter 接到新 eth0；
- default interface 总吞吐重新选择；
- 不重复加 bridge/veth。

### 场景 I：collector 部分无权限

- 其他 section 继续上报；
- unreadable section 为 unavailable；
- UI 不显示 0；
- 同一稳定缺失不每分钟制造新 Issue。

### 场景 J：恶意大报告

- 超过整体 HTTP body 上限时在 JSON 解码前返回 413；
- 整体未超限、但 Host 子树超过 128 KiB 时，丢弃 Host 且核心 sync 继续；
- 不分配无界 map；
- 不写无效 snapshot；
- 关键 sync 状态不被非关键遥测提交或回滚逻辑污染。

## 21. 文件改动地图

以下文件名是导航建议，不是线上合约；若现有包结构要求改名，仍必须保持本节的分层职责。

### Passwall-Node

- protocol/report.go：Host 字段与 partial marshal；
- protocol/envelope.go：host cadence；
- protocol/host.go：共享类型；
- protocol/limits.go：上限；
- protocol/validate.go：验证；
- protocol/compatibility.go：不得把 telemetry 混入 upgrade compatibility；
- internal/host：平台 collector；
- internal/agent/report.go：Host 注入；
- internal/agent/run.go 或 sync.go：调度；
- internal/agent/transport.go：runtime stats；
- internal/core/process：只暴露受控 Core process observation，不泄露任意 PID API；
- internal/manage/doctor.go：doctor 文本/JSON；
- cmd/node/main.go：composition；
- deployment acceptance：权限不扩大；
- README：用户可见说明。

### Passwall-Sub-Panel

- internal/domain：host observation/sample/health；
- internal/ports/repos.go：repos；
- internal/adapters/sqlstore/schema.go：表和索引；
- internal/adapters/sqlstore：latest/sample/rollup repos；
- internal/service/nodesync：best-effort ingest；
- internal/transport/http/handler/node_sync.go：base/Host 分离验证且保留 body/trailing 上限；
- internal/service/nodehealth：纯 evaluator；
- internal/service/rollup 或独立 node metric rollup：先 rollup 后 prune；
- internal/service/alert：finding 投影；
- internal/transport/http/handler：metrics API；
- internal/app/app.go：composition 与 hourly maintenance 接线；
- web-react/src/api：API；
- web-react/src/types：DTO；
- web-react/src/pages 或 components：服务器详情；
- web-react/src/locales：源语言 key；
- docs：协议与运维说明。

## 22. 十五条不要

1. 不要为了读指标给 Agent root。
2. 不要增加 CAP_NET_ADMIN 或 privileged container。
3. 不要调用 shell、sysctl、tc、ip、ss、docker。
4. 不要把 unavailable 写成 0。
5. 不要把 Docker cgroup 内存叫宿主机内存。
6. 不要让 Host 写失败阻断 roster/quota。
7. 不要把 raw counter 直接画成速率。
8. 不要跨 BootID 或 counter reset 做差。
9. 不要把所有接口相加成总带宽。
10. 不要向 JS 发送会丢精度的 uint64 number。
11. 不要每个服务器逐行查询 metric summary。
12. 不要把一次尖峰变成告警。
13. 不要上传环境、命令行、凭据、完整配置或 client IP。
14. 不要用 task 表示持续的 desired state。
15. 不要因为新增 telemetry capability 改变基础协议或升级兼容判断。

## 23. 最终 Definition of Done

只有同时满足以下条件，第一阶段才能标记完成：

- 协议类型只有 Passwall-Node 一份真相源；
- 新旧 Node/Panel 四向兼容经过真 fixture；
- systemd 与 Docker 都不扩大权限；
- Linux collector 覆盖本文第一阶段字段；
- collector 任意局部失败不阻断同步；
- Panel 三方言持久化、rollup、prune 通过；
- 所有速率与百分比严格按本文公式；
- gap/reset/reboot 不产生负值或尖峰；
- current/history/health API 有管理员边界和上限；
- 前端正确区分 unsupported、unavailable、stale、offline；
- 默认告警有持续窗口和恢复迟滞；
- 服务器列表无 N+1；
- passwall-node doctor --json 通过 secret golden test；
- Go test、go vet、前端 test/build/smoke 全绿；
- 1 台 systemd + 1 台 Docker 运行 7 天，无权限扩大、无同步回归；
- 数据库增长、wire 大小、collector P95 耗时有实测记录；
- 文档更新为“已实现”前，所有验收证据可复现。

## 24. 后续评审时允许重开的决定

以下不是第一阶段实现阻塞项，但未来可以用生产数据重开：

- raw retention 是否从 7 天调整；
- hourly retention 是否从 90 天调整；
- 60 秒 host cadence 是否开放为设置；
- 是否只保存默认接口历史以降低行数；
- 告警阈值是否进入全局或 per-node settings；
- 是否实现 macOS/Windows 完整 collector；
- 是否上线 diagnostics.collect.v1；
- 是否设计 node maintenance desired state；
- 是否提供类型化 TCP/TLS 外部探测。

以下决定除非另立安全 ADR，不应重开：

- 新功能非 root；
- 不新增宿主机特权 helper；
- 不做 BBR 写入；
- 不做 tc/eBPF 强制限速；
- 不提供任意命令执行；
- 遥测失败不阻断数据面控制协议。
