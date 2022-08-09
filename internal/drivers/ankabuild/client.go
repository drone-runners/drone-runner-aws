package ankabuild

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

	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
)

const (
	pathVMController = "%s/api/v1/vm"
	pathVMFind       = "%s/api/v1/vm?id=%s"
	pathStatus       = "%s/api/v1/status"
)

type createVMParams struct {
	Name                   string `json:"name,omitempty"`
	Version                string `json:"version,omitempty"`
	Count                  string `json:"count,omitempty"`
	NodeID                 string `json:"node_id,omitempty"`
	StartupScript          string `json:"startup_script,omitempty"`
	StartupScriptCondition int    `json:"startup_script_condition,omitempty"`
	ScriptMonitoring       bool   `json:"script_monitoring,omitempty"`
	ScriptTimeout          int    `json:"script_timeout,omitempty"`
	ScriptFailHandler      int    `json:"script_fail_handler,omitempty"`
	VMID                   string `json:"vmid,omitempty"`
	GroupID                string `json:"group_id,omitempty"`
	Priority               string `json:"priority,omitempty"`
	Tag                    string `json:"tag,omitempty"`
}

type vmResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Body    struct {
		InstanceID    string `json:"instance_id"`
		InstanceState string `json:"instance_state"`
		AnkaRegistry  string `json:"anka_registry"`
		Vmid          string `json:"vmid"`
		Tag           string `json:"tag"`
		Vminfo        struct {
			UUID                string    `json:"uuid"`
			Name                string    `json:"name"`
			CPUCores            int       `json:"cpu_cores"`
			RAM                 string    `json:"ram"`
			Status              string    `json:"status"`
			NodeID              string    `json:"node_id"`
			HostIP              string    `json:"host_ip"`
			IP                  string    `json:"ip"`
			VncPort             int       `json:"vnc_port"`
			VncConnectionString string    `json:"vnc_connection_string"`
			VncPassword         string    `json:"vnc_password"`
			CreationDate        time.Time `json:"creation_date"`
			StopDate            time.Time `json:"stop_date"`
			Version             string    `json:"version"`
			PortForwarding      []struct {
				GuestPort int    `json:"guest_port"`
				HostPort  int    `json:"host_port"`
				Protocol  string `json:"protocol"`
				RuleName  string `json:"rule_name"`
			} `json:"port_forwarding,omitempty"`
		} `json:"vminfo"`
		NodeID string    `json:"node_id"`
		TS     time.Time `json:"ts"`
		CrTime time.Time `json:"cr_time"`
		Arch   string    `json:"arch"`
		Vlan   string    `json:"vlan"`
	} `json:"body"`
}

type statusResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Body    struct {
		Status          string `json:"status"`
		Version         string `json:"version"`
		RegistryAddress string `json:"registry_address"`
		RegistryStatus  string `json:"registry_status"`
		License         string `json:"license"`
	} `json:"body"`
}

type createVMResponse struct {
	Status  string   `json:"status"`
	Body    []string `json:"body"`
	Message string   `json:"message"`
}

type deleteVMRequest struct {
	ID string `json:"id"`
}

type client struct {
	client    *http.Client
	addr      string
	authToken string
}

type Client interface {
	VMCreate(ctx context.Context, params *createVMParams) (*createVMResponse, error)
	VMDelete(ctx context.Context, id string) error
	VMFind(ctx context.Context, id string) (*vmResponse, error)
	Status(ctx context.Context) (*statusResponse, error)
}

func (c *client) VMCreate(ctx context.Context, in *createVMParams) (*createVMResponse, error) {
	out := new(createVMResponse)
	uri := fmt.Sprintf(pathVMController, c.addr)
	err := c.post(ctx, uri, in, out)
	return out, err
}

func (c *client) VMFind(ctx context.Context, id string) (*vmResponse, error) {
	out := new(vmResponse)
	uri := fmt.Sprintf(pathVMFind, c.addr, id)
	err := c.get(ctx, uri, nil, out)
	return out, err
}

func (c *client) VMDelete(ctx context.Context, id string) error {
	uri := fmt.Sprintf(pathVMController, c.addr)
	in := &deleteVMRequest{ID: id}
	err := c.delete(ctx, uri, in)
	return err
}

func (c *client) Status(ctx context.Context) (*statusResponse, error) {
	out := new(statusResponse)
	uri := fmt.Sprintf(pathStatus, c.addr)
	err := c.get(ctx, uri, nil, out)
	return out, err
}

// helper function for making a http POST request.
func (c *client) post(ctx context.Context, rawURL string, in, out interface{}) error {
	return c.do(ctx, rawURL, "POST", in, out)
}

// helper function for making a http GET request.
func (c *client) get(ctx context.Context, rawURL string, in, out interface{}) error {
	return c.do(ctx, rawURL, "GET", in, out)
}

// helper function for making a http DELETE request.
func (c *client) delete(ctx context.Context, rawURL string, in interface{}) error {
	return c.do(ctx, rawURL, "DELETE", in, nil)
}

func (c *client) do(ctx context.Context, rawURL, method string, in, out interface{}) error {
	body, err := c.open(ctx, rawURL, method, in, out)
	if err != nil {
		return err
	}
	defer body.Close()
	if out != nil {
		return json.NewDecoder(body).Decode(out)
	}
	return nil
}

func (c *client) open(ctx context.Context, rawURL, method string, in, out interface{}) (io.ReadCloser, error) { //nolint
	uri, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, uri.String(), http.NoBody)

	// set auth
	req.SetBasicAuth("root", c.authToken)

	if err != nil {
		return nil, err
	}
	if in != nil {
		decoded, dErr := json.Marshal(in)
		if dErr != nil {
			return nil, dErr
		}
		buf := bytes.NewBuffer(decoded)
		req.Body = io.NopCloser(buf)
		req.ContentLength = int64(len(decoded))
		req.Header.Set("Content-Length", strconv.Itoa(len(decoded)))
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode > 299 { //nolint
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("client error %d: %s", resp.StatusCode, string(out))
	}
	return resp.Body, nil
}

func NewClient(uri, authToken string) Client {
	return &client{http.DefaultClient, strings.TrimSuffix(uri, "/"), authToken}
}

func tempdir(inputOS string) string {
	const dir = "anka"

	switch inputOS {
	case oshelp.OSWindows:
		return oshelp.JoinPaths(inputOS, "C:\\Windows\\Temp", dir)
	case oshelp.OSMac:
		return oshelp.JoinPaths(inputOS, "/tmp", dir)
	default:
		return oshelp.JoinPaths(inputOS, "/tmp", dir)
	}
}
