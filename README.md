# fc_server

[five_card](https://github.com/BeyongTheMemory)(Cocos 卡牌游戏)的服务端:微信小游戏登录与玩家资料、成绩上报与三类排行榜——**天榜**(全国)、**地榜**(城市,人数不足时为省份)、**人榜**(微信同玩好友),每类含**总榜/月榜/周榜**三个时间窗口(自然月、ISO 周,Asia/Shanghai;新窗口自动从零累计,旧窗口自动过期)。排名依次比较到达关卡数、最后一关修为、整局修为总数。

- **登录**:`wx.login` 的 code 经服务端 `code2Session` 换取 openid,生成稳定玩家 ID(`wx_…`)并签发 Redis 业务会话 token;排行榜接口的身份只来自 token(见 [specs/002](specs/002-wechat-login/))
- **在线存储**:Redis(ZSET 榜单 + HASH 玩家最好成绩元数据 + 玩家资料 + 会话 + 好友列表),每窗口只保留每人该窗口最好的一局,更新走 Lua 脚本保证"只升不降"与幂等;三项成绩编码在 ZSET 成员名中按字节序排序(修为为任意长度十进制字符串,带位数前缀),不受 float/int64 范围限制;周/月榜仅存 Redis(总榜才落 MySQL)
- **持久备份**:后台任务将增量(脏集)定期同步到 MySQL(`user_best_score` 最好成绩、`player_profile` 玩家资料);支持从 MySQL 全量重建 Redis
- **游戏记录**:玩家上报的任意 JSON 记录先进 Redis 队列,后台批量追加到 MySQL `player_game_record`(按 `user_id` 索引,一人多条)
- **来源 IP**:成绩(创下最好成绩的那次上报)、游戏记录、成就(首次解锁)都随数据保存 `client_ip`,用于排查刷榜;请求日志也记录 `client_ip`
- **成就**:结构化上报(`achievement_id` + 可选解锁时间与 JSON 附加信息),每人每项只记首次解锁,Redis 判重后异步写入 MySQL `player_achievement`
- **架构**:processor(HTTP/DTO)→ application(领域应用)→ repository 接口 ← infrastructure(Redis/MySQL/微信实现),详见 [spec/constitution](.specify/memory/constitution.md)、[specs/001](specs/001-regional-rank-and-save/) 与 [specs/002](specs/002-wechat-login/)

## 运行

本机配置统一写在仓库根目录的 `.env.local`(已被 git 与 Docker 忽略),变量名与微信云托管一致。首次使用:

```bash
cp .env.example .env.local   # 填入 WECHAT_APP_ID、WECHAT_APP_SECRET、MySQL/Redis 连接
go run .
```

`go run .` 启动时自动读取 `.env.local`;同名的真实环境变量优先,例如临时换端口 `PORT=9090 go run .`。

前置:本地 Redis(默认 `127.0.0.1:6379`)。微信配置缺失或非法时服务拒绝启动。MySQL 可选:不配置 `MYSQL_ADDRESS`(或 `MYSQL_DSN`)时同步自动停用,在线功能不受影响,但玩家资料只存在 Redis 中。

本地联调建议:`127.0.0.1:8080` 留给 Cocos 远程资源服务,API 用 `PORT=8081`;真机访问用电脑的局域网 IP。正式环境须 HTTPS,并在小游戏后台配置 request 合法域名。

## 部署到微信云托管

仓库根目录的 `Dockerfile`、`.dockerignore`、`container.config.json` 按 [wxcloudrun-golang](https://github.com/WeixinCloud/wxcloudrun-golang) 模板约定编写:服务监听 80 端口,日志只输出到 stdout 由云托管采集,时区 Asia/Shanghai。本机配置文件 `.env.local` 不会进入镜像。

**服务设置 → 端口**:80。**公网出口**:必须开启(登录要访问 `api.weixin.qq.com`)。**实例数**:最小实例建议 ≥1,避免冷启动时首个请求等待 Redis/MySQL 连接。

**环境变量**:

| 变量 | 来源 | 说明 |
|---|---|---|
| `MYSQL_ADDRESS` / `MYSQL_USERNAME` / `MYSQL_PASSWORD` | 云托管自动注入 | 服务据此拼出连接(utf8mb4);库名取 `MYSQL_DATABASE`,默认 `fc`。库不存在时启动时自动创建,4 张表在第一轮同步时自动创建(也可手动执行 `deploy/schema.sql`) |
| `WECHAT_APP_ID` / `WECHAT_APP_SECRET` | **需手动添加** | 小游戏 AppID 与 AppSecret,缺失则服务拒绝启动 |
| `REDIS_ADDR` / `REDIS_PASSWORD` | **需手动添加** | 云托管不提供 Redis,需购买**腾讯云数据库 Redis**,地域与云托管环境相同(如 ap-shanghai),并放在云托管环境所在的 VPC 内,填内网地址,如 `10.0.0.12:6379`。缺失则服务启动失败 |
| 其余 | 可选 | 见下表,如 `LEADERBOARD_HIDDEN`、`MIN_SECONDS_PER_STAGE` |

镜像已内置:`PORT=80`、`GIN_MODE=release`、`LOG_DIR=""`(不写日志文件)、`TRUSTED_PROXIES`=内网网段(信任云托管网关转发的客户端 IP)。在服务设置里同名变量可以覆盖。

## 配置(环境变量)

| 变量 | 默认 | 说明 |
|------|------|------|
| `PORT` | `8080` | HTTP 监听端口 |
| `REDIS_ADDR` | `127.0.0.1:6379` | Redis 地址 |
| `REDIS_PASSWORD` | (空) | Redis 密码 |
| `REDIS_DB` | `0` | Redis 逻辑库;测试号与正式号使用不同的库/实例 |
| `MYSQL_DATABASE` | `fc` | 由 `MYSQL_ADDRESS`/`MYSQL_USERNAME`/`MYSQL_PASSWORD` 拼连接时使用的库名 |
| `MYSQL_DSN` | (空) | 完整连接串,如 `user:pass@tcp(127.0.0.1:3306)/fc?charset=utf8mb4`,优先于 `MYSQL_ADDRESS` 等拆分变量;都没有时停用同步 |
| `RANK_SYNC_INTERVAL` | `60s` | Redis→MySQL 同步周期(排行榜与玩家资料共用) |
| `RANK_LIMIT` | `100` | 每级榜单长度 |
| `MAX_STAGE` | `100000` | 上报关卡数上限(≤9999999999);修为不设数值上限,以十进制字符串传输,最多 320 位 |
| `LEADERBOARD_HIDDEN` | `false` | `true` 时 `fetch_rank` 返回 `hidden:true` 且不返回任何榜单,前端隐藏整个排行榜(改后需重启) |
| `REGION_MIN_PLAYERS` | `10` | 城市榜至少需要的人数,不足时地榜显示省份榜;`0` 表示总用城市榜 |
| `REBUILD_RANK_FROM_DB` | `false` | `true` 时启动前从 MySQL 重建 Redis 榜单 |
| `WECHAT_APP_ID` | **必填** | 小游戏 AppID |
| `WECHAT_APP_SECRET` | **必填** | 小游戏 AppSecret,不得写入源码/文档/前端 |
| `WECHAT_HTTP_TIMEOUT` | `5s` | code2Session 超时 |
| `AUTH_SESSION_TTL` | `168h` | 业务会话有效期(5m~720h) |
| `LOGIN_RATE_LIMIT` / `LOGIN_RATE_WINDOW` | `20` / `1m` | 每个来源 IP 的登录尝试上限 |
| `TRUSTED_PROXIES` | (空) | 可信反向代理 IP/CIDR,逗号分隔;为空时忽略 `X-Forwarded-For`。部署在 Nginx/负载均衡/CDN 之后必须配置,否则登录限流与入库的 `client_ip` 都是代理的地址 |
| `CORS_ALLOW_ORIGINS` | (空) | H5 预览跨域白名单,逗号分隔;微信原生请求不需要 |
| `LOG_DIR` | `logs` | 日志目录,相对启动时的工作目录;可写绝对路径;显式设为空串则只输出到终端(容器部署用) |
| `LOG_CONSOLE` | `true` | 是否同时输出到终端(stderr);服务器上可设 `false` |
| `LOG_RETENTION_DAYS` | `30` | 保留包括当天在内最近 N 天的日志文件,`0` 为不清理 |
| `GIN_MODE` | `release` | 设为 `debug` 可看到 gin 的路由调试输出 |
| `RUN_TICKET_TTL` | `720h` | 对局票有效期(≥1h),覆盖存档后续玩 |
| `RUN_START_LIMIT` | `60` | 每个玩家每小时最多开局次数 |
| `MIN_SECONDS_PER_STAGE` | `0` | 每关最短耗时(秒),快于 关卡数×该值 的成绩被拒;0 为关闭 |
| `GAME_RECORD_QUEUE_MAX` | `100000` | 游戏记录、成就各自在 Redis 中等待写入 MySQL 的最大积压条数,满了返回 503 |
| `REBUILD_PLAYER_FROM_DB` | `false` | `true` 时启动前从 MySQL 按版本恢复玩家资料(Redis 丢失后、开放登录前使用) |

认证类变量写错(如 `AUTH_SESSION_TTL=abc`)会让启动失败,而不是静默回退默认值。

## 日志

JSON 格式(zap),按级别分文件写入 `LOG_DIR`,按服务器本地时间每天切换:

```
logs/
├── info-2026-10-31.log    只有 info(每个 HTTP 请求一行:方法、路径、状态、耗时、request_id)
├── warn-2026-10-31.log    只有 warn(被限流/风控拒绝的请求、MySQL 未配置等)
├── error-2026-10-31.log   error 及以上(5xx、同步失败、panic、启动失败),附调用栈
├── info.log -> info-2026-10-31.log   指向当天文件的软链接
├── warn.log -> …
└── error.log -> …
```

服务器上查看:`tail -F logs/error.log`。日志不含请求体、token、登录 code 和玩家资料原文;按 `request_id`(也在响应头 `X-Request-Id` 中)关联同一请求的多条日志。

## API

统一响应包裹:`{"code":200,"msg":"","data":{...}}`,`code` 与 HTTP 状态一致。完整契约与错误码见 [002 contracts/http-api.md](specs/002-wechat-login/contracts/http-api.md)(排行榜字段见 [001 contracts](specs/001-regional-rank-and-save/contracts/http-api.md))。

### POST /auth/wechat

```bash
curl -s -X POST localhost:8081/auth/wechat -H 'Content-Type: application/json' -d '{"code":"<wx.login 返回的 code>"}'
```

→ `{"code":200,"data":{"token":"…","expires_at":1793923200,"user":{"user_id":"wx_…","nickname":"无名","avatar_url":""}},"msg":""}`

### GET /users/me · PATCH /users/me/profile · POST /auth/logout

```bash
curl -s -X PATCH localhost:8081/users/me/profile -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"nickname":"阿甘","avatar_url":"https://thirdwx.qlogo.cn/…"}'
```

### POST /runs/start(需要 token)

```bash
curl -s -X POST localhost:8081/runs/start -H "Authorization: Bearer $TOKEN"
```

→ `{"code":200,"data":{"run_id":"…","seed":"Ab3dE9xQ","started_at_ms":…,"expires_at_ms":…},"msg":""}`。普通局用服务端下发的种子,结束时凭 `run_id` 上报成绩,每局只能提交一份;梦蝶局不领票、不上报。见 [specs/005](specs/005-run-ticket/contracts/http-api.md)。

### POST /upload_score(需要 token)

```bash
curl -s -X POST localhost:8081/upload_score -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"run_id":"<runs/start 返回>","location":{"province":"浙江省","city":"杭州市","district":"西湖区"},"stage":12,"stage_cultivation":"3400","total_cultivation":"98000","profile":{"nickname":"阿甘","avatar_url":"https://thirdwx.qlogo.cn/…/132"}}'
```

→ `{"code":200,"data":{"updated":true,"best":{"stage":12,"stage_cultivation":"3400","total_cultivation":"98000"}},"msg":""}`(仅刷新历史最好成绩时 `updated:true`)。玩家身份来自登录态;可选的 `profile` 是玩家授权后的微信昵称头像,会先保存到玩家资料再记分。

### POST /fetch_rank(需要 token)

```bash
curl -s -X POST localhost:8081/fetch_rank -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"location":{"province":"浙江省","city":"杭州市","district":"西湖区"}}'
```

→ `{"hidden":false,"global":{…},"region":{…},"friend":{…}}`(`hidden:true` 时前端隐藏整个排行榜):天榜、地榜(城市或省份,看 `scope`)、人榜,各含 `all`/`month`/`week` 三个窗口,每个榜前 100 名并带当前玩家的 `self_rank`/`self_score`;没有省份时 `region` 为 null,未同步好友时 `friend` 为 null。

### POST /friends/sync(需要 token)

客户端先 `wx.login` 取新 code,再调用 `wx.getRelationFriendList`,把 `code`、`encryptedData`、`iv` 一起提交;服务端用 code 换 `session_key` 解密出同玩好友并保存 30 天,供人榜使用。完整步骤见契约与 `dto/friend.go`。

### POST /upload_game_record(需要 token)

```bash
curl -s -X POST localhost:8081/upload_game_record -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"stage":3,"result":"win","score":1200}'
```

→ `{"code":200,"data":{"record_id":"<32 位十六进制>","created_at_ms":1793380000000},"msg":""}`。请求体就是记录本身(任意 JSON 对象,≤16 KiB),归属玩家取自 token;MySQL 写入是异步的(`RANK_SYNC_INTERVAL` 一轮)。按玩家查询:`SELECT * FROM player_game_record WHERE user_id = ? ORDER BY created_at_ms`。

### POST /upload_achievement(需要 token)

```bash
curl -s -X POST localhost:8081/upload_achievement -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"achievement_id":"daluo_10","detail":{"stage":37}}'
```

→ `{"code":200,"data":{"achievement_id":"daluo_10","newly_unlocked":true,"unlocked_at_ms":1793380000000},"msg":""}`。重复上传返回 `newly_unlocked:false` 与首次解锁时间,可安全重试。完整契约见 [specs/004](specs/004-achievement/contracts/http-api.md)。

> 排行榜接口需要 token,地区放在顶层 `location`(不再有 `user_info`)。客户端接入要求见 [契约](specs/002-wechat-login/contracts/http-api.md) 与 `internal/processor/dto` 中各 DTO 的注释。

## 测试

```bash
go test ./...
```

- 单测无外部依赖(微信用 `httptest` 与内存 fake,不需要真实密钥)
- Redis 集成测试连 `127.0.0.1:6379`:`repository_impl` 用 **DB 15**,HTTP 端到端用 **DB 14**(与 DB 0 隔离),Redis 不可达时自动跳过
- MySQL 集成测试仅在 `TEST_MYSQL_DSN` 设置时运行
