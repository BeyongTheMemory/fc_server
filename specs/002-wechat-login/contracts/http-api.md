# HTTP API 契约：微信登录与玩家资料（002）

日期：2026-10-31 · 来源：[plan.md](../plan.md) §5 · 取代 [001 契约](../../001-regional-rank-and-save/contracts/http-api.md) 中排行榜接口的身份语义

## 通用约定

- 响应包裹：`{"code": <HTTP 状态>, "msg": "<文案>", "data": <对象或 null>}`，`code` 与 HTTP 状态一致；错误时 `data` 为 `null`。
- 请求体上限 16 KiB（所有接口），超限返回 413。
- 受保护接口需要 `Authorization: Bearer <token>`，token 来自 `POST /auth/wechat`。
- `/auth/*`、`/users/*` 响应带 `Cache-Control: no-store`。
- 每个响应带 `X-Request-Id`（客户端可传入 ≤64 位 `[A-Za-z0-9_-]`，否则服务端生成），排查问题时提供该值。
- 401/403/429/5xx 的 `msg` 为固定文案，不含上游细节；400 的 `msg` 描述具体校验失败。

## 玩家 ID

```
user_id = "wx_" + base64url_without_padding(SHA256("v1\0" + appid + "\0" + openid))
```

固定 46 个 ASCII 字符。同一 `(AppID, openid)` 永远得到同一 ID；测试号与正式号产生不同 ID，不合并。
测试向量：`appid=wx1234567890abcdef`、`openid=oABCD-openid_example` → `wx_pXPJm6VxJyqTEh380eIxSDK0XOsmHwpS-waglkJKiw4`。
`user_id` 是标识而非凭据；`openid`、`session_key`、`appid` 不会出现在任何响应中。

## 用户对象 `User`

| 字段 | 类型 | 说明 |
|---|---|---|
| `user_id` | string | 见上 |
| `nickname` | string | 未授权资料时为 `"无名"` |
| `avatar_url` | string | 未授权或未提供时为 `""`，客户端显示默认头像 |

## POST /auth/wechat

无需 token。按来源 IP 限流（默认每分钟 20 次，`LOGIN_RATE_LIMIT`/`LOGIN_RATE_WINDOW`）。

请求：`{"code": "<wx.login 返回的 code>"}`，`code` 必填、≤1024 字节、不含空白或控制字符。

成功 200：

```json
{"code":200,"msg":"","data":{"token":"<43 字符随机串>","expires_at":1793923200,"user":{"user_id":"wx_…","nickname":"无名","avatar_url":""}}}
```

- `expires_at`：Unix 秒，默认签发后 7 天（`AUTH_SESSION_TTL`）。过期后重新 `wx.login` 登录，无 refresh token。
- 重复登录返回同一 `user_id`、已有资料和一个新 token；其他设备的 token 不受影响。

## GET /users/me

需要 token。返回 `User`。玩家记录缺失时返回 401（重新登录会重建）。

## PATCH /users/me/profile

需要 token，只更新当前玩家；请求中不接受 `user_id`。整体替换两个字段：

```json
{"nickname": "玩家昵称", "avatar_url": "https://thirdwx.qlogo.cn/…"}
```

| 字段 | 规则 |
|---|---|
| `nickname` | 必填；1～64 个 Unicode 码点；合法 UTF-8；不能全为空白；不含控制字符（允许 emoji 与 `\|`） |
| `avatar_url` | 可为空；否则 ≤2048 字节、`https`、主机名合法、不含 `user:pass@`、不含空白 |

成功返回更新后的 `User`。资料与现有完全相同时不产生新版本。资料是客户端授权后提交的展示信息，不改变 `user_id` 或权限。

## POST /auth/logout

需要 token。删除当前 token 的会话，返回 `{"code":200,"msg":"","data":null}`。不影响其他设备、不删除玩家、不撤销微信授权。

## 资料授权（客户端要求）

- 首次获取头像昵称必须由玩家点击原生 `wx.createUserInfoButton` 触发（`withCredentials: false`），不能用 `wx.authorize({scope: 'scope.userInfo'})` 代替；已授权后可用 `wx.getUserInfo` 静默刷新。小游戏不受 2022 年小程序头像昵称匿名化调整的影响。
- 提交 `nickname = userInfo.nickName`、`avatar_url = userInfo.avatarUrl`，原样提交，不要改写。
- 拒绝、取消或失败时不提交资料，也不阻塞单机游玩；业务 token 过期与头像授权相互独立。
- 微信头像 URL 在玩家更换头像后失效：已授权玩家建议每次 `/upload_score` 都带最新 `profile`。
- 小游戏后台的隐私保护指引须声明收集“用户信息（昵称、头像）”用于排行榜展示；如使用定位还需声明位置信息。

## POST /upload_score

需要 token。身份只来自 token，请求中的 `user_id`/`user_name` 等未知字段被忽略。每局结束上报一次。

```json
{"run_id":"<POST /runs/start 返回>",
 "location":{"province":"浙江省","city":"杭州市","district":"西湖区"},
 "stage":38,"stage_cultivation":"931009731811524","total_cultivation":"1.2345678901234567e+21",
 "profile":{"nickname":"阿甘","avatar_url":"https://thirdwx.qlogo.cn/…/132"}}
```

| 字段 | 规则 |
|---|---|
| `run_id` | 必填，本局开局时 `POST /runs/start` 签发的对局票，规则见 [005 契约](../../005-run-ticket/contracts/http-api.md)（每局只能提交一份成绩；梦蝶局不领票、不上报） |
| `stage` | 必填，本局到达的关卡数，1 ～ `MAX_STAGE`（默认 100000）的整数 |
| `stage_cultivation` | 必填，最后一关获得的修为，非负整数，见下方“修为数值协议” |
| `total_cultivation` | 必填，整局获得的修为总数（含最后一关），必须 ≥ `stage_cultivation` |

**修为数值协议**（大罗无尽模式的修为会超过 JavaScript 安全整数 2^53−1 和 int64）：

- 修为以**十进制字符串**传输，不限大小，最多 320 位（覆盖 `Number.MAX_VALUE`）。服务端按字符串精确比较与保存，全链路不经过浮点。
- 前端写法：`String(Math.floor(x))`。`x < 1e21` 时是纯数字；`x ≥ 1e21` 时 JavaScript 输出科学计数法（如 `"9.31009731811524e+23"`），服务端按它表示的整数值规范化。两种形式都接受。`x` 不是有限数（NaN/Infinity）时不上报。
- Number 只有约 17 位有效数字，超过 2^53 的修为在前端本来就是近似值；服务端如实保存这个整数值，不额外截断或放大。
- 兼容：修为也可以是 JSON 整数，但只接受 ≤ `9007199254740991`。更大的 JSON 数字会被 400 拒绝，提示改用字符串，防止传输中被静默舍入。
- 不接受负数、小数、空值、布尔值。
- 响应中的修为**一律是规范化的纯数字字符串**（无前导零）。展示时可以 `Number(s)` 后格式化为万/亿；比较两个成绩时先比字符串长度再比字典序，不要转成 Number 再比较。
| `location` | 可选，见下文“地区” |
| `profile` | 可选，规则同 `PATCH /users/me/profile`。带上时先保存资料再记分；省略时沿用已保存资料 |

**排名规则**：先比 `stage`，相同比 `stage_cultivation`，再相同比 `total_cultivation`，仍相同则先达成者在前。每个榜只保留玩家最好的一局（总榜、本月、本周各自比较）。

- 任一字段不合法：整个请求 400，**不记分、不改资料**。小数、字符串都不合法。
- 成功：`{"updated": true, "best": {"stage":38,"stage_cultivation":"931009731811524","total_cultivation":"1234567890123456700000"}}`。`updated` 表示本局刷新了历史最好成绩，`best` 为当前历史最好成绩。

## POST /fetch_rank

需要 token。`{"location":{"province":"浙江省","city":"杭州市","district":"西湖区"}}`

一次返回三类榜单，每类含 `all`（总榜）/`month`（本月）/`week`（本周）三个窗口：

```json
{"hidden": false,
 "global": {"all": Board, "month": Board, "week": Board},
 "region": {"all": Board, "month": Board, "week": Board},
 "friend": {"all": Board, "month": Board, "week": Board}}
```

`hidden`：服务端配置 `LEADERBOARD_HIDDEN=true` 时为 `true`，此时三类榜单都是 `null`。前端读到 `true` 时隐藏整个排行榜（入口、页面、我的排名），不显示空榜或报错；每次以接口返回为准，不跨启动缓存。

| 榜单 | 含义 | 为 null 的情况 |
|---|---|---|
| `global` 天榜 | 全国所有玩家 | 不会 |
| `region` 地榜 | 玩家所在城市；某个窗口里该城市人数少于 `REGION_MIN_PLAYERS`（默认 10）时，该窗口改用省份榜；只上报了省份时直接是省份榜 | 没有上报省份 |
| `friend` 人榜 | 自己 + 同玩互动好友（需先 `POST /friends/sync`） | 从未同步或同步已过期（30 天） |

`Board`：

| 字段 | 说明 |
|---|---|
| `scope` | `global` / `city` / `province` / `friend`，地榜据此显示“杭州市榜”或“浙江省榜” |
| `address` / `address_level` | 地榜的城市或省份名与级别（1 省、2 市），其余为 `""` / `0` |
| `window` | `all` / `month` / `week` |
| `self_rank` | 自己的名次，1 起；`0` 表示未上榜 |
| `self_score` | 自己在该榜的成绩 `{stage, stage_cultivation, total_cultivation}`（修为为字符串）；未上榜为 `null` |
| `score_meta_list` | 前 N 名（`RANK_LIMIT`，默认 100），已排好序 |

榜单条目：

| 字段 | 说明 |
|---|---|
| `rank` | 名次，1 起 |
| `user_id` | 玩家 ID |
| `user_name` | 该玩家**当前**资料的昵称（未授权为 `"无名"`）；改名后对历史榜单立即生效 |
| `avatar_url` | 该玩家当前头像 URL，可能为 `""` |
| `stage` / `stage_cultivation` / `total_cultivation` | 该窗口内的最好成绩；`stage` 为数字，两项修为为十进制字符串 |
| `create_time` | 达成时间（Unix 秒） |

客户端要求：

- 三类榜单一起返回，切换 tab 不需要重复请求；结果可缓存几十秒。
- `friend` 为 `null` 时先调用 `POST /friends/sync` 再刷新。
- `avatar_url` 为空或加载失败（对方已换头像）时显示默认头像，不阻塞榜单渲染。
- URL 末尾数字为正方形尺寸（`0`/`46`/`64`/`96`/`132`，`0` 为 640px），列表中可替换为 `/64` 节省流量。
- 远程加载头像需把头像域名（如 `thirdwx.qlogo.cn`）加入小游戏后台 downloadFile 合法域名，以真机加载结果为准。

## POST /friends/sync

需要 token，请求体上限 256 KiB。把 `wx.getRelationFriendList` 的加密结果交给服务端解密，保存为人榜的好友列表。

```json
{"code":"<新的 wx.login code>","encrypted_data":"<res.encryptedData>","iv":"<res.iv>"}
```

成功：`{"friend_count": 2}`（保存的好友数，不含自己）。

**为什么前后端都要参与**：`wx.getRelationFriendList`（基础库 ≥ 3.16.2，需授权 `scope.interactedUserInfo`）只能在客户端调用，返回的是用 `session_key` 加密的数据；`session_key` 只能由服务端用 `wx.login` 的 code 向微信换取，所以由服务端解密。服务端只保存好友对应的玩家 ID，不保存好友 openid 与 `session_key`。

客户端集成步骤：

1. 在小游戏后台开通该接口；隐私保护指引中声明收集“好友关系（同玩互动好友）”用于好友排行榜。基础库低于 3.16.2 时隐藏人榜入口。
2. 授权 `scope.interactedUserInfo`：首次调用会弹窗，也可提前 `wx.authorize`；拒绝后再调用直接进 fail（`errMsg` 含 `auth deny`），可用 `wx.showModal` + `wx.openSetting` 引导。拒绝不影响天榜、地榜与游戏本身。
3. **顺序必须是**：先 `wx.login` 取新 code → 再 `wx.getRelationFriendList` → 把 `code`、`encryptedData`、`iv` 一起提交。反过来会失败（`wx.login` 可能刷新 `session_key`）。这个 code 只用于本次解密，不要再拿去 `/auth/wechat`。
4. 时机：登录成功后同步一次；`fetch_rank` 返回的 `friend` 为 `null` 时再同步；之后每次启动同步一次即可（服务端保存 30 天）。同一时间只保留一个在途请求。
5. 失败处理：400（加密数据无效、不属于当前玩家）或 401 且 `msg` 为 `invalid or expired login code` 时，重新执行第 3 步一次，仍失败则放弃本次同步。token 失效的 401（`msg` 为 `unauthorized`）按通用规则重新登录。
6. 只有也登录过本游戏并上传过成绩的好友才会出现在人榜里。

## 地区 `location`

- 微信 `UserInfo` 已不再返回 `province`/`city`/`country`，地区需通过 `wx.getLocation`（需隐私声明与授权）加逆地址解析得到，或由玩家手动选择。
- 只填到已知的最细层级：市级榜需要省+市，区级榜需要省+市+区；`location` 可整体省略（不参与地区榜）。
- 各字段 ≤32 个字符，不能包含 `|`。

## 错误码

| 情况 | HTTP / code | `msg` |
|---|---:|---|
| 参数格式、长度或资料内容非法 | 400 | 具体校验信息 |
| 好友加密数据无法解密或不属于本小游戏 | 400 | `invalid encrypted data, retry with a fresh wx.login code` |
| 好友数据属于另一个微信号 | 400 | `friend data does not belong to current player` |
| token 缺失/无效/过期/已退出 | 401 | `unauthorized` |
| 微信 code 无效或已使用（40029/40163） | 401 | `invalid or expired login code` |
| 微信风险拦截（40226） | 403 | `login is temporarily unavailable` |
| 微信限流（45011）或本服务登录限流 | 429 | `too many requests, retry later` |
| 请求体超过 16 KiB（`/friends/sync` 为 256 KiB） | 413 | `request body too large` |
| 微信非预期响应、JSON 错误、AppID/Secret 错误等 | 502 | `upstream service error` |
| Redis 等依赖不可用（不会签发 token） | 503 | `service unavailable` |
| 微信调用超时 | 504 | `upstream timeout`（下次使用新 code） |
| 其他内部错误 | 500 | `internal server error` |

客户端处理：401 时重新 `wx.login` 并登录，最多自动恢复一次；429 等待后再试；所有失败都不阻塞单机游玩。
