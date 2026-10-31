package sync

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"fc_server/internal/domain/person/entity"
	"fc_server/internal/domain/person/entity/vo"
	"fc_server/internal/infrastructure/repository_impl"
	"fc_server/internal/util"
)

// 集成测试:仅在 TEST_MYSQL_DSN 设置时运行;Redis 部分使用本地 DB 15。
func newTestPlayerSyncer(t *testing.T) (*PlayerSyncer, *repository_impl.PlayerRedisStorage, *sql.DB) {
	t.Helper()
	_, rankStorage, db := newTestSyncer(t)
	storage := repository_impl.NewPlayerRedisStorage(rankStorage.Client())
	ps := NewPlayerSyncer(db, storage, time.Second)
	ctx := context.Background()
	if err := ps.ensureSchema(ctx); err != nil {
		t.Fatalf("ensure player schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM player_profile"); err != nil {
		t.Fatalf("clean player_profile: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DELETE FROM player_profile") })
	return ps, storage, db
}

func readNickRev(t *testing.T, db *sql.DB, userID string) (string, int64) {
	t.Helper()
	var nick string
	var rev int64
	if err := db.QueryRowContext(context.Background(),
		"SELECT nickname, revision FROM player_profile WHERE user_id = ?", userID).Scan(&nick, &rev); err != nil {
		t.Fatalf("read player_profile %s: %v", userID, err)
	}
	return nick, rev
}

func TestPlayerSyncRoundBacksUpAndAcks(t *testing.T) {
	ps, storage, db := newTestPlayerSyncer(t)
	ctx := context.Background()
	uid := entity.PlayerID("wx_app", "o-1")
	if _, err := storage.UpsertOnLogin(ctx, uid, "wx_app", "o-1", 100); err != nil {
		t.Fatalf("seed login: %v", err)
	}
	if _, err := storage.UpdateProfile(ctx, uid, vo.Profile{Nickname: "阿甘🎴", AvatarURL: "https://a.com/x"}, 110); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	ps.syncRound(ctx)

	if nick, rev := readNickRev(t, db, uid); nick != "阿甘🎴" || rev != 2 {
		t.Errorf("backup = %s/%d, want 阿甘🎴/2", nick, rev)
	}
	if entries, _, _ := storage.ScanDirty(ctx, 0, 10); len(entries) != 0 {
		t.Errorf("dirty after sync = %+v, want empty", entries)
	}
}

func TestPlayerSyncFailureKeepsDirty(t *testing.T) {
	ps, storage, db := newTestPlayerSyncer(t)
	ctx := context.Background()
	uid := entity.PlayerID("wx_app", "o-2")
	if _, err := storage.UpsertOnLogin(ctx, uid, "wx_app", "o-2", 100); err != nil {
		t.Fatalf("seed login: %v", err)
	}

	broken, err := sql.Open("mysql", "nobody:wrong@tcp(127.0.0.1:1)/none")
	if err != nil {
		t.Fatalf("open broken db: %v", err)
	}
	defer broken.Close()
	failing := NewPlayerSyncer(broken, storage, time.Second)
	failing.schemaReady = true
	failing.syncRound(ctx)
	if entries, _, _ := storage.ScanDirty(ctx, 0, 10); len(entries) != 1 {
		t.Fatalf("dirty after failed sync = %+v, want retained", entries)
	}

	ps.syncRound(ctx)
	if _, rev := readNickRev(t, db, uid); rev != 1 {
		t.Errorf("retry backup revision = %d, want 1", rev)
	}
}

func TestPlayerUpsertRejectsOlderRevision(t *testing.T) {
	_, _, db := newTestPlayerSyncer(t)
	ctx := context.Background()
	exec := func(nick string, rev int64) {
		t.Helper()
		if _, err := db.ExecContext(ctx, upsertPlayerSQL, "wx_x", "wx_app", "o-x", nick, "", 1, 1, 1, rev); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	exec("新", 5)
	exec("旧", 3)
	if nick, rev := readNickRev(t, db, "wx_x"); nick != "新" || rev != 5 {
		t.Errorf("after stale upsert = %s/%d, want 新/5", nick, rev)
	}
	exec("更新", 6)
	if nick, rev := readNickRev(t, db, "wx_x"); nick != "更新" || rev != 6 {
		t.Errorf("after newer upsert = %s/%d, want 更新/6", nick, rev)
	}
}

func TestPlayerRebuildFromDBKeepsRevision(t *testing.T) {
	ps, storage, db := newTestPlayerSyncer(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, upsertPlayerSQL, "wx_r", "wx_app", "o-r", "备份", "https://a.com/r", 10, 20, 15, 7); err != nil {
		t.Fatalf("seed mysql: %v", err)
	}
	n, err := ps.RebuildFromDB(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RebuildFromDB = %d, %v; want 1", n, err)
	}
	p, err := storage.Get(ctx, "wx_r")
	if err != nil || p.Revision != 7 || p.Profile.Nickname != "备份" || p.OpenID != "o-r" {
		t.Fatalf("restored = (%+v, %v)", p, err)
	}
	if entries, _, _ := storage.ScanDirty(ctx, 0, 10); len(entries) != 0 {
		t.Errorf("rebuild must not mark dirty: %+v", entries)
	}
}

func TestDisabledPlayerSyncerIsSafe(t *testing.T) {
	util.InitLogger()
	ps := NewPlayerSyncer(nil, nil, time.Second)
	if ps.Enabled() {
		t.Error("syncer without db should be disabled")
	}
	ps.Start()
	ps.Stop()
	if _, err := ps.RebuildFromDB(context.Background()); err == nil {
		t.Error("RebuildFromDB without db should error")
	}
}
