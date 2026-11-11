package mediafacts

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// CoordinateClaim 只记录文件声明，是否允许自动整理由入口采用；季零和集零均为有效坐标。
type CoordinateClaim struct {
	Numbering Numbering
	Start     int
	End       int
	Found     bool
	Valid     bool
	Code      string
}

var coordinateDeclaration = regexp.MustCompile(`(?i)S[0-9]+\s*E`)
var coordinateSingle = regexp.MustCompile(`(?i)^S([0-9]{1,4})\s*E([0-9]{1,5})(?:v[0-9]{1,3})?`)
var coordinateRange = regexp.MustCompile(`(?i)^\s*([-~～〜至到+&])\s*E?([0-9]{1,5})(?:v[0-9]{1,3})?`)
var coordinateExtraEpisode = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:EPISODE|EP|E)[ ._]*#?[ ._]*[0-9]`)
var coordinateBracketNumber = regexp.MustCompile(`(?i)[\[【(（]\s*[0-9]{1,6}(?:\.[0-9]{1,3})?[AB]?(?:v[0-9]{1,3})?(?:\s*[-~～〜至到+&/、,]\s*(?:E|EP|第)?\s*[0-9]{1,6}(?:\.[0-9]{1,3})?[AB]?(?:v[0-9]{1,3})?)*\s*[\]】)）]`)
var coordinateExtraBare = regexp.MustCompile(`(?:^|\s)-\s*([0-9]+)`)
var coordinateSceneYear = regexp.MustCompile(`^\.(?:19|20|21)[0-9]{2}(?:$|[ ._\-])`)
var coordinateUnsupportedContinuation = regexp.MustCompile(`(?i)^\s*[-~～〜至到+&/、,]\s*(?:E|EP)?\s*[0-9]`)
var coordinateIncompleteContinuation = regexp.MustCompile(`(?i)^\s*[-~～〜至到+&/、,]\s*(?:EP|E)(?:$|[^a-z0-9])`)

// ParseStandardCoordinates 直接解析标准坐标和范围，不用发布标题分类否定已明确的坐标。
// Start、End 是去扩展名原文中的字节位置，描述中的章号与原播话数不替代它。
func ParseStandardCoordinates(stem string) CoordinateClaim {
	claim := CoordinateClaim{Code: "NUMBERING_MISSING"}
	declarations := coordinateDeclaration.FindAllStringIndex(stem, -1)
	positions := make([][]int, 0, len(declarations))
	for _, declaration := range declarations {
		if declaration[0] == 0 || !numberingAlnum(stem[declaration[0]-1]) {
			positions = append(positions, declaration)
		}
	}
	if len(positions) == 0 {
		return claim
	}
	claim.Found, claim.Start, claim.End, claim.Code = true, positions[0][0], positions[0][1], "NUMBERING_INVALID"
	var numbering Numbering
	var validation []byte
	if len(positions) > 1 {
		validation = []byte(stem)
	}
	for i, position := range positions {
		parsed, end, code := coordinateNumberingAt(stem, position[0])
		if i == 0 && end > claim.End {
			claim.End = end
		}
		if code != "NUMBERING_EXPLICIT" {
			claim.Code = code
			return claim
		}
		if i == 0 {
			numbering = parsed
			continue
		}
		if parsed != numbering {
			claim.Code = "NUMBERING_CONFLICT"
			return claim
		}
		// 只在校验副本中遮蔽等价声明，原文位置与第一处坐标保持不变。
		for j := position[0]; j < end; j++ {
			validation[j] = ' '
		}
	}
	remainder := stem[claim.End:]
	if validation != nil {
		remainder = string(validation[claim.End:])
	}
	if coordinateExtraEpisode.MatchString(remainder) || coordinateExtraBracketNumber(remainder) || coordinateExtraBareNumber(remainder) {
		claim.Code = "NUMBERING_CONFLICT"
		return claim
	}
	if coordinateExtraEpisode.MatchString(stem[:claim.Start]) {
		claim.Code = "NUMBERING_CONFLICT"
		return claim
	}
	claim.Numbering, claim.Valid, claim.Code = numbering, true, "NUMBERING_EXPLICIT"
	return claim
}

// 每份声明独立校验完整范围，不能用其他位置的合法坐标掩盖残缺或错误声明。
func coordinateNumberingAt(stem string, start int) (Numbering, int, string) {
	match := coordinateSingle.FindStringSubmatchIndex(stem[start:])
	if match == nil {
		return Numbering{}, start, "NUMBERING_INVALID"
	}
	end := start + match[1]
	season, _ := strconv.Atoi(stem[start+match[2] : start+match[3]])
	episode, _ := strconv.Atoi(stem[start+match[4] : start+match[5]])
	if season > 1000 || episode > 10000 || !coordinateEnd(stem, end) {
		return Numbering{}, end, "NUMBERING_INVALID"
	}
	numbering := Numbering{Season: season, Episode: episode}
	if matched := coordinateRange.FindStringSubmatchIndex(stem[end:]); matched != nil {
		rangeEnd := end + matched[1]
		if !coordinateRangeDescription(stem[end:], matched) {
			if !coordinateEnd(stem, rangeEnd) {
				return Numbering{}, end, "NUMBERING_INVALID"
			}
			last, _ := strconv.Atoi(stem[end+matched[4] : end+matched[5]])
			join := stem[end+matched[2] : end+matched[3]]
			if last <= episode || last > 10000 || (join == "+" || join == "&") && last != episode+1 {
				return Numbering{}, end, "NUMBERING_INVALID"
			}
			numbering.EndEpisode, end = last, rangeEnd
		}
	}
	remainder := stem[end:]
	if coordinateIncompleteContinuation.MatchString(remainder) || coordinateUnsupportedContinuation.MatchString(remainder) && !coordinateLeadingDescription(remainder) {
		return Numbering{}, end, "NUMBERING_CONFLICT"
	}
	return numbering, end, "NUMBERING_EXPLICIT"
}

func coordinateEnd(stem string, end int) bool {
	if end == len(stem) {
		return true
	}
	if numberingAlnum(stem[end]) {
		return false
	}
	if stem[end] == '.' && end+1 < len(stem) && stem[end+1] >= '0' && stem[end+1] <= '9' {
		return coordinateQuality(stem[end+1:]) || coordinateSceneYear.MatchString(stem[end:])
	}
	return true
}

func coordinateQuality(value string) bool {
	match := resourceQuality.FindStringIndex(value)
	return match != nil && match[0] == 0 && (match[1] == len(value) || !numberingAlnum(value[match[1]]))
}

func coordinateDescriptiveNumber(stem string, start, end int) bool {
	if end == len(stem) {
		return false
	}
	next, _ := utf8.DecodeRuneInString(stem[end:])
	if unicode.IsLetter(next) && next > unicode.MaxASCII {
		return true
	}
	// 有空格分隔的数字开头发布组保留描述语义，紧贴坐标的残缺范围仍报错。
	if match := numberingPublisher.FindStringSubmatchIndex(stem[start:]); match != nil && match[0] == 0 {
		tokenEnd := start + match[3]
		if tokenEnd < len(stem) {
			next, _ := utf8.DecodeRuneInString(stem[tokenEnd:])
			if !unicode.IsSpace(next) {
				return false
			}
		}
		token := stem[start+match[2] : start+match[3]]
		return !numberingPart.MatchString(token) && strings.ContainsFunc(token, func(r rune) bool { return unicode.Is(unicode.Latin, r) })
	}
	return false
}

func coordinateLeadingDescription(remainder string) bool {
	matched := coordinateRange.FindStringSubmatchIndex(remainder)
	if matched == nil {
		return false
	}
	return coordinateRangeDescription(remainder, matched)
}

func coordinateRangeDescription(remainder string, matched []int) bool {
	// 结束集显式携带 E 时，后面的中文单位不能把完整范围降为描述。
	if strings.EqualFold(strings.TrimSpace(remainder[matched[3]:matched[4]]), "E") {
		return false
	}
	return coordinateQuality(remainder[matched[4]:]) || coordinateDescriptiveNumber(remainder, 0, matched[1])
}

func coordinateExtraBareNumber(remainder string) bool {
	for _, match := range coordinateExtraBare.FindAllStringSubmatchIndex(remainder, -1) {
		if coordinateQuality(remainder[match[2]:]) || coordinateDescriptiveNumber(remainder, match[0], match[1]) {
			continue
		}
		if match[1] == len(remainder) || !numberingAlnum(remainder[match[1]]) {
			return true
		}
	}
	return false
}

func coordinateExtraBracketNumber(remainder string) bool {
	for _, span := range coordinateBracketNumber.FindAllStringIndex(remainder, -1) {
		match := remainder[span[0]:span[1]]
		// 四位发行年份不是第二份集号声明，分集标题也可能直接提到年份。
		number := strings.Trim(match, " []【】()（）\t")
		if year, err := strconv.Atoi(number); err == nil && len(number) == 4 && year >= 1900 && year <= 2199 {
			continue
		}
		// 紧贴描述或规格的尾部复制后缀不提供集号；独立括号和补零编号仍保留冲突。
		if span[0] > 0 && span[1] == len(remainder) && (strings.HasPrefix(match, "(") || strings.HasPrefix(match, "（")) && len(number) <= 3 && number[0] >= '1' && number[0] <= '9' && onlyDigits(number) {
			previous, _ := utf8.DecodeLastRuneInString(remainder[:span[0]])
			if unicode.IsLetter(previous) || unicode.IsDigit(previous) {
				continue
			}
		}
		return true
	}
	return false
}
