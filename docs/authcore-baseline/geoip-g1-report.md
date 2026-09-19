# G1：GeoIP 接入实验报告

结论：**adopted**。判据来自 ADR 0037（在 `kazuha/authcore-experiment-baseline` 分支上，本分支尚无该文件）§4 的 GeoIP 行（`Δ_conservative <= 0` 且存在真实机制转移，**不适用** SAML/Passkey 的预算例外）。

本文件是 G1 的独立结论。**权威的 A/D/Δ 表格仍以 `docs/authcore-migration-measurement.md` §6 为准**，E1 阶段把下面这些数字并入该表；本报告放在 G1 分支上，避免两条分支各自带着测量文档的分叉副本。

日期：2026-09-19。分支：`kazuha/authcore-geoip-experiment`（**基于 `origin/main`**，与 H 线无关——GeoIP 没有前置加固，按计划书 §1.2 令 H = B0）。

## 1. 采用的判据计算

计数范围：`internal/pkg/geoip/` 的生产 Go 行（测试单列），固定物理行口径，含注释。

| 符号 | 值 | 依据 |
| --- | --- | --- |
| `A` | **57** | `git diff --numstat -- internal/pkg/geoip/geoip.go` 的 + 列 |
| `D_pre` | **113** | 同一次 diff 的 − 列；全部是 B0 原有实现 |
| `D_hardening` | **0** | GeoIP 没有 H 阶段加固（H = B0） |
| `D_bridge` | **0** | 没有仅为接库而搭建、随后删除的代码 |
| `Δ_actual` | **−56** | `A − D_pre − D_hardening` |
| `Δ_conservative` | **−56** | `A − D_pre` |

文件从 156 行降到 100 行；测试从 95 行到 91 行（删除 64 行、新增 60 行，见 §4）。

两条口径相同，因为 GeoIP 没有可记入 `D_hardening` 的前置加固——这正是 §1.2 允许 H = B0 的情形。

**机制转移**（职责表 §5.3，须三项同时成立才算一项）：

| ID | 判断 | PSP 侧实现 | authcore 侧契约与回归测试 |
| --- | --- | --- | --- |
| G-DECODE | MMDB 解码与双 schema 映射 | 已删除（`mapRecord`/`asString`/`mapOf`/`enName`） | `Location` + `mapRecord`；`TestMapRecord_MaxMindSchema`、`TestMapRecord_IPinfoSchema`、`TestReader_Lookup_EndToEnd` |
| G-MULTICAST | 组播地址过滤 | 已删除（`IsResolvable` 改为委托） | `IsResolvable` 含 `!p.IsMulticast()`；`TestIsResolvable` |
| G-FALLBACK | name/city/region/country_code 回退 | 已删除（原实现没有这些回退） | `TestMapRecord_CityAndRegionAsPlainStrings`、`TestMapRecord_CountryCodeOnlyVariant`、`TestMapRecord_NameFallbackWithoutEnglish` |
| G-READER | Reader 生命周期、文件替换、读写锁 | **保留**（`service/geo`） | 不转移 |
| G-CONVERT | `Location → domain.GeoLocation` | **保留**（`toDomain`） | 不转移 |

transfer 的成立不是靠"我们不再实现它"，而是 authcore 的测试是 PSP 原测试的**严格超集**：见 §4。

## 2. 有意为之的行为差异（不声称逐字节等价）

按计划书 §8.1，两处差异如实记录，都用测试钉住：

1. **组播地址不再被查询。** 原实现的不可解析集合是 loopback / private / unspecified / link-local，`224.0.0.1`、`239.1.1.1`、`ff02::1` 不在其中，会被送去查库并只能得到空结果；authcore 额外排除组播。影响面是"少一次无意义的查询"，上层展示不会因此改变（查库本来也返回空）。
2. **三处字段回退会填充过去为空的值。** 只有扁平 `city`/`region`/`country_code` 的记录、以及 `names` 里没有 `en` 的记录，现在能显示出城市/地区/国家名。这些是**展示值**，不参与账户判断；新增的是过去显示空白的地方。

PSP 侧测试对这两点的覆盖：`TestIsResolvable` 增加了三个组播用例（并注明原实现不会拒绝它们）；字段回退属于 authcore 的映射，由它的 fixture 覆盖，PSP 不重复验证。

## 3. 依赖变化（按 §2.4 记录，不宣称风险消失）

`go mod tidy` 之后：

- **新增**：`github.com/KazuhaHub/authcore v0.3.0` — **直接**依赖（固定 tag，非伪版本，无本地 replace）。
- **变化**：`github.com/oschwald/maxminddb-golang v1.13.1` 由**直接**变为**间接**——包装层不再直接 import 它。**这不等于依赖消失**：它仍通过 authcore/geoip 留在模块图里。计划书 §2.4 预判的正是这一情形。
- 其余四个上游库（crewjam、go-webauthn、base64Captcha、maxminddb）仍在图中，与本实验无关。

## 4. 测试的移动（不是删除覆盖）

`internal/pkg/geoip/geoip_test.go`：删除 64 行、新增 60 行。

删掉的是 `TestMapRecord_MaxMindSchema`、`TestMapRecord_IPinfoSchema`、`TestMapRecord_EnNameFallback`、`TestGranularityOf` —— 它们测的是 `mapRecord`/`enName`/`granularityOf`，这些函数在 PSP 已经不存在。**同一范围在 authcore 侧有更宽的覆盖**（函数名逐一核对过）：

| PSP（已删除） | authcore（现存） |
| --- | --- |
| `TestMapRecord_MaxMindSchema` | `TestMapRecord_MaxMindSchema` |
| `TestMapRecord_IPinfoSchema` | `TestMapRecord_IPinfoSchema` |
| — | `TestMapRecord_CountryCodeOnlyVariant`（PSP 没有） |
| — | `TestMapRecord_CityAndRegionAsPlainStrings`（PSP 没有） |
| `TestMapRecord_EnNameFallback` | `TestMapRecord_NameFallbackWithoutEnglish` |
| `TestGranularityOf` | `TestReader_Info_CountryGranularity`（**用真实生成的 .mmdb**） |
| `TestIsResolvable` | `TestIsResolvable`（PSP 侧保留，并补组播用例） |

authcore 侧的 fixture 是**真实 MMDB**：`fixture_test.go` 用 `mmdbwriter` 生成，覆盖 MaxMind 嵌套 schema（TEST-NET-1）、ipinfo 扁平 schema（TEST-NET-2，且 `country_code` 故意小写以检验大写化）；`TestReader_Lookup_EndToEnd` 走完整 `Open`→`Lookup` 路径。PSP 仓库里没有 `.mmdb` fixture，也不重复造这些。

新增的是 PSP 自己那一层的测试：`TestToDomain`（转换，含空值）、`TestIsResolvable`（含组播）、`TestNilReaderIsSafe`（geo 未配置时服务持 nil Reader）、`TestOpenMissingFile`（错误契约）。

## 5. 验证证据

```
$ GOWORK=off go build ./...            # BUILD_OK
$ GOWORK=off go vet ./...              # VET_OK
$ gofmt -l internal/                   # 空
$ GOWORK=off go test -count=1 ./internal/pkg/geoip/... ./internal/service/geo/...
ok  .../internal/pkg/geoip   0.153s
ok  .../internal/service/geo 0.416s
$ GOWORK=off go mod tidy               # go.mod 变化见 §3
```

`service/geo` 的测试（含并发查表/换库、坏文件、禁用状态）全部照原样通过——生命周期那一层没有改动。

## 6. 未做 / 未验证

- **没有真实数据库下载与真实 IP 查询**：G01/G02/G03 的等价验证走的是 authcore 的生成 fixture 与本机单元测试，不需要联网。真实 MaxMind 文件的表现未实测。
- **MySQL/PostgreSQL 无关**：本包不碰数据库。
- **未测量前端影响**：该包只被 `service/geo` 与登录来源展示使用；未跑前端 smoke（本机 headless Chrome 不可用；原因见 `kazuha/passkey-state-hardening` 分支上的 `docs/authcore-baseline/h-saml-artifact-smoke.txt` §3）。
