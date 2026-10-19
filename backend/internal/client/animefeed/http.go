package animefeed

import (
	"fmt"
	"time"
)

// HTTPError preserves a site's bounded retry hint without retaining its body.
type HTTPError struct {
	Site    string
	Status  int
	RetryAt time.Time
}

func (e *HTTPError) Error() string { return fmt.Sprintf("anime source %s HTTP %d", e.Site, e.Status) }
