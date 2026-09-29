package cli

import "errors"

// errNoFiles is returned by resolvePaths when the selection is legitimately empty (nothing
// changed): the caller reports it and exits 0 rather than falling back to a walk of the whole
// repository, which is what an empty paths slice means to engine.Run.
var errNoFiles = errors.New("no files to process")

// errOutsideGitNoPaths is returned by selectFiles when there is no git repository to diff against
// and no explicit paths were given: unlike errNoFiles (a legitimately empty selection inside git),
// this is a caller mistake -- there is no default to fall back to -- so resolvePaths' callers must
// treat it as a real failure, not a "nothing to do" no-op.
var errOutsideGitNoPaths = errors.New("outside a git repository, explicit paths are required")

// unstableFormatError is fmt's "did not converge" verdict: a result about the formatters, like
// `fmt --check` finding files to reformat, not a run that failed to run.
type unstableFormatError string

func (e unstableFormatError) Error() string { return string(e) }
