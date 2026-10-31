package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	minSessionTTL = 5 * time.Minute
	maxSessionTTL = 30 * 24 * time.Hour
)

// Config 汇总全部运行配置,均来自环境变量,未设置时取默认值。
type Config struct {
	Port              string
	RedisAddr         string
	RedisPassword     string
	RedisDB           int
	MySQLDSN          string // 空表示停用 MySQL 同步;见 mysqlDSN 的取值顺序
	RankSyncInterval  time.Duration
	RankLimit         int
	RebuildRankFromDB bool

	// 认证相关配置(specs/002):解析失败不回退默认值,由 Validate 报告。
	WechatAppID         string
	WechatAppSecret     string
	WechatHTTPTimeout   time.Duration
	AuthSessionTTL      time.Duration
	LoginRateLimit      int           // 每个来源 IP 在一个窗口内允许的登录尝试次数
	LoginRateWindow     time.Duration // 登录限流窗口
	TrustedProxies      []string      // 可信反向代理(IP/CIDR);为空时只信任直连地址
	CORSAllowOrigins    []string      // H5 预览跨域白名单;为空时不处理跨域
	RebuildPlayerFromDB bool
	GameRecordQueueMax  int // 游戏记录、成就各自在 Redis 中等待写入 MySQL 的最大积压条数

	MaxStage          int64 // 上报关卡数上限;修为不设数值上限(以十进制字符串传输与存储)
	RegionMinPlayers  int   // 城市榜至少需要的人数,不足时地榜显示省份榜
	LeaderboardHidden bool  // 为 true 时 fetch_rank 返回 hidden=true,前端隐藏整个排行榜

	RunTicketTTL       time.Duration // 对局票有效期
	RunStartLimit      int           // 每个玩家每小时最多开局次数
	MinSecondsPerStage int           // 每关最短耗时(秒),0 表示不校验

	LogDir           string // 日志目录(相对启动时的工作目录),按 info/warn/error 分文件;显式设为空串则只输出到 stderr
	LogConsole       bool   // 是否同时输出到 stderr
	LogRetentionDays int    // 日志保留天数,0 表示不清理

	errs []error
}

func Load() *Config {
	c := &Config{
		Port:              getString("PORT", "8080"),
		RedisAddr:         getString("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword:     getString("REDIS_PASSWORD", ""),
		RedisDB:           getInt("REDIS_DB", 0),
		MySQLDSN:          mysqlDSN(),
		RankSyncInterval:  getDuration("RANK_SYNC_INTERVAL", 60*time.Second),
		RankLimit:         getInt("RANK_LIMIT", 100),
		RebuildRankFromDB: getBool("REBUILD_RANK_FROM_DB", false),

		WechatAppID:      strings.TrimSpace(os.Getenv("WECHAT_APP_ID")),
		WechatAppSecret:  strings.TrimSpace(os.Getenv("WECHAT_APP_SECRET")),
		TrustedProxies:   getList("TRUSTED_PROXIES"),
		CORSAllowOrigins: getList("CORS_ALLOW_ORIGINS"),
	}
	c.WechatHTTPTimeout = c.strictDuration("WECHAT_HTTP_TIMEOUT", 5*time.Second)
	c.AuthSessionTTL = c.strictDuration("AUTH_SESSION_TTL", 168*time.Hour)
	c.LoginRateLimit = c.strictInt("LOGIN_RATE_LIMIT", 20)
	c.LoginRateWindow = c.strictDuration("LOGIN_RATE_WINDOW", time.Minute)
	c.RebuildPlayerFromDB = c.strictBool("REBUILD_PLAYER_FROM_DB", false)
	c.GameRecordQueueMax = c.strictInt("GAME_RECORD_QUEUE_MAX", 100_000)
	c.MaxStage = int64(c.strictInt("MAX_STAGE", 100_000))
	c.RegionMinPlayers = c.strictInt("REGION_MIN_PLAYERS", 10)
	c.LeaderboardHidden = c.strictBool("LEADERBOARD_HIDDEN", false)
	c.RunTicketTTL = c.strictDuration("RUN_TICKET_TTL", 30*24*time.Hour)
	c.RunStartLimit = c.strictInt("RUN_START_LIMIT", 60)
	c.MinSecondsPerStage = c.strictInt("MIN_SECONDS_PER_STAGE", 0)
	c.LogDir = getStringAllowEmpty("LOG_DIR", "logs")
	c.LogConsole = c.strictBool("LOG_CONSOLE", true)
	c.LogRetentionDays = c.strictInt("LOG_RETENTION_DAYS", 30)
	return c
}

// Validate 检查认证接入所需的配置;错误信息只包含变量名,不包含取值。
func (c *Config) Validate() error {
	errs := append([]error{}, c.errs...)
	if c.WechatAppID == "" {
		errs = append(errs, errors.New("WECHAT_APP_ID is required"))
	}
	if c.WechatAppSecret == "" {
		errs = append(errs, errors.New("WECHAT_APP_SECRET is required"))
	}
	if c.WechatHTTPTimeout <= 0 {
		errs = append(errs, errors.New("WECHAT_HTTP_TIMEOUT must be positive"))
	}
	if c.AuthSessionTTL < minSessionTTL || c.AuthSessionTTL > maxSessionTTL {
		errs = append(errs, fmt.Errorf("AUTH_SESSION_TTL must be between %v and %v", minSessionTTL, maxSessionTTL))
	}
	if c.LoginRateLimit <= 0 {
		errs = append(errs, errors.New("LOGIN_RATE_LIMIT must be positive"))
	}
	if c.LoginRateWindow < time.Second {
		errs = append(errs, errors.New("LOGIN_RATE_WINDOW must be at least 1s"))
	}
	// 关卡数受排行榜编码约束(≤10 位十进制)
	if c.MaxStage < 1 || c.MaxStage > 9_999_999_999 {
		errs = append(errs, errors.New("MAX_STAGE must be between 1 and 9999999999"))
	}
	if c.RunTicketTTL < time.Hour {
		errs = append(errs, errors.New("RUN_TICKET_TTL must be at least 1h"))
	}
	if c.RunStartLimit <= 0 {
		errs = append(errs, errors.New("RUN_START_LIMIT must be positive"))
	}
	if c.MinSecondsPerStage < 0 {
		errs = append(errs, errors.New("MIN_SECONDS_PER_STAGE must not be negative"))
	}
	if c.RegionMinPlayers < 0 {
		errs = append(errs, errors.New("REGION_MIN_PLAYERS must not be negative"))
	}
	if c.LogRetentionDays < 0 {
		errs = append(errs, errors.New("LOG_RETENTION_DAYS must not be negative"))
	}
	if c.GameRecordQueueMax <= 0 {
		errs = append(errs, errors.New("GAME_RECORD_QUEUE_MAX must be positive"))
	}
	for _, p := range c.TrustedProxies {
		if net.ParseIP(p) == nil {
			if _, _, err := net.ParseCIDR(p); err != nil {
				errs = append(errs, errors.New("TRUSTED_PROXIES must be a comma-separated list of IPs or CIDRs"))
				break
			}
		}
	}
	return errors.Join(errs...)
}

func (c *Config) strictDuration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		c.errs = append(c.errs, fmt.Errorf("%s is not a valid duration", key))
		return def
	}
	return d
}

func (c *Config) strictInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		c.errs = append(c.errs, fmt.Errorf("%s is not a valid integer", key))
		return def
	}
	return n
}

func (c *Config) strictBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		c.errs = append(c.errs, fmt.Errorf("%s is not a valid boolean", key))
		return def
	}
	return b
}

// mysqlDSN 取值顺序:MYSQL_DSN(完整 DSN)→ MYSQL_ADDRESS/MYSQL_USERNAME/MYSQL_PASSWORD
// (微信云托管注入;本机写在 .env.local;库名取 MYSQL_DATABASE,默认 fc)→ 空(停用备份)。
func mysqlDSN() string {
	if dsn := os.Getenv("MYSQL_DSN"); dsn != "" {
		return dsn
	}
	addr := strings.TrimSpace(os.Getenv("MYSQL_ADDRESS"))
	if addr == "" {
		return ""
	}
	mc := mysql.NewConfig()
	mc.User = os.Getenv("MYSQL_USERNAME")
	mc.Passwd = os.Getenv("MYSQL_PASSWORD")
	mc.Net = "tcp"
	mc.Addr = addr
	mc.DBName = getString("MYSQL_DATABASE", "fc")
	mc.Timeout = 5 * time.Second
	mc.ReadTimeout = 30 * time.Second
	mc.WriteTimeout = 30 * time.Second
	mc.Params = map[string]string{"charset": "utf8mb4"}
	return mc.FormatDSN()
}

// getStringAllowEmpty 与 getString 相同,但变量显式设为空串时返回空串(用于关闭某项功能)。
func getStringAllowEmpty(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(v)
	}
	return def
}

func getString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getList(key string) []string {
	var out []string
	for _, part := range strings.Split(os.Getenv(key), ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

func getBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
