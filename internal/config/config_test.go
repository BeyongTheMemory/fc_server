package config

import (
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestLoadDefaults(t *testing.T) {
	cfg := Load()
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.RedisAddr != "127.0.0.1:6379" {
		t.Errorf("RedisAddr = %q, want 127.0.0.1:6379", cfg.RedisAddr)
	}
	if cfg.RedisDB != 0 {
		t.Errorf("RedisDB = %d, want 0", cfg.RedisDB)
	}
	if cfg.MySQLDSN != "" {
		t.Errorf("MySQLDSN = %q, want empty", cfg.MySQLDSN)
	}
	if cfg.RankSyncInterval != 60*time.Second {
		t.Errorf("RankSyncInterval = %v, want 60s", cfg.RankSyncInterval)
	}
	if cfg.RankLimit != 100 {
		t.Errorf("RankLimit = %d, want 100", cfg.RankLimit)
	}
	if cfg.RebuildRankFromDB {
		t.Error("RebuildRankFromDB = true, want false")
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("REDIS_ADDR", "10.0.0.1:6380")
	t.Setenv("REDIS_DB", "3")
	t.Setenv("MYSQL_DSN", "user:pass@tcp(127.0.0.1:3306)/fc")
	t.Setenv("RANK_SYNC_INTERVAL", "5s")
	t.Setenv("RANK_LIMIT", "50")
	t.Setenv("REBUILD_RANK_FROM_DB", "true")

	cfg := Load()
	if cfg.Port != "9090" || cfg.RedisAddr != "10.0.0.1:6380" || cfg.RedisDB != 3 {
		t.Errorf("basic overrides not applied: %+v", cfg)
	}
	if cfg.MySQLDSN != "user:pass@tcp(127.0.0.1:3306)/fc" {
		t.Errorf("MySQLDSN = %q", cfg.MySQLDSN)
	}
	if cfg.RankSyncInterval != 5*time.Second || cfg.RankLimit != 50 {
		t.Errorf("rank overrides not applied: %+v", cfg)
	}
	if !cfg.RebuildRankFromDB {
		t.Error("RebuildRankFromDB = false, want true")
	}
}

func TestLoadInvalidValuesFallBack(t *testing.T) {
	t.Setenv("REDIS_DB", "not-a-number")
	t.Setenv("RANK_SYNC_INTERVAL", "banana")
	t.Setenv("REBUILD_RANK_FROM_DB", "maybe")

	cfg := Load()
	if cfg.RedisDB != 0 {
		t.Errorf("RedisDB = %d, want fallback 0", cfg.RedisDB)
	}
	if cfg.RankSyncInterval != 60*time.Second {
		t.Errorf("RankSyncInterval = %v, want fallback 60s", cfg.RankSyncInterval)
	}
	if cfg.RebuildRankFromDB {
		t.Error("RebuildRankFromDB should fall back to false")
	}
}

func TestLoadNegativeIntervalFallsBack(t *testing.T) {
	t.Setenv("RANK_SYNC_INTERVAL", "-10s")
	if cfg := Load(); cfg.RankSyncInterval != 60*time.Second {
		t.Errorf("RankSyncInterval = %v, want fallback 60s", cfg.RankSyncInterval)
	}
}

func setWechatEnv(t *testing.T) {
	t.Helper()
	t.Setenv("WECHAT_APP_ID", "wx_test_app")
	t.Setenv("WECHAT_APP_SECRET", "test-secret-value")
}

func TestAuthDefaults(t *testing.T) {
	setWechatEnv(t)
	cfg := Load()
	if cfg.WechatHTTPTimeout != 5*time.Second {
		t.Errorf("WechatHTTPTimeout = %v, want 5s", cfg.WechatHTTPTimeout)
	}
	if cfg.AuthSessionTTL != 168*time.Hour {
		t.Errorf("AuthSessionTTL = %v, want 168h", cfg.AuthSessionTTL)
	}
	if cfg.RunTicketTTL != 30*24*time.Hour || cfg.RunStartLimit != 60 || cfg.MinSecondsPerStage != 0 {
		t.Errorf("run defaults = %v/%d/%d", cfg.RunTicketTTL, cfg.RunStartLimit, cfg.MinSecondsPerStage)
	}
	if cfg.LeaderboardHidden {
		t.Error("LeaderboardHidden should default to false")
	}
	if cfg.MaxStage != 100_000 || cfg.RegionMinPlayers != 10 {
		t.Errorf("score limits = %d/%d", cfg.MaxStage, cfg.RegionMinPlayers)
	}
	if cfg.LogDir != "logs" || !cfg.LogConsole || cfg.LogRetentionDays != 30 {
		t.Errorf("log defaults = %q/%v/%d, want logs/true/30", cfg.LogDir, cfg.LogConsole, cfg.LogRetentionDays)
	}
	if cfg.GameRecordQueueMax != 100_000 {
		t.Errorf("GameRecordQueueMax = %d, want 100000", cfg.GameRecordQueueMax)
	}
	if cfg.LoginRateLimit != 20 || cfg.LoginRateWindow != time.Minute {
		t.Errorf("login rate = %d/%v, want 20/1m", cfg.LoginRateLimit, cfg.LoginRateWindow)
	}
	if cfg.RebuildPlayerFromDB || len(cfg.TrustedProxies) != 0 || len(cfg.CORSAllowOrigins) != 0 {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestAuthOverrides(t *testing.T) {
	setWechatEnv(t)
	t.Setenv("WECHAT_HTTP_TIMEOUT", "2s")
	t.Setenv("AUTH_SESSION_TTL", "24h")
	t.Setenv("LOGIN_RATE_LIMIT", "5")
	t.Setenv("LOGIN_RATE_WINDOW", "30s")
	t.Setenv("TRUSTED_PROXIES", "10.0.0.1, 192.168.0.0/16 ,")
	t.Setenv("CORS_ALLOW_ORIGINS", "http://localhost:7456")
	t.Setenv("REBUILD_PLAYER_FROM_DB", "true")

	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	if cfg.WechatHTTPTimeout != 2*time.Second || cfg.AuthSessionTTL != 24*time.Hour ||
		cfg.LoginRateLimit != 5 || cfg.LoginRateWindow != 30*time.Second || !cfg.RebuildPlayerFromDB {
		t.Errorf("overrides not applied: %+v", cfg)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[1] != "192.168.0.0/16" {
		t.Errorf("TrustedProxies = %v", cfg.TrustedProxies)
	}
	if len(cfg.CORSAllowOrigins) != 1 || cfg.CORSAllowOrigins[0] != "http://localhost:7456" {
		t.Errorf("CORSAllowOrigins = %v", cfg.CORSAllowOrigins)
	}
}

func TestValidateRejectsMissingOrInvalidAuthConfig(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"missing app id", map[string]string{"WECHAT_APP_ID": ""}, "WECHAT_APP_ID"},
		{"missing secret", map[string]string{"WECHAT_APP_SECRET": ""}, "WECHAT_APP_SECRET"},
		{"unparsable timeout", map[string]string{"WECHAT_HTTP_TIMEOUT": "soon"}, "WECHAT_HTTP_TIMEOUT"},
		{"zero timeout", map[string]string{"WECHAT_HTTP_TIMEOUT": "0s"}, "WECHAT_HTTP_TIMEOUT"},
		{"ttl too short", map[string]string{"AUTH_SESSION_TTL": "1m"}, "AUTH_SESSION_TTL"},
		{"ttl too long", map[string]string{"AUTH_SESSION_TTL": "9999h"}, "AUTH_SESSION_TTL"},
		{"bad rate limit", map[string]string{"LOGIN_RATE_LIMIT": "many"}, "LOGIN_RATE_LIMIT"},
		{"bad rebuild flag", map[string]string{"REBUILD_PLAYER_FROM_DB": "maybe"}, "REBUILD_PLAYER_FROM_DB"},
		{"bad proxy", map[string]string{"TRUSTED_PROXIES": "10.0.0.1,not-an-ip"}, "TRUSTED_PROXIES"},
		{"bad record queue max", map[string]string{"GAME_RECORD_QUEUE_MAX": "0"}, "GAME_RECORD_QUEUE_MAX"},
		{"negative log retention", map[string]string{"LOG_RETENTION_DAYS": "-1"}, "LOG_RETENTION_DAYS"},
		{"stage too large", map[string]string{"MAX_STAGE": "99999999999"}, "MAX_STAGE"},
		{"negative region min", map[string]string{"REGION_MIN_PLAYERS": "-1"}, "REGION_MIN_PLAYERS"},
		{"short run ttl", map[string]string{"RUN_TICKET_TTL": "10m"}, "RUN_TICKET_TTL"},
		{"bad leaderboard hidden", map[string]string{"LEADERBOARD_HIDDEN": "yes please"}, "LEADERBOARD_HIDDEN"},
		{"zero run limit", map[string]string{"RUN_START_LIMIT": "0"}, "RUN_START_LIMIT"},
		{"negative min seconds", map[string]string{"MIN_SECONDS_PER_STAGE": "-1"}, "MIN_SECONDS_PER_STAGE"},
		{"bad log console", map[string]string{"LOG_CONSOLE": "sometimes"}, "LOG_CONSOLE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setWechatEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			err := Load().Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want error mentioning %s", err, tt.want)
			}
			if strings.Contains(err.Error(), "test-secret-value") {
				t.Errorf("validation error leaks secret: %v", err)
			}
		})
	}
}

func TestMySQLDSNFromCloudRunEnv(t *testing.T) {
	t.Setenv("MYSQL_ADDRESS", "10.33.108.9:3306")
	t.Setenv("MYSQL_USERNAME", "root")
	t.Setenv("MYSQL_PASSWORD", "p@ss:w/rd#1")

	dsn := Load().MySQLDSN
	mc, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN(%q): %v", dsn, err)
	}
	if mc.Addr != "10.33.108.9:3306" || mc.User != "root" || mc.Passwd != "p@ss:w/rd#1" || mc.DBName != "fc" ||
		!strings.Contains(dsn, "charset=utf8mb4") || mc.Timeout != 5*time.Second {
		t.Errorf("parsed DSN = %+v", mc)
	}

	t.Setenv("MYSQL_DATABASE", "fc_prod")
	if mc, _ := mysql.ParseDSN(Load().MySQLDSN); mc.DBName != "fc_prod" {
		t.Errorf("MYSQL_DATABASE not applied: %+v", mc)
	}
	// 完整 DSN 优先于拆分的变量
	t.Setenv("MYSQL_DSN", "u:p@tcp(1.2.3.4:3306)/x?charset=utf8mb4")
	if got := Load().MySQLDSN; got != "u:p@tcp(1.2.3.4:3306)/x?charset=utf8mb4" {
		t.Errorf("MYSQL_DSN should win: %q", got)
	}
}

func TestLogDirCanBeDisabled(t *testing.T) {
	t.Setenv("LOG_DIR", "")
	if dir := Load().LogDir; dir != "" {
		t.Errorf("LOG_DIR set to empty should disable file logs, got %q", dir)
	}
}

func TestNoMySQLConfigDisablesBackup(t *testing.T) {
	t.Setenv("MYSQL_DSN", "")
	t.Setenv("MYSQL_ADDRESS", "")
	if dsn := Load().MySQLDSN; dsn != "" {
		t.Errorf("MySQLDSN = %q, want empty (backup disabled)", dsn)
	}
}
