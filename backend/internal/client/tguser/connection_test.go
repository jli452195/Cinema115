package tguser

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

func TestTemporaryConnectionKeepsOtherBorrowersAndClosesAfterLast(t *testing.T) {
	c := testClient(t)
	var starts, stops atomic.Int32
	c.runOperation = func(ctx context.Context, operation func(context.Context, *telegram.Client) error) error {
		starts.Add(1)
		defer stops.Add(1)
		return operation(ctx, telegram.NewClient(1, "test", telegram.Options{}))
	}
	firstEntered, secondEntered, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	firstCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		first <- c.run(firstCtx, func(ctx context.Context, _ *telegram.Client) error {
			close(firstEntered)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	<-firstEntered
	go func() {
		second <- c.run(context.Background(), func(ctx context.Context, _ *telegram.Client) error {
			close(secondEntered)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-release:
				return nil
			}
		})
	}()
	<-secondEntered
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) || stops.Load() != 0 {
		t.Fatal("one caller closed another caller's connection", err)
	}
	close(release)
	if err := <-second; err != nil || starts.Load() != 1 || stops.Load() != 1 {
		t.Fatal("last caller did not join the sole temporary client", err, starts.Load(), stops.Load())
	}
	if err := c.run(context.Background(), func(context.Context, *telegram.Client) error { return nil }); err != nil || starts.Load() != 2 || stops.Load() != 2 {
		t.Fatal("idle temporary connection stayed alive or could not reopen", err)
	}
}

func TestTemporaryConnectionStartupCancellationAndClose(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "close"}[shutdown], func(t *testing.T) {
			c := testClient(t)
			started, stopped := make(chan struct{}), make(chan struct{})
			c.runOperation = func(ctx context.Context, _ func(context.Context, *telegram.Client) error) error {
				close(started)
				defer close(stopped)
				<-ctx.Done()
				return ctx.Err()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- c.Check(ctx) }()
			<-started
			if shutdown {
				c.Close()
			} else {
				cancel()
			}
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal("startup cancellation was lost", err)
			}
			select {
			case <-stopped:
			default:
				t.Fatal("temporary startup outlived its last borrower")
			}
		})
	}
}

func TestPickerRequestsWaitIndependentlyAndRespectCancellation(t *testing.T) {
	c, f := relayClient(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.list = func(*tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error) {
		once.Do(func() { close(entered) })
		<-release
		return fullChannels(broadcast(100)), nil
	}
	done := make(chan error, 1)
	go func() { _, err := c.ListChannels(context.Background(), ""); done <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.ListChannels(ctx, ""); !errors.Is(err, context.DeadlineExceeded) {
		close(release)
		t.Fatal("same picker did not wait within caller deadline", err)
	}
	if err := c.Check(context.Background()); err != nil {
		close(release)
		t.Fatal("picker blocked an unrelated account check", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestConfigChangeWaitsForQueuedBotJobs(t *testing.T) {
	c, transport := transportClient(t)
	release, entered := make(chan struct{}), make(chan struct{})
	var once sync.Once
	transport.reply = func(id, poll int) []*tg.Message {
		once.Do(func() { close(entered) })
		<-release
		return []*tg.Message{{ID: id + 1, Message: "没有找到资源"}}
	}
	first, queued := make(chan error, 1), make(chan error, 1)
	go func() { _, err := c.Search(context.Background(), "first", false); first <- err }()
	<-entered
	go func() { _, err := c.Search(context.Background(), "queued", false); queued <- err }()
	searchEventually(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.queue) == 1 })
	changed := make(chan error, 1)
	go func() {
		changed <- c.Save(context.Background(), Config{APIID: 123, APIHash: strings.Repeat("a", 32), Bot: "replacement_bot"})
	}()
	select {
	case err := <-changed:
		close(release)
		t.Fatal("configuration changed before old Bot jobs completed", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-queued; err != nil {
		t.Fatal("queued Bot job crossed configuration change", err)
	}
	if err := <-changed; err != nil || c.View().Bot != "replacement_bot" || transport.sentCount() != 2 {
		t.Fatal("queued jobs failed to release configuration change", err)
	}
}

func TestFailedRelayStartupDoesNotBlockAnIndependentRead(t *testing.T) {
	c, f := relayClient(t)
	store := seedRelay(c)
	c.relayPeers = relaySettings{}
	started, release := make(chan struct{}), make(chan struct{})
	var connections, active, maxActive atomic.Int32
	original := c.runOperation
	c.runOperation = func(ctx context.Context, operation func(context.Context, *telegram.Client) error) error {
		n := active.Add(1)
		defer active.Add(-1)
		if n > maxActive.Load() {
			maxActive.Store(n)
		}
		if connections.Add(1) == 1 {
			close(started)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-release:
			}
		}
		return original(ctx, operation)
	}
	relayDone, readDone := make(chan error, 1), make(chan error, 1)
	go func() {
		relayDone <- c.RunRelay(context.Background(), store, func(context.Context, int64, *tg.Message) error { return nil })
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { readDone <- c.Check(ctx) }()
	close(release)
	var detail *OperationError
	if err := <-relayDone; !errors.As(err, &detail) || detail.Code != "selection_required" {
		t.Fatal("relay fixture did not reject missing selections", err)
	}
	if err := <-readDone; err != nil {
		t.Fatal("an independent read inherited the relay's startup failure", err)
	}
	if connections.Load() != 2 || maxActive.Load() != 1 || f.starts.Load() != 2 {
		t.Fatal("fallback opened simultaneous session writers", connections.Load(), maxActive.Load())
	}
}

func TestCachedTelegramDataDoesNotWaitForAccountWriterOrRemoteSearch(t *testing.T) {
	c, calls, _, expected := avatarTestClient(t, true, false, nil)
	if _, _, err := c.RelayAvatar(context.Background(), "self", "123"); err != nil {
		t.Fatal(err)
	}
	c.channels = channelSettings{OwnerID: 42}
	c.channelSearches = map[string]*channelSearch{"cached": {key: "cached", ownerID: 42, fingerprint: channelFingerprint(c.channels), expires: time.Now().Add(time.Minute), result: ChannelSearchResult{Items: []ChannelResource{{ID: "saved"}}}}}
	hold, err := c.read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(hold) }
	defer release()
	writerDone := make(chan error, 1)
	go func() {
		unlock, err := c.exclusive(context.Background())
		if err == nil {
			unlock()
		}
		writerDone <- err
	}()
	searchEventually(t, func() bool {
		if c.lifecycle.TryAcquire(1) {
			c.lifecycle.Release(1)
			return false
		}
		return true
	})
	if !c.channelSearchGate.TryAcquire(1) {
		t.Fatal("unexpected remote search fixture")
	}
	defer c.channelSearchGate.Release(1)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if data, _, err := c.RelayAvatar(ctx, "self", "123"); err != nil || string(data) != string(expected) || calls.Load() != 1 {
		t.Fatal("cached avatar waited behind account writer", err)
	}
	if result, err := c.SearchChannels(ctx, "cached", "", false); err != nil || !result.Cached || len(result.Items) != 1 {
		t.Fatal("cached channel result waited behind account writer or search", err)
	}
	release()
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	c.Close()
	if _, _, err := c.RelayAvatar(context.Background(), "self", "123"); !errors.Is(err, ErrLogin) {
		t.Fatal("closed client returned a cached avatar", err)
	}
	if _, err := c.SearchChannels(context.Background(), "cached", "", false); !errors.Is(err, context.Canceled) {
		t.Fatal("closed client returned cached channel data", err)
	}
}

func TestTemporaryTransportFailureCancelsExistingBorrowers(t *testing.T) {
	c := testClient(t)
	fail, entered := make(chan struct{}), make(chan struct{})
	c.runOperation = func(ctx context.Context, operation func(context.Context, *telegram.Client) error) error {
		callbackCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- operation(callbackCtx, telegram.NewClient(1, "test", telegram.Options{})) }()
		select {
		case <-ctx.Done():
		case <-fail:
		}
		cancel()
		<-done
		return errors.New("mock transport disconnected")
	}
	done := make(chan error, 1)
	go func() {
		done <- c.run(context.Background(), func(ctx context.Context, _ *telegram.Client) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	<-entered
	close(fail)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("borrower lost transport cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("borrower outlived failed transport")
	}
}

func TestCanceledSavesDoNotCommitAfterWaiting(t *testing.T) {
	c, f := channelClient(t)
	f.list = func(*tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error) {
		return fullChannels(broadcast(100)), nil
	}
	page, err := c.ListChannels(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	hold, err := c.read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	err = c.Save(ctx, Config{APIID: 123, Bot: "replacement_bot"})
	cancel()
	hold()
	if !errors.Is(err, context.DeadlineExceeded) || c.View().Bot != "" {
		t.Fatal("canceled configuration save committed after its request", err)
	}
	unlock, err := c.acquire(context.Background(), c.channelPicker, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Millisecond)
	err = c.SaveChannels(ctx, page.SelectionToken, []string{"100"})
	cancel()
	unlock()
	if !errors.Is(err, context.DeadlineExceeded) || len(c.ChannelConfig().Channels) != 0 {
		t.Fatal("canceled channel save committed after its request", err)
	}
}

func TestLogoutCancelsRelayBeforeStartupIsReady(t *testing.T) {
	for _, stage := range []string{"transport", "ready_callback"} {
		t.Run(stage, func(t *testing.T) {
			c, _ := relayClient(t)
			c.config.Bot = testBotUsername
			store := seedRelay(c)
			started := make(chan struct{})
			original := c.runOperation
			var starts atomic.Int32
			c.runOperation = func(ctx context.Context, operation func(context.Context, *telegram.Client) error) error {
				if starts.Add(1) == 1 && stage == "transport" {
					close(started)
					<-ctx.Done()
					return ctx.Err()
				}
				return original(ctx, operation)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- c.RunRelay(ctx, store, func(ctx context.Context, _ int64, message *tg.Message) error {
					if stage == "ready_callback" && message == nil {
						close(started)
						<-ctx.Done()
					}
					return nil
				})
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("relay startup did not begin")
			}
			c.mu.Lock()
			connection := c.relayConnection
			c.mu.Unlock()
			canceled, stop := context.WithCancel(ctx)
			stop()
			if _, err := c.exclusive(canceled); !errors.Is(err, context.Canceled) || connection.ctx.Err() != nil {
				t.Fatal("an already canceled writer interrupted relay startup", err)
			}
			hold, err := c.read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var once sync.Once
			releaseRead := func() { once.Do(hold) }
			defer releaseRead()
			readDone, botDone, queuedDone := make(chan error, 1), make(chan error, 1), make(chan error, 1)
			go func() { readDone <- c.Check(ctx) }()
			go func() { _, err := c.Search(ctx, "startup", false); botDone <- err }()
			searchEventually(t, func() bool {
				c.mu.Lock()
				defer c.mu.Unlock()
				job := c.searches["search:startup"]
				return job != nil && job.started
			})
			go func() { _, err := c.Search(ctx, "queued", false); queuedDone <- err }()
			searchEventually(t, func() bool {
				c.mu.Lock()
				defer c.mu.Unlock()
				return len(c.queue) == 1
			})
			// Both ordinary reads must be waiting on this unready connection.
			select {
			case err := <-readDone:
				t.Fatal("account check did not wait for relay startup", err)
			case err := <-botDone:
				t.Fatal("Bot job did not wait for relay startup", err)
			case <-time.After(10 * time.Millisecond):
			}
			logoutCtx, stop := context.WithTimeout(ctx, 250*time.Millisecond)
			defer stop()
			logoutDone := make(chan error, 1)
			go func() { logoutDone <- c.Disconnect(logoutCtx) }()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal("relay startup did not stop", err)
			}
			// This reader already owns its lease but enters run only after the
			// relay has exited. The pending writer must still prevent a restart.
			if err := c.run(ctx, func(context.Context, *telegram.Client) error {
				t.Error("late reader dispatched during account change")
				return nil
			}); !errors.Is(err, context.Canceled) {
				t.Fatal("late reader lost the canceled relay placeholder", err)
			}
			releaseRead()
			if err := <-logoutDone; err != nil {
				t.Fatal("logout waited for reads blocked by relay initialization", err)
			}
			for _, pending := range []chan error{readDone, botDone, queuedDone} {
				if err := <-pending; !errors.Is(err, context.Canceled) {
					t.Fatal("an old read survived explicit relay startup cancellation", err)
				}
			}
			select {
			case <-connection.ready:
				t.Fatal("canceled relay startup published ready")
			default:
			}
			if starts.Load() != 2 {
				t.Fatal("an old read reopened a connection before logout", starts.Load())
			}
			if c.View().HasSession || c.View().Authorized || c.RelayConnected() {
				t.Fatal("logout retained a connection or restored old identity")
			}
		})
	}
}

func TestCanceledAccountWritersReleaseRelayStartupPlaceholder(t *testing.T) {
	for _, scenario := range []string{"after_exit", "during_exit", "another_writer"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := relayClient(t)
			store := seedRelay(c)
			started, finish := make(chan struct{}), make(chan struct{})
			var finishOnce sync.Once
			finishRelay := func() { finishOnce.Do(func() { close(finish) }) }
			defer finishRelay()
			original := c.runOperation
			var starts atomic.Int32
			c.runOperation = func(ctx context.Context, operation func(context.Context, *telegram.Client) error) error {
				if starts.Add(1) == 1 {
					close(started)
					<-ctx.Done()
					<-finish
					return ctx.Err()
				}
				return original(ctx, operation)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			relayDone := make(chan error, 1)
			go func() {
				relayDone <- c.RunRelay(ctx, store, func(context.Context, int64, *tg.Message) error { return nil })
			}()
			<-started
			c.mu.Lock()
			connection := c.relayConnection
			c.mu.Unlock()
			hold, err := c.read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var holdOnce sync.Once
			releaseRead := func() { holdOnce.Do(hold) }
			defer releaseRead()
			firstCtx, cancelFirst := context.WithCancel(ctx)
			defer cancelFirst()
			firstDone := make(chan error, 1)
			go func() {
				release, err := c.exclusive(firstCtx)
				if release != nil {
					release()
				}
				firstDone <- err
			}()
			searchEventually(t, func() bool {
				c.mu.Lock()
				defer c.mu.Unlock()
				return connection.writers == 1
			})
			secondDone := make(chan error, 1)
			if scenario == "another_writer" {
				go func() {
					release, err := c.exclusive(ctx)
					if err == nil {
						defer release()
						err = c.run(ctx, c.status)
					}
					secondDone <- err
				}()
				searchEventually(t, func() bool {
					c.mu.Lock()
					defer c.mu.Unlock()
					return connection.writers == 2
				})
			}
			if scenario == "after_exit" {
				finishRelay()
				<-relayDone
			}
			cancelFirst()
			if err := <-firstDone; !errors.Is(err, context.Canceled) {
				t.Fatal("canceled writer did not release its startup reference", err)
			}
			if scenario != "after_exit" {
				finishRelay()
				<-relayDone
			}
			c.mu.Lock()
			retained := c.relayConnection == connection
			c.mu.Unlock()
			if retained != (scenario == "another_writer") {
				t.Fatal("startup placeholder did not follow remaining writers", retained)
			}
			releaseRead()
			if scenario == "another_writer" {
				if err := <-secondDone; err != nil {
					t.Fatal("remaining writer could not join and replace startup", err)
				}
			} else if err := c.Check(ctx); err != nil {
				t.Fatal("canceled writer left a permanent closing placeholder", err)
			}
			c.mu.Lock()
			clean := c.relayConnection == nil && connection.writers == 0
			c.mu.Unlock()
			if !clean || starts.Load() != 2 {
				t.Fatal("writer completion retained startup state or duplicated clients", starts.Load())
			}
		})
	}
}
