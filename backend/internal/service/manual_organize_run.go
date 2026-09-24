package service

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Start is idempotent for a preview ID, including a lost HTTP response.
func (s *ManualOrganize) Start(accountID, id string) (OrganizeJob, error) {
	a, err := s.account(accountID)
	if err != nil {
		return OrganizeJob{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range s.doc.Jobs {
		if job.ID == id && job.Config.AccountID == accountID {
			result := job
			result.Items = append([]OrganizeItem{}, job.Items...)
			return result, nil
		}
	}
	s.pruneLocked()
	job, ok := s.plans[id]
	if !ok || job.Config.AccountID != accountID {
		return OrganizeJob{}, fmt.Errorf("%w：预览已过期，请重新预览", ErrOrganizeConflict)
	}
	if a.Revision != job.Revision {
		return OrganizeJob{}, fmt.Errorf("%w：账号配置已变化，请重新扫描", ErrOrganizeConflict)
	}
	if s.pending != nil {
		return OrganizeJob{}, fmt.Errorf("%w：已有整理任务执行中，请等待完成", ErrOrganizeConflict)
	}
	for _, item := range job.Items {
		if item.Conflict != "" {
			return OrganizeJob{}, fmt.Errorf("%w：请先处理预览中的冲突", ErrOrganizeConflict)
		}
	}
	job.Items = append([]OrganizeItem{}, job.Items...)
	job.Status, job.UpdatedAt = "queued", time.Now()
	doc := s.doc
	doc.Jobs = append([]OrganizeJob{job}, doc.Jobs...)
	if len(doc.Jobs) > 30 {
		doc.Jobs = doc.Jobs[:30]
	}
	if err := s.saveLocked(doc); err != nil {
		return OrganizeJob{}, err
	}
	workerJob := job
	workerJob.Items = append([]OrganizeItem{}, job.Items...)
	s.pending = &organizePending{job: workerJob, cookie: a.Cookie}
	delete(s.plans, id)
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return job, nil
}

func (s *ManualOrganize) checkpoint(job *OrganizeJob) error {
	started := time.Now()
	job.UpdatedAt = time.Now()
	job.Stats.Checkpoints++
	if !job.Stats.StartedAt.IsZero() {
		job.Stats.ElapsedMS = time.Since(job.Stats.StartedAt).Milliseconds()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.doc
	doc.Jobs = append([]OrganizeJob{}, s.doc.Jobs...)
	for i := range doc.Jobs {
		if doc.Jobs[i].ID == job.ID {
			doc.Jobs[i] = *job
			doc.Jobs[i].Items = append([]OrganizeItem{}, job.Items...)
			if err := s.saveLocked(doc); err != nil {
				return err
			}
			job.Stats.SaveMS += time.Since(started).Milliseconds()
			// Include this write in live stats; the durable snapshot contains
			// timing through the preceding write (measuring a save cannot be atomic
			// with that same save). No extra disk write just for timing statistics.
			s.doc.Jobs[i].Stats = job.Stats
			return nil
		}
	}
	return fmt.Errorf("整理任务记录不存在")
}

// Run is owned and joined by the server's worker group. Config changes after
// Start cannot change the account, cookie, paths or names of the pending job.
func (s *ManualOrganize) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.wake:
			s.mu.Lock()
			pending := s.pending
			s.mu.Unlock()
			if pending == nil {
				continue
			}
			jobCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
			err := s.runJob(jobCtx, &pending.job, pending.cookie)
			cancel()
			s.mu.Lock()
			s.pending = nil
			s.mu.Unlock()
			if err != nil {
				return err
			}
		}
	}
}

func organizePublicError(err error, cookie string) string {
	message := err.Error()
	if cookie != "" {
		message = strings.ReplaceAll(message, cookie, "[凭据已隐藏]")
	}
	for _, part := range strings.Split(cookie, ";") {
		_, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && len(value) >= 4 {
			message = strings.ReplaceAll(message, value, "[已隐藏]")
		}
	}
	return message
}

type organizeSafeError struct {
	err     error
	message string
}

func (e *organizeSafeError) Error() string { return e.message }
func (e *organizeSafeError) Unwrap() error { return e.err }
