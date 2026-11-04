package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestDriveSTRMAttachmentCooldownEscalatesAndHonorsRetryAfter(t *testing.T) {
	s := &DriveSTRMService{}
	for i, want := range []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute} {
		before := time.Now()
		if !s.coolAttachments(&p115.HTTPError{Status: 403}, "附件下载") {
			t.Fatal("HTTP 403 未触发冷却")
		}
		state := s.AttachmentState()
		if state.CoolingUntil.Before(before.Add(want)) || state.CoolingUntil.After(time.Now().Add(want)) || !state.Probing {
			t.Fatal("冷却未按失败次数递增", i, state)
		}
		until := state.CoolingUntil
		s.coolAttachments(&p115.HTTPError{Status: 403}, "附件下载")
		if !s.AttachmentState().CoolingUntil.Equal(until) {
			t.Fatal("同一批错误延长或重复升级了冷却")
		}
		s.metadataControlMu.Lock()
		s.metadataControl.state.CoolingUntil = time.Now().Add(-time.Second)
		s.metadataControlMu.Unlock()
	}
	before := time.Now()
	s.coolAttachments(&p115.HTTPError{Status: 429, RetryAfter: 20 * time.Minute}, "附件下载")
	if s.AttachmentState().CoolingUntil.Before(before.Add(20 * time.Minute)) {
		t.Fatal("未遵守 Retry-After", s.AttachmentState())
	}
	s.attachmentProbeSucceeded()
	if !s.AttachmentState().Probing {
		t.Fatal("冷却未结束就允许成功探测解除")
	}
	s.metadataControlMu.Lock()
	s.metadataControl.state.CoolingUntil = time.Now().Add(-time.Second)
	s.metadataControlMu.Unlock()
	s.attachmentProbeSucceeded()
	if state := s.AttachmentState(); state.Probing || state.LastError != "" || !state.CoolingUntil.IsZero() {
		t.Fatal("成功探测未清除冷却状态", state)
	}
}

func TestDriveSTRMAttachmentCooldownClassifiesBusinessThrottleSafely(t *testing.T) {
	s := &DriveSTRMService{}
	if s.coolAttachments(errors.New("网络暂时不可用"), "附件下载") || s.coolAttachments(&p115.HTTPError{Status: 500}, "附件下载") {
		t.Fatal("普通网络失败不应暂停整条队列")
	}
	err := fmt.Errorf("上游私密响应内容：%w", p115.ErrShareThrottled)
	if !s.coolAttachments(err, "附件取链") {
		t.Fatal("未识别被包装的业务风控")
	}
	if state := s.AttachmentState(); state.LastError != "115 返回业务风控，附件队列暂停后将单件探测恢复" || time.Until(state.CoolingUntil) < 55*time.Second {
		t.Fatal("业务风控提示或冷却不安全", state)
	}
}
