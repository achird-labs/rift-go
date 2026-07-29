//go:build darwin || linux || freebsd

package riftembed

import (
	"fmt"

	"github.com/ebitengine/purego"
)

// libSuffixes are the platform's shared-library file names, most specific first.
func libSuffixes() []string {
	return platformLibNames()
}

// dlopen loads the shared library at path.
func dlopen(path string) (uintptr, error) {
	lib, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return 0, fmt.Errorf("dlopen %s: %w", path, err)
	}
	return lib, nil
}

// dlclose releases the library. Errors are advisory: the process is usually exiting.
func dlclose(lib uintptr) error {
	return purego.Dlclose(lib)
}

// registerOne binds a single symbol. purego panics when a symbol is missing, which would
// surface as an opaque crash inside library loading; converting it to an error lets the caller
// report which symbol an outdated library is missing.
func registerOne(lib uintptr, fn any, name string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("symbol %s: %v (library too old for C-ABI v%d?)", name, r, abiVersion)
		}
	}()
	purego.RegisterLibFunc(fn, lib, name)
	return nil
}
