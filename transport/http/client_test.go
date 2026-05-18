package http

import (
	"context"
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestResolveURL(t *testing.T) {
	c, _ := New(Config{BaseURL: "https://api.example.com/v1"})
	cases := []struct {
		path   string
		query  url.Values
		expect string
	}{
		// Leading-slash relative — does NOT clobber the BaseURL path
		// (this was the real bug that bit us with Telegram's /bot<token>).
		{"/users", nil, "https://api.example.com/v1/users"},
		{"users", nil, "https://api.example.com/v1/users"},
		{"/users/me", nil, "https://api.example.com/v1/users/me"},
		// Absolute URLs pass through.
		{"https://other.host/x", nil, "https://other.host/x"},
		// Query merges with the path.
		{"/users", url.Values{"q": {"hi"}}, "https://api.example.com/v1/users?q=hi"},
	}
	for _, c2 := range cases {
		t.Run(c2.path, func(t *testing.T) {
			got, err := c.resolveURL(c2.path, c2.query)
			if err != nil {
				t.Fatalf("resolveURL: %v", err)
			}
			if got != c2.expect {
				t.Errorf("got %q, want %q", got, c2.expect)
			}
		})
	}
}

func TestResolveURLPreservesNonTrivialBasePath(t *testing.T) {
	// The Telegram failure mode: BaseURL has a /bot<token> path
	// and the leading-slash relative would otherwise clobber it.
	c, _ := New(Config{BaseURL: "https://api.telegram.org/bot12345"})
	got, _ := c.resolveURL("/getMe", nil)
	want := "https://api.telegram.org/bot12345/getMe"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestGetJSONHappyPath(t *testing.T) {
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.URL.Path != "/v1/things" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":42,"name":"x"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL + "/v1"})
	var out struct {
		Value int    `json:"value"`
		Name  string `json:"name"`
	}
	if err := c.GetJSON(context.Background(), "/things", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.Value != 42 || out.Name != "x" {
		t.Fatalf("unmarshal: %+v", out)
	}
}

func TestSendJSONUsesMethod(t *testing.T) {
	gotMethod := ""
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		gotMethod = ""
		if err := c.SendJSON(context.Background(), m, "/x", map[string]any{"k": 1}, nil); err != nil {
			t.Fatalf("SendJSON(%s): %v", m, err)
		}
		if gotMethod != m {
			t.Errorf("expected method %s, got %s", m, gotMethod)
		}
	}
}

func TestAPIErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	err := c.GetJSON(context.Background(), "/missing", nil, nil)
	if err == nil {
		t.Fatal("expected error on 404")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Status != 404 {
		t.Errorf("Status = %d, want 404", apiErr.Status)
	}
	if !strings.Contains(string(apiErr.Body), "not found") {
		t.Errorf("Body missing 'not found': %s", apiErr.Body)
	}
}

func TestDoLeavesAcceptUnsetByDefault(t *testing.T) {
	// Important for binary downloads (Lexware /v1/files returns
	// base64-in-JSON if Accept is application/json, native bytes
	// otherwise).
	gotAccept := ""
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		gotAccept = r.Header.Get("Accept")
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	resp, err := c.Do(context.Background(), stdhttp.MethodGet, "/x", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotAccept != "" {
		t.Errorf("Do should not set Accept by default; got %q", gotAccept)
	}
}

func TestGetJSONSetsAccept(t *testing.T) {
	gotAccept := ""
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		gotAccept = r.Header.Get("Accept")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL})
	var out any
	_ = c.GetJSON(context.Background(), "/x", nil, &out)
	if gotAccept != "application/json" {
		t.Errorf("GetJSON should set Accept=application/json; got %q", gotAccept)
	}
}

func TestSendJSONMarshalsBody(t *testing.T) {
	gotBody := ""
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		buf := make([]byte, 256)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL})
	body := map[string]any{"k": "v"}
	_ = c.SendJSON(context.Background(), "POST", "/x", body, nil)
	var got map[string]any
	if err := json.Unmarshal([]byte(gotBody), &got); err != nil {
		t.Fatalf("server didn't receive valid JSON: %v (body=%q)", err, gotBody)
	}
	if got["k"] != "v" {
		t.Errorf("expected k=v in body, got %+v", got)
	}
}
