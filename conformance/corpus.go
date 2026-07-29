// Package conformance replays the Rift SDK conformance corpus.
//
// The corpus is the engine-canonical definition of DSL ↔ engine parity: every official SDK
// replays it, and a fixture the typed DSL cannot express is a red build. This package holds the
// loading and normalisation logic; the assertions live in the test file beside it.
//
// See sdk-conformance/README.md in the engine repo for the normative replay contract.
package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvCorpusDir names an extracted sdk-conformance directory (the one containing manifest.json).
const EnvCorpusDir = "RIFT_CORPUS_DIR"

// Manifest is the corpus index.
type Manifest struct {
	SchemaVersion int       `json:"schemaVersion"`
	EngineVersion string    `json:"engineVersion"`
	Fixtures      []Fixture `json:"fixtures"`
}

// Fixture is one entry in the manifest.
type Fixture struct {
	// File is the fixture path relative to the corpus root, e.g. "corpus/imposters/01-basic-rest.json".
	File string `json:"file"`
	Port uint16 `json:"port"`
	Name string `json:"name"`
	// Requires names capability gates from the closed set
	// ["injection", "proxy", "redis", "https", "shell"]. A lane may skip a fixture only when it
	// lacks a capability the fixture declares — never for any other reason.
	Requires  []string `json:"requires"`
	HasVerify bool     `json:"hasVerify"`
}

// Verify is a fixture's `_verify` annotation: a transcript the engine must reproduce.
type Verify struct {
	Sequence []VerifyStep `json:"sequence"`
}

// VerifyStep is one request/expectation pair.
type VerifyStep struct {
	Request VerifyRequest `json:"request"`
	Expect  VerifyExpect  `json:"expect"`
}

// VerifyRequest describes the request to send. Only path is required.
type VerifyRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    any               `json:"body"`
}

// VerifyExpect is what the response must satisfy.
type VerifyExpect struct {
	Status       int    `json:"status"`
	BodyContains string `json:"bodyContains"`
}

// Corpus is a loaded conformance corpus.
type Corpus struct {
	// Root is the directory holding manifest.json.
	Root     string
	Manifest Manifest
}

// Load finds and reads a corpus.
//
// Resolution order: an explicit dir, then $RIFT_CORPUS_DIR, then a sibling checkout of the
// engine repo — the last so a contributor with both repos cloned needs no setup. CI extracts
// the published sdk-conformance-<version>.tar.gz and sets the environment variable.
func Load(explicit string) (*Corpus, error) {
	root, err := locate(explicit)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json")) //nolint:gosec // path is resolved above
	if err != nil {
		return nil, fmt.Errorf("read corpus manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse corpus manifest: %w", err)
	}
	if m.SchemaVersion != 1 {
		return nil, fmt.Errorf("corpus schemaVersion %d is not supported (this SDK understands 1)",
			m.SchemaVersion)
	}
	return &Corpus{Root: root, Manifest: m}, nil
}

func locate(explicit string) (string, error) {
	candidates := []string{explicit, os.Getenv(EnvCorpusDir)}
	// A sibling engine checkout, relative to this package's directory during `go test`.
	candidates = append(candidates,
		filepath.Join("..", "..", "rift", "sdk-conformance"),
		filepath.Join("..", "sdk-conformance"),
	)

	var tried []string
	for _, c := range candidates {
		if c == "" {
			continue
		}
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		tried = append(tried, abs)
		if st, err := os.Stat(filepath.Join(abs, "manifest.json")); err == nil && !st.IsDir() {
			return abs, nil
		}
	}
	return "", fmt.Errorf(
		"no sdk-conformance corpus found\n  fix: set %s to an extracted sdk-conformance-<version>/\n  searched:\n    %s",
		EnvCorpusDir, strings.Join(tried, "\n    "))
}

// ReadFixture loads one fixture's raw JSON, with relative data paths absolutised.
//
// Fixture paths like "data/products.csv" are relative to corpus/, and the replay contract says
// the replayer's working directory must be corpus/. An embedded lane shares its working
// directory with the whole test binary and cannot chdir without racing every other test, so the
// contract's stated alternative applies: absolutise at load time instead.
func (c *Corpus) ReadFixture(f Fixture) ([]byte, error) {
	path := filepath.Join(c.Root, filepath.FromSlash(f.File))
	raw, err := os.ReadFile(path) //nolint:gosec // path derives from the manifest under Root
	if err != nil {
		return nil, fmt.Errorf("read fixture %s: %w", f.File, err)
	}

	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse fixture %s: %w", f.File, err)
	}
	corpusDir := filepath.Join(c.Root, "corpus")
	doc = absolutise(doc, corpusDir)

	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("re-encode fixture %s: %w", f.File, err)
	}
	return out, nil
}

// absolutise rewrites corpus-relative file references to absolute paths.
//
// It only touches strings that begin with a known corpus subdirectory, so an ordinary body or
// path value that happens to contain a slash is left alone. Being conservative matters: a
// false positive would silently corrupt a fixture's meaning.
func absolutise(v any, corpusDir string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = absolutise(val, corpusDir)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = absolutise(val, corpusDir)
		}
		return t
	case string:
		for _, prefix := range []string{"data/", "fixtures/"} {
			if strings.HasPrefix(t, prefix) {
				return filepath.Join(corpusDir, filepath.FromSlash(t))
			}
		}
		return t
	default:
		return v
	}
}

// Capabilities is the set a lane can satisfy.
type Capabilities map[string]bool

// Missing returns the capabilities a fixture requires that the lane lacks. A fixture may be
// skipped only when this is non-empty — the replay contract permits no other reason.
func (c Capabilities) Missing(f Fixture) []string {
	var missing []string
	for _, req := range f.Requires {
		if !c[req] {
			missing = append(missing, req)
		}
	}
	return missing
}
