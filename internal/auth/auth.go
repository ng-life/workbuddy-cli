// Package auth reads the local session without modifying it or logging secrets.
package auth

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"howett.net/plist"
)

//go:embed helper.js
var helper string
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+=*$`)
var headerPattern = regexp.MustCompile(`^[\x21-\x7e]+$`)
var keyPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// Session deliberately has no JSON tags for output: callers must use Doctor only.
type Session struct {
	Auth struct {
		AccessToken json.RawMessage `json:"accessToken"`
		Endpoint    string          `json:"endpoint"`
		Domain      string          `json:"domain"`
	} `json:"auth"`
	Account struct {
		UID          string `json:"uid"`
		EnterpriseID string `json:"enterpriseId"`
	} `json:"account"`
	Path string `json:"-"`
}

type Diagnosis struct {
	Result        string `json:"result"`
	Format        string `json:"credential_format"`
	Runtime       string `json:"runtime,omitempty"`
	OnlineChecked bool   `json:"online_checked"`
	Report        string `json:"report"`
}

func Find(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("无法定位用户目录")
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		local = filepath.Join(home, "AppData", "Local")
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	rel := filepath.Join("CodeBuddyExtension", "Data", "Public", "auth", "workbuddy-desktop.info")
	candidates := []string{filepath.Join(local, rel), filepath.Join(home, "Library", "Application Support", rel), filepath.Join(data, "CodeBuddyExtension", "Data", "Public", "auth", "Tencent-Cloud.coding-copilot.info"), filepath.Join(home, ".config", rel), filepath.Join(home, ".workbuddy", "auth", "workbuddy-desktop.info")}
	for _, p := range candidates {
		if st, e := os.Stat(p); e == nil && st.Mode().IsRegular() {
			return p, nil
		}
	}
	return "", errors.New("未找到登录态，请先登录 WorkBuddy，或使用 --auth-file 指定凭据文件")
}

func Read(path string) (*Session, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, errors.New("无法读取登录态，请检查文件路径与权限")
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil || len(b) > 65536 {
		return nil, errors.New("登录态读取失败或超过 64 KiB")
	}
	var s Session
	if json.Unmarshal(b, &s) != nil {
		return nil, errors.New("登录态 JSON 格式无效")
	}
	if s.Account.UID == "" || len(s.Auth.AccessToken) == 0 {
		return nil, errors.New("登录态缺少账号或令牌，请重新登录客户端")
	}
	for _, v := range []string{s.Account.UID, s.Account.EnterpriseID, s.Auth.Domain} {
		if v != "" && (len(v) > 2048 || !headerPattern.MatchString(v)) {
			return nil, errors.New("登录态字段格式无效")
		}
	}
	if _, e = Format(s.Auth.AccessToken); e != nil {
		return nil, e
	}
	s.Path = path
	return &s, nil
}

func validToken(s string) bool { return len(s) > 0 && len(s) <= 32768 && tokenPattern.MatchString(s) }

func Format(raw json.RawMessage) (string, error) {
	invalid := errors.New("登录态令牌格式无效或加密格式暂不支持")
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		if validToken(plain) {
			return "plaintext", nil
		}
		return "", invalid
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || len(obj) != 2 || string(obj["$wbEncrypted"]) != "1" {
		return "", invalid
	}
	var enc string
	if json.Unmarshal(obj["envelope"], &enc) != nil || len(enc) > 64512 {
		return "", invalid
	}
	b, e := base64.StdEncoding.Strict().DecodeString(enc)
	if e != nil || base64.StdEncoding.EncodeToString(b) != enc {
		return "", invalid
	}
	var env map[string]json.RawMessage
	if json.Unmarshal(b, &env) != nil || len(env) != 5 || string(env["suite"]) != "1" {
		return "", invalid
	}
	var key string
	if json.Unmarshal(env["keyId"], &key) != nil || !keyPattern.MatchString(key) {
		return "", invalid
	}
	for k, n := range map[string]int{"nonce": 12, "authTag": 16, "ciphertext": -1} {
		var v string
		if json.Unmarshal(env[k], &v) != nil {
			return "", invalid
		}
		dec, e := base64.StdEncoding.Strict().DecodeString(v)
		if e != nil || base64.StdEncoding.EncodeToString(dec) != v || (n >= 0 && len(dec) != n) {
			return "", invalid
		}
	}
	return "sym-v1", nil
}

func Runtime(override string) (string, error) {
	candidates := []string{override}
	if override == "" {
		home, _ := os.UserHomeDir()
		candidates = nil
		if runtime.GOOS == "darwin" {
			for _, root := range []string{"/Applications", filepath.Join(home, "Applications")} {
				bundle := filepath.Join(root, "WorkBuddy.app", "Contents")
				b, e := os.ReadFile(filepath.Join(bundle, "Info.plist"))
				if e != nil {
					continue
				}
				var info struct {
					Executable string `plist:"CFBundleExecutable"`
				}
				if _, e = plist.Unmarshal(b, &info); e == nil && info.Executable != "" && info.Executable != "." && info.Executable != ".." && !strings.ContainsAny(info.Executable, "/\\") {
					candidates = append(candidates, filepath.Join(bundle, "MacOS", info.Executable))
				}
			}
		}
		if runtime.GOOS == "windows" {
			local := os.Getenv("LOCALAPPDATA")
			if local == "" {
				local = filepath.Join(home, "AppData", "Local")
			}
			candidates = append(candidates, filepath.Join(local, "Programs", "WorkBuddy", "WorkBuddy.exe"))
			for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
				if root := os.Getenv(env); root != "" {
					candidates = append(candidates, filepath.Join(root, "WorkBuddy", "WorkBuddy.exe"))
				}
			}
		}
	}
	for _, p := range candidates {
		if s, e := os.Stat(p); e == nil && s.Mode().IsRegular() && (runtime.GOOS == "windows" || s.Mode()&0111 != 0) {
			return filepath.Abs(p)
		}
	}
	return "", errors.New("未找到可用的 WorkBuddy 运行时，请使用 --runtime 指定客户端可执行文件")
}

// limitedBuffer bounds captured helper pipes; errors never include child output.
type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("helper output limit")
	}
	return b.Buffer.Write(p)
}

func invoke(ctx context.Context, exe, operation string, value json.RawMessage) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req := map[string]any{"version": 1, "operation": operation}
	if value != nil {
		req["value"] = value
	}
	b, e := json.Marshal(req)
	if e != nil || len(b) > 65536 {
		return "", errors.New("凭据助手输入无效")
	}
	cmd := exec.CommandContext(ctx, exe, "-e", helper)
	cmd.WaitDelay = time.Second
	for _, v := range os.Environ() {
		k := strings.ToUpper(strings.SplitN(v, "=", 2)[0])
		if !strings.HasPrefix(k, "NODE_") && !strings.HasPrefix(k, "ELECTRON_") && !strings.HasPrefix(k, "WORKBUDDY_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "ELECTRON_RUN_AS_NODE=1")
	cmd.Stdin = bytes.NewReader(b)
	out := &limitedBuffer{limit: 65536}
	diagnostic := &limitedBuffer{limit: 8192}
	cmd.Stdout = out
	cmd.Stderr = diagnostic
	e = cmd.Run()
	if ctx.Err() != nil {
		return "", errors.New("凭据助手超时或已取消")
	}
	var reply struct {
		Version  int    `json:"version"`
		OK       bool   `json:"ok"`
		Token    string `json:"accessToken"`
		Reason   string `json:"reason"`
		Electron string `json:"electron"`
	}
	if json.Unmarshal(out.Bytes(), &reply) != nil || reply.Version != 1 {
		return "", errors.New("凭据助手响应无效")
	}
	if e != nil || !reply.OK {
		switch reply.Reason {
		case "KEY_MISMATCH":
			return "", errors.New("凭据与客户端密钥不匹配，请指定对应的 --runtime")
		case "UNSUPPORTED_ENVELOPE":
			return "", errors.New("加密凭据版本暂不支持")
		case "RUNTIME_UNAVAILABLE":
			return "", errors.New("客户端运行时不具备所需的原生存储能力")
		default:
			return "", errors.New("凭据助手执行失败，请检查客户端与登录态版本")
		}
	}
	if operation == "decrypt" && !validToken(reply.Token) {
		return "", errors.New("凭据助手返回无效令牌")
	}
	if operation == "probe" && reply.Electron == "" {
		return "", errors.New("凭据助手探测响应无效")
	}
	return reply.Token, nil
}

func (s *Session) Token(ctx context.Context, exe string) (string, error) {
	kind, e := Format(s.Auth.AccessToken)
	if e != nil {
		return "", e
	}
	if kind == "plaintext" {
		var token string
		_ = json.Unmarshal(s.Auth.AccessToken, &token)
		return token, nil
	}
	path, e := Runtime(exe)
	if e != nil {
		return "", e
	}
	return invoke(ctx, path, "decrypt", s.Auth.AccessToken)
}

func (s *Session) Doctor(ctx context.Context, exe string) (Diagnosis, error) {
	kind, e := Format(s.Auth.AccessToken)
	if e != nil {
		return Diagnosis{}, e
	}
	d := Diagnosis{Result: "AUTH_READY", Format: kind, Report: "本地凭据格式有效；未验证解密或服务端登录状态"}
	if kind == "sym-v1" {
		d.Runtime, e = Runtime(exe)
		if e == nil {
			_, e = invoke(ctx, d.Runtime, "probe", nil)
		}
	}
	return d, e
}
