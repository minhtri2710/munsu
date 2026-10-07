package home

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicCreatePostPublish makes the no-replace publication durable after the
// target appears. Tests inject an error to pin that a published target is
// preserved when this final durability step fails.
var atomicCreatePostPublish = syncAtomicCreateDirectory

// AtomicCreate durably creates path with data and perm without replacing any
// existing entry. The parent directory must already exist; callers own its
// mode. If the final directory/write-through durability step fails, the target
// may already be published and is intentionally left untouched.
func AtomicCreate(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".home-create-*")
	if err != nil {
		return fmt.Errorf("home: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("home: secure temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("home: write temp: %w", err)
	}
	if err := syncTemp(tmp); err != nil {
		tmp.Close()
		return fmt.Errorf("home: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("home: close temp: %w", err)
	}
	if err := atomicCreateNoReplace(tmpPath, path); err != nil {
		return fmt.Errorf("home: publish without replacement: %w", err)
	}
	if err := atomicCreatePostPublish(filepath.Dir(path)); err != nil {
		return fmt.Errorf("home: sync published target: %w", err)
	}
	return nil
}
