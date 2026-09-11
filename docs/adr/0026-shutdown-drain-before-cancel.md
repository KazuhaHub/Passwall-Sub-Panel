# ADR 0026：`App.Shutdown` 必须先排空 HTTP，再取消后台上下文

- **状态**：已接受（v3.6.3-beta.11）
- **日期**：2026-08-14
- **相关代码**：`internal/app/app.go`（`App.Shutdown`、`asyncDispatcher`、`bgRootCtx` / `bgCancel` / `bgWG` 的装配）、`internal/transport/http/middleware/audit.go`（`AuditWrites`）、`internal/transport/http/handler/sub.go`（`logSubAsync`）、`internal/pkg/safego`（`GoTracked`）

## 背景

面板的两条高频写入——**审计日志**（每一次 POST/PUT/PATCH/DELETE）和**订阅访问日志**（`/sub` 每一次拉取）——都不在请求线程里写。原因是纯性能的，两处注释都写明了：`sub_logs` 是公共端点上写入频率最高的表（N 个用户每几分钟轮询一次，同步 INSERT 就是把 N×fsync 挂进请求预算），审计 INSERT 在 SQLite 上每次写请求要多花 5–50ms、有争用时更糟。

于是两者都走 `asyncDispatcher.Go(name, fn)`：它用 `safego.GoTracked` 起一个受 panic 防护、并登记进 `bgWG` 的协程，把 **`bgRootCtx`**（构造时注入的后台根上下文）交给 `fn`。

这就在关停时埋了一个顺序陷阱。`Shutdown` 手上有三件事要做：排空在途 HTTP 请求、取消 `bgRootCtx` 让后台循环退出、等 `bgWG`。看起来这三件事可以任意排列——"先把后台停了再收 HTTP"甚至更符合直觉（先关水源再关水龙头）。

但**在途请求是在排空阶段才跑完 handler 的**，而 handler 跑完的最后一件事，恰恰是 `asyncDispatcher.Go(...)` 派发那条审计 / 订阅日志写入。如果 `bgRootCtx` 在排空之前就被取消，这些写入拿到的是一个**早已取消的 context**——协程照样起来了、`bgWG` 照样等到了它、`safego` 也不会报错，但 DB 调用第一时间就以 `context canceled` 返回。**最后一批审计与订阅日志被静默丢弃**，日志里没有任何痕迹。

这正是旧的实现。它的症状极其难归因：关停期的审计缺失只有事后查审计表才会发现，而且"少的那几条"看起来完全像是"关停时刚好没有请求"。

## 决策

**`App.Shutdown` 严格按三段执行，顺序是语义的一部分：**

1. **`a.server.Shutdown(ctx)`** —— 停止接受新连接，排空在途请求。此时 `bgRootCtx` **仍然存活**，所以每个刚跑完的 handler 都能成功派发它的异步审计 / 订阅日志写入。
2. **`a.bgCancel()`** —— 取消 `bgRootCtx`。所有后台循环在各自的 `select` 里看到 `ctx.Done()` 并退出。
3. **等 `a.bgWG`，上限是调用方给的 deadline** —— 保证一个卡住的 SMTP 或 3X-UI HTTP 调用不会留下半提交的事务或泄漏的连接，同时给第 1 步刚派发出去的那批写入留出完成时间。

超时不阻塞：`bgWG` 没在 deadline 内清空时记一条 warn 并返回，把整体时限交回给调用方的 `ctx`。返回值是 `server.Shutdown` 的错误——第 1 步的结果才是"HTTP 有没有干净收尾"的答案。

第 3 步"等 `bgWG`"必须排在第 2 步之后：`bgWG` 里既有后台循环也有异步写入，先取消才能让循环退出，否则 `Wait` 会一直等到 deadline。

## 后果

**正面**：关停期最后一批审计与订阅日志能落库；后台循环有确定的退出路径；卡住的外部调用有上界。

**代价**：关停多花一个"排空在途请求"的时长。对一个订阅面板来说，在途请求本来就短。

**运维**：日志里出现 `shutdown: background workers did not exit before deadline` 说明有后台调用（多半是 SMTP 或某个不可达的 3X-UI）超过了 deadline，进程仍会退出。

**必须保持的不变量**（未来修改时不能破坏）：

- 三段顺序不能重排。特别是 `bgCancel()` 不能提到 `server.Shutdown` 之前——那正是被修掉的旧行为，且**不会有任何测试变红**。
- 异步写入必须继续从 `asyncDispatcher` 拿 `bgRootCtx`，而不是自己造一个上下文；`asyncDispatcher.Go` 里对 nil ctx 回退到 `context.Background()` 只是构造期的兜底，不是给调用方用的口子。
- 异步写入必须继续经 `safego.GoTracked` 登记进 `bgWG`。裸 `go` 起的协程 `Shutdown` 等不到，会被进程退出直接截断。

## 考虑过但未采用的方案

- **先 `bgCancel()` 再排空**（旧顺序）：直接导致本 ADR 描述的静默丢日志。它读起来更"干净"（先停源头），这正是它危险的地方。
- **把审计 / 订阅日志改回同步写**：能彻底绕开这个顺序问题，但代价是把 fsync 挂回请求路径——`sub_logs` 是公共端点上写入最频繁的表，审计 INSERT 在 SQLite 上每次写请求 5–50ms。两处注释都记录了这次权衡，异步化本身就是为修这个而做的。
- **用 `context.Background()` 派发异步写入**，使其完全不受 `bgRootCtx` 影响：确实能免疫顺序问题，但也就同时丢掉了统一的取消语义——一个卡住的 DB 写将只能靠 `Shutdown` 的 deadline 兜底，而 deadline 到期时 `Shutdown` 只记一条 warn 就返回、进程随后退出，那条写入照样丢，只是丢得更不可预测。当前方案里它至少是**有界且可解释**的。
- **在 `Shutdown` 里单独 flush 一次审计队列**：需要引入一个当前不存在的队列抽象；把顺序排对就已经免费达到同样效果。
