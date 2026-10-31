package tguser

import (
	"context"
	"errors"

	"github.com/gotd/td/telegram"
	"golang.org/x/sync/semaphore"
)

const lifecycleCapacity int64 = 1 << 30

// Reads may share one authenticated session; account changes wait for every
// active operation, including Bot jobs whose browser has already disconnected.
func (c *Client) read(ctx context.Context) (func(), error) {
	return c.acquire(ctx, c.lifecycle, 1)
}

func (c *Client) exclusive(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Readers waiting for relay startup cannot finish before it is canceled.
	// Stop only an unready relay before draining their lifecycle leases.
	var startup *relayConnection
	c.mu.Lock()
	if connection := c.relayConnection; connection != nil {
		select {
		case <-connection.ready:
		default:
			startup = connection
			connection.writers++
			connection.closing = true
			connection.cancel()
		}
	}
	c.mu.Unlock()
	defer func() {
		if startup == nil {
			return
		}
		c.mu.Lock()
		startup.writers--
		if startup.writers == 0 && c.relayConnection == startup {
			select {
			case <-startup.done:
				c.relayConnection = nil
			default: // RunRelay clears the last reference when it finishes.
			}
		}
		c.mu.Unlock()
	}()
	release, err := c.acquire(ctx, c.lifecycle, lifecycleCapacity)
	if err != nil || startup == nil {
		return release, err
	}
	select {
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	case <-startup.done:
	}
	// Until every old reader has drained, keep the canceled relay visible to
	// queued jobs and readers that acquired their lease before this writer.
	c.mu.Lock()
	if c.relayConnection == startup {
		c.relayConnection = nil
	}
	c.mu.Unlock()
	return release, nil
}

func (c *Client) acquire(ctx context.Context, gate *semaphore.Weighted, weight int64) (func(), error) {
	waitCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.workerContext, cancel)
	defer stop()
	defer cancel()
	if err := gate.Acquire(waitCtx, weight); err != nil {
		return nil, err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		gate.Release(weight)
		return nil, context.Canceled
	}
	return func() { gate.Release(weight) }, nil
}

// run is the only entry for ordinary MTProto operations. An existing relay is
// borrowed; otherwise overlapping operations share one temporary client. No
// connection remains after its last caller has left.
func (c *Client) run(ctx context.Context, operation func(context.Context, *telegram.Client) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return context.Canceled
		}
		if connection := c.relayConnection; connection != nil {
			c.mu.Unlock()
			started := false
			err := c.runShared(ctx, connection, func(ctx context.Context, client *telegram.Client) error {
				started = true
				return operation(ctx, client)
			})
			if !started && errors.Is(err, ErrBusy) {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-connection.done:
				}
				c.mu.Lock()
				closing := connection.closing
				c.mu.Unlock()
				if closing {
					return context.Canceled
				}
				continue // A relay that closed before dispatch cannot own this RPC.
			}
			return err
		}
		connection := c.temporaryConnection
		if connection != nil && connection.closing {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-connection.done:
				continue
			}
		}
		if connection == nil {
			runCtx, cancel := context.WithCancel(c.workerContext)
			connection = &relayConnection{ctx: runCtx, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{})}
			c.temporaryConnection = connection
			cfg := c.config
			c.workers.Add(1)
			go c.runTemporary(connection, cfg)
		}
		connection.borrowers++
		c.mu.Unlock()
		err := c.runShared(ctx, connection, operation)
		c.mu.Lock()
		connection.borrowers--
		last := connection.borrowers == 0
		if last {
			connection.closing = true
			connection.cancel()
		}
		c.mu.Unlock()
		if last {
			<-connection.done
		}
		return err
	}
}

func (c *Client) runTemporary(connection *relayConnection, cfg Config) {
	defer c.workers.Done()
	err := c.connect(connection.ctx, cfg, func(ctx context.Context, client *telegram.Client) error {
		c.mu.Lock()
		connection.client, connection.connected = client, true
		close(connection.ready)
		c.mu.Unlock()
		<-ctx.Done()
		return nil
	})
	connection.cancel()
	c.mu.Lock()
	connection.connected = false
	connection.err = err
	if c.temporaryConnection == connection {
		c.temporaryConnection = nil
	}
	close(connection.done)
	c.mu.Unlock()
}
