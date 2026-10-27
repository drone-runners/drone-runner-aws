package orka

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

const (
	// defaultRequestTimeout bounds short, synchronous calls (Version/GetVM/DeleteVM) that aren't
	// expected to block server-side. DeployVM deliberately gets no client-level timeout here - it
	// blocks server-side honoring the request's own "timeout" field (which can be 10+ minutes), and
	// is bounded solely by the caller's context (see driver.go's deployCtx).
	defaultRequestTimeout = 30 * time.Second

	// /version is cluster-level - no /api/v1 prefix, not namespace-scoped, and unauthenticated.
	pathVersion = "%s/version"
	pathVMs     = "%s/api/v1/namespaces/%s/vms"
	pathVM      = "%s/api/v1/namespaces/%s/vms/%s"
)

type versionResponse struct {
	APIVersion      string `json:"apiVersion"`
	OperatorVersion string `json:"operatorVersion"`
}

// deployVMRequest mirrors v1.OrkaVMDeployRequestModel. shouldGenerateName/iso are deliberately
// omitted - we always generate our own deterministic name, and iso is an Intel-only legacy
// feature that's out of scope.
type deployVMRequest struct {
	Name           string            `json:"name,omitempty"`
	VMConfig       string            `json:"vmConfig,omitempty"`
	Image          string            `json:"image,omitempty"`
	CPU            int               `json:"cpu,omitempty"`
	Memory         float64           `json:"memory,omitempty"`
	GPUPassthrough bool              `json:"gpuPassthrough,omitempty"`
	NetBoost       bool              `json:"netBoost,omitempty"`
	LegacyIO       bool              `json:"legacyIO,omitempty"`
	VNC            bool              `json:"vnc,omitempty"`
	Tag            string            `json:"tag,omitempty"`
	TagRequired    bool              `json:"tagRequired,omitempty"`
	Node           string            `json:"node,omitempty"`
	Scheduler      string            `json:"scheduler,omitempty"`
	DisplayWidth   int               `json:"displayWidth,omitempty"`
	DisplayHeight  int               `json:"displayHeight,omitempty"`
	DisplayDPI     int               `json:"displayDPI,omitempty"`
	SystemSerial   string            `json:"systemSerial,omitempty"`
	ReservedPorts  string            `json:"reservedPorts,omitempty"`
	CustomMetadata map[string]string `json:"customMetadata,omitempty"`
	Timeout        int               `json:"timeout,omitempty"` // In minutes (Orka's unit).
}

// deployVMResponse mirrors v1.OrkaVMDeployResponseModel. No forwarded-port field is echoed back
// - the driver already knows which reservedPorts value it deployed with.
type deployVMResponse struct {
	IP           string `json:"ip"`
	Memory       string `json:"memory"`
	Name         string `json:"name"`
	Node         string `json:"node"`
	PortWarnings string `json:"portWarnings"`
	Screenshare  int    `json:"screenshare"`
	SSH          int    `json:"ssh"`
	Status       string `json:"status"`
	VNC          int    `json:"vnc"`
}

// getVMResponse mirrors v1.OrkaVMResponseModel.
type getVMResponse struct {
	CPU            int    `json:"cpu"`
	DeployDate     string `json:"deployDate"`
	DisplayDPI     int    `json:"displayDPI"`
	DisplayHeight  int    `json:"displayHeight"`
	DisplayWidth   int    `json:"displayWidth"`
	GPUPassthrough bool   `json:"gpuPassthrough"`
	Image          string `json:"image"`
	IP             string `json:"ip"`
	ISO            string `json:"iso"`
	Memory         string `json:"memory"`
	Name           string `json:"name"`
	Node           string `json:"node"`
	Owner          string `json:"owner"`
	ReservedPorts  string `json:"reservedPorts"`
	Screenshare    int    `json:"screenshare"`
	SSH            int    `json:"ssh"`
	Status         string `json:"status"`
	Type           string `json:"type"`
	VNC            int    `json:"vnc"`
}

// statusModel mirrors v1.Status, the Kubernetes-style error body Orka returns on 4xx responses.
type statusModel struct {
	Message string `json:"message"`
	Reason  string `json:"reason"`
	Code    int    `json:"code"`
}

// APIError is returned by client methods on any non-2xx response. StatusCode lets callers
// distinguish specific cases (e.g. 409 Conflict on a port-reservation clash during vmDeploy)
// without string-matching the message.
type APIError struct {
	StatusCode int
	Status     statusModel
	Raw        string
}

func (e *APIError) Error() string {
	if e.Status.Message != "" {
		return fmt.Sprintf("orka: %s (http %d)", e.Status.Message, e.StatusCode)
	}
	return fmt.Sprintf("orka: http %d: %s", e.StatusCode, e.Raw)
}

// IsConflict reports whether the controller rejected the request with 409 Conflict - e.g. a
// duplicate VM name. Confirmed live: NOT what a port-reservation clash during vmDeploy returns
// (see IsPortUnavailable).
func (e *APIError) IsConflict() bool {
	return e.StatusCode == http.StatusConflict
}

// IsPortUnavailable reports whether the controller rejected vmDeploy because the requested
// reservedPorts range isn't free on the chosen node(s). Confirmed live against a real cluster:
// this comes back as HTTP 500/InternalError with this message, not 409 - Orka auto-cleans the
// rejected deploy either way, and it's safe to retry immediately with a different port.
func (e *APIError) IsPortUnavailable() bool {
	return e.StatusCode == http.StatusInternalServerError &&
		strings.Contains(e.Status.Message, "reserved port range not available")
}

// IsNotFound reports whether the controller returned 404 - used by Destroy to treat a VM that's
// already gone as successfully deleted, rather than a failure.
func (e *APIError) IsNotFound() bool {
	return e.StatusCode == http.StatusNotFound
}

type client struct {
	client    *http.Client
	addr      string
	namespace string
	authToken string
}

type Client interface {
	Version(ctx context.Context) (*versionResponse, error)
	DeployVM(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error)
	GetVM(ctx context.Context, namespace, name string) (*getVMResponse, error)
	DeleteVM(ctx context.Context, namespace, name string) error
}

// NewClient builds a thin REST client for an Orka controller. It has no fixed Timeout - DeployVM
// can legitimately block for several minutes server-side, so timeouts are applied per-call via
// context instead (see defaultRequestTimeout, and driver.go's deployCtx for DeployVM itself).
func NewClient(controllerURL, authToken, namespace string) Client {
	return &client{
		client:    &http.Client{},
		addr:      strings.TrimSuffix(controllerURL, "/"),
		namespace: namespace,
		authToken: authToken,
	}
}

func (c *client) Version(ctx context.Context) (*versionResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer cancel()
	out := new(versionResponse)
	uri := fmt.Sprintf(pathVersion, c.addr)
	err := c.get(ctx, uri, out)
	return out, err
}

// DeployVM deliberately applies no extra client-side timeout beyond the caller's own ctx - the
// caller (driver.go's Create()) already bounds it with a context sized to the request's own
// "timeout" field, since the server blocks until the VM reaches a terminal state or that elapses.
func (c *client) DeployVM(ctx context.Context, namespace string, in *deployVMRequest) (*deployVMResponse, error) {
	out := new(deployVMResponse)
	uri := fmt.Sprintf(pathVMs, c.addr, namespace)
	err := c.post(ctx, uri, in, out)
	return out, err
}

func (c *client) GetVM(ctx context.Context, namespace, name string) (*getVMResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer cancel()
	out := new(getVMResponse)
	uri := fmt.Sprintf(pathVM, c.addr, namespace, name)
	err := c.get(ctx, uri, out)
	return out, err
}

func (c *client) DeleteVM(ctx context.Context, namespace, name string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer cancel()
	uri := fmt.Sprintf(pathVM, c.addr, namespace, name)
	return c.do(ctx, uri, http.MethodDelete, nil, nil)
}

// helper function for making a http GET request.
func (c *client) get(ctx context.Context, rawURL string, out interface{}) error {
	return c.do(ctx, rawURL, http.MethodGet, nil, out)
}

// helper function for making a http POST request.
func (c *client) post(ctx context.Context, rawURL string, in, out interface{}) error {
	return c.do(ctx, rawURL, http.MethodPost, in, out)
}

func (c *client) do(ctx context.Context, rawURL, method string, in, out interface{}) error {
	body, err := c.open(ctx, rawURL, method, in)
	if err != nil {
		return err
	}
	defer body.Close()
	if out != nil {
		return json.NewDecoder(body).Decode(out)
	}
	return nil
}

func (c *client) open(ctx context.Context, rawURL, method string, in interface{}) (io.ReadCloser, error) { //nolint
	uri, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, uri.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.authToken)

	if in != nil {
		encoded, mErr := json.Marshal(in)
		if mErr != nil {
			return nil, mErr
		}
		buf := bytes.NewBuffer(encoded)
		req.Body = io.NopCloser(buf)
		req.ContentLength = int64(len(encoded))
		req.Header.Set("Content-Length", strconv.Itoa(len(encoded)))
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode > 299 { //nolint
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		apiErr := &APIError{StatusCode: resp.StatusCode, Raw: string(raw)}
		_ = json.Unmarshal(raw, &apiErr.Status) // best-effort; Raw still holds the body on failure
		return nil, apiErr
	}
	return resp.Body, nil
}
