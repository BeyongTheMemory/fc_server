package sync

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"fc_server/internal/config"
	"fc_server/internal/domain/rank/entity/vo"
	"fc_server/internal/infrastructure/repository_impl"
	"fc_server/internal/util"

	"github.com/redis/go-redis/v9"
)

// 集成测试:仅在 TEST_MYSQL_DSN 设置时运行;Redis 部分使用本地 DB 15。
func newTestSyncer(t *testing.T) (*Syncer, *repository_impl.RankRedisStorage, *sql.DB) {
	t.Helper()
	util.InitLogger()

	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set; skipping MySQL sync integration test")
	}

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379", DB: 15})
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("redis not available: %v", err)
	}
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flush redis test db: %v", err)
	}
	storage := repository_impl.NewRankRedisStorage(client)

	cfg := &config.Config{MySQLDSN: dsn, RankSyncInterval: time.Second}
	syncer, err := New(cfg, storage)
	if err != nil {
		t.Fatalf("New syncer: %v", err)
	}
	if err := syncer.ensureSchema(ctx); err != nil {
		t.Skipf("mysql not reachable: %v", err)
	}
	if _, err := syncer.db.ExecContext(ctx, "DELETE FROM user_best_score"); err != nil {
		t.Fatalf("clean table: %v", err)
	}

	t.Cleanup(func() {
		_, _ = syncer.db.ExecContext(context.Background(), "DELETE FROM user_best_score")
		_ = syncer.db.Close()
		_ = client.FlushDB(context.Background()).Err()
		_ = client.Close()
	})
	return syncer, storage, syncer.db
}

func execUpsert(t *testing.T, db *sql.DB, id, name string, s vo.Score, at int64) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), upsertSQL, id, name, s.Stage, s.StageCultivation.String(), s.TotalCultivation.String(),
		repository_impl.ScoreKey(s), at, "203.0.113.9", "浙江省", "杭州市", "西湖区"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
}

func readBest(t *testing.T, db *sql.DB, id string) (vo.Score, string, int64) {
	t.Helper()
	var s vo.Score
	var name, last, total string
	var at int64
	if err := db.QueryRowContext(context.Background(),
		"SELECT stage, stage_cultivation, total_cultivation, user_name, achieved_at FROM user_best_score WHERE user_id = ?", id).
		Scan(&s.Stage, &last, &total, &name, &at); err != nil {
		t.Fatalf("scan: %v", err)
	}
	s.StageCultivation, s.TotalCultivation = vo.Cultivation(last), vo.Cultivation(total)
	return s, name, at
}

func TestUpsertNeverRegresses(t *testing.T) {
	_, _, db := newTestSyncer(t)
	// 超过 int64 的修为也按数值比较
	best := vo.Score{Stage: 9, StageCultivation: "50", TotalCultivation: vo.Cultivation("1" + strings.Repeat("0", 40))}
	execUpsert(t, db, "u_1", "v1", best, 1000)
	// 乱序到达的更差批次不得回退:关卡少;或关卡相同但最后一关修为更少;或总修为位数更少
	execUpsert(t, db, "u_1", "stale", vo.Score{Stage: 8, StageCultivation: "999", TotalCultivation: "999"}, 900)
	execUpsert(t, db, "u_1", "stale", vo.Score{Stage: 9, StageCultivation: "49", TotalCultivation: "999"}, 900)
	execUpsert(t, db, "u_1", "stale", vo.Score{Stage: 9, StageCultivation: "50", TotalCultivation: vo.Cultivation(strings.Repeat("9", 40))}, 900)
	if got, name, at := readBest(t, db, "u_1"); got != best || name != "v1" || at != 1000 {
		t.Errorf("after stale upserts: %+v %s %d", got, name, at)
	}
	better := vo.Score{Stage: 9, StageCultivation: "50", TotalCultivation: vo.Cultivation("1" + strings.Repeat("0", 39) + "1")}
	execUpsert(t, db, "u_1", "v2", better, 1100)
	if got, name, at := readBest(t, db, "u_1"); got != better || name != "v2" || at != 1100 {
		t.Errorf("after better upsert: %+v %s %d", got, name, at)
	}
}

func TestSyncRoundConsumesDirtySet(t *testing.T) {
	syncer, storage, db := newTestSyncer(t)
	ctx := context.Background()
	s := vo.Score{Stage: 12, StageCultivation: "3400", TotalCultivation: "98000"}
	loc := &vo.Location{Province: "浙江省", City: "杭州市", District: "西湖区"}
	if _, _, err := storage.UploadBest(ctx, loc, &vo.UserInfo{UserId: "u_1", UserName: "阿甘"}, s, "203.0.113.9"); err != nil {
		t.Fatalf("seed upload: %v", err)
	}

	syncer.syncRound(ctx)

	if got, name, _ := readBest(t, db, "u_1"); got != s || name != "阿甘" {
		t.Errorf("synced row = %+v %s", got, name)
	}
	var ip string
	if err := db.QueryRowContext(ctx, "SELECT client_ip FROM user_best_score WHERE user_id = 'u_1'").Scan(&ip); err != nil || ip != "203.0.113.9" {
		t.Errorf("client_ip = %q (%v)", ip, err)
	}
	if ids, _ := storage.PopDirty(ctx, 10); len(ids) != 0 {
		t.Errorf("dirty set should be empty after sync, got %v", ids)
	}
	syncer.syncRound(ctx)
	var cnt int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_best_score").Scan(&cnt); err != nil || cnt != 1 {
		t.Errorf("row count = %d (%v), want 1", cnt, err)
	}
}

func TestRebuildFromDB(t *testing.T) {
	syncer, storage, db := newTestSyncer(t)
	ctx := context.Background()
	s := vo.Score{Stage: 7, StageCultivation: "70", TotalCultivation: vo.Cultivation("7" + strings.Repeat("0", 30))}
	execUpsert(t, db, "u_r", "恢复者", s, 1700000000)

	count, err := syncer.RebuildFromDB(ctx)
	if err != nil || count != 1 {
		t.Fatalf("RebuildFromDB = %d, %v; want 1, nil", count, err)
	}
	ub, ok, err := storage.GetUserBest(ctx, "u_r")
	if err != nil || !ok || ub.Score != s || ub.AchievedAt != 1700000000 || ub.Province != "浙江省" {
		t.Errorf("rebuilt meta = (%+v, %v, %v)", ub, ok, err)
	}
}

func TestDisabledSyncerIsSafe(t *testing.T) {
	util.InitLogger()
	syncer, err := New(&config.Config{MySQLDSN: "", RankSyncInterval: time.Second}, nil)
	if err != nil {
		t.Fatalf("New disabled syncer: %v", err)
	}
	if syncer.Enabled() {
		t.Error("syncer should be disabled without DSN")
	}
	syncer.Start() // 只打日志,不 panic
	syncer.Stop()  // 未启动时安全返回
	if _, err := syncer.RebuildFromDB(context.Background()); err == nil {
		t.Error("RebuildFromDB without DSN should error")
	}
}

func TestEnsureDatabase(t *testing.T) {
	util.InitLogger()
	bad, err := New(&config.Config{MySQLDSN: "u:p@tcp(127.0.0.1:1)/bad-name", RankSyncInterval: time.Second}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := bad.EnsureDatabase(context.Background()); err == nil || !strings.Contains(err.Error(), "letters, digits") {
		t.Errorf("invalid database name err = %v", err)
	}
	disabled, _ := New(&config.Config{RankSyncInterval: time.Second}, nil)
	if err := disabled.EnsureDatabase(context.Background()); err != nil {
		t.Errorf("disabled syncer should be a no-op: %v", err)
	}

	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set; skipping live part")
	}
	live, _ := New(&config.Config{MySQLDSN: dsn, RankSyncInterval: time.Second}, nil)
	defer live.Close()
	if err := live.EnsureDatabase(context.Background()); err != nil {
		t.Errorf("EnsureDatabase on existing database: %v", err)
	}
}
