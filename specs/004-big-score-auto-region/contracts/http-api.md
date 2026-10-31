# 004 HTTP 增量契约

日期：2026-10-31，待实现。保留 002 的 Bearer、响应 `{code,msg,data}`、请求体限制与错误脱敏约定。

## 大数版本：POST /v2/upload_score、POST /v2/fetch_rank

显式新路径避免破坏现有数值客户端；002 路径继续服务兼容范围的数据。新上传请求：

```json
{"stage":39,"stage_cultivation":"9007199254740993","total_cultivation":"19007199254740993"}
```

可选 profile 与 002 相同；不接受客户端 location 指定榜区，使用当前认证玩家保存的标准省市，未设置则不参与地榜。

`ScoreV2 = {stage: number, stage_cultivation: string, total_cultivation: string}`。

- v2 请求严格只接受规范十进制字符串（含小数、指数的字符串也不接受），规则和位数上限见 spec。
- 成功上传 `data = {updated: boolean, best: ScoreV2}`。
- v2 查询请求 `{}`，响应保持 `{global,region,friend}`，每类 `all/month/week`。Board 的 scope/address/address_level/window/self_rank、列表 rank/user_id/user_name/avatar_url/create_time 均不变；所有两项修为均改为 string，self_score 未上榜为 null。
- 关卡/排序优先级/时间窗口/地区回退/好友30天TTL保持 002 语义。
- 旧数值上传必须是精确安全整数，校验后转换到统一新内部模型，不走另一套排名系统。
- 旧数值查询或上传的 best 如需返回任一超过 Number.MAX_SAFE_INTEGER 的修为，返回 HTTP/code 409、固定 msg `score format upgrade required`，data=null。不得返回取整近似值；新版客户端转 v2 后解决。兼容适配器需在产生副作用前判断可返回性，避免写成功却返回升级错误；不得修改历史精确值以迎合旧协议。
- 400 非法输入/超长；401 无效登录；429 限流；503 依赖不可用。校验错误全请求无记分/资料更新副作用。

## POST /location/resolve

需要 Bearer。前端获得定位用途授权后调用 `wx.getFuzzyLocation({type:'gcj02'})`，仅上传该次返回值：

```json
{"latitude":30.2,"longitude":120.1,"coordinate_type":"gcj02"}
```

上例只是契约示例，联调不能用它代替玩家实际定位。

- latitude/longitude 必须为有限 JSON number，范围分别 [-90,90]/[-180,180]；coordinate_type 必填且仅支持 gcj02/wgs84。不得省略坐标系后猜测，不自行传入供应商 URL/key。
- 后端正确转换或指定坐标系，调用配置的逆地址服务，返回并保存标准地区。

成功：

```json
{"code":200,"msg":"","data":{"location":{"province":"浙江省","city":"杭州市","district":""},"resolved_at":1793400000}}
```

- province 非空为可用结果，city 无法确定时允许空、只参加省榜；district 统一为空，当前需求无需保存区县。直辖市省市字段按同一行政规范固定，不能制造两套榜。
- 默认每玩家每分钟最多 5 次（配置可调），上游总超时默认 3 秒。成功结果保存于玩家资料独立地区字段，与昵称头像分开；同一账号多设备复用。
- 失败不覆盖已有有效地区。未配置供应商 503 `location service unavailable`；超时504；上游失败502；海外/无可支持省份422 `region unavailable`；非法参数400；限流429。
- 不向客户端返回原始坐标、供应商详情、密钥。缓存只按玩家保存解析结果；不缓存带原始坐标的请求体。

## GET /users/me/location 与 POST /users/me/location/clear

均需 Bearer。GET 返回 `{location: {province,city,district} | null, resolved_at: number | null}`，不返回坐标。clear 请求 `{}`，返回 data=null，幂等删除保存地区，不删历史成绩，不撤销微信侧授权。

客户端只在微信定位仍授权时使用已存地区；主动撤权后清理，不自动回填。冷启动最多一次刷新定位，有效缓存期间切 tab 不重复定位。定位失败可保留最近有效地区并显示相应提示，用户可重新尝试。

## 与现有路由过渡

- `/fetch_rank` 与 `/upload_score` 省略 location 时也默认使用服务端已保存地区，保证当前前端在接上 resolve 后查询/参榜一致。
- v1 显式 location 暂按旧契约保留仅用于旧客户端兼容；v2 不允许手填地区。这不等于可靠反作弊：客户端坐标本身也可以伪造。
- 新地区字段纳入玩家持久化备份、版本更新和恢复，勿把单次 resolve 当成游戏成绩事件。

## 回归样例

- 0、9、10、9007199254740991、9007199254740992、9007199254740993、9223372036854775808、100位、1024位边界；1025位拒绝。
- 同关卡相邻大数、相同本关不同累计、不同关卡相反修为；总/月/周三类榜同样排序。
- 未定位 null、城市人数9/10的回退、仅省份、地区切换后新上传、清除授权、供应商超时、海外422；所有失败保留全服/好友可用。
