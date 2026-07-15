//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris

package backgrounddebate

import "os/exec"

func configureProcessTreeCancellation(_ *exec.Cmd) {
	// exec.CommandContext retains its default immediate-process cancellation.
}
