package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/animeresource"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type AnimeRSSReader interface {
	ReadSource(context.Context, *AnimeSourceBudget, AnimeSourceInput) (AnimeSourceResult, error)
}

// AnimeDiscoveryRunner owns only bounded RSS discovery and resource metadata
// verification. Download submission/status/files have separate later workers.
type AnimeDiscoveryRunner struct {
	Admin    *AnimeAdminService
	Feeds    AnimeRSSReader
	now      func() time.Time
	interval time.Duration
}

func (r *AnimeDiscoveryRunner) timestamp() int64 {
	if r.now != nil {
		return r.now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

func (r *AnimeDiscoveryRunner) valid() bool {
	return r != nil && r.Admin != nil && r.Admin.Store != nil && r.Feeds != nil
}

func (s *AnimeAdminService) animeVerificationWakeSignal() chan struct{} {
	s.verificationWakeOnce.Do(func() { s.verificationWake = make(chan struct{}, 1) })
	return s.verificationWake
}

func (s *AnimeAdminService) wakeAnimeVerification() {
	select {
	case s.animeVerificationWakeSignal() <- struct{}{}:
	default:
	}
}

func (s *AnimeAdminService) animeDiscoveryWakeSignal() chan struct{} {
	s.discoveryWakeOnce.Do(func() { s.discoveryWake = make(chan struct{}, 1) })
	return s.discoveryWake
}
func (s *AnimeAdminService) wakeAnimeDiscovery() {
	select {
	case s.animeDiscoveryWakeSignal() <- struct{}{}:
	default:
	}
}

// Run owns both loops and waits for both on cancellation/error. A slow or broken
// RSS source cannot consume the verifier's capacity. Each lane has one worker,
// at most four operations per pass, and no detached request goroutines.
func (r *AnimeDiscoveryRunner) Run(ctx context.Context) error {
	if !r.valid() {
		return storage.ErrAnimeInvalid
	}
	group, runCtx := errgroup.WithContext(ctx)
	for _, lane := range []struct {
		tick func(context.Context) error
		wake <-chan struct{}
	}{{r.PollOnce, r.Admin.animeDiscoveryWakeSignal()}, {r.VerifyOnce, r.Admin.animeVerificationWakeSignal()}} {
		lane := lane
		group.Go(func() error {
			interval := r.interval
			if interval <= 0 {
				interval = 5 * time.Second
			}
			for {
				if err := runCtx.Err(); err != nil {
					return nil
				}
				if err := lane.tick(runCtx); err != nil && runCtx.Err() == nil {
					return err
				}
				timer := time.NewTimer(interval)
				select {
				case <-runCtx.Done():
					timer.Stop()
					return nil
				case <-timer.C:
				case <-lane.wake:
					timer.Stop()
				}
			}
		})
	}
	return group.Wait()
}

func (r *AnimeDiscoveryRunner) PollOnce(ctx context.Context) error {
	if !r.valid() {
		return storage.ErrAnimeInvalid
	}
	for i := 0; i < 4; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		poll, found, err := r.Admin.Store.ReserveAnimeSourcePoll(ctx, r.timestamp())
		if err != nil {
			return fmt.Errorf("reserve anime RSS read: %w", err)
		}
		if !found {
			return nil
		}
		if err := r.poll(ctx, poll); err != nil {
			return err
		}
	}
	return nil
}

func (r *AnimeDiscoveryRunner) poll(ctx context.Context, poll storage.AnimeSourcePoll) error {
	var readErr error
	if poll.IssueCode == "" {
		budget, err := NewAnimeSourceBudget(nil)
		if err != nil {
			return err
		}
		readCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		input := AnimeSourceInput{Source: animefeed.NewSource(poll.Source.Provider, *poll.Source.ItemID, *poll.Source.GroupID), Kind: "rss"}
		if poll.Initialize && poll.Source.Rules.InitialMode == "from_episode" {
			input.Kind, input.Take = "history", 2000
		}
		page, err := r.Feeds.ReadSource(readCtx, budget, input)
		cancel()
		readErr = err
		if err == nil {
			_, readErr = r.Admin.Store.CompleteAnimeSourcePoll(ctx, poll, page.SourcePage, r.timestamp())
			if readErr == nil {
				r.Admin.wakeAnimeVerification()
				return nil
			}
			if errors.Is(readErr, storage.ErrAnimeConflict) {
				return nil
			}
			if ctx.Err() == nil && !errors.Is(readErr, storage.ErrAnimeInvalid) && !errors.Is(readErr, animepreview.ErrInput) && !errors.Is(readErr, storage.ErrAnimeSettingsChanged) && !errors.Is(readErr, storage.ErrAnimeAccountChanged) {
				return fmt.Errorf("commit anime RSS observations: %w", readErr)
			}
		}
	}
	code, retry, unavailable := animeDiscoveryReadFailure(readErr)
	if errors.Is(readErr, animepreview.ErrInput) || errors.Is(readErr, storage.ErrAnimeInvalid) {
		code = "SOURCE_RESPONSE_INVALID"
	}
	if poll.IssueCode != "" {
		code, unavailable = poll.IssueCode, true
	}
	if errors.Is(readErr, storage.ErrAnimeSettingsChanged) || errors.Is(readErr, storage.ErrAnimeAccountChanged) {
		code, unavailable = "DOWNLOAD_TARGET_CHANGED", true
	}
	if ctx.Err() != nil {
		code, retry, unavailable = "POLL_CANCELLED", 0, false
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := r.Admin.Store.FailAnimeSourcePoll(writeCtx, poll, code, retry, unavailable, r.timestamp())
	if errors.Is(err, storage.ErrAnimeConflict) {
		return nil
	}
	return err
}

func animeDiscoveryReadFailure(err error) (string, int64, bool) {
	var response *mikan.ResponseError
	var site *animefeed.HTTPError
	var cooldown *MikanCooldownError
	status, retry := 0, int64(0)
	if errors.As(err, &response) {
		status = response.StatusCode
		if !response.RetryAt.IsZero() {
			retry = response.RetryAt.UnixMilli()
		}
	}
	if errors.As(err, &site) {
		status = site.Status
		if !site.RetryAt.IsZero() {
			retry = site.RetryAt.UnixMilli()
		}
	}
	if errors.As(err, &cooldown) {
		return "SOURCE_RATE_LIMITED", cooldown.Until.UnixMilli(), false
	}
	if status == 401 || status == 403 {
		return "SOURCE_UNAVAILABLE", 0, true
	}
	if status == 429 || status == 503 && retry > 0 {
		return "SOURCE_RATE_LIMITED", retry, false
	}
	if status == 0 && errors.Is(err, mikan.ErrBlocked) {
		return "SOURCE_UNAVAILABLE", 0, true
	}
	if errors.Is(err, mikan.ErrSourceBinding) {
		return "SOURCE_BINDING_CHANGED", 0, true
	}
	if errors.Is(err, mikan.ErrLimit) {
		return "SOURCE_READ_BUDGET", 0, false
	}
	return "SOURCE_READ_FAILED", retry, false
}

func (r *AnimeDiscoveryRunner) VerifyOnce(ctx context.Context) error {
	if !r.valid() {
		return storage.ErrAnimeInvalid
	}
	sessions := make(map[model.AnimeDownloadTarget]*AnimeDownloadSession, 4)
	for i := 0; i < 4; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		work, found, err := r.Admin.Store.ReserveAnimeDiscovery(ctx, r.timestamp())
		if err != nil {
			return fmt.Errorf("reserve anime resource verification: %w", err)
		}
		if !found {
			return nil
		}
		if err := r.verify(ctx, work, sessions); err != nil {
			return err
		}
	}
	// Yield after a bounded batch, then drain locally pending selections without
	// an extra five-second sleep. Persisted due times still gate retries.
	r.Admin.wakeAnimeVerification()
	return nil
}

func (r *AnimeDiscoveryRunner) verify(ctx context.Context, work storage.AnimeDiscoveryWork, sessions map[model.AnimeDownloadTarget]*AnimeDownloadSession) error {
	code, retryable := "RESOURCE_REVIEW_REQUIRED", false
	basis := work.Decision.Snapshot.Selection
	var err error
	if basis != nil {
		// Restore precisely the selected target, then require current settings
		// before a new resource claim. Defaults never retarget a pending choice.
		readCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		session := sessions[basis.Target]
		if session == nil {
			session, err = r.Admin.ResumeDownloadTarget(readCtx, basis.Target)
			if err == nil {
				sessions[basis.Target] = session
			}
		}
		if err == nil {
			err = r.Admin.Store.CheckAnimeDownloadSettings(readCtx, basis.Target)
		}
		if err == nil {
			_, err = r.Admin.ClaimDownload(readCtx, session, work.Decision.SourceID, work.Decision.ReleaseID, work.Decision.Generation, work.Observation)
		}
		cancel()
		if err == nil {
			code = ""
		}
		if errors.Is(err, animeresource.ErrRead) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, ErrAnimeDirectoryRead) {
			code, retryable = "RESOURCE_READ_FAILED", true
		}
		if errors.Is(err, storage.ErrAnimeAccountChanged) || errors.Is(err, storage.ErrAnimeSettingsChanged) || errors.Is(err, storage.ErrAnimeDirectoryRequired) {
			code = "DOWNLOAD_TARGET_CHANGED"
		}
		if err != nil && code == "RESOURCE_REVIEW_REQUIRED" && !animeDiscoveryReviewError(err) {
			// A database/infrastructure failure must leave the durable pending
			// choice recoverable; it is not evidence that a resource is invalid.
			return fmt.Errorf("claim anime discovered resource: %w", err)
		}
	}
	if ctx.Err() != nil {
		code, retryable = "DISCOVERY_CANCELLED", false
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	finishErr := r.Admin.Store.CompleteAnimeDiscovery(writeCtx, work, code, retryable, r.timestamp())
	if errors.Is(finishErr, storage.ErrAnimeConflict) {
		return nil
	}
	return finishErr
}

func animeDiscoveryReviewError(err error) bool {
	for _, known := range []error{storage.ErrAnimeConflict, storage.ErrAnimeInvalid, storage.ErrAnimeIdentity, storage.ErrAnimeSelectionWithdrawn, storage.ErrAnimeResourceTarget, storage.ErrAnimeResourceSource, animeresource.ErrInvalid, animeresource.ErrUnsupported, animeresource.ErrConflict} {
		if errors.Is(err, known) {
			return true
		}
	}
	var config *AnimeConfigError
	return errors.As(err, &config)
}
