package monitor

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// 期望的变体数量。任何增删变体都要同步这里，提醒 review 者重新过一遍不变量。
const wantVariantCount = 5

func TestSpell(t *testing.T) {
	cases := map[int]string{
		10: "ten", 11: "eleven", 12: "twelve", 13: "thirteen", 14: "fourteen",
		15: "fifteen", 16: "sixteen", 17: "seventeen", 18: "eighteen", 19: "nineteen",
		20: "twenty", 21: "twenty-one", 30: "thirty", 40: "forty", 42: "forty-two",
		50: "fifty", 60: "sixty", 70: "seventy", 80: "eighty", 90: "ninety", 99: "ninety-nine",
	}
	for n, want := range cases {
		if got := spell(n); got != want {
			t.Errorf("spell(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestSpellNeverEmitsDigits(t *testing.T) {
	for n := 10; n <= 99; n++ {
		if strings.ContainsFunc(spell(n), unicode.IsDigit) {
			t.Fatalf("spell(%d) = %q contains a digit", n, spell(n))
		}
	}
}

func TestSpellPanicsOutOfRange(t *testing.T) {
	for _, n := range []int{-1, 0, 9, 100, 1000} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("spell(%d) did not panic", n)
				}
			}()
			_ = spell(n)
		}()
	}
}

// TestPromptVariantExamples 锁死每个变体在固定 (a,b) 下的确切文案，
// 让措辞改动是一次有意识的 review，而不是悄悄漂移。
func TestPromptVariantExamples(t *testing.T) {
	if len(promptVariants) != wantVariantCount {
		t.Fatalf("len(promptVariants) = %d, want %d", len(promptVariants), wantVariantCount)
	}
	want := []string{
		"Compute 42 + 37." + arithmeticReplyInstruction,
		"A shelf holds 42 books on one row and 37 on another. How many books in total?" + arithmeticReplyInstruction,
		"Find the sum of forty-two and thirty-seven." + arithmeticReplyInstruction,
		"Add thirty-seven to forty-two." + arithmeticReplyInstruction,
		"Combine forty-two items with 37 more items, then give the total." + arithmeticReplyInstruction,
	}
	for i, variant := range promptVariants {
		if got := variant(42, 37); got != want[i] {
			t.Errorf("promptVariants[%d](42,37) =\n  %q\nwant\n  %q", i, got, want[i])
		}
	}
}

// TestPromptInvariantsExhaustive 穷举全部操作数×全部变体，锁死安全不变量：
// 预期答案绝不是题面的子串（回显题面骗不过检测）、题面里不含裸和、
// 题面可原样注入模板 JSON 字符串（无引号/反斜杠/换行/控制符）。
func TestPromptInvariantsExhaustive(t *testing.T) {
	if len(promptVariants) != wantVariantCount {
		t.Fatalf("len(promptVariants) = %d, want %d", len(promptVariants), wantVariantCount)
	}
	for a := 10; a <= 99; a++ {
		for b := 10; b <= 99; b++ {
			expected := fmt.Sprintf("RP_ANSWER=%d", a+b)
			for i, variant := range promptVariants {
				if err := assertPromptInvariants(variant(a, b), expected, a+b); err != nil {
					t.Fatalf("variant=%d a=%d b=%d: %v", i, a, b, err)
				}
			}
		}
	}
}

// TestGenerateArithmeticPromptContract 校验对外契约：expected 恒为
// RP_ANSWER=<a+b>、operands 落在 [10,99]、题面命中且仅命中一个变体、满足全部不变量。
func TestGenerateArithmeticPromptContract(t *testing.T) {
	seen := make([]bool, len(promptVariants))
	for i := 0; i < 5000; i++ {
		a, b, prompt, expected := GenerateArithmeticPrompt()
		if a < 10 || a > 99 || b < 10 || b > 99 {
			t.Fatalf("operands out of range: a=%d b=%d", a, b)
		}
		if want := fmt.Sprintf("RP_ANSWER=%d", a+b); expected != want {
			t.Fatalf("expected = %q, want %q", expected, want)
		}
		if err := assertPromptInvariants(prompt, expected, a+b); err != nil {
			t.Fatalf("iter=%d: %v", i, err)
		}
		matched := -1
		for vi, variant := range promptVariants {
			if prompt == variant(a, b) {
				if matched >= 0 {
					t.Fatalf("prompt matched variants %d and %d: %q", matched, vi, prompt)
				}
				matched = vi
			}
		}
		if matched < 0 {
			t.Fatalf("prompt matched no variant: %q", prompt)
		}
		seen[matched] = true
	}
	for i, ok := range seen {
		if !ok {
			t.Errorf("promptVariants[%d] never selected across 5000 draws", i)
		}
	}
}

// TestGenerateArithmeticPromptConcurrent 让 -race 证明三次随机取样在同一把锁内。
func TestGenerateArithmeticPromptConcurrent(t *testing.T) {
	const workers, iters = 32, 400
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		go func() {
			for i := 0; i < iters; i++ {
				a, b, prompt, expected := GenerateArithmeticPrompt()
				if err := assertPromptInvariants(prompt, expected, a+b); err != nil {
					errs <- err
					return
				}
			}
			errs <- nil
		}()
	}
	for w := 0; w < workers; w++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

func assertPromptInvariants(prompt, expected string, sum int) error {
	if !strings.Contains(prompt, "`RP_ANSWER=`") {
		return fmt.Errorf("prompt missing backtick-wrapped marker: %q", prompt)
	}
	if strings.Contains(prompt, expected) {
		return fmt.Errorf("expected answer %q leaked into prompt: %q", expected, prompt)
	}
	if s := strconv.Itoa(sum); strings.Contains(prompt, s) {
		return fmt.Errorf("bare sum %q leaked into prompt: %q", s, prompt)
	}
	if strings.ContainsAny(prompt, "\"\\\n\r") {
		return fmt.Errorf("prompt has a JSON-unsafe character: %q", prompt)
	}
	for _, r := range prompt {
		if unicode.IsControl(r) {
			return fmt.Errorf("prompt has control rune %U: %q", r, prompt)
		}
	}
	if !json.Valid([]byte(`{"text":"` + prompt + `"}`)) {
		return fmt.Errorf("prompt breaks raw JSON string insertion: %q", prompt)
	}
	return nil
}
