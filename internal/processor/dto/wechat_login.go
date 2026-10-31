package dto

import (
	"errors"
	"fmt"
	"strings"
)

const maxLoginCodeBytes = 1024

type WechatLoginRequest struct {
	Code string `json:"code"`
}

func (r *WechatLoginRequest) Validate() error {
	if r == nil {
		return errors.New("request body is required")
	}
	if r.Code == "" {
		return errors.New("code is required")
	}
	if len(r.Code) > maxLoginCodeBytes {
		return fmt.Errorf("code exceeds %d bytes", maxLoginCodeBytes)
	}
	if strings.ContainsFunc(r.Code, func(c rune) bool { return c <= ' ' || c == 0x7f }) {
		return errors.New("code contains illegal characters")
	}
	return nil
}

type WechatLoginResponse struct {
	Token     string   `json:"token"`
	ExpiresAt int64    `json:"expires_at"`
	User      *UserDto `json:"user"`
}
