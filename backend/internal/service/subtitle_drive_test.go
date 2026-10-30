package service

import (
	"context"
	"errors"
	"fmt"
	"strm115/backend/internal/client/p115"
)

type subtitleFakeDrive struct {
	*organizeFakeDrive
	uploads      int
	uploadErr    error
	beforeUpload func()
	noApply      bool
}

func (d *subtitleFakeDrive) DeleteEntries(context.Context, []string, string) error {
	return errors.New("unexpected delete during subtitle import")
}

func (d *subtitleFakeDrive) UploadSubtitle(ctx context.Context, parent, name string, data []byte, cookie string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.uploads++
	d.cookies = append(d.cookies, cookie)
	if d.beforeUpload != nil {
		d.beforeUpload()
	}
	if !d.noApply {
		d.dirs[parent] = append(d.dirs[parent], p115.ShareFile{ID: fmt.Sprint(9000 + d.uploads), Name: name, Size: int64(len(data))})
	}
	return d.uploadErr
}
