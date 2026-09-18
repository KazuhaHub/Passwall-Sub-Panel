# PSP Node 无特权可观测性 —— 实施进度

- **规格**：[psp-node-rootless-observability-plan.md](psp-node-rootless-observability-plan.md)（冻结于 2026-09-17，唯一权威）
- **建立日期**：2026-09-17
- **当前批次**：Node 侧 WP0 → WP3（一次性做完后统一评审）

本文件只做勾选、记录决策与偏差。任何字段名、公式、阈值、边界的争议**一律回到规格**，
本文件不复制规格内容，以免两份文档漂移。

## 已冻结的开工决策

| 议题 | 决定 | 日期 |
|---|---|---|
| 推进节奏 | Node 侧 WP0→WP3 连续做完，再统一评审 | 2026-09-17 |
| "旧 Node" 兼容基准 | `v0.0.1-beta9`（最新已发布 tag） | 2026-09-17 |
| Node 工作树遗留改动 | 其内容是对齐 PSP 已落地的 Xray 日志格式，单独提 PR | 2026-09-17 |
| 工作分支 | 两个仓库均用 `kazuha/<task-name>`，从 `origin/main` 切出 | 2026-09-17 |

## 前置准备

- [x] 识别 Node 工作树上的遗留改动：并非独立工作，而是 Xray 日志格式对齐的 Node 侧一半
      （PSP 侧格式已随 `origin/main` 落地，Node 侧仍是 `level=info message="..."`）
- [x] 将该改动 rebase 到 `origin/main`（原工作基于落后 11 个提交的 HEAD，
      上游已加入 `pn` CLI 与 `upgrade.UpgradeContract`，直接提会回退上游）
- [x] 补上规格评审时发现的两处遗漏：`docker-entrypoint.sh` 的 `fatal` 路径、
      `deployment/baseline_test.go` 中钉住旧格式的断言
- [x] 提交并开 PR：**KazuhaHub/Passwall-Node#23**（分支 `kazuha/node-xray-style-logs`）
- [x] WP0 分支自 `origin/main` 切出 —— #23 只触及 `cmd/node/`、`deployment/`、
      `docker-entrypoint.sh`、`README.md`，与 `protocol/` 无重叠，无需等待其合并
- [x] 提交并开 PR：**KazuhaHub/Passwall-Node#24**（分支 `kazuha/node-wp0-protocol`）
- [ ] **#23、#24 合入 `origin/main`**

### 仓库同步状态（2026-09-17 核对）

两个仓库的本地 `main` 都曾落后于 `origin/main`，且 PSP 上已有他人的多个 PR 在 GitHub 合并。
已处理如下，**后续每个 WP 开工前都要重新核对一次**。

- [x] PSP：本地 `main` 落后 20 个提交，已 `--ff-only` 快进到 `bcae72a`
- [x] Node：本地 `main` 落后 11 个提交，已更新到 `29e6f3c`
- [x] PSP 旧分支 `kazuha/psp-node-rootless-observability` 已随 **PR #96（已合并）** 整体落地，
      其中"计划文档"与"CI race"两个提交在 `origin/main` 上内容一致（文档字节相同，
      `test.yml` 已含 `go test -race`）。**因此该分支是被 squash 合并的旧副本，
      不得 rebase 重放。** 远端分支已随合并自动删除，本地那份是残留。
- [x] WP0 分支重新核对：`kazuha/node-wp0-protocol` 相对 `origin/main` 为 1 ahead / 0 behind

他人在 PSP 上合并的近 20 个提交基本是依赖升级与 CI 调整，其中一条影响本计划的认知：

- **#119 已删除 `web-react/vite.config.js`、`vite.config.d.ts`、`tsconfig.node.tsbuildinfo`**
  （CLAUDE.md 中"这三个是 tracked 文件，改 `vite.config.ts` 并重建会弄脏 git"的说法已过时）。
  依赖也有大版本跃迁（TypeScript → 7.0.2、Vitest → 5.0.0、jsdom → 30）。
  WP9 开始前需按当前实际状态重新确认，不要照抄本文或 CLAUDE.md 的旧描述。

## 实现前需要补的规格缺口

规格把这些当作既有条件，实际不是。开工前必须先在规格里补记理由，再实现。

- [x] **§5.10 依赖的 Core 子进程 handle 并不存在。**
      `internal/core/core.go:87` 的 `Supervisor` 接口只有 `Run/Apply/Deploy/Status`，
      `*exec.Cmd` 被封在 `process.managedProcess` 内部，没有任何对外暴露 PID 的路径。
      → 已解决：**KazuhaHub/Passwall-Node#25**（分支 `kazuha/node-core-process-handle`）
      新增 `agentcore.ProcessHandle`（PID + 内核 starttime）与 `Supervisor.ProcessHandle()`。
      该改动不依赖 protocol 类型，可与 WP0 并行落地。
      实现中两个值得记的点：proc stat 的字段必须**从最后一个 `)` 之后数**
      （comm 里的可执行名不被转义，二进制被替换时读作 `xray) (deleted)`）；
      非 Linux 平台 starttime 不可得，返回"有 Core 运行但不可校验"，
      由 `Verifiable()` 区分，采集器据此报 unavailable 而非"零占用的可用 Core"。

## WP0 协议骨架（Passwall-Node）

分支：`kazuha/node-wp0-protocol`（自 `origin/main` 切出，提交 `97ace17`）

- [x] `protocol/host.go`：`HostObservation` 及全部子结构（规格 §5.4–§5.12）
- [x] `protocol/report.go`：`NodeReport.Host` 字段
- [x] `protocol/report.go`：partial 专用结构显式携带 `Host`
      ——并验证过：删掉该行后 `TestHostObservationSurvivesBothReportShapes` 立即失败
- [x] `protocol/envelope.go`：`HostReportSeconds` / `WantHostReport`
- [x] `protocol/envelope.go`：`ShouldSendHost` 与 `EffectiveHostReportPeriod`，
      与既有 `ShouldSendFull` 同风格，PSP 与 Node 共用
- [x] `protocol/limits.go`：Host 相关上限常量
- [x] capability `host.telemetry.v1`；未混入 `AgentUpgradeCapabilities`
- [x] `protocol/validate.go`：`ValidateNodeReportBase` / `ValidateHostObservation` / `ValidateNodeReport`
- [x] §18.1 的协议测试矩阵（边界表、NaN/Inf、MaxInt64、重复项、未知新增字段、
      nil 不得编码为 0、`ShouldSendHost` 周期、envelope 边界），fuzz 15 秒无反例

完成判据（规格 §17 WP0）：

- [x] 新旧 JSON 互通（未知新增字段不导致拒绝）
- [x] `ValidateHostObservation` 拒绝 malformed host
- [x] partial / full 都可携带 Host
- [ ] wire 上限测试（见下方缺口）
- [x] PSP 尚未改动时，Node protocol 包独立测试全绿
- [ ] **真旧二进制 fixture 的四向兼容验证**（§18.7）——需 checkout `v0.0.1-beta9` 编译

### WP0 记录的两处规格缺口

- [ ] **§6 第 14 条（Host 编码后最大 128 KiB）在结构体校验器里不可达。**
      各字段上限相加远低于 128 KiB，真正要防的是"未来 agent 新增的未知字段"把原始子树撑大，
      那必须在拿到原始字节的层做：Node 发送前、Panel 解码前。
      计划：Panel 侧先用 `json.RawMessage` 取 `host` 子树量长度，再解码完整 report。
      这条随 WP5 落地并补测试，**不**写成结构体校验器里的死代码。
- [ ] `protocol/conformance/` 目前是语义契约（attachment / object_state / quota）配变异测试，
      与 §18.7 要求的跨版本 fixture 不是一回事；后者尚未开始。

## WP1 Linux collector（Passwall-Node）

依赖：WP0。分支 `kazuha/node-collector`（待建，基于 `origin/main`）

**第一片（Core 身份）已完成** → **KazuhaHub/Passwall-Node#25**

- [x] 扩 `core.Supervisor`，暴露受控子进程 handle 与预期 starttime
- [x] handle 在每个终止路径上清除（配置切换、context 取消、启动失败、自行退出）；
      已用变异测试确认：去掉 `stop()` 里的清除，生命周期测试立即失败
- [x] proc stat 解析覆盖 comm 含括号与空格、截断、非数字、零值
- [ ] 采集器侧：拿到 handle 后**重新读取** starttime 再比对，不一致即丢弃 Core section

**采集器本体（已完成字段覆盖）**

- [x] `internal/host` 包：`Collector` 接口与 Linux 实现
      分支 `kazuha/node-host-collector`（叠在 WP0 之上，并合入 #25 的 handle）
- [x] 固定 proc/sys parser（§7.2 的来源表，路径全部写死在代码里）
- [x] cgroup v1 / v2（§7.4 的解析规则）
- [x] statfs、network / TCP / socket / conntrack、process、BBR 只读
- [x] fixture 测试（§18.2 清单的大部分）
- [x] resource scope 判定（§4.3）
- [x] 采集器侧：拿到 handle 后重新读取 starttime 再比对，不一致即丢弃 Core section
      （已用变异测试确认：去掉再比对，PID 复用测试立即失败）

**实现方式上的一处主动偏离（已记录理由）**

规格 §7.1 把解析器放在 `procfs_linux.go` 等带平台标签的文件里。实际实现是
**解析逻辑为平台无关的纯函数、组装层跑在注入的根目录上**，只有 `New` 一个函数带
`//go:build linux`。理由：这样"老内核没有 MemAvailable""容器读宿主 /proc"
"cgroup v1"这些真正值得测的场景，在非 Linux 开发机上就能跑，而不是全部推给 CI。
规格 §21 允许这种偏离（"文件名是导航建议，不是线上合约；若现有包结构要求改名，
仍必须保持本节的分层职责"）。

**新发现的一处规格自相矛盾**

- [ ] **§5.10 关于 AT_CLKTCK 的那句话无法同时满足。**
      规格说"读取失败时仍可报告 RSS、FD 和 thread，但两项 CPU 字段为 nil"，
      但 `StartedAtMS` 在 §6 里是必填且必须由 boot time 与 proc starttime 推导 ——
      没有 CLKTCK 就推不出来。实现选择：CLKTCK 缺失时**整个 processes 节省略**并加
      `process.agent` token，而不是用猜的 tick 率编一个启动时间。
      需要规格明确到底是放宽 `StartedAtMS`，还是接受整节缺失。

完成判据：

- [x] 无外部命令（§7.3 禁止清单）——由源码扫描测试守住，
      并且是在**防止以后有人加进来**（运行期无法察觉）
- [x] 非 root unit 与 Docker 均可采集 —— **已在真实 Linux 上验证**，见下
- [x] 每个文件缺失都有测试（§18.2 清单中除"真实内核边界"外的项）
- [x] 100 次 fixture benchmark 无泄漏、无非线性遍历
      —— `-benchtime=100x` 实测约 0.58 ms/次，远低于 100 ms 软预算
- [x] `go test -race` 通过

## 真实 Linux 验证（2026-09-18）

本机装了 Lima（`brew install lima`），虚拟机名 **`psp-node`**：
Ubuntu 26.04、内核 7.0.0-28-generic/aarch64、cgroup v2、含 containerd+nerdctl。
`limactl shell psp-node -- <cmd>` 即可进入；`limactl stop psp-node` 停。

验证方式：本机 `GOOS=linux GOARCH=arm64 go test -c` 交叉编译出测试二进制，
拷进 VM 直接运行（VM 里不用装 Go）。门控环境变量 `PSP_HOST_LIVE=1`。

结果：

| 场景 | Deployment | ResourceScope | DataFilesystemScope |
|---|---|---|---|
| 宿主，uid 501（非 root） | `systemd` | `host` | `host_mount` |
| 容器，cap_drop ALL + 512 MiB | `docker` | `mixed` | `container_mount` |

**这一步抓出了三个真实缺陷**，全部是"画在图上看起来很合理"的那类：

1. **没有默认路由的地址族被当成失败**。纯 IPv4 主机的 IPv6 表里根本没有默认路由
   （所有全零目标项属于 loopback 且未 UP）。这会给每台单栈机器永久打上
   `network.default_route`——和"某个非默认 veth 没有 speed 不能标记整机"是同一条规则。
   token 现在只表示"无法确定"。
2. **裸机上的 tmpfs 被标成容器挂载**。`/tmp`、`/run` 在裸机上就是 tmpfs，
   映射成 `container_mount` 等于告诉面板这些数字描述的是别处——与这个字段存在的
   目的恰好相反。文件系统类型单独不足以判定：overlay 在哪都是容器层，
   而内存文件系统只有在 agent 身处容器中时才是容器的。
3. **private cgroupns 的容器探测不到运行时**。这种容器的 `/proc/self/cgroup` 是
   `0::/`，PID 1 的也一样，cgroup 路径里没有任何运行时名字。容器里的 agent
   因此把自己报成 `manual` + `host` scope——把宿主 CPU 说成容器自己的占用。
   根文件系统是 overlay 是唯一能穿透该 namespace 的信号，现在与路径标记一起检查。

交叉核对（观测值 vs 原始内核文件）：文件系统数字正好等于 mountinfo 里 `/tmp` 的
`size=1994416k` 与 `nr_inodes=1048576`；`cpuset.cpus.effective=0-3` 对应
`effective_cpus=4`；`MemTotal` 与 `/proc/meminfo` 一致。

**建议**：后续每个 WP 的验收都走一遍这个 VM。WP0 的协议层也能在这里验
（真实 HostObservation 过 validator），WP3 的 doctor 更是必须在真机上跑。

**本地环境限制**：本机是 darwin，`//go:build linux` 的部分只能用
`GOOS=linux go vet` 做编译检查；不过因为解析器是平台无关的，绝大部分断言
（含 cgroup v1、老内核、容器作用域）都在本机实际执行。

## WP2 调度与同步统计（Passwall-Node）

依赖：WP0、WP1。分支 `kazuha/node-host-collector`（与 WP1 同分支）

- [x] `HostReporter` cache 与 single-flight（§7.5 的 latch 语义）
      已用变异测试确认：让超时的调用方也释放 latch，latch 测试立即失败
- [x] `ShouldSendHost` 接线
- [x] `RuntimeStats`（§7.6）
- [x] `ReportBuilder` 注入 Host
- [x] collector 失败 Issue episode（§5.3）
- [x] `fitReportToWire` 降级顺序（§7.1）
- [x] `cmd/node` 组合接线

完成判据：

- [x] 60 秒周期在 30 秒 poll 下每两轮一次
- [x] `WantHostReport` 下一轮生效一次
- [x] POST 失败不错误推进 last sent
- [x] 重发 sample id 幂等
- [x] collector 超时不阻断 sync
- [x] capability 只在实现存在时声明
- [ ] **端到端**：真实 Node 对着真实 PSP 跑一轮，确认 Host 出现在报告里（要等 PSP 侧 WP5）

## WP3 passwall-node doctor（Passwall-Node）

依赖：WP1（复用同一套采集器，不得另写第二份）。分支 `kazuha/node-host-collector`

- [x] `doctor` / `doctor --json` 子命令，在 daemon flag 解析之前识别
- [x] §7.7 的固定 check code 全集（10 个，每个恒出现一次、按 code 排序）
- [x] data-dir 的 `O_CREATE|O_EXCL` probe（fsync → close → 立即删除，
      失败路径也删；probe 名不进输出）
- [x] SQLite 只读 `PRAGMA quick_check`（`mode=ro`，**不跑 migration**）
- [x] 退出码 0/1/2

完成判据：

- [x] 文本输出兼容（文本与 JSON 由同一个 CheckResult slice 渲染）
- [x] JSON schema 固定（stdout 只有一个 document，诊断进 stderr）
- [x] secret golden test
- [x] 非 root systemd 安装与 Docker 容器内均可运行 —— 已在真机验证
- [x] exit code 测试（0/1/2 全部覆盖，含相对路径、位置参数等用法错误）

一处值得记的判断：doctor **不启动 Core**，所以 `collector.process` 只能报
unavailable——这是规格 §7.7 的直接后果，不是缺口。

## Node 侧批次完成（WP0–WP3）

PR：**KazuhaHub/Passwall-Node#24**（WP0 协议）、**#25**（Core 进程身份）、
**#26**（WP1–WP3，叠在前两者之上）。三个都合入后本地 main 需再次 fetch 核对。

尚未开 PR 的：无。Node 侧到此为止，后续是 PSP 侧 WP4–WP9。

## WP4 Panel domain、ports、SQL（Passwall-Sub-Panel）

依赖：WP0 的协议类型。分支 `kazuha/psp-node-host-metrics`，**PR #123**

- [x] `NodeHostObservation` 及 latest/sample/interface/hourly domain
- [x] repo 接口（`NodeHostMetricRepo`，9 个方法，一个 port 覆盖四张表）
- [x] 三方言 schema + `schemaModels` 登记
- [x] 幂等写入（重试=重复、同 id 异 payload=identity conflict、迟到不覆盖）
- [x] 批量列表摘要（`LatestBatchByPanelIDs`，一次 join，无 N+1）
- [x] 删除 agent 的级联逻辑 —— 加在 `DeleteConverged` 的事务里，
      与 agent 的其他行一起删；已用变异测试确认
- [ ] app.go 接线（hourly maintenance 与 retention）
      —— **属于 WP6**（§17 把 retention 与 rollup 放在一起，清理必须在降采样之后）

完成判据：

- [x] SQLite/Postgres/MySQL 全套 repo 测试 —— 见下
- [x] duplicate sample 幂等
- [x] late sample 不覆盖 latest（变异测试确认：去掉判定即失败）
- [x] 每 agent 60 秒 throttle —— **throttle 是 service 的决定**，
      repo 只写被给到的东西；测试把这条边界固定下来
- [x] 删除 agent 不留 orphan
- [x] 大整数边界一致（MaxInt64 往返 + 负数按损坏拒绝）

### 三方言实测（2026-09-18，用第 3 节的 VM）

VM 里起 Postgres 17 与 MySQL 8 容器，交叉编译 `internal/adapters/sqlstore`
的测试二进制跑**全量**：

| 方言 | 结果 |
|---|---|
| SQLite | 全过（本机） |
| MySQL 8 | **全量零失败** |
| PostgreSQL 17 | 全量仅 1 个失败 |

那一个失败是 `TestV4BaselineDoesNotReinterpretHistoricalLimitsIdentityAndDisableState`。
**在未修改的 `origin/main` 上、同一个数据库里，它失败得一模一样**
（用 `git worktree` 检出 main 做对照实验确认）。所以**不是本计划引入的回归**。
但它值得单独查：要么仓库的 Postgres CI 用了不同版本或配置，
要么 v3.9.2 基线迁移在 Postgres 17 上确有潜伏问题。

### 依赖与前置条件

- PSP 目前依赖**未发布的 Node 修订**（伪版本
  `v0.0.1-beta9.0.20260918045153-97ace17e5f31`）以取得 WP0 的协议类型。
  **合并前必须换成正式 tag。**
- 升级立刻暴露一个真实前置条件：`nodebootstrap` 的 preflight 测试发现
  新版安装器需要 `ln`，而既有迁移脚本没覆盖它。已补。

## WP5 nodesync 接收（Passwall-Sub-Panel）

依赖：WP4。分支 `kazuha/psp-node-host-metrics`（与 WP4 同分支）

- [x] Host validator —— 分两段：wire 边界先验 Base 再验 Host
- [x] latest/history ingest
- [x] best-effort 持久化边界（`Ingest` 签名里**没有 error 可返回**）
- [x] current capability observation —— 既有机制已覆盖：
      `UpdateProtocolObservation` 每轮把 `report.Capabilities` 写进
      `node_agents.observed_capabilities`，`host.telemetry.v1` 自然流过去
- [x] refresh request 热窗（API 在 WP8）
- [x] `cloneReport` / `cloneObservationReport` 不保留 Host（显式清空，第二道防线）

完成判据：

- [x] Host DB 写失败仍返回有效 SyncResponse
- [x] malformed Host 在 SQL 前丢弃，但仍返回有效 SyncResponse
- [x] partial/full 均接收
- [x] 旧 Node 无回归（原有 nodesync 测试全过）
- [x] refresh 合并和过期测试
- [x] **§6 第 14 条（128 KiB）落地** —— 在 handler 里对**原始** host 子树量长度，
      这是唯一能看到"未来 agent 新增的未知字段"的层。WP0 记录的缺口已闭合。

一处值得记的边界：**`"host": "字符串"` 属于 JSON 类型错误，仍拒绝整个请求**。
规格说"JSON 语法或控制面契约失败仍拒绝整个请求，仅 Host 子树的语义错误隔离"，
所以隔离只适用于"能解码但验证不过"的子树。测试里写明了这条分界。

## 后续批次（尚未排期）

依赖图见规格 §17。WP8 之前不得跳到前端。

- [ ] WP4 Panel domain / ports / SQL（依赖 WP0 发布 module）
- [ ] WP5 nodesync 接收与故障隔离
- [ ] WP6 派生与 rollup
- [ ] WP7 健康与 alert
- [ ] WP8 管理 API
- [ ] WP9 前端
- [ ] WP10 远程诊断（第二阶段）

## 必须由人工/真实环境完成的事项

这些不是"实现完就算完"的项，列在这里以免被误判为已完成。

- [ ] **发布 Node module revision/tag**，再 bump PSP `go.mod`
      （PSP 当前 pin 的是 `v0.0.1-beta5`，与基准 tag `beta9` 之间差 4 个版本）
- [ ] §20 场景 B/C 的真实 systemd / Docker 非 root 实测
- [ ] §23 DoD 的"1 台 systemd + 1 台 Docker 运行 7 天"
- [ ] PostgreSQL / MySQL 全套 repo 测试（本地跑不全，依赖 CI）
- [ ] 数据库增长、wire 大小、collector P95 耗时的实测记录

## 与本计划的已知偏差与遗留

- Node 工作树上的 `README.zh-CN.md` 是未跟踪文件，属于独立的 README 中文化工作，
  **不在本计划范围内**，无需处理。
