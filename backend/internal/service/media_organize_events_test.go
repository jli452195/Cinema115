package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestMediaHistoryNotificationsMergeAndRelease(t *testing.T) {
	s := &MediaOrganizer{}
	first, stopFirst := s.SubscribeHistoryChanges()
	second, stopSecond := s.SubscribeHistoryChanges()
	defer stopSecond()
	s.mu.Lock()
	for i := 0; i < 10000; i++ {
		s.historyChangedLocked()
	}
	s.mu.Unlock()
	if len(first) != 1 || len(second) != 1 {
		t.Fatal("高频变化应合并为一次通知且不等待消费者")
	}
	<-first
	stopFirst()
	stopFirst()
	s.mu.Lock()
	s.historyChangedLocked()
	remaining := len(s.historyChanges)
	s.mu.Unlock()
	if len(first) != 0 || remaining != 1 {
		t.Fatal("取消订阅应幂等并从通知列表移除")
	}
}

func TestMediaHistoryNotificationsCoverPublishedStateAndPermissions(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	changes, stop := s.SubscribeHistoryChanges()
	defer stop()
	assertChanged := func(want bool) {
		t.Helper()
		select {
		case <-changes:
			if !want {
				t.Fatal("仅更新规则不应通知历史刷新")
			}
		default:
			if want {
				t.Fatal("历史数据或权限改变后缺少通知")
			}
		}
	}
	s.mu.Lock()
	err := s.persistLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	assertChanged(false)
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertChanged(true)
	run.Status = "running"
	run.Entries = []MediaOrganizeEntry{{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "video"}, MediaType: "video"}, Status: "ready"}}
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	assertChanged(true)
	_, release := s.beginMediaEntries(&run, []int{0})
	assertChanged(true)
	release()
	assertChanged(true)
	if _, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Items: []MediaOrganizeDeleteItem{{RunID: run.ID, FileID: "video"}}}); err != nil {
		t.Fatal(err)
	}
	assertChanged(true)
}

func TestMediaHistoryRecoveryProtectionRemovalNotifies(t *testing.T) {
	for _, action := range []string{"stop", "refresh", "replace"} {
		for _, recovery := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/recovery_%v", action, recovery), func(t *testing.T) {
				s, _, _, rule := newMediaOrganizeTest(t)
				run := mediaRecordFixture("recovery", "failed", 1)
				run.Rule = rule
				s.doc.Runs = []MediaOrganizeRun{run}
				runtime := &mediaCloudRuntime{rule: rule}
				if recovery {
					runtime.eventRecoveryIDs = []string{run.ID}
				}
				s.cloudMonitors = map[string]*mediaCloudRuntime{rule.ID: runtime}
				changes, stop := s.SubscribeHistoryChanges()
				defer stop()
				if recovery && s.taskPermissionsLocked(run)[run.Entries[0].File.ID].CanDelete {
					t.Fatal("恢复中的记录应先受保护")
				}
				switch action {
				case "stop":
					if _, err := s.Stop(context.Background(), rule.ID); err != nil {
						t.Fatal(err)
					}
				case "refresh":
					s.refreshCloudMonitorsLocked(time.Now())
				case "replace":
					rule.Name += "变更"
					s.cloudRuntimeLocked(rule)
				}
				if !s.taskPermissionsLocked(run)[run.Entries[0].File.ID].CanDelete || (len(changes) > 0) != recovery {
					t.Fatal("仅解除恢复保护时应通知历史权限刷新", len(changes))
				}
			})
		}
	}
}

func BenchmarkMediaHistoryNotification(b *testing.B) {
	for _, count := range []int{0, 1, 100} {
		b.Run(fmt.Sprintf("subscribers_%d", count), func(b *testing.B) {
			s := &MediaOrganizer{}
			for i := 0; i < count; i++ {
				_, stop := s.SubscribeHistoryChanges()
				b.Cleanup(stop)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.mu.Lock()
				s.historyChangedLocked()
				s.mu.Unlock()
			}
		})
	}
}
