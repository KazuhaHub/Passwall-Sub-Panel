# 兼容体系交接包

日期：2026-09-19。配套：[整改实施手册](compat-remediation-plan.md)、[维护 SOP](compat-maintenance.md)、[机器可读政策](compat/passwall-node-v4.json)。

手册 §15 要求交付一份交接包，列出合并 PR、两仓 SHA、支持政策、required case 清单、通过与失败注入证据、已知限制、运行时示例、发布 needs 图、分支保护设置和下一位维护者的操作命令。这份文档就是那份包，**每一项都指向可核对的东西**，不是一句"已完成"。

## 1. 两仓最终 SHA

```
PSP  41b3480c0b86593fe7f4561df8b18c653b163a0f
PN   a68c75b562fa1c45048ab3741417bb2bc2c8bc66
```

## 2. 兼容整改相关的合并 PR

PSP：

```
#168  feat(compat): require a verified edge for a remote upgrade (R10 step 8)
#167  docs(compat): record the branch protection the gate now sits in (R03)
#166  test(compat): drive quota exhaustion as well as expiry (R08)
#165  test(compat): drive the dataplane's refusal, and stop it lying (R08)
#164  test(compat): cover the manifest refusals that had no test (R09)
#163  fix(compat): keep the result checker searchable as text
#160  feat(compat): drive a real subscription through a real client (R08, R10)
#156  feat(compat): run the adapters against real isolated panels (R07)
#155  docs(compat): add the maintenance SOP and the handover (R11)
#153  feat(compat): plan the case set and pin each tag's identity (R04)
#152  feat(compat): gate every publishing job behind the case set (R03)
#151  feat(compat): judge the Node wire run against a profile (R02)
```

PN：

```
#34  test(compat): the reverse-direction profile, B01-B08 (R05)
#33  test(upgrade): verify the systemd path and start the historical suite (R06)
#32  feat(compat): run the candidate agent against a released old panel (R05)
```

## 3. 机器可读的支持政策

| 文件 | 是什么 | 权威性 |
| --- | --- | --- |
| `docs/compat/passwall-node-v4.json` | CI 测试矩阵与协议世代：`wire`、`features`、`min_supported`、`released_nodes` | 规划器与矩阵读取的那一份（面板的升级列表已改由**已发布发行**决定）|
| `docs/compat/verification-v1.json` | 身份固定：每个 tag 当初解引用到的**提交**、profile 断言、`excluded` 的排除理由 | 规划器读取的那一份 |
| `deploy/compat/profiles.json`、`profiles-third-party.json` | 逐 case 的 required / notApplicable 闭集 | 两个校验器读取 |
| `docs/compat/3x-ui-v4.json` | 第三方后端的**实测记录** | **不是政策**，是人类评审的留痕 |

## 4. required case 清单

```
$ node deploy/compat/plan.mjs --emit cases
```

11 个 case：`node-wire-v1@v0.0.1-beta1` 起、按 `min_supported` **位置**切片到最后一个已发布版本（当前为 beta1..beta11）。切片按位置而非版本比较——`v0.0.1-beta9` 在字典序上**大于** `beta11`，比较会把 floor 反过来。

第三方与数据链路的 case 不在这一份清单里：它们由 `profiles-third-party.json` 与 `deploy/compat/dataplane` 各自定义，见下。

## 5. 通过与失败注入证据

| 门 | 通过证据 | 失败注入证据 |
| --- | --- | --- |
| R02 逐 case 校验器 | `deploy/compat/check-go-results.test.mjs` | 同一文件：skip、零匹配、截断流、非零退出、后报成功 |
| R03 汇总门 | `deploy/compat/check-case-set.test.mjs` | 同一文件：删一个报告、空目录、报告自身非通过、报告不可读 |
| R03/R10 发布门 | `deploy/release_workflow_test.mjs` | 同一文件：共享缓存、可覆盖发布、写出 token 越界、把发布挂在交叉编译上 |
| R07 第三方隔离 | CI 作业 `third-party adapters (isolated real panels)` 全绿 | 空环境检查；`third-party_env.test.mjs` |
| R08 数据链路 | `dataplane.sh` 端到端 | `PSP_DATA_SABOTAGE=credentials` / `no-enforcement`，两者都必须非零退出 |
| R05 反方向 | PN `deployment/compat/old-psp.sh` 全 PASS | `PSP_CANDIDATE_AGENT=/bin/true` 必须 FAIL |

## 6. 已知限制

**逐条列出，不合并成"部分支持"。**

- **升级边这条轴已删除，不再是「空的」——它不存在。** 曾经的含义是「没有一条已验证的 `from→to`，所以没有升级会被准入」，而准入现在只看能力与协议世代两道门：节点声明 `task.agent.upgrade.v1` 且近期上报，升级就会下发。`verification-v1.json` 里没有 `upgrade_edges` 字段，SOP §3 已改写。
- **反方向（候选 Node × 旧 PSP）没有 CI。** harness 可跑（PN #34），但需要真实旧 PSP 发布物与候选 PN 作为两个进程。
- **R08 只测了 VLESS/TCP/none 一种组合。** TLS／REALITY 不在 `dataplane.sh` 里；PN 自己的 core-acceptance 在原生 Linux 上覆盖官方核心与 REALITY，未接的是"经第三方面板升级后"这个触发点。
- **R05 的 B06 是 N/A**，理由是版本事实：`node-diagnostics` 于 2026-09-18 加入，`v4.0.0-beta.19` 于 2026-09-17 发布。
- **第三方范围与上限没有机器可读政策。**
- **quota 生效窗口 300s、到期窗口 180s** 是这套配置的实测值，不是通用承诺。

## 7. 运行时界面／API 示例

```bash
# 升级准入（现在会因为缺边被拒，理由具名）
curl -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: example-key-0001' \
  -d '{"version":"v0.0.1-beta11","expected_version":"v0.0.1-beta9"}' \
  "$PSP/api/admin/servers/1/upgrade-node-agent"
# → 400 {"error":"invalid exact-version native upgrade request"}
#    服务端决策的 reason 是 upgrade-edge-missing

# 列表里同一件事的一面
curl -H "Authorization: Bearer $TOKEN" "$PSP/api/admin/servers"
# → node_compatibility:"compatible", node_compatibility_reason:"upgrade-edge-missing",
#   node_upgrade_ready:false
```

## 8. 发布 needs 图

```
setup ──┬─ node-compatibility ─┐
        ├─ node-contract ──────┴─ compatibility (always) ─┬─ release
        ├─ web ───────────────────────────────────────────┤
        └─ build ─────────────────────────────────────────┘
                                                          └─ docker
```

`compatibility` 是所有发布动作的唯一入口；`release` 与 `docker` 都只依赖它，不直接依赖 `node-compatibility`。取消或缺少报告都不通过。

**R10 的性质**（手册 §15 原文："最终被发布的摘要与被验收摘要完全一致，无发布旁路"）由这几处强制，且都有守卫测试：tag 必须解引用到 CI 自身的 SHA、所有下游 job 一律 checkout `github.sha`、`target_commitish` 指向被验收的 SHA、tag 在构建期间变动即拒绝、`overwrite_files: false`、发布先以 draft 上传并读回核对（`SHA256SUMS.txt` 与索引里的二进制摘要）之后才公开。

## 9. 分支保护设置

见 [维护 SOP §6](compat-maintenance.md)：ruleset `main protection`（id `23046834`）要求九个检查，含 `compatibility gate`，附读取命令与改前原值。

## 10. 下一位维护者的操作命令

```bash
# 规划与校验
node deploy/compat/plan.mjs --emit cases
node --test deploy/compat/*.test.mjs deploy/release_workflow_test.mjs deploy/test_workflow_test.mjs

# 第三方隔离（常驻面板）
deploy/compat/backends/third-party.sh up
eval "$(deploy/compat/backends/third-party.sh env)"
deploy/compat/backends/third-party.sh down

# 真实代理链路（需要候选 PSP、真实 3X-UI、固定版本 sing-box）
deploy/compat/dataplane/dataplane.sh
PSP_DATA_SABOTAGE=credentials deploy/compat/dataplane/dataplane.sh   # 必须失败

# 反方向（PN 仓库，需要真实旧 PSP 发布物）
PSP_CANDIDATE_AGENT=/path/to/passwall-node ./deployment/compat/old-psp.sh

# 发布路径的本地试跑（不发布任何东西）
# 见维护 SOP §6"发布路径的试跑范围"
```

## 11. 一次真实发布

`v4.0.0-beta.25`（2026-09-19）是兼容整改之后第一次真实发布，也是发布路径第一次被完整执行。

| | |
| --- | --- |
| 被验收的提交 | `7c4d45559519827d2a257aeb076d6943a07b113f` |
| 发布 | `v4.0.0-beta.25`，Pre-release，六个归档 + `SHA256SUMS.txt` |
| 镜像 | `ghcr.io/kazuhahub/passwall-sub-panel:v4.0.0-beta.25` 与 `:beta` |
| `:latest` | **未移动**（仍是 v3.9.3 那次正式版），`/releases/latest` 也仍指向它，因此预发布不会出现在应用内升级提示里 |
| 证据索引 | 本次运行产出，并作为附件随发布留存：11 个 case、`missing: []`、六个产物的 sha256、`source_sha` 即上面那个提交。**这一次的附件是手工上传的**（比工作流上传的归档晚两分钟），之后的发布一直没有它，见下文 |

**R10 的验收性质由此可核对**：索引里的 `source_sha` 与被发布的归档来自同一次构建，`release` 与 `docker` 只经由 `compatibility gate`（两者都不直接依赖 node-compatibility），`:latest` 按分支规则未动。

### 索引与镜像 digest 如何留存

证据索引作为 artifact 最多保留 90 天（公开仓库的上限），过期之后，发布背后的兼容声明就无从核对，而这正是 `evidence-index.mjs` 开头说它要避免的事。所以现在由 `release` job 自动附上：它下载本次运行门禁写出的 `compatibility-evidence-index`，先核对 `source_sha` 是这个提交、`missing` 为空，再以 **`compat-evidence-index-<版本>.json`** 放进 `dist/`。放入发生在生成 `SHA256SUMS.txt` **之前**，所以校验和覆盖它，和六个归档一样。文件名用版本不用 tag：`dist/` 里的文件名都归版本管，而 tag 可能带斜杠。

镜像 digest 以前哪里都没记，证据和被推送的镜像之间只靠那个精确 tag 连着。现在 `docker` job 把 build-push 的 digest 作为 job 输出 `digest` 导出，并写进运行摘要：`ghcr.io/<owner>/passwall-sub-panel@sha256:…` 以及它被打上的每个 tag。空的或格式不对的 digest 会让这一步失败，而不是被当成一条记录写下来。

**来源可以核对，不只是完整性。** `SHA256SUMS.txt` 和它覆盖的文件在同一个发布页上，能换掉文件的人也能换掉它，所以它只证明文件完整到达，证明不了是谁构建的。现在两个发布 job 各自给产物生成 build provenance 证明（`actions/attest-build-provenance`，Sigstore 签名，绑定本工作流、提交与运行）：`release` 在 draft 读回核对之后、公开之前证明**读回的那些文件**（即公开出去的字节，不是 `dist/`：同一运行重跑时 draft 保留第一次的归档，重新打包的 tar 时间戳不同）；`docker` 按 digest（不是 tag）证明推送的镜像，并把证明推到镜像旁边。核对命令：

```bash
gh attestation verify passwall-sub-panel_<版本>_linux_amd64.tar.gz --repo KazuhaHub/Passwall-Sub-Panel
gh attestation verify oci://ghcr.io/kazuhahub/passwall-sub-panel:<版本> --repo KazuhaHub/Passwall-Sub-Panel
```

镜像证明也就把 digest 与提交、运行永久绑在了一起，不随运行日志过期。

### 第一次执行抓到的东西

第一次运行就失败了，而且是**正确地**失败:证据索引那一步引用的 `.compat/digests.json` **没有任何作业产出**——引用写了，产出没写，而这条路径此前从未跑过，所以一直没人发现。门挡住了发布，`release` 与 `docker` 未运行，没有任何归档被上传、没有任何渠道被移动。修复见 #173。

**这件事本身就是 §15"完成一项必须补相应 CI 和发布物证据"的理由**：本地试跑抓不到它——我手工喂了自己算的摘要，而那正是 workflow 缺失的那段交接。只有真的跑一次才会暴露。
