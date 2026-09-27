package service

import (
	"context"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

func TestPlaybackSubmissionReasonReachesLogAndRecord(t *testing.T) {
	s, f, selected := setupFastCache(t)
	f.uploadErr = &p115.InstantUploadError{Stage: "第 1 次秒传提交", Reason: "115 返回 HTTP 401", Cause: p115.ErrInstantUncertain}
	_, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "player")
	if err == nil || !strings.Contains(err.Error(), "第 1 次秒传提交：115 返回 HTTP 401") {
		t.Fatal("error detail lost", err)
	}
	entries := s.Status().Entries
	if len(entries) != 1 || !strings.Contains(entries[0].Message, "HTTP 401") || entries[0].Status != "uncertain" || f.uploads != 1 {
		t.Fatal("diagnostic missing or retried upload")
	}
}
