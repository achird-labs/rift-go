package rifttest

import (
	"testing"

	"github.com/achird-labs/rift-go/rift"
)

func TestSummariseShowsTheAnsweredStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    rift.RecordedRequest
		want string
	}{
		{"answered", rift.RecordedRequest{Method: "GET", Path: "/x", Status: 503}, "GET /x → 503"},
		{"older engine", rift.RecordedRequest{Method: "GET", Path: "/x"}, "GET /x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarise(tc.r); got != tc.want {
				t.Errorf("summarise = %q, want %q", got, tc.want)
			}
		})
	}
}
