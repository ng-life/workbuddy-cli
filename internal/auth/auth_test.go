package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func encrypted(t *testing.T, suite int) json.RawMessage {
	t.Helper()
	envelope, _ := json.Marshal(map[string]any{"suite": suite, "keyId": "0123456789abcdef", "nonce": base64.StdEncoding.EncodeToString(make([]byte, 12)), "authTag": base64.StdEncoding.EncodeToString(make([]byte, 16)), "ciphertext": ""})
	raw, _ := json.Marshal(map[string]any{"$wbEncrypted": 1, "envelope": base64.StdEncoding.EncodeToString(envelope)})
	return raw
}

func TestCredentialFormatsAndHeaderInjection(t *testing.T) {
	for _, tc := range []struct {
		raw  json.RawMessage
		kind string
		bad  bool
	}{{json.RawMessage(`"abc.def_123-=="`), "plaintext", false}, {encrypted(t, 1), "sym-v1", false}, {encrypted(t, 2), "", true}, {json.RawMessage(`"bad\ntoken"`), "", true}, {json.RawMessage(`null`), "", true}} {
		kind, e := Format(tc.raw)
		if kind != tc.kind || (e != nil) != tc.bad {
			t.Fatalf("kind=%s error=%v", kind, e)
		}
	}
	path := filepath.Join(t.TempDir(), "session")
	for _, uid := range []string{"", "u\r\nX-Evil: yes", strings.Repeat("a", 2049)} {
		raw, _ := json.Marshal(map[string]any{"account": map[string]any{"uid": uid}, "auth": map[string]any{"accessToken": "secret"}})
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := Read(path); e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatalf("error=%v", e)
		}
	}
}

func TestEncryptedHelperProbeAndDecrypt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake runtime")
	}
	path := filepath.Join(t.TempDir(), "runtime with spaces")
	script := `#!/bin/sh
if [ "$ELECTRON_RUN_AS_NODE" != 1 ] || [ -n "$NODE_OPTIONS" ]; then exit 1; fi
IFS= read -r input || :
case "$input" in
 *probe*) printf '%s' '{"version":1,"ok":true,"electron":"39.0.0"}' ;;
 *) printf '%s' '{"version":1,"ok":true,"accessToken":"decrypted-test-token"}' ;;
esac
`
	if e := os.WriteFile(path, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("NODE_OPTIONS", "should-not-reach-child")
	s := &Session{}
	s.Auth.AccessToken = encrypted(t, 1)
	d, e := s.Doctor(context.Background(), path)
	if e != nil || d.OnlineChecked || d.Format != "sym-v1" {
		t.Fatalf("%+v %v", d, e)
	}
	token, e := s.Token(context.Background(), path)
	if e != nil || token != "decrypted-test-token" {
		t.Fatalf("helper failure: %v", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = s.Token(ctx, path); e == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestHelperOutputAndErrorsAreBoundedAndRedacted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake runtime")
	}
	path := filepath.Join(t.TempDir(), "runtime")
	if e := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' 'secret-token-and-invalid-json'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	_, e := invoke(context.Background(), path, "decrypt", encrypted(t, 1))
	if e == nil || strings.Contains(e.Error(), "secret-token") {
		t.Fatalf("%v", e)
	}
	b := &limitedBuffer{limit: 3}
	if _, e = b.Write([]byte("1234")); e == nil || b.Len() != 0 {
		t.Fatal("output unbounded")
	}
}
