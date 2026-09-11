# ADR 0029：Xray core 升级按客户端兼容矩阵放行，不跟随 latest

- **状态**：已接受
- **日期**：2026-09-10
- **相关代码**：Passwall-Node `corecatalog/`、`internal/core/xray/compat.go`、`internal/core/xray/compiler.go`
- **前置决策**：[ADR 0024](0024-psp-native-node-backend.md) —— 自研节点后端同时接手 xray-core 兼容责任

## 背景

PSP 同时生成 Mihomo/Clash、sing-box 和 URI-list 订阅，因此“Xray 自己的新客户端能连”不是节点
升级的充分条件。服务端 core、传输协议和订阅客户端构成一个三维兼容面；只验证 Xray 配置能启动，
会把握手期的不兼容留给订阅用户发现。

2026-09-10 对上游源码和当前发布版的复核得到两处分界：

1. Xray `26.7.11` 的 `af7eb680...` 变更为 REALITY 配置补了默认
   `minClientVer = 26.3.27`。Mihomo / sing-box 使用自己的版本编码，因此会被当成旧 Xray 客户端。
   `26.7.x` 可通过服务端显式设置 `minClientVer: "0.0.0"` 消除这层版本检查。
2. Xray `26.9.8`、`26.9.9` 使用的 `github.com/xtls/reality` 版本包含
   `8cdf7bf9...`：服务端要求 ClientHello 先带 `X25519MLKEM768` key share，再接受可选 X25519；
   缺少前者会直接结束握手。这是库内硬检查，不受 `minClientVer` 配置控制。复核时的 sing-box
   REALITY 客户端不能满足这条要求；Mihomo 默认也不满足，但存在下述受限组合。

Mihomo 的 `reality-opts.support-x25519mlkem768: true` 是**客户端 proxy 配置**，不是 Xray inbound
配置。它的实现语义是“不再从 ClientHello 删除 ML-KEM”，不会替一个本来不含 ML-KEM key share 的
指纹凭空补上它。Clash Verge Rev 2.5.2 携带的 Mihomo 1.19.29 和当前稳定 Mihomo 1.19.30
均已实测；可用组合是 `client-fingerprint: chrome` + 该开关。其他固定指纹和 `random` 不能据此
宣称兼容。这个字段也不在
`vless://` 分享链接中，因此通用 URI-list 不能声明这项 Mihomo 专用修复；使用自身 Xray core 的
URI 消费端可能兼容，但在完成逐客户端握手前只能标为“未核验”，不能笼统承诺可用。

这两项只直接影响 **VLESS + REALITY**。不能把它误写成“Xray 不再支持 Clash Verge / sing-box”：
VLESS+TLS、VMess、Trojan、Shadowsocks 不因这项 REALITY 检查失效，其他传输若不兼容应各自建条目。

核验入口：

- Xray 发布：[XTLS/Xray-core releases](https://github.com/XTLS/Xray-core/releases)
- Xray `26.7.11` 报告：[XTLS/Xray-core#6477](https://github.com/XTLS/Xray-core/issues/6477)
- Mihomo 的 ML-KEM 跟踪：[MetaCubeX/mihomo#3193](https://github.com/MetaCubeX/mihomo/issues/3193)
- sing-box 的版本门跟踪：[SagerNet/sing-box#4403](https://github.com/SagerNet/sing-box/issues/4403)
- sing-box REALITY 客户端实现：[reality_client.go](https://github.com/SagerNet/sing-box/blob/testing/common/tls/reality_client.go)

Issue 是互操作现象的交叉证据；最终门限以对应 tag 的依赖版本和源码 diff 为准。

## 决策

### 1. 默认发行固定 Xray `26.6.27`

Passwall-Node 的兼容优先通道固定到最后一个已验证可同时服务 Xray、Mihomo 和 sing-box REALITY
客户端的版本 `26.6.27`。Dockerfile、二进制安装包、PSP 选择器、Node 安装器和 CI 使用同一个
编译期嵌入的版本目录，不解析
GitHub `latest`，也不在节点上后台自升 core。

固定版本不是永不升级。它的含义是：先通过矩阵，再改 pin；版本号变化本身就是一项需要 review 的
代码变更。

### 2. 精确版本目录和 REALITY 编译门均 fail-closed

| Xray 目标版本 | 级别 | 默认行为 |
|---|---|---|
| `26.6.27` | 推荐 | 正常生成；默认版本 |
| `26.7.28` | 已核验 | 强制生成 `realitySettings.minClientVer = "0.0.0"`；Xray、Mihomo、sing-box 握手均通过 |
| `26.9.9` | 受限 | 默认拒绝 REALITY 配置，报码 `reality_client_incompatible`；显式确认后才可启用 |
| 其他任意版本（包括 `latest`） | 未列入目录 | 安装和编译均拒绝，不使用版本范围猜测兼容性 |

门只在配置确实含 REALITY listener 时触发。这样将来为非 REALITY 部署试用较新 core，不会被一个
无关协议拦住。

2026-09-10 的实际数据面测试在 `darwin/arm64` 上完成，均为
VLESS/TCP/REALITY/XTLS Vision，并通过本地 SOCKS 代理发出真实 TLS 1.3 HTTPS 请求：

| 服务端 | Xray 客户端 | Mihomo | sing-box |
|---|---|---|---|
| `26.6.27` | `26.6.27` 通过 | `1.19.30` 通过 | `1.14.0` 通过 |
| `26.7.28` + `minClientVer=0.0.0` | `26.7.28` 通过 | `1.19.30` 通过 | `1.14.0` 通过 |
| `26.9.9` | `26.9.9` 通过 | `1.19.29`、`1.19.30` 均通过（`chrome` + ML-KEM 开关） | `1.14.0` 预期失败，确认不兼容 |

结构化证据与客户端版本保存在 Passwall-Node `corecatalog/catalog.json`，PSP 选择器直接展示同一份
数据；不能只改文档或前端标签来提高发行级别。

### 3. 限定客户端模式必须显式选择

高级管理员可以选择受限客户端模式，越过 REALITY 门。当前允许集合只能是：(a) 已兼容的新
Xray 客户端；或 (b) Mihomo YAML 输出固定 `client-fingerprint: chrome` 并生成
`reality-opts.support-x25519mlkem768: true`。sing-box 必须从该节点的可选输出中排除；URI-list
在具体消费端完成握手前标为“未核验”，不得作为可用性承诺。不能继续生成一个已知无法连接的条目。

这个选择不得由升级动作隐式开启，不得伪装成普通 patch upgrade，也不得改变 PSP 默认生成的
Mihomo / sing-box / URI-list 均可用这一承诺。管理界面和审计事件必须明确显示兼容面已收窄。

Mihomo 的兼容变换按每个节点所属的面板/原生 agent 实际 core 版本分别计算，不是订阅级全局开关。
因此同一份订阅可以同时包含旧 Xray 节点和 `26.9.8+` 节点：只有后者固定 `chrome` 并输出
`support-x25519mlkem768: true`。不得为了覆盖混合版本而给所有 REALITY 节点统一开启该字段；Mihomo
实现保留关闭默认值正是因为携带该 key share 会使一部分旧 REALITY 服务端异常。

### 4. core 发布闸门同时验证四层

每次修改 pin 至少验证：

1. 官方校验和/签名和来源；
2. `xray run -test` 接受 Passwall-Node 编译的配置；
3. Xray、当前稳定 Mihomo、当前稳定 sing-box 对每个承诺的协议/传输完成真实握手；
4. 流量计数、在线 IP、到期停用和配置回滚仍工作。

只做第 2 项不能证明订阅兼容。只有四项全绿，版本才能进入默认通道；实验通道也必须保存失败项和
限定范围，不能用“最新版”代替矩阵。

## 后果

- 节点不会因为上游发版就在无人确认时切断第三方 REALITY 客户端；同时不会漏掉 Mihomo 已有的
  `chrome + support-x25519mlkem768` 受限兼容路径。
- 默认 pin 可能暂时拿不到上游的新功能或安全修复；遇到安全公告时必须做风险分级：升级并缩窄兼容
  面、回移补丁，或临时禁用受影响协议，不能悄悄保持旧版。
- 兼容矩阵现在是发布输入，不是文档备注。未来恢复 ML-KEM 互操作时，更新已审计上界和默认 pin 要
  与握手测试同一个变更提交。
- `minClientVer: "0.0.0"` 只放宽客户端版本字符串判断，不绕过 REALITY 的密钥认证；仍需在安全审计
  中保留这一区别。

## 考虑过但未采用的方案

**始终跟随 Xray latest。** 上游 latest 的验收目标是 Xray 自身，不包含 PSP 对 Mihomo 和 sing-box
的产品承诺。两者不是同一个兼容集合。

**只在文档提示用户降级。** 握手失败发生在数据面，控制面仍会显示配置已应用；这会制造一个“全绿但
用户断网”的静默故障。可以在编译时确定的风险必须在编译时阻断。

**把所有 Xray `26.9+` 一律永久拉黑。** 未来版本可能恢复互操作。对未审计版本 fail-closed，同时
允许经过矩阵测试抬高上界，比永久版本比较更准确。
