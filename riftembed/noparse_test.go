package riftembed_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/achird-labs/rift-go/rift"
	"github.com/achird-labs/rift-go/riftembed"
)

// A config file whose body holds a literal "<%" is refused by the EJS preprocessor, and NoParse is
// the only way to load it verbatim.
func TestNoParseLoadsALiteralEJSTag(t *testing.T) {
	port := freeTCPPort(t)
	path := filepath.Join(t.TempDir(), "imposters.json")
	doc := fmt.Sprintf(`{"imposters":[{"port":%d,"protocol":"http","stubs":[`+
		`{"responses":[{"is":{"statusCode":200,"body":"<%% not a tag %%>"}}]}]}]}`, port)
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("preprocessed", func(t *testing.T) {
		_, err := startEngine(t).ServeAdmin(t.Context(), riftembed.ServeOptions{Host: "127.0.0.1", ConfigFile: path})
		if err == nil {
			t.Fatal("a config file with an unevaluated EJS tag loaded without NoParse")
		}
	})

	t.Run("verbatim", func(t *testing.T) {
		serve(t, startEngine(t), riftembed.ServeOptions{Host: "127.0.0.1", ConfigFile: path, NoParse: true})
		assertServes(t, port, "<% not a tag %>")
	})
}

func TestNoParseWithoutConfigFileIsRefusedBeforeTheEngine(t *testing.T) {
	eng := startEngine(t)
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err := eng.ServeAdmin(t.Context(), riftembed.ServeOptions{NoParse: true})
	if !errors.Is(err, rift.ErrInvalidDefinition) {
		t.Errorf("err = %v, want ErrInvalidDefinition", err)
	}
	if errors.Is(err, rift.ErrClosed) {
		t.Errorf("err = %v: the engine was reached before the options were checked", err)
	}
}
