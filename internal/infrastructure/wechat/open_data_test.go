package wechat

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"fc_server/internal/domain/person/provider"
)

var (
	testSessionKey = []byte("0123456789abcdef")
	testIV         = []byte("fedcba9876543210")
)

// encryptOpenData 按微信开放数据的方式加密(AES-128-CBC + PKCS#7),用于构造测试输入。
func encryptOpenData(t *testing.T, key, iv []byte, plain string) string {
	t.Helper()
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	data := append([]byte(plain), bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
	return base64.StdEncoding.EncodeToString(out)
}

func friendClient(t *testing.T, openID string) *Client {
	t.Helper()
	return newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("js_code") != testCode {
			_, _ = w.Write([]byte(`{"errcode":40029,"errmsg":"invalid code"}`))
			return
		}
		fmt.Fprintf(w, `{"openid":%q,"session_key":%q}`, openID, base64.StdEncoding.EncodeToString(testSessionKey))
	}, time.Second)
}

func TestRelationFriendsDecrypts(t *testing.T) {
	c := friendClient(t, "o-me")
	plain := `{"list":[{"openid":"o-f1"},{"openid":"o-f2"},{"openid":""}],"watermark":{"appid":"` + testAppID + `","timestamp":1793000000}}`
	enc := encryptOpenData(t, testSessionKey, testIV, plain)

	got, err := c.RelationFriends(context.Background(), testCode, enc, base64.StdEncoding.EncodeToString(testIV))
	if err != nil {
		t.Fatalf("RelationFriends: %v", err)
	}
	if got.OpenID != "o-me" || len(got.FriendOpenIDs) != 2 || got.FriendOpenIDs[0] != "o-f1" || got.FriendOpenIDs[1] != "o-f2" {
		t.Errorf("result = %+v", got)
	}
}

func TestRelationFriendsWithoutWatermarkIsAccepted(t *testing.T) {
	c := friendClient(t, "o-me")
	enc := encryptOpenData(t, testSessionKey, testIV, `{"list":[]}`)
	got, err := c.RelationFriends(context.Background(), testCode, enc, base64.StdEncoding.EncodeToString(testIV))
	if err != nil || len(got.FriendOpenIDs) != 0 {
		t.Fatalf("got (%+v, %v)", got, err)
	}
}

func TestRelationFriendsRejectsBadData(t *testing.T) {
	c := friendClient(t, "o-me")
	iv := base64.StdEncoding.EncodeToString(testIV)
	otherKey := []byte("ffffffffffffffff")
	tests := []struct {
		name string
		enc  string
		iv   string
	}{
		{"other app watermark", encryptOpenData(t, testSessionKey, testIV, `{"list":[],"watermark":{"appid":"wx_other"}}`), iv},
		{"encrypted with another session key", encryptOpenData(t, otherKey, testIV, `{"list":[{"openid":"x"}]}`), iv},
		{"not base64", "%%%", iv},
		{"not block aligned", base64.StdEncoding.EncodeToString([]byte("short")), iv},
		{"bad iv", encryptOpenData(t, testSessionKey, testIV, `{"list":[]}`), base64.StdEncoding.EncodeToString([]byte("tiny"))},
		{"plaintext not json", encryptOpenData(t, testSessionKey, testIV, `not json`), iv},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.RelationFriends(context.Background(), testCode, tt.enc, tt.iv)
			if !errors.Is(err, provider.ErrInvalidEncryptedData) {
				t.Fatalf("err = %v, want ErrInvalidEncryptedData", err)
			}
			if strings.Contains(err.Error(), base64.StdEncoding.EncodeToString(testSessionKey)) {
				t.Errorf("error leaks session key: %v", err)
			}
		})
	}
}

func TestRelationFriendsInvalidCode(t *testing.T) {
	c := friendClient(t, "o-me")
	_, err := c.RelationFriends(context.Background(), "stale-code", "x", "y")
	if !errors.Is(err, provider.ErrInvalidCode) {
		t.Errorf("err = %v, want ErrInvalidCode", err)
	}
}
