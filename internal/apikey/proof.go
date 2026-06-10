package apikey

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ProofIssuer 签发和验证探测成功后的测试证明。
// proof 是 HMAC-SHA256 签名令牌，绑定测试参数和过期时间。
type ProofIssuer struct {
	secret []byte
	ttl    time.Duration
}

// NewProofIssuer 创建 ProofIssuer。
func NewProofIssuer(secret string, ttl time.Duration) *ProofIssuer {
	return &ProofIssuer{
		secret: []byte(secret),
		ttl:    ttl,
	}
}

// proofPayload 构建待签名的 payload。
// 绑定探测参数：jobID|testType|apiURL|apiKeyFingerprint|expiresAt
func proofPayload(jobID, testType, apiURL, apiKeyFingerprint string, expiresAt int64) string {
	return fmt.Sprintf("%s|%s|%s|%s|%d",
		jobID, testType, apiURL, apiKeyFingerprint, expiresAt)
}

// Issue 签发测试证明。返回格式：signature.expiresAt
func (pi *ProofIssuer) Issue(jobID, testType, apiURL, apiKeyFingerprint string) string {
	proof, _ := pi.IssueWithExpiry(jobID, testType, apiURL, apiKeyFingerprint)
	return proof
}

// IssueWithExpiry 签发测试证明，并额外返回该 proof 的绝对过期时间（Unix 秒）。
// 过期时间即编码进 proof 字符串尾部的同一值，供前端做权威倒计时/提交前校验，
// 避免前端硬编码 TTL 与后端 proof_ttl 漂移。
func (pi *ProofIssuer) IssueWithExpiry(jobID, testType, apiURL, apiKeyFingerprint string) (string, int64) {
	expiresAt := time.Now().Add(pi.ttl).Unix()
	payload := proofPayload(jobID, testType, apiURL, apiKeyFingerprint, expiresAt)

	mac := hmac.New(sha256.New, pi.secret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf("%s.%d", sig, expiresAt), expiresAt
}

// Verify 验证测试证明的签名和有效期。
func (pi *ProofIssuer) Verify(proof, jobID, testType, apiURL, apiKeyFingerprint string) error {
	parts := strings.SplitN(proof, ".", 2)
	if len(parts) != 2 {
		return fmt.Errorf("proof 格式无效")
	}

	sig := parts[0]
	expiresAtStr := parts[1]

	expiresAt, err := strconv.ParseInt(expiresAtStr, 10, 64)
	if err != nil {
		return fmt.Errorf("proof 过期时间无效")
	}

	if time.Now().Unix() > expiresAt {
		return fmt.Errorf("proof 已过期")
	}

	payload := proofPayload(jobID, testType, apiURL, apiKeyFingerprint, expiresAt)
	mac := hmac.New(sha256.New, pi.secret)
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return fmt.Errorf("proof 签名不匹配")
	}

	return nil
}
