package service

import (
	"fmt"
	"strings"
	"time"
)

func (s *OrganizeEngine) checkpoint(job *OrganizeJob) error {
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
			doc.Jobs[i] = cloneOrganizeJob(*job)
			doc.Jobs[i].Items = append([]OrganizeItem{}, job.Items...)
			updateDownloadShareMembers(&doc, *job)
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
