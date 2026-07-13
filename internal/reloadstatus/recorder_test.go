package reloadstatus

import (
	"errors"
	"sync"
	"testing"
)

// 全新 Recorder 未记录过任何跳过：Snapshot 的 ok 必须为 false，字段全零。
func TestSnapshotEmptyBeforeAnySkip(t *testing.T) {
	r := New()

	got, ok := r.Snapshot()
	if ok {
		t.Fatalf("ok = true, want false（尚未发生跳过）")
	}
	if got.SkipCount != 0 || got.LastSkipError != "" || !got.LastSkipAt.IsZero() {
		t.Fatalf("空 Recorder 快照非零：%+v", got)
	}
}

// 记录一次跳过后：ok=true、计数=1、错误文本取自 err、时间戳非零。
func TestRecordSkipPopulatesSnapshot(t *testing.T) {
	r := New()

	r.RecordSkip(errors.New("缺 model_id"))

	got, ok := r.Snapshot()
	if !ok {
		t.Fatalf("ok = false, want true（已发生一次跳过）")
	}
	if got.SkipCount != 1 {
		t.Fatalf("SkipCount = %d, want 1", got.SkipCount)
	}
	if got.LastSkipError != "缺 model_id" {
		t.Fatalf("LastSkipError = %q, want %q", got.LastSkipError, "缺 model_id")
	}
	if got.LastSkipAt.IsZero() {
		t.Fatalf("LastSkipAt 为零值，应被打戳")
	}
}

// 多次跳过累计计数，错误文本与时间戳取最近一次。
func TestRecordSkipAccumulatesCount(t *testing.T) {
	r := New()

	r.RecordSkip(errors.New("first"))
	got1, _ := r.Snapshot()
	r.RecordSkip(errors.New("second"))

	got2, ok := r.Snapshot()
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if got2.SkipCount != 2 {
		t.Fatalf("SkipCount = %d, want 2", got2.SkipCount)
	}
	if got2.LastSkipError != "second" {
		t.Fatalf("LastSkipError = %q, want %q（应为最近一次）", got2.LastSkipError, "second")
	}
	if got2.LastSkipAt.Before(got1.LastSkipAt) {
		t.Fatalf("LastSkipAt 未随新跳过前进：%v < %v", got2.LastSkipAt, got1.LastSkipAt)
	}
}

// nil error 仍计数，错误文本为空串（不 panic）。
func TestRecordSkipNilError(t *testing.T) {
	r := New()

	r.RecordSkip(nil)

	got, ok := r.Snapshot()
	if !ok || got.SkipCount != 1 {
		t.Fatalf("nil error 应仍计数：ok=%v count=%d", ok, got.SkipCount)
	}
	if got.LastSkipError != "" {
		t.Fatalf("LastSkipError = %q, want 空串", got.LastSkipError)
	}
}

// 并发读写不得触发 data race（go test -race 下验证）。
func TestRecorderConcurrentAccess(t *testing.T) {
	r := New()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); r.RecordSkip(errors.New("boom")) }()
		go func() { defer wg.Done(); _, _ = r.Snapshot() }()
	}
	wg.Wait()

	got, _ := r.Snapshot()
	if got.SkipCount != 50 {
		t.Fatalf("SkipCount = %d, want 50", got.SkipCount)
	}
}
