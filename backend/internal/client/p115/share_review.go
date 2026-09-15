package p115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var ErrShareUnderReview = errors.New("115 分享正在审核中，已停止本条任务；请在审核通过后重新提交")

// 只识别接口的明确审核提示，不猜测未核实的数字状态码，也不扫描文件名。
func reviewMessage(message string) bool {
	message = strings.ToLower(strings.Join(strings.Fields(message), " "))
	for _, hint := range []string{"审核中", "正在审核", "等待审核", "待审核", "审核尚未完成", "审核未完成", "审核暂未完成", "under review", "pending review", "awaiting review", "being reviewed"} {
		if strings.Contains(message, hint) {
			return true
		}
	}
	return false
}

func responseMessage(value json.RawMessage) string {
	var message string
	_ = json.Unmarshal(value, &message)
	return strings.TrimSpace(message)
}

// share/snap 的审核样本即使 state=true 且有文件列表，share_state 仍为 0。
// 可访问对照样本为 1。使用原始字段区分“明确为 0”和“字段缺失”。
func snapshotUnderReview(data json.RawMessage) bool {
	var snapshot struct {
		State json.RawMessage `json:"share_state"`
		Info  struct {
			State  json.RawMessage `json:"share_state"`
			Reason json.RawMessage `json:"forbid_reason"`
		} `json:"shareinfo"`
	}
	if json.Unmarshal(data, &snapshot) != nil {
		return false
	}
	for _, state := range []json.RawMessage{snapshot.State, snapshot.Info.State} {
		value := strings.TrimSpace(string(state))
		if value == "0" || responseMessage(state) == "0" {
			return true
		}
	}
	return reviewMessage(responseMessage(snapshot.Info.Reason))
}

type shareResponse struct {
	State    json.RawMessage `json:"state"`
	Data     json.RawMessage `json:"data"`
	Error    json.RawMessage `json:"error"`
	Message  json.RawMessage `json:"message"`
	Msg      json.RawMessage `json:"msg"`
	ErrorMsg json.RawMessage `json:"error_msg"`
}

func (response shareResponse) underReview() bool {
	if snapshotUnderReview(response.Data) {
		return true
	}
	for _, value := range []json.RawMessage{response.Error, response.Message, response.Msg, response.ErrorMsg} {
		if reviewMessage(responseMessage(value)) {
			return true
		}
	}
	return false
}

// ownerID 从目录响应读取分享者编号，不依赖昵称或公开可访问性。
// 使用整数解析避免大整数经浮点数转换后发生误匹配，缺失或异常字段均不判为本人。
func (response shareResponse) ownerID() string {
	var snapshot struct {
		User struct {
			ID json.Number `json:"user_id"`
		} `json:"userinfo"`
	}
	if json.Unmarshal(response.Data, &snapshot) != nil {
		return ""
	}
	owner, err := strconv.ParseUint(snapshot.User.ID.String(), 10, 64)
	if err != nil || owner == 0 {
		return ""
	}
	return strconv.FormatUint(owner, 10)
}

// CookieUserID 仅从 UID 的用户编号部分解析，不比较设备标识和其他 Cookie 内容。
func CookieUserID(cookie string) string {
	for _, part := range strings.Split(cookie, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || name != "UID" {
			continue
		}
		uid, _, _ := strings.Cut(value, "_")
		user, err := strconv.ParseUint(uid, 10, 64)
		if err != nil || user == 0 {
			return ""
		}
		return strconv.FormatUint(user, 10)
	}
	return ""
}

func (response shareResponse) ownedBy(cookie string) bool {
	owner := response.ownerID()
	return owner != "" && owner == CookieUserID(cookie)
}

func (response shareResponse) message() string {
	for _, value := range []json.RawMessage{response.Error, response.Message, response.Msg, response.ErrorMsg} {
		if message := responseMessage(value); message != "" {
			return message
		}
	}
	return "请检查分享有效期、提取码及 115 登录状态"
}

// CheckPublicShareAccess 仅供公开状态诊断，转存工作流不使用它判断本人分享能否清理。
// 不带 Cookie，复用串行限速策略，只取一项。
func (c *ShareClient) CheckPublicShareAccess(ctx context.Context, share *ShareInfo) error {
	if share == nil || share.Code == "" {
		return fmt.Errorf("分享码不能为空")
	}
	query := url.Values{"share_code": {share.Code}, "receive_code": {share.Password}, "cid": {"0"}, "offset": {"0"}, "limit": {"1"}}
	raw, err := c.requestEnvelope(ctx, http.MethodGet, c.WebURL+"/share/snap?"+query.Encode(), "", nil)
	if err != nil {
		return err
	}
	var response shareResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("解析公开分享状态: %w", err)
	}
	if response.underReview() {
		return ErrShareUnderReview
	}
	_, err = parseSharePage(response.Data, 0)
	return err
}
