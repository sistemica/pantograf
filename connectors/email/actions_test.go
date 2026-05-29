package email

import (
	"strings"
	"testing"

	"github.com/sistemica/pantograf/connector"
)

// render builds a message from params and returns the rendered RFC822 text.
func render(t *testing.T, params connector.Values) string {
	t.Helper()
	msg, _, err := buildMessage(params, "me@example.com")
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	var sb strings.Builder
	if _, err := msg.WriteTo(&sb); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	return sb.String()
}

func TestBuildMessageThreadingDefaultsReferences(t *testing.T) {
	out := render(t, connector.Values{
		"to":          []string{"them@example.com"},
		"subject":     "Re: hi",
		"body":        "hello",
		"in_reply_to": "abc@host", // bare id — should be angle-wrapped
	})
	if !strings.Contains(out, "In-Reply-To: <abc@host>") {
		t.Errorf("missing/!=wrapped In-Reply-To header:\n%s", out)
	}
	// References defaults to the in_reply_to id when not supplied.
	if !strings.Contains(out, "References: <abc@host>") {
		t.Errorf("References did not default to in_reply_to:\n%s", out)
	}
}

func TestBuildMessageExplicitReferencesChain(t *testing.T) {
	out := render(t, connector.Values{
		"to":          []string{"them@example.com"},
		"subject":     "Re: hi",
		"body":        "hello",
		"in_reply_to": "<c@host>",
		"references":  []string{"<a@host>", "b@host"}, // mixed wrapped/bare
	})
	if !strings.Contains(out, "References: <a@host> <b@host>") {
		t.Errorf("References chain not joined/normalised:\n%s", out)
	}
}

func TestBuildMessageNoThreadingHeadersWhenAbsent(t *testing.T) {
	out := render(t, connector.Values{
		"to":      []string{"them@example.com"},
		"subject": "fresh",
		"body":    "hello",
	})
	if strings.Contains(out, "In-Reply-To:") || strings.Contains(out, "References:") {
		t.Errorf("threading headers leaked into a non-reply:\n%s", out)
	}
}

func TestAngleWrap(t *testing.T) {
	cases := map[string]string{
		"abc@host":   "<abc@host>",
		"<abc@host>": "<abc@host>",
		"  x@y  ":    "<x@y>",
		"":           "",
	}
	for in, want := range cases {
		if got := angleWrap(in); got != want {
			t.Errorf("angleWrap(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearchCriteria(t *testing.T) {
	if c, _ := searchCriteria("", "q"); len(c.Header) != 1 || c.Header[0].Key != "Subject" {
		t.Errorf("empty field should default to Subject header, got %+v", c)
	}
	if c, _ := searchCriteria("from", "q"); len(c.Header) != 1 || c.Header[0].Key != "From" {
		t.Errorf("from -> From header, got %+v", c)
	}
	if c, _ := searchCriteria("body", "q"); len(c.Body) != 1 {
		t.Errorf("body -> Body criterion, got %+v", c)
	}
	if c, _ := searchCriteria("text", "q"); len(c.Text) != 1 {
		t.Errorf("text -> Text criterion, got %+v", c)
	}
	if _, err := searchCriteria("nonsense", "q"); err == nil {
		t.Error("unknown field should error")
	}
}
