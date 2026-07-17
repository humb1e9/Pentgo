package runtime

import "regexp"

var findingLabelPattern = regexp.MustCompile("\\[(VERIFIED|LIKELY|INFERRED)\\]")

// extractFindingLabels 从模型回复文本按出现顺序提取去重后的发现标签。
func extractFindingLabels(text string) []EvidenceLevel {
	matches := findingLabelPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[EvidenceLevel]bool, 3)
	labels := make([]EvidenceLevel, 0, len(matches))
	for _, match := range matches {
		level := EvidenceLevel(match[1])
		if seen[level] {
			continue
		}
		seen[level] = true
		labels = append(labels, level)
	}
	return labels
}
