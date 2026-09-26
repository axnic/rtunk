// Package install holds the one filesystem primitive shared by everything that installs into
// rtunk's download cache (pkg/trunk/download's archive extraction, pkg/trunk/runtime's package
// installs): publishing a finished scratch directory atomically.
package install

import "os"

// Finalize atomically publishes a completed install: tmpDir (scratch work done in a sibling
// directory of destDir, so this stays on one filesystem -- os.Rename requires that) is renamed
// into destDir only once every step has already succeeded. If destDir already exists, a
// concurrent or earlier caller won the race and finished first -- that's success, not a conflict:
// this caller's tmpDir is discarded and the winner's result is used as-is.
func Finalize(tmpDir, destDir string) error {
	if err := os.Rename(tmpDir, destDir); err != nil {
		if info, statErr := os.Stat(destDir); statErr == nil && info.IsDir() {
			_ = os.RemoveAll(tmpDir)
			return nil
		}
		return err
	}
	return nil
}
