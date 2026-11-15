package recognition

import "regexp/syntax"

// 每个分支只需命中一项必含文字即可成为候选，不能据此直接生成识别结果。
type literalHint struct {
	runes []rune
	fold  bool
}

// requiredLiterals 返回一组覆盖所有成功路径的必含文字。
// 不能证明必含文字、包含可空分支或分支太多时退回通用正则，避免漏识别。
func requiredLiterals(node *syntax.Regexp) []literalHint {
	const maxHints = 16
	switch node.Op {
	case syntax.OpLiteral:
		if len(node.Rune) > 0 {
			return []literalHint{{runes: node.Rune, fold: node.Flags&syntax.FoldCase != 0}}
		}
	case syntax.OpCapture, syntax.OpPlus:
		return requiredLiterals(node.Sub[0])
	case syntax.OpRepeat:
		if node.Min > 0 {
			return requiredLiterals(node.Sub[0])
		}
	case syntax.OpConcat:
		var best []literalHint
		for _, child := range node.Sub {
			if hints := requiredLiterals(child); literalHintScore(hints) > literalHintScore(best) {
				best = hints
			}
		}
		return best
	case syntax.OpAlternate:
		hints := make([]literalHint, 0, min(len(node.Sub), maxHints))
		for _, child := range node.Sub {
			branch := requiredLiterals(child)
			if len(branch) == 0 || len(hints)+len(branch) > maxHints {
				return nil
			}
			hints = append(hints, branch...)
		}
		return hints
	}
	return nil
}

// 最短分支决定筛选能力；单个 ASCII 字符太常见，继续直接执行正则。
func literalHintScore(hints []literalHint) int {
	if len(hints) == 0 {
		return 0
	}
	score := int(^uint(0) >> 1)
	for _, hint := range hints {
		weight := len(hint.runes)
		if weight == 1 && hint.runes[0] > 127 {
			weight = 2
		}
		score = min(score, weight)
	}
	return score
}
