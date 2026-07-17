package runtime

import "strings"

var refusalMarkers = []string{
	"i can't help", "i cannot help", "i can't assist", "i cannot assist",
	"i'm unable to assist", "i am unable to assist", "i won't help",
	"i must decline", "i cannot provide assistance", "i'm not able to help",
	"i'm sorry, but i can't", "i cannot comply",
	"无法协助", "无法帮助", "不能协助", "无法提供帮助", "恕难从命", "我不能帮",
}

// isRefusal 判断一段无可执行代码的模型回复是否为拒绝执行授权任务。
func isRefusal(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range refusalMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
