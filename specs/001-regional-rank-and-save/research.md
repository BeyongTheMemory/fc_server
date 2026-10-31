# Research: 地区排行榜(US1)技术决策

**Date**: 2026-08-31 | **Plan**: [plan.md](./plan.md)

所有 Technical Context 中的未知项在此归零。每项含 Decision / Rationale / Alternatives。

## R1. Redis key 方案(命名空间 + 层级全路径)

- **Decision**: ZSET key 按层级带全路径前缀:
  - 省:`rank:prov:{省}`
  - 市:`rank:city:{省}|{市}`
  - 区(县):`rank:dist:{省}|{市}|{区}`
  - 玩家元数据 HASH:`rank:user:{userId}`
  - 脏数据集 SET:`rank:dirty`
  - 分隔符 `|` 在请求校验中禁止出现在地区字段与 userId 中。
- **Rationale**: 现状直接用裸地名做 key,(a) 与未来其它数据(存档等)共享命名空间会冲
  突;(b) 跨省同名市/区(如吉林省/吉林市、朝阳区在北京与长春都有)会把不同地区的玩家
  混进同一个榜,违反"地区维度排行榜"的语义。全路径 key 一次性消除歧义。
- **Alternatives considered**: 裸地名 key(现状)——同名地区互相污染,否;地区编码
  (行政区划码)——前端只传名字,引入编码映射超出本轮范围,否。

## R2. ZSET member 与"只保留最高分"语义

- **Decision**: ZSET member = `userId`(纯 ID,不含昵称与时间);score = 该玩家历史最
  高分。昵称、达成时间、地区等放 `rank:user:{userId}` HASH。更新用 Lua 脚本实现
  "只升不降"(见 R3)。
- **Rationale**: 现状 member = `userId:userName:createTime`,同一玩家每次上传都会生成
  新 member,导致同人重复上榜(直接违反 FR-001),且昵称含 `:` 会破坏解析。member 固
  定为 userId 后,ZADD 天然按玩家去重,幂等(FR-010)也随之成立。
- **Alternatives considered**: 维持复合 member 并在读取时去重——写入侧数据仍然膨胀、
  无法给出正确的 ZREVRANK 名次,否;member 带昵称——改名会分裂身份,否。

## R3. 更新原子性:Lua 脚本

- **Decision**: 上传走一段 Lua 脚本(go-redis `Script`,EVALSHA 自动降级 EVAL):
  读取 `rank:user:{userId}` 当前最高分 → 仅当新分数更高(或无记录)时,对省/市/区各级
  ZSET 执行 ZADD、HSET 元数据(name/score/achieved_at/省市区)、SADD `rank:dirty`,并
  返回 1;否则返回 0。地区字段为空的层级跳过(FR:只参与能确定的层级)。
- **Rationale**: "比较-更新 ZSET-更新 HASH-标脏"必须原子:纯管道 `ZADD GT CH` 只保证
  ZSET 单 key 的 max 语义,HASH 与脏集写入在其后存在竞态——并发上传时低分请求可能把
  自己的达成时间/昵称覆盖进元数据。单实例 Redis 上多 key Lua 完全合法,脚本 <30 行。
- **Alternatives considered**: `ZADD GT CH` + 条件管道——竞态窗口如上,否;WATCH/MULTI
  乐观锁——重试逻辑更复杂且性能更差,否。

## R4. 同分排序与"自己的名次"

- **Decision**: 榜单读取:每级 `ZREVRANGE ... WITHSCORES` 取前 100,再批量取成员的
  HASH 元数据,应用层对**同分条目**按 `achieved_at` 升序重排后返回。自己的名次用
  `ZREVRANK`(+1)与 `ZSCORE` 给出,未上榜返回"无名次"。
- **Rationale**: Redis ZSET 同分按 member 字典序,无法直接表达"先达成者在前"
  (FR-003)。复合分数(score×10^10+反向时间戳)受 float64 2^53 精度限制,分数上限被
  压到 ~9×10^5,对游戏分数不安全。应用层只对已取回的 ≤100 条重排,代价 O(100),精确
  满足展示需求。已知局限:第 100 名边界上同分成员的取舍、以及 ZREVRANK 的同分名次仍按
  字典序,属可接受近似,记录于 data-model.md。
- **Alternatives considered**: 复合分数编码——精度上限不可接受,否;全量取回排序——
  数据量增长后不可控,否。

## R5. Redis → MySQL 定期同步(脏集增量)

- **Decision**: 上传成功(Lua 返回 1)即已 `SADD rank:dirty {userId}`。后台 goroutine
  每 `RANK_SYNC_INTERVAL`(默认 60s)执行一轮:`SPOP rank:dirty COUNT 128` 循环取批 →
  逐用户 `HGETALL rank:user:{id}` → 批量 upsert 到 MySQL;某批失败则将该批 userId
  `SADD` 回脏集并记错误日志,下轮重试。进程收到退出信号时执行最后一轮 flush 再退出。
- **Rationale**: 增量集是最简单的"精确知道谁变了"的机制,幂等(重复同步同一玩家结果
  一致),失败可安全回补;比全量扫描省资源,比 keyspace notification/CDC 简单一个量级。
- **Alternatives considered**: 定时全量 SCAN 导出——玩家量大时浪费且慢,否;写 MySQL
  与写 Redis 双写——用户明确要求"直接写 redis、定期同步 DB",且双写引入在线路径延迟
  与部分失败问题,否。

## R6. MySQL 访问方式与驱动

- **Decision**: 标准库 `database/sql` + `github.com/go-sql-driver/mysql`(仅此一个新
  直接依赖)。启动时 `CREATE TABLE IF NOT EXISTS user_best_score ...` 自动建表。upsert
  语句用 `INSERT ... ON DUPLICATE KEY UPDATE`,score 更新带 `GREATEST()` 保护(防乱序
  批次回退最高分)。
- **Rationale**: 宪法 V(依赖纪律)禁止无理由引入 ORM;同步任务只有 1 张表、2 条 SQL,
  database/sql 足够。`go-sql-driver/mysql` 是 MySQL 事实标准驱动。
- **Alternatives considered**: GORM/sqlx——为 1 表 2 SQL 引入大依赖,否;
  纯文本备份文件——用户点名 MySQL,否。

## R7. MySQL 未配置时的行为(配置稍后提供)

- **Decision**: `MYSQL_DSN` 为空 → 跳过建表与同步任务,启动时打一条 Warn 日志
  ("MySQL sync disabled: MYSQL_DSN not set"),在线功能完全可用。配置好后重启进程即
  自动启用(脏集里累积的增量会在首轮全部同步,不丢数据)。
- **Rationale**: 用户明确说 DB 配置稍后给;脏集机制天然允许"晚接入":未同步的玩家 ID
  一直留在 `rank:dirty` 里。
- **Alternatives considered**: 阻塞启动等待配置——在线功能被无关依赖绑架,否。

## R8. 从 MySQL 重建 Redis(备份的意义闭环)

- **Decision**: 环境变量 `REBUILD_RANK_FROM_DB=true` 时,启动阶段(监听端口前)从
  `user_best_score` 全表读出,重放进各级 ZSET 与元数据 HASH(重放用与上传同一段 Lua,
  天然幂等、只升不降),完成后打印重建条数再开始服务。
- **Rationale**: 只备份不恢复的备份没有意义;Redis 数据丢失(如误 FLUSHALL)时给出确
  定的恢复路径。做成显式开关而非"启动自动检测为空即重建",避免把"合法的空榜"误判
  成灾难。
- **Alternatives considered**: 自动检测空库重建——新部署与灾难无法区分,否;不做恢复
  ——备份闭环缺失,否。

## R9. 配置注入方式

- **Decision**: 纯环境变量 + 默认值,集中在 `internal/config`:
  `PORT=8080`、`REDIS_ADDR=127.0.0.1:6379`、`REDIS_DB=0`、`REDIS_PASSWORD=`、
  `MYSQL_DSN=`(空=停用同步)、`RANK_SYNC_INTERVAL=60s`、`RANK_LIMIT=100`、
  `MAX_SCORE=1000000000`、`REBUILD_RANK_FROM_DB=false`。
- **Rationale**: 零新依赖,单机部署下 export/systemd EnvironmentFile 均可;用户稍后给
  的 DB 配置只需拼成 DSN 设进环境变量。
- **Alternatives considered**: viper/yaml 配置文件——为 9 个配置项引入依赖,否。

## R10. 测试策略(宪法 III 落地)

- **Decision**:
  - 单测(无外部依赖):application impl 用 fake `RankStorage`(表驱动:首传/低分不更
    新/高分更新/同分重排/缺失层级/非法入参);processor 用 gin+httptest(校验 400、
    包裹格式、成功路径);key 方案纯函数单测。
  - 集成测试:`rank_redis_storage_test.go` 连 `127.0.0.1:6379` **DB 15**(与线上 DB 0
    隔离),Ping 失败即 `t.Skip`;用例覆盖 Lua 只升不降、幂等重放、跨省同名区不串榜、
    脏集标记。`rank_mysql_sync_test.go` 仅当 `TEST_MYSQL_DSN` 设置时运行(建表、upsert
    GREATEST、脏集回补)。
  - 门禁:`go build ./...`、`go vet ./...`、`go test ./...` 全绿。
- **Rationale**: 与宪法 III/开发流程一致;条件跳过让 CI 无 Redis 时仍可跑单测,本地
  (Redis 已就绪)跑全量。
- **Alternatives considered**: miniredis 仿真——Lua 脚本支持不完整且引新依赖,否;
  testcontainers——单机个人项目过重,否。

## R11. HTTP 错误语义修正

- **Decision**: 统一响应包裹 `{"code":<业务码>,"msg":<描述>,"data":<载荷>}`;参数非法
  → HTTP 400(code=400),内部错误 → HTTP 500(code=500),成功 → HTTP 200(code=0)。
- **Rationale**: 现状内部错误返回 HTTP 200 + code 500、绑定错误又是另一种
  `{"error":...}` 形状,与宪法 II("非法入参 4xx、内部错误 5xx、统一包裹")冲突;客户
  端同为 WIP,现在统一代价最低。**注意**:成功码从现状的 `200` 改为 `0`?——否,保持
  `code=200` 以贴近现有客户端预期,详见 contracts/http-api.md(最终:成功 code=200)。
- **Alternatives considered**: 维持现状——违宪且两种错误形状,否。

## R12. 时间窗口榜(总/月/周)设计(2026-08-31 需求变更)

- **Decision**:
  - 窗口定义:总榜=全时段(现状);月榜=自然月(`YYYY-MM`);周榜=ISO 周、周一起点
    (`YYYY-Www`);时间基准 Asia/Shanghai(加载失败回退固定 +8)。
  - Key 方案:窗口榜 ZSET 在现有层级 key 前插入窗口段——
    `rank:w:{周期号}:prov|city|dist:{路径}`、`rank:m:{月期号}:...`;总榜 key 不变
    (向后兼容)。每玩家窗口元数据独立 HASH:`rank:wuser:{id}`、`rank:muser:{id}`
    (字段:win/name/score/achieved_at/prov/city/dist)。
  - 滚动与清理:新窗口自然形成新 key;窗口 key 每次写入时 EXPIRE(周 14 天、月 62 天),
    旧窗口停止写入后自动过期(FR-013),无需清理任务。
  - 更新原子性:并入同一段 Lua——总榜"只升不降"逻辑不变;窗口段在**总榜未破纪录时也
    执行**(本周最好成绩可能低于历史最高),窗口内同样只升不降,期号变化视为无记录。
  - MySQL:仅总榜同步/重建(需求方确认周/月榜不持久化);RestoreBest 传空期号跳过窗口段,
    重建后当期窗口榜从零累计。
  - API:`fetch_rank` 一次返回全部窗口,榜单对象增加 `window` 字段
    (`all`/`month`/`week`),顺序:总→月→周,各窗口内省→市→区。
- **Rationale**: key 前缀窗口段让三个窗口完全隔离且复用同一套层级/成员/元数据模型;
  EXPIRE 是最简单可靠的滚动清理;窗口 meta HASH 使"窗口内只升不降 + 同分先达成 + 搬家
  清理"与总榜逻辑同构。
- **Alternatives considered**: 定时任务清空/归档旧窗口——多一个常驻任务且有清理窗口期,
  否;客户端按窗口分别请求——三次往返,收益无,否;复合分数编码窗口——不可行(R4 同理),否。
