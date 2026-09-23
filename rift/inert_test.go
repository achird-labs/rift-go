package rift_test

import (
	"testing"

	"github.com/achird-labs/rift-go/rift"
)

// RecordMatches is inert on the engine, but a Mountebank config that carries it must still
// round-trip, so the builder keeps writing the key.
func TestRecordMatchesStillSerialises(t *testing.T) {
	wireEqual(t, rift.NewImposter("m").RecordMatches().Build(), `{"name":"m","recordMatches":true}`)
}
