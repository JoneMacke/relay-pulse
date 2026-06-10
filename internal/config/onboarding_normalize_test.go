package config

import (
	"testing"
	"time"
)

// change_requests 与 onboarding 共享 admin_token / encryption_key / proof_secret / proof_ttl。
// 只开 change_requests（onboarding 关闭）时，这组共享密钥仍须默认化与校验——
// 否则 ProofTTLDuration 留零值会导致 proof 即刻过期、变更提交全被拒（finding C 回归）。
func TestNormalizeOnboardingConfig_ChangeRequestsOnly(t *testing.T) {
	cfg := &AppConfig{}
	cfg.ChangeRequests.Enabled = true
	cfg.Onboarding.AdminToken = "admin-tok"
	cfg.Onboarding.EncryptionKey = "enc-key"
	cfg.Onboarding.ProofSecret = "proof-secret"

	if err := cfg.normalizeOnboardingConfig(); err != nil {
		t.Fatalf("normalizeOnboardingConfig: %v", err)
	}
	if cfg.Onboarding.ProofTTLDuration != 5*time.Minute {
		t.Errorf("ProofTTLDuration = %v, want default 5m", cfg.Onboarding.ProofTTLDuration)
	}
}

// 共享密钥缺失时必须报错，即便驱动方是 change_requests 而非 onboarding。
func TestNormalizeOnboardingConfig_ChangeRequestsOnlyRequiresProofSecret(t *testing.T) {
	cfg := &AppConfig{}
	cfg.ChangeRequests.Enabled = true
	cfg.Onboarding.AdminToken = "admin-tok"
	cfg.Onboarding.EncryptionKey = "enc-key"
	// 故意不设 ProofSecret（无环境变量覆盖路径）

	if err := cfg.normalizeOnboardingConfig(); err == nil {
		t.Error("expected error when proof_secret missing with change_requests enabled")
	}
}

// 两者都关闭时为 no-op，不应填充 ProofTTLDuration。
func TestNormalizeOnboardingConfig_BothDisabledNoop(t *testing.T) {
	cfg := &AppConfig{}
	if err := cfg.normalizeOnboardingConfig(); err != nil {
		t.Fatalf("normalizeOnboardingConfig: %v", err)
	}
	if cfg.Onboarding.ProofTTLDuration != 0 {
		t.Errorf("ProofTTLDuration = %v, want 0 (untouched)", cfg.Onboarding.ProofTTLDuration)
	}
}
