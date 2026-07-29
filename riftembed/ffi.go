// Package riftembed runs a Rift engine in-process, calling the native library over its C ABI
// through purego.
//
// # No cgo
//
// purego resolves symbols through the dynamic linker at runtime rather than at compile time, so
// CGO_ENABLED=0 keeps working: no C toolchain, no cross-compilation penalty, no effect on your
// users' build. The engine's ABI is deliberately all `char*` and integers, which is exactly the
// subset purego handles.
//
// # Thread affinity
//
// The engine reports failures through rift_last_error, which is *thread-local*. A Go goroutine
// may migrate between OS threads at any call boundary, so a downcall on one thread followed by an
// error read on another would read an empty error. Every call in this package therefore pins its
// goroutine with runtime.LockOSThread for the duration of the (downcall, error-read) pair.
package riftembed

import (
	"unsafe"
)

// abiVersion is the C-ABI major version this SDK is written against. rift_abi_version must
// return exactly this; a different value means the library and the SDK disagree about symbol
// signatures and ownership, which is not something either side can paper over at runtime.
const abiVersion = 2

// symbols holds the bound C entry points.
//
// Two pointer representations, deliberately:
//
//   - The opaque RiftHandle is a uintptr. It is never dereferenced — only handed back to the
//     engine — so there is nothing for the garbage collector to misunderstand.
//   - Every `char*` is an unsafe.Pointer. Binding these as uintptr would force a
//     uintptr-to-pointer conversion on every read, which `go vet` flags as a possible misuse
//     and which is genuinely unsound if the value ever round-trips through an integer.
//
// purego converts a Go string argument into a temporary NUL-terminated buffer for `const char*`
// parameters, so those stay plain Go strings.
type symbols struct {
	// lifecycle
	start func() uintptr
	stop  func(h uintptr)

	// diagnostics
	abiVersion func() uint32
	buildInfo  func() unsafe.Pointer // const char*, static — must NOT be freed
	lastError  func() unsafe.Pointer // char*, owned — free after reading
	free       func(p unsafe.Pointer)

	// imposters
	createImposter    func(h uintptr, json string) uint16
	deleteImposter    func(h uintptr, port uint16) int32
	deleteAll         func(h uintptr) int32
	listImposters     func(h uintptr, optionsJSON string) unsafe.Pointer
	getImposter       func(h uintptr, port uint16, optionsJSON string) unsafe.Pointer
	setImposterEnable func(h uintptr, port uint16, enabled int32) int32
	applyConfig       func(h uintptr, configJSON string) unsafe.Pointer

	// stubs
	replaceStubs func(h uintptr, port uint16, json string) int32
	addStub      func(h uintptr, port uint16, stubJSON string, index int32) int32
	getStub      func(h uintptr, port uint16, refJSON string) unsafe.Pointer
	updateStub   func(h uintptr, port uint16, refJSON, stubJSON string) int32
	deleteStub   func(h uintptr, port uint16, refJSON string) int32
	stubWarnings func(h uintptr, port uint16) unsafe.Pointer

	// recording + verification
	recorded             func(h uintptr, port uint16) unsafe.Pointer
	clearRecorded        func(h uintptr, port uint16) int32
	clearProxyRecordings func(h uintptr, port uint16) int32
	verify               func(h uintptr, port uint16, bodyJSON string) unsafe.Pointer

	// scenarios
	scenarios        func(h uintptr, port uint16, flowID string) unsafe.Pointer
	setScenarioState func(h uintptr, port uint16, scenario, state, flowID string) int32
	resetScenarios   func(h uintptr, port uint16, flowID string) int32

	// spaces + flow state
	spaceAddStub   func(h uintptr, port uint16, flowID, stubJSON string) int32
	spaceListStubs func(h uintptr, port uint16, flowID string) unsafe.Pointer
	spaceDelete    func(h uintptr, port uint16, flowID string) int32
	spaceRecorded  func(h uintptr, port uint16, flowID string) unsafe.Pointer

	flowStateGet    func(h uintptr, port uint16, flowID, key string) unsafe.Pointer
	flowStatePut    func(h uintptr, port uint16, flowID, key, value string) int32
	flowStateDelete func(h uintptr, port uint16, flowID string) int32

	// intercept
	startIntercept     func(h uintptr, optionsJSON string) unsafe.Pointer
	stopIntercept      func(h uintptr) int32
	interceptAddRules  func(h uintptr, rulesJSON string) int32
	interceptClear     func(h uintptr) int32
	interceptListRules func(h uintptr) unsafe.Pointer
	interceptCAPEM     func(h uintptr) unsafe.Pointer
	interceptTruststor func(h uintptr, format, password, outPath string) int32

	// admin plane
	serveAdmin func(h uintptr, optionsJSON string) unsafe.Pointer
}

// bind resolves every symbol in the loaded library. A missing symbol is a hard error: a partial
// binding would fail later, at an arbitrary call site, with a much worse message.
func bind(lib uintptr) (*symbols, error) {
	s := &symbols{}
	// Each entry is (destination, symbol name). Registration panics on a missing symbol, so it
	// runs behind a recover in registerAll.
	regs := []struct {
		fn   any
		name string
	}{
		{&s.start, "rift_start"},
		{&s.stop, "rift_stop"},
		{&s.abiVersion, "rift_abi_version"},
		{&s.buildInfo, "rift_build_info"},
		{&s.lastError, "rift_last_error"},
		{&s.free, "rift_free"},

		{&s.createImposter, "rift_create_imposter"},
		{&s.deleteImposter, "rift_delete_imposter"},
		{&s.deleteAll, "rift_delete_all"},
		{&s.listImposters, "rift_list_imposters"},
		{&s.getImposter, "rift_get_imposter"},
		{&s.setImposterEnable, "rift_set_imposter_enabled"},
		{&s.applyConfig, "rift_apply_config"},

		{&s.replaceStubs, "rift_replace_stubs"},
		{&s.addStub, "rift_add_stub"},
		{&s.getStub, "rift_get_stub"},
		{&s.updateStub, "rift_update_stub"},
		{&s.deleteStub, "rift_delete_stub"},
		{&s.stubWarnings, "rift_stub_warnings"},

		{&s.recorded, "rift_recorded"},
		{&s.clearRecorded, "rift_clear_recorded"},
		{&s.clearProxyRecordings, "rift_clear_proxy_recordings"},
		{&s.verify, "rift_verify"},

		{&s.scenarios, "rift_scenarios"},
		{&s.setScenarioState, "rift_set_scenario_state"},
		{&s.resetScenarios, "rift_reset_scenarios"},

		{&s.spaceAddStub, "rift_space_add_stub"},
		{&s.spaceListStubs, "rift_space_list_stubs"},
		{&s.spaceDelete, "rift_space_delete"},
		{&s.spaceRecorded, "rift_space_recorded"},

		{&s.flowStateGet, "rift_flow_state_get"},
		{&s.flowStatePut, "rift_flow_state_put"},
		{&s.flowStateDelete, "rift_flow_state_delete"},

		{&s.startIntercept, "rift_start_intercept"},
		{&s.stopIntercept, "rift_stop_intercept"},
		{&s.interceptAddRules, "rift_intercept_add_rules"},
		{&s.interceptClear, "rift_intercept_clear_rules"},
		{&s.interceptListRules, "rift_intercept_list_rules"},
		{&s.interceptCAPEM, "rift_intercept_ca_pem"},
		{&s.interceptTruststor, "rift_intercept_export_truststore"},

		{&s.serveAdmin, "rift_serve_admin"},
	}
	for _, r := range regs {
		if err := registerOne(lib, r.fn, r.name); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// goString copies a NUL-terminated C string into Go memory. It does not free the source.
//
// The pointer arithmetic is unsafe but not cgo — that distinction is the whole point of the
// purego approach, and CGO_ENABLED=0 builds keep working. Walking to the NUL and then taking one
// slice is cheaper than appending byte by byte, and unsafe.Add keeps the pointer a pointer
// throughout, so nothing here converts through uintptr.
func goString(p unsafe.Pointer) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	if n == 0 {
		return ""
	}
	// string() copies, so the result outlives the C allocation the caller is about to free.
	return string(unsafe.Slice((*byte)(p), n))
}

// takeString copies an owned `char*` return into Go memory and frees the original.
//
// Every engine function returning `char*` transfers ownership to the caller. This is the single
// place that discipline lives: if a call site reads a returned string any other way, it leaks.
func (s *symbols) takeString(p unsafe.Pointer) string {
	if p == nil {
		return ""
	}
	out := goString(p)
	s.free(p)
	return out
}
