# Implementation Plan: 地区排行榜(US1:得分上传 + 省/市/区排行榜,Redis 直写 + 定期同步 MySQL)

**Branch**: `001-regional-rank-and-save` | **Date**: 2026-08-31 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/001-regional-rank-and-save/spec.md`

**Note**: This template is filled in by the `/speckit-plan` command; its definition describes the execution workflow.

## Summary

本轮仅实现 **User Story 1(P1)**:玩家上传得分,服务端只保留每人历史最高分,并按省/市/
区(县)三级维护排行榜;玩家一次请求可取回三级榜单(每级前 100 名)及自己的名次。技术
路线:**Redis(127.0.0.1:6379)作为在线读写主存储**(ZSET 承载榜单、HASH 承载玩家最高
分元数据),**后台定时任务将增量数据同步到 MySQL** 作为持久备份(连接配置通过环境变量
注入,未配置时同步自动停用并告警,不影响在线功能)。US2/US3(存档、微信登录)不在本轮
范围。

现有 WIP 代码保留分层骨架(processor → application → repository 接口 ← infrastructure),
但需要修正三处设计缺陷:① ZSET member 含 `createTime` 导致同一玩家多次上传产生重复条
目(违反 FR-001);② Redis key 直接用地名,无命名空间且跨省同名市/区会互相污染;③
`Upload` 中 pipeline 被 `Exec` 两次的 bug。member 改为 `userId`,key 改为带层级全路径的
命名空间 key,更新走 Lua 脚本保证"只升不降"原子性。

## Technical Context

**Language/Version**: Go 1.24(module `fc_server`)

**Primary Dependencies**: gin(HTTP)、go-redis/v9(Redis)、zap(日志);新增
`go-sql-driver/mysql`(database/sql 驱动,理由见 research.md R6)

**Storage**: Redis 7.x @ 127.0.0.1:6379(在线主存储:ZSET 榜单 + HASH 玩家元数据 +
SET 脏数据集);MySQL(持久备份,表 `user_best_score`,DSN 由环境变量 `MYSQL_DSN` 注
入,配置稍后由用户提供)

**Testing**: go test(表驱动单测 + fake RankStorage;基础设施层集成测试连本地 Redis
DB 15,不可达时 t.Skip;MySQL 集成测试仅在 `TEST_MYSQL_DSN` 设置时运行)

**Target Platform**: 本地/单机 Linux/macOS 服务器,单实例部署,端口默认 8080

**Project Type**: web-service(游戏后端 HTTP API,客户端为 Cocos 游戏 five_card)

**Performance Goals**: SC-002:95% 榜单查询 < 1s;SC-001:上传后 5s 内榜单可见(实际
为同步写,毫秒级);SC-005:100 并发无劣化

**Constraints**: 地区由前端随请求传省/市/区(县),服务端不校验真实性;同分先达成者在
前;榜单每级前 100 名 + 自己名次;上报幂等;MySQL 未配置时服务必须可正常运行(同步停
用)

**Scale/Scope**: ≥10,000 玩家;2 个既有 HTTP 端点(`/upload_score`、`/fetch_rank`)重
构 + 1 个后台同步任务 + 1 个启动时重建能力;不新增对外端点

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 结论 | 说明 |
|------|------|------|
| I. 分层架构 | PASS | 保持 processor → application → repository 接口 ← infrastructure;domain 不 import gin/redis/mysql;同步任务与 Redis key 方案属基础设施层(`internal/infrastructure/`) |
| II. 契约优先 API | PASS | 端点路径不变;DTO 显式定义于 `processor/dto`;统一响应包裹 `{code,msg,data}`;参数校验在 processor 边界,非法入参 400、内部错误 500(修正现状:错误也返回 HTTP 200 的行为) |
| III. 测试随行 | PASS | 本轮为存量零测试代码补齐:application/processor 表驱动单测(fake 存储)、Redis/MySQL 集成测试(条件跳过);`go test ./...` 为合并门禁 |
| IV. 结构化日志 | PASS | 复用 `internal/util` zap 与请求日志中间件;同步任务的成功/失败按批次记录;不引入 `fmt.Println` |
| V. 简单性与依赖纪律 | PASS | 仅新增 1 个直接依赖 `go-sql-driver/mysql`(标准 database/sql 驱动,不引 ORM,理由见 research.md R6);删除 main.go 中注释掉的死代码 |
| 技术约束:存储引擎 | PASS(经修订) | 原 v1.0.0 规定"Redis 为唯一存储引擎,新增引擎需修宪"。用户本轮明确要求"定期同步到 MySQL",已按治理流程将宪法修订为 v1.1.0:Redis 为在线主存储,MySQL 为持久备份存储 |

**Post-Phase-1 re-check**: PASS —— 设计产物未引入新的越层依赖或额外存储引擎;Lua 脚本
与脏数据集均落在基础设施层,domain 接口保持存储无关。

## Project Structure

### Documentation (this feature)

```text
specs/001-regional-rank-and-save/
├── plan.md              # This file (/speckit-plan command output)
├── research.md          # Phase 0 output (/speckit-plan command)
├── data-model.md        # Phase 1 output (/speckit-plan command)
├── quickstart.md        # Phase 1 output (/speckit-plan command)
├── contracts/
│   └── http-api.md      # Phase 1 output (/speckit-plan command)
└── tasks.md             # Phase 2 output (/speckit-tasks command - NOT created by /speckit-plan)
```

### Source Code (repository root)

```text
main.go                                  # gin 路由与启动(清理死代码,接入 config 与优雅停机)
internal/
├── config/
│   └── config.go                        # NEW:环境变量配置(PORT/REDIS_ADDR/MYSQL_DSN/同步间隔等)
├── initer.go                            # 组件装配(接入 config、MySQL 同步任务、重建流程)
├── domain/rank/
│   ├── entity/
│   │   ├── rank_result.go               # RENAME(自 rank_reuslt.go)+ 重构:去掉 key 编解码,增加 Self 名次
│   │   └── vo/                          # location.go / address_level.go / user_info.go / score_meta.go(微调)
│   ├── application/
│   │   ├── rank_application.go          # 接口:UploadScore / GetRank(带 userId,返回三级榜 + 自己名次)
│   │   └── impl/rank_application_impl.go # 校验、同分按达成时间重排、榜长 100
│   └── repository/storage.go            # RankStorage 接口重构(member=userId 语义)
├── infrastructure/
│   ├── repository_impl/
│   │   ├── rank_redis_storage.go        # 重写:命名空间 key + Lua 只升不降 + 元数据 HASH + 脏集
│   │   └── redis_keys.go                # NEW:key 方案与合法性(分隔符)常量
│   └── sync/
│   │   └── rank_mysql_sync.go           # NEW:建表、脏集增量 upsert、定时器、启动重建
├── processor/
│   ├── upload_score.go / fetch_rank_list.go  # 参数校验 + 应用层调用
│   └── dto/                             # 请求/响应 DTO(响应含 self 名次;统一包裹)
└── util/                                # 既有 zap 日志与请求日志中间件(不动)

# 测试与源码同包放置(Go 惯例):
# internal/domain/rank/application/impl/rank_application_impl_test.go
# internal/processor/*_test.go(httptest)
# internal/infrastructure/repository_impl/rank_redis_storage_test.go(连本地 Redis DB15,条件跳过)
# internal/infrastructure/sync/rank_mysql_sync_test.go(TEST_MYSQL_DSN 设置时运行)
```

**Structure Decision**: 沿用仓库既有的单项目 DDD-lite 分层(constitution 原则 I),不
引入新顶层目录;新增 `internal/config`(装配所需的横切配置)与
`internal/infrastructure/sync`(MySQL 持久化属基础设施关注点,domain 无感知)。person
域与 `fetch_ongoing_game.go` 本轮不动。

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| 引入 MySQL 作为第二个存储引擎(原宪法 v1.0.0 限定仅 Redis) | 用户明确要求"数据直接写到 redis,定期同步到 DB",Redis 单存储无持久性保障,进程/实例故障会丢失全部榜单 | 仅靠 Redis RDB/AOF 持久化被否:用户显式要求同步到 DB;已按治理流程修宪至 v1.1.0,不再是违例 |
| Redis 更新走 Lua 脚本(而非纯 ZADD GT 管道) | "只升不降 + 元数据 + 脏标记"需原子完成,否则并发上传会把低分的达成时间/昵称写进元数据 | 纯管道 ZADD GT CH 被否:CH 只能告知变更与否,后续 HSET 与 ZADD 之间存在竞态窗口,会违反 FR-001/FR-002 的正确性 |
