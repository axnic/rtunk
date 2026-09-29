// Package install holds the one filesystem primitive shared by everything that installs into
// rtunk's download cache (pkg/cache/download's archive extraction, pkg/cache/runtime's package
// installs): publishing a finished scratch directory atomically.
package install
