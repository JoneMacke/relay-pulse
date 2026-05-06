// Package identity 管理运行时用户标识，与配置加载/校验无关。
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// UserIDManager 按通道维度管理 user_id，支持确定性生成和定期刷新
type UserIDManager struct {
	mu      sync.Mutex
	entries map[string]*userIDEntry
}

type userIDEntry struct {
	id        string
	hash      string
	expiresAt time.Time
}

// NewUserIDManager 创建 UserIDManager
func NewUserIDManager() *UserIDManager {
	return &UserIDManager{
		entries: make(map[string]*userIDEntry),
	}
}

// GetUserIDPair 原子获取 user_id 和 hash（确保同一次调用中两者一致）
func (m *UserIDManager) GetUserIDPair(provider, service, channel string, refreshMinutes int) (string, string) {
	return m.resolve(provider, service, channel, refreshMinutes)
}

func (m *UserIDManager) resolve(provider, service, channel string, refreshMinutes int) (string, string) {
	channelKey := strings.TrimSpace(fmt.Sprintf("%s/%s/%s", provider, service, channel))
	cacheKey := fmt.Sprintf("%s|%d", channelKey, refreshMinutes)

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	if entry, ok := m.entries[cacheKey]; ok {
		if refreshMinutes <= 0 || now.Before(entry.expiresAt) {
			return entry.id, entry.hash
		}
	}

	// 生成哈希
	var hash string
	if refreshMinutes <= 0 {
		hash = sha256Hex(channelKey)
	} else {
		period := time.Duration(refreshMinutes) * time.Minute
		periodStart := now.Truncate(period)
		hash = sha256Hex(fmt.Sprintf("%s|%d", channelKey, periodStart.Unix()))
	}

	id := formatUserID(hash)

	var expiresAt time.Time
	if refreshMinutes > 0 {
		period := time.Duration(refreshMinutes) * time.Minute
		expiresAt = now.Truncate(period).Add(period)
	}

	m.entries[cacheKey] = &userIDEntry{
		id:        id,
		hash:      hash,
		expiresAt: expiresAt,
	}

	return id, hash
}

func sha256Hex(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

// formatUserID 生成与生产一致的 user_id 格式
// 格式: user_<sha256_64hex>_account__session_<uuid_format>
func formatUserID(hash string) string {
	uuid := uuidFromHash(hash)
	return fmt.Sprintf("user_%s_account__session_%s", hash, uuid)
}

// uuidFromHash 从哈希中提取 UUID 格式字符串
func uuidFromHash(hash string) string {
	if len(hash) < 32 {
		hash = sha256Hex(hash)
	}
	s := hash[:32]
	return fmt.Sprintf("%s-%s-%s-%s-%s", s[0:8], s[8:12], s[12:16], s[16:20], s[20:32])
}

// DeriveUUID 用 hash + salt 派生一个稳定的 UUID，不同 salt 产生不同结果。
func DeriveUUID(hash, salt string) string {
	salted := sha256Hex(hash + salt)
	return uuidFromHash(salted)
}
