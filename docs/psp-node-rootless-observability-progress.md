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

- [ ] **§5.10 依赖的 Core 子进程 handle 并不存在。**
      `internal/core/core.go:87` 的 `Supervisor` 接口只有 `Run/Apply/Deploy/Status`，
      `*exec.Cmd` 被封在 `process.managedProcess` 内部，没有任何对外暴露 PID 的路径。
      WP1 需要先扩接口（xray 与 singbox 两个实现都要动），
      并保证暴露的是"受控 handle + 预期 starttime"而非任意 PID。

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

依赖：WP0

- [ ] 固定 proc/sys parser（§7.2 的来源表，路径全部写死在代码里）
- [ ] cgroup v1 / v2（§7.4 的解析规则）
- [ ] statfs、network / TCP / socket / process、BBR 只读
- [ ] fixture 测试（§18.2 清单）
- [ ] resource scope 判定（§4.3）
- [ ] **先扩 `core.Supervisor` 接口**，暴露受控子进程 handle 与预期 starttime
      （见上方"实现前需要补的规格缺口"）

完成判据：

- [ ] 无外部命令（§7.3 禁止清单）
- [ ] 非 root unit 与 Docker 均可采集
- [ ] 每个文件缺失都有测试
- [ ] 100 次 fixture benchmark 无泄漏、无非线性遍历
- [ ] `go test -race` 通过

## WP2 调度与同步统计（Passwall-Node）

依赖：WP0、WP1

- [ ] `HostReporter` cache 与 single-flight（§7.5 的 latch 语义）
- [ ] `ShouldSendHost` 接线
- [ ] `RuntimeStats`（§7.6）
- [ ] `ReportBuilder` 注入 Host
- [ ] collector 失败 Issue episode（§5.3）
- [ ] `fitReportToWire` 降级顺序：先带 Host → 超限则移除 Host 重试 → 仍超限才走 outbox partial（§7.1）

完成判据：

- [ ] 60 秒周期在 30 秒 poll 下每两轮一次
- [ ] `WantHostReport` 下一轮生效一次
- [ ] POST 失败不错误推进 last sent
- [ ] 重发 sample id 幂等
- [ ] collector 超时不阻断 sync
- [ ] capability 只在实现存在时声明

## WP3 passwall-node doctor（Passwall-Node）

依赖：WP1（复用同一套采集器，不得另写第二份）

- [ ] `doctor` / `doctor --json` 子命令，在 daemon flag 解析之前识别
- [ ] §7.7 的固定 check code 全集
- [ ] data-dir 的 `O_CREATE|O_EXCL` probe
- [ ] SQLite 只读 `PRAGMA quick_check`
- [ ] 退出码 0/1/2

完成判据：

- [ ] 文本输出兼容
- [ ] JSON schema 固定
- [ ] secret golden test
- [ ] 非 root systemd 安装与 Docker 容器内均可运行
- [ ] exit code 测试

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
