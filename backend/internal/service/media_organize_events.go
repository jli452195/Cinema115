package service

import "sync"

// 只发送失效通知，不复制任务或查询历史；容量为一，慢页面不阻塞整理线程。
func (s *MediaOrganizer) SubscribeHistoryChanges() (<-chan struct{}, func()) {
	changes := make(chan struct{}, 1)
	s.mu.Lock()
	if s.historyChanges == nil {
		s.historyChanges = make(map[chan struct{}]struct{})
	}
	s.historyChanges[changes] = struct{}{}
	s.mu.Unlock()
	var once sync.Once
	return changes, func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.historyChanges, changes)
			s.mu.Unlock()
		})
	}
}

// 调用方持有业务锁；只在历史数据或操作权限改变时调用。
func (s *MediaOrganizer) historyChangedLocked() {
	for changes := range s.historyChanges {
		select {
		case changes <- struct{}{}:
		default:
		}
	}
}
