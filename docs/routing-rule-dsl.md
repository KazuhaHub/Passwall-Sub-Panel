# PSP 抽象路由规则语法

> 状态：设计规范草案。本文定义 PSP 路由规则的抽象边界、命名原则、Mihomo 与 sing-box 的能力交集，以及当前渲染器的实现状态。文中“计划支持”不表示代码已经实现。

## 1. 目标与定位

PSP 的主规则沿用 Mihomo/Clash 的行式规则语法，并以 `content` 作为唯一真相源。PSP 不另外发明一套与现有配置割裂的中立语法，而是在保持 Mihomo 规则兼容的基础上，增加能够表达 sing-box 独有条件和动作的扩展，形成：

> **PSP 抽象规则语法 = Mihomo 规则语法的兼容超集。**

一条普通路由规则保持以下基本形态：

```yaml
- TYPE,VALUE,TARGET
```

例如：

```yaml
- DOMAIN-SUFFIX,example.com,🚀 节点选择
- SRC-IP-CIDR,192.168.1.0/24,DIRECT
- AND,((NETWORK,UDP),(DST-PORT,443)),⚡ QUIC控制
- MATCH,🐟 漏网之鱼
```

PSP 将规则解析成统一的内部表示，再分别编译为 Mihomo 行式规则和 sing-box 结构化路由规则。不能等价生成到目标内核的规则必须出现在兼容性诊断中，不得无提示地改变路由语义。

`REMATCH-NAME` 与 `SUB-RULE` 直接写在主规则中：Mihomo 保持原始写法和顺序；sing-box 不生成这两类规则。子规则和 Rematch 出站本身仍以结构化字段保存，因为它们分别对应 Mihomo 根级 `sub-rules` 和 `proxies` 配置，而不是另一份主规则。

参考上游文档：

- [Mihomo 路由规则](https://wiki.metacubex.one/config/rules/)
- [sing-box 路由规则](https://sing-box.sagernet.org/zh/configuration/route/rule/)
- [sing-box 规则动作](https://sing-box.sagernet.org/zh/configuration/route/rule_action/)

## 2. 命名原则

### 2.1 Mihomo 名称是规范名称

两端具有相同或可可靠转换的能力时，PSP 使用 Mihomo 的规则类型名称。例如 PSP 使用 `DOMAIN-SUFFIX`，而不把 sing-box 字段名 `domain_suffix` 暴露为另一种并列语法。

### 2.2 sing-box 独有名称是暂定扩展名

当前仅 sing-box 支持的匹配条件，使用与 Mihomo 风格一致的大写连字符名称，例如：

```yaml
- NETWORK-TYPE,wifi,DIRECT
- WIFI-SSID,HomeWiFi,DIRECT
- SOURCE-MAC-ADDRESS,00:11:22:33:44:55,DIRECT
```

这些名称属于 PSP 扩展，不代表 Mihomo 已经支持对应规则。

### 2.3 新增 Mihomo 支持时的名称归一化

**如果 Mihomo 将来原生支持某项当前仅 sing-box 支持的能力，PSP 必须以 Mihomo 最终采用的规则类型名称为准。**

具体规则如下：

1. Mihomo 名称与 PSP 暂定名称相同：该规则直接从 sing-box 专属提升为通用规则。
2. Mihomo 名称与 PSP 暂定名称不同：Mihomo 名称成为新的规范名称。
3. 已发布的 PSP 暂定名称可作为兼容别名保留一个明确的迁移周期，但新建、格式化、导出和文档示例一律使用 Mihomo 名称。
4. 别名必须在解析阶段归一化为同一种内部规则，不得让两个名称长期形成不同语义。
5. 名称迁移必须记录版本、兼容期限和诊断信息，不能静默改变已有规则含义。

例如，假设 PSP 先定义了 `SOURCE-HOSTNAME`，而 Mihomo 后来把相同能力命名为 `SRC-HOSTNAME`，则 `SRC-HOSTNAME` 成为规范名称；旧的 `SOURCE-HOSTNAME` 仅作为兼容输入存在。

### 2.4 不用内核前缀污染规则名称

一般不使用 `SINGBOX-*` 或 `MIHOMO-*` 前缀。规则的适用内核属于能力元数据，不属于匹配条件本身。编辑器可以显示“通用”“仅 Mihomo”“仅 sing-box”标签，生成器根据目标内核决定是否编译。

## 3. 匹配条件 Venn 表

下表按语义能力分类。“共同支持”表示可以表达基本相同的匹配意图，不保证字段格式、平台限制和边界行为完全一致。

| Mihomo 独有 | 两者共同支持 | sing-box 独有 |
|---|---|---|
| `IP-SUFFIX` | `DOMAIN` | `IP-VERSION` (`ip_version`) |
| `IP-ASN` | `DOMAIN-SUFFIX` | `PROTOCOL` (`protocol`) |
| `SRC-IP-ASN` | `DOMAIN-KEYWORD` | `CLIENT` (`client`) |
| `SRC-IP-SUFFIX` | `DOMAIN-REGEX` | `IP-IS-PRIVATE` (`ip_is_private`) |
| `IN-PORT` | `IP-CIDR` / `IP-CIDR6` | `SRC-IP-IS-PRIVATE` (`source_ip_is_private`) |
| `IN-TYPE` | `SRC-IP-CIDR` | `NETWORK-TYPE` (`network_type`) |
| `REMATCH-NAME` | `DST-PORT` | `NETWORK-IS-EXPENSIVE` (`network_is_expensive`) |
| `DSCP` | `SRC-PORT` | `NETWORK-IS-CONSTRAINED` (`network_is_constrained`) |
| `PROCESS-NAME-WILDCARD` | `IN-NAME` ↔ `inbound` | `INTERFACE-ADDRESS` (`interface_address`) |
| `PROCESS-NAME-REGEX`¹ | `IN-USER` ↔ `auth_user` | `NETWORK-INTERFACE-ADDRESS` (`network_interface_address`) |
| `SUB-RULE`² | `PROCESS-NAME` | `DEFAULT-INTERFACE-ADDRESS` (`default_interface_address`) |
| `src` 附加参数³ | `PROCESS-PATH` | `WIFI-SSID` (`wifi_ssid`) |
|  | `PROCESS-PATH-REGEX` | `WIFI-BSSID` (`wifi_bssid`) |
|  | `UID` ↔ `user_id` | `PREFERRED-BY` (`preferred_by`) |
|  | `NETWORK` | `DNS-SERVER-ADDRESS` (`dns_server_address`) |
|  | `RULE-SET` | `DNS-SEARCH-DOMAIN` (`dns_search_domain`) |
|  | `AND` / `OR` / `NOT` | `SOURCE-MAC-ADDRESS` (`source_mac_address`) |
|  | `MATCH` | `SOURCE-HOSTNAME` (`source_hostname`) |
|  | Android 包名匹配⁴ | `CLASH-MODE` (`clash_mode`) |
|  | GeoIP / GeoSite⁵ | `USER` (`user`) |

说明：

1. sing-box 没有通用的 `process_name_regex`；其 `package_name_regex` 主要表达 Android 包名，不能完整承接 Mihomo 的普通进程名正则。
2. sing-box 可用 `rule_set` 或逻辑规则组织规则，但没有与 Mihomo `SUB-RULE` 完全相同的子规则控制流。
3. Mihomo 的 `src` 把目标 IP 规则改为来源 IP 匹配；sing-box 使用独立的 `source_*` 字段。
4. Mihomo 在 Android 上允许 `PROCESS-NAME` 系列匹配包名；sing-box 使用独立的 `package_name` 和 `package_name_regex`。
5. Mihomo 原生提供 `GEOSITE`、`GEOIP` 和 `SRC-GEOIP`。sing-box 的内联 GeoIP/GeoSite 字段已经废弃，现代配置应通过 `rule_set` 引用对应规则集。

## 4. PSP 通用规则语法

### 4.1 可直接或可靠转换的通用类型

以下类型作为 PSP 通用规则的稳定基础：

```text
DOMAIN
DOMAIN-SUFFIX
DOMAIN-KEYWORD
DOMAIN-REGEX
IP-CIDR
IP-CIDR6
SRC-IP-CIDR
DST-PORT
SRC-PORT
IN-NAME
IN-USER
PROCESS-NAME
PROCESS-PATH
PROCESS-PATH-REGEX
UID
NETWORK
RULE-SET
AND
OR
NOT
MATCH
```

这些名称均采用 Mihomo 命名。sing-box 编译器负责转换为对应结构化字段。

### 4.2 需要受控转换的通用类型

以下类型两端有相同或相近意图，但转换存在额外条件：

```text
DOMAIN-WILDCARD
PROCESS-PATH-WILDCARD
GEOSITE
GEOIP
SRC-GEOIP
SUB-RULE
no-resolve
```

具体约束：

| PSP/Mihomo 写法 | sing-box 目标 | 转换约束 |
|---|---|---|
| `DOMAIN-WILDCARD` | `domain_regex` | 必须正确转义普通字符，并将 `*`、`?` 转成等价正则 |
| `PROCESS-PATH-WILDCARD` | `process_path_regex` | 必须按 Mihomo 通配符语义生成锚定正则 |
| `GEOSITE` | `rule_set` | 生成对应 GeoSite 规则集引用；带属性分类需验证存在可用规则集 |
| `GEOIP` | `rule_set` | 生成对应 GeoIP 规则集引用 |
| `SRC-GEOIP` | `rule_set` + 来源匹配 | 必须启用来源 IP 匹配语义 |
| `SUB-RULE` | 规则展开、逻辑规则或规则集 | 只有证明顺序与返回行为等价时才能转换，否则标记不兼容 |
| `no-resolve` | 无直接同名字段 | 必须验证不会触发额外 DNS 解析；不能仅删除文本后宣称等价 |

## 5. 各内核独有匹配条件

### 5.1 Mihomo 独有条件

PSP 保留 Mihomo 原生写法，不为 sing-box 人为改名：

```text
IP-SUFFIX
IP-ASN
SRC-IP-ASN
SRC-IP-SUFFIX
IN-PORT
IN-TYPE
REMATCH-NAME
DSCP
PROCESS-NAME-WILDCARD
PROCESS-NAME-REGEX
SUB-RULE
src
```

其中部分能力未来可能获得 sing-box 等价编译；在等价性得到验证前，应标记为“仅 Mihomo”或“有条件转换”。

### 5.2 sing-box 独有条件的 PSP 暂定写法

| PSP 暂定名称 | sing-box 字段 | 含义 |
|---|---|---|
| `IP-VERSION` | `ip_version` | 独立限制 IPv4 或 IPv6 |
| `PROTOCOL` | `protocol` | 匹配探测到的协议 |
| `CLIENT` | `client` | 匹配探测到的客户端类型 |
| `IP-IS-PRIVATE` | `ip_is_private` | 目标地址是否为私有地址 |
| `SRC-IP-IS-PRIVATE` | `source_ip_is_private` | 来源地址是否为私有地址 |
| `NETWORK-TYPE` | `network_type` | Wi-Fi、蜂窝网络、以太网等网络类型 |
| `NETWORK-IS-EXPENSIVE` | `network_is_expensive` | 是否为计费网络 |
| `NETWORK-IS-CONSTRAINED` | `network_is_constrained` | 是否为受限网络 |
| `INTERFACE-ADDRESS` | `interface_address` | 指定接口上的地址范围 |
| `NETWORK-INTERFACE-ADDRESS` | `network_interface_address` | 指定网络类型接口的地址范围 |
| `DEFAULT-INTERFACE-ADDRESS` | `default_interface_address` | 默认接口的地址范围 |
| `WIFI-SSID` | `wifi_ssid` | Wi-Fi SSID |
| `WIFI-BSSID` | `wifi_bssid` | Wi-Fi BSSID |
| `PREFERRED-BY` | `preferred_by` | 首选端点或网络能力 |
| `DNS-SERVER-ADDRESS` | `dns_server_address` | DNS 服务器地址 |
| `DNS-SEARCH-DOMAIN` | `dns_search_domain` | DNS 搜索域 |
| `SOURCE-MAC-ADDRESS` | `source_mac_address` | 来源设备 MAC 地址 |
| `SOURCE-HOSTNAME` | `source_hostname` | 来源设备主机名 |
| `CLASH-MODE` | `clash_mode` | sing-box Clash API 模式 |
| `USER` | `user` | Linux 用户名 |

这些名称必须遵守 §2.3：**未来 Mihomo 添加同类规则时，无条件以 Mihomo 的正式名称作为 PSP 规范名称。**

## 6. 规则动作

### 6.1 共同动作

| PSP/Mihomo 表达 | sing-box 表达 | 说明 |
|---|---|---|
| 代理组或出站名称 | `action: route` + `outbound` | 路由到指定出站 |
| `DIRECT` | `route` 到 direct 出站 | 直连 |
| `REJECT` | `action: reject` | 拒绝连接 |
| `REJECT-DROP` | `reject` + `method: drop` | 丢弃数据包，具体限流行为可能不同 |

### 6.2 Mihomo 特有动作和控制流

```text
PASS
SUB-RULE
Rematch 出站与 REMATCH-NAME
```

`PASS` 表示继续匹配，不能错误转换为 sing-box 的 direct 出站。

### 6.3 sing-box 特有动作

```text
bypass
hijack-dns
route-options
sniff
resolve
```

其中 `route-options` 还可设置目标地址/端口覆盖、网络策略、UDP 行为、TLS 分片和 TLS ClientHello 伪装等参数。

动作不应伪装成代理组名称。PSP 计划采用显式的类 Mihomo 扩展形式：

```yaml
- PROTOCOL,dns,ACTION,HIJACK-DNS
- DOMAIN-SUFFIX,example.com,ACTION,SNIFF
- DOMAIN-SUFFIX,example.com,ACTION,RESOLVE,strategy=prefer_ipv4
- DOMAIN-SUFFIX,example.com,ACTION,ROUTE-OPTIONS,override_port=8443
```

动作语法仍处于设计阶段；实现前必须定义参数转义、多个非最终动作的执行顺序，以及与最终路由动作的组合方式。

## 7. 生成与兼容性规则

每条规则至少应具有以下能力元数据：

```text
both       两端可等价生成
mihomo     仅 Mihomo 支持
sing-box   仅 sing-box 支持
partial    可转换，但存在明确限制
```

生成器应遵守：

1. 通用规则分别编译到两个内核，不能依靠文本偶然兼容。
2. 单端规则只进入对应内核的输出。
3. 无法等价转换的规则必须产生可定位到原始规则行的诊断。
4. 默认模式可以在明确报告后忽略目标内核不支持的规则；严格模式应拒绝生成。
5. 不能把未知规则、解析失败和“目标内核不支持”混为同一种静默跳过。
6. 生成摘要至少报告通用、Mihomo 专属、sing-box 专属、部分转换和被忽略的规则数量。

示例：

```text
✓ 126 条通用规则
✓ 3 条 Mihomo 专属规则
✓ 2 条 sing-box 专属规则
⚠ 1 条有条件转换规则
⚠ Mihomo 输出忽略 2 条 sing-box 专属规则
```

## 8. 当前实现状态

本节描述当前代码，不代表 §4 的目标能力已经全部落地。

### 8.1 已生成到 Mihomo 和 sing-box

```text
DOMAIN
DOMAIN-SUFFIX
DOMAIN-KEYWORD
IP-CIDR
IP-CIDR6
SRC-IP-CIDR
DST-PORT
PROCESS-NAME
NETWORK
MATCH
GEOIP
```

### 8.2 部分实现

| 类型 | 当前限制 |
|---|---|
| `AND` | 仅支持当前 sing-box 转换器已认识的简单子条件；同一 sing-box 字段不能重复 |
| `GEOSITE` | 转换为 `geosite-*` 远程规则集；带 `@` 属性的分类会被忽略 |
| `no-resolve` | 解析器会识别并跳过该参数，但尚未保留它的完整语义 |

`REMATCH-NAME` 和 `SUB-RULE` 已允许写入统一 `content` 并完整输出到 Mihomo；它们属于 Mihomo 专属控制流，sing-box 生成时有意跳过。短期分支版本使用过的 `mihomo_rules` 字段会在 YAML/API 读取时前置合并到 `content`，规范化保存后不再写出旧字段。

### 8.3 Mihomo 可原样输出、sing-box 尚未转换

```text
DOMAIN-REGEX
SRC-PORT
IN-NAME
IN-USER
PROCESS-PATH
PROCESS-PATH-REGEX
UID
RULE-SET
OR
NOT
DOMAIN-WILDCARD
PROCESS-PATH-WILDCARD
SRC-GEOIP
SUB-RULE
```

当前 Mihomo 模板直接插入主规则，因此上游支持的规则通常能够原样输出。sing-box 渲染器则只转换已登记的类型；当前未登记类型会被跳过。实现 §7 的兼容性诊断前，维护者必须特别注意这种行为。

## 9. 实施顺序建议

1. 建立主规则 AST、类型注册表和能力元数据。
2. 补齐有直接字段映射的 `DOMAIN-REGEX`、`SRC-PORT`、`IN-NAME`、`IN-USER`、`PROCESS-PATH`、`PROCESS-PATH-REGEX`、`UID`、`RULE-SET`。
3. 实现通用逻辑规则 `AND`、`OR`、`NOT`，并覆盖嵌套、重复字段和否定语义测试。
4. 实现通配符到正则的受控转换。
5. 明确 `SRC-GEOIP`、`SUB-RULE` 和 `no-resolve` 的等价性边界。
6. 添加 sing-box 独有条件和显式动作语法。
7. 在编辑器、预览和生成接口中加入逐行兼容性诊断与严格模式。

任何新增规则类型都应同时更新本文档、解析器测试、两个目标内核的生成测试和兼容性摘要测试。
