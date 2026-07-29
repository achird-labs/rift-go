package rift

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RemoteOptions configure a connection to a running engine's admin API.
type RemoteOptions struct {
	// HTTPClient overrides the client used for admin calls. The zero value uses a client with
	// a 30s timeout; supply your own to change timeouts, proxies, or TLS.
	HTTPClient *http.Client

	// APIKey is sent as x-api-key when the engine was started with an API key.
	APIKey string

	// Host overrides the host imposters are reached at. It defaults to the admin URL's host,
	// which is right for a local engine and for a container publishing every imposter port.
	Host string
}

// Remote is a Client backed by an engine's admin API.
type Remote struct {
	base   *url.URL
	http   *http.Client
	apiKey string
	host   string

	// ownedProc is set when this Remote was produced by Spawn and is responsible for stopping
	// the child on Close.
	ownedProc *process
}

var _ Client = (*Remote)(nil)

// Connect returns a Client for an engine already listening at adminURL, e.g.
// "http://localhost:2525". It does not contact the engine; call Ping to check reachability.
func Connect(adminURL string, opts RemoteOptions) (*Remote, error) {
	u, err := url.Parse(adminURL)
	if err != nil {
		return nil, fmt.Errorf("%w: admin URL %q: %w", ErrInvalidDefinition, adminURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("%w: admin URL %q needs a scheme and host", ErrInvalidDefinition, adminURL)
	}
	c := opts.HTTPClient
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	host := opts.Host
	if host == "" {
		host = u.Hostname()
	}
	return &Remote{base: u, http: c, apiKey: opts.APIKey, host: host}, nil
}

// HostFor reports the host an imposter on port is reachable at, so BaseURLFor works against a
// remote or containerised engine as well as a local one.
func (r *Remote) HostFor(port uint16) string {
	return r.host + ":" + strconv.Itoa(int(port))
}

// AdminURL returns the admin base URL.
func (r *Remote) AdminURL() string { return r.base.String() }

// Ping checks that the engine is reachable and healthy.
func (r *Remote) Ping(ctx context.Context) error {
	_, err := r.do(ctx, http.MethodGet, "/health", nil)
	return err
}

// Config returns the engine's capability document. The serveOptions list is the supported way
// to feature-detect: a key's *absence* means an engine too old to accept it.
func (r *Remote) Config(ctx context.Context) (json.RawMessage, error) {
	return r.do(ctx, http.MethodGet, "/config", nil)
}

// Close releases idle connections, and stops the child process when this Remote came from Spawn.
func (r *Remote) Close() error {
	r.http.CloseIdleConnections()
	if r.ownedProc != nil {
		return r.ownedProc.stop()
	}
	return nil
}

// --- imposters ---

// CreateImposter creates an imposter and returns the port it bound. When the definition omits a
// port the engine assigns one, which is read back from the response.
func (r *Remote) CreateImposter(ctx context.Context, src ImposterSource) (uint16, error) {
	imp := src.BuildImposter()
	body, err := ToJSON(imp)
	if err != nil {
		return 0, err
	}
	raw, err := r.do(ctx, http.MethodPost, "/imposters", body)
	if err != nil {
		return 0, err
	}
	var created struct {
		Port uint16 `json:"port"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return 0, wrapInvalid("decode created imposter", err)
	}
	if created.Port == 0 {
		// An engine that echoes no port leaves the caller with nothing to address; fall back to
		// the requested port rather than returning a silently useless zero.
		if imp.Port != 0 {
			return imp.Port, nil
		}
		return 0, fmt.Errorf("%w: engine returned no port for the created imposter", ErrInvalidDefinition)
	}
	return created.Port, nil
}

// DeleteImposter removes one imposter.
func (r *Remote) DeleteImposter(ctx context.Context, port uint16) error {
	_, err := r.do(ctx, http.MethodDelete, "/imposters/"+itoa(port), nil)
	return err
}

// DeleteAll removes every imposter.
func (r *Remote) DeleteAll(ctx context.Context) error {
	_, err := r.do(ctx, http.MethodDelete, "/imposters", nil)
	return err
}

// ListImposters returns the engine's imposters. Replayable returns full configs rather than a
// summary.
func (r *Remote) ListImposters(ctx context.Context, replayable bool) (json.RawMessage, error) {
	path := "/imposters"
	if replayable {
		path += "?replayable=true"
	}
	return r.do(ctx, http.MethodGet, path, nil)
}

// GetImposter returns one imposter's projection.
func (r *Remote) GetImposter(ctx context.Context, port uint16, replayable bool) (json.RawMessage, error) {
	path := "/imposters/" + itoa(port)
	if replayable {
		path += "?replayable=true"
	}
	return r.do(ctx, http.MethodGet, path, nil)
}

// ReplaceAll replaces the engine's whole imposter set from a config envelope.
func (r *Remote) ReplaceAll(ctx context.Context, cfg ImpostersConfig) (json.RawMessage, error) {
	body, err := ToJSON(cfg)
	if err != nil {
		return nil, err
	}
	return r.do(ctx, http.MethodPut, "/imposters", body)
}

// SetImposterEnabled enables or disables an imposter without deleting it.
func (r *Remote) SetImposterEnabled(ctx context.Context, port uint16, enabled bool) error {
	verb := "/disable"
	if enabled {
		verb = "/enable"
	}
	_, err := r.do(ctx, http.MethodPost, "/imposters/"+itoa(port)+verb, nil)
	return err
}

// --- stubs ---

// ReplaceStubs replaces every stub on an imposter.
func (r *Remote) ReplaceStubs(ctx context.Context, port uint16, stubs []Stub) error {
	body, err := ToJSON(map[string]any{"stubs": stubs})
	if err != nil {
		return err
	}
	_, err = r.do(ctx, http.MethodPut, "/imposters/"+itoa(port)+"/stubs", body)
	return err
}

// AddStub inserts a stub at index, or appends when index is negative.
func (r *Remote) AddStub(ctx context.Context, port uint16, src StubSource, index int) error {
	payload := map[string]any{"stub": src.BuildStub()}
	if index >= 0 {
		payload["index"] = index
	}
	body, err := ToJSON(payload)
	if err != nil {
		return err
	}
	_, err = r.do(ctx, http.MethodPost, "/imposters/"+itoa(port)+"/stubs", body)
	return err
}

// UpdateStub replaces the stub at index.
func (r *Remote) UpdateStub(ctx context.Context, port uint16, index int, src StubSource) error {
	body, err := ToJSON(map[string]any{"stub": src.BuildStub()})
	if err != nil {
		return err
	}
	_, err = r.do(ctx, http.MethodPut,
		"/imposters/"+itoa(port)+"/stubs/"+strconv.Itoa(index), body)
	return err
}

// DeleteStub removes the stub at index.
func (r *Remote) DeleteStub(ctx context.Context, port uint16, index int) error {
	_, err := r.do(ctx, http.MethodDelete,
		"/imposters/"+itoa(port)+"/stubs/"+strconv.Itoa(index), nil)
	return err
}

// --- recording and verification ---

// Recorded returns the imposter's request journal.
func (r *Remote) Recorded(ctx context.Context, port uint16) ([]RecordedRequest, error) {
	raw, err := r.do(ctx, http.MethodGet, "/imposters/"+itoa(port)+"/savedRequests", nil)
	if err != nil {
		return nil, err
	}
	return decodeRecorded(raw)
}

// ClearRecorded empties the request journal.
func (r *Remote) ClearRecorded(ctx context.Context, port uint16) error {
	_, err := r.do(ctx, http.MethodDelete, "/imposters/"+itoa(port)+"/savedRequests", nil)
	return err
}

// ClearProxyRecordings drops stubs recorded by proxy responses.
func (r *Remote) ClearProxyRecordings(ctx context.Context, port uint16) error {
	_, err := r.do(ctx, http.MethodDelete, "/imposters/"+itoa(port)+"/savedProxyResponses", nil)
	return err
}

// Verify counts journal entries matching a predicate set, evaluated engine-side.
func (r *Remote) Verify(ctx context.Context, port uint16, req VerifyRequest) (VerifyResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return VerifyResult{}, err
	}
	raw, err := r.do(ctx, http.MethodPost, "/imposters/"+itoa(port)+"/verify", body)
	if err != nil {
		return VerifyResult{}, err
	}
	var out VerifyResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return VerifyResult{}, wrapInvalid("decode verify result", err)
	}
	return out, nil
}

// --- scenarios ---

// Scenarios returns scenario states for an imposter.
func (r *Remote) Scenarios(ctx context.Context, port uint16) (json.RawMessage, error) {
	return r.do(ctx, http.MethodGet, "/imposters/"+itoa(port)+"/scenarios", nil)
}

// SetScenarioState forces a scenario into a state.
func (r *Remote) SetScenarioState(ctx context.Context, port uint16, scenario, state, flowID string) error {
	body, err := json.Marshal(map[string]string{"state": state, "flowId": flowID})
	if err != nil {
		return err
	}
	_, err = r.do(ctx, http.MethodPut,
		"/imposters/"+itoa(port)+"/scenarios/"+url.PathEscape(scenario)+"/state", body)
	return err
}

// ResetScenarios returns every scenario to its start state.
func (r *Remote) ResetScenarios(ctx context.Context, port uint16, flowID string) error {
	var body []byte
	if flowID != "" {
		var err error
		if body, err = json.Marshal(map[string]string{"flowId": flowID}); err != nil {
			return err
		}
	}
	_, err := r.do(ctx, http.MethodPost, "/imposters/"+itoa(port)+"/scenarios/reset", body)
	return err
}

// --- spaces ---

// SpaceAddStub adds a stub scoped to one flow.
func (r *Remote) SpaceAddStub(ctx context.Context, port uint16, flowID string, src StubSource) error {
	body, err := ToJSON(map[string]any{"stub": src.BuildStub()})
	if err != nil {
		return err
	}
	_, err = r.do(ctx, http.MethodPost,
		"/imposters/"+itoa(port)+"/spaces/"+url.PathEscape(flowID)+"/stubs", body)
	return err
}

// SpaceListStubs returns the stubs scoped to one flow.
func (r *Remote) SpaceListStubs(ctx context.Context, port uint16, flowID string) (json.RawMessage, error) {
	return r.do(ctx, http.MethodGet,
		"/imposters/"+itoa(port)+"/spaces/"+url.PathEscape(flowID)+"/stubs", nil)
}

// SpaceDelete removes a flow's space and everything in it.
func (r *Remote) SpaceDelete(ctx context.Context, port uint16, flowID string) error {
	_, err := r.do(ctx, http.MethodDelete,
		"/imposters/"+itoa(port)+"/spaces/"+url.PathEscape(flowID), nil)
	return err
}

// --- transport ---

// do issues an admin request and returns the response body. A non-2xx status becomes an
// *EngineError carrying the engine's own message; a transport failure becomes
// ErrEngineUnavailable, so callers can tell "the engine said no" from "the engine wasn't there".
func (r *Remote) do(ctx context.Context, method, path string, body []byte) (json.RawMessage, error) {
	u := *r.base
	rawPath, rawQuery, _ := strings.Cut(path, "?")
	u.Path = strings.TrimRight(u.Path, "/") + rawPath
	u.RawQuery = rawQuery

	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return nil, wrapInvalid("build "+method+" "+path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.apiKey != "" {
		req.Header.Set("x-api-key", r.apiKey)
	}

	resp, err := r.http.Do(req)
	if err != nil {
		return nil, wrapUnavailable(method+" "+path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapUnavailable("read "+method+" "+path+" response", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newEngineError(method+" "+path, resp.StatusCode, engineMessage(respBody, resp.Status))
	}
	return respBody, nil
}

// engineMessage pulls the human-readable part out of an error body. Mountebank-shaped errors are
// {"errors":[{"message":...}]}; anything else falls back to the raw body, then the HTTP status,
// so a caller is never left with an empty explanation.
func engineMessage(body []byte, status string) string {
	var shaped struct {
		Errors []struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"errors"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &shaped); err == nil {
		if len(shaped.Errors) > 0 && shaped.Errors[0].Message != "" {
			return shaped.Errors[0].Message
		}
		if shaped.Message != "" {
			return shaped.Message
		}
	}
	if trimmed := strings.TrimSpace(string(body)); trimmed != "" {
		return trimmed
	}
	return status
}

// decodeRecorded accepts both the bare array and the {"requests":[...]} envelope, because the
// admin route and the C ABI differ on which they return.
func decodeRecorded(raw []byte) ([]RecordedRequest, error) {
	var direct []RecordedRequest
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct, nil
	}
	var wrapped struct {
		Requests []RecordedRequest `json:"requests"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, wrapInvalid("decode recorded requests", err)
	}
	return wrapped.Requests, nil
}

func itoa(p uint16) string { return strconv.Itoa(int(p)) }
