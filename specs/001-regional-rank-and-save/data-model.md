# Data Model: 地区排行榜(US1)

**Date**: 2026-08-31 | **Plan**: [plan.md](./plan.md) | **Research**: [research.md](./research.md)

## 领域对象(internal/domain/rank)

### Location(值对象,已有,微调)

| 字段 | 类型 | 约束 |
|------|------|------|
| Province | string | 可空;≤32 字符;禁含 `|` |
| City | string | 可空;≤32 字符;禁含 `|` |
| District | string | 可空(区/县);≤32 字符;禁含 `|` |

规则:空字段的层级不参与榜单(spec Edge Case);三级全空时仅记录个人最高分元数据,不
进任何榜。层级归属必须按"省→市→区"前缀路径解释(R1),不再允许裸地名歧义。

### UserInfo(值对象,已有)

| 字段 | 类型 | 约束 |
|------|------|------|
| UserId | string | 必填;1~64 字符;禁含 `|` 与空白 |
| UserName | string | 必填;1~32 字符(展示用,随最近一次上报更新) |

### ScoreMeta(值对象,已有,语义收紧)

| 字段 | 类型 | 约束 |
|------|------|------|
| UserInfo | *UserInfo | 必填 |
| Score | int | ≥1 且 ≤ MAX_SCORE(默认 10^9);表示**历史最高分** |
| CreateTime | int64 | 达成最高分的 Unix 秒;由服务端生成,不信任客户端时间 |

### RankResult(实体,已有,重构)

| 字段 | 类型 | 说明 |
|------|------|------|
| Address | string | 地区名(展示用,如 "杭州市") |
| AddressLevel | vo.AddressLevel | Province/City/District |
| ScoreMetaList | []*ScoreMeta | 该级榜单,已按 score desc、同分 achieved_at asc 排好 |
| SelfRank | int64 | 请求玩家在该级的名次(1 起);未上榜为 0 |
| SelfScore | int | 请求玩家在该级的最高分;未上榜为 0 |

删除:`AddScoreMetaFromKey` / `AddNewScoreMetaAndReturnKey`(member 编解码逻辑随 R2 废弃)。

### 状态转移(玩家最高分)

```
无记录 --上传 s--> 最高分 = s(进入三级榜 + 标脏)
最高分 = b --上传 s>b--> 最高分 = s(更新三级榜 + 元数据 + 标脏)
最高分 = b --上传 s<=b--> 不变(幂等,无任何写入)
```

## Redis 结构(在线主存储,internal/infrastructure/repository_impl)

| Key | 类型 | 内容 | 说明 |
|-----|------|------|------|
| `rank:prov:{省}` | ZSET | member=userId, score=最高分 | 省级榜 |
| `rank:city:{省}\|{市}` | ZSET | 同上 | 市级榜(全路径防同名,R1) |
| `rank:dist:{省}\|{市}\|{区}` | ZSET | 同上 | 区/县级榜 |
| `rank:user:{userId}` | HASH | name, score, achieved_at, prov, city, dist | 玩家最高分元数据(单一事实源) |
| `rank:dirty` | SET | userId | 待同步 MySQL 的增量(R5) |

写路径:一段 Lua 原子完成"比较→ZADD(仅更高时)→HSET→SADD dirty"(R3)。
读路径:每级 `ZREVRANGE 0 99 WITHSCORES` + `ZREVRANK`/`ZSCORE`(自己)→ 管道
`HGETALL rank:user:{id}` 补齐元数据 → 应用层同分重排(R4)。

已知近似(记录于 R4):第 100 名边界同分成员取舍、`ZREVRANK` 同分名次按 member 字典
序,均为可接受近似,不影响 SC 验收。

## MySQL 结构(持久备份,internal/infrastructure/sync)

```sql
CREATE TABLE IF NOT EXISTS user_best_score (
  user_id     VARCHAR(64)  NOT NULL,
  user_name   VARCHAR(64)  NOT NULL DEFAULT '',
  score       BIGINT       NOT NULL,
  achieved_at BIGINT       NOT NULL,           -- Unix 秒
  province    VARCHAR(64)  NOT NULL DEFAULT '',
  city        VARCHAR(64)  NOT NULL DEFAULT '',
  district    VARCHAR(64)  NOT NULL DEFAULT '',
  updated_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP
                           ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id),
  KEY idx_region_score (province, city, district, score)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

Upsert(R6,GREATEST 防乱序回退):

```sql
INSERT INTO user_best_score
  (user_id, user_name, score, achieved_at, province, city, district)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  user_name   = IF(VALUES(score) >= score, VALUES(user_name), user_name),
  achieved_at = IF(VALUES(score) >  score, VALUES(achieved_at), achieved_at),
  province    = IF(VALUES(score) >= score, VALUES(province), province),
  city        = IF(VALUES(score) >= score, VALUES(city), city),
  district    = IF(VALUES(score) >= score, VALUES(district), district),
  score       = GREATEST(score, VALUES(score));
```

方向:Redis → MySQL(定时,R5);反向仅在显式重建时(R8)。MySQL 不在在线请求路径上。

## 校验规则汇总(processor 边界,FR-011)

| 规则 | 失败响应 |
|------|----------|
| user_id 必填、≤64、不含 `\|`/空白 | 400 |
| user_name 必填、≤32 | 400 |
| score 为整数且 1 ≤ score ≤ MAX_SCORE | 400 |
| 省/市/区各 ≤32、不含 `\|`;允许为空 | 400(超限/非法字符时) |
| fetch_rank 请求含 user_id(用于返回自己名次) | 400 |

## 时间窗口扩展(R12,2026-08-31)

| Key | 类型 | TTL | 说明 |
|-----|------|-----|------|
| `rank:w:{YYYY-Www}:prov:{省}` 等三级 | ZSET | 14 天(每次写入刷新) | 周榜,ISO 周(周一起),Asia/Shanghai |
| `rank:m:{YYYY-MM}:prov:{省}` 等三级 | ZSET | 62 天(每次写入刷新) | 月榜,自然月 |
| `rank:wuser:{userId}` | HASH | 14 天 | 周窗口元数据:win/name/score/achieved_at/prov/city/dist |
| `rank:muser:{userId}` | HASH | 62 天 | 月窗口元数据,字段同上 |

- 总榜 key 与 `rank:user:{id}`、`rank:dirty` 不变;MySQL 仅存总榜(user_best_score 不变)。
- `RankResult` 实体新增 `Window` 字段(`all`/`month`/`week`);`fetch_rank` 每次返回
  可用层级 × 3 窗口的全部榜单。
- 窗口滚动:Lua 中比较 HASH 的 `win` 字段与当前期号,期号不同视为"窗口内无记录"。
