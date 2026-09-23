package rift_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/achird-labs/rift-go/rift"
)

func TestProxyBehaviorsSerialise(t *testing.T) {
	resp := rift.Proxy("http://up").Always().After(250 * time.Millisecond).Repeat(2).Build()
	wireEqualDoc(t, resp, `{"proxy":{"to":"http://up","mode":"proxyAlways"},"_behaviors":{"wait":250,"repeat":2}}`)
}

func TestProxyWithoutBehaviorsWritesNone(t *testing.T) {
	wireEqualDoc(t, rift.Proxy("http://up").Build(), `{"proxy":{"to":"http://up"}}`)
}

// The same chain must produce the same behaviors block on a proxy response as on a canned one.
func TestProxyAndIsBehaviorsAgree(t *testing.T) {
	onIs := rift.OK().AfterBetween(10*time.Millisecond, 20*time.Millisecond).Repeat(3).
		Decorate("d").Copy("c").Lookup("l").ShellTransform("a", "b").Build().Behaviors
	onProxy := rift.Proxy("http://up").AfterBetween(10*time.Millisecond, 20*time.Millisecond).Repeat(3).
		Decorate("d").Copy("c").Lookup("l").ShellTransform("a", "b").Build().Behaviors
	want := &rift.Behaviors{
		Wait:           map[string]rift.JSON{"min": 10, "max": 20},
		Repeat:         3,
		Decorate:       "d",
		Copy:           "c",
		Lookup:         "l",
		ShellTransform: []rift.JSON{"a", "b"},
	}
	if !reflect.DeepEqual(onIs, want) {
		t.Errorf("is behaviors = %+v, want %+v", onIs, want)
	}
	if !reflect.DeepEqual(onProxy, want) {
		t.Errorf("proxy behaviors = %+v, want %+v", onProxy, want)
	}
}
