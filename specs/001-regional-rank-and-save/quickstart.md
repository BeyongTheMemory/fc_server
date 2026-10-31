# Quickstart: 地区排行榜(US1)验证指南

**Plan**: [plan.md](./plan.md) | **Contract**: [contracts/http-api.md](./contracts/http-api.md)

## 前置条件

- Go 1.24;本地 Redis 已就绪:`127.0.0.1:6379`(无密码,DB 0)
- MySQL 可选:未配置 `MYSQL_DSN` 时同步自动停用,在线功能不受影响(research.md R7)

## 配置(环境变量,全部有默认值)

| 变量 | 默认 | 说明 |
|------|------|------|
| PORT | 8080 | HTTP 监听端口 |
| REDIS_ADDR | 127.0.0.1:6379 | Redis 地址 |
| REDIS_PASSWORD | (空) | Redis 密码 |
| REDIS_DB | 0 | Redis 逻辑库 |
| MYSQL_DSN | (空) | 如 `user:pass@tcp(127.0.0.1:3306)/fc?charset=utf8mb4&parseTime=true`;空=停用同步 |
| RANK_SYNC_INTERVAL | 60s | Redis→MySQL 同步周期 |
| RANK_LIMIT | 100 | 每级榜单长度 |
| MAX_SCORE | 1000000000 | 得分上限校验 |
| REBUILD_RANK_FROM_DB | false | true 时启动前从 MySQL 重建 Redis 榜单 |

## 构建与运行

```bash
go build ./... && go vet ./... && go test ./...
```

```bash
go run .
```

启动日志应包含:Redis 连接成功;`MYSQL_DSN` 未设置时一条 Warn(`MySQL sync disabled`)。

## 端到端验证(对应 spec 验收场景)

1. 首次上报(US1-AS1):

```bash
curl -s -X POST localhost:8080/upload_score -H 'Content-Type: application/json' -d '{"user_info":{"user_id":"u_1","user_name":"阿甘","location":{"province":"浙江省","city":"杭州市","district":"西湖区"}},"score":1000}'
```

期望:`{"code":200,...,"data":{"updated":true,"best_score":1000}}`

2. 低分不更新(US1-AS2):同上但 `"score":800` → `updated:false, best_score:1000`
3. 高分更新(US1-AS3):`"score":1500` → `updated:true, best_score:1500`
4. 拉榜(US1-AS1/AS6):

```bash
curl -s -X POST localhost:8080/fetch_rank -H 'Content-Type: application/json' -d '{"user_info":{"user_id":"u_1","user_name":"阿甘","location":{"province":"浙江省","city":"杭州市","district":"西湖区"}}}'
```

期望:三级榜均含 u_1(score 1500,仅一条,不重复);`self_rank` ≥1。

5. 非法入参(FR-011):`"score":-5` → HTTP 400、`code:400`
6. 空榜(US1-AS5):换一个无人地区拉榜 → 对应层级 `score_meta_list: []`,不报错
7. 幂等(FR-010):重复发送同一上报 → 状态与发送一次完全相同

## MySQL 同步验证(拿到 DSN 后)

```bash
export MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/fc?charset=utf8mb4&parseTime=true'
go run .
```

- 上报若干分数,等待一个同步周期(或缩短 `RANK_SYNC_INTERVAL=5s`)
- 检查:`SELECT * FROM user_best_score;` 应出现对应行;重复同步不产生重复行
- 重建演练(R8):`redis-cli -n 0 flushdb` 后以 `REBUILD_RANK_FROM_DB=true` 启动,
  拉榜应恢复(达成时间、昵称一致)

## 自动化测试说明

- 单测无外部依赖,任何环境可跑
- Redis 集成测试使用 **DB 15**(不污染 DB 0),Redis 不可达时自动 Skip
- MySQL 集成测试仅在 `TEST_MYSQL_DSN` 设置时运行
