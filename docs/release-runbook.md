# 发布运行手册

版本：2；日期：2026-11-03。**编号与渠道规则以 [`RELEASING.md`](../RELEASING.md) 为准**（#298，2026-10-22 起）；本文只写发布动作，两者冲突时改本文。早期规则来源：[产品版本迁移实施手册](product-version-migration-plan.md) §3、§10；阶段来源：[统一实施顺序](implementation-sequence.md) S07–S09。

这份文档管**发布动作怎么做**。维护侧的例行步骤在 [兼容体系维护 SOP](compat-maintenance.md)。

> **今天的状态：编号分配、候选构建、候选晋级与 stable 推广都已写进工作流，但当前方案下还没有一次端到端成功**——`v4.1.0` 的发布运行失败在 Publish step，它的 stable 是手工推广的（见第 7 节 V06）。 候选走 `release.yml`（推 tag 或 `workflow_dispatch`，见第 2 节）；候选转正式走 `advance-release.yml`，正式版进 stable 走 `promote.yml`（见第 3 节）。第 7 节列出仍然缺的部分。

## 1. 三种身份，先分清

| 对象 | 形式 | 谁在用 |
| --- | --- | --- |
| 产品版本 | 正式版 `4.1.0`（三段、无 v）；候选 `4.1.0-beta.1`、`4.1.0-rc.1` | 界面、API、构建信息、文件名、Docker 精确标签 |
| 发布 tag | `v4.1.0`、`v4.1.0-beta.1`（**当前**命名空间，`v` + 版本原样） | Git、GitHub Release |
| 历史四段版本 | `4.0.1.22`（**只读**） | #298 之前发布的 `4.0.1.N`；新发布拒绝四段 |
| 历史发布 tag | `release/4.0.0`（**四条，只读**） | 地址变更前发布的四个 release |
| Go module 版本 | `v0.1.0`（**带 v**） | 仅 Go 依赖工具 |

修复递增 PATCH（`4.1.0` 之后是 `4.1.1`），兼容的新功能递增 MINOR，不兼容递增 MAJOR；面板与 Node **各自编号**，版本号相同不代表需要配对，配对以兼容矩阵为准。`4.1.0-beta.2`、`4.1.0-rc.1`、`4.1.0` 是三个**不同且不可变**的身份。

`v` 是当前的产品 tag 命名空间。`release/` 是**历史**命名空间：四个已发布的 release 住在那里（`4.0.0`、`4.0.1`、`4.0.1.1`、`4.0.1.2`），因为**已发布的 tag 不可移动**，所以读的一方——`cmd/compatwatch`、节点发布目录、面板内的升级提示、安装脚本 —— **永远同时接受两种命名空间**；写的一方只写 `v`。这份「读两种」的规则由 `internal/version`（Go）、`web-react/src/utils/productVersion.ts`（前端）与 PN 的 `releaseid` 共同实现，并由共享向量钉住。

由此有一个后果要记住：**版本号不再唯一决定地址**。派生（`ReleaseTagFor`）给出的是**当前**命名空间的地址，对「即将发布」的版本是对的，对那四个已经发布的版本是错的——它们的地址是**带着的**（目录里的 `release_tag` 字段），不是算出来的。

**不能**用 `release/4.0.0` 当 Docker 标签或文件名：斜杠在 Docker 里是**仓库分隔符**，会推到一个没人看的仓库去——这正是当初决定迁走命名空间的代价之一。**不能**用 `102.1.0` 为 Protocol 冒充模块版本。历史 `v…` tag 不改名、不 force push。

## 2. 发一个候选（testing）

**两条路，选一条。**

**推 tag（首选）。** 把附注 tag 推到 `main` 的最新 commit 上，`push: tags: v*` 触发发布。tag 就是地址，一眼看得见发的是哪个版本，也不用等表单分配号。**前提是这个 commit 的 Test 工作流已经跑完并且成功**——`tag` job 会查，没跑完会被拒（重跑发布即可，不要重新打 tag）。推 `v4.1.1-beta.1` 发出的是 Beta；推不带后缀的 `v4.1.1`，发出的是标题为 **Awaiting promotion** 的 prerelease，不进任何渠道，要再跑第 3 节的 `promote.yml` 才进 stable。

**空白 `workflow_dispatch`（要自动取号时）。** 表单六个输入：

| 输入 | 作用 |
| --- | --- |
| `tag` | **留空即切下一个号**。填了则不切号也不建 tag：这个 tag 必须已经存在并指向本次运行的 commit，而且 dispatch 时要**选这个 tag ref 本身**作为运行的 ref（`GITHUB_REF` 必须是 `refs/tags/<tag>`），否则 `setup` 拒绝。 |
| `line` | `MAJOR.MINOR`（如 `4.1`）。tag 留空时必填——**发到哪条线是人定的**，仓库里没有任何东西知道下一个版本该落在 4.1 还是 4.2。 |
| `stage` | `beta`（默认）或 `rc`。同一目标一旦出了 rc，就不能再切 beta。 |
| `target` | 可选，三段目标版本（如 `4.2.0`），必须在 `line` 上；用来跳过分配器默认的 PATCH+1。 |
| `notes` | 写进附注 tag 的说明，也是读者唯一看得到的那份。留空则给一行标题。 |
| `channel` | `auto` 或 `testing`，**两者效果相同**：都以 prerelease 发布。Beta／RC／Awaiting promotion 由**版本后缀**决定，不由这个输入决定。**这里不能直接发 stable**——正式版走第 3 节。 |

编号**留空即自动**，分配规则（`internal/version.AllocateCandidate`）是：

- 线上还没有任何版本 → `X.Y.0-<stage>.1`；
- 线上最高的是候选 → 目标不变，在所选 `stage` 内计数：`4.1.1-beta.1` 之后选 beta 得 `-beta.2`，选 rc 得 `4.1.1-rc.1`；该目标已出过 rc 时再选 beta 会被拒绝；
- 线上最高的是正式版或历史四段版本 → **PATCH+1** 起新候选（`4.1.0` → `4.1.1-beta.1`）；
- 给了 `target` → 以它为目标；目标已经发过正式版，或结果没有超过该线当前最高版本，都会被拒绝。

自动分配只产出**候选**。取号由**本仓库**的分配器做（`internal/version` + `cmd/allocate-release-tag`），发布工作流调它，不再调用 Passwall Node 的实现——规则是共享契约，实现各自持有，共用同一份向量。

**两条路都做的事。** `tag` job 有三步，顺序不能换：

1. **判断这次是否要切号。** 只有"空白 dispatch"这条路要切；推 tag 和填了 tag 的 dispatch 到这里就停——**但 job 本身照跑**，理由见下面的警示。
2. **拒绝没跑过测试的 commit。** 它查的是 **Test 工作流**在这个 SHA 上的结论，不是"这个 commit 上的所有 run"——枚举所有 run 会把**正在运行的这次 release 自己**算进去（in progress、无结论），于是提交因为自己存在而被拒。**这一步在两条路上都跑**：手工推 tag 的路径上，commit 是人挑的，正好是最不能跳过这一问的地方。**而且只认 `main`**：commit 必须已在 `main` 上（`git merge-base --is-ancestor`），结论也只取 `main` 上那次 Test 运行（`--branch main`）。dispatch 可以从任意分支发起，分支头往往已有一次绿色的 `pull_request` 运行——它测的是合并结果而不是这个 commit，还跳过了第三方隔离那条线；不加这两道限制，它就足以在未合并的代码上切出一个永久 tag、移动 `:beta`。
3. **调本仓库的分配器取号**（仅切号路径），并把 `-on-commit` 一起给它：已经绑在这个源码上的号会被**续用**，重跑不会烧掉新号。随后**创建并推送附注 tag**，冲突即重读再取（最多 5 次）。`git push` 拒绝移动已有 ref，所以并发两个发布里输的那个会看到别人的号已占用，重取下一个——**永不覆盖已有 tag**。构建失败允许留下空号。

> **发布图里不允许有"可被跳过"的 job。** 被 skip 的 job 会**连带跳掉它整条下游链路**，而且在中间那个 job 上写 `always()` **救不了下游**：被豁免的 job 照跑并报成功，它下面的 job 还是被跳过。v4.0.1.8——第一个用推 tag 发的版本——就是这样什么都没发出去：`tag` job 在那条路上被自己的 `if:` 跳过，`setup` 靠 `always()` 跑成功，而 `web`/`build`/`release`/`docker` 全被跳掉，run 一路走到兼容门禁（它带 `always()`），报出一句 `LEG=skipped` 然后失败。**这条路径此前从未被走过**，因为在那之前每一次发布都是空白 dispatch，而那条路上 `tag` job 是真的会跑。
>
> 所以现在的规矩是：**构建产物依赖的 job 一律不带 job 级 `if:`**，需要条件就写在 step 上；文件里剩下的 job 级 `if:` 必须带 `always()`，即"链路失败时我照跑来出结论"的那种（兼容门禁）。这两条由 `deploy/release_workflow_test.mjs` 钉住。

之后 `setup` 用本仓库的 `release-tag` 命令把 tag 解析成版本（tag 与版本是两个身份），门禁、构建、上传为 **draft**、Docker 依次跑；draft 建出来时就是 `prerelease=true`，阶段由版本决定：带 `-beta.N` 是 Beta、带 `-rc.N` 是 RC、不带后缀的是 **Awaiting promotion**（待批准，不进任何渠道）。draft 不进用户目录。**draft 在公开之前会被读回来核对**：`release` job 把 draft 上的文件经 API 下载回来，文件集合必须恰好是本 job 打包的那些，每个文件都要通过同一发布里的 `SHA256SUMS.txt`，每个归档里的 `psp` 都必须是证据索引记录、门禁接受过的那个摘要；三条都过，才只改 draft 标志把它公开——渠道、Latest、说明都在建 draft 时已定，不再改。核对失败时发布停在 draft：若是本次运行较早一次尝试留下的 draft，重跑失败的 job 即可（二进制相同，照样通过；只是动作更新已有 release 时会把说明末尾的 Docker / Verify 一段再追加一遍，公开后手工删掉即可）；若是**别的运行**留下的，它保留着那次的文件（动作不覆盖 draft 上已有的文件），先删掉这个 draft 再重跑。

一次发布只发一个候选：**不移动 tag、不重建、不替换附件。**

> **分配器在本仓库里，改规则就是改代码。** 这条规则**曾经**来自 pin 住的 Node 模块，于是一个规则修复要先合并上游、再升 pin，发布路径才用得上新规则；现在它在 `internal/version` 里，合并到 `main` 即生效。
>
> **但"同一规则"仍要靠向量保证**，不是靠两边各写一遍：`internal/version` 与前端 `web-react/src/utils/productVersion.vectors.json` 由同一份向量驱动。**跨仓那一半由 pinned-source 契约 job 做**——它本来就把 Node 仓库按记录的 revision 签出来，现在这份 checkout 也用来比对向量；本仓库不再要求 Node 模块，`go.mod` 里已经没有它。

**候选阶段由版本后缀决定，GitHub 元数据标错了也不改变它**：带 `-beta.N`／`-rc.N` 的版本永远进不了 stable。不带后缀的版本则要看发布元数据和 `promote.yml` 的批准——**不能**按「有没有连字符」反推它是正式版，没经过推广的不带后缀版本仍是待批准。

## 3. 转 stable

1. 选定一个**已验收**的 release，核对 commit 与制品 digest 没有变。
2. 确认 required 证据完整（`check-case-set.mjs` 的汇总）。
3. **只做一次：让 `release/v3` 交出 `latest`。** 原定在 V4 的第一个 stable 之前完成；**截至 2026-11-03 仍未完成**——V4 的第一个 stable 是 2026-10-06 的 `v4.0.1.20`（之后有 `v4.0.1.21`、`v4.1.0`），而 `release/v3` 的 `release.yml` 仍带 `type=raw,value=latest,...`，`make_latest: false` 只出现在注释里。所以在这件事做完之前**不要发任何 V3 补丁**，下面描述的降级就会发生。做法是在 `release/v3` 上合入一个改动，改它自己的 `.github/workflows/release.yml`：
   - 删掉 docker metadata 里的 `type=raw,value=latest,...` 一行（文件里 `# >>> WHEN V4 PUBLISHES ITS FIRST STABLE RELEASE, DELETE THE :latest` 这条标记指的那行；标记在下一行注释 `# >>> LINE BELOW. <<<` 才结束，按整句搜是搜不到的）；
   - 在同一文件的 `Publish GitHub Release` step 加 `make_latest: false`——GitHub 的 Latest 标记是全仓库**一个**指针，按线拆不开。

   **原因**：两条线的发布工作流都会写 Docker `:latest` 和 GitHub Latest，谁最后发布谁赢。交接没做，下一个 V3 例行补丁就会把 `:latest` 拖回 V3——**数据库模型不同的镜像**；`docker-compose.yml` 默认就是 `:latest` 加 `pull_policy: always`，每个跟着它的部署下一次 `up -d` 就跨大版本降级。两个工作流都不检测、不告警，而这条交接说明原先只写在另一条分支的注释里，在 `main` 上转 stable 的人看不到。V3 用户改用滚动标签 `:v3`（`release/v3` 的 README 已写；main 的 README 还没有），交接对他们不丢任何东西。

   核对（先 `git fetch origin release/v3`）：`git show origin/release/v3:.github/workflows/release.yml | grep -n 'value=latest'` 应当**没有输出**；同一文件 `grep -nE '^ +make_latest: false'` 应当命中 Publish step 里的那一行——注释里提到它不算，今天的注释就提到了。两条都成立再改元数据：V4 一旦是正式版，交接前的任何一个 V3 补丁都会把两个 `latest` 抢回去。
4. 按起点选一个工作流（都只能从 `main` 发起）：
   - **从候选转正式**（`v4.1.1-rc.1` → `4.1.1`）：dispatch **Advance a published candidate**（`advance-release.yml`），`source_tag` 填已发布的候选 tag，`target_stage` 选 `rc` 或 `stable`。它钉住候选 tag 的 commit（`main` 已前进也一样），在同一 commit 上打**新的**不可变 tag、重新打版本戳构建并发布；目标是 stable 时，等发布完成后接着调 `promote.yml`。
   - **已经是不带后缀的正式版、停在 Awaiting promotion**（推了 `v4.1.0` 这类 tag）：直接 dispatch **Promote a release to stable**（`promote.yml`），`tag` 填它。
5. `promote.yml` 跑在 `release-promotion` environment 下：核对发布已完成、六个归档与 `SHA256SUMS.txt`、构建证明和镜像来源，**拒绝把 stable 往回拨**，然后把 Docker `stable`／`latest` 指向**已经验证过的 digest**（`rc`／`beta`／`testing` 也一起前移，但只在它们还没指向更新版本时才动），并把 GitHub release 改为 `prerelease=false`、`make_latest=true`、标题 `· Stable`。它要求这个 tag 有一次**成功的** release 运行。核对阶段失败就直接失败、什么都没改；推广 step 已经开始后失败，或者 stable 指针没收敛，才会报 `promotion_pending`。这两种情况都重跑同一个工作流。
6. **不移动 tag、不替换附件、不要求已安装的实例重装。**

> 带后缀的候选**永远不会**被标成 stable、改名或移动 tag：正式版是同一 commit 上另打 tag、另打版本戳的新身份。若修复代码或改变构建输入导致制品不同，**分配新号**（规则见第 4 节），不要在原 tag 下重新上传。

## 4. 修一个坏候选

代码变更必须新号：同一目标上再切一个候选（`4.1.1-beta.2`，或 rc 阶段的 `4.1.1-rc.2`）；目标已经发过正式版的，修复走 **PATCH+1**（`4.1.1` → `4.1.2-beta.1`）。旧候选保留为历史。

> **不要用「撤销推荐」冒充回退。** 面板提供什么由**已发布的发行**决定，这里没有一份政策可以把某个版本从列表里拿掉——那份签名政策已经删除。所以撤销推荐是**操作决定**：在公告里说明、不去安装它。已经装上的实例按第 6 节的恢复路径处理。

## 5. 推广中途失败

GitHub 元数据、Docker 标签和政策更新**不是跨服务原子事务**。任何一步失败：

- 保留已知有效状态，不猜测；
- 列出 `promotion_pending` 的**准确步骤**；
- 未完成的推广不引发节点自动重装；
- 重复事件按 `release_id + 目标渠道 + digest` **幂等**——事件会重复，也可能由不同 token 触发。

## 6. 回退

`stable → prerelease` **不是**常规回退手段。坏版本的「撤销推荐」是公告与不安装，不是改元数据——面板不再有一份政策可以据此收紧准入（见第 4 节）。已安装实例走已验证的恢复路径。不得通过把旧版本改成很大的数字来欺骗升级器。

## 7. 今天缺什么（不要假装已自动化）

| 计划项 | 状态 | 缺的是 |
| --- | --- | --- |
| V05 编号分配 | **已实施** | `tag` job：切号、原子冲突确认、空号规则；分配器已归本仓库（X07 A）。守 `deploy/release_workflow_test.mjs` |
| V05 候选构建 | **已实施** | 一次构建；跨平台编译 + 门禁 + 精确上传为 draft。linux 两个架构各在原生 runner 上构建，并运行产物核对版本戳；darwin / windows 只检查构建元数据 |
| V06 渠道推广 | **代码已有，未跑通过** | `advance-release.yml`、`promote.yml` 已在（#298），但**当前方案下还没有一次成功的发布**：`v4.1.0` 的两次 release 运行（41053341293 推 tag、41054912915 dispatch）都失败在 `Publish GitHub Release`，镜像发布与渠道推进被跳过；`promote.yml`／`advance-release.yml` 从未运行。`v4.1.0` 的 `· Stable` 元数据与 ghcr `:stable`／`:latest` 是手工完成的。要先修好 Publish step，再走一次完整的候选 → 正式 → 推广 |
| C08 发布门回归 | 部分 | `deploy/compat/` 的校验器已在 CI。**推 tag 这条路在 `v4.0.1.8` 上第一次被走就失败了**——`tag` job 被自己的 `if:` 跳过，连带跳掉整条下游，什么都没发出去（第 2 节的警示）；缺陷修好后，`v4.0.1.9`–`v4.0.1.19` 都由推 tag 成功发布 |
| S08 桥接验收 | **作废** | 旧命名方案已整体删除，没有要桥接的部署 |
| S09 新命名首发 | **已发** | `4.0.0`、`4.0.1` 作为 testing 候选已发布；`v4.1.0` 于 2026-10-30 发布，现为 stable（手工推广，见 V06）。**生产部署仍未授权** |

**发布授权与代码授权是两件事。** 改代码、合并 PR、构建候选、发布制品、推广渠道、部署生产是六种操作，通过上一项不授权下一项。

## 8. 每次发布后

- 支持政策是否仍然不超过证据（`docs/compat/verification-v1.json` 的 required 集合非空且都通过）
- 固定 SHA 是否仍可获取
- 旧的解析路径是否仍然能读历史清单
- 过期的 observation 是否有人处理

维护侧的完整检查表在 [兼容体系维护 SOP](compat-maintenance.md)。
