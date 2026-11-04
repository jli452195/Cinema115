package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/storage"
)

type mediaHistoryGateStore struct {
	mediaOrganizeStorage
	entered chan struct{}
	resume  chan struct{}
}

func (store *mediaHistoryGateStore) QueryMediaOrganizeLive(ctx context.Context, query, status string, page int, live []storage.MediaOrganizeLiveTaskRow) (storage.MediaOrganizeQuery, error) {
	close(store.entered)
	select {
	case <-ctx.Done():
		return storage.MediaOrganizeQuery{}, ctx.Err()
	case <-store.resume:
		return store.mediaOrganizeStorage.QueryMediaOrganizeLive(ctx, query, status, page, live)
	}
}

func TestMediaHistorySlowQueryDoesNotBlockCheckpoint(t *testing.T) {
	s, _, _ := newMediaSQLiteTest(t)
	run := mediaRecordFixture("history-slow", "running", 1)
	run.Entries[0].Status, run.Entries[0].Result = "ready", nil
	saveMediaFixtures(t, s, run)
	s.active = run.ID
	gate := &mediaHistoryGateStore{mediaOrganizeStorage: s.db, entered: make(chan struct{}), resume: make(chan struct{})}
	s.db = gate
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	type historyResult struct {
		value MediaOrganizeHistory
		err   error
	}
	result := make(chan historyResult, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		value, err := s.History(ctx, "", "all", 1)
		result <- historyResult{value, err}
	}()
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("历史查询未进入隔离等待")
	}
	checkpoint := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		run.Message = "后台仍可保存进度"
		checkpoint <- s.checkpoint(&run)
	}()
	select {
	case err := <-checkpoint:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("慢历史查询占用了整理业务锁")
	}
	_, release := s.beginMediaEntries(&run, []int{0})
	defer release()
	close(gate.resume)
	select {
	case got := <-result:
		if got.err != nil || len(got.value.Items) != 1 || got.value.Items[0].CanDelete || !got.value.Active {
			t.Fatal("响应须按最新在途操作计算权限", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("释放慢查询后未返回")
	}
}

func TestMediaHistoryCanceledQueryReleasesState(t *testing.T) {
	s, _, _ := newMediaSQLiteTest(t)
	gate := &mediaHistoryGateStore{mediaOrganizeStorage: s.db, entered: make(chan struct{}), resume: make(chan struct{})}
	s.db = gate
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	workers.Add(1)
	go func() {
		defer workers.Done()
		_, err := s.History(ctx, "", "all", 1)
		done <- err
	}()
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("历史查询未进入隔离等待")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("取消后历史查询未结束")
	}
	if state := s.State(); state.Active {
		t.Fatal(state)
	}
}
