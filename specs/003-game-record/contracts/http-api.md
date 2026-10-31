# HTTP API 契约：玩家游戏记录（003）

日期：2026-10-31 · 通用约定（响应包裹、Bearer token、16 KiB 请求体上限、错误码）同 [002 契约](../../002-wechat-login/contracts/http-api.md)

## POST /upload_game_record

需要 token。请求体**就是一条记录**：任意 JSON 对象，服务端原样保存、不解析业务字段。

```json
{"stage":3,"result":"win","score":1200,"cards":["A","K"],"duration_ms":93000}
```

成功 200：

```json
{"code":200,"msg":"","data":{"record_id":"9f1c…（32 位十六进制）","created_at_ms":1793380000000}}
```

| 情况 | HTTP |
|---|---:|
| 不是 JSON 对象（数组、字符串、数字、null）、JSON 非法、非 UTF-8、嵌套超过 64 层 | 400 |
| 未登录或 token 失效 | 401 |
| 请求体超过 16 KiB | 413 |
| 待写入积压已满（`GAME_RECORD_QUEUE_MAX`）或 Redis 不可用 | 503 |

## 语义

- 归属玩家只取自 token。记录里即使有 `user_id` 之类的字段，也只作为普通数据保存。
- 只追加、不修改、不去重：每次请求都生成新的 `record_id`。客户端只在没收到响应时重试，重试可能产生重复记录。
- 200 表示已进入 Redis 队列；后台任务按 `RANK_SYNC_INTERVAL`（默认 60 秒）批量写入 MySQL。未配置 `MYSQL_DSN` 时记录留在 Redis，积压到上限后返回 503。
- `created_at_ms` 是服务端接收时间（Unix 毫秒）。如需事件发生时间，由客户端放进记录字段。

## 存储

```sql
CREATE TABLE player_game_record (
  id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,  -- 写入顺序
  record_id     CHAR(32)    NOT NULL UNIQUE,                 -- 队列重放去重
  user_id       VARCHAR(64) NOT NULL,
  data          JSON        NOT NULL,
  created_at_ms BIGINT      NOT NULL,
  client_ip     VARCHAR(45) NOT NULL DEFAULT '',            -- 上报请求的来源 IP
  KEY idx_user_created (user_id, created_at_ms)
);
```

查询某玩家的记录：`SELECT data, created_at_ms FROM player_game_record WHERE user_id = ? ORDER BY created_at_ms`。

写入可靠性：同一时刻只有持有 Redis 消费锁的实例写 MySQL；事务提交后才删除队头，提交后崩溃导致的重放由 `record_id` 唯一键去重；单条被 MySQL 拒绝的记录（如嵌套过深、非法字符）会被记录日志并丢弃，不阻塞后续记录。
