package layr8

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// restClient is the internal HTTP client for the cloud-node REST API.
// It handles JSON serialization, API key auth, and localhost resolution.
type restClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func newRestClient(baseURL, apiKey string) *restClient {
	return &restClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					host, port, err := net.SplitHostPort(addr)
					if err == nil && isLocalhost(host) {
						addr = net.JoinHostPort("127.0.0.1", port)
					}
					return (&net.Dialer{}).DialContext(ctx, network, addr)
				},
			},
		},
	}
}

// post sends a JSON POST request and decodes the response into result.
func (r *restClient) post(ctx context.Context, path string, body any, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("x-api-key", r.apiKey)
	}

	return r.do(req, result)
}

// get sends a GET request and decodes the response into result.
func (r *restClient) get(ctx context.Context, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if r.apiKey != "" {
		req.Header.Set("x-api-key", r.apiKey)
	}

	return r.do(req, result)
}

func (r *restClient) do(req *http.Request, result any) error {
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("REST request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return parseRESTError(resp.StatusCode, respBody)
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// RESTError represents an error response from the cloud-node REST API.
type RESTError struct {
	StatusCode int
	Message    string
}

func (e *RESTError) Error() string {
	return fmt.Sprintf("REST API error %d: %s", e.StatusCode, e.Message)
}

func parseRESTError(statusCode int, body []byte) *RESTError {
	var parsed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error != "" {
		return &RESTError{StatusCode: statusCode, Message: parsed.Error}
	}
	return &RESTError{StatusCode: statusCode, Message: string(body)}
}
