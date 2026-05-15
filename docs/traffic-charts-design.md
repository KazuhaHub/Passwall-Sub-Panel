# 流量统计图表功能设计文档

| 字段 | 值 |
|---|---|
| 文档版本 | 1.1 |
| 状态 | 开发中 |
| 创建时间 | 2026-05-14 |
| 最后更新 | 2026-05-15 |

---

## 1. 现状分析

### 1.1 当前功能

| 功能 | 状态 | 说明 |
|---|---|---|
| 流量采集 | ✅ 已有 | 每 5 分钟从 3X-UI 拉取流量数据 |
| Top-N 排行 | ✅ 已有 | 管理员查看流量排行 |
| 用户报告 | ✅ 已有 | 累计/周期/今日用量 |
| 手动设置用量 | ✅ 已有 | 管理员可手动调整 |
| 自动禁用 | ✅ 已有 | 超限自动停用 |

### 1.2 缺失功能

| 功能 | 影响 |
|---|---|
| ❌ 流量趋势图表 | 无法直观查看流量使用趋势 |
| ❌ 时间范围筛选 | 只能查看固定周期 |
| ❌ 单用户历史详情 | 无法查看用户历史流量变化 |
| ❌ 用户端流量页面 | 用户只能看到数字，没有图表 |

---

## 2. 设计目标

1. **管理员流量页面**：增加流量趋势图表（日/周/月）
2. **用户自助页面**：增加流量图表展示
3. **时间范围筛选**：支持自定义时间范围查询
4. **数据聚合**：支持按天/周/月聚合流量数据

---

## 3. 数据库设计

### 3.1 现有表结构（无需修改）

```sql
-- 已有表，存储流量快照
CREATE TABLE traffic_snapshots (
  id          BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id     BIGINT NOT NULL,
  up_bytes    BIGINT,      -- 上行（累计）
  down_bytes  BIGINT,      -- 下行（累计）
  total_bytes BIGINT,      -- 总计（累计）
  captured_at DATETIME,
  INDEX idx_user_time (user_id, captured_at)
);
```

### 3.2 数据聚合算法

聚合在 Go 代码中完成，不依赖 MySQL / SQLite 特定 SQL 语法。

核心规则：

1. 查询窗口为 `[since 00:00, until+1day 00:00)`，按服务器本地时区解释日期。
2. 额外读取 `since` 前最后一条快照作为第一段 baseline，避免第一天被当成从 0 开始。
3. 每个 bucket（日/周/月）取该 bucket 内最后一条快照，减去上一个有效快照。
4. 没有快照的 bucket 补 0，方便前端连续绘图。
5. 如果差值为负，说明 3X-UI 计数被重置或发生手动修正；该 bucket 按当前 bucket 末快照值计算，并保证结果不小于 0。
6. `week` 使用 ISO 周（周一 00:00 起），`month` 使用自然月。

注意：管理员“手动设置用量”会插入 synthetic snapshot，保证总量准确；但上行/下行拆分可能不代表真实方向流量，图表应以 total 为主。

---

## 4. 后端 API 设计

### 4.1 新增 API 端点

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/traffic/history` | 管理员流量历史（支持时间范围） |
| GET | `/api/admin/traffic/user/:id/history` | 单用户流量历史 |
| GET | `/api/user/me/traffic/history` | 用户自己的流量历史 |

### 4.2 请求参数

```
GET /api/admin/traffic/history?user_id=1&period=day&since=2026-04-01&until=2026-05-14
```

| 参数 | 类型 | 说明 |
|---|---|---|
| `user_id` | int64 | 可选，不传则返回所有用户汇总 |
| `period` | string | 聚合周期：`day` / `week` / `month`，默认 `day` |
| `since` | string | 起始日期 `YYYY-MM-DD`，默认 30 天前 |
| `until` | string | 结束日期 `YYYY-MM-DD`，默认今天 |

`since` 和 `until` 都是日期粒度，语义为包含两端日期；后端实际查询到 `until` 次日 00:00 前。

### 4.3 响应格式

```json
{
  "scope": "user",
  "user_id": 1,
  "period": "day",
  "since": "2026-04-01",
  "until": "2026-05-14",
  "items": [
    {
      "date": "2026-04-01",
      "up_bytes": 104857600,
      "down_bytes": 524288000,
      "total_bytes": 629145600
    },
    {
      "date": "2026-04-02",
      "up_bytes": 52428800,
      "down_bytes": 209715200,
      "total_bytes": 262144000
    }
  ]
}
```

所有用户汇总时：

```json
{
  "scope": "all",
  "period": "day",
  "since": "2026-04-01",
  "until": "2026-05-14",
  "users_count": 12,
  "items": []
}
```

---

## 5. 前端设计

### 5.1 管理员流量页面重构

**当前**：只有 Top-N 表格
**目标**：Tab 切换「排行榜」和「趋势图」

```
┌─────────────────────────────────────────────────────────┐
│  流量统计                                                │
├─────────────────────────────────────────────────────────┤
│  [排行榜]  [趋势图]                                      │
│                                                          │
│  ┌─ 排行榜 Tab ──────────────────────────────────────┐  │
│  │  Top [10 ▼]  [刷新]                                │  │
│  │  ┌────┬──────┬────────┬────────┬────────┐         │  │
│  │  │ #  │ 用户 │ 周期用量│ 今日   │ 累计   │         │  │
│  │  ├────┼──────┼────────┼────────┼────────┤         │  │
│  │  │ 1  │ user1│ 10 GB  │ 1 GB   │ 100 GB │         │  │
│  │  └────┴──────┴────────┴────────┴────────┘         │  │
│  └────────────────────────────────────────────────────┘  │
│                                                          │
│  ┌─ 趋势图 Tab ──────────────────────────────────────┐  │
│  │  用户: [全部用户 ▼]  周期: [日 ▼]  [最近30天 ▼]    │  │
│  │                                                    │  │
│  │  ┌────────────────────────────────────────────┐   │  │
│  │  │     📊 流量趋势图 (ECharts Line Chart)     │   │  │
│  │  │     X轴: 日期  Y轴: 流量 (GB)              │   │  │
│  │  │     上行 / 下行 / 总计 三条线               │   │  │
│  │  └────────────────────────────────────────────┘   │  │
│  │                                                    │  │
│  │  ┌────────────────────────────────────────────┐   │  │
│  │  │     📊 每日流量柱状图 (ECharts Bar Chart)  │   │  │
│  │  │     堆叠柱状图: 上行 + 下行                 │   │  │
│  │  └────────────────────────────────────────────┘   │  │
│  └────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

### 5.2 用户自助页面增强

**当前**：只显示数字
**目标**：增加流量图表

```
┌─────────────────────────────────────────────────────────┐
│  我的流量                                                │
├─────────────────────────────────────────────────────────┤
│  ┌─ 流量概览 ─────────────────────────────────────────┐ │
│  │  周期用量: 10.5 GB / 50 GB  ████████░░░░ 21%       │ │
│  │  今日用量: 1.2 GB                                  │ │
│  │  到期时间: 2026-06-01                               │ │
│  └─────────────────────────────────────────────────────┘ │
│                                                          │
│  ┌─ 使用趋势 ─────────────────────────────────────────┐ │
│  │  周期: [日 ▼]  范围: [最近7天 ▼]                    │ │
│  │                                                    │ │
│  │  ┌────────────────────────────────────────────┐   │ │
│  │  │     📊 流量趋势图 (ECharts Area Chart)     │   │ │
│  │  └────────────────────────────────────────────┘   │ │
│  └─────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
```

### 5.3 图表库选型

| 方案 | 优点 | 缺点 |
|---|---|---|
| **ECharts** | 功能强大，中文文档好 | 包体积较大（~800KB） |
| **Chart.js** | 轻量（~200KB），社区活跃 | 功能略少 |
| **ApexCharts** | 中等体积，现代 API | 中文文档少 |

**建议**：使用 **ECharts**，按需引入减少体积：
```typescript
import * as echarts from 'echarts/core'
import { LineChart, BarChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([LineChart, BarChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])
```

---

## 6. 实现步骤

### Phase 1：后端 API
1. `traffic_repo.go` 复用 `ListByUser` / `LastBefore` 读取原始快照
2. `traffic.go` 新增 `HistoryReport` 方法
3. `admin_traffic.go` 新增 `History` handler
4. `user_me.go` 新增 `TrafficHistory` handler
5. `router.go` 注册新路由

### Phase 2：前端图表
1. `web/src/api/traffic.ts` 新增历史 API 调用
2. 安装 ECharts 依赖：`npm install echarts`
3. `TrafficView.vue` 重构为 Tab 布局（排行榜 + 趋势图）
4. `MeView.vue` 增加流量图表组件
5. 创建可复用的 `TrafficChart.vue` 组件

### Phase 3：优化
1. 图表响应式适配（移动端）
2. 暗色主题适配
3. 数据缓存（避免重复请求）
4. 后端测试：首日 baseline、空日期补 0、负增量、周/月 bucket、用户权限隔离

---

## 7. 文件清单

### 新增文件
| 文件 | 说明 |
|---|---|
| `web/src/components/TrafficChart.vue` | 可复用的流量图表组件 |

### 修改文件
| 文件 | 修改内容 |
|---|---|
| `internal/adapters/mysql/traffic_repo.go` | 复用快照查询；必要时优化索引 |
| `internal/service/traffic/traffic.go` | 新增 HistoryReport 方法 |
| `internal/transport/http/handler/admin_traffic.go` | 新增 History handler |
| `internal/transport/http/handler/user_me.go` | 新增 TrafficHistory handler |
| `internal/transport/http/router.go` | 注册新路由 |
| `web/src/api/traffic.ts` | 新增历史 API |
| `web/src/views/admin/TrafficView.vue` | 重构为 Tab 布局 + 图表 |
| `web/src/views/user/MeView.vue` | 增加流量图表 |
| `web/package.json` | 添加 echarts 依赖 |
