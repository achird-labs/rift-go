package riftembed

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/achird-labs/rift-go/rift"
)

// BuildInfo is the native library's build metadata, as rift_build_info reports it.
type BuildInfo struct {
	Version string `json:"version"`
	// Commit and BuiltAt are empty unless the library was stamped at build time.
	Commit  string `json:"commit"`
	BuiltAt string `json:"builtAt"`
	// Features are the compiled cargo features, e.g. "javascript". They say what the engine can
	// run, not which serve options it accepts; that is ServeOptions.
	Features []string `json:"features"`
	// ServeOptions lists the keys ServeAdmin's options may carry. It is nil on an engine older
	// than 0.17.0, which published no list.
	ServeOptions []string `json:"serveOptions"`
}

// preListServeOptions are the serve options every engine accepted before 0.17.0 started
// publishing ServeOptions (checked against the v0.14.0 and v0.16.0 sources). Those engines
// silently ignore any other key, so nothing else can be assumed supported on them.
var preListServeOptions = []string{
	"host", "port", "apiKey", "metricsPort", "configFile", "config", "allowInjection",
}

// SupportsServeOption reports whether the engine accepts the named serve option. On an engine
// that publishes no list, only the options that predate the list count as supported.
func (b BuildInfo) SupportsServeOption(name string) bool {
	if b.ServeOptions == nil {
		return slices.Contains(preListServeOptions, name)
	}
	return slices.Contains(b.ServeOptions, name)
}

func parseBuildInfo(raw string) (BuildInfo, error) {
	var info BuildInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return BuildInfo{}, fmt.Errorf("%w: rift_build_info is not valid JSON: %w",
			rift.ErrEngineUnavailable, err)
	}
	return info, nil
}

// checkServeOptions refuses any option in the marshalled options document that the engine does
// not advertise. An engine that does not know a key ignores it instead of rejecting it, because
// rejection only comes from an engine that already knows the field. Without this check, asking
// an old engine for requireAdminAuth would quietly serve an open admin plane.
func checkServeOptions(info BuildInfo, body []byte) error {
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(body, &sent); err != nil {
		return err
	}
	keys := make([]string, 0, len(sent))
	for k := range sent {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !info.SupportsServeOption(k) {
			return fmt.Errorf("%w: engine %s does not accept serve option %q; it would ignore it "+
				"rather than refuse it", rift.ErrVersionMismatch, info.Version, k)
		}
	}
	return nil
}
