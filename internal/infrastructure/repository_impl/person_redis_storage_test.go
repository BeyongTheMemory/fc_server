package repository_impl

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"fc_server/internal/domain/person/entity"
	"fc_server/internal/domain/person/entity/vo"
	"fc_server/internal/domain/person/repository"
)

const (
	testApp    = "wx_app"
	testOpenID = "openid-a"
)

func newTestPlayerStorage(t *testing.T) *PlayerRedisStorage {
	t.Helper()
	return NewPlayerRedisStorage(newTestStorage(t).Client())
}

func TestUpsertOnLoginCreatesThenRefreshes(t *testing.T) {
	s := newTestPlayerStorage(t)
	ctx := context.Background()
	uid := entity.PlayerID(testApp, testOpenID)

	p, err := s.UpsertOnLogin(ctx, uid, testApp, testOpenID, 100)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if p.ID != uid || p.AppID != testApp || p.OpenID != testOpenID || p.Profile != (vo.Profile{}) ||
		p.CreatedTime != 100 || p.LastLoginTime != 100 || p.Revision != 1 {
		t.Errorf("created player = %+v", p)
	}
	if rev := s.client.HGet(ctx, keyPlayerDirty, uid).Val(); rev != "1" {
		t.Errorf("dirty revision = %q, want 1", rev)
	}

	if _, err := s.UpdateProfile(ctx, uid, vo.Profile{Nickname: "阿甘", AvatarURL: "https://a.com/x"}, 150); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	p, err = s.UpsertOnLogin(ctx, uid, testApp, testOpenID, 200)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if p.Profile.Nickname != "阿甘" || p.Profile.AvatarURL != "https://a.com/x" {
		t.Errorf("login overwrote profile: %+v", p.Profile)
	}
	if p.CreatedTime != 100 || p.LastLoginTime != 200 || p.UpdatedTime != 150 || p.Revision != 3 {
		t.Errorf("refreshed player = %+v", p)
	}
	if ttl := s.client.TTL(ctx, playerKey(uid)).Val(); ttl > 0 {
		t.Errorf("player key must not expire, TTL = %v", ttl)
	}
}

func TestUpsertOnLoginIdentityMismatch(t *testing.T) {
	s := newTestPlayerStorage(t)
	ctx := context.Background()
	if _, err := s.UpsertOnLogin(ctx, "wx_same", testApp, "openid-1", 1); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.UpsertOnLogin(ctx, "wx_same", testApp, "openid-2", 2); !errors.Is(err, repository.ErrIdentityMismatch) {
		t.Errorf("err = %v, want ErrIdentityMismatch", err)
	}
}

func TestConcurrentFirstLoginCreatesOnePlayer(t *testing.T) {
	s := newTestPlayerStorage(t)
	ctx := context.Background()
	uid := entity.PlayerID(testApp, testOpenID)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.UpsertOnLogin(ctx, uid, testApp, testOpenID, int64(1000+i)); err != nil {
				t.Errorf("upsert %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	p, err := s.Get(ctx, uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Revision != 20 {
		t.Errorf("revision = %d, want 20 (every login counted once)", p.Revision)
	}
	if keys := s.client.Keys(ctx, "player:wx_*").Val(); len(keys) != 1 {
		t.Errorf("player keys = %v, want exactly one", keys)
	}
}

func TestUpdateProfileRevisionAndDirty(t *testing.T) {
	s := newTestPlayerStorage(t)
	ctx := context.Background()
	uid := entity.PlayerID(testApp, testOpenID)
	if _, err := s.UpsertOnLogin(ctx, uid, testApp, testOpenID, 1); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.client.HDel(ctx, keyPlayerDirty, uid).Err(); err != nil {
		t.Fatalf("clear dirty: %v", err)
	}

	profile := vo.Profile{Nickname: "阿甘🎴", AvatarURL: "https://thirdwx.qlogo.cn/a"}
	p, err := s.UpdateProfile(ctx, uid, profile, 50)
	if err != nil || p.Profile != profile || p.Revision != 2 || p.UpdatedTime != 50 {
		t.Fatalf("UpdateProfile = (%+v, %v)", p, err)
	}
	if rev := s.client.HGet(ctx, keyPlayerDirty, uid).Val(); rev != "2" {
		t.Errorf("dirty revision = %q, want 2", rev)
	}

	// 相同资料:不递增、不标脏
	if err := s.client.HDel(ctx, keyPlayerDirty, uid).Err(); err != nil {
		t.Fatalf("clear dirty: %v", err)
	}
	p, err = s.UpdateProfile(ctx, uid, profile, 60)
	if err != nil || p.Revision != 2 || p.UpdatedTime != 50 {
		t.Errorf("identical update = (%+v, %v), want unchanged", p, err)
	}
	if s.client.HExists(ctx, keyPlayerDirty, uid).Val() {
		t.Error("identical update must not mark dirty")
	}

	if _, err := s.UpdateProfile(ctx, "wx_missing", profile, 1); !errors.Is(err, repository.ErrPlayerNotFound) {
		t.Errorf("missing player err = %v, want ErrPlayerNotFound", err)
	}
	if _, err := s.Get(ctx, "wx_missing"); !errors.Is(err, repository.ErrPlayerNotFound) {
		t.Errorf("Get missing err = %v, want ErrPlayerNotFound", err)
	}
}

func TestAckDirtyKeepsNewerRevision(t *testing.T) {
	s := newTestPlayerStorage(t)
	ctx := context.Background()
	uid := entity.PlayerID(testApp, testOpenID)
	if _, err := s.UpsertOnLogin(ctx, uid, testApp, testOpenID, 1); err != nil {
		t.Fatalf("seed: %v", err)
	}

	entries, next, err := s.ScanDirty(ctx, 0, 100)
	if err != nil || next != 0 || len(entries) != 1 || entries[0].UserID != uid || entries[0].Revision != 1 {
		t.Fatalf("ScanDirty = (%+v, %d, %v)", entries, next, err)
	}

	// 备份 revision 1 期间玩家又改了资料 → revision 2
	if _, err := s.UpdateProfile(ctx, uid, vo.Profile{Nickname: "新"}, 2); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if err := s.AckDirty(ctx, uid, 1); err != nil {
		t.Fatalf("AckDirty stale: %v", err)
	}
	if rev := s.client.HGet(ctx, keyPlayerDirty, uid).Val(); rev != "2" {
		t.Errorf("stale ack removed newer pending revision, dirty = %q", rev)
	}
	if err := s.AckDirty(ctx, uid, 2); err != nil {
		t.Fatalf("AckDirty current: %v", err)
	}
	if s.client.HExists(ctx, keyPlayerDirty, uid).Val() {
		t.Error("ack of current revision should clear dirty mark")
	}
}

func TestRestorePlayerRespectsRevision(t *testing.T) {
	s := newTestPlayerStorage(t)
	ctx := context.Background()
	backup := &entity.Person{
		ID: "wx_restored", AppID: testApp, OpenID: "o-r",
		Profile:     vo.Profile{Nickname: "备份", AvatarURL: "https://a.com/b"},
		CreatedTime: 10, LastLoginTime: 20, UpdatedTime: 15, Revision: 5,
	}
	ok, err := s.Restore(ctx, backup)
	if err != nil || !ok {
		t.Fatalf("Restore = (%v, %v)", ok, err)
	}
	p, err := s.Get(ctx, "wx_restored")
	if err != nil || p.Profile != backup.Profile || p.Revision != 5 || p.CreatedTime != 10 {
		t.Fatalf("restored = (%+v, %v)", p, err)
	}
	if s.client.HExists(ctx, keyPlayerDirty, "wx_restored").Val() {
		t.Error("restore must not mark dirty")
	}

	stale := *backup
	stale.Revision = 4
	stale.Profile.Nickname = "旧"
	if ok, err := s.Restore(ctx, &stale); err != nil || ok {
		t.Errorf("older restore = (%v, %v), want skipped", ok, err)
	}
	if p, _ := s.Get(ctx, "wx_restored"); p.Profile.Nickname != "备份" {
		t.Errorf("older backup overwrote newer data: %+v", p.Profile)
	}
}

func TestSessionStorageRoundtripAndTTL(t *testing.T) {
	client := newTestStorage(t).Client()
	s := NewSessionRedisStorage(client)
	ctx := context.Background()
	hash := entity.HashToken("tok")
	in := &entity.Session{UserID: "wx_u", AppID: testApp, IssuedAt: 100, ExpiresAt: 200}

	if err := s.Create(ctx, hash, in, time.Hour); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ttl := client.TTL(ctx, sessionKey(hash)).Val(); ttl <= 0 || ttl > time.Hour {
		t.Errorf("session TTL = %v, want (0, 1h]", ttl)
	}
	if keys := client.Keys(ctx, "auth:session:tok").Val(); len(keys) != 0 {
		t.Error("plaintext token must not appear in keys")
	}
	got, err := s.Get(ctx, hash)
	if err != nil || *got != *in {
		t.Fatalf("Get = (%+v, %v), want %+v", got, err, in)
	}
	if err := s.Delete(ctx, hash); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, hash); !errors.Is(err, repository.ErrSessionNotFound) {
		t.Errorf("Get after delete err = %v, want ErrSessionNotFound", err)
	}
}

func TestRateLimiterWindow(t *testing.T) {
	client := newTestStorage(t).Client()
	l := NewRateLimiterRedis(client)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		ok, err := l.Allow(ctx, "login:1.2.3.4", 3, time.Minute)
		if err != nil || !ok {
			t.Fatalf("attempt %d = (%v, %v), want allowed", i, ok, err)
		}
	}
	if ok, err := l.Allow(ctx, "login:1.2.3.4", 3, time.Minute); err != nil || ok {
		t.Errorf("4th attempt = (%v, %v), want denied", ok, err)
	}
	if ttl := client.PTTL(ctx, rateLimitKey("login:1.2.3.4")).Val(); ttl <= 0 || ttl > time.Minute {
		t.Errorf("limit key TTL = %v, want (0, 1m]", ttl)
	}
	if ok, _ := l.Allow(ctx, "login:5.6.7.8", 3, time.Minute); !ok {
		t.Error("other key must have its own budget")
	}
}

func TestGetManyPlayers(t *testing.T) {
	s := newTestPlayerStorage(t)
	ctx := context.Background()
	if _, err := s.UpsertOnLogin(ctx, "wx_a", testApp, "o-a", 1); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if _, err := s.UpsertOnLogin(ctx, "wx_b", testApp, "o-b", 1); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	if _, err := s.UpdateProfile(ctx, "wx_b", vo.Profile{Nickname: "小B", AvatarURL: "https://a.com/b"}, 2); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	got, err := s.GetMany(ctx, []string{"wx_a", "wx_b", "u_legacy"})
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if len(got) != 2 || got["wx_a"] == nil || got["wx_b"].Profile.Nickname != "小B" || got["wx_b"].Profile.AvatarURL != "https://a.com/b" {
		t.Errorf("GetMany = %+v", got)
	}
	if _, ok := got["u_legacy"]; ok {
		t.Error("missing player must be omitted")
	}
	if empty, err := s.GetMany(ctx, nil); err != nil || len(empty) != 0 {
		t.Errorf("GetMany(nil) = (%v, %v)", empty, err)
	}
}

func TestFriendStorageReplaceAndList(t *testing.T) {
	client := newTestStorage(t).Client()
	f := NewFriendRedisStorage(client)
	ctx := context.Background()

	if ids, synced, err := f.List(ctx, "wx_me"); err != nil || synced || ids != nil {
		t.Fatalf("never synced = (%v, %v, %v)", ids, synced, err)
	}
	if err := f.Replace(ctx, "wx_me", []string{"wx_a", "wx_b"}, time.Hour); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	ids, synced, err := f.List(ctx, "wx_me")
	if err != nil || !synced || len(ids) != 2 {
		t.Fatalf("List = (%v, %v, %v)", ids, synced, err)
	}
	if ttl := client.TTL(ctx, friendsKey("wx_me")).Val(); ttl <= 0 || ttl > time.Hour {
		t.Errorf("TTL = %v", ttl)
	}
	// 整体替换;好友为空也算已同步
	if err := f.Replace(ctx, "wx_me", nil, time.Hour); err != nil {
		t.Fatalf("Replace empty: %v", err)
	}
	if ids, synced, _ := f.List(ctx, "wx_me"); !synced || len(ids) != 0 {
		t.Errorf("after empty replace = (%v, %v), want synced with no friends", ids, synced)
	}
}
