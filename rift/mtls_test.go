package rift_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/achird-labs/rift-go/rift"
)

// wireEqual asserts an imposter serialises to exactly the given JSON document.
func wireEqual(t *testing.T, imp rift.Imposter, want string) {
	t.Helper()
	wireEqualDoc(t, imp, want)
}

// wireEqualDoc asserts a value serialises to exactly the given JSON document.
func wireEqualDoc(t *testing.T, v any, want string) {
	t.Helper()
	got, err := rift.ToJSON(v)
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad expectation %s: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("wire =\n  %s\nwant\n  %s", got, want)
	}
}

func TestRequireClientCert(t *testing.T) {
	t.Run("any certificate", func(t *testing.T) {
		wireEqual(t, rift.NewImposter("m").RequireClientCert().Build(),
			`{"name":"m","protocol":"https","mutualAuth":true}`)
	})
	t.Run("one CA is a bare string", func(t *testing.T) {
		wireEqual(t, rift.NewImposter("m").RequireClientCert("CA1").Build(),
			`{"name":"m","protocol":"https","mutualAuth":true,"rejectUnauthorized":true,"ca":"CA1"}`)
	})
	t.Run("several CAs are an array", func(t *testing.T) {
		wireEqual(t, rift.NewImposter("m").RequireClientCert("CA1", "CA2").Build(),
			`{"name":"m","protocol":"https","mutualAuth":true,"rejectUnauthorized":true,"ca":["CA1","CA2"]}`)
	})
	t.Run("keeps the server certificate", func(t *testing.T) {
		wireEqual(t, rift.NewImposter("m").HTTPS("CERT", "KEY").RequireClientCert("CA1").Build(),
			`{"name":"m","protocol":"https","cert":"CERT","key":"KEY","mutualAuth":true,"rejectUnauthorized":true,"ca":"CA1"}`)
	})
	t.Run("called after HTTPS or before it, same result", func(t *testing.T) {
		wireEqual(t, rift.NewImposter("m").RequireClientCert("CA1").HTTPS("CERT", "KEY").Build(),
			`{"name":"m","protocol":"https","cert":"CERT","key":"KEY","mutualAuth":true,"rejectUnauthorized":true,"ca":"CA1"}`)
	})
}

func TestClientAuthKeysRoundTrip(t *testing.T) {
	for _, doc := range []string{
		`{"protocol":"https","mutualAuth":true,"rejectUnauthorized":true,"ca":"PEM"}`,
		`{"protocol":"https","mutualAuth":true,"rejectUnauthorized":true,"ca":["A","B"]}`,
	} {
		imp, err := rift.ImposterFromJSON([]byte(doc))
		if err != nil {
			t.Fatalf("ImposterFromJSON: %v", err)
		}
		if !imp.RejectUnauthorized {
			t.Errorf("%s: RejectUnauthorized not decoded into the typed field", doc)
		}
		if _, inExtra := imp.Extra["ca"]; inExtra {
			t.Errorf("%s: ca landed in Extra instead of the typed field", doc)
		}
		wireEqual(t, imp, doc)
	}
}
