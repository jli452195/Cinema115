package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"strm115/backend/internal/client/p115"
)

// ShareGenerationTimeout leaves room for source enumeration, asynchronous
// receiving and snapshot verification. Successful stages never wait out a budget.
const ShareGenerationTimeout = 20 * time.Minute

func transferWaitBudget(files int) time.Duration {
	if files > 1000 {
		return 10 * time.Minute
	}
	return 90 * time.Second
}

var (
	errTransferDirectoryChanging = errors.New("转存目录仍在变化")
	errTransferListingInvalid    = errors.New("转存目录数据无效")
)

type transferPageLister func(context.Context, string, int) (*p115.SharePage, error)

// transferPages buffers each directory before descending into children. Only
// an unstable directory is retried; completed siblings need not be reread.
// The cache belongs to one scan, never to a subsequent completion check: an
// apparently stable directory may still receive more files later.
func (w *ShareWorkflow) transferPages(list transferPageLister, verified ...map[string][]p115.ShareFile) transferPageLister {
	pages := make(map[generationSnapshotPageKey]*p115.SharePage)
	return func(ctx context.Context, cid string, offset int) (*p115.SharePage, error) {
		if offset == 0 {
			var first *p115.SharePage
			lastCount := 0
			read := func(ctx context.Context, id string, start int) (*p115.SharePage, error) {
				if start == 0 && first != nil {
					p := first
					first = nil
					return p, nil
				}
				p, err := list(ctx, id, start)
				if p != nil {
					lastCount = p.Count
				}
				return p, err
			}
			for attempt := 0; ; attempt++ {
				directory, err := readTransferDirectory(ctx, cid, read)
				if err == nil {
					if d := directoryMetrics(ctx, "transfer", cid); d != nil {
						d.FullReads++
					}
					if len(verified) > 0 {
						children := make([]p115.ShareFile, 0)
						for start := 0; ; {
							page := directory[start]
							children = append(children, page.Files...)
							start += len(page.Files)
							if start >= page.Count {
								break
							}
						}
						verified[0][cid] = children
					}
					for start, page := range directory {
						pages[generationSnapshotPageKey{CID: cid, Offset: start}] = page
					}
					break
				}
				if !errors.Is(err, errTransferDirectoryChanging) {
					return nil, err
				}
				// This error contains only locally constructed directory/page IDs,
				// not upstream bodies, credentials or signed URLs.
				reportGeneration(ctx, "scanning", err.Error()+"；丢弃本目录本轮分页，稍后重新核验")
				if attempt >= 2 {
					return nil, err
				}
				if d := directoryMetrics(ctx, "transfer", cid); d != nil {
					d.Retries++
				}
				if waitErr := waitTransfer(ctx, w.PollInterval*time.Duration(1<<attempt)); waitErr != nil {
					return nil, errors.Join(waitErr, err)
				}
				// Only a directory already observed changing and larger than one
				// page uses probes. Stable directories have no extra calls/delay.
				if lastCount > 100 {
					var probeErr error
					first, probeErr = w.probeTransferDirectory(ctx, cid, lastCount, list)
					if probeErr != nil {
						return nil, errors.Join(probeErr, err)
					}
				}
			}
		}
		key := generationSnapshotPageKey{CID: cid, Offset: offset}
		page, ok := pages[key]
		if !ok {
			return nil, fmt.Errorf("%w：目录 %s 缺少分页 %d", errTransferListingInvalid, cid, offset)
		}
		delete(pages, key)
		return page, nil
	}
}

// A count plateau is a scheduling hint, never a completion check. The last
// probe page is consumed by the following full read; no page is counted twice.
// Bounded probing also handles upstream counts that continue to fluctuate.
func (w *ShareWorkflow) probeTransferDirectory(ctx context.Context, cid string, previous int, list transferPageLister) (*p115.SharePage, error) {
	delay := w.PollInterval
	for probe := 0; ; probe++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := list(ctx, cid, 0)
		if d := directoryMetrics(ctx, "transfer", cid); d != nil {
			d.Probes++
		}
		if err != nil {
			return nil, err
		}
		if page == nil || page.Count < 0 || page.Count > 20000 {
			return nil, fmt.Errorf("%w：目录 %s 探测条目数无效", errTransferListingInvalid, cid)
		}
		if page.Count == previous || probe >= 3 {
			return page, nil
		}
		delay = min(15*time.Second, max(w.PollInterval, delay*2))
		reportGeneration(ctx, "scanning", fmt.Sprintf("目录 %s 仍在转入：条目数 %d 到 %d，%.1f 秒后只探测首页；稳定后完整核验", cid, previous, page.Count, delay.Seconds()))
		previous = page.Count
		if err := waitTransfer(ctx, delay); err != nil {
			return nil, err
		}
	}
}

func readTransferDirectory(ctx context.Context, cid string, list transferPageLister) (map[int]*p115.SharePage, error) {
	pages := make(map[int]*p115.SharePage)
	seen := make(map[string]int)
	count := -1
	for offset := 0; ; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(pages) >= 500 {
			return nil, fmt.Errorf("%w：目录 %s 超过 500 页", errTransferListingInvalid, cid)
		}
		page, err := list(ctx, cid, offset)
		if err != nil {
			return nil, err
		}
		if page == nil || page.Count < 0 || page.Count > 20000 {
			return nil, fmt.Errorf("%w：目录 %s 条目数无效或超过 20000", errTransferListingInvalid, cid)
		}
		if count >= 0 && page.Count != count {
			return nil, fmt.Errorf("%w：目录 %s，分页 %d，条目数 %d → %d", errTransferDirectoryChanging, cid, offset, count, page.Count)
		}
		count = page.Count
		if len(page.Files) == 0 && offset < count || len(page.Files) > count-offset {
			return nil, fmt.Errorf("%w：目录 %s，分页 %d 不完整（总数 %d，本页 %d）", errTransferDirectoryChanging, cid, offset, count, len(page.Files))
		}
		for _, f := range page.Files {
			id, err := strconv.ParseUint(f.ID, 10, 64)
			if err != nil || id == 0 {
				return nil, fmt.Errorf("%w：目录 %s，分页 %d 存在非法 ID", errTransferListingInvalid, cid, offset)
			}
			if previous, ok := seen[f.ID]; ok {
				return nil, fmt.Errorf("%w：目录 %s，分页 %d 与 %d 重复 ID %s", errTransferDirectoryChanging, cid, offset, previous, f.ID)
			}
			seen[f.ID] = offset
		}
		copyPage := *page
		copyPage.Files = append([]p115.ShareFile(nil), page.Files...)
		pages[offset] = &copyPage
		offset += len(page.Files)
		if offset == count {
			return pages, nil
		}
	}
}
