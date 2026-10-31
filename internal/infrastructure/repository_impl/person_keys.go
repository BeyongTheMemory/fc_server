package repository_impl

// Redis key 方案(specs/002 plan §6):
//
//	玩家资料: player:{user_id}            HASH,无 TTL
//	待备份:   player:dirty                HASH user_id → 待同步 revision
//	会话:     auth:session:{sha256(token)} HASH,TTL = 会话有效期
//	限流:     auth:ratelimit:{key}         计数,TTL = 限流窗口
//
// user_id 恒以 "wx_" 开头,不会与 player:dirty 冲突。
const (
	keyPrefixPlayer    = "player:"
	keyPlayerDirty     = "player:dirty"
	keyPrefixSession   = "auth:session:"
	keyPrefixRateLimit = "auth:ratelimit:"
)

func playerKey(userID string) string     { return keyPrefixPlayer + userID }
func sessionKey(tokenHash string) string { return keyPrefixSession + tokenHash }
func rateLimitKey(key string) string     { return keyPrefixRateLimit + key }
