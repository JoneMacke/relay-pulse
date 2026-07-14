package config

import (
	"strings"
	"testing"
)

func findAnn(items []Annotation, id string) *Annotation {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

func TestResolveAnnotations_QualityHardFail_WithModels(t *testing.T) {
	task := ServiceConfig{BoardReason: "quality_hardfail", BoardReasonModels: "opus-4-8, sonnet"}
	got := ResolveAnnotations(task, nil, 0)
	a := findAnn(got, "quality_hardfail")
	if a == nil {
		t.Fatal("expected quality_hardfail annotation")
	}
	if a.Family != AnnotationFamilyNegative {
		t.Errorf("family = %q, want %q", a.Family, AnnotationFamilyNegative)
	}
	if a.Label != "质量移板" {
		t.Errorf("label = %q, want 质量移板", a.Label)
	}
	if !strings.Contains(a.Tooltip, "opus-4-8, sonnet") {
		t.Errorf("tooltip missing models: %q", a.Tooltip)
	}
	if !strings.Contains(a.Tooltip, "近3次评测均未取得可评分响应，已暂移备用板") {
		t.Errorf("tooltip body wrong: %q", a.Tooltip)
	}
}

func TestResolveAnnotations_QualityHardFail_NoModels(t *testing.T) {
	task := ServiceConfig{BoardReason: "quality_hardfail", BoardReasonModels: ""}
	a := findAnn(ResolveAnnotations(task, nil, 0), "quality_hardfail")
	if a == nil {
		t.Fatal("expected quality_hardfail annotation")
	}
	if a.Tooltip != "近3次评测均未取得可评分响应，已暂移备用板" {
		t.Errorf("no-models tooltip = %q", a.Tooltip)
	}
}

func TestResolveAnnotations_NoBoardReason_NoQualityAnnotation(t *testing.T) {
	task := ServiceConfig{BoardReason: ""}
	if a := findAnn(ResolveAnnotations(task, nil, 0), "quality_hardfail"); a != nil {
		t.Errorf("unexpected quality_hardfail annotation: %+v", a)
	}
}

func TestSortAnnotations_RiskBeforeQualityDemote(t *testing.T) {
	items := []Annotation{
		{ID: "quality_hardfail", Family: AnnotationFamilyNegative, Priority: 5},
		{ID: "risk_x", Family: AnnotationFamilyNegative, Priority: 90},
	}
	sortAnnotations(items)
	if items[0].ID != "risk_x" {
		t.Errorf("want risk first, got %q", items[0].ID)
	}
}

// TestResolveAnnotations_Idempotent 逐字段比较而非整结构体 `!=`：
// Annotation 含 Metadata map[string]any 字段，使该类型不可比较，
// `a[i] != b[i]` 会编译失败（Go 语言规范：含 map 字段的结构体不可比较）。
func TestResolveAnnotations_Idempotent(t *testing.T) {
	task := ServiceConfig{BoardReason: "quality_hardfail", BoardReasonModels: "opus-4-8"}
	a := ResolveAnnotations(task, nil, 0)
	b := ResolveAnnotations(task, nil, 0)
	if len(a) != len(b) {
		t.Fatalf("len mismatch %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID ||
			a[i].Family != b[i].Family ||
			a[i].Icon != b[i].Icon ||
			a[i].Label != b[i].Label ||
			a[i].Tooltip != b[i].Tooltip ||
			a[i].Priority != b[i].Priority ||
			a[i].Origin != b[i].Origin {
			t.Errorf("idx %d differs: %+v vs %+v", i, a[i], b[i])
		}
	}
}
