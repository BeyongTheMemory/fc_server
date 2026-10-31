# HTTP API Contract: 地区排行榜(US1)

**Base**: `http://<host>:8080`(端口由 `PORT` 配置)| 所有请求/响应均为 `application/json; charset=utf-8`

## 统一响应包裹

```json
{ "code": 200, "msg": "", "data": { } }
```

| 场景 | HTTP 状态码 | code | msg | data |
|------|-------------|------|-----|------|
| 成功 | 200 | 200 | ""(或省略) | 业务载荷 |
| 参数非法 | 400 | 400 | 人类可读原因 | null |
| 内部错误 | 500 | 500 | 简要原因(不含堆栈/敏感信息) | null |

> 变更说明:修正现状"内部错误返回 HTTP 200"与绑定错误 `{"error":...}` 两种形状不一致
> 的问题(research.md R11)。客户端判断成功统一使用 `code == 200`。

## POST /upload_score

上报一局得分。服务端只在**高于历史最高分**时更新;重复/更低分上报无副作用(幂等)。

### Request

```json
{
  "user_info": {
    "user_id": "u_10086",
    "user_name": "阿甘",
    "location": {
      "province": "浙江省",
      "city": "杭州市",
      "district": "西湖区"
    }
  },
  "score": 1500
}
```

| 字段 | 必填 | 约束 |
|------|------|------|
| user_info.user_id | 是 | 1~64 字符,不含 `\|` 与空白 |
| user_info.user_name | 是 | 1~32 字符 |
| user_info.location.province/city/district | 否 | 各 ≤32 字符,不含 `\|`;空层级不参与该级榜单 |
| score | 是 | 整数,1 ≤ score ≤ 10^9 |

### Response(200)

```json
{ "code": 200, "msg": "", "data": { "updated": true, "best_score": 1500 } }
```

| 字段 | 说明 |
|------|------|
| data.updated | 本次是否刷新了历史最高分 |
| data.best_score | 当前(可能刚更新的)历史最高分 |

## POST /fetch_rank

拉取请求玩家所在省/市/区三级榜单(每级前 100 名)与自己在各级的名次。

### Request

```json
{
  "user_info": {
    "user_id": "u_10086",
    "user_name": "阿甘",
    "location": {
      "province": "浙江省",
      "city": "杭州市",
      "district": "西湖区"
    }
  }
}
```

约束同上;`location` 中为空的层级不会出现在响应中;`user_name` 此接口仅透传不落库。

### Response(200)

```json
{
  "code": 200,
  "msg": "",
  "data": {
    "rank_list": [
      {
        "address": "浙江省",
        "address_level": 1,
        "self_rank": 42,
        "self_score": 1500,
        "score_meta_list": [
          { "user_id": "u_1", "user_name": "第一名", "score": 99999, "create_time": 1756600000 }
        ]
      },
      { "address": "杭州市", "address_level": 2, "self_rank": 7, "self_score": 1500, "score_meta_list": [] },
      { "address": "西湖区", "address_level": 3, "self_rank": 1, "self_score": 1500, "score_meta_list": [] }
    ]
  }
}
```

| 字段 | 说明 |
|------|------|
| address_level | 1=省 2=市 3=区/县(与现有 `vo.AddressLevel` 一致) |
| score_meta_list | 按 score desc、同分按 create_time asc;最多 100 条;空榜为 `[]` |
| score_meta_list[].create_time | 达成该最高分的 Unix 秒(服务端时间) |
| self_rank | 该玩家在该级名次(1 起);未上榜为 0 |
| self_score | 该玩家在该级最高分;未上榜为 0 |

## 兼容性说明

- 端点路径、请求体结构与现有客户端约定保持不变;响应新增 `user_id`、`self_rank`、
  `self_score` 字段,移除无(客户端为 WIP,按本契约对齐)。
- 榜单长度从现实现的 10 调整为 100(spec FR-004)。

## 变更记录:时间窗口榜(2026-08-31)

`POST /fetch_rank` 响应的 `rank_list` 条目新增 **`window`** 字段:`"all"`(总榜)、
`"month"`(月榜)、`"week"`(周榜)。返回顺序:总→月→周,各窗口内省→市→区,最多
3 窗口 × 3 层级 = 9 个榜单对象;每个对象仍含 `address`/`address_level`/`self_rank`/
`self_score`/`score_meta_list`,规则与总榜一致。

`POST /upload_score` 请求不变;响应中 `updated`/`best_score` 语义仍指**总榜历史最高分**
(即使总榜未刷新,该次上报仍可能刷新周/月榜,由拉榜结果体现)。

月=自然月、周=ISO 周(周一起点),时间基准 Asia/Shanghai;进入新窗口后对应榜单自动从零
开始,旧窗口数据自动过期清理。
