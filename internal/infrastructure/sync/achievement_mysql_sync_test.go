package sync

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"fc_server/internal/domain/achievement/entity"
	"fc_server/internal/infrastructure/repository_impl"
	"fc_server/internal/util"
)

// 集成测试:仅在 TEST_MYSQL_DSN 设置时运行;Redis 部分使用本地 DB 15。
func newTestAchievementSyncer(t *testing.T) (*AchievementSyncer, *repository_impl.AchievementRedisStorage, *sql.DB) {
	t.Helper()
	_, rankStorage, db := newTestSyncer(t)
	storage := repository_impl.NewAchievementRedisStorage(rankStorage.Client(), 1000)
	as := NewAchievementSyncer(db, storage, time.Second)
	ctx := context.Background()
	if err := as.ensureSchema(ctx); err != nil {
		t.Fatalf("ensure achievement schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM player_achievement"); err != nil {
		t.Fatalf("clean player_achievement: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DELETE FROM player_achievement") })
	return as, storage, db
}

func TestAchievementSyncWritesFirstUnlocks(t *testing.T) {
	as, storage, db := newTestAchievementSyncer(t)
	ctx := context.Background()
	for _, a := range []*entity.Achievement{
		{UserID: "wx_a", AchievementID: "first_ascension", UnlockedAtMs: 1000, ReceivedAtMs: 1100, Detail: []byte(`{"stage":19,"note":"飞升🎴"}`), ClientIP: "203.0.113.9"},
		{UserID: "wx_a", AchievementID: "first_ascension", UnlockedAtMs: 9000, ReceivedAtMs: 9100}, // 重复,不入队
		{UserID: "wx_a", AchievementID: "daluo_10", UnlockedAtMs: 2000, ReceivedAtMs: 2100},
		{UserID: "wx_b", AchievementID: "first_ascension", UnlockedAtMs: 3000, ReceivedAtMs: 3100},
	} {
		if _, _, err := storage.Unlock(ctx, a); err != nil {
			t.Fatalf("unlock: %v", err)
		}
	}

	as.syncRound(ctx)

	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM player_achievement").Scan(&n); err != nil || n != 3 {
		t.Fatalf("rows = %d (%v), want 3", n, err)
	}
	var at int64
	var note, ip string
	if err := db.QueryRowContext(ctx, `SELECT unlocked_at_ms, JSON_UNQUOTE(JSON_EXTRACT(detail, '$.note')), client_ip
		FROM player_achievement WHERE user_id = 'wx_a' AND achievement_id = 'first_ascension'`).Scan(&at, &note, &ip); err != nil {
		t.Fatalf("read: %v", err)
	}
	if at != 1000 || note != "飞升🎴" || ip != "203.0.113.9" {
		t.Errorf("first_ascension = %d / %q / %q", at, note, ip)
	}
	var detailIsNull bool
	if err := db.QueryRowContext(ctx, `SELECT detail IS NULL FROM player_achievement
		WHERE user_id = 'wx_a' AND achievement_id = 'daluo_10'`).Scan(&detailIsNull); err != nil || !detailIsNull {
		t.Errorf("missing detail should be NULL (%v)", err)
	}
	if left, _ := storage.Len(ctx); left != 0 {
		t.Errorf("queue len = %d, want 0", left)
	}
}

func TestAchievementReplayKeepsFirstUnlock(t *testing.T) {
	as, _, db := newTestAchievementSyncer(t)
	ctx := context.Background()
	first := &entity.Achievement{UserID: "wx_a", AchievementID: "x", UnlockedAtMs: 1000, ReceivedAtMs: 1000}
	if _, err := as.insertBatch(ctx, []*entity.Achievement{first}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	replay := &entity.Achievement{UserID: "wx_a", AchievementID: "x", UnlockedAtMs: 5000, ReceivedAtMs: 5000}
	if _, err := as.insertBatch(ctx, []*entity.Achievement{replay}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	var at int64
	if err := db.QueryRowContext(ctx, "SELECT unlocked_at_ms FROM player_achievement WHERE user_id='wx_a' AND achievement_id='x'").Scan(&at); err != nil || at != 1000 {
		t.Errorf("unlocked_at_ms = %d (%v), want first unlock 1000", at, err)
	}
}

func TestDisabledAchievementSyncerIsSafe(t *testing.T) {
	util.InitLogger()
	as := NewAchievementSyncer(nil, nil, time.Second)
	if as.Enabled() {
		t.Error("syncer without db should be disabled")
	}
	as.Start()
	as.Stop()
}
