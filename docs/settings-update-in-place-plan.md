# Settings 写路径改造计划：UPDATE-in-place（仿 Cloudreve，消除自增 id 跳号）

> 状态：草案 / 待实施
> 影响范围：`internal/adapters/sqlstore/settings_kv_repo.go`（+ 测试、changelog）
> 迁移：无（零数据迁移、读路径不变、可直接 revert）

## 1. 背景

`settings` 是一张 KV 表 `settings(id BIGINT autoinc PK, type, name, value, encrypted, updated_at)`，
唯一键为 `UNIQUE(type, name)`。当前保存逻辑（[settings_kv_repo.go](../internal/adapters/sqlstore/settings_kv_repo.go) 的 `Save`）
是**一条批量 `INSERT ... ON DUPLICATE KEY UPDATE`**，一次写入全部 ~46 行。

MySQL InnoDB 把 `INSERT ... ON DUPLICATE KEY UPDATE` 归类为 *mixed-mode insert*：
在逐行判断冲突之前，会按 VALUES 行数**预留**自增值；命中唯一键走 UPDATE 分支的行，
其预留 id 被丢弃且不回收。于是**每次保存都烧掉 ~46 个 id**，导致 id 出现大段空洞
（如 `38–46` → `217` → `921–968` → `2070–2076`）。

> 这是 InnoDB 的文档化预期行为，不是 bug。`id` 是没人 join 的代理键（真正的键是 `(type,name)`），
> 跳号对功能**零影响**。本计划纯属"看着干净 + 对齐成熟同类项目做法"，**非紧急**。

## 2. 参考：Cloudreve 怎么做（已核对真实源码）

| | Cloudreve v3 (GORM) | Cloudreve v4 (ent) |
|---|---|---|
| 表 | `Setting{ gorm.Model; Type; Name(unique); Value }` | `Setting{ name(unique); value(text) }`（去掉了 type） |
| 主键 | 自增 `id`（gorm.Model） | 自增 `int id`（ent 默认） |
| 写 | 事务内逐行 `UPDATE settings SET value=? WHERE name=?` | `Setting.Update().Where(Name(k)).SetValue(v)`，逐行 UPDATE |
| 跳号 | **无**（从不 INSERT，行在安装时预播种） | **无**（同上） |

结论：Cloudreve 两代都保留没人 join 的自增 id（ORM 默认），**靠"行预先存在 + 运行时纯 UPDATE"避免跳号**，
而不是靠 schema 更聪明。这正是本计划要借鉴的写模型。

来源：
[v3 models/setting.go](https://raw.githubusercontent.com/cloudreve/Cloudreve/3.8.3/models/setting.go)、
[v3 service/admin/site.go](https://raw.githubusercontent.com/cloudreve/Cloudreve/3.8.3/service/admin/site.go)、
[v4 ent/schema/setting.go](https://github.com/cloudreve/Cloudreve/blob/master/ent/schema/setting.go)、
[v4 inventory/setting.go](https://github.com/cloudreve/Cloudreve/blob/master/inventory/setting.go)。

## 3. 方案选择

采用 **contained 方案**：把"已存在则 UPDATE、缺失才 INSERT"的逻辑收进 `Save` 内部，
**只改 1 个文件**，不动 schema、不加启动 seed。

理由：
- 本项目现有语义是「设置行懒创建」（新 key 直到第一次 save 才落库，平时 `applyUISettingsDefaults` 兜底）。
  contained 方案**保留这个语义**，行为与今天最接近。
- diff 最小、**零数据迁移**、**读路径 `Load` 完全不变**。
- 不触碰 `EnsureSchema` 与 `ConfigureSecretKey` 的初始化顺序（`Save` 在密钥配置之后才被调用，加密照常）。

> 备选（不采用）：完整 Cloudreve 版——在 `EnsureSchema` 后做幂等 ensure-seed 让行始终预先存在，
> `Save` 退化为纯 UPDATE-only。见 §7。
>
> 备选（不采用）：删掉代理 `id`，把 `(type,name)` 设为主键（Unleash 式），从根上没有自增列。
> 用户已选择"仿 Cloudreve"，故保留代理 id。

## 4. 改动清单

### 4.1 `internal/adapters/sqlstore/settings_kv_repo.go` — 重写 `Save`（核心）

```go
func (r *kvSettingsRepo) Save(ctx context.Context, s ports.UISettings) error {
    now := time.Now()
    rows := /* 按现有逻辑构建 ~46 个 settingRow，含加密 —— 不变 */

    return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        // ① 一条 SELECT 取出当前已存在的 key 集合（确定性，不依赖 RowsAffected）
        var existing []settingRow
        if err := tx.Model(&settingRow{}).Select("type", "name").Find(&existing).Error; err != nil {
            return err
        }
        have := make(map[string]bool, len(existing))
        for _, e := range existing {
            have[e.Type+"\x00"+e.Name] = true
        }

        // ② 已存在 → 纯 UPDATE（绝不 mint id）；缺失 → 收集起来批量 INSERT
        var missing []settingRow
        for _, row := range rows {
            if have[row.Type+"\x00"+row.Name] {
                // 必须用 map：struct 会跳过零值，导致 value=""/encrypted=false 写不进去
                if err := tx.Model(&settingRow{}).
                    Where("type = ? AND name = ?", row.Type, row.Name).
                    Updates(map[string]any{
                        "value":      row.Value,
                        "encrypted":  row.Encrypted,
                        "updated_at": now,
                    }).Error; err != nil {
                    return err
                }
            } else {
                missing = append(missing, row)
            }
        }

        // ③ 仅对缺失的新 key 批量 INSERT；OnConflict 兜并发首插竞态
        if len(missing) > 0 {
            return tx.Clauses(clause.OnConflict{
                Columns:   []clause.Column{{Name: "type"}, {Name: "name"}},
                DoUpdates: clause.AssignmentColumns([]string{"value", "encrypted", "updated_at"}),
            }).Create(&missing).Error
        }
        return nil
    })
}
```

正确性要点（实施时逐条落实）：

1. **`Updates` 用 `map[string]any`，不能用 struct** —— 否则 GORM 跳过零值，`value=""` / `encrypted=false` 不会被写入。
2. **不依赖 `RowsAffected`** 判断存在性 —— MySQL 对"值未变"的 UPDATE 返回 0，会误判为缺失。改用前置 `SELECT type,name`，完全确定。
3. **原子性不变** —— 仍是单事务全有全无，更新表头注释（原 `settings_kv_repo.go:107-109` 处）。
4. **MySQL/SQLite 双兼容** —— `Updates` 与 `clause.OnConflict` 在两种方言下都成立（CI 用 SQLite 内存库）。
5. `Load`、加密字段、缓存层 `cachingSettingsRepo`、`KnownSettingNames`、迁移器 `copySettingsKV` **均不改**。

### 4.2 `internal/adapters/sqlstore/settings_kv_repo_test.go` — 新增测试

- `TestSave_DoesNotGrowAutoIncrement`（**核心保证**）：建表 → 首次 Save 记 `MAX(id)` → 连续 Save 多次（改值/不改值各来一遍）→ 断言 `MAX(id)` 不变。
- `TestSave_FreshEmptyTable_InsertsAll`：空表 Save → 全部行被插入，`Load` 能读回。
- `TestSave_NewKeyGetsInserted`：删掉一行模拟"升级新增 key" → Save → 该行被重建且只新增 1 个 id。
- `TestSave_EncryptedRoundTrip`：`geo_ip_update_token` 经 Save→Load 解密一致。
- 复用/确认现有 round-trip 测试仍通过。

### 4.3 文档 / 变更记录

- `CHANGELOG.md`：一行 fix —— "settings 保存改为按 (type,name) UPDATE-in-place，不再烧自增 id（消除 id 跳号），新 key 仍懒插入"。
- `docs/ARCHITECTURE.md` 的 Settings storage 段落补一句写路径说明（可选）。

## 5. 兼容性与回滚

- **零迁移**：现有部署历史 gap 原样冻结，从此 id 不再增长；旧行走 UPDATE、新 key 走 INSERT，全部向后兼容。
- **读路径零变化**，前端 / 消费方无感。
- **回滚**：单文件改动，`git revert` 即可；回退到旧二进制后老的 upsert 在同一张表照常工作（只是恢复烧 id），**前后双向兼容**。
- 可选（非本次、纯美观）：一次性收缩历史 gap —— MySQL `ALTER TABLE settings AUTO_INCREMENT = <max+1>;`；
  SQLite 用 `VACUUM` / 重建。**建议不做**，功能上毫无收益，且下次新 key 插入又会从新基准继续。

## 6. 新增设置的行为（升级后）

加新设置的开发流程不变：给 `UISettings` 加字段 + 在 `settingDescriptors` 加一行。新版二进制上线后：

1. 新 key 的 `(type,name)` 不在老库 → 第一次 `Save` 时被 INSERT，为它 mint 恰好 **1 个** id；
2. 老的 ~46 行仍走 UPDATE → 不烧 id；
3. 此后该新 key 已存在 → 后续保存都走 UPDATE。

即「懒创建」语义与今天一致：新设置在被首次保存前按 `applyUISettingsDefaults` 默认值生效；
id 计数器一生只按"历史上新增过的设置总数"缓慢增长，不再大段跳号。**无需为新设置做任何迁移或手动 seed。**

## 7. 验证

```bash
go test ./internal/adapters/sqlstore/...                 # 新老测试
go build -ldflags="-s -w" -o psp ./cmd/panel          # 编译
```

手动：连点两次"保存设置"，在 phpMyAdmin 里确认 `MAX(id)` 不再变化。

## 8. 可选增强（更贴近 Cloudreve，默认不做）

若希望"DB 里始终有全部行"（方便 SQL 浏览、升级后新 key 立即可见而不必等首次 save），
可在 `EnsureSchema`（[schema.go](../internal/adapters/sqlstore/schema.go) 的 `EnsureSchema`）AutoMigrate 之后
加一个**幂等 ensure-seed**：

1. `SELECT type,name` 取现存 keys；
2. 与全部描述符 diff，**仅插入缺失项**（值用默认 / 空；加密字段存 `""`，故不需要密钥 ——
   规避 `ConfigureSecretKey` 在 `EnsureSchema` 之后调用的顺序问题）。

之后 `Save` 可退化为更纯粹的 UPDATE-only（去掉 §4.1 的 ③ 分支）。

不默认采用：contained 方案（§4）已达成"不跳号"目标，且 diff 更小、不引入启动期耦合。

## 9. 工作量

小。核心 1 个文件 + 测试 + changelog；读路径与 schema 不动，零迁移。
