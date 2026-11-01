package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLookupUpdate(t *testing.T) {
	for _, tt := range []struct {
		name, current, body, want string
		status                    int
	}{
		{"newer", "0.2.0", `{"tag_name":"v0.10.0"}`, "0.10.0", 200},
		{"older", "1.0.0", `{"tag_name":"v0.99.0"}`, "", 200},
		{"large version components", "99999999999999999999.0.0", `{"tag_name":"v100000000000000000000.0.0"}`, "100000000000000000000.0.0", 200},
		{"equal metadata", "1.0.0+one", `{"tag_name":"v1.0.0+two"}`, "", 200},
		{"stable after prerelease", "1.0.0-rc.1", `{"tag_name":"v1.0.0"}`, "1.0.0", 200},
		{"prerelease tag", "0.1.0", `{"tag_name":"v1.0.0-rc.1"}`, "", 200},
		{"prerelease flag", "0.1.0", `{"tag_name":"v1.0.0","prerelease":true}`, "", 200},
		{"draft", "0.1.0", `{"tag_name":"v1.0.0","draft":true}`, "", 200},
		{"invalid current", "dev", `{"tag_name":"v1.0.0"}`, "", 200},
		{"invalid prerelease", "1.0.0-01", `{"tag_name":"v1.0.0"}`, "", 200},
		{"invalid tag", "0.1.0", `{"tag_name":"v01.0.0"}`, "", 200},
		{"invalid json", "0.1.0", `{`, "", 200},
		{"oversized response", "0.1.0", `{"tag_name":"v` + strings.Repeat("1", 1<<20) + `.0.0"}`, "", 200},
		{"http error", "0.1.0", `{"tag_name":"v1.0.0"}`, "", 500},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			if got := lookupUpdate(server.Client(), server.URL, tt.current); got != tt.want {
				t.Errorf("lookupUpdate() = %q, want %q", got, tt.want)
			}
		})
	}
	if got := lookupUpdate(http.DefaultClient, "://invalid", "1.0.0"); got != "" {
		t.Errorf("invalid URL: got %q", got)
	}
}

func TestLookupUpdateSkipsRequestForDevelopmentVersion(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()
	if got := lookupUpdate(server.Client(), server.URL, "dev"); got != "" || called {
		t.Fatalf("development version: got %q, request made: %v", got, called)
	}
}
