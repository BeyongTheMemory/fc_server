package wechat

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"

	"fc_server/internal/domain/person/provider"
)

// 开放数据解密(微信"开放数据校验与解密"):AES-128-CBC,密钥为 base64 解码后的 session_key,
// 初始向量为 base64 解码后的 iv,PKCS#7 填充;明文 JSON 中的 watermark.appid 必须是本小游戏。

type relationFriendData struct {
	List []struct {
		OpenID string `json:"openid"`
	} `json:"list"`
	Watermark *struct {
		AppID string `json:"appid"`
	} `json:"watermark"`
}

func (c *Client) RelationFriends(ctx context.Context, code, encryptedData, iv string) (*provider.RelationFriends, error) {
	session, err := c.code2Session(ctx, code)
	if err != nil {
		return nil, err
	}
	plain, err := decryptOpenData(session.SessionKey, encryptedData, iv)
	if err != nil {
		return nil, &provider.UpstreamError{Kind: provider.ErrInvalidEncryptedData, Detail: err.Error()}
	}
	var data relationFriendData
	if err := json.Unmarshal(plain, &data); err != nil {
		return nil, &provider.UpstreamError{Kind: provider.ErrInvalidEncryptedData, Detail: "decrypted data is not json"}
	}
	if data.Watermark != nil && data.Watermark.AppID != c.appID {
		return nil, &provider.UpstreamError{Kind: provider.ErrInvalidEncryptedData, Detail: "watermark appid mismatch"}
	}
	friends := make([]string, 0, len(data.List))
	for _, f := range data.List {
		if f.OpenID != "" {
			friends = append(friends, f.OpenID)
		}
	}
	return &provider.RelationFriends{OpenID: session.OpenID, FriendOpenIDs: friends}, nil
}

// decryptOpenData 返回的错误不包含密钥或密文内容。
func decryptOpenData(sessionKey, encryptedData, iv string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(sessionKey)
	if err != nil || len(key) != 16 {
		return nil, errors.New("invalid session key")
	}
	ivBytes, err := base64.StdEncoding.DecodeString(iv)
	if err != nil || len(ivBytes) != aes.BlockSize {
		return nil, errors.New("invalid iv")
	}
	data, err := base64.StdEncoding.DecodeString(encryptedData)
	if err != nil || len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, errors.New("invalid encrypted data")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("invalid session key")
	}
	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, ivBytes).CryptBlocks(plain, data)

	// 微信示例实现的 PKCS#7 填充长度可达 32 字节
	pad := int(plain[len(plain)-1])
	if pad < 1 || pad > 32 || pad > len(plain) {
		return nil, errors.New("bad padding")
	}
	for _, b := range plain[len(plain)-pad:] {
		if int(b) != pad {
			return nil, errors.New("bad padding")
		}
	}
	return plain[:len(plain)-pad], nil
}
