package main

import (
	"fmt"
	"path/filepath"
)

// instanceLockPath is the lock file AcquireInstanceLock uses for configPath.
func instanceLockPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), fmt.Sprintf(".%s.lock", filepath.Base(configPath)))
}
