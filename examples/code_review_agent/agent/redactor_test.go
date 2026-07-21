package agent

import (
	"strings"
	"testing"
)

func TestRedactor(t *testing.T) {
	r := NewRedactor()
	input := `password := "super-secret-password-12345" token = "ghp_1234567890abcdefghijklmnopqrstuvwxyz" Authorization: Bearer sk_live_1234567890abcdef`
	out := r.Redact(input)
	for _, secret := range []string{"super-secret-password-12345", "ghp_1234567890abcdefghijklmnopqrstuvwxyz", "sk_live_1234567890abcdef"} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret %q leaked in %q", secret, out)
		}
	}
	if !strings.Contains(out, "<redacted:") {
		t.Fatalf("missing redaction marker: %q", out)
	}
}
