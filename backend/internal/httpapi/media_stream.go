package httpapi

import (
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
)

// Share and directory STRM use the same original-file transport. Only the
// lookup depends on the source; media requests never carry account credentials.
func streamMedia(c *gin.Context, client *http.Client, link, name string, onReady func()) error {
	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, link, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", p115.PlaybackUserAgent)
	req.Header.Set("Accept-Encoding", "identity")
	for _, h := range []string{"Range", "If-Range"} {
		if v := c.GetHeader(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		return fmt.Errorf("媒体返回状态 %d", resp.StatusCode)
	}
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if v := resp.Header.Get(h); v != "" {
			c.Header(h, v)
		}
	}
	c.Header("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	c.Status(resp.StatusCode)
	// After committing media headers, callers must not append a JSON error even
	// if the very first body read fails.
	c.Writer.WriteHeaderNow()
	if onReady != nil {
		onReady()
	}
	if c.Request.Method == http.MethodHead {
		return nil
	}
	_, err = io.Copy(c.Writer, resp.Body)
	return err
}
