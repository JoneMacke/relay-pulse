package apikey

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProofIssuer_IssueAndVerify(t *testing.T) {
	pi := NewProofIssuer("test-secret", 5*time.Minute)

	proof := pi.Issue("job-123", "cc", "https://api.example.com/v1", "fingerprint-abc")
	if proof == "" {
		t.Fatal("expected non-empty proof")
	}

	// Verify with matching params
	err := pi.Verify(proof, "job-123", "cc", "https://api.example.com/v1", "fingerprint-abc")
	if err != nil {
		t.Fatalf("expected valid proof, got error: %v", err)
	}
}

// IssueWithExpiry 返回的过期时间必须等于编码进 proof 尾部的同一值，
// 且落在 (now, now+ttl] 区间——前端据此做权威倒计时。
func TestProofIssuer_IssueWithExpiry(t *testing.T) {
	ttl := 5 * time.Minute
	pi := NewProofIssuer("test-secret", ttl)

	before := time.Now().Unix()
	proof, expiresAt := pi.IssueWithExpiry("job-1", "cc", "https://api.example.com", "fp")
	after := time.Now().Unix()

	// 与 token 尾部编码值一致
	parts := strings.SplitN(proof, ".", 2)
	if len(parts) != 2 || parts[1] != strconv.FormatInt(expiresAt, 10) {
		t.Fatalf("expiresAt %d not encoded in proof %q", expiresAt, proof)
	}
	// 落在合理区间
	if expiresAt < before+int64(ttl.Seconds()) || expiresAt > after+int64(ttl.Seconds()) {
		t.Errorf("expiresAt %d outside expected window [%d, %d]",
			expiresAt, before+int64(ttl.Seconds()), after+int64(ttl.Seconds()))
	}
	// Issue 与 IssueWithExpiry 行为一致（仍可验证）
	if err := pi.Verify(proof, "job-1", "cc", "https://api.example.com", "fp"); err != nil {
		t.Errorf("proof from IssueWithExpiry failed verify: %v", err)
	}
}

func TestProofIssuer_WrongJobID(t *testing.T) {
	pi := NewProofIssuer("test-secret", 5*time.Minute)
	proof := pi.Issue("job-123", "cc", "https://api.example.com", "fp")

	err := pi.Verify(proof, "job-wrong", "cc", "https://api.example.com", "fp")
	if err == nil {
		t.Error("expected error for wrong jobID")
	}
}

func TestProofIssuer_WrongTestType(t *testing.T) {
	pi := NewProofIssuer("test-secret", 5*time.Minute)
	proof := pi.Issue("job-1", "cc", "https://api.example.com", "fp")

	err := pi.Verify(proof, "job-1", "cx", "https://api.example.com", "fp")
	if err == nil {
		t.Error("expected error for wrong testType")
	}
}

func TestProofIssuer_WrongFingerprint(t *testing.T) {
	pi := NewProofIssuer("test-secret", 5*time.Minute)
	proof := pi.Issue("job-1", "cc", "https://api.example.com", "fp-a")

	err := pi.Verify(proof, "job-1", "cc", "https://api.example.com", "fp-b")
	if err == nil {
		t.Error("expected error for wrong fingerprint")
	}
}

func TestProofIssuer_Expired(t *testing.T) {
	// Use negative TTL to ensure proof is immediately expired
	pi := NewProofIssuer("test-secret", -1*time.Second)

	proof := pi.Issue("job-1", "cc", "https://api.example.com", "fp")

	err := pi.Verify(proof, "job-1", "cc", "https://api.example.com", "fp")
	if err == nil {
		t.Fatal("expected error for expired proof")
	}
	if !strings.Contains(err.Error(), "过期") {
		t.Errorf("error should mention expiration, got: %v", err)
	}
}

func TestProofIssuer_InvalidFormat(t *testing.T) {
	pi := NewProofIssuer("test-secret", 5*time.Minute)

	err := pi.Verify("no-dot-separator", "j", "t", "u", "f")
	if err == nil {
		t.Error("expected error for invalid format")
	}

	err = pi.Verify("sig.not-a-number", "j", "t", "u", "f")
	if err == nil {
		t.Error("expected error for invalid expiry")
	}
}

func TestProofIssuer_DifferentSecrets(t *testing.T) {
	pi1 := NewProofIssuer("secret-a", 5*time.Minute)
	pi2 := NewProofIssuer("secret-b", 5*time.Minute)

	proof := pi1.Issue("job-1", "cc", "https://api.example.com", "fp")
	err := pi2.Verify(proof, "job-1", "cc", "https://api.example.com", "fp")
	if err == nil {
		t.Error("expected error when verifying with different secret")
	}
}
