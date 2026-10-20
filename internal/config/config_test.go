package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelectionAndOverrides(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("WORKBUDDY_AUTH_FILE", "")
	t.Setenv("WORKBUDDY_EXE", "")
	user := filepath.Join(xdg, "workbuddy-cli")
	if e := os.MkdirAll(user, 0700); e != nil {
		t.Fatal(e)
	}
	write := func(path, body string) {
		t.Helper()
		if e := os.WriteFile(path, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(filepath.Join(user, "config.toml"), `auth_file="user"`)
	c, e := Load("")
	if e != nil || c.AuthFile != "user" {
		t.Fatalf("%+v %v", c, e)
	}
	write("config.toml", `auth_file="cwd"`)
	c, e = Load("")
	if e != nil || c.AuthFile != "cwd" {
		t.Fatalf("%+v %v", c, e)
	}
	write("explicit.toml", `auth_file="explicit"`)
	c, e = Load("explicit.toml")
	if e != nil || c.AuthFile != "explicit" {
		t.Fatalf("%+v %v", c, e)
	}
	write("config.toml", "not toml = [")
	if _, e = Load(""); e == nil {
		t.Fatal("invalid cwd config fell back")
	}
	if _, e = Load("missing"); e == nil {
		t.Fatal("missing explicit config fell back")
	}
	t.Setenv("WORKBUDDY_AUTH_FILE", "env")
	c, e = Load("explicit.toml")
	if e != nil || c.AuthFile != "env" {
		t.Fatalf("%+v %v", c, e)
	}
}

func TestHomeFallback(t *testing.T) {
	t.Chdir(t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("WORKBUDDY_AUTH_FILE", "")
	dir := filepath.Join(home, ".config", "workbuddy-cli")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(`auth_file="home"`), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := Load("")
	if e != nil || c.AuthFile != "home" {
		t.Fatalf("%+v %v", c, e)
	}
}
