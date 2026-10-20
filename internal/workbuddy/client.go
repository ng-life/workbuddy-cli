// Package workbuddy implements WorkBuddy's unofficial check-in and growth API.
package workbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultEndpoint = "https://copilot.tencent.com"
const growthBase = "/v2/activity/growth"

type Client struct {
	endpoint string
	headers  http.Header
	http     *http.Client
}

type APIError struct {
	Status int
	Kind   string
}

func (e *APIError) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("%s（HTTP %d）", e.Kind, e.Status)
	}
	return e.Kind
}
func fatal(err error) bool {
	var e *APIError
	return errors.As(err, &e) && (e.Status == 401 || e.Status == 403 || e.Status == 0)
}

func New(endpoint, token, uid, enterprise, domain string, timeout time.Duration) (*Client, error) {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(endpoint, " \t\r\n") {
		return nil, errors.New("登录态 endpoint 必须是无用户信息、查询或片段的 HTTPS 地址")
	}
	h := http.Header{"Accept": {"application/json"}, "Content-Type": {"application/json"}, "Authorization": {"Bearer " + token}, "X-User-Id": {uid}, "User-Agent": {"WorkBuddy"}}
	if enterprise != "" {
		h.Set("X-Enterprise-Id", enterprise)
		h.Set("X-Tenant-Id", enterprise)
	}
	if domain != "" {
		h.Set("X-Domain", domain)
	}
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), headers: h, http: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// call never retries writes, including ambiguous network outcomes. Response text is
// not returned in errors because upstream messages may contain credential values.
func (c *Client) call(ctx context.Context, method, path string, payload any) (any, error) {
	var data []byte
	var err error
	if method == http.MethodPost {
		if payload == nil {
			payload = map[string]any{}
		}
		data, err = json.Marshal(payload)
		if err != nil {
			return nil, errors.New("请求参数无法编码")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("无法创建 API 请求")
	}
	req.Header = c.headers.Clone()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &APIError{Kind: "网络请求失败、超时或已取消；写入结果可能未知，请查询状态后再运行"}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil || len(b) > 2*1024*1024 {
		return nil, &APIError{Status: resp.StatusCode, Kind: "API 响应读取失败或超过 2 MiB"}
	}
	var value any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&value) != nil {
		return nil, &APIError{Status: resp.StatusCode, Kind: "API 响应不是有效 JSON"}
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, &APIError{Status: resp.StatusCode, Kind: "API 响应包含多余内容"}
	}
	if resp.StatusCode == 403 && path == growthBase+"/redeem" && strings.Contains(text(Dig(value, "msg")), "天数不足") {
		return map[string]any{"skipped": true, "reason": "连登档位未解锁"}, nil
	}
	if (resp.StatusCode == 400 || (resp.StatusCode >= 200 && resp.StatusCode < 300)) && path == "/v2/billing/meter/daily-checkin" && number(Dig(value, "code")) == 10001 {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		kind := "API 请求被拒绝"
		if resp.StatusCode == 401 {
			kind = "登录态已失效，请重新登录客户端"
		}
		if resp.StatusCode == 403 {
			kind = "API 权限或业务条件不满足"
		}
		return nil, &APIError{Status: resp.StatusCode, Kind: kind}
	}
	if err := businessError(value, resp.StatusCode); err != nil {
		return nil, err
	}
	return value, nil
}

func businessError(v any, status int) error {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	if code, ok := m["code"]; ok {
		n, valid := code.(json.Number)
		if !valid || n.String() != "0" {
			return &APIError{Status: status, Kind: "API 返回业务错误"}
		}
	}
	for _, k := range []string{"data", "result", "resp", "response"} {
		if err := businessError(m[k], status); err != nil {
			return err
		}
	}
	return nil
}

func Dig(v any, key string) any { return dig(v, key, 0) }
func dig(v any, key string, depth int) any {
	if depth > 32 {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		if x, exists := m[key]; exists && x != nil {
			return x
		}
		for _, k := range []string{"data", "result", "resp", "response"} {
			if x := dig(m[k], key, depth+1); x != nil {
				return x
			}
		}
	}
	return nil
}
func number(v any) int {
	if n, ok := v.(json.Number); ok {
		i, e := n.Int64()
		if e == nil && i >= 0 && i <= 1000000 {
			return int(i)
		}
	}
	return 0
}
func text(v any) string { s, _ := v.(string); return s }
func yes(v any) bool    { return v == true || number(v) == 1 }
func list(v any) []any  { a, _ := v.([]any); return a }

// Result is stable machine output. Failures retain earlier successful steps.
type Result struct {
	Result         string `json:"result"`
	Data           any    `json:"data,omitempty"`
	Steps          []Step `json:"steps,omitempty"`
	NeedsAttention bool   `json:"needs_attention"`
}
type Step struct {
	Action string `json:"action"`
	Result string `json:"result"`
	Data   any    `json:"data,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (c *Client) Status(ctx context.Context) (any, error) {
	return c.call(ctx, "POST", "/v2/billing/meter/checkin-activity-status", nil)
}
func (c *Client) Signin(ctx context.Context) (Result, error) {
	r := Result{Result: "UNKNOWN"}
	status, err := c.Status(ctx)
	if err != nil {
		return r, err
	}
	r.Data = status
	if Dig(status, "active") == false {
		r.Result = "INACTIVE"
		return r, nil
	}
	if yes(Dig(status, "today_checked_in")) {
		r.Result = "ALREADY"
		return r, nil
	}
	// Fail closed on schema drift: missing status must never imply permission to write.
	if Dig(status, "active") != true || (Dig(status, "today_checked_in") != false && Dig(status, "today_checked_in") != json.Number("0")) {
		return r, errors.New("签到状态字段缺失或类型异常，已停止领取")
	}
	claim, err := c.call(ctx, "POST", "/v2/billing/meter/daily-checkin", nil)
	if err != nil {
		return r, err
	}
	if claim == nil {
		r.Result = "ALREADY"
		return r, nil
	}
	if credit, ok := Dig(claim, "credit").(json.Number); ok {
		if _, e := credit.Float64(); e == nil {
			r.Result = "CLAIMED"
			r.Data = claim
			return r, nil
		}
	}
	return r, errors.New("未识别的签到领取响应，请查询状态确认结果")
}

func (c *Client) GrowthStatus(ctx context.Context) (Result, error) {
	r := Result{Result: "GROWTH_STATUS", Steps: []Step{}}
	for _, path := range []string{"/buddy/travel/status", "/tasks", "/streak", "/redeem/summary", "/lottery/chances", "/buddy/quota", "/energy"} {
		v, e := c.call(ctx, "GET", growthBase+path, nil)
		s := Step{Action: path, Result: "OK", Data: v}
		if e != nil {
			s.Result = "ERROR"
			s.Error = e.Error()
			r.NeedsAttention = true
		}
		r.Steps = append(r.Steps, s)
		if e != nil && fatal(e) {
			return r, e
		}
	}
	if r.NeedsAttention {
		return r, errors.New("部分成长中心状态查询失败")
	}
	return r, nil
}

// Sanitize removes credentials even if the server accidentally echoes them.
func (c *Client) Sanitize(value any) any {
	b, e := json.Marshal(value)
	if e != nil {
		return nil
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return nil
	}
	secrets := []string{strings.TrimPrefix(c.headers.Get("Authorization"), "Bearer ")}
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case string:
			for _, secret := range secrets {
				if secret != "" {
					t = strings.ReplaceAll(t, secret, "[REDACTED]")
				}
			}
			return t
		case map[string]any:
			out := make(map[string]any, len(t))
			for k, v := range t {
				lower := strings.ToLower(k)
				if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "authorization") {
					out[k] = "[REDACTED]"
				} else {
					out[walk(k).(string)] = walk(v)
				}
			}
			return out
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
			return t
		}
		return x
	}
	return walk(v)
}
