package monitor

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

var (
	promptRandMu sync.Mutex
	promptRand   = rand.New(rand.NewSource(time.Now().UnixNano()))
)

// arithmeticReplyInstruction 是所有变体共用的输出指令。要点：
//   - 用反引号包住 `RP_ANSWER=`，故题面里出现的是 "`RP_ANSWER=`" 而非
//     "RP_ANSWER=<和>"——预期答案永远不会成为题面的子串，纯回显题面骗不过检测。
//   - "immediately followed" + "no spaces" 逼模型输出 RP_ANSWER=79 而非
//     RP_ANSWER = 79，避免真模型因多一个空格被子串校验判红。
//   - 刻意不给具体数字示例：示例数字可能恰好等于某次的和，反而把答案泄进题面。
const arithmeticReplyInstruction = " Reply only with `RP_ANSWER=` immediately followed by the total in digits, no spaces."

// promptVariants 是随机轮换的题面池——每次探测随机挑一个。设计意图是"移动靶"：
// 各变体用不同方式卡掉简单的正则抓取（纯数词让 \d+ 空手而归、混合表示让"抓所有
// 数字求和"算错、纯数字应用题没有 a+b 运算符模式……），没有单条正则能通吃五种；
// 想再加一种，往切片里 append 一行即可，中转商则要为每种新格式重新适配。
//
// 硬约束（新增/改动变体必须守住，prompt_test.go 穷举校验）：
//  1. 题面被原样注入模板 JSON 字符串值，故绝不能含 " \ 换行 回车 或控制符。
//  2. 只给算式、绝不写出答案——预期答案 RP_ANSWER=<和> 不得是题面子串，裸和也不得出现。
//  3. 单行文本，两位数加法，任何在线模型都能稳过。
var promptVariants = []func(a, b int) string{
	// ① 裸算式：最自然、真模型最稳；对纯正则最友好，靠池子里其它变体拉高整体成本。
	func(a, b int) string {
		return fmt.Sprintf("Compute %d + %d.%s", a, b, arithmeticReplyInstruction)
	},
	// ② 纯数字应用题：有两个数字但没有 "a + b" 运算符模式。
	func(a, b int) string {
		return fmt.Sprintf("A shelf holds %d books on one row and %d on another. How many books in total?%s", a, b, arithmeticReplyInstruction)
	},
	// ③ 全英文数词：题面里一个阿拉伯数字都没有，\d+ 抓不到操作数。
	func(a, b int) string {
		return fmt.Sprintf("Find the sum of %s and %s.%s", spell(a), spell(b), arithmeticReplyInstruction)
	},
	// ④ 数词 + 换序 + "Add"：干扰依赖固定位置/运算符字面的脆弱正则。
	func(a, b int) string {
		return fmt.Sprintf("Add %s to %s.%s", spell(b), spell(a), arithmeticReplyInstruction)
	},
	// ⑤ 混合表示（一个数词一个数字）：让"抓所有数字相加"只抓到 b、算错。
	func(a, b int) string {
		return fmt.Sprintf("Combine %s items with %d more items, then give the total.%s", spell(a), b, arithmeticReplyInstruction)
	},
}

var (
	numberOnes  = [...]string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine"}
	numberTeens = [...]string{"ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen"}
	numberTens  = [...]string{"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"}
)

// spell 把 10-99 转成不含阿拉伯数字的英文数词（如 42 → "forty-two"）。
// 越界直接 panic：操作数由 GenerateArithmeticPrompt 固定在 [10,99]，故正常路径不会触发；
// 一旦触发说明调用契约被破坏，fail-loud 好过静默回退成阿拉伯数字、破坏"纯数词无数字"不变量。
func spell(n int) string {
	if n < 10 || n > 99 {
		panic(fmt.Sprintf("spell: %d out of range [10,99]", n))
	}
	if n < 20 {
		return numberTeens[n-10]
	}
	tens := numberTens[n/10]
	if n%10 == 0 {
		return tens
	}
	return tens + "-" + numberOnes[n%10]
}

// GenerateArithmeticPrompt 随机挑一个题面变体生成两位数加法题，用于抬高中转商
// "缓存/回显 mock"的作弊成本。返回操作数、题面和预期答案（格式恒为 RP_ANSWER=<和>）。
// 答案只在此处算出、只用于检测，绝不写进题面（见 promptVariants 硬约束）。
func GenerateArithmeticPrompt() (a, b int, prompt, expectedAnswer string) {
	// 三次取样必须在同一把锁内：promptRand 非并发安全。
	promptRandMu.Lock()
	a = promptRand.Intn(90) + 10 // 10-99
	b = promptRand.Intn(90) + 10 // 10-99
	variant := promptVariants[promptRand.Intn(len(promptVariants))]
	promptRandMu.Unlock()

	expectedAnswer = fmt.Sprintf("RP_ANSWER=%d", a+b)
	prompt = variant(a, b)
	return a, b, prompt, expectedAnswer
}
