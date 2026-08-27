# 连接限制：并发 IP 与设备数

> 状态：**已实现，实机验证（3X-UI 3.7.0 + xray-core 26.7.28）。**
> 关联：[ARCHITECTURE.md](ARCHITECTURE.md)、[inbound-ownership.md](inbound-ownership.md)、[3xui-compat.md](3xui-compat.md)、[data-plane-plan.md](data-plane-plan.md)。

## 1. 设计立场

**PSP 的领域模型是真相源，面板是兼容目标。**

字段按 PSP 想表达的意图命名和定义（`User.IPLimit` / `User.DeviceLimit`），**不跟任何面板的字段名或语义走**。各适配器把它翻译成自己面板能表达的部分，翻译不了的**通过 capability 显式声明缺口**——而不是静默不生效。

这条立场决定了下面几乎每个具体选择。

## 2. 领域模型

```go
User.IPLimit     int  // 并发源 IP 上限，0 = 不限
User.DeviceLimit int  // 绑定设备数上限，0 = 不限
```

**为什么 0 表示不限**：和既有的 `TrafficLimitBytes` 一致，也和面板侧编码一致（面板把 0 读作「无上限」）。于是 GORM AutoMigrate 给存量安装加列时，**零值天然等于「不限」，不需要任何回填**，升级前后行为完全一致。

**为什么是 per-user 而不是 per-group**：分组承载的是节点选择与布局，从不承载配额；`TrafficLimitBytes` 已经立了这个先例。

## 3. 推送契约：`domain.UserLifecycle`

推送链路不再是一串标量参数，而是一个契约类型：

```go
type UserLifecycle struct {
    Enable        bool
    ExpiryTime    int64
    QuotaHeadroom int64  // 本期剩余；不是给面板的数
    IPLimit       int
    DeviceLimit   int
}
```

**它是契约，不是参数打包。** PSP 自己的数据面才是设计目标，3X-UI / S-UI 是兼容目标——所以这个集合由「PSP 想强制什么」定义，各适配器再按能力翻译。往里加字段不应该波及五个函数签名。

### 3.1 `QuotaHeadroom` 始终是本期剩余

面板拿终身累计计数器做比较（见 [traffic-floor-defect.md](traffic-floor-defect.md)），所以必须重基。**重基只在唯一知道「是哪个 client 的计数器」的那一点发生**：

```go
want.PanelQuota(panelLifetime)   // = PanelQuotaCap(QuotaHeadroom, panelLifetime)
```

一个用户的 lifecycle 会扇出到多个 client，每个 client 有各自的计数器——所以这个转换**不能**折进结构体里，它是「per-user 意图」的「per-client 解析」。

## 4. `limitHwid`：从「故意不发」到「PSP 所有」

这个字段的历史值得完整记一笔，因为它是本立场的最佳例证。

**3.7.0 之前**：字段不存在。

**3.7.0 引入后，PSP 故意不发它**。原因不是疏忽：上游 `clients/update/:email` 会把**缺失的 key 绑成 0**，而 `EnforceHwidForSubID` 在 `limit <= 0` 时直接返回 `Allowed = true`——**即 PSP 每次推送都在静默关掉这个安全控制**。

那为什么不回显？因为回显更糟：`setClientLimitHwidByEmail` 把读到的值喂给 `trimClientHwidsForSubID`，后者会**删除超出上限的设备注册记录**。读-改-写窗口内的任何变化（管理员调高上限、一台设备刚注册）都会让陈旧值**永久销毁设备行**。

两害相权，当时选了「清零」——丢一个标量，好过删数据。

**所有权消灭了这个两难。** PSP 现在发的是**自己的意图值**（`User.DeviceLimit`），不是回显，**根本不存在读-改-写窗口**。于是 trim 做的正是设备上限该做的事。

> 这正是「领域模型为主、面板为辅」的价值：只要 PSP 拥有该字段，上游那个「缺失 key 绑 0」的缺陷就不再能伤到任何人。

## 5. 能力与兼容

| 能力 | xui | sui |
|---|---|---|
| `client.iplimit` | ✅ | ❌ |
| `client.devicelimit` | ✅ | ❌ |

**S-UI 两个都不支持**——它的 client 模型既没有并发 IP 上限也没有设备上限，`applySpec` 直接丢弃这两个字段。给 S-UI 面板上的用户设了限制，那里就是不生效。这通过 capability API 呈现，而不是让写入失败。

**xui 无条件声明两者**：字段是 PSP 所说协议的一部分，低于 3.7.0 的面板会**忽略**这个 key 而不是报错（gin 用 encoding/json 默认行为，从不开 `DisallowUnknownFields`）。

**但「某个面板构建是否真的执行它」是版本问题**，由 `docs/compat/v3.json` 回答——capability 是每适配器静态的，看不见眼前这台面板的版本。

### 5.1 `limitIp` 的执行依赖（上线前必验）

3X-UI 的并发 IP 限制走 **core 的 online-stats API**（不需要访问日志，这是好消息），但**超限后的动作是把 IP 排队交给 fail2ban**（`check_client_ip_job.go`）。

**也就是说节点上没装配 fail2ban，这个限制就不会真正拦截任何东西。** PSP 侧的推送是正确的，执行侧的前提在节点上。

## 6. 漂移愈合

两个上限是 PSP 所有的，所以**被人在面板界面上改动必须被改回来**，而不是被跳过。它们已进入 `SyncLifecycle` 的比较集合（`lifecycleWriteReason`），因此：

- 面板值与 PSP 意图不一致 → 重推，并在 `psp_lifecycle_sync_write_reason_total{reason=ip_limit|device_limit}` 上计数
- 一致 → 仍然命中 no-op skip，不会因为多了两个字段就每周期多一次写

## 6.2 缺口是被上报的，不是被吞掉的

声明能力只解决了一半：**S-UI 上的用户设了限制，写入照样成功、就是不生效。** 这正是能力列表本该防住的失败形状，所以 `SyncLifecycle` 在推送前检查：

```
PSP 想强制某个上限  且  目标面板没有对应能力  →  psp_capability_gap_total{capability=…} 计数 + 告警
```

两个设计选择：

- **只在 PSP 真的想强制时才算缺口。** 不限的用户落在不支持的面板上不是问题，把它计进去会让这个指标失去意义。
- **计数器是持久信号，日志每（面板, 能力）只打一次。** 这个状态是稳态的——直到运维方把用户挪走或升级面板才会消失，每周期为每个受影响的客户端打一行会把别的都埋掉。

**缺口是警告，不是拒绝**：同一次写入里的 enable / 到期 / 配额，那台面板执行得好好的。

## 7. 已验证范围

实机（3X-UI 3.7.0 源码编译 + xray-core 26.7.28 `5ca6f4b`），走 PSP 自己的适配器：

- 创建携带两个上限 ✅
- 更新携带两个上限 ✅
- **一次无关的更新（只改到期时间）不再抹掉设备上限** ✅ ← 本功能存在的核心回归
- 0 能清空两个上限（不限的用户不会凭空继承一个上限）✅

用例：`internal/adapters/xui/client_live_limits_test.go`（env 门控，CI 默认跳过）。

## 8. 未做

- **组级默认值**：分组不承载配额，见 §2。真要做应连 `TrafficLimitBytes` 一起，作为独立的一次改动。
- **SSO 新用户默认值**：`NewUserDefaults` 目前只带流量上限。加进去是小事，但属于 SSO 配置面的扩展，不混在本次里。
- **UI 上的能力提示**：编辑界面暂不在字段旁提示「该用户所在面板不支持此项」。服务端已经在推送点上报缺口（§6.2），能力也已通过 `/api/admin/servers` 的 `capabilities` 暴露给前端；缺的是把两者接起来的呈现。
