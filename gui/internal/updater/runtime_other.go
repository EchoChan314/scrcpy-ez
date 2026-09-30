//go:build !windows

package updater

import (
	"fmt"
	"os"
)

func lockFile(path string) (*os.File, error) { return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600) }
func Launch(Plan) error                      { return fmt.Errorf("应用内安装仅支持 Windows") }
func RunIfRequested([]string) bool           { return false }
func NotifyHealthy([]string, string)         {}
func RecoverIfNeeded(string) bool            { return false }
func replaceFile(src, dst string) error      { return os.Rename(src, dst) }
