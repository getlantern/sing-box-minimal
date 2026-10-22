//go:build (darwin || linux || windows) && !badlinkname

package oomprofile

import "errors"

// WriteFile needs linknames into runtime/pprof that the linker rejects
// unless built with -checklinkname=0, which badlinkname builds set.
func WriteFile(filePath string, name string) error {
	return errors.New("oom profile requires the badlinkname build tag")
}
