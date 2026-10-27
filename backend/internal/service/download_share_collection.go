package service

import (
	"fmt"
	"path"
	"strings"
	"time"
)

const (
	downloadShareQuiet   = 30 * time.Second
	downloadShareMaxWait = 2 * time.Minute
)

func unhandedDownloadShare(job OrganizeJob) bool {
	return job.Download != nil && job.Download.Share != nil && !job.Download.Share.HandedOff
}

func waitingDownloadShare(job OrganizeJob) bool {
	return unhandedDownloadShare(job) && job.Download.BatchID != "" && job.Download.Share.LeaderID == "" && !job.Download.Share.ReadyAt.IsZero() && job.Status == "running"
}

// Caller holds the download and document locks. Bound resources waiting for a
// collection still occupy queue capacity even though no goroutine tracks them.
func (s *DownloadOrganize) trackedDownloadsLocked() int {
	count := len(s.live)
	for _, job := range s.Organize.doc.Jobs {
		collected := unhandedDownloadShare(job) && job.Download.Share.LeaderID != "" && (job.Status == "queued" || job.Status == "running" || job.Status == "completed")
		if (waitingDownloadShare(job) || collected) && s.live[job.ID] == nil {
			count++
		}
	}
	return count
}

// This pass is local only. Seal before writes and persist the original members
// with their leader atomically. A later arrival gets its own immutable wrapper.
func (s *DownloadOrganize) sealShareCollections(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.Organize
	o.mu.Lock()
	defer o.mu.Unlock()
	type collection struct {
		ready       []int
		first, last time.Time
		waiting     bool
	}
	groups := make(map[string]*collection, len(o.doc.Jobs))
	for i, job := range o.doc.Jobs {
		if !unhandedDownloadShare(job) || job.Download.BatchID == "" || job.Download.Share.LeaderID != "" {
			continue
		}
		id := job.Download.BatchID
		group := groups[id]
		if group == nil {
			group = &collection{}
			groups[id] = group
		}
		if !waitingDownloadShare(job) || s.live[job.ID] != nil {
			group.waiting = true
			continue
		}
		group.ready = append(group.ready, i)
		at := job.Download.Share.ReadyAt
		if group.first.IsZero() || at.Before(group.first) {
			group.first = at
		}
		if at.After(group.last) {
			group.last = at
		}
	}
	for _, group := range groups {
		if len(group.ready) == 0 || group.waiting && now.Before(group.last.Add(downloadShareQuiet)) && now.Before(group.first.Add(downloadShareMaxWait)) {
			continue
		}
		leader := cloneOrganizeJob(o.doc.Jobs[group.ready[0]])
		account, err := o.account(leader.Config.AccountID)
		if err != nil || account.Revision != leader.Revision {
			// Stop this collection only. Other batches and healthy downloads run on.
			doc := o.doc
			doc.Jobs = append([]OrganizeJob(nil), doc.Jobs...)
			for _, i := range group.ready {
				job := cloneOrganizeJob(doc.Jobs[i])
				job.Status, job.Message = "interrupted", "原任务主账号或凭据已变化，已保留文件；请核对账号后继续原任务"
				doc.Jobs[i] = job
			}
			if err := o.saveLocked(doc); err != nil {
				return err
			}
			continue
		}
		itemCount := 0
		for _, i := range group.ready {
			itemCount += len(o.doc.Jobs[i].Items)
		}
		leader.Items = make([]OrganizeItem, 0, itemCount)
		leader.Download.Share.SourceDirs = nil
		leader.Download.Share.Directory = "Cinema115-" + leader.ID
		leader.Download.Share.LeaderID = leader.ID
		// Every bound path remains below this original download parent, while
		// per-file ParentID retains the exact source directory for the engine.
		leader.Config.SourceCID, leader.Config.SourcePath = leader.Download.ParentCID, leader.Download.ParentPath
		leader.Status, leader.Message = "queued", fmt.Sprintf("合并 %d 条已就绪磁力，等待统一整理与分享；晚到资源将自动补批", len(group.ready))
		doc := o.doc
		doc.Jobs = append([]OrganizeJob(nil), doc.Jobs...)
		for _, i := range group.ready {
			member := cloneOrganizeJob(doc.Jobs[i])
			previous := member.Download.Share.Directory
			for j := range member.Items {
				item := &member.Items[j]
				if item.Directory != previous && !strings.HasPrefix(item.Directory, previous+"/") {
					return fmt.Errorf("分享批次的原定目录不一致")
				}
				item.Directory = leader.Download.Share.Directory + strings.TrimPrefix(item.Directory, previous)
				item.TargetPath = path.Join(member.Config.TargetPath, item.Directory, item.Name)
			}
			leader.Items = append(leader.Items, member.Items...)
			leader.Download.Share.SourceDirs = append(leader.Download.Share.SourceDirs, member.Download.Share.SourceDirs...)
			member.Download.Share.Directory = leader.Download.Share.Directory
			member.Download.Share.LeaderID = leader.ID
			member.Status, member.Message = leader.Status, leader.Message
			doc.Jobs[i] = member
		}
		for _, i := range group.ready {
			if doc.Jobs[i].ID == leader.ID {
				doc.Jobs[i] = leader
				break
			}
		}
		if err := o.saveLocked(doc); err != nil {
			return err
		}
		pending := newDownloadPending(leader, account)
		pending.ready, pending.continuing = true, true
		pending.waitUntil = now.Add(24 * time.Hour)
		s.live[leader.ID] = pending
	}
	return nil
}

// Member cards follow their original batch; only its leader owns the share
// receipt and recovery. Copy before updating so a failed save changes no state.
func updateDownloadShareMembers(doc *organizeDocument, leader OrganizeJob) {
	if leader.Download == nil || leader.Download.Share == nil || leader.Download.Share.LeaderID != leader.ID {
		return
	}
	files := make(map[string]OrganizeItem, len(leader.Items))
	for _, item := range leader.Items {
		files[item.File.ID] = item
	}
	for i, member := range doc.Jobs {
		if member.ID == leader.ID || member.Download == nil || member.Download.Share == nil || member.Download.Share.LeaderID != leader.ID {
			continue
		}
		member = cloneOrganizeJob(member)
		member.Status, member.Message, member.UpdatedAt = leader.Status, leader.Message, leader.UpdatedAt
		member.Download.Share.HandedOff = leader.Download.Share.HandedOff
		for j, item := range member.Items {
			if result, ok := files[item.File.ID]; ok {
				member.Items[j] = result
			}
		}
		doc.Jobs[i] = member
	}
}
