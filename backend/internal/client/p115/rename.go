package p115

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// RenameEntry performs one mutation without automatic retries. Callers must
// verify the name by ID before treating an ambiguous response as success.
func (c *ShareClient) RenameEntry(ctx context.Context, id, name, cookie string) error {
	if !validDriveID(id, false) || strings.TrimSpace(name) == "" || len([]rune(name)) > 200 || strings.ContainsAny(name, "/\\\x00\r\n") {
		return fmt.Errorf("重命名参数无效")
	}
	_, err := c.requestEnvelope(ctx, http.MethodPost, c.WebURL+"/files/batch_rename", cookie, url.Values{"files_new_name[" + id + "]": {name}})
	if err != nil {
		return fmt.Errorf("115 重命名失败: %w", err)
	}
	return nil
}
