package notify

import (
	"strings"
	"testing"
)

func TestReceiverText(t *testing.T) {
	if got := receiverText("text/html", []byte("no_team")); got != ": no_team" {
		t.Fatalf("short text labelled html should be kept, got %q", got)
	}
	if got := receiverText("text/html", []byte("<!doctype html><html><body>Example</body></html>")); got != "" {
		t.Fatalf("html pages should be left out, got %q", got)
	}
	long := strings.Repeat("x", 500)
	if got := receiverText("application/json", []byte(long)); len([]rune(got)) > 165 {
		t.Fatalf("long bodies should be cut, got %d runes", len([]rune(got)))
	}
	if got := receiverText("", []byte("  bad \n\n request ")); got != ": bad request" {
		t.Fatalf("whitespace should collapse, got %q", got)
	}
}
