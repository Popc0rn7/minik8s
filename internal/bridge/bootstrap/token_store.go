package bootstrap

import (
	"time"

	"minik8s/internal/bridge/tokens"
)

var ErrBootstrapTokenUnauthorized = tokens.ErrBootstrapTokenUnauthorized

type BootstrapTokenRecord = tokens.BootstrapTokenRecord
type BootstrapTokenState = tokens.BootstrapTokenState

func DefaultBootstrapTokenPath() string {
	return tokens.DefaultBootstrapTokenPath()
}

func SetBootstrapToken(path, token string, ttl time.Duration, now time.Time) error {
	return tokens.SetBootstrapToken(path, token, ttl, now)
}

func ClearBootstrapToken(path string) error {
	return tokens.ClearBootstrapToken(path)
}

func BootstrapTokenStatus(path string, now time.Time) (BootstrapTokenState, error) {
	return tokens.BootstrapTokenStatus(path, now)
}

func ValidateBootstrapToken(path, token string, now time.Time) error {
	return tokens.ValidateBootstrapToken(path, token, now)
}
