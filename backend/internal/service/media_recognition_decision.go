package service

// MediaRecognitionPolicyVersion 固定整理规则，已保存的作品不随搜索顺序改变。
const MediaRecognitionPolicyVersion = "2026-11-03-v1"

// MediaRecognitionDecision 只返回当前结果，候选与筛选过程不生成快照。
type MediaRecognitionDecision struct {
	ResultCode string `json:"resultCode"`
	Message    string `json:"message"`
}
