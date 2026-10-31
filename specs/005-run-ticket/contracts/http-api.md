# HTTP API 契约：对局票（005）

日期：2026-10-31 · 通用约定同 [002 契约](../../002-wechat-login/contracts/http-api.md)

目的：排行榜成绩必须来自服务端登记过的一局。种子由服务端下发，玩家不能挑选种子刷榜；每局只能提交一份成绩。

梦蝶局（玩家自行输入种子）不计入排行榜，与服务端无关：**不领票，也不上报成绩**。

## POST /runs/start

需要 token，无请求体。每开一局普通局调用一次。

成功 200：

```json
{"code":200,"msg":"","data":{"run_id":"<32 位十六进制>","seed":"Ab3dE9xQ",
 "started_at_ms":1793380000000,"expires_at_ms":1795972000000}}
```

- `seed` 为 8 位字母数字（与前端默认种子同格式），客户端**必须**用它初始化随机流。
- 对局票有效期 `RUN_TICKET_TTL`（默认 30 天），覆盖存档后隔天续玩。
- 开局频率限制 `RUN_START_LIMIT`（默认每玩家每小时 60 次），超出返回 429。

## 与 POST /upload_score 的关系

- 请求必须带 `run_id`；不存在、过期或不是本人的票 → 400 `invalid or expired run_id`（不区分，避免探测）。
- 每局只有一个提交名额：
  - 同一份成绩重试 → 200（`updated:false`，`best` 为当前最好成绩），网络重试是安全的；
  - 同一局提交不同的成绩 → 409 `a different score was already submitted for this run`。
- 可选耗时校验：`MIN_SECONDS_PER_STAGE` > 0 时，`当前时间 - 开局时间 < 关卡数 × 该值` → 400 `run finished faster than possible`，且不占用提交名额。默认 0（关闭），阈值需按实际最快通关节奏设定。

## 客户端要求

1. 新开普通局时先调用 `/runs/start`，用返回的 `seed` 初始化 `GameRandom`，不要用本地种子。
2. 把 `run_id` 和种子一起写进本局存档；读档续局**不要**重新领票（新票种子不同，也会让旧局成绩无法提交）。
3. 结束上报时带上 `run_id`。409 说明本局已经提交过另一份成绩，不要再重试。
4. 领票失败（断网、5xx、429）时可以让玩家离线开局，但这一局没有 `run_id`，结束时不上报成绩，并提示“本局未连接服务器，不计入排行榜”。
5. 梦蝶局不调用 `/runs/start` 和 `/upload_score`。

## 存储

Redis `run:{run_id}`（HASH：`user_id`、`seed`、`started_at_ms`、`expires_at_ms`，提交后增加 `submitted`/`submitted_at_ms`），TTL 为对局票有效期；提交名额由 Lua 脚本原子占用。对局票不备份到 MySQL。
