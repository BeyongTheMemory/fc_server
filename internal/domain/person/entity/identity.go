package entity

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// PlayerIDPrefix 微信身份玩家 ID 前缀。
const PlayerIDPrefix = "wx_"

// PlayerID 稳定玩家 ID(specs/002 plan §3):
//
//	"wx_" + base64url_without_padding(SHA256("v1\0" + appid + "\0" + openid))
//
// 结果固定 46 个 ASCII 字符。算法标识与分隔方式属于契约,修改会让所有玩家换 ID。
// 它只是标识,不是鉴权凭据。
func PlayerID(appID, openID string) string {
	sum := sha256.Sum256([]byte("v1\x00" + appID + "\x00" + openID))
	return PlayerIDPrefix + base64.RawURLEncoding.EncodeToString(sum[:])
}

// HashToken 业务 token 的存储键;明文 token 只交给客户端,不落存储与日志。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
