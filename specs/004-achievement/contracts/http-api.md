# HTTP API 契约：玩家成就（004）

日期：2026-10-31 · 通用约定（响应包裹、Bearer token、16 KiB 请求体上限、错误码）同 [002 契约](../../002-wechat-login/contracts/http-api.md)

## POST /upload_achievement

需要 token。结构化请求：

```json
{"achievement_id":"daluo_10","unlocked_at_ms":1793380000000,"detail":{"stage":37}}
```

| 字段 | 规则 |
|---|---|
| `achievement_id` | 必填，1～64 个字符，只能是字母、数字与 `_ . : -`。由客户端统一定义，发布后不改名；分档成就用不同 ID（如 `daluo_10`、`daluo_20`） |
| `unlocked_at_ms` | 可选，客户端解锁时间（Unix 毫秒）；省略或 0 时用服务端接收时间。早于 2020 年或领先服务端超过 24 小时返回 400 |
| `detail` | 可选，附加信息，必须是 JSON 对象，序列化后 ≤ 4 KiB、嵌套 ≤ 64 层；服务端原样保存，不解析 |

成功 200：

```json
{"code":200,"msg":"","data":{"achievement_id":"daluo_10","newly_unlocked":true,"unlocked_at_ms":1793380000000}}
```

## 语义

- 每位玩家的每项成就只记录**首次解锁**。重复上传返回 200、`newly_unlocked: false` 与首次的 `unlocked_at_ms`，不覆盖、不报错，所以客户端可以放心重试。
- 归属玩家只取自 token；`detail` 里即使有 `user_id` 也只是普通数据。
- `newly_unlocked` 只表示服务端此前有没有这条记录（换设备后第一次补报也是 `true`），客户端是否弹“新解锁”提示以本地判断为准。

| 情况 | HTTP |
|---|---:|
| 字段不合法 | 400 |
| 未登录或 token 失效 | 401 |
| 请求体超过 16 KiB | 413 |
| 待写入积压已满（`GAME_RECORD_QUEUE_MAX`）或 Redis 不可用 | 503（稍后重试） |

## 存储

请求先写 Redis（`achievements:{user_id}` 判重，`achievement:queue` 待持久化，两步在同一个 Lua 脚本中原子完成），后台任务按 `RANK_SYNC_INTERVAL` 批量写入 MySQL：

```sql
CREATE TABLE player_achievement (
  id             BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  user_id        VARCHAR(64) NOT NULL,
  achievement_id VARCHAR(64) NOT NULL,
  unlocked_at_ms BIGINT      NOT NULL,
  received_at_ms BIGINT      NOT NULL,
  detail         JSON        NULL,
  client_ip      VARCHAR(45) NOT NULL DEFAULT '',  -- 首次上报请求的来源 IP
  UNIQUE KEY uk_user_achievement (user_id, achievement_id),
  KEY idx_achievement (achievement_id)
);
```

查询某玩家的成就：`SELECT achievement_id, unlocked_at_ms, detail FROM player_achievement WHERE user_id = ?`。
写入流程与游戏记录相同（持锁消费、提交后删除队头、唯一键去重重放、单条坏数据丢弃不阻塞）。
