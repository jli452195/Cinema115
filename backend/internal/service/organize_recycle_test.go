package service

import (
	"context"
)

type cleanupFakeDrive struct {
	*organizeFakeDrive
	deleted      []string
	deleteErr    error
	applyOnError bool
	beforeDelete func()
	noDelete     bool
}

func (d *cleanupFakeDrive) DeleteEntries(ctx context.Context, ids []string, cookie string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.cookies = append(d.cookies, cookie)
	d.deleted = append(d.deleted, ids...)
	if d.beforeDelete != nil {
		d.beforeDelete()
	}
	if !d.noDelete && (d.deleteErr == nil || d.applyOnError) {
		for _, id := range ids {
			for parent, files := range d.dirs {
				for i, file := range files {
					if file.ID == id {
						d.dirs[parent] = append(files[:i], files[i+1:]...)
						break
					}
				}
			}
			delete(d.dirs, id)
		}
	}
	return d.deleteErr
}
