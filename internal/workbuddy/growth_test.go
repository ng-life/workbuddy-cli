package workbuddy

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Stateful fixtures model the server-side record that opening a Buddy replaces.
func TestBuddyTravelLifecycle(t *testing.T) {
	cases := []struct {
		name, state, statusReply, claimReply string
		statusCode, claimCode, departCode    int
		limit                                bool
		want                                 []string
		fail                                 bool
	}{
		{name: "idle opens before departing", state: "idle", want: []string{"/buddy/open", "/buddy/travel/depart"}},
		{name: "traveling preserves trip", state: "traveling", want: []string{}},
		{name: "arrival claimed before open and depart", state: "arrived", want: []string{"/buddy/travel/claim", "/buddy/open", "/buddy/travel/depart"}},
		{name: "failed claim preserves gift", state: "arrived", claimCode: 500, want: []string{"/buddy/travel/claim"}, fail: true},
		{name: "business failure with reward is not success", state: "arrived", claimReply: `{"code":12,"reward_credit":50}`, want: []string{"/buddy/travel/claim"}, fail: true},
		{name: "unknown status prevents mutations", state: "unknown", want: []string{}, fail: true},
		{name: "business failure with idle is not permission", state: "idle", statusReply: `{"code":12,"state":"idle"}`, want: []string{}, fail: true},
		{name: "status server error prevents mutations", state: "idle", statusCode: 500, want: []string{}, fail: true},
		{name: "daily limit permits open only", state: "idle", limit: true, want: []string{"/buddy/open"}},
		{name: "no active buddy is skipped", state: "idle", departCode: 403, want: []string{"/buddy/open", "/buddy/travel/depart"}},
		{name: "late auth error retains reward", state: "arrived", departCode: 401, want: []string{"/buddy/travel/claim", "/buddy/open", "/buddy/travel/depart"}, fail: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := tc.state
			writes := []string{}
			tasksClaimed := false
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				path := strings.TrimPrefix(r.URL.Path, growthBase)
				if r.Method == "POST" {
					if path == "/tasks/reward/claim" {
						tasksClaimed = true
						io.WriteString(w, `{"credit":300}`)
						return
					}
					writes = append(writes, path)
					switch path {
					case "/buddy/travel/claim":
						if tc.claimCode != 0 {
							w.WriteHeader(tc.claimCode)
						}
						if tc.claimReply != "" {
							io.WriteString(w, tc.claimReply)
							return
						}
						if tc.claimCode == 0 {
							state = "idle"
						}
						io.WriteString(w, `{"reward_credit":50}`)
					case "/buddy/open":
						if state != "idle" {
							t.Errorf("opening erased %s travel record", state)
						}
						state = "idle"
						io.WriteString(w, `{"buddy":"new"}`)
					case "/buddy/travel/depart":
						if tc.departCode != 0 {
							w.WriteHeader(tc.departCode)
							io.WriteString(w, `{"msg":"no active buddy"}`)
							return
						}
						state = "traveling"
						io.WriteString(w, `{}`)
					default:
						t.Errorf("unexpected write %s", path)
					}
					return
				}
				switch path {
				case "/buddy/travel/status":
					if tc.statusCode != 0 {
						w.WriteHeader(tc.statusCode)
					}
					if tc.statusReply != "" {
						io.WriteString(w, tc.statusReply)
					} else {
						io.WriteString(w, `{"state":"`+tc.state+`","record_id":"r","daily_limit_reached":`+map[bool]string{true: "true", false: "false"}[tc.limit]+`}`)
					}
				case "/tasks":
					io.WriteString(w, `{"tasks":[{"task_code":"reward","accept_status":"completed"}]}`)
				case "/streak":
					io.WriteString(w, `{"makeup_cards":0}`)
				case "/redeem/summary":
					io.WriteString(w, `{"starter_status":"locked","advanced_status":"locked","legendary_status":"locked"}`)
				case "/lottery/chances":
					io.WriteString(w, `{"balance":0}`)
				case "/buddy/quota":
					io.WriteString(w, `{"affordable":1}`)
				case "/buddy/travel/config":
					io.WriteString(w, `{"locations":[{"id":1}]}`)
				default:
					t.Errorf("unexpected read %s", path)
				}
			})
			r, e := c.Growth(context.Background())
			if (e != nil) != tc.fail || r.NeedsAttention != tc.fail {
				t.Fatalf("result=%+v error=%v", r, e)
			}
			if !reflect.DeepEqual(writes, tc.want) {
				t.Fatalf("writes=%v want=%v", writes, tc.want)
			}
			if !tasksClaimed {
				t.Error("independent task rewards skipped")
			}
			if tc.name == "late auth error retains reward" {
				found := false
				for _, step := range r.Steps {
					if step.Action == "POST /buddy/travel/claim" && Dig(step.Data, "reward_credit") != nil {
						found = true
					}
				}
				if !found {
					t.Error("earlier reward lost after late failure")
				}
			}
		})
	}
}

func TestNoActiveBuddyExceptionIsEndpointSpecific(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		io.WriteString(w, `{"msg":"no active buddy"}`)
	})
	for _, path := range []string{"/buddy/open", "/tasks/reward/claim"} {
		if _, e := c.call(context.Background(), "POST", growthBase+path, nil); !fatal(e) {
			t.Errorf("403 relaxed for %s: %v", path, e)
		}
	}
}
