package importer

import (
	"fmt"
	"os"
	"path/filepath"
)

// lockState uses atomic directory creation so it works without platform-specific
// syscalls. Never steal a lock: a slow live importer is indistinguishable from
// an abandoned lock without an OS-specific process lock.
func lockState(path string) (string, func(), error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", nil, err
	}
	path = filepath.Join(parent, filepath.Base(path))
	if resolved, e := filepath.EvalSymlinks(path); e == nil {
		path = resolved
	} else if !os.IsNotExist(e) {
		return "", nil, e
	}
	lock := path + ".lock"
	if err = os.Mkdir(lock, 0700); err != nil {
		if os.IsExist(err) {
			return "", nil, fmt.Errorf("state is locked: %s; another import may be running; remove this directory only after confirming no importer is active", lock)
		}
		return "", nil, fmt.Errorf("lock state: %w", err)
	}
	return path, func() { _ = os.Remove(lock) }, nil
}
