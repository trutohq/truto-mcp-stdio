package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientSendsUserAgent(t *testing.T) {
	var got []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.Header.Get("User-Agent"))
		if r.Method == http.MethodGet {
			// Ends runGETStream after one request.
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	client := newClient()

	post, err := newPostRequest(context.Background(), server.URL, `{"method":"ping"}`, "", "")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	runGETStream(context.Background(), client, server.URL, "session-1", "")
	deleteSession(client, server.URL, "session-1")

	want := []string{
		"POST truto-mcp-stdio/dev",
		"GET truto-mcp-stdio/dev",
		"DELETE truto-mcp-stdio/dev",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d requests %q, want %q", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d: got %q, want %q", i, got[i], want[i])
		}
	}
}
