package service

import (
	"context"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
)

var ErrCloudCoordinationInput = errors.New("invalid cloud download coordination identity")

type cloudResourceKey struct {
	protocol string
	hash     string
}

type cloudUIDWork struct {
	gate      chan struct{}
	resources map[cloudResourceKey]struct{}
}

// CloudDownloadCoordinator is shared by manual and automatic entry points.
// It stores identities only, never credentials, directory paths or receipts.
// A UID runs one operation at a time; duplicate resources fail immediately,
// including while that resource is waiting for another operation on the UID.
// The zero value is ready for use. Assemble one instance for the application.
type CloudDownloadCoordinator struct {
	mu   sync.Mutex
	uids map[string]*cloudUIDWork
}

func cloudCoordinationIdentity(uid, protocol, hash string) (string, cloudResourceKey, error) {
	n, err := strconv.ParseUint(uid, 10, 64)
	if err != nil || n == 0 {
		return "", cloudResourceKey{}, ErrCloudCoordinationInput
	}
	key := cloudResourceKey{protocol: protocol, hash: strings.ToLower(hash)}
	size := 40
	switch protocol {
	case "magnet", "bt_v1":
		key.protocol = "bt_v1"
	case "ed2k":
		size = 32
	default:
		return "", cloudResourceKey{}, ErrCloudCoordinationInput
	}
	if len(key.hash) != size {
		return "", cloudResourceKey{}, ErrCloudCoordinationInput
	}
	if _, err := hex.DecodeString(key.hash); err != nil {
		return "", cloudResourceKey{}, ErrCloudCoordinationInput
	}
	return strconv.FormatUint(n, 10), key, nil
}

// Acquire reserves a resource and waits, with cancellation, for the UID gate.
// Reservation is bookkeeping under mu, not a resource lock held while waiting.
// The returned release must cover lookup/write/result inspection, but must not
// span a download's lifetime or a database transaction waiting on network I/O.
func (c *CloudDownloadCoordinator) Acquire(ctx context.Context, uid, protocol, hash string) (release func(), err error) {
	if c == nil {
		return nil, ErrCloudCoordinationInput
	}
	uid, key, err := cloudCoordinationIdentity(uid, protocol, hash)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	work := c.uids[uid]
	if work == nil {
		work = &cloudUIDWork{gate: make(chan struct{}, 1), resources: make(map[cloudResourceKey]struct{}, 1)}
		if c.uids == nil {
			c.uids = make(map[string]*cloudUIDWork)
		}
		c.uids[uid] = work
	}
	if _, exists := work.resources[key]; exists {
		c.mu.Unlock()
		return nil, ErrCloudTaskBusy
	}
	work.resources[key] = struct{}{}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		c.mu.Lock()
		c.removeLocked(uid, work, key)
		c.mu.Unlock()
		return nil, ctx.Err()
	case work.gate <- struct{}{}:
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			c.mu.Lock()
			<-work.gate
			c.removeLocked(uid, work, key)
			c.mu.Unlock()
		})
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func (c *CloudDownloadCoordinator) removeLocked(uid string, work *cloudUIDWork, key cloudResourceKey) {
	delete(work.resources, key)
	if len(work.resources) == 0 {
		delete(c.uids, uid)
	}
}
