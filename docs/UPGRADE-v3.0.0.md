# 升级到 v3.0.0：数据库重构

> **历史 V3 工具手册，不是 V4 操作指南。** 下文的 `psp migrate` 仅存在于冻结的 V3 二进制/镜像中；执行时固定使用 [v3.9.2](https://github.com/KazuhaHub/Passwall-Sub-Panel/releases/tag/v3.9.2)，不要从当前 `main` 构建或使用滚动 `:beta` 标签。`v4.0.0-beta.2` 起的迁移收敛已移除该工具，并对旧命令明确拒绝启动；已发布的 `v4.0.0-beta.1` 标签保持不变。V3→V4 请阅读 [V4 升级指南](UPGRADE-v4.md)，正常启动自动迁移。

> v3.0.0 是一次破坏性的数据库 schema 重构（KV `settings` 主表、`xui_clients` 改名为 `user_xui_clients`、`panel_name` 冗余列删除、新增 `lifetime/last_raw/period_baseline` 字段等）。**v3.0.0 主程序只识别新 schema**，不会原地升级旧库。
>
> 升级方式：**side-by-side 新库**（参考 Cloudreve `drive → drive_v2` 升级模式）—— 旧库**永远不被新版主程序触碰**，可作为永久 backup；迁移由 v3.0.0 主程序自带的 `psp migrate` 子命令完成；admin 在 `config.yaml` 改一行 `database` 字段切换。

适用版本：**当前 ≤ v2.5.x → v3.0.0+**

## 跨大版本升级须知

按本项目的版本升级政策（详见 [docs/ARCHITECTURE.md §17](ARCHITECTURE.md)）：**不支持跨大版本跳级**。

- 当前 ≤ v2.5.x → v3.0.0：本文档适用，一步完成
- 当前 ≤ v2.5.x → V4：先使用本文的冻结 V3 工具导入并成功启动最终 V3，再按 [V4 升级指南](UPGRADE-v4.md) 操作
- 当前 v3.0.0 → v3.x.x（minor / patch）：直接换二进制，**不用跑 migrate**
- 当前 V3 → 迁移收敛后的 V4：先成功启动 `v3.9.2` / `v3.9.2-beta.20`，备份，再正常启动 V4；**不运行 `psp migrate`**

V3 的 minor / patch 演进可能包含启动时的兼容 schema 转换；必须让最终 V3 成功启动，不能只下载其二进制后立即切到 V4。

---

## 0. 升级前必读

1. **AES-GCM 加密字段**（SMTP 密码、SAML SP 私钥、OIDC client secret、3X-UI panel API token / 密码）的密文会原样搬到新库。新版主程序使用 `config.yaml` 的 `jwt_secret`（或 `PSP_JWT_SECRET` 环境变量）作为 secret key material 解密。**新 `jwt_secret` 必须和旧版完全一致**，否则解密会失败、SMTP / SSO / panel 接管全部断掉。

2. **旧库零修改**。迁移程序只读旧库 → 写新库。旧库一行都不动，理论上随时可以切回旧版主程序。

3. **V2→V3 工具仅留在冻结 V3 与 Git 历史中**。`v4.0.0-beta.2` 起删除该工具；必要的最终 V3→V4 桥接由正常启动执行，不替换成另一个 `psp migrate` 子命令。详见 [docs/ARCHITECTURE.md §17](ARCHITECTURE.md)。

4. **新库名 admin 自己定**。下面示例用 `psp_v3`（或 SQLite `panel_v3.db`），你可以叫任何名字；迁移程序通过 `--src` / `--dst` 参数接收。

5. **流量图表历史会清零**。v3.0.0 重做了流量存储为 `raw + hourly UTC` 两层 rollup 流水线，旧的 5 分钟快照表（`traffic_snapshots` / `client_traffic_snapshots` / `node_traffic_snapshots`）**不迁移**，新库从空表起步、面板启动后 5 分钟开始攒新数据。
   - **不丢的**：用户 / 节点上的 `lifetime_*_bytes` 累计计数（quota 计算、"all-time used" 都基于这个，跟快照表无关）
   - **会丢的**：历史曲线图（"上个月 5 号 14 点流量是多少" 这种细节）
   - 想保留的话先在旧版面板里截图存档；v3.0.0 启动后旧的曲线就回不来了

---

## 1. 通用前置步骤（任何部署方式都做）

### 1.1 备份旧库

**MySQL**：
```bash
mysqldump -u root -p --single-transaction --routines --triggers passwall \
  > passwall-backup-$(date +%F).sql
```

**SQLite**：
```bash
cp /opt/psp/data/panel.db /opt/psp/data/panel-backup-$(date +%F).db
# 或 Docker 命名卷：
docker run --rm -v psp-data:/data -v "$PWD":/backup alpine \
  cp /data/panel.db /backup/panel-backup-$(date +%F).db
```

旧库本身是永久 backup，但额外离线 dump 一份仍推荐（防 mysqldump 漏掉触发器等极端场景）。

### 1.2 记录当前 `jwt_secret`

```bash
grep '^jwt_secret' /opt/psp/config/config.yaml
# 或
docker exec psp grep '^jwt_secret' /app/config/config.yaml
```

把这个值原样复制下来 —— 新版 `config.yaml` 必须用同一个值。

---

## 2. 二进制部署升级

适用：直接跑 `psp` 二进制（systemd 或裸跑），数据存 SQLite 或 MySQL。

### 2.1 准备冻结 V3 二进制

从 [v3.9.2 发行页](https://github.com/KazuhaHub/Passwall-Sub-Panel/releases/tag/v3.9.2) 下载对应平台的 `psp` 并核对发行校验和。迁移工具内置于该 V3 二进制的 `migrate` 子命令中。

如果自行编译，使用隔离的 `v3.9.2` 标签源码，先构建前端再构建后端；**当前 `main` 已不提供此历史工具**。将核验过的 V3 二进制复制到生产机。

### 2.2 停旧版主程序 + 替换二进制

```bash
sudo systemctl stop psp                  # 或裸跑：kill 进程
mv /opt/psp/psp /opt/psp/psp.bak         # 备份旧二进制以备回滚
cp ./psp /opt/psp/psp
```

### 2.3 跑迁移

**SQLite 场景**：

```bash
cd /opt/psp
./psp migrate --driver=sqlite \
  --src=./data/panel.db \
  --dst=./data/panel_v3.db
```

**MySQL 场景**：先创建空目标库：

```bash
mysql -u root -p -e 'CREATE DATABASE psp_v3 CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'
```

然后跑迁移：

```bash
./psp migrate --driver=mysql \
  --src='user:pw@tcp(127.0.0.1:3306)/passwall?charset=utf8mb4&parseTime=true' \
  --dst='user:pw@tcp(127.0.0.1:3306)/psp_v3?charset=utf8mb4&parseTime=true'
```

迁移会打印 plan + 每张表的进度 + `Migration complete`。如果 mid-way 失败，按提示 DROP 目标库重跑（迁移首步会写一个 `_migration` sentinel 行，重跑会被 guard 拦住直到 DROP）。

> 也可以加 `--dry-run` 看每张表行数，不实际写入。

### 2.4 改 config.yaml 指向新库

```bash
sudo -u psp vi /opt/psp/config/config.yaml
```

**SQLite**：把 `mysql.dsn` 一段改成（或新增）：

```yaml
mysql:
  dsn: "sqlite:./data/panel_v3.db"
```

**MySQL**：把 `mysql.database` 改成新库名：

```yaml
mysql:
  host: "127.0.0.1"
  port: 3306
  user: "psp"
  password: "your_password"
  database: "psp_v3"        # ← 从 passwall 改成 psp_v3
```

**`jwt_secret` 保持不变**（必须和旧版一致）。

### 2.5 启动 v3.0.0

```bash
sudo systemctl start psp
sudo journalctl -u psp -f
```

观察日志，确认：
- `traffic poll start` 周期性出现，无 decrypt 错误
- 管理后台能登录（SSO 不报 `decrypt secret` 错误）
- 节点 / 用户列表显示正常 panel 名

---

## 3. Docker 部署升级

适用：通过 Docker Compose 部署。历史迁移必须使用固定 V3 镜像，不使用滚动通道。

### 3.1 拉取新 image

先将现有 Compose 的 PSP `image:` 固定为 `ghcr.io/kazuhahub/passwall-sub-panel:v3.9.2`，其余卷挂载、配置和环境变量保持不变，再执行：

```bash
cd /opt/Passwall-Sub-Panel
docker compose pull psp
```

确认实际使用的是冻结 `v3.9.2` 镜像；不要用 `:latest` / `:beta` 的未来指向来推断工具版本。

### 3.2 停容器

```bash
docker compose stop psp
```

### 3.3 跑迁移

**迁移工具内置在 v3.0.0 image 里**作为主程序的 `migrate` 子命令，直接 `docker compose run` 即可 —— 不需要 host 上编译 Go，不需要 volume 桥接。

> 用 `docker compose run --rm` 而不是 `docker exec`，因为容器已经 stop 了。`--rm` 跑完即删 ephemeral 容器；volume 共享 image 的卷挂载（compose 自动处理）。

**SQLite + named volume（默认 compose 配置）**：

```bash
docker compose run --rm psp migrate --driver=sqlite \
  --src=/app/data/panel.db \
  --dst=/app/data/panel_v3.db
```

容器内 `/app/data/` 对应 host 的 `psp-data` 命名卷，所以新旧 SQLite 文件都进同一个卷，主程序起来后能直接读到。

**MySQL（外部数据库）**：先建空库：

```bash
mysql -u root -p -e 'CREATE DATABASE psp_v3 CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'
```

然后从容器内跑迁移（DSN 用容器视角的连接串 —— compose 用了 `network_mode: host`，所以 `127.0.0.1` 直接可达）：

```bash
docker compose run --rm psp migrate --driver=mysql \
  --src='user:pw@tcp(127.0.0.1:3306)/passwall?charset=utf8mb4&parseTime=true' \
  --dst='user:pw@tcp(127.0.0.1:3306)/psp_v3?charset=utf8mb4&parseTime=true'
```

### 3.4 改 config.yaml 指向新库

```bash
sudo vi /opt/Passwall-Sub-Panel/config/config.yaml
```

**SQLite**：
```yaml
mysql:
  dsn: "sqlite:/app/data/panel_v3.db"
```

注意路径是容器内路径 `/app/data/panel_v3.db`（对应 `psp-data` 卷的 `/data/panel_v3.db`）。

**MySQL**：把 `database` 字段从 `passwall` 改成 `psp_v3`。`host` 通常是 `127.0.0.1`（compose 用了 `network_mode: host`）。

### 3.5 启动 v3.0.0

```bash
docker compose up -d psp
docker compose logs -f psp
```

观察日志，同 2.5。

---

## 4. 升级后验证清单

按顺序过一遍：

- [ ] 主程序日志没有 `decrypt secret` 错误
- [ ] 管理员能登录后台
- [ ] 节点列表 / 用户列表渲染正常，每个 panel 显示正确 name
- [ ] 改一个 panel 的 name → 立即在所有视图生效（旧版是冗余列要等下次轮询才更新）
- [ ] 流量轮询日志正常 (`traffic poll start ... users=X panels=Y`)
- [ ] 全局设置页面打开，数值都还在（SMTP / SSO / 限流 / 站点品牌 / 通知阈值 / 流量快照保留天数）
- [ ] Admin → 设置 → 通用 找到新增字段：**流量快照保留天数**（默认 180）+ **到期前 N 天提醒** + **剩余流量 < N% 时提醒**（后两个从 mail_settings 搬过来的）
- [ ] 跑一次 SSO 登录测试（如启用），确认 SAML SP 私钥 / OIDC client secret 解密 OK
- [ ] 跑一次邮件测试（如启用 SMTP）
- [ ] 订阅 URL 拉取正常
- [ ] 等一个 cron 周期（5 分钟），用户流量看板数字正常更新

---

## 5. 失败回滚

**主程序启动失败 / 数据明显出问题**：

二进制：
```bash
sudo systemctl stop psp
mv /opt/psp/psp.bak /opt/psp/psp           # 恢复旧二进制
sudo vi /opt/psp/config/config.yaml         # 把 database 改回旧库名（passwall）
sudo systemctl start psp
```

Docker：
```bash
docker compose down
# 把 compose 文件里的 image 标签固定回旧版本（如 :v2.5.5）
# 改回 config.yaml 的 database 字段
docker compose up -d
```

旧库完全没动过，回滚后状态等于升级前。

**迁移程序中途崩溃**：

```bash
# MySQL
mysql -u root -p -e 'DROP DATABASE psp_v3; CREATE DATABASE psp_v3 CHARACTER SET utf8mb4;'

# SQLite
rm /opt/psp/data/panel_v3.db
```

然后重跑迁移即可。`_migration` sentinel 会确保半成品状态被检测到。

---

## 6. 清理旧库（升级稳定一周后）

观察一周确认 v3.0.0 稳定后，可以释放旧库占用的空间。

**MySQL**：
```bash
mysql -u root -p -e 'DROP DATABASE passwall;'
```

**SQLite**：
```bash
rm /opt/psp/data/panel.db
# Docker 命名卷场景
docker run --rm -v psp-data:/data alpine rm /data/panel.db
```

**保留** `passwall-backup-*.sql` 一段时间（建议至少 30 天），万一发现某个边界数据不对还能挖回来。

---

## 7. 常见问题

**Q: 迁移程序报 "dst has 2 rows in `settings` already"**

A: dst 库不是全新的（含 `_migration` sentinel 或其他数据）。按 §5 的 DROP+CREATE 重来即可。

**Q: 启动新版报 `decrypt secret` 错误**

A: `config.yaml` 的 `jwt_secret` 跟旧版不一致。回去对一遍（包括 `PSP_JWT_SECRET` 环境变量覆盖）。

**Q: 管理后台节点列表 panel 名为空**

A: 新版从 in-memory pool 现查 panel name，pool 在主程序启动时初始化。如果 panel pool init 失败（比如 3X-UI 不可达），name 会暂时为空。检查 `xui_panels` 表的 panel 是否能 ping 通。

**Q: 流量曲线在升级时刻有断点**

A: 正常 —— 迁移会保留旧的 `traffic_snapshots` 历史 + 用户的 `lifetime_total_bytes`，但 `client_traffic_snapshots`（per-client 级历史）不复制（新版把它的 raw counter 信息收纳进了 `user_xui_clients.last_raw_*_bytes`）。所以**节点 / 用户级**累计曲线连续，**per-client** 级看板从升级时刻起重建。

**Q: 迁移代码什么时候从代码库里删除？**

A: `v4.0.0-beta.2` 起已删除 V2→V3 专用包；冻结 `v3.9.2` 标签和发行镜像仍提供此工具，Git 历史保留源码审计轨迹。最终 V3→V4 通过正常启动的有界桥接完成，不再运行 `psp migrate`。V2 必须先导入 V3 并成功启动最终 V3。
