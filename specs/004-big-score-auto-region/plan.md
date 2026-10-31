# 004 实施计划（后端 agent）

按现有 Gin → processor/DTO → domain → infrastructure 分层，依赖沿用现有 Go/Redis/MySQL；不修改前端仓库。

## P1 精确成绩模型与存储

- `internal/domain/rank/entity/vo/score.go`：引入规范化非负十进制值类型；比较按长度、等长字典序，stage 与时间仍用安全有界整数。校验一次，后续使用可信模型。
- `internal/processor/dto/upload_score.go`、`fetch_rank_list.go`：增加 v2 DTO 与严格解析，所有列表/self_score/best 输出字符串；v1 适配在边界显式转换。
- `internal/infrastructure/repository_impl/rank_redis_storage.go` 及成员编码：消除两项修为的 Lua tonumber；继续 Redis 原子只升不降。ZSET score 保持常数，成员编码使用可证明保持三项降序和时间升序的十进制排序键（长度前缀+数字，或固定最大宽度补齐/反码）；不能直接将未补齐数字拼接。改变配置位宽需要重建版本化索引，不能运行中混用。
- `internal/infrastructure/sync/rank_mysql_sync.go`：两项修为改足够长度的 ASCII 十进制字符串列（最大4096位），不使用 BIGINT/DECIMAL(65)。若SQL需要比较，使用长度与 binary collation，不按普通VARCHAR直接大小比较。备份、重放、脏版本比较、恢复使用统一模型。
- 新命名空间/版本化排序成员，离线或受控停写迁移：先备份旧Redis和SQL；逐条验证旧安全整数并转换；原归属/时间/窗口TTL保留；重建索引并核对排名与成员数；切换读写；保留回滚数据。失败不自动清库。混合部署不得由旧实例覆盖新字符串数据，具体切换命令由实现补入 quickstart。

## P2 自动地区

- 新 domain location resolver provider（输入模糊坐标、输出标准省市），infrastructure 地图HTTP实现。默认建议腾讯位置服务，可按项目配置接入其他供应商但公共契约一致；密钥通过运行配置，不入仓库。
- processor 新增 resolve/get/clear DTO与路由；绑定登录身份；坐标范围/坐标系/限流/超时、海外不可用、上游错误映射。
- person 资料独立保存省市+resolved_at（含版本/备份/恢复），不保存坐标。不能让 PATCH 昵称头像意外清空地区。
- upload_score/fetch_rank 默认从服务端取地区；v2禁止客户端切区，v1兼容按契约。沿用现有按窗口人数回退省榜逻辑与地区历史成绩归属。

## P3 契约与联调

- 更新 README 配置及新契约说明，不把旧002的原型计划作为新实现事实。
- 假供应商+隔离 Redis/MySQL 测试覆盖正确性，之后8081上实测新契约；大数压测/迁移测试只能使用独立测试库/账号，不能污染真实榜。
- 前端本轮先联调既有 v1 天榜/人榜。后端交付后告知新路由、部署版本、地图服务是否配置；再接自动地区和 v2。
- 前端任意精度计分、达标比较、累计和存档迁移仍是独立依赖；仅新榜单读取可先适配字符串，不能宣称超安全整数上报已完成。
