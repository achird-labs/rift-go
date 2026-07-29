//go:build windows

package riftembed

import (
	"fmt"
	"syscall"

	"github.com/ebitengine/purego"
)

// dlopen loads the DLL. purego binds against a Windows module handle the same way it binds
// against a dlopen handle, so the rest of the package is platform-independent.
func dlopen(path string) (uintptr, error) {
	h, err := syscall.LoadLibrary(path)
	if err != nil {
		return 0, fmt.Errorf("LoadLibrary %s: %w", path, err)
	}
	return uintptr(h), nil
}

func dlclose(lib uintptr) error {
	return syscall.FreeLibrary(syscall.Handle(lib))
}

func registerOne(lib uintptr, fn any, name string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("symbol %s: %v (library too old for C-ABI v%d?)", name, r, abiVersion)
		}
	}()
	purego.RegisterLibFunc(fn, lib, name)
	return nil
}
