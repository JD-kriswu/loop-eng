// Package poll implements the HTTP poll client for daemon.
package poll

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/yourorg/loopany-go/internal/protocol"
)

const (
	PollTimeout    = 30 * time.Second
	PollInterval   = 3 * time.Second
	RepollInterval = 250 * time.Millisecond
)

// Client is the poll client.
type Client struct {
	server string
	token  string
	hc     *http.Client
}

// NewClient creates a new poll client.
func NewClient(server, token string) *Client {
	return &Client{
		server: server,
		token:  token,
		hc: &http.Client{
			Timeout: PollTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				IdleConnTimeout:     30 * time.Second,
				DisableCompression:  false,
				MaxConnsPerHost:     5,
				MaxIdleConnsPerHost: 5,
			},
		},
	}
}

// Poll sends a poll request and returns claimed deliveries.
func (c *Client) Poll(ctx context.Context, info map[string]string, progress []protocol.ProgressEntry, idle bool, watchDigest string) (*protocol.PollResponse, error) {
	req := protocol.PollRequest{
		MachineInfo: protocol.MachineInfo{
			Host:     info["host"],
			Platform: info["platform"],
			Arch:     info["arch"],
			Version:  info["version"],
		},
		Progress:    progress,
		Wait:        idle,
		WatchDigest: watchDigest,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal poll request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.server+"/api/machine/poll", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create poll request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("poll request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("poll returned %d: %s", resp.StatusCode, string(respBody))
	}

	var pollResp protocol.PollResponse
	if err := json.NewDecoder(resp.Body).Decode(&pollResp); err != nil {
		return nil, fmt.Errorf("decode poll response: %w", err)
	}

	return &pollResp, nil
}

// Report sends a run report to the server.
func (c *Client) Report(ctx context.Context, runToken string, report *protocol.ReportRequest) error {
	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.server+"/machine/report", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create report request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+runToken)

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return fmt.Errorf("report request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("report returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// CLI sends a CLI callback to the server.
func (c *Client) CLI(ctx context.Context, runToken string, argv []string) (*protocol.CLIResponse, error) {
	req := protocol.CLIRequest{
		Argv: argv,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal CLI request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.server+"/api/machine/cli", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create CLI request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+runToken)

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("CLI request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CLI returned %d: %s", resp.StatusCode, string(respBody))
	}

	var cliResp protocol.CLIResponse
	if err := json.NewDecoder(resp.Body).Decode(&cliResp); err != nil {
		return nil, fmt.Errorf("decode CLI response: %w", err)
	}

	return &cliResp, nil
}

// Sync sends a watch manifest to the server.
func (c *Client) Sync(ctx context.Context, manifest *protocol.WatchManifest) ([]string, error) {
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.server+"/api/machine/sync", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create sync request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sync request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sync returned %d: %s", resp.StatusCode, string(respBody))
	}

	var needHashes []string
	if err := json.NewDecoder(resp.Body).Decode(&needHashes); err != nil {
		return nil, fmt.Errorf("decode sync response: %w", err)
	}

	return needHashes, nil
}

// UploadBlob uploads a blob to the server.
func (c *Client) UploadBlob(ctx context.Context, hash string, data []byte) error {
	httpReq, err := http.NewRequestWithContext(ctx, "PUT", c.server+"/api/machine/blob/"+hash, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create blob request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/octet-stream")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return fmt.Errorf("blob upload failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("blob upload returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// NextPollDelay returns the delay before next poll based on elapsed time.
// If the server held the request (long-poll), poll immediately.
func NextPollDelay(elapsed time.Duration) time.Duration {
	if elapsed >= PollInterval {
		return RepollInterval
	}
	return PollInterval - elapsed
}