package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

type DriveSTRMAttachmentState struct {
	CoolingUntil time.Time `json:"coolingUntil,omitempty"`
	LastError    string    `json:"lastError,omitempty"`
	Probing      bool      `json:"probing"`
}

type driveSTRMAttachmentControl struct {
	state    DriveSTRMAttachmentState
	failures int
}

func (s *DriveSTRMService) AttachmentState() DriveSTRMAttachmentState {
	s.metadataControlMu.Lock()
	defer s.metadataControlMu.Unlock()
	return s.metadataControl.state
}

func (s *DriveSTRMService) attachmentCooling() time.Duration {
	s.metadataControlMu.Lock()
	defer s.metadataControlMu.Unlock()
	return max(time.Duration(0), time.Until(s.metadataControl.state.CoolingUntil))
}

func attachmentThrottled(err error) bool {
	var upstream *p115.HTTPError
	return errors.Is(err, p115.ErrShareThrottled) || errors.As(err, &upstream) && (upstream.Status == 403 || upstream.Status == 429)
}

// HTTP 和业务风控影响整条附件队列，不能通过切换待下载文件继续请求。
func (s *DriveSTRMService) coolAttachments(err error, stage string) bool {
	var upstream *p115.HTTPError
	if !attachmentThrottled(err) {
		return false
	}
	errors.As(err, &upstream)
	now := time.Now()
	s.metadataControlMu.Lock()
	control := &s.metadataControl
	newWindow := !control.state.CoolingUntil.After(now)
	if newWindow {
		control.failures++
	}
	delay := time.Minute
	if control.failures == 2 {
		delay = 5 * time.Minute
	} else if control.failures > 2 {
		delay = 15 * time.Minute
	}
	if newWindow {
		control.state.CoolingUntil = now.Add(delay)
	}
	if upstream != nil && now.Add(upstream.RetryAfter).After(control.state.CoolingUntil) {
		control.state.CoolingUntil = now.Add(upstream.RetryAfter)
	}
	if upstream != nil {
		control.state.LastError = fmt.Sprintf("%s返回 HTTP %d，附件队列暂停后将单件探测恢复", stage, upstream.Status)
	} else {
		control.state.LastError = "115 返回业务风控，附件队列暂停后将单件探测恢复"
	}
	control.state.Probing = true
	s.metadataControlMu.Unlock()
	// 预览地址可能过期，失败后不继续复用同一批签名地址。
	s.mu.Lock()
	clear(s.metadataThumbnails)
	s.mu.Unlock()
	return true
}

func (s *DriveSTRMService) attachmentProbeSucceeded() {
	s.metadataControlMu.Lock()
	defer s.metadataControlMu.Unlock()
	if !s.metadataControl.state.CoolingUntil.After(time.Now()) {
		s.metadataControl = driveSTRMAttachmentControl{}
	}
}

// 所有目录共享取链批次间隔，不积攒突发额度；等待取消不会占用下一批额度。
func (s *DriveSTRMService) waitAttachment(ctx context.Context, settings model.DriveSTRMSettings) error {
	interval := time.Duration(max(0, settings.MetadataBatchIntervalSeconds)) * time.Second
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.metadataControlMu.Lock()
		until := s.metadataStarted.Add(max(interval, s.metadataInterval))
		if s.metadataControl.state.CoolingUntil.After(until) {
			until = s.metadataControl.state.CoolingUntil
		}
		delay := time.Until(until)
		if delay <= 0 {
			s.metadataStarted, s.metadataInterval = time.Now(), interval
			s.metadataControlMu.Unlock()
			return nil
		}
		s.metadataControlMu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *DriveSTRMService) attachmentSafetySettings(ctx context.Context, frozen model.DriveSTRMSettings) (model.DriveSTRMSettings, error) {
	latest, err := s.Store.GetDriveSTRMSettings(ctx)
	if err != nil {
		return frozen, err
	}
	frozen.MetadataBatchIntervalSeconds = max(frozen.MetadataBatchIntervalSeconds, latest.MetadataBatchIntervalSeconds)
	frozen.MetadataConcurrency = max(1, min(max(1, frozen.MetadataConcurrency), max(1, latest.MetadataConcurrency)))
	return frozen, nil
}
