package sync

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"fc_server/internal/domain/record/entity"
	"fc_server/internal/infrastructure/repository_impl"
	"fc_server/internal/util"
)

// 集成测试:仅在 TEST_MYSQL_DSN 设置时运行;Redis 部分使用本地 DB 15。
func newTestGameRecordSyncer(t *testing.T) (*GameRecordSyncer, *repository_impl.GameRecordRedisQueue, *sql.DB) {
	t.Helper()
	_, rankStorage, db := newTestSyncer(t)
	queue := repository_impl.NewGameRecordRedisQueue(rankStorage.Client(), 1000)
	gs := NewGameRecordSyncer(db, queue, time.Second)
	ctx := context.Background()
	if err := gs.ensureSchema(ctx); err != nil {
		t.Fatalf("ensure game record schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM player_game_record"); err != nil {
		t.Fatalf("clean player_game_record: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DELETE FROM player_game_record") })
	return gs, queue, db
}

func gameRec(id, user, data string, at int64) *entity.GameRecord {
	return &entity.GameRecord{ID: fmt.Sprintf("%032s", id), UserID: user, Data: []byte(data), CreatedAtMs: at, ClientIP: "2001:db8::1"}
}

func countRecords(t *testing.T, db *sql.DB, userID string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM player_game_record WHERE user_id = ?", userID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestGameRecordSyncAppendsAndDrainsQueue(t *testing.T) {
	gs, queue, db := newTestGameRecordSyncer(t)
	ctx := context.Background()
	for i, r := range []*entity.GameRecord{
		gameRec("1", "wx_a", `{"stage":1,"note":"胜利🎴"}`, 1000),
		gameRec("2", "wx_a", `{"stage":2}`, 2000),
		gameRec("3", "wx_b", `{"stage":1}`, 3000),
	} {
		if err := queue.Append(ctx, r); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	gs.syncRound(ctx)

	if a, b := countRecords(t, db, "wx_a"), countRecords(t, db, "wx_b"); a != 2 || b != 1 {
		t.Errorf("rows a=%d b=%d, want 2/1", a, b)
	}
	var note string
	if err := db.QueryRowContext(ctx,
		"SELECT JSON_UNQUOTE(JSON_EXTRACT(data, '$.note')) FROM player_game_record WHERE record_id = ?",
		fmt.Sprintf("%032s", "1")).Scan(&note); err != nil || note != "胜利🎴" {
		t.Errorf("stored json note = %q (%v)", note, err)
	}
	if n, _ := queue.Len(ctx); n != 0 {
		t.Errorf("queue len after sync = %d, want 0", n)
	}
	var ip string
	if err := db.QueryRowContext(ctx, "SELECT client_ip FROM player_game_record LIMIT 1").Scan(&ip); err != nil || ip != "2001:db8::1" {
		t.Errorf("client_ip = %q (%v)", ip, err)
	}
}

func TestGameRecordReplayIsDeduplicated(t *testing.T) {
	gs, queue, db := newTestGameRecordSyncer(t)
	ctx := context.Background()
	r := gameRec("9", "wx_a", `{"stage":9}`, 1000)
	if _, err := gs.insertBatch(ctx, []*entity.GameRecord{r}); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	// 模拟"已提交但未删除队头"后重放同一记录
	if err := queue.Append(ctx, r); err != nil {
		t.Fatalf("append: %v", err)
	}
	gs.syncRound(ctx)
	if n := countRecords(t, db, "wx_a"); n != 1 {
		t.Errorf("rows after replay = %d, want 1", n)
	}
}

func TestGameRecordPoisonRecordIsDropped(t *testing.T) {
	gs, queue, db := newTestGameRecordSyncer(t)
	ctx := context.Background()
	tooDeep := strings.Repeat(`{"a":`, 120) + `1` + strings.Repeat(`}`, 120)
	_ = queue.Append(ctx, gameRec("1", "wx_a", tooDeep, 1000))
	_ = queue.Append(ctx, gameRec("2", "wx_a", `{"ok":true}`, 2000))

	gs.syncRound(ctx)

	if n := countRecords(t, db, "wx_a"); n != 1 {
		t.Errorf("rows = %d, want only the valid record", n)
	}
	if n, _ := queue.Len(ctx); n != 0 {
		t.Errorf("poison record must not block the queue, len = %d", n)
	}
}

func TestGameRecordSyncSkipsWhenLockHeld(t *testing.T) {
	gs, queue, db := newTestGameRecordSyncer(t)
	ctx := context.Background()
	_ = queue.Append(ctx, gameRec("1", "wx_a", `{}`, 1000))
	if ok, _ := queue.AcquireFlushLock(ctx, "another-instance", time.Minute); !ok {
		t.Fatal("seed lock")
	}
	gs.syncRound(ctx)
	if n := countRecords(t, db, "wx_a"); n != 0 {
		t.Errorf("rows = %d, want 0 while another instance holds the lock", n)
	}
	if n, _ := queue.Len(ctx); n != 1 {
		t.Errorf("queue len = %d, want 1", n)
	}
}

func TestDisabledGameRecordSyncerIsSafe(t *testing.T) {
	util.InitLogger()
	gs := NewGameRecordSyncer(nil, nil, time.Second)
	if gs.Enabled() {
		t.Error("syncer without db should be disabled")
	}
	gs.Start()
	gs.Stop()
}
