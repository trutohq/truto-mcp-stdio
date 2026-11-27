package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type jsonRPCMessage struct {
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

type initializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
}

// version is set when a release is built (-ldflags "-X main.version=1.0.4").
var version = "dev"

// userAgentTransport names this program on every request it sends, so
// Truto can tell bridge traffic from other clients.
type userAgentTransport struct {
	base http.RoundTripper
}

func (t userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", "truto-mcp-stdio/"+version)
	return t.base.RoundTrip(req)
}

func newClient() *http.Client {
	return &http.Client{Transport: userAgentTransport{base: http.DefaultTransport}}
}

var stdoutMu sync.Mutex

func writeStdout(line string) {
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	fmt.Println(line)
}

func newPostRequest(ctx context.Context, apiURL, body, sessionID, protocolVersion string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("MCP-Session-Id", sessionID)
	}
	if protocolVersion != "" {
		req.Header.Set("MCP-Protocol-Version", protocolVersion)
	}
	return req, nil
}

// processSSEStream reads an SSE stream, dispatching each event's data via onMessage.
// Tracks event IDs and retry intervals through the provided pointers when non-nil.
func processSSEStream(r io.Reader, onMessage func(string), lastEventID *string, retryMs *int) {
	var dataBuf strings.Builder
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			value := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(value, " ") {
				value = value[1:]
			}
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(value)
		case strings.HasPrefix(line, "id:"):
			if lastEventID != nil {
				id := strings.TrimPrefix(line, "id:")
				if strings.HasPrefix(id, " ") {
					id = id[1:]
				}
				*lastEventID = id
			}
		case strings.HasPrefix(line, "retry:"):
			if retryMs != nil {
				val := strings.TrimSpace(strings.TrimPrefix(line, "retry:"))
				if ms, err := strconv.Atoi(val); err == nil {
					*retryMs = ms
				}
			}
		case line == "":
			if dataBuf.Len() > 0 {
				data := dataBuf.String()
				dataBuf.Reset()
				if strings.TrimSpace(data) != "" {
					onMessage(data)
				}
			}
		}
	}
	if dataBuf.Len() > 0 {
		data := dataBuf.String()
		if strings.TrimSpace(data) != "" {
			onMessage(data)
		}
	}
}

func handlePostResponse(resp *http.Response) ([]string, error) {
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		return nil, nil
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "text/event-stream") {
		var messages []string
		processSSEStream(resp.Body, func(data string) {
			messages = append(messages, data)
		}, nil, nil)
		return messages, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %v", err)
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, nil
	}
	return []string{trimmed}, nil
}

// runGETStream maintains a GET SSE connection for server-initiated messages,
// reconnecting with Last-Event-ID when the connection drops.
func runGETStream(ctx context.Context, client *http.Client, apiURL, sessionID, protocolVersion string) {
	var lastEventID string
	retryMs := 3000

	for {
		if ctx.Err() != nil {
			return
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "GET SSE: error creating request: %v\n", err)
			return
		}
		req.Header.Set("Accept", "text/event-stream")
		if sessionID != "" {
			req.Header.Set("MCP-Session-Id", sessionID)
		}
		if protocolVersion != "" {
			req.Header.Set("MCP-Protocol-Version", protocolVersion)
		}
		if lastEventID != "" {
			req.Header.Set("Last-Event-ID", lastEventID)
		}

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			fmt.Fprintf(os.Stderr, "GET SSE: connection error: %v\n", err)
			sleepCtx(ctx, time.Duration(retryMs)*time.Millisecond)
			continue
		}

		switch resp.StatusCode {
		case http.StatusMethodNotAllowed:
			resp.Body.Close()
			return
		case http.StatusNotFound:
			resp.Body.Close()
			fmt.Fprintln(os.Stderr, "GET SSE: session expired (404)")
			return
		case http.StatusOK:
		default:
			resp.Body.Close()
			fmt.Fprintf(os.Stderr, "GET SSE: unexpected status %d\n", resp.StatusCode)
			sleepCtx(ctx, time.Duration(retryMs)*time.Millisecond)
			continue
		}

		processSSEStream(resp.Body, func(data string) {
			writeStdout(data)
		}, &lastEventID, &retryMs)
		resp.Body.Close()

		if ctx.Err() != nil {
			return
		}
		sleepCtx(ctx, time.Duration(retryMs)*time.Millisecond)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func deleteSession(client *http.Client, apiURL, sessionID string) {
	if sessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("MCP-Session-Id", sessionID)
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: truto-mcp-stdio <API_URL>")
		os.Exit(1)
	}

	apiURL := os.Args[1]
	client := newClient()

	var sessionID string
	var protocolVersion string

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var getStreamCancel context.CancelFunc

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
		os.Stdin.Close()
	}()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		if ctx.Err() != nil {
			break
		}

		line := scanner.Text()
		if line == "" {
			continue
		}

		var msg jsonRPCMessage
		isInitialize := false
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			isInitialize = msg.Method == "initialize"
		}

		req, err := newPostRequest(ctx, apiURL, line, sessionID, protocolVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating request: %v\n", err)
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			fmt.Fprintf(os.Stderr, "error making request: %v\n", err)
			continue
		}

		if sid := resp.Header.Get("MCP-Session-Id"); sid != "" {
			sessionID = sid
		}
		statusCode := resp.StatusCode

		messages, err := handlePostResponse(resp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}

		if statusCode == http.StatusNotFound && sessionID != "" {
			fmt.Fprintln(os.Stderr, "session expired, server returned 404")
			sessionID = ""
			protocolVersion = ""
		}
		if statusCode >= 400 {
			fmt.Fprintf(os.Stderr, "HTTP error %d\n", statusCode)
		}

		for _, m := range messages {
			if isInitialize && protocolVersion == "" {
				var initResp jsonRPCMessage
				if err := json.Unmarshal([]byte(m), &initResp); err == nil && initResp.Result != nil {
					var result initializeResult
					if err := json.Unmarshal(initResp.Result, &result); err == nil && result.ProtocolVersion != "" {
						protocolVersion = result.ProtocolVersion
					}
				}
			}
			writeStdout(m)
		}

		if isInitialize && sessionID != "" {
			if getStreamCancel != nil {
				getStreamCancel()
			}
			var getCtx context.Context
			getCtx, getStreamCancel = context.WithCancel(ctx)
			go runGETStream(getCtx, client, apiURL, sessionID, protocolVersion)
		}
	}

	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "error reading stdin: %v\n", err)
	}

	if getStreamCancel != nil {
		getStreamCancel()
	}
	cancel()
	deleteSession(client, apiURL, sessionID)
}
