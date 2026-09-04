# 接入第一个节点

把一台 3X-UI 接进 PSP，直到一个用户能拿到可用订阅。

**本文每一步都在实机上跑过**（3X-UI 3.7.0 + xray 26.7.28 + PSP v3.9.2-beta.16），不是从代码推出来的。踩到的坑单独标了出来——它们都是真的踩到过。

---

## 0. 先明确一件事：PSP 不能用 `127.0.0.1` 连面板

PSP 连上游面板走的 HTTP 客户端**刻意拒绝 loopback**（`internal/pkg/safehttp`）。面板地址是管理员填进数据库、可以指向任何地方的字符串，那道拨号守卫存在的头号理由就是它——顺带挡掉云厂商的 `169.254.169.254` 元数据端点。

| 面板地址 | 能否连上 |
|---|---|
| `http://127.0.0.1:2053` | ❌ `refusing connection to non-public address 127.0.0.1` |
| `http://10.0.0.5:2053`、`http://192.168.1.10:2053` | ✅ RFC1918 私网段刻意放行 |
| `https://panel.example.com:2053` | ✅ |
| `http://169.254.169.254/...` | ❌ link-local（云元数据） |

**即使 3X-UI 和 PSP 在同一台机器上也一样**——填那台机器的内网 IP，不要填 loopback。

> 主机名不在保存时校验：`localhost` 能存进去，但会在「测试连接」时才失败。守卫是按解析后的 IP 判的。

---

## 1. 节点机器：装 3X-UI

版本 **≥ 3.1.0**（3.1.0 起 `/inbounds/list` 把 settings 改成嵌套对象，PSP 适配的就是这版格式）。推荐 3.7.0。

装完做三件事：

**a. 面板别只监听 `127.0.0.1`。** 让它监听 `0.0.0.0` 或那台机器的内网 IP，然后用防火墙把面板端口限制到只有 PSP 能访问。上一节的规则在面板侧同样成立——面板只绑 loopback，PSP 一样连不上。

**b. 保持单机，不要再套 3X-UI 自己的 Nodes 聚合。** 指向一台已配子节点的 central panel 时，`clientStats` 会是跨节点聚合值，和 PSP「一 inbound 一 node」的模型冲突。

**c. 装 fail2ban，并且不要设 `XUI_ENABLE_FAIL2BAN`。**

这是并发 IP 上限（`limitIp`）能否真正生效的前提，而它**从面板的读写里完全看不出来**：面板照常保存、照常返回成功，然后什么也不封。两道闸任一不满足就整条执行路径关闭：

| 闸 | 条件 |
|---|---|
| ① 二进制 | 节点上 `fail2ban-client -h` 能跑通 |
| ② 环境变量 | `XUI_ENABLE_FAIL2BAN` **未设**，或等于**字面量 `true`** |

**②是个陷阱**：设成 `1` 看着像「启用」，实际把执行关掉了（上游只认字面量 `"true"`）。

PSP 会主动探测这两道闸并把结论显示在服务器列表里，所以装完不用自己验证——看徽标即可（§6）。

---

## 2. 节点机器：建一个 API token

在 3X-UI 里新建 token，**必须 `admin` scope、不设过期**。

3.7.0 起 `monitor` / `node-sync` scope 的 token 访问 PSP 需要的端点会 403，而 **PSP 把 403 当永久失败、不重试**——表现是这块面板彻底不工作，而不是慢。

> **不要用用户名/密码模式。** 3.7.0 上实测不可用：`POST /login` 直接回 HTTP 403 空 body，PSP 侧显示
> `login: unexpected end of JSON input (raw: )` —— 既不提 auth 也不提 CSRF，看着像面板坏了。
> 原因是顺序：PSP 在登录**成功之后**才去取 CSRF token，而 3.7.0 把 `/login` 本身也 gate 住了。
> 详见 [`3xui-compat.md`](3xui-compat.md)。

---

## 3. PSP：添加服务器

**服务器 → 新建**：

| 字段 | 填什么 |
|---|---|
| 面板类型 | 3X-UI |
| 名称 | 任意，**全局唯一**（重名返回 409） |
| URL | `{http\|https}://{IP或域名}:{面板端口}{webBasePath}` |
| 认证方式 | **token** |
| API token | 上一步那个 admin scope token |

**URL 的形状是最容易错的一项**：

- 要带面板的 `webBasePath`（例如 `https://panel.example.com:54321/mySecretPath`）
- **不要**带 `/panel`、`/panel/api` 或 `/login` —— 适配器自己会拼
- 末尾斜杠无所谓，会被去掉

webBasePath 填错有一个专门的症状：**cookie 模式下登录能成功，但每个 API 调用持续 401**。适配器的报错文本里点了名。

> 自签证书的面板：勾上该面板的「跳过 TLS 校验」。这只放松证书校验，SSRF 拨号守卫不变。

---

## 4. PSP：测试连接

点「测试连接」。成功应该看到 `panel_version`、xray 版本/状态，以及一个 IP 上限执行状态。

失败对照：

| 报错 | 原因 |
|---|---|
| `refusing connection to non-public address` | 填了 loopback / link-local，见 §0 |
| `login: unexpected end of JSON input (raw: )` | 用了用户名/密码模式，见 §2 |
| 持续 401 但登录成功 | webBasePath 填错，见 §3 |
| 403 | token scope 不对（必须 admin），见 §2 |

---

## 5. PSP：建节点

**注册服务器不会产生任何节点**，要去「节点」页，**先选刚才那台面板**（未纳管列表必须带面板才有内容）。

两条路：

| | 做什么 | 什么时候用 |
|---|---|---|
| **导入** | 收编面板上**已有**的 inbound。不往面板写 inbound，只是接管：核对存在性、把线上配置抓成 PSP 的本地快照（此后 **PSP 是配置的真相源**）、建节点行、后台补发已有用户 | 面板上已经有配好的 inbound |
| **新建**（页头 `+`） | PSP 在面板上**真的建一个新 inbound**，拿回 inbound id，再建节点行（建库失败会把远端 inbound 回滚掉） | 从零开始 |

细节：
- 「未纳管」= 该面板上没有被任何 PSP 节点认领的 inbound，**减去** PSP 渲染不了的协议（wireguard / socks / dokodemo / http 会被过滤）
- 面板临时不可达时「新建」不报错，返回 `202 queued` 转成后台任务
- 「地区」是必填项

---

## 6. 回头看一眼服务器列表

节点状态下方可能出现一个 IP 上限徽标：

| 徽标 | 含义 | 怎么办 |
|---|---|---|
| 无 | 正常生效，或面板低于 3.7.0（没有上报路由） | — |
| **IP 上限不生效**（红） | `XUI_ENABLE_FAIL2BAN` 被设成了 `true` 以外的值，或没装 fail2ban | 回 §1c |
| 仅断连（蓝） | Windows 节点：会断连但不封 IP，对方可立刻重连 | 正常，知道就行 |
| 无法确认（灰） | 本该能回答的面板没回答——**探测本身出了问题**，不代表限制失效 | 查面板可达性 |

探测挂在流量轮询上，周期 `cron_traffic_pull_minutes`（**默认 5 分钟**，管理员可调），不是独立定时器。点「测试连接」会立刻重探一次。

---

## 7. PSP：建用户，拿订阅

**用户 → 新建**——**分组是必填的**（用默认的 `default` 即可）。

然后 **系统设置 → 公网基地址** 填 `https://你的PSP域名`。

**不填的话订阅链接会是 `http://127.0.0.1:8788/sub/...`**，发给用户没法用。这一条实测踩到过。

拉一次订阅确认节点渲染出来了：

```yaml
proxies:
  - name: Test Node JP
    server: 10.0.0.5
    port: 30001
    type: vless
    uuid: ...
```

> **订阅渲染出来 ≠ 真的能连**。渲染只依赖 PSP 的本地快照 + 用户 UUID；客户端下发到面板是**异步**的（同步任务循环，30 秒一轮）。刚建完用户就拉订阅，可能节点已经在列表里、面板上的 client 还没到。等一轮再试。

---

## 已知的空白

诚实列出来，免得你以为是自己配错了：

- **设备数上限在 PSP 架构下从不生效**（任何面板版本）。3X-UI 只在客户端拉取**面板自己的订阅端点**时才认设备，而 PSP 接管了订阅。见 [`connection-limits.md`](connection-limits.md) §4.1。
- **fail2ban 只探到「装了没」和环境变量**，不检查 `3x-ipl` jail 是否存在/启用。手工装的 fail2ban 可能缺 `x-ui.sh` 建的 `filter.d/3x-ipl.conf`。
- **IP 上限的断连半边只支持** `vmess / vless / trojan / shadowsocks / hysteria`，其他协议只打一行 warning。目前没有任何界面提示。
- **xray-core 的存活状态被记录但没人处理**：没有指标、告警或徽标，而 IP 上限依赖 core 的 online-stats API。
- **S-UI 面板没有任何 IP 上限执行状态**——该探测只对 3X-UI 生效。
