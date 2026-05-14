# 流量统计图表功能设计文档

| 字段 | 值 |
|---|---|
| 文档版本 | 1.0 |
| 状态 | 待开发 |
| 创建时间 | 2026-05-14 |

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

### 3.2 数据聚合查询

**按天聚合**（从快照计算每日增量）：
```sql
-- 方法：取每天最后一条快照与前一天最后一条快照的差值
WITH daily AS (
  SELECT 
    user_id,
    DATE(captured_at) as date,
    MAX(total_bytes) as end_total,
    MAX(up_bytes) as end_up,
    MAX(down_bytes) as end_down
  FROM traffic_snapshots
  WHERE user_id = ? AND captured_at >= ?
  GROUP BY user_id, DATE(captured_at)
)
SELECT 
  d1.date,
  d1.end_total - COALESCE(d2.end_total, 0) as daily_total,
  d1.end_up - COALESCE(d2.end_up, 0) as daily_up,
  d1.end_down - COALESCE(d2.end_down, 0) as daily_down
FROM daily d1
LEFT JOIN daily d2 ON d2.date = d1.date - INTERVAL 1 DAY
ORDER BY d1.date;
```

**注意**：此查询可在后端 Go 代码中实现，避免依赖数据库特定语法。

---

## 4. 后端 API 设计

### 4.1 新增 API 端点

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/traffic/history` | 管理员流量历史（支持时间范围） |
| GET | `/api/admin/traffic/history/:id` | 单用户流量历史 |
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

### 4.3 响应格式

```json
{
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
1. `traffic_repo.go` 新增 `AggregateByDay/Week/Month` 方法
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

---

## 7. 文件清单

### 新增文件
| 文件 | 说明 |
|---|---|
| `web/src/components/TrafficChart.vue` | 可复用的流量图表组件 |

### 修改文件
| 文件 | 修改内容 |
|---|---|
| `internal/adapters/mysql/traffic_repo.go` | 新增聚合查询方法 |
| `internal/service/traffic/traffic.go` | 新增 HistoryReport 方法 |
| `internal/transport/http/handler/admin_traffic.go` | 新增 History handler |
| `internal/transport/http/handler/user_me.go` | 新增 TrafficHistory handler |
| `internal/transport/http/router.go` | 注册新路由 |
| `web/src/api/traffic.ts` | 新增历史 API |
| `web/src/views/admin/TrafficView.vue` | 重构为 Tab 布局 + 图表 |
| `web/src/views/user/MeView.vue` | 增加流量图表 |
| `web/package.json` | 添加 echarts 依赖 |

---

## 8. 估算工作量

| 阶段 | 工作量 |
|---|---|
| 后端 API | 2-3 小时 |
| 前端图表 | 3-4 小时 |
| 测试调优 | 1-2 小时 |
| **总计** | **6-9 小时** |
