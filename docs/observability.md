# 可观测性：内置指标与测量窗口

> 本文说明 PSP 的进程内指标怎么读、怎么用它跑一次可验收的测量。
> 关联：[data-plane-plan.md](data-plane-plan.md)（这些指标是为回答该文 §2 的问题而加的）。

## 1. 为什么不是 Prometheus

PSP 是单二进制，绝大多数部署是一台 VPS，附近没有任何抓取设施。引入 client library 会带来一棵传递依赖树和第二个需要加固的 HTTP 面，而它要回答的问题——「一轮 poll 花在哪、面板 RTT 多少、skip 命中率多少」——一个挂在既有 admin 鉴权后面的 JSON 就够了。

记录 API（counter / gauge / 带显式 bucket 边界的 histogram）刻意照着 Prometheus 的形状写，命名也遵循其约定（单位后缀、counter 带 `_total`）。**将来若真要暴露抓取端点，改的是渲染，不是调用点。**

热路径开销：counter/gauge 是一次原子加；一次 histogram 观测是十几个边界的二分查找加几次原子操作。一轮 poll 最多几千次观测，相对单次 HTTP 往返不可测量。

## 2. 端点

两个，均为 **admin 专属**（不是 staff）——快照会暴露部署规模和面板响应性，那是站长的信息，不是 operator 干活需要的。

| 方法 | 路径 | 作用 |
|---|---|---|
| `GET` | `/api/admin/diagnostics/metrics` | 当前快照 |
| `POST` | `/api/admin/diagnostics/metrics/reset` | 归零并开启新窗口，**返回被关闭的那个窗口** |

`reset` 返回旧快照不是顺手为之：直方图的分位数**不可相减**，所以「取样两次做差」这种做法对 Phase 0 真正关心的那一半数据根本不成立。窗口必须靠 reset 划出来，而 reset 又不能把它结束的那个窗口丢掉。

## 3. 跑一次测量

```bash
# 0. 确认跑的是带插桩的构建——对着错的二进制测，比不测更糟
curl -s -b cookies.txt https://panel.example.com/api/admin/diagnostics/metrics | jq '{version, commit, uptime_ms}'

# 1. 开窗
curl -s -b cookies.txt -X POST https://panel.example.com/api/admin/diagnostics/metrics/reset > /dev/null

# 2. 等。至少 24 小时，且必须盖过业务高峰——
#    N（每周期活跃用户数）在低谷和高峰能差一个数量级。

# 3. 收窗
curl -s -b cookies.txt https://panel.example.com/api/admin/diagnostics/metrics > window.json
```

`uptime_ms` 与 `metrics.window_ms` 是两个不同的东西：前者是进程活了多久，后者是本次测量窗口开了多久。**若两者相差无几，说明中途重启过，样本作废**——counter 会跟着进程一起归零。

## 4. 指标与它回答的问题

每一项都对应 [data-plane-plan.md §2](data-plane-plan.md) 的一个未知数。

### 4.1 面板往返（RTT）

| 指标 | 含义 |
|---|---|
| `psp_panel_rtt_ms{op=…}` | 单次 HTTP 交换耗时 |
| `psp_panel_op_total{op=…}` | 交换次数 |
| `psp_panel_op_error_total{op=…}` | 出错次数 |

**计的是 HTTP 交换，不是逻辑调用。** `mutateWithRetry` 一次 `UpdateClient` 最多发五个请求，每个都是调用方真金白银等的往返。计划里的成本模型以往返计价，所以这样计数才对得上。

耗时覆盖整个 `doJSONRetry`，含透明重登录与那一次 401 重试。这是**故意的**：那也是调用方要等的延迟，折进去才诚实地反映「一次调用要多少钱」，而不是「顺利时要多少钱」。

`op=other` 是一个真实存在的桶，不是丢弃。**它涨起来就说明有热路径新增了却没打标签。**

### 4.2 skip 命中率与写入原因

| 指标 | 含义 |
|---|---|
| `psp_lifecycle_sync_total` | 分母：过了 provisioned 闸的调用 |
| `psp_lifecycle_sync_skipped_total` | skip 生效 |
| `psp_lifecycle_sync_write_total` | 发了 `UpdateClient` |
| `psp_lifecycle_sync_error_total` | 失败 |
| `psp_lifecycle_sync_write_reason_total{reason=…}` | **是哪个字段挫败了 skip** |
| `psp_lifecycle_sync_not_provisioned_total` | 单列，不进分母 |

**口径要说清楚，否则会算错**：`skipped + write = total`，这两个互斥且穷尽。**`error` 是与 `write` 重叠的子计数**（失败的 `UpdateClient` 两个都加一），另外还包含 pool 取用失败（那种既不算 skip 也不算 write）。所以：

```
skip 命中率 = skipped / total        ✅
skipped + write + error = total      ❌ 别这么算
```

未 provisioned 的调用单列，否则一支迁移到一半的机队会报出一个由「从未考虑过推送的调用」堆出来的漂亮 skip 率。

`reason=panel_unread` 表示 `GetClient` 失败或报告客户端不存在，skip 无从判断而直接走写入。单列它是为了让 `sum(write_reason) == write_total` 成立——否则漏掉的恰好是**最不稳定的那些面板**。

```
skip 命中率 = psp_lifecycle_sync_skipped_total / psp_lifecycle_sync_total
```

**`{reason=…}` 是 Phase 0 最吃重的一项。** 计划 §1.2 断言：不断收缩的流量下限每周期都挫败 skip。若分布压倒性地落在 `total_gb`，该断言成立，且对这**一个**字段做迟滞带就能把 skip 救回来。若别的原因大量出现，迟滞带就白做了，Phase 1a 打错了靶。

`reason` 的判定顺序里 `total_gb` **排第一**——嫌疑犯不能被排在它前面的字段掩盖掉。

### 4.2.1 能力缺口

`psp_capability_gap_total{capability=…}`：PSP 想强制某个设置，但目标面板执行不了。

写入会成功、就是不生效——这是能力列表本该防住的失败形状。**只在 PSP 真的想强制时才计数**（不限的用户落在不支持的面板上不算缺口）。日志每（面板, 能力）只打一次，因为这个状态是稳态的；计数器才是持久信号。

见 [connection-limits.md](connection-limits.md) §6.2。

### 4.3 迟滞带该设多大

`psp_lifecycle_quota_delta_bytes`：每次比较都采样，**skip 与否都采**。迟滞带要吞下的正是这个漂移分布，包含那些下限几乎没动的周期。

只有拿到这个分布，band 参数才是**算出来的**，不是拍出来的。

### 4.4 P 与每用户扇出

| 指标 | 含义 |
|---|---|
| `psp_user_client_count` | 每用户共享客户端数（成本模型里的 **P**） |
| `psp_sync_user_lifecycle_ms` | 单用户全量扇出墙钟 |

看 P50 对 P95：**均匀成本还是长尾问题，决定 Phase 1b 的并行化到底值多少。**

### 4.5 推送信号量（雪崩预警）

| 指标 | 含义 |
|---|---|
| `psp_push_sem_capacity` | 容量（默认 8） |
| `psp_push_sem_inflight` | 当前持槽数，含峰值 |
| `psp_push_sem_waiting` | 当前排队数，含峰值 |
| `psp_push_sem_wait_ms` | 等槽耗时 |
| `psp_push_sem_carryover_total` | **上一周期还没排空，新周期就开了**——每一次都是守卫压掉了一整轮推送 |
| `psp_push_suppressed_total` | 被守卫压掉的推送**条数**（carryover 是轮数） |

等待与服务时间**分开记**：这是排队问题，合在一起恰好会盖住 §1.4 要看的东西——

- 等待涨、服务时间平 → **积压**
- 两者一起涨 → 面板变慢

`carryover` 每涨一次，就是跨周期守卫压掉的一拍。两个计数一起看：**carryover 持平而 suppressed 一路涨，说明部署已经长期超出信号量容量**，不是偶发过载。

容量一并发布，是为了让快照自洽：「峰值 in-flight 8」只有在读者也知道容量是 8 时才意味着饱和，而那是个 admin 可调的设置。

### 4.6 Poll 周期

| 指标 | 含义 |
|---|---|
| `psp_poll_total` / `psp_poll_error_total` / `psp_poll_ms` | 周期计数与总耗时 |
| `psp_poll_stage_ms{stage=…}` | 分段耗时 |
| `psp_poll_users` | 扫描用户数 |
| `psp_poll_active_users` | **N**：本周期动了流量的用户数 |
| `psp_poll_panels` | 拉取面板数 |
| `psp_poll_floor_push_enqueued_total` | 真正入队的下限推送数 |

分段耗时原本只在 `PSP_LOG_LEVEL=debug` 下以日志存在。现在进直方图，于是它**不开 debug 也在**，且能当分布读，而不是「碰巧被日志抓到的那一轮」。

`psp_poll_panels` 是用来核对计划 §1.1「读侧是每面板一次、不是每节点一次」的——让它可对着真实部署检验，而不是靠重读循环体。

`active_users` 与 `floor_push_enqueued` 之间有个缺口：用户动了流量，也可能到不了入队（inbound 拉取失败被跳过、或已因配额停用）。**这个缺口本身有诊断价值**——它大，说明模型里的 N 高估了负载。

## 5. 已知限制

- **快照跨指标非原子。** 并发跑着的 poll 可能只被记了一半，两个相关计数最多差一个周期的量。要做成原子的就得在每次记录上加锁，用热路径的真实成本，去买一个以小时计的测量窗口根本不需要的一致性。
- **进程内，重启即清零。** 见 §3 的 `uptime_ms` 核对。
- **Vec 的标签空间必须有界。** 子项懒创建后永不回收。标签值只能是固定集合里的名字（操作名、阶段名），**绝不能是用户 ID 或面板地址**。这一条无人强制，是调用点的约定。
- **分位数是插值估计**，误差以桶宽为界。桶集取 1-2-5 序列，每个数量级三个点，所以任意位置的 P95 落在真值的约 1.6 倍以内。**估计值被钳在实测 max 以内**——分位数在数学上不可能超过样本最大值，而桶内插值可以：一个 3 微秒的孤立样本落在最低桶里，插值出来是该桶中点，可以是实测值的几十倍。钳位保证每个报出来的分位数都是「某个样本可能取到的数」。
- **样本极少时看 max 和 mean，不要看分位数。** n=1 时分位数只能落在该样本所在桶内的某处；`count` 就在同一个对象里，先看它。
