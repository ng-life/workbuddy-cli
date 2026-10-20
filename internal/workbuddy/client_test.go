package workbuddy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client, e := New(server.URL, "sensitive-token", "user", "org", "domain", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	client.http = server.Client()
	client.http.Timeout = time.Second
	client.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}
func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		t.Fatal(e)
	}
	return v
}

func TestSigninContract(t *testing.T) {
	cases := []struct {
		name, status, claim string
		http                int
		want                string
		writes              int
		fail                bool
	}{
		{"already", `{"active":true,"today_checked_in":true}`, `{}`, 200, "ALREADY", 0, false},
		{"inactive", `{"active":false}`, `{}`, 200, "INACTIVE", 0, false},
		{"credit", `{"data":{"active":true,"today_checked_in":false}}`, `{"data":{"credit":100}}`, 200, "CLAIMED", 1, false},
		{"race-null", `{"active":true,"today_checked_in":false}`, `null`, 200, "ALREADY", 1, false},
		{"race-code", `{"active":true,"today_checked_in":false}`, `{"code":10001}`, 400, "ALREADY", 1, false},
		{"schema-drift", `{"data":{}}`, `{}`, 200, "UNKNOWN", 0, true},
		{"business-error", `{"code":12,"data":{"active":true,"today_checked_in":false}}`, `{}`, 200, "UNKNOWN", 0, true},
		{"no-false-success-on-500", `{"active":true,"today_checked_in":false}`, `{"credit":100}`, 500, "UNKNOWN", 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer sensitive-token" || r.Header.Get("X-Tenant-Id") != "org" {
					t.Error("auth headers missing")
				}
				if r.URL.Path == "/v2/billing/meter/checkin-activity-status" {
					io.WriteString(w, tc.status)
				} else {
					writes++
					w.WriteHeader(tc.http)
					io.WriteString(w, tc.claim)
				}
			})
			r, e := c.Signin(context.Background())
			if (e != nil) != tc.fail || r.Result != tc.want || writes != tc.writes {
				t.Fatalf("result=%+v error=%v writes=%d", r, e, writes)
			}
		})
	}
}

func TestNoRedirectOrWriteRetry(t *testing.T) {
	for _, status := range []int{302, 401, 403, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requests := 0
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Location", "https://example.invalid/steal")
				w.WriteHeader(status)
				io.WriteString(w, `{"msg":"sensitive-token"}`)
			})
			_, e := c.call(context.Background(), "POST", "/write", nil)
			if e == nil || requests != 1 || strings.Contains(e.Error(), "sensitive-token") {
				t.Fatalf("error=%v requests=%d", e, requests)
			}
		})
	}
}

func TestGrowthRoundAndPartialFailure(t *testing.T) {
	var writes []string
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, growthBase)
		if r.Method == "POST" {
			writes = append(writes, path)
			var payload map[string]any
			if json.NewDecoder(r.Body).Decode(&payload) != nil {
				t.Error("missing payload")
			}
			switch path {
			case "/buddy/travel/claim":
				io.WriteString(w, `{"reward_credit":3}`)
			case "/tasks/accept":
				if _, ok := payload["task_codes"].([]any); !ok {
					t.Error("task_codes must be array")
				}
				io.WriteString(w, `{"results":[{"task_code":"new","status":"error"}]}`)
			case "/lottery/draw", "/redeem", "/buddy/open":
				if payload["client_token"] == nil {
					t.Error("missing client token")
				}
				io.WriteString(w, `{"credit_granted":50}`)
			default:
				io.WriteString(w, `{}`)
			}
			return
		}
		responses := map[string]string{
			"/buddy/travel/status": `{"state":"arrived","record_id":"r","daily_limit_reached":false}`,
			"/buddy/travel/config": `{"locations":[{"id":"l"}]}`,
			"/tasks":               `{"tasks":[{"task_code":"new","accept_status":"not_accepted"},{"task_code":"done","accept_status":"completed"},{"task_code":"locked","locked":true,"accept_status":"completed"}]}`,
			"/streak":              `{"makeup_cards":1,"launch_date":"2026-06-17","streak":{"makeup_dates":[]}}`,
			"/heatmap":             `{"today":{"date":"2026-10-20"},"cells":[{"date":"2026-10-18","score":0},{"date":"2026-10-19","score":0}]}`,
			"/redeem/summary":      `{"starter_status":"available","advanced_status":"locked","legendary_status":"claimed"}`,
			"/lottery/chances":     `{"balance":5}`,
			"/buddy/quota":         `{"affordable":4,"max_open_count":2}`,
		}
		value, ok := responses[path]
		if !ok {
			t.Errorf("unexpected path %s", path)
		}
		io.WriteString(w, value)
	})
	r, e := c.Growth(context.Background())
	if e == nil || !r.NeedsAttention {
		t.Fatalf("partial failure hidden: %+v %v", r, e)
	}
	want := []string{"/buddy/travel/claim", "/buddy/travel/depart", "/tasks/done/claim", "/tasks/accept", "/makeup-cards/use", "/redeem", "/lottery/draw", "/buddy/open"}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("writes=%v", writes)
	}
}

func TestGrowthStopsAfterAuthOrNetworkFailure(t *testing.T) {
	requests := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(401); io.WriteString(w, `{}`) })
	r, e := c.Growth(context.Background())
	if e == nil || !r.NeedsAttention || requests != 1 {
		t.Fatalf("requests=%d result=%+v error=%v", requests, r, e)
	}
}

func TestTravelClaimFailureDoesNotDepart(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, growthBase)
		if path == "/buddy/travel/status" {
			io.WriteString(w, `{"state":"arrived","record_id":"r"}`)
			return
		}
		if path == "/buddy/travel/claim" {
			w.WriteHeader(500)
			io.WriteString(w, `{}`)
			return
		}
		if strings.Contains(path, "depart") || strings.Contains(path, "config") {
			t.Error("must not depart after failed claim")
		}
		io.WriteString(w, `{}`)
	})
	_, e := c.Growth(context.Background())
	if e == nil {
		t.Fatal("failure hidden")
	}
}

func TestMakeupCalendarFailClosed(t *testing.T) {
	streak := decode(t, `{"launch_date":"2026-06-17","streak":{"makeup_dates":["2026-10-18"]}}`)
	heat := decode(t, `{"today":{"date":"2026-10-20"},"cells":[{"date":"2026-09-30","score":0},{"date":"2026-10-18","score":0},{"date":"2026-10-19","score":0},{"date":"2026-10-20","score":0}]}`)
	dates, e := makeupCandidates(streak, heat)
	if e != nil || !reflect.DeepEqual(dates, []string{"2026-10-19"}) {
		t.Fatalf("dates=%v error=%v", dates, e)
	}
	for _, raw := range []string{`{}`, `{"today":{"date":"2026-10-20"},"cells":[{"date":"2026-10-19","score":"0"}]}`, `{"today":{"date":"2026-10-20"},"cells":[{"date":"2026-10-19","score":0},{"date":"2026-10-19","score":1}]}`} {
		if _, e := makeupCandidates(streak, decode(t, raw)); e == nil {
			t.Fatalf("accepted invalid calendar %s", raw)
		}
	}
}

func TestOutputRedaction(t *testing.T) {
	c := fixture(t, func(http.ResponseWriter, *http.Request) {})
	b, _ := json.Marshal(c.Sanitize(map[string]any{"message": "echo sensitive-token", "data": map[string]any{"accessToken": "other", "credit": json.Number("12345678901234567890")}}))
	if strings.Contains(string(b), "sensitive-token") || strings.Contains(string(b), "other") || !strings.Contains(string(b), "12345678901234567890") {
		t.Fatal(string(b))
	}
}
