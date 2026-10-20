package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func runCLI(args ...string) (int, string, string) {
	var out, err bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(""), &out, &err)
	return code, out.String(), err.String()
}

func TestOfflineAndArgumentContract(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("WORKBUDDY_AUTH_FILE", "missing")
	for _, args := range [][]string{{"--help"}, {"signin", "--help"}, {"completion", "zsh"}, {"completion", "bash"}, {"completion", "fish"}, {"version"}} {
		code, out, err := runCLI(args...)
		if code != 0 || out == "" || err != "" {
			t.Fatalf("%v -> %d %q %q", args, code, out, err)
		}
	}
	for _, args := range [][]string{{"unknown"}, {"signin", "extra"}, {"signin", "--unknown"}, {"signin", "--output=yaml"}, {"completion", "nope"}, {"signin", "--timeout=0s"}, {"signin", "--", "extra"}} {
		code, _, _ := runCLI(args...)
		if code != 2 {
			t.Fatalf("%v -> %d", args, code)
		}
	}
}
func TestConfigNamedLikeCommandAndGlobalPositions(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("WORKBUDDY_AUTH_FILE", "")
	t.Setenv("WORKBUDDY_EXE", "")
	if e := os.WriteFile("auth.json", []byte(`{"auth":{"accessToken":"secret-token"},"account":{"uid":"u"}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile("signin", []byte("auth_file='auth.json'\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"--config", "signin", "doctor", "--output=json"}, {"doctor", "-c", "signin", "-o", "json"}, {"doctor", "--config=signin", "--output=json"}} {
		code, out, err := runCLI(args...)
		var v map[string]any
		if code != 0 || err != "" || json.Unmarshal([]byte(out), &v) != nil || v["online_checked"] != false || strings.Contains(out, "secret-token") {
			t.Fatalf("%v -> %d %s %s", args, code, out, err)
		}
	}
	code, _, _ := runCLI("doctor", "-c", "missing")
	if code != 1 {
		t.Fatal(code)
	}
}

func TestTextEscapesControlCharacters(t *testing.T) {
	var b bytes.Buffer
	if e := emit(&b, "text", map[string]any{"\x1b[31m": "\x1b[2J\nforged"}); e != nil {
		t.Fatal(e)
	}
	if strings.ContainsRune(b.String(), '\x1b') || strings.Contains(b.String(), "\nforged") {
		t.Fatal(b.String())
	}
}
