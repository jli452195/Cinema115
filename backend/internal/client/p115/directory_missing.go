package p115

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// DirectoryMissing only accepts the exact get_info missing-ID business error.
// Empty success, redirected listings, HTTP 404 and transport errors are not
// evidence of deletion. Successful metadata does not prove deletion either.
func (c *ShareClient) DirectoryMissing(ctx context.Context, id, cookie string) (bool, error) {
	if !validDriveID(id, false) {
		return false, fmt.Errorf("核对目录 ID 无效")
	}
	_, err := c.requestEnvelope(ctx, http.MethodGet, c.WebURL+"/files/get_info?"+url.Values{"file_id": {id}}.Encode(), cookie, nil)
	if errors.Is(err, ErrDriveFileMissing) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("核对目录是否删除: %w", err)
	}
	return false, nil
}
