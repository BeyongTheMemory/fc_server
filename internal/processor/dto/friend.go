package dto

import (
	"errors"
	"fmt"
)

const (
	maxEncryptedDataBytes = 200 << 10
	maxIVBytes            = 64
)

// FriendSyncRequest POST /friends/sync,需要 `Authorization: Bearer <token>`。
// 把 wx.getRelationFriendList 的加密结果交给服务端解密,保存为人榜(好友榜)的好友列表。
//
//	{"code":"<新的 wx.login code>","encrypted_data":"<res.encryptedData>","iv":"<res.iv>"}
//
// 为什么前后端都要参与:wx.getRelationFriendList 只能在客户端调用,但它返回的是用 session_key
// 加密的数据;session_key 只能由服务端用 wx.login 的 code 向微信换取,所以由服务端解密。
// 服务端只保存好友对应的玩家 ID,不保存好友 openid,也不保存 session_key。
//
// 客户端集成步骤:
//  1. 需要基础库 ≥ 3.16.2(低版本隐藏人榜入口),并在小游戏后台开通该接口权限;
//     隐私保护指引中声明收集"好友关系(同玩互动好友)"用于好友排行榜。
//  2. 授权 scope.interactedUserInfo:首次调用 wx.getRelationFriendList 会弹窗,也可提前 wx.authorize;
//     用户拒绝后再调用直接进 fail(errMsg 含 "auth deny"),可用 wx.showModal + wx.openSetting 引导开启;
//     拒绝不影响天榜、地榜与游戏本身。
//  3. 顺序必须是:先 wx.login 取新 code → 再调用 wx.getRelationFriendList → 把 code、encryptedData、iv
//     一起提交。反过来会失败:wx.login 可能刷新 session_key,导致之前拿到的加密数据无法解密。
//     这个 code 只用于本次解密,不要再拿它去 /auth/wechat 登录(code 只能用一次)。
//  4. 时机:登录成功后同步一次;打开人榜时若 fetch_rank 返回的 friend 为 null 再同步;
//     之后每次启动同步一次即可(服务端保存 30 天)。同一时间只保留一个在途请求。
//  5. 失败处理:400(加密数据无效或不属于当前玩家)、401 且 msg 为 "invalid or expired login code"
//     时重新执行第 3 步一次,仍失败则放弃本次同步;token 失效的 401(msg 为 "unauthorized")按通用规则重新登录。
//  6. 只有也登录过本游戏并上传过成绩的好友才会出现在人榜里。
type FriendSyncRequest struct {
	Code          string `json:"code"`
	EncryptedData string `json:"encrypted_data"`
	Iv            string `json:"iv"`
}

func (r *FriendSyncRequest) Validate() error {
	if r == nil {
		return errors.New("request body is required")
	}
	if err := (&WechatLoginRequest{Code: r.Code}).Validate(); err != nil {
		return err
	}
	if r.EncryptedData == "" || len(r.EncryptedData) > maxEncryptedDataBytes {
		return fmt.Errorf("encrypted_data is required and must not exceed %d bytes", maxEncryptedDataBytes)
	}
	if r.Iv == "" || len(r.Iv) > maxIVBytes {
		return fmt.Errorf("iv is required and must not exceed %d bytes", maxIVBytes)
	}
	return nil
}

// FriendSyncResponse friend_count 为保存的好友数(不含自己,不论好友是否玩过本游戏)。
type FriendSyncResponse struct {
	FriendCount int `json:"friend_count"`
}
