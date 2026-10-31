# 任务清单：微信登录与玩家资料（002）

来源：[plan.md](plan.md) §10 · 契约：[contracts/http-api.md](contracts/http-api.md)

## P1 身份、存储与微信客户端（后端，已完成）

- [x] 配置：`WECHAT_APP_ID`/`WECHAT_APP_SECRET` 必填，`WECHAT_HTTP_TIMEOUT`、`AUTH_SESSION_TTL`、`LOGIN_RATE_*`、`TRUSTED_PROXIES`、`CORS_ALLOW_ORIGINS`、`REBUILD_PLAYER_FROM_DB`，非法值启动失败（`internal/config`）
- [x] 稳定玩家 ID 算法与固定测试向量（`domain/person/entity/identity.go`）
- [x] 资料校验规则（`domain/person/entity/vo/profile.go`）
- [x] person 应用服务：登录、认证、查本人、改资料、退出、登录限流（`domain/person/application`）
- [x] code2Session 客户端：超时、响应上限、errcode 分类、错误文本不含 URL/secret/code（`infrastructure/wechat`）
- [x] Redis 玩家/会话/限流存储，Lua 原子 upsert、revision 与脏标记（`infrastructure/repository_impl`）
- [x] 请求日志改为元数据（无请求体/响应体/请求头），先于登录路由生效（`util/gin_logger.go`）

## P2 HTTP 登录与资料、排行榜鉴权（后端，已完成）

- [x] `POST /auth/wechat`、`GET /users/me`、`PATCH /users/me/profile`、`POST /auth/logout`
- [x] Bearer 认证中间件；`/upload_score`、`/fetch_rank` 身份只取自登录态，昵称取服务端资料
- [x] 错误映射 400/401/403/413/429/502/503/504，5xx 文案不含内部细节
- [x] 16 KiB 请求体上限、`no-store`、可信代理与 CORS 白名单
- [x] 回归测试：A 的 token 携带 B 的 user_id 不能写 B 的分或查 B 的名次；日志不含 code/token/资料原文

## P4 资料备份与恢复（后端代码已完成，待联调）

- [x] `player_profile` 表与版本化增量同步：提交后按 revision 比较再清脏，失败与并发修改不丢更新（`infrastructure/sync/player_mysql_sync.go`）
- [x] `REBUILD_PLAYER_FROM_DB` 在开放 HTTP 前按 revision 恢复
- [ ] 用 `TEST_MYSQL_DSN` 运行 MySQL 集成测试（本地未配置，当前跳过）

## P3 Cocos 前端（`/Users/gang.xu/cocos/five_card`，未开始）

- [ ] `assets/auth/wechat_auth.ts`、`player_profile.ts`、`network/api_client.ts`
- [ ] 统一头像昵称组件，覆盖游戏/商店/结算三处；`wx.createUserInfoButton` 授权入口
- [ ] 排行榜请求带 Bearer token，移除 `user_id`/`user_name` 的可信语义

## 联调前置（需要账号管理员）

- [ ] 确认小游戏 AppID/AppSecret，配置隐私保护指引与资料授权能力
- [ ] 正式 HTTPS 域名与 request 合法域名；头像下载域名
- [ ] 开发者工具与两台真机验证同一 `user_id`
- [ ] 与客户端同步切换：旧客户端不带 token 调用排行榜将收到 401
