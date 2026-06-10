package change

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"monitor/internal/apikey"
	"monitor/internal/config"
)

// --- mock store ---

type mockStore struct {
	mu      sync.Mutex
	records map[string]*ChangeRequest
	nextID  int64
}

func newMockStore() *mockStore {
	return &mockStore{records: make(map[string]*ChangeRequest), nextID: 1}
}

func (m *mockStore) Save(_ context.Context, r *ChangeRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.records[r.PublicID]; exists {
		return fmt.Errorf("duplicate public_id")
	}
	r.ID = m.nextID
	m.nextID++
	cp := *r
	m.records[r.PublicID] = &cp
	return nil
}

func (m *mockStore) GetByPublicID(_ context.Context, publicID string) (*ChangeRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[publicID]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (m *mockStore) List(_ context.Context, status string, limit, offset int) ([]*ChangeRequest, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []*ChangeRequest
	for _, r := range m.records {
		if status != "" && status != "all" && string(r.Status) != status {
			continue
		}
		cp := *r
		all = append(all, &cp)
	}
	// 按 created_at DESC 排序，与真实实现一致
	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt > all[j].CreatedAt
	})
	total := len(all)
	if offset >= len(all) {
		return nil, total, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end], total, nil
}

func (m *mockStore) Update(_ context.Context, r *ChangeRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range m.records {
		if v.ID == r.ID {
			cp := *r
			m.records[k] = &cp
			return nil
		}
	}
	return fmt.Errorf("not found")
}

func (m *mockStore) CountByIPToday(_ context.Context, ipHash string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	start, end := todayRange()
	count := 0
	for _, r := range m.records {
		if r.SubmitterIPHash == ipHash && r.CreatedAt >= start && r.CreatedAt < end {
			count++
		}
	}
	return count, nil
}

func (m *mockStore) DeleteByPublicID(_ context.Context, publicID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.records[publicID]; !ok {
		return fmt.Errorf("变更请求不存在")
	}
	delete(m.records, publicID)
	return nil
}

// --- test helpers ---

const testHexKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const testProofSecret = "test-proof-secret-for-unit-tests"

func newTestService(t *testing.T) (*Service, *mockStore) {
	t.Helper()
	cipher, err := apikey.NewKeyCipher(testHexKey)
	if err != nil {
		t.Fatalf("NewKeyCipher: %v", err)
	}
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	store := newMockStore()
	cfg := &config.ChangeRequestConfig{
		Enabled:        true,
		MaxPerIPPerDay: 5,
	}
	svc := NewService(store, cipher, proofIssuer, cfg)

	// 构建索引
	monitors := []config.ServiceConfig{
		{
			Provider: "testprov", Service: "cc", Channel: "vip",
			APIKey:       "sk-test-key-12345",
			ProviderName: "TestProvider", ChannelName: "VIP Channel",
			Category: "commercial", BaseURL: "https://api.test.com",
		},
		{
			Provider: "other", Service: "cc", Channel: "free",
			APIKey:  "sk-other-key-6789",
			BaseURL: "https://api.other.com",
		},
	}
	svc.UpdateConfig(cfg, monitors)

	return svc, store
}

// --- Auth tests ---

func TestService_Auth_Success(t *testing.T) {
	svc, _ := newTestService(t)

	resp, err := svc.Auth("sk-test-key-12345")
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if len(resp.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(resp.Candidates))
	}
	c := resp.Candidates[0]
	if c.Provider != "testprov" || c.Service != "cc" || c.Channel != "vip" {
		t.Errorf("unexpected candidate: %+v", c)
	}
	if c.MonitorKey != "testprov--cc--vip" {
		t.Errorf("MonitorKey: got %q", c.MonitorKey)
	}
	if c.ProviderName != "TestProvider" {
		t.Errorf("ProviderName: got %q", c.ProviderName)
	}
}

func TestService_Auth_NoMatch(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.Auth("sk-nonexistent-key")
	if err == nil {
		t.Error("expected error for non-matching key")
	}
}

// --- Submit tests ---

func TestService_Submit_NoTestRequired(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"provider_name": "NewName"},
		Locale:          "en-US",
	}

	resp, err := svc.Submit(ctx, req, "127.0.0.1")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if resp.PublicID == "" {
		t.Error("expected non-empty PublicID")
	}
}

func TestService_Submit_WithTestRequired(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// 需要一个有效的 proof
	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	proof := proofIssuer.Issue("job-1", "cc", "https://api.new.com", fingerprint)

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"base_url": "https://api.new.com"},
		TestProof:       proof,
		TestJobID:       "job-1",
		TestType:        "cc",
		TestAPIURL:      "https://api.new.com",
		TestLatency:     150,
		TestHTTPCode:    200,
	}

	resp, err := svc.Submit(ctx, req, "127.0.0.1")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if resp.PublicID == "" {
		t.Error("expected non-empty PublicID")
	}
}

func TestService_Submit_DisallowedField(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"url": "https://evil.com"},
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error for disallowed field")
	}
}

// category / sponsor_level 须人工对接，不在用户自助白名单内——
// 持有 API Key 的用户绕过 UI 直接提交也必须被拒（安全边界回归）。
func TestService_Submit_RejectsAdminOnlyFields(t *testing.T) {
	for _, field := range []string{"category", "sponsor_level"} {
		t.Run(field, func(t *testing.T) {
			svc, _ := newTestService(t)
			req := &SubmitRequest{
				APIKey:          "sk-test-key-12345",
				TargetKey:       "testprov--cc--vip",
				ProposedChanges: map[string]string{field: "x"},
			}
			if _, err := svc.Submit(context.Background(), req, "127.0.0.1"); err == nil {
				t.Errorf("expected %q to be rejected for self-service change", field)
			}
		})
	}
}

func TestService_Submit_IPRateLimit(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// 提交到限额
	for i := 0; i < 5; i++ {
		req := &SubmitRequest{
			APIKey:          "sk-test-key-12345",
			TargetKey:       "testprov--cc--vip",
			ProposedChanges: map[string]string{"provider_name": fmt.Sprintf("Name%d", i)},
		}
		if _, err := svc.Submit(ctx, req, "10.0.0.1"); err != nil {
			t.Fatalf("Submit #%d: %v", i, err)
		}
	}

	// 第 6 次应被限流
	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"provider_name": "Overflow"},
	}
	_, err := svc.Submit(ctx, req, "10.0.0.1")
	if err == nil {
		t.Error("expected rate limit error")
	}
}

func TestService_Submit_KeyMismatch(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// 使用 other 的 key 但目标指向 testprov
	req := &SubmitRequest{
		APIKey:          "sk-other-key-6789",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"provider_name": "Hack"},
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error for key/target mismatch")
	}
}

func TestService_Submit_MissingProofWhenTestRequired(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"base_url": "https://api.new.com"},
		// 没有 proof
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error when test required but no proof provided")
	}
}

func TestService_Submit_NewAPIKeyRequiresTest(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"provider_name": "NewName"},
		NewAPIKey:       "sk-brand-new-key-abcd",
		// 没有 proof，但 new key 需要测试
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error when new API key provided but no proof")
	}
}

func TestService_Submit_EncryptsNewAPIKey(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	newKey := "sk-brand-new-key-abcd"
	fingerprint := cipher.Fingerprint(newKey)
	proof := proofIssuer.Issue("job-2", "cc", "https://api.test.com", fingerprint)

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"provider_name": "Updated"},
		NewAPIKey:       newKey,
		TestProof:       proof,
		TestJobID:       "job-2",
		TestType:        "cc",
		TestAPIURL:      "https://api.test.com",
	}

	resp, err := svc.Submit(ctx, req, "192.168.0.1")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// 验证存储中有加密字段
	cr, _ := store.GetByPublicID(ctx, resp.PublicID)
	if cr.NewKeyEncrypted == "" {
		t.Error("expected encrypted new key")
	}
	if cr.NewKeyFingerprint == "" {
		t.Error("expected new key fingerprint")
	}
	if cr.NewKeyLast4 != "abcd" {
		t.Errorf("NewKeyLast4: got %q, want %q", cr.NewKeyLast4, "abcd")
	}
}

// --- AdminList tests ---

func TestService_AdminList_DefaultLimits(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	// 种子 25 条记录
	for i := 0; i < 25; i++ {
		cr := makeRequest(fmt.Sprintf("pub-list-%03d", i))
		cr.CreatedAt = int64(1000 + i)
		if err := store.Save(ctx, cr); err != nil {
			t.Fatalf("Save #%d: %v", i, err)
		}
	}

	tests := []struct {
		name      string
		limit     int
		offset    int
		wantCount int // 期望返回的记录数
		wantTotal int
	}{
		{"normal", 20, 0, 20, 25},
		{"zero limit clamps to 20", 0, 0, 20, 25},
		{"negative limit clamps to 20", -1, 0, 20, 25},
		{"over 100 clamps to 20", 200, 0, 20, 25},
		{"negative offset clamps to 0", 10, -1, 10, 25},
		{"offset past end", 10, 30, 0, 25},
		{"small page", 5, 20, 5, 25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, total, err := svc.AdminList(ctx, "", tt.limit, tt.offset)
			if err != nil {
				t.Fatalf("AdminList: %v", err)
			}
			if total != tt.wantTotal {
				t.Errorf("total: got %d, want %d", total, tt.wantTotal)
			}
			if len(results) != tt.wantCount {
				t.Errorf("count: got %d, want %d", len(results), tt.wantCount)
			}
		})
	}
}

// --- AdminGetDetail tests ---

func TestService_AdminGetDetail_WithNewKey(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	encrypted, _ := cipher.Encrypt("sk-secret-new-key")

	cr := makeRequest("pub-detail")
	cr.NewKeyEncrypted = encrypted
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, newKey, err := svc.AdminGetDetail(ctx, "pub-detail")
	if err != nil {
		t.Fatalf("AdminGetDetail: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if newKey != "sk-secret-new-key" {
		t.Errorf("decrypted new key: got %q, want %q", newKey, "sk-secret-new-key")
	}
}

func TestService_AdminGetDetail_NoNewKey(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-detail-none")
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, newKey, err := svc.AdminGetDetail(ctx, "pub-detail-none")
	if err != nil {
		t.Fatalf("AdminGetDetail: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if newKey != "" {
		t.Errorf("expected empty new key, got %q", newKey)
	}
}

func TestService_AdminGetDetail_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	got, _, err := svc.AdminGetDetail(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("AdminGetDetail: %v", err)
	}
	if got != nil {
		t.Error("expected nil for non-existent")
	}
}

// --- AdminApprove tests ---

func TestService_AdminApprove_Success(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-approve")
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := svc.AdminApprove(ctx, "pub-approve", "looks good"); err != nil {
		t.Fatalf("AdminApprove: %v", err)
	}

	got, _ := store.GetByPublicID(ctx, "pub-approve")
	if got.Status != StatusApproved {
		t.Errorf("Status: got %q, want %q", got.Status, StatusApproved)
	}
	if got.AdminNote != "looks good" {
		t.Errorf("AdminNote: got %q, want %q", got.AdminNote, "looks good")
	}
	if got.ReviewedAt == nil {
		t.Error("ReviewedAt should be set")
	}
}

func TestService_AdminApprove_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	err := svc.AdminApprove(ctx, "nonexistent", "")
	if err == nil {
		t.Error("expected error for non-existent")
	}
}

func TestService_AdminApprove_WrongStatus(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-approve-rej")
	cr.Status = StatusRejected
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	err := svc.AdminApprove(ctx, "pub-approve-rej", "")
	if err == nil {
		t.Error("expected error for non-pending status")
	}
}

// --- AdminReject tests ---

func TestService_AdminReject_Success(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-reject")
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := svc.AdminReject(ctx, "pub-reject", "not acceptable"); err != nil {
		t.Fatalf("AdminReject: %v", err)
	}

	got, _ := store.GetByPublicID(ctx, "pub-reject")
	if got.Status != StatusRejected {
		t.Errorf("Status: got %q, want %q", got.Status, StatusRejected)
	}
}

func TestService_AdminReject_AppliedCannotReject(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-reject-app")
	cr.Status = StatusApplied
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	err := svc.AdminReject(ctx, "pub-reject-app", "too late")
	if err == nil {
		t.Error("expected error for already-applied status")
	}
}

// --- AdminDelete tests ---

func TestService_AdminDelete_Success(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-delete")
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := svc.AdminDelete(ctx, "pub-delete"); err != nil {
		t.Fatalf("AdminDelete: %v", err)
	}

	got, _ := store.GetByPublicID(ctx, "pub-delete")
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestService_AdminDelete_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	err := svc.AdminDelete(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for non-existent")
	}
}

// --- IssueProof test ---

func TestService_IssueProof(t *testing.T) {
	svc, _ := newTestService(t)

	proof := svc.IssueProof("job-1", "cc", "https://api.test.com", "sk-test-key-12345")
	if proof == "" {
		t.Error("expected non-empty proof")
	}

	// Proof 应包含签名和过期时间
	if len(proof) < 10 {
		t.Errorf("proof too short: %q", proof)
	}
}

// --- Negative proof tests ---

func TestService_Submit_ProofWrongJobID(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	// proof 签发时使用 job-good，但提交时用 job-wrong
	proof := proofIssuer.Issue("job-good", "cc", "https://api.new.com", fingerprint)

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"base_url": "https://api.new.com"},
		TestProof:       proof,
		TestJobID:       "job-wrong",
		TestType:        "cc",
		TestAPIURL:      "https://api.new.com",
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error for wrong jobID")
	}
	if !strings.Contains(err.Error(), "签名不匹配") && !strings.Contains(err.Error(), "无效") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestService_Submit_ProofWrongAPIURL(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	proof := proofIssuer.Issue("job-url", "cc", "https://api.legit.com", fingerprint)

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"base_url": "https://api.evil.com"},
		TestProof:       proof,
		TestJobID:       "job-url",
		TestType:        "cc",
		TestAPIURL:      "https://api.evil.com", // 与 proof 中的不同
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error for wrong apiURL in proof")
	}
}

func TestService_Submit_ProofExpired(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	// 负 TTL 确保签发即过期（expiresAt 在过去）
	expiredIssuer := apikey.NewProofIssuer(testProofSecret, -time.Second)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	proof := expiredIssuer.Issue("job-exp", "cc", "https://api.new.com", fingerprint)

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"base_url": "https://api.new.com"},
		TestProof:       proof,
		TestJobID:       "job-exp",
		TestType:        "cc",
		TestAPIURL:      "https://api.new.com",
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Fatal("expected error for expired proof")
	}
	if !strings.Contains(err.Error(), "过期") {
		t.Errorf("expected expiry error, got: %v", err)
	}
}

func TestService_Submit_ProofTampered(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	proof := proofIssuer.Issue("job-tamp", "cc", "https://api.new.com", fingerprint)

	// 篡改签名部分（翻转第一个字符）
	tampered := "x" + proof[1:]

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"base_url": "https://api.new.com"},
		TestProof:       tampered,
		TestJobID:       "job-tamp",
		TestType:        "cc",
		TestAPIURL:      "https://api.new.com",
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error for tampered proof signature")
	}
}

func TestService_Submit_ProofWrongKeyFingerprint(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	// proof 签发时使用另一个 key 的指纹
	wrongFingerprint := cipher.Fingerprint("sk-different-key-xyz")
	proof := proofIssuer.Issue("job-fp", "cc", "https://api.new.com", wrongFingerprint)

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"base_url": "https://api.new.com"},
		TestProof:       proof,
		TestJobID:       "job-fp",
		TestType:        "cc",
		TestAPIURL:      "https://api.new.com",
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error for proof with wrong key fingerprint")
	}
}

// --- GetStatus test ---

func TestService_GetStatus(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	cr := makeRequest("pub-status")
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := svc.GetStatus(ctx, "pub-status")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.PublicID != "pub-status" {
		t.Errorf("PublicID: got %q, want %q", got.PublicID, "pub-status")
	}
}

func TestService_GetStatus_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	got, err := svc.GetStatus(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if got != nil {
		t.Error("expected nil for non-existent")
	}
}

// --- Ghost api_key field tests ---

func TestService_Submit_DisallowGhostAPIKeyField(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"api_key": "sk-some-key"},
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error: api_key should not be allowed in proposed_changes")
	}
	if !strings.Contains(err.Error(), "不允许自助变更") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- TestAPIURL host consistency tests ---

func TestService_Submit_BaseURLHostMustMatchTestAPIURL(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// proof 绑定 api.other.com，但 base_url 指向 api.new.com，host 不一致
	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	fingerprint := cipher.Fingerprint("sk-test-key-12345")
	proof := proofIssuer.Issue("job-host", "cc", "https://api.other.com", fingerprint)

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"base_url": "https://api.new.com"},
		TestProof:       proof,
		TestJobID:       "job-host",
		TestType:        "cc",
		TestAPIURL:      "https://api.other.com",
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error: test_api_url host does not match new base_url")
	}
	if !strings.Contains(err.Error(), "host 必须一致") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestService_Submit_NewAPIKeyHostMustMatchTargetBaseURL(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// target.BaseURL = "https://api.test.com"，TestAPIURL 指向不同 host
	cipher, _ := apikey.NewKeyCipher(testHexKey)
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	newKey := "sk-brand-new-key-efgh"
	fingerprint := cipher.Fingerprint(newKey)
	proof := proofIssuer.Issue("job-newkey-host", "cc", "https://api.evil.com", fingerprint)

	req := &SubmitRequest{
		APIKey:          "sk-test-key-12345",
		TargetKey:       "testprov--cc--vip",
		ProposedChanges: map[string]string{"provider_name": "Updated"},
		NewAPIKey:       newKey,
		TestProof:       proof,
		TestJobID:       "job-newkey-host",
		TestType:        "cc",
		TestAPIURL:      "https://api.evil.com",
	}

	_, err := svc.Submit(ctx, req, "127.0.0.1")
	if err == nil {
		t.Error("expected error: test_api_url host does not match target base_url")
	}
	if !strings.Contains(err.Error(), "host 必须一致") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestService_Submit_SkipHostCheckWhenBaseURLEmpty(t *testing.T) {
	// 构造 target.BaseURL 为空的 monitor，host 校验应跳过
	cipher, err := apikey.NewKeyCipher(testHexKey)
	if err != nil {
		t.Fatalf("NewKeyCipher: %v", err)
	}
	proofIssuer := apikey.NewProofIssuer(testProofSecret, 5*time.Minute)
	store := newMockStore()
	cfg := &config.ChangeRequestConfig{
		Enabled:        true,
		MaxPerIPPerDay: 5,
	}
	svc := NewService(store, cipher, proofIssuer, cfg)

	monitors := []config.ServiceConfig{
		{
			Provider: "nbase", Service: "cc", Channel: "ch",
			APIKey:  "sk-nobase-key-00001",
			BaseURL: "", // 无 base_url
		},
	}
	svc.UpdateConfig(cfg, monitors)

	newKey := "sk-brand-new-key-0001"
	fingerprint := cipher.Fingerprint(newKey)
	proof := proofIssuer.Issue("job-nobase", "cc", "https://any.host.com/v1", fingerprint)

	req := &SubmitRequest{
		APIKey:          "sk-nobase-key-00001",
		TargetKey:       "nbase--cc--ch",
		ProposedChanges: map[string]string{"provider_name": "NoBase"},
		NewAPIKey:       newKey,
		TestProof:       proof,
		TestJobID:       "job-nobase",
		TestType:        "cc",
		TestAPIURL:      "https://any.host.com/v1",
	}

	ctx := context.Background()
	resp, err := svc.Submit(ctx, req, "127.0.0.1")
	if err != nil {
		t.Fatalf("expected success when target BaseURL is empty, got: %v", err)
	}
	if resp.PublicID == "" {
		t.Error("expected non-empty PublicID")
	}
}
