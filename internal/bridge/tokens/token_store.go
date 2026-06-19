package tokens

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const bootstrapTokenFileName = "bootstrap-token.json"

var ErrBootstrapTokenUnauthorized = errors.New("bootstrap token is invalid or expired")

type BootstrapTokenRecord struct {
	TokenHash string    `json:"tokenHash"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type BootstrapTokenState struct {
	Configured bool
	Expired    bool
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

func DefaultBootstrapTokenPath() string {
	if dir := os.Getenv("MINIK8S_STATE_DIR"); dir != "" {
		return filepath.Join(dir, bootstrapTokenFileName)
	}
	return filepath.Join(string(os.PathSeparator), "opt", "minik8s", "state", bootstrapTokenFileName)
}

func SetBootstrapToken(path, token string, ttl time.Duration, now time.Time) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("token is required")
	}
	if ttl <= 0 {
		return fmt.Errorf("ttl must be greater than zero")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	record := BootstrapTokenRecord{
		TokenHash: hashToken(token),
		CreatedAt: now.UTC(),
		ExpiresAt: now.UTC().Add(ttl),
	}
	return writeBootstrapTokenRecord(path, record)
}

func ClearBootstrapToken(path string) error {
	if path == "" {
		path = DefaultBootstrapTokenPath()
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clearing bootstrap token: %w", err)
	}
	return nil
}

func BootstrapTokenStatus(path string, now time.Time) (BootstrapTokenState, error) {
	record, err := readBootstrapTokenRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		return BootstrapTokenState{}, nil
	}
	if err != nil {
		return BootstrapTokenState{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return BootstrapTokenState{
		Configured: true,
		Expired:    !record.ExpiresAt.IsZero() && !now.UTC().Before(record.ExpiresAt),
		CreatedAt:  record.CreatedAt,
		ExpiresAt:  record.ExpiresAt,
	}, nil
}

func ValidateBootstrapToken(path, token string, now time.Time) error {
	if strings.TrimSpace(token) == "" {
		return ErrBootstrapTokenUnauthorized
	}
	record, err := readBootstrapTokenRecord(path)
	if err != nil {
		return ErrBootstrapTokenUnauthorized
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !record.ExpiresAt.IsZero() && !now.UTC().Before(record.ExpiresAt) {
		return ErrBootstrapTokenUnauthorized
	}
	if !hmac.Equal([]byte(record.TokenHash), []byte(hashToken(token))) {
		return ErrBootstrapTokenUnauthorized
	}
	return nil
}

func readBootstrapTokenRecord(path string) (BootstrapTokenRecord, error) {
	if path == "" {
		path = DefaultBootstrapTokenPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return BootstrapTokenRecord{}, err
	}
	var record BootstrapTokenRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return BootstrapTokenRecord{}, fmt.Errorf("parsing bootstrap token: %w", err)
	}
	if record.TokenHash == "" {
		return BootstrapTokenRecord{}, fmt.Errorf("bootstrap token hash is missing")
	}
	return record, nil
}

func writeBootstrapTokenRecord(path string, record BootstrapTokenRecord) error {
	if path == "" {
		path = DefaultBootstrapTokenPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating bootstrap token dir: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding bootstrap token: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing bootstrap token: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replacing bootstrap token: %w", err)
	}
	return nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
