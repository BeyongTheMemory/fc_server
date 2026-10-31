# Tasks: 地区排行榜(US1:得分上传 + 省/市/区排行榜,Redis 直写 + 定期同步 MySQL)

**Input**: Design documents from `/specs/001-regional-rank-and-save/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/http-api.md, quickstart.md

**Tests**: 包含。宪法 III(测试随行)为强制条款:每个行为变更必须附带测试,`go test ./...` 为合并门禁。

**Organization**: 本轮范围仅 **User Story 1(P1)**;US2(存档)/US3(微信登录)明确不在本轮,不生成其任务。

**Scope note**: 用户指令"仅为需求1(User Story 1,P1)生成任务;US2/US3 不在本轮范围"。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行(不同文件、无未完成依赖)
- **[US1]**: 归属 User Story 1
- 每个任务附精确文件路径

## Path Conventions

单项目 Go 服务,源码在仓库根 `main.go` + `internal/`;测试与被测代码同包同目录(Go 惯例)。

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: 配置与依赖就绪

- [X] T001 新建 `internal/config/config.go`:定义 Config 结构与 `Load()`(纯环境变量+默认值):PORT=8080、REDIS_ADDR=127.0.0.1:6379、REDIS_PASSWORD=""、REDIS_DB=0、MYSQL_DSN=""、RANK_SYNC_INTERVAL=60s、RANK_LIMIT=100、MAX_SCORE=1000000000、REBUILD_RANK_FROM_DB=false(research.md R9);附表驱动单测 `internal/config/config_test.go`(默认值/覆盖/非法时长回退)
- [X] T002 添加 MySQL 驱动依赖:`go get github.com/go-sql-driver/mysql` 并 `go mod tidy`,确认 go.mod/go.sum 更新(research.md R6,宪法 V 已在 plan.md 记录理由)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: 所有 US1 任务共同依赖的横切骨架

**⚠️ CRITICAL**: 本阶段完成前不得开始 User Story 实现

- [X] T003 重构 `main.go` 骨架:统一响应包裹 helper(成功 HTTP 200/code 200;参数非法 HTTP 400/code 400;内部错误 HTTP 500/code 500,见 contracts/http-api.md);删除全部注释掉的死代码(宪法 V);用 `http.Server` + signal 实现优雅停机(为同步任务 flush 留钩子);端口取自 config
- [X] T004 更新 `internal/initer.go`:`Init()` 改为 `Init(cfg *config.Config)`,logger 最先初始化;Redis 存储初始化改为接收 config(地址/密码/DB 不再硬编码);预留 MySQL 同步组件装配点(本阶段先空置,T015 接入)

**Checkpoint**: 骨架就绪 —— US1 实现可以开始

---

## Phase 3: User Story 1 - 上传最高分并查看地区排行榜 (Priority: P1) 🎯 MVP

**Goal**: 玩家上报得分,服务端仅保留历史最高分,按省/市/区三级榜单(每级前 100)返回,含玩家自己的名次;数据直写 Redis,增量定期同步 MySQL,支持从 MySQL 重建。

**Independent Test**: 按 quickstart.md 第 1~7 步:多玩家多地区上报后拉榜,验证去重、只升不降、同分先达成者在前、自己名次、幂等与 400 校验;配置 MYSQL_DSN 后验证同步与重建。

### Tests for User Story 1

> 宪法 III:每个实现任务的测试与实现同任务落地(表驱动);跨组件校验测试单列如下,先写先失败者优先。

- [X] T005 [P] [US1] 新建 `internal/infrastructure/repository_impl/redis_keys.go`:key 构造函数(`rank:prov:{省}`、`rank:city:{省}|{市}`、`rank:dist:{省}|{市}|{区}`、`rank:user:{id}`、`rank:dirty`,research.md R1)与字段合法性常量(禁 `|`);附单测 `redis_keys_test.go`(全路径拼接、空层级跳过、非法字符检测)
- [X] T006 [P] [US1] 重构 rank 领域模型:`internal/domain/rank/repository/storage.go` 接口改为 `UploadBest(ctx, location, user, score) (updated bool, best int, err)` 与 `GetBoards(ctx, location, userId, limit) ([]*entity.RankResult, err)`;`internal/domain/rank/entity/rank_reuslt.go` 重命名为 `rank_result.go`,RankResult 增加 SelfRank/SelfScore 字段,删除 member key 编解码方法(data-model.md);同步修正 `vo.ScoreMeta` 注释语义(历史最高分)
- [X] T007 [US1] 重写 `internal/domain/rank/application/impl/rank_application_impl.go`:接口 `internal/domain/rank/application/rank_application.go` 改为 `UploadScore(...) (updated bool, best int, err)` / `GetRank(ctx, location, userId) ([]*entity.RankResult, err)`;榜长取 config(Init 传入);GetRank 对同分条目按 achieved_at 升序稳定重排(research.md R4);依赖 T006
- [X] T008 [US1] 新建 `internal/domain/rank/application/impl/rank_application_impl_test.go`:fake RankStorage 表驱动覆盖——首次上报 updated=true、低分 updated=false、高分更新、同分重排顺序、缺失市/区层级只返回可用层级、榜长截断;依赖 T007
- [X] T009 [US1] 重写 `internal/infrastructure/repository_impl/rank_redis_storage.go`:client 由 config 注入;UploadBest 走 Lua 脚本(比较→仅更高时对非空层级 ZADD→HSET 元数据→SADD rank:dirty→返回 updated+best,research.md R2/R3);GetBoards 用管道 ZREVRANGE WITHSCORES(limit)+ZREVRANK/ZSCORE(自己)+HGETALL 补元数据;修复旧代码 pipeline 双重 Exec bug(随重写消失);依赖 T005、T006
- [X] T010 [US1] 新建 `internal/infrastructure/repository_impl/rank_redis_storage_test.go`:集成测试连 127.0.0.1:6379 **DB 15**,Ping 失败 `t.Skip`,每用例前清库;覆盖——只升不降、重复上报幂等(FR-010)、跨省同名市/区不串榜(R1)、脏集标记、缺失层级跳过、自己名次(上榜/未上榜);依赖 T009
- [X] T011 [US1] 更新 DTO 与 processor:`internal/processor/dto/upload_score.go`(响应加 updated/best_score)、`internal/processor/dto/fetch_rank_list.go`(条目加 user_id,层级加 self_rank/self_score)、`internal/processor/dto/user_info.go`(校验方法:user_id/user_name 必填与长度、禁 `|`、score 区间、地区字段长度,data-model.md 校验表);`internal/processor/upload_score.go`、`internal/processor/fetch_rank_list.go` 接新应用层签名,校验失败返回可区分的参数错误(FR-011);依赖 T007
- [X] T012 [US1] 新建 `internal/processor/upload_score_test.go`、`internal/processor/fetch_rank_list_test.go`:gin+httptest,fake 存储经 `impl.Init` 注入;覆盖——非法 score/缺 user_id 返回 400 包裹、成功包裹 `{code:200,data:...}` 形状与 contracts/http-api.md 一致、fetch 返回 self_rank;依赖 T011
- [X] T013 [US1] 更新 `main.go` 路由:`/upload_score`、`/fetch_rank` 接入新 DTO 校验与统一包裹(绑定错误与业务校验错误均 400;内部错误 500);依赖 T003、T011
- [X] T014 [US1] 新建 `internal/infrastructure/sync/rank_mysql_sync.go`:`CREATE TABLE IF NOT EXISTS user_best_score`(data-model.md DDL);Syncer:ticker(RANK_SYNC_INTERVAL)每轮 `SPOP rank:dirty COUNT 128` 循环→HGETALL→批量 upsert(GREATEST 防回退);失败批次 SADD 回补+错误日志;`Stop()` 触发收尾 flush;MYSQL_DSN 为空则构造为禁用态并 Warn(R5/R6/R7);附 `rank_mysql_sync_test.go`(仅 TEST_MYSQL_DSN 设置时跑:建表、upsert GREATEST、脏集消费与回补);依赖 T005、T001
- [X] T015 [US1] 在 `internal/infrastructure/sync/rank_mysql_sync.go` 增加 `RebuildFromDB(ctx)`:全表读 user_best_score,经 UploadBest 重放(幂等只升不降,R8);`internal/initer.go` 与 `main.go` 装配:REBUILD_RANK_FROM_DB=true 时监听前重建并打印条数,启动 Syncer,优雅停机时 `Stop()`;依赖 T009、T014、T004
- [X] T016 [US1] 全量门禁与端到端:`gofmt -l .` 为空、`go build ./...`、`go vet ./...`、`go test ./...` 全绿;起服务按 quickstart.md 1~7 步用 curl 验证(本地 Redis 已就绪);依赖 T013、T015

**Checkpoint**: US1 完整可独立验证(MySQL 部分在 DSN 提供前处于优雅停用态,不阻塞验收)

---

## Phase 4: Polish & Cross-Cutting Concerns

**Purpose**: 收尾与文档

- [X] T017 [P] 更新 `README.md`:项目简介、配置表(quickstart.md 配置节)、两个端点简述与 curl 示例、测试运行说明
- [X] T018 复核宪法符合性:domain 无 gin/redis/mysql import(原则 I)、无 fmt.Println/log.Println(原则 IV)、无注释死代码(原则 V);违规即修复

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1(Setup)**: 无前置
- **Phase 2(Foundational)**: 依赖 T001;阻塞所有 US1 任务
- **Phase 3(US1)**: 依赖 Phase 2 完成
- **Phase 4(Polish)**: 依赖 Phase 3 完成

### Task-level Graph(US1 内)

```
T001 ─┬─ T003 ─┐
      └─ T004 ─┤
T002 ──────────┤(仅 T014 需要)
               ├─ T005 ─┬─ T009 ─ T010
               ├─ T006 ─┤
               │        └─ T007 ─ T008
               │            └─ T011 ─ T012
               │                 └─ T013
               ├─ T014(T005,T001,T002)
               └─ T015(T009,T014,T004)─ T016(T013,T015,T010,T012,T008)
```

### Parallel Opportunities

- T001 与 T002 并行;T003 与 T004 并行
- T005 与 T006 并行(不同文件);T008 与 T009 并行;T010 与 T011 并行;T012 与 T014 并行
- T017 与 T018 并行

## Parallel Example: User Story 1

```bash
# Foundational 完成后可同时开工:
Task: "T005 redis_keys.go + 单测"
Task: "T006 领域模型与 RankStorage 接口重构"
# T007 完成后:
Task: "T008 应用层表驱动单测"
Task: "T009 Redis 存储 Lua 重写"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Phase 1 → Phase 2(骨架)
2. Phase 3 按依赖图推进:先领域与存储正确性(T005~T010),再 API 层(T011~T13),最后持久化闭环(T014~T15)
3. T016 **STOP and VALIDATE**:quickstart 全场景过一遍
4. MySQL DSN 到位后补跑同步/重建验证(quickstart「MySQL 同步验证」节)

### Incremental Delivery

- 本轮交付即 MVP(US1);US2/US3 留待后续轮次,其任务不在本文件
- MySQL 配置晚到不阻塞:同步组件优雅停用,脏集保留增量,配置后重启即补同步(R7)

---

## Notes

- [P] = 不同文件且无未完成依赖
- 每个实现任务的测试与实现同任务交付(宪法 III);T010/T012/T014 内的集成测试按环境条件跳过
- 提交建议:每完成一个任务或紧密任务组提交一次
- person 域与 `internal/processor/fetch_ongoing_game.go` 本轮禁止改动(US2 范围)

---

## Phase 5: Convergence

- [X] T019 [US1] 修正昵称更新语义:`internal/infrastructure/repository_impl/rank_redis_storage.go` 的 Lua 脚本在分数未破纪录、但上报昵称与已存昵称不同时,仍更新 `rank:user:{id}` 的 name 字段并 SADD 脏集(使 MySQL 侧昵称随之刷新);`rank_redis_storage_test.go` 增加用例:低分上报携带新昵称 → 榜单/元数据显示新昵称、脏集被标记、分数与达成时间不变 per spec Edge Case「玩家修改昵称」 (partial)
- [X] T020 [US1] MYSQL_DSN 提供后完成持久化闭环真实验证:设置 `MYSQL_DSN` 启动服务,按 specs/001-regional-rank-and-save/quickstart.md「MySQL 同步验证」节验证定时同步(user_best_score 出现对应行、重复同步无重复)与 `REBUILD_RANK_FROM_DB=true` 重建;同时以 `TEST_MYSQL_DSN` 跑一次 `go test ./internal/infrastructure/sync/` 使被跳过的集成测试真实执行 per plan R5/R8 (partial)

---

## Phase 6: 时间窗口榜(总/月/周,需求变更 2026-08-31)

- [X] T021 [US1] `internal/infrastructure/repository_impl/redis_keys.go`:新增窗口期号计算(`weekWindowId`/`monthWindowId`,Asia/Shanghai,ISO 周)与带前缀的层级 key 构造(`rank:w:{win}:...`/`rank:m:{win}:...`,R12);`redis_keys_test.go` 补周/月期号用例(含周一 00:00 边界)与窗口 key 拼接用例
- [X] T022 [US1] `internal/infrastructure/repository_impl/rank_redis_storage.go`:Lua 扩展窗口段(总榜未破纪录也更新窗口;窗口内只升不降;期号变化即重置;窗口 key/HASH 每次写入 EXPIRE 14d/62d;同窗口搬家 ZREM 旧地区);GetBoards 返回 3 窗口 × 可用层级(entity.RankResult 加 Window 字段,`internal/domain/rank/entity/rank_result.go`);RestoreBest 传空期号仅重建总榜;集成测试:窗口榜生成+TTL、窗口内只升不降、AS8(总榜不动周榜更新)、期号切换重置(AS7)、重建不产生窗口数据
- [X] T023 [US1] DTO 与端到端:`internal/processor/dto/fetch_rank_list.go` RankResultDto 加 `window` 字段;processor/main 测试断言 window 透传;`main_test.go` 校验 9 榜单响应形状
- [X] T024 [US1] 门禁与验证:`gofmt`/`go build`/`go vet`/`go test ./...` 全绿;起服 curl 验证上传后总/月/周 × 省/市/区榜单齐全、self_rank 正确;更新 README 榜单说明
