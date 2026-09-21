# PSP／PN／第三方后端兼容体系整改实施手册

版本：1；日期：2026-09-18；状态：待实施。本文是工程交接单，不是已通过的兼容认证。

配套规范：[兼容、升级与 CI 规范](compat-policy.md)。发生设计冲突时先更新规范和对应 ADR，不能由实现者静默扩大支持范围。

## 0. 接手说明与边界

本次核对基线为 PSP 分支 `kazuha/node-compat-policy`，commit `4f115bc3`；PN 为 `60d96490`。执行者必须重新记录实际完整 SHA，核对差异后再开工。后续源码发生变化时，以源码和已执行的测试为事实依据。

仓库分工：PSP 负责整体兼容政策、第三方适配、测试矩阵与控制端准入；PN 负责节点协议、安装、状态存储和升级助手。跨仓任务分别提交 PR，在描述中关联对方的精确 commit。不得让两个 CI 相互跟随浮动 main。

本手册中的“现有”路径在基线中存在；标为“新增”的文件、命令和接口是实施目标，交接时尚不可直接调用。全部任务初始为待实施；文档落库不等于任务完成。

### 0.1 已完成，禁止重复重做

- PSP 两条 workflow 已从 `docs/compat/passwall-node-v4.json` 的 `min_supported` 和有序 `released_nodes` 派生 PN 集合。
- `min_supported` 当前是 `v0.0.1-beta1`，保留 beta1–beta11，不缩减已有支持。
- PN 已有协议 v1、可选能力、升级任务能力检查；保留现有双重准入。
- PN 已有升级助手、同 schema／升级 contract 限制及机制级恢复测试；新增历史测试不能删除这些测试。
- PSP 已有第三方 live tests、动态兼容范围和上游版本 watcher；优先复用。

### 0.2 不得误读的现状

1. `min_supported` 改变 CI 集合，不会自动成为运行时旧 PN 拒绝器。
2. 新 PSP 与旧 PN 的真实协议测试使用确定性核心替身，不证明完整认证安装或真实代理握手。
3. PN 升级 E2E 将同一份源码编译成不同版本身份，证明机制，不证明历史代码间的数据兼容。
4. 新 PN 与旧 PSP 有人工观测记录，不等于基础 profile 全部通过，也不是已存在的持续 CI。
5. 上游 watcher 发现版本超出上限，不会证明新版可用。

### 0.3 开工步骤

1. PSP 在指定分支继续；新的实施分支按仓库规则从最新 `origin/main` 建立，并明确依赖本政策 PR，或按维护者指定接续本分支。
2. 执行 `git fetch origin main --prune`、`git status --short --branch`、`git rev-list --left-right --count HEAD...origin/main`，保存结果。
3. PN 单独检查工作树，保留已有未提交文件，不在 main 上编码。
4. 阅读两个仓库适用的 AGENTS.md、PSP ADR 0032/0033/0035、PN `internal/upgrade/ACCEPTANCE.md`。
5. 所有写入后端的 live tests 只使用本次创建的隔离实例；生产地址、生产凭据和生产数据不得传入。

## 1. 任务拆分与交付顺序

| ID | 优先级 | 归属 | 交付内容 | 前置 |
| --- | --- | --- | --- | --- |
| R01 | P0 | PSP | 明确支持边界与现有证据标签 | 无 |
| R02 | P0 | PSP，可供 PN 复用 | 预期测试集合与 Go JSON 结果校验 | 无 |
| R03 | P0 | PSP | 汇总门、发布依赖、完整报告留存 | R02 |
| R04 | P1 | 两仓 | 固定实现身份的矩阵与 case planner | R01 |
| R05 | P1 | PN＋PSP | 有界反方向基础 profile 测试 | R02、R04 |
| R06 | P1 | PN | 真实历史发布物升级、失败恢复与回退 | R02、R04 |
| R07 | P1 | PSP | 真实第三方面板隔离测试 | R02、R04 |
| R08 | P1 | 两仓 | 订阅到真实代理的业务链路验收 | R05、R07 |
| R09 | P2 | PSP | 统一运行兼容判断、操作资格与清单缓存 | R01、R04 |
| R10 | P2 | 两仓 | 最终发布物验收和渠道推广 | R03、R05–R08 |
| R11 | P2 | PSP | 维护 SOP、支持退出与交接收尾 | R09、R10 |

推荐 PR 顺序：R01；R02；R03；R04；R05；R06；R07；R08；R09；R10；R11。R05/R06/R07 可由不同工程师在 R04 后独立推进，但不得同时修改同一 workflow 而不协调。

每个 PR 都必须给出：任务 ID、源基线、改动文件、行为变化、测试命令与结果、失败注入证据、尚未覆盖范围。禁止只写“所有测试通过”。

## 2. R01：支持边界和证据命名

现有入口：`docs/compat-policy.md`、`docs/compat/passwall-node-v4.json`、`docs/compat/3x-ui-v4.json`、`internal/version/node_compat_matrix_test.go`、`internal/version/compat.go`、`internal/version/compat_sui.go`。

实施步骤：

1. 搜索所有 `min_supported`、`supported`、`too_old` 文案，逐项标明它是政策承诺、测试选择还是运行时判断。
2. 保留有序发行列表的定位语义，禁止用字符串／普通 SemVer 排序重排 `beta1…beta11`。
3. 修订字段说明，明确移动 floor 不会自动终止旧 PN 连接；退出协议需要独立变更、公告和迁移验收。
4. 对现有证据增加类型：`wire-contract`、`adapter-live`、`dataplane`、`upgrade-mechanism`、`historical-upgrade`、`artifact-runtime`。这是新报告分类，不直接重命名现有 API 状态枚举。
5. 把历史人工实测保留为有限范围证据，不推导为完整 profile 支持。
6. 保留显式支持的 beta；不采用“预发布版本全部无保证”的笼统文案。

验收：改动前后当前支持 PN 集合完全一致；所有描述能区分最低测试版本和协议拒绝边界；未新增未经验证的第三方范围或反向承诺。

失败场景：floor 不存在、重复版本、空集合、不认识的状态必须报错；有序列表不能因比较器变化而倒置。R04 可承担新增校验代码，R01 不必重复实现。

## 3. R02：让“没测到”不能绿灯

现有入口：PSP `.github/workflows/test.yml` 的 `node-compatibility`、`node-contract`；`.github/workflows/release.yml` 的 `node-compatibility`；`internal/service/nodesync/*live_test.go`。

新增建议：`deploy/compat/check-go-results.mjs`、`deploy/compat/check-go-results.test.mjs`、`deploy/compat/profiles.json`。采用 Node 标准库即可，不引入服务或数据库；若选其他语言，CLI 契约和验收必须保持一致。

### 3.1 首批必须运行的测试

包：`github.com/KazuhaHub/passwall-sub-panel/internal/service/nodesync`。

- `TestLive_RealNodeAgentContract`
- `TestLive_RealNodeMigratedServerContract`
- `TestLive_RealNodeTaskEvidenceReceipt`
- `TestLive_RealNodeTaskExpiryContract`

实施前用源码声明核对名称，不根据上面的推测静默删除找不到的项；名称不一致必须修正文档与 profile。现有 workflow 的正则为 `^TestLive_RealNode(AgentContract|MigratedServerContract|TaskEvidenceReceipt|TaskExpiryContract)$`。

### 3.2 目标 CLI（新增，实施后才可运行）

```bash
node deploy/compat/check-go-results.mjs \
  --profile node-wire-v1 \
  --input evidence/go-events.jsonl \
  --go-exit-code "$test_exit" \
  --output evidence/result.json
```

程序必须：

1. 从提交中固定的 profile 读取预期 `(Package, Test)` 集合，不能从实际日志反推预期。
2. 解析 `go test -json` 的逐行事件；区分包事件、顶层测试和子测试，不能把包 PASS 当作目标测试 PASS。
3. 每个必需测试必须实际运行且最终 PASS；FAIL、SKIP、缺失、截断、非法 JSON、进程非零都拒绝。
4. 子测试失败不能因父测试或其他包 PASS 被覆盖；未知的额外 PASS 不替代缺失项。
5. 记录未预期测试和子测试，明确它们是否属于当前 profile；必须子场景应单独列入 profile，避免只校验父测试而漏掉条件跳过。
6. 只有矩阵预先声明的“不适用”允许不运行，报告中记原因；不适用项不能计入通过覆盖。
7. 生成结构化结果，即使校验失败也尽可能写出缺失／失败清单；退出码 0 仅代表完整通过。

### 3.3 shell 接入要求

使用 `set -euo pipefail`。运行 Go 测试时局部保存退出码，不能让失败退出导致报告校验器根本不运行。最简单方式是先重定向，再统一校验，不依赖 tee 管道的最后一个退出码：

```bash
mkdir -p evidence
test_exit=0
GOWORK=off go test -json -count=1 -timeout=5m \
  ./internal/service/nodesync \
  -run '^TestLive_RealNode(AgentContract|MigratedServerContract|TaskEvidenceReceipt|TaskExpiryContract)$' \
  > evidence/go-events.jsonl 2> evidence/go-stderr.txt || test_exit=$?
# 接着调用新增校验器；PSP_LIVE_NODE_REPO 必须已指向隔离的固定旧源码。
```

每个后端版本用独立 evidence 目录，禁止循环覆盖上一个版本的结果。stderr 单独保存，不混进 JSON 流。

### 3.4 校验器必须有的负向测试

| 输入 | 必须结果 |
| --- | --- |
| 四个目标均 RUN→PASS，包 PASS，进程 0 | 通过 |
| 日志空／只有包 PASS／正则匹配零测试 | 拒绝并列缺失项 |
| 一个目标 SKIP，其他 PASS | 拒绝 |
| 某必需子测试 SKIP | 拒绝 |
| JSON 流截断、非 JSON 内容或读取失败 | 拒绝 |
| 测试 PASS 但 Go 进程非零 | 拒绝 |
| 同名测试出现在错误包 | 不抵消目标缺失 |
| 先 FAIL 后 PASS／冲突终态 | 拒绝 |
| 测试进程被杀、没有终态 | 拒绝 |
| 矩阵事前批准的不适用项 | 明示 N/A，不计为 PASS |

完成标准：故意移除 `PSP_LIVE_NODE_REPO`、故意改错正则、故意截断日志都使专用 CI 变红；原始有效组合保持通过。通用单元测试中的合法 skip 不应被误施加这套专用 profile 规则。

## 4. R03：汇总门、发布依赖与报告

现有入口：PSP 两条 workflow、`deploy/release_workflow_test.mjs`、`internal/version` 中 workflow 相关测试。当前 release 的发布任务已依赖 node-compatibility，保留并扩展，不能改成只在普通 test workflow 里跑。

实施步骤：

1. 每个 case 上传结构化结果、Go JSON、stderr、双方 SHA 和环境身份；失败也上传，通过也上传。
2. 新增固定名称汇总 job（建议 `compatibility gate`），使用 always 条件，明确检查前置 job 的 success／failure／cancelled／skipped；不能让取消变成绿灯。
3. 汇总器根据 planner 的预期 case ID 检查报告集合，不仅检查已下载报告；缺一个 artifact 就未通过。
4. 仓库分支保护必须要求实际产出的固定 check 名称。修改名称前读当前规则，记录原值；不能只改 YAML 后假定保护已更新。
5. release 中真正发布归档、镜像和渠道标签的 job 都依赖汇总门；画出 needs 关系，逐一排除旁路。
6. 测试门失败时只禁止发布候选，不改现有发行物、不下架已有部署。

验收：取消一个矩阵 leg、删一个报告、让 planner 失败、让上传失败，汇总都不得通过；release 不得启动对应发布动作。涉及 GitHub 规则配置的变更由有权限者完成并提供规则截图或 API 输出，不能仅靠本地测试确认。

报告至少保留整个支持周期。Actions artifact 可用于调试，但其过期时间不能成为兼容承诺唯一证据；正式证据随版本发布为不可变附件或进入专用证据存储。不要把 token、完整订阅凭据、数据库密码放进日志。

## 5. R04：固定身份的矩阵与 planner

现有入口：`docs/compat/passwall-node-v4.json`、`internal/version/node_compat_matrix_test.go`、两条 workflow 中的 Python 切片。

新增建议：`deploy/compat/plan.mjs` 与对应测试；`docs/compat/verification-v1.json` 保存 CI 组合和证据引用。第一阶段保留现有运行时 JSON 格式，只增加旁侧验证清单，避免旧 PSP 读取新 schema 失效。

### 5.1 规划数据

每个 case 必须包含：唯一 case ID、direction、profile、required 或 observation、双方仓库和完整 commit、release tag（存在时）、发布物摘要（测试发布物时）、OS/arch、安装方式、核心与客户端身份、预期测试集合引用。

- `required` 失败阻断该承诺的发布；`observation` 失败单独上报，不自动变更支持。
- tag 只作可读标签。首次解析后固定 SHA，执行时核对；浮动标签变化必须报错，不能静默接受另一份源码。
- 候选版本使用本次 workflow 的 SHA 和本次构建摘要；禁止提交伪造摘要占位。
- 模型区分运行组合与升级边：升级边额外带 from/to、schema、安装方式、回退资格及前提。
- 旧 PN 集合仍从现有有序 manifest 派生，不在新文件再手写第二份支持列表。新文件只补身份和 profile 元数据，缺少元数据时规划失败。

### 5.2 planner 验收

必须拒绝重复 ID、缺 SHA、未知 profile、无源目标的升级边、空 required 集合、未解析 floor、版本顺序异常、required case 缺测试和引用不存在证据。

输出同时供 GitHub matrix 和本地执行使用，保证同一个修订产生同一集合。不要把字符串插入 shell eval；以结构化参数或受校验的文件传递。

当前 beta1–beta11 的输出集合与改造前逐项相等；测试用专门样例证明 beta9 不会排到 beta11 后。声明已知坏版本时用显式排除和原因，不靠范围两端掩盖中间故障。

### 5.3 清单迁移

先上线读取旧 manifest 的 planner，再替换两条 workflow 的内联切片。仅在所有消费者识别新格式后才迁移运行时 schema；迁移期间保留旧字段和保守范围。向后兼容测试必须用旧解析器验证新增可选字段，不以新解析器测试自己代替。

## 6. R05：新 PN 对旧 PSP 的基础 profile

现有 PSP 入口：`internal/service/nodesync/contract_live_test.go`、`live_fixture_test.go`、`task_receipt_live_test.go`、`task_expiry_live_test.go`、`internal/transport/http/handler/node_sync.go`。

现有 PN 入口：`protocol/compatibility.go`、`protocol/report.go`、`.github/workflows/test.yml`。新增建议：PN `deployment/compat/` 中的隔离 runner，以及单独的兼容 workflow。

### 6.1 选择旧 PSP

取当前维护线最近已发布 PSP，以及该线最后一个尚未理解候选协议扩展的 PSP；若重复只保留一次。核对 tag 对应源代码确实不含新字段处理。`v4.0.0-beta.19` 是历史 host 上报观测样例，不能无限期固定为所有变化的唯一旧版本。

将这些组合先标为 observation，运行完整 profile；达到验收后通过政策 PR 转 required。已有升级／回退承诺依赖的组合从开始就是硬门，不能用 observation 降低既有保证。

### 6.2 运行方式

旧 PSP 必须从它自己的精确源码／发布物运行，使用其自己的依赖，不用新 PSP 服务配上旧版本字符串。新 PN 使用候选源码／发布物。

旧 PSP 旧测试夹具不一定能调用新 PN 的内部函数：只允许调整外部驱动或测试入口，不准修改旧 PSP 的解析、协调、存储等生产实现。夹具适配 diff 必须随报告保留；生产补丁结果单列为 patched，不计官方历史兼容。

进程测试使用临时 DB 和配置，通过实际接口注册、认证和同步。既有确定性核心协议测试继续保留；完整认证和真实核心覆盖分别记录，不混成一个 PASS。

### 6.3 必须覆盖的场景

| ID | 行为 | 判定 |
| --- | --- | --- |
| B01 | 首次认证、拉取配置、应用并上报 | 正确身份、内容收敛，不只 heartbeat 200 |
| B02 | 用户新增、凭据更新、禁用、到期、限额 | 状态正确；实际访问拒绝由 R08 补证据 |
| B03 | 新 PN 带旧 PSP 不认识的观测字段 | 旧 PSP 不崩溃、不拒绝原有合法报告，字段可丢弃 |
| B04 | 缺少旧字段、零值和默认行为 | 遵循既有 wire 契约；不把缺省变为无限授权 |
| B05 | 能力撤回／助手停用 | 停止新升级任务准入／下发；基础同步继续 |
| B06 | 未知 task kind、过期任务、重复结果 | 未知任务不执行；过期不启动；重放不重复副作用 |
| B07 | 控制端短暂失联、双方重启 | 最后有效配置保留、限制继续执行、重连收敛 |
| B08 | 协议不兼容 | 明确拒绝并保留诊断；不循环下发不可执行配置 |

若旧版本缺少某可选任务功能，profile 应明确 N/A，同时验证不下发该任务；不能因此跳过基础同步。反方向完整支持只有在其必需业务和 R08 对应链路证据满足后才对外声明。

验收：候选 PN 的 required 旧 PSP 集合全部通过；故意让新 PN 必须依赖新 PSP 字段时，旧 PSP case 必须失败。支持范围和测试选择来自同一政策修订。

## 7. R06：真实历史升级与回退

PN 现有入口：`internal/upgrade/systemd_e2e_test.go`、`helper_linux.go`、`docker_helper.go`、`release.go`、`ACCEPTANCE.md`，以及 `.github/workflows/test.yml` 的升级步骤。PSP 数据入口：`internal/adapters/sqlstore/v3_v4_upgrade_test.go`、`schema_upgrade_v4.go`。

### 7.1 保留现有测试，新增独立历史套件

保留同源码多版本身份的机制测试，命名和报告标注 `upgrade-mechanism`。新增 `historical-upgrade` 套件读取明确 from/to 发行物，不覆盖原测试。

历史源至少覆盖：当前维护线最近 release、仍支持的最老升级起点、最近一次 schema／安装契约边界。无法跨越的边声明人工或桥接路径；基础同步支持不代表每个老 beta 都支持自动升级。

### 7.2 每条升级边的执行步骤

1. 获取并验证精确旧发布物、候选发布物及摘要；官方签名验收与内部测试签名分开，不能削弱生产信任根。
2. 用旧程序首次启动，真实创建身份、配置、用户和状态；不直接用候选 ORM 构造“旧数据库”。
3. 通过旧程序产生非零流量计数和任务执行证据，保存可比较快照。只截取语义数据，不依赖 SQLite 文件字节一致。
4. 经实际升级入口执行目标安装；systemd 与 managed Docker 独立 case，不能互相代替。
5. 验证目标版本与摘要、身份不变、配置收敛、限制保留、数据可读、任务无重复副作用。
6. 再次重启验证持久性；验证旧任务证据重放不会重新执行副作用。
7. 若声明可回退，在候选已写入合法新数据后执行回退，再验证旧程序读取和服务，而不只在新程序尚未启动时恢复文件。
8. 保留全过程日志、前后语义快照、安装方式及最终终态。

### 7.3 故障注入矩阵

| 点位 | 预期 |
| --- | --- |
| 下载失败／摘要或签名不匹配 | 旧服务继续；未启动不可信文件 |
| 准备完成后授权过期 | 不开始替换；不能重试续期授权 |
| 目标启动失败／认证失败／配置不能收敛 | 已验证回退或进入明确人工恢复状态 |
| 切换后助手崩溃、宿主重启 | journal 恢复可判定，无重复升级 |
| 目标已成功但结果回传丢失 | 查询／重放恢复证据，不重新执行升级 |
| 并发请求，源版本已变化 | 串行／CAS 拒绝，不覆盖新状态 |
| 目标 schema 或 upgrade contract 不兼容 | 停止自动路径，提示手工／桥接方案 |
| 回退也失败 | 明确 recovery-required，不报告成功 |

跨 schema 不默认允许自动降级。若靠数据备份恢复，明确恢复点、允许的数据丢失窗口和任务 fencing；不能恢复旧 queued 记录后重新授权已执行任务。遵守 ADR 0032，不在此项目中暗中扩张恢复保证。

### 7.4 平台与数据库

PN systemd 验收在原生 Linux runner，按声明的 amd64/arm64 覆盖。容器安装必须验证实际 updater／持久卷／旧容器保留流程。PSP schema 变更分别验证支持的 SQLite、MySQL、PostgreSQL；没有相关变更的链路测试无需再乘全部方言。

完成标准：每条自动升级／回退声明都有精确源目标报告；“同 schema”只作为准入条件，不作为唯一回退证据。

## 8. R07：第三方后端隔离 CI

PSP 现有入口：`internal/adapters/xui/client_live_test.go`、`client_live_surface_test.go`、`client_live_limits_test.go`、`client_live_floor_matrix_test.go`；S-UI 为 `internal/adapters/sui/client_live_test.go`。

新增建议：`deploy/compat/backends/`，包含明确版本的启动、初始化、就绪、取测试凭据、采集日志、销毁脚本。初始化逻辑按旧版本 API 分支记录，不修改受测面板源码来绕过兼容问题。

### 8.1 环境契约

- 每个 case 独立数据库、容器名、网络和数据目录，服务只绑定测试 loopback 或隔离网络。
- 固定上游源 SHA／镜像摘要，同时记录运行中的面板版本及实际内核版本。
- 启动后轮询真实认证 API，使用有界 deadline；不能固定 sleep 后假设已就绪。
- 动态分配端口；cleanup 无论成功失败都执行。销毁只针对本 case 资源，禁止全局 prune。
- 日志脱敏；临时密码可随机产生，但不得输出到公共 artifact。
- 禁止把已有生产数据库挂载给测试。Traffic floor 测试的 DB 路径必须指向本 case 临时 DB。

### 8.2 已有测试调用入口

以下命令本身已可用，但必须先由隔离启动器设置环境，缺变量会 skip，因此必须接 R02 校验器。

```bash
# 3X-UI：隔离启动器设置 PSP_LIVE_XUI_URL、PSP_LIVE_XUI_TOKEN。
go test -json -count=1 -timeout=10m ./internal/adapters/xui \
  -run '^TestLive_(XUISurface|MultiInboundClientSurface|SharedClientMigrationFlow|BulkDelPreservesSharedClient|ConcurrentSameClientNoCorruption|TwoClientsSameBackendNoCorruption)$'

# S-UI：设置 PSP_LIVE_SUI_URL、PSP_LIVE_SUI_TOKEN。
go test -json -count=1 -timeout=10m ./internal/adapters/sui \
  -run '^TestLive_SUISurface$'
```

额外 profile 覆盖 connection limits、traffic floor、REALITY scan、cookie/CSRF 和 fail2ban 等已声明功能。没有能力的后端标明不适用，并验证对应操作被拒绝，不可直接全部 skip。

`PSP_LIVE_XUI_NO_GITHUB` 仅用于现有测试允许隔离外网更新检查的场景，报告注明；不得把该环境下的结果当作上游升级下载功能已通过。查看源码确认每个开关影响范围。

### 8.3 功能断言

至少覆盖创建→读回→更新→再读回→删除；用户共享多入站；删除单入站不误删共享客户端；重复写幂等；并发修改不丢字段；禁用／到期／额度单位与边界；非托管资源保持不变。

S-UI 特别覆盖：创建携带 links、秒与毫秒转换、editbulk 前补全 config/links、凭据不被清空、不支持入站 enable 时明确拒绝。3X-UI 特别覆盖：token 与已声明 cookie 模式、API 失败信封、共享 client 生命周期、入站更新保留上游字段。

初始矩阵使用现有声明范围的具体已验证点、实现最低版本及已知回归边界。只有边界实测时报告为抽样，不声称整个范围每版均测过。S-UI 当前未确立最低版本，先保留已验证精确版本，不猜测下限。

完成标准：至少一个 3X-UI 和一个 S-UI 固定发行物在无人工登录、无生产 secret 的 CI 全自动执行；所有正式 required profile 有完整结果；故意破坏认证或读写结构时 gate 失败。

## 9. R08：真实代理链路

PSP 入口：`internal/service/render/`、用户／流量服务；PN 可复用 `deployment/coreacceptance/` 和 `.github/workflows/core-acceptance.yml` 的真实核心环境，但先阅读其 README，确认没有把“核心单测”误当成 PSP 订阅闭环。

新增建议：独立链路 runner，不把高成本进程测试混入普通单元测试。用本地 HTTP／TCP／UDP 目标服务与固定内容，记录收到的请求 ID 和字节数。

每个 case 包含 PSP 身份、后端身份、核心版本、客户端版本、协议、传输、安全层、规则 profile、平台。客户端版本必须固定；订阅由 PSP 正常输出，不手写一份正确配置替代受测渲染。

执行：创建合法用户和节点→取得订阅→由真实客户端解析并加载→经代理请求目标→检查响应和目标日志→施加限制→确认新的访问被拒绝→恢复允许状态→确认重新可用。

必需场景：

- 凭据正确可连接，错误凭据被拒绝；合法连接作为负向测试的环境对照。
- 用户禁用、到期、额度耗尽后，在既有业务定义的生效时限内阻止访问；时限必须写入 profile，不能无限重试。
- 重启前后计量无重复；共享 client 多入站不重复计量；first observation 的基线按服务语义验证。
- 配置不变时不无故重启核心；配置变更后的 applied 证据对应实际目标内容。
- 核心随第三方面板升级后，重新检验关键 TLS／REALITY 组合。
- 只支持一部分客户端的组合明确限定，不把 unsupported 节点静默省略当作整条链路通过。

计量断言先定义“代理 payload”或“核心上报字节”的口径与容差；禁止拿 HTTP body 字节数直接要求 TLS／代理开销完全相等。拒绝连接必须区分认证拒绝与测试目标宕机，目标健康检查和允许用户对照必须成功。

完成标准：报告有实际流量和实际拒绝证据；关闭真实核心、改错渲染凭据、移除限制执行任一项都能使对应 case 失败。

## 10. R09：运行时判断与操作资格

现有入口：`internal/version/compat*.go`、`internal/ports/xui.go`、`internal/service/nodeagentupgrade/upgrade.go`、`internal/transport/http/handler/admin_servers.go`、`node_agent_upgrade.go`，以及前端服务器 API／能力展示。

实施顺序：

1. 先建立纯函数决策模型，输入本地实现约束、经过认证的远端观察、观察时间、适用政策与请求操作；输出运行结论、操作是否允许、稳定 reason code、证据修订。
2. 区分 protocol-incompatible、capability-missing、unverified、observation-stale、upgrade-edge-missing。前端显示人话，API 保留机器原因。
3. 保留现有 wire 映射和能力撤回；生产适配器必须声明能力。修复缺 CapabilityProvider 默认全支持时仅影响生产注册，避免不必要破坏历史测试替身。
4. 普通读取、基础同步、配置写入、远程升级分别决定资格；未验证不自动等于停服。
5. 新高风险操作要求执行时重新校验，明确超时常量／设置。不得以旧快照永久授权。
6. 清单具备内置基线和最后有效缓存，校验格式、适用版本、来源与修订；失败保持原有效值并展示过期。
7. 旧 API 字段先保持兼容，新字段可选加入；旧客户端读新响应、新客户端遇旧响应都测试。

必需测试表：无观察；新鲜观察；观察过期；能力被撤回；协议代际未知；manifest 断网／损坏／未知 schema／回退；hard floor 与动态 floor 冲突；已知坏版本例外；不适用的 PSP 范围；相同请求重新评估；force 不得绕过硬协议／身份／签名限制。

缓存不可用不能凭空 supported，也不能单因远端 JSON 失败清空存量代理配置。若未验证组合允许人工覆盖，仅覆盖证据不足；必须有操作审计，不能成为签名和任务授权旁路。

完成标准：API、任务创建、任务实际下发与 UI 使用一致决策来源；不能前端允许、服务端根据另一份表执行。所有涉及权限和高风险行为的判断在后端执行。

## 11. R10：最终发布物与发布顺序

现有入口：两仓 `.github/workflows/release.yml`；PSP `deploy/check-build.sh`、`check-image.mjs`、`release_workflow_test.mjs`；PN `deployment/check-build.sh`、签名发布工具和 acceptance workflows。

目标发布路径：精确候选源码→构建候选归档／镜像→生成摘要→对该候选执行适用门禁→生成证据索引→发布／推广同一摘要→更新支持政策。不得验收后重新构建另一份发布物再发布。

实施步骤：

1. 梳理 release needs 图，列出所有真正写 release asset、镜像和渠道的 job。
2. 构建一次，测试读取这一批候选；若必须重建，重新验收并更新摘要。
3. 跨平台编译不替代运行验收。发布所声明运行支持的关键平台需原生或明确标注的运行证据；模拟执行不能冒称原生。
4. 把完整 case manifest、结果、源 SHA、归档和镜像 digest 建成证据索引。
5. 签名验证与候选测试信任链明确分离；不要为私有候选修改生产代码以信任任意发布源。
6. 只有 required cases 完整通过且无缺失 artifact 时才能推广；observation 独立展示。
7. 渠道 latest/beta 遵守仓库原有语义；未通过候选不能成为自动升级目标。
8. 升级推荐必须匹配当前组合到目标组合的已验证边，不仅检查目标版本处在范围内。

验收故障：混入另一 commit 的报告；上传不同摘要归档；缺一个架构结果；候选失败；取消任一 required job；空矩阵；只有 observation 通过。以上均不得发布对应支持声明或自动升级候选。

不要为了让 CI 通过删除现有可信发布保护、缓存隔离或镜像不可覆盖检查。新增发布门需要更新已有 workflow 保护测试，覆盖真实依赖关系而不只断言文件出现某个字符串。

## 12. R11：维护 SOP 与支持退出

每次发现上游新版：记录身份→建立 observation case→运行适配器及受影响链路→修复或记录失败→生成证据→政策 PR 审核→转 required／更新支持范围。监测失败、认证失败、上游新版、行为不兼容分开报告。

每次移动 floor：列出受影响版本及安装数量（可获得时）、最后受支持 PSP、桥接路径、是否仍接受 wire、停止测试日期和真正移除协议日期；保留旧证据。新 floor 不是顺便优化 CI 耗时的手段。

弃用期限按正式政策执行；尚未正式确定期限时在 PR 明示待决策，不能默认已经获得 90 天后断连的授权。安全紧急例外独立记录。

维护者每个发布周期检查：required 集合非空、报告可下载、固定 SHA 仍可获取、支持范围没有超过证据、旧客户端解析清单不受损、过期 observation 有人处理。

## 13. 本地验证命令与环境

以下是现有命令，按改动范围运行。Go 使用仓库 go.mod 指定工具链；`GOWORK=off` 避免本地替换掩盖跨仓问题。命令成功但 live test skip 不能算兼容通过。

```bash
# PSP：矩阵、版本及相关准入回归。
GOWORK=off go test -count=1 ./internal/version
GOWORK=off go test -count=1 ./internal/service/nodesync ./internal/service/nodeagentupgrade
node --test deploy/release_workflow_test.mjs

# PN 仓库：协议和升级机制回归；平台专用 acceptance 另见其 workflow。
GOWORK=off go test -count=1 ./protocol ./internal/upgrade
```

新增校验器与 planner 落地后，将其单元测试接入普通 CI；具体命令写入脚本 README。专用 systemd acceptance 必须按 PN workflow 设置环境并在隔离 Linux runner 执行，不能在开发机直接启动宿主 systemd 测试。

若需构建 PSP 可运行程序，先 `web-react` 中 `npm ci && npm run build`，再 Go build，保证嵌入的是本次前端产物。Go 只读单测无需为此提前构建 SPA。

## 14. 每个任务的交付格式

在 PR 描述使用以下模板：

```text
任务：Rxx
基线：PSP 完整 SHA；PN 完整 SHA；政策修订
问题：什么输入会产生什么错误／缺失保证
变更：修改了哪些判断、测试或 workflow
支持集合变化：无／列出新增与退出组合
验证：命令、运行环境、CI URL、结构化证据 ID
负向验证：注入什么故障，哪个门如何拒绝
范围限制：尚未覆盖的 profile、平台、安装方式
部署／回退：是否改变数据或运行时行为，如何恢复
跨仓依赖：先合并哪一个精确 PR／commit
```

评审者必须按任务验收表检查实际证据，不以作者勾选完成为唯一依据。代码、测试、运行结果和规范相矛盾时不合并；可以拆分实施，但必须保持状态为部分完成并列出余项。

## 15. 最终验收与交接清单

- [ ] R01：floor 语义、证据类别明确，既有 PN 支持集合不缩减。
- [ ] R02：漏测、skip、零匹配、非零退出和截断日志均被拒绝。
- [ ] R03：汇总 check 已接入分支保护及所有适用发布任务，取消和缺报告不通过。
- [ ] R04：固定身份、单一派生集合、本地与 CI 计划一致。
- [ ] R05：支持清单内旧 PSP 组合有完整基础 profile 证据。
- [ ] R06：每条声明升级／回退边用真实历史发布物及历史数据验证。
- [ ] R07：第三方面板可自动隔离启动，读写语义和资源所有权有证据。
- [ ] R08：真实订阅、核心、客户端和访问限制闭环通过。
- [ ] R09：运行状态、能力和操作资格统一决策，缓存异常有明确行为。
- [ ] R10：最终被发布的摘要与被验收摘要完全一致，无发布旁路。
- [ ] R11：支持退出、上游认证和证据留存 SOP 已交接。

最终交接包必须包含：合并 PR 清单、两仓最终 SHA、机器可读支持政策、完整 required case 清单、通过与失败注入证据、已知限制、运行时界面／API 示例、发布 needs 图、分支保护设置、下一位维护者操作命令。

**本手册交付时上述任务均未据此执行。禁止把这些复选框当成已完成记录；完成一项必须补相应 CI 和发布物证据。**
