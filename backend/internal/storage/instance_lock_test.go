package storage

import "testing"

func TestInstanceLockExcludesSecondServer(t *testing.T) {
	dir := t.TempDir()
	first, err := LockInstance(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := LockInstance(dir); err == nil {
		second.Close()
		t.Fatal("second server acquired lock")
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := LockInstance(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
}
