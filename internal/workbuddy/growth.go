package workbuddy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"time"
)

func clientToken() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", errors.New("无法生成请求幂等标识")
	}
	return "u" + hex.EncodeToString(b[:]), nil
}

// Growth executes one bounded round. Each mutation is issued at most once;
// module failures remain visible while independent modules may still proceed.
func (c *Client) Growth(ctx context.Context) (Result, error) {
	r := Result{Result: "GROWTH", Steps: []Step{}}
	var stopped bool
	request := func(method, path string, payload any) (any, error) {
		if stopped {
			return nil, errors.New("认证、权限或网络异常，已停止后续请求")
		}
		v, e := c.call(ctx, method, growthBase+path, payload)
		if e == nil && method == "GET" {
			e = validateGrowthState(path, v)
		}
		step := Step{Action: method + " " + path, Result: "OK", Data: v}
		if Dig(v, "skipped") == true {
			step.Result = "SKIPPED"
		}
		if e != nil {
			step.Result = "ERROR"
			step.Error = e.Error()
			r.NeedsAttention = true
			if fatal(e) {
				stopped = true
			}
		}
		r.Steps = append(r.Steps, step)
		return v, e
	}
	fail := func(action string, e error) {
		r.NeedsAttention = true
		r.Steps = append(r.Steps, Step{Action: action, Result: "ERROR", Error: e.Error()})
	}
	write := func(path string, payload map[string]any, token bool) (any, error) {
		if token {
			v, e := clientToken()
			if e != nil {
				fail(path, e)
				return nil, e
			}
			payload["client_token"] = v
		}
		return request("POST", path, payload)
	}
	travel, e := request("GET", "/buddy/travel/status", nil)
	if e == nil {
		state := text(Dig(travel, "state"))
		if state == "arrived" {
			id := Dig(travel, "record_id")
			if id == nil {
				fail("travel", errors.New("到达状态缺少 record_id，未领取或派出"))
			} else {
				reward, err := write("/buddy/travel/claim", map[string]any{"record_id": id}, false)
				if err == nil && Dig(reward, "reward_credit") != nil {
					state = "idle"
				} else if err == nil {
					fail("travel", errors.New("未识别的旅行领奖响应，未继续派出"))
				}
			}
		}
		if state == "idle" && Dig(travel, "daily_limit_reached") != true {
			cfg, err := request("GET", "/buddy/travel/config", nil)
			if err == nil {
				locations := list(Dig(cfg, "locations"))
				if len(locations) > 0 && Dig(locations[0], "id") != nil {
					_, _ = write("/buddy/travel/depart", map[string]any{"location_id": Dig(locations[0], "id")}, false)
				}
			}
		}
	}
	tasks, e := request("GET", "/tasks", nil)
	if e == nil {
		pending := []string{}
		for _, t := range list(Dig(tasks, "tasks")) {
			if Dig(t, "locked") == true {
				continue
			}
			code := text(Dig(t, "task_code"))
			if code == "" {
				continue
			}
			switch text(Dig(t, "accept_status")) {
			case "not_accepted":
				pending = append(pending, code)
			case "completed":
				_, _ = write("/tasks/"+url.PathEscape(code)+"/claim", map[string]any{}, false)
			}
		}
		for i := 0; i < len(pending) && !stopped; i += 20 {
			end := min(i+20, len(pending))
			v, err := write("/tasks/accept", map[string]any{"task_codes": pending[i:end]}, false)
			if err == nil {
				results, ok := Dig(v, "results").([]any)
				if !ok || len(results) != end-i {
					fail("tasks/accept", errors.New("任务接单响应条数异常"))
				} else {
					for _, res := range results {
						if text(Dig(res, "status")) != "ok" {
							fail("tasks/accept", errors.New("任务未能确认接单成功，请在客户端查看前置条件"))
						}
					}
				}
			}
		}
	}
	streak, e := request("GET", "/streak", nil)
	if e == nil {
		cards := Dig(streak, "makeup_cards")
		if v := Dig(cards, "balance"); v != nil {
			cards = v
		}
		if number(cards) > 0 {
			heat, err := request("GET", "/heatmap", nil)
			if err == nil {
				dates, err := makeupCandidates(streak, heat)
				if err != nil {
					fail("makeup", err)
				} else if len(dates) > 0 {
					_, _ = write("/makeup-cards/use", map[string]any{"target_date": dates[0]}, false)
				}
			}
		}
	}
	summary, e := request("GET", "/redeem/summary", nil)
	if e == nil {
		for _, tier := range []struct{ ID, Key string }{{"7d", "starter_status"}, {"14d", "advanced_status"}, {"28d", "legendary_status"}} {
			state := text(Dig(summary, tier.Key))
			if state != "" && state != "claimed" && state != "locked" {
				_, _ = write("/redeem", map[string]any{"tier": tier.ID}, true)
			}
		}
	}
	chances, e := request("GET", "/lottery/chances", nil)
	if e == nil && number(Dig(chances, "balance")) > 0 {
		_, _ = write("/lottery/draw", map[string]any{}, true)
	}
	quota, e := request("GET", "/buddy/quota", nil)
	if e == nil {
		affordable := number(Dig(quota, "affordable"))
		limit := number(Dig(quota, "max_open_count"))
		if limit == 0 {
			limit = 1
		}
		if affordable > 0 {
			_, _ = write("/buddy/open", map[string]any{"count": min(affordable, limit)}, true)
		}
	}
	if r.NeedsAttention {
		return r, errors.New("成长中心存在失败步骤，请查看 steps；已完成的操作不会回滚")
	}
	return r, nil
}

// makeupCandidates rejects incomplete/conflicting calendars instead of inferring
// absent dates as broken days. Server dates, not the machine's timezone, govern.
func makeupCandidates(streak, heat any) ([]string, error) {
	invalid := errors.New("补登日历缺失或格式异常，未消耗补登卡")
	parse := func(v any) (time.Time, error) {
		s := text(v)
		d, e := time.Parse("2006-01-02", s)
		if e != nil || d.Format("2006-01-02") != s {
			return time.Time{}, invalid
		}
		return d, nil
	}
	today, e := parse(Dig(Dig(heat, "today"), "date"))
	if e != nil {
		return nil, e
	}
	launch, e := parse(Dig(streak, "launch_date"))
	if e != nil {
		return nil, e
	}
	start := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	floor, _ := time.Parse("2006-01-02", "2026-06-17")
	if launch.After(start) {
		start = launch
	}
	if floor.After(start) {
		start = floor
	}
	s, ok := Dig(streak, "streak").(map[string]any)
	if !ok {
		return nil, invalid
	}
	made := map[string]bool{}
	if raw, exists := s["makeup_dates"]; exists {
		dates, ok := raw.([]any)
		if !ok {
			return nil, invalid
		}
		for _, v := range dates {
			d, e := parse(v)
			if e != nil {
				return nil, e
			}
			made[d.Format("2006-01-02")] = true
		}
	}
	cells, ok := Dig(heat, "cells").([]any)
	if !ok {
		return nil, invalid
	}
	scores := map[string]float64{}
	for _, cell := range cells {
		d, e := parse(Dig(cell, "date"))
		if e != nil {
			return nil, e
		}
		n, ok := Dig(cell, "score").(json.Number)
		if !ok {
			return nil, invalid
		}
		score, e := n.Float64()
		if e != nil || score < 0 {
			return nil, invalid
		}
		key := d.Format("2006-01-02")
		if old, exists := scores[key]; exists && old != score {
			return nil, invalid
		}
		scores[key] = score
	}
	result := []string{}
	for date, score := range scores {
		d, _ := parse(date)
		if score == 0 && !d.Before(start) && d.Before(today) && !made[date] {
			result = append(result, date)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(result)))
	return result, nil
}

func (c *Client) Auto(ctx context.Context) (Result, error) {
	signin, e := c.Signin(ctx)
	r := Result{Result: "AUTO", Steps: []Step{{Action: "signin", Result: signin.Result, Data: signin.Data}}}
	if e != nil {
		r.Steps[0].Error = e.Error()
		r.NeedsAttention = true
		if fatal(e) {
			return r, e
		}
	}
	growth, ge := c.Growth(ctx)
	r.Steps = append(r.Steps, growth.Steps...)
	r.NeedsAttention = r.NeedsAttention || growth.NeedsAttention
	if e != nil || ge != nil {
		return r, fmt.Errorf("自动领取存在失败步骤，请查看 steps")
	}
	return r, nil
}

// Require the state fields used to decide writes; a changed API must be reported,
// not interpreted as an empty task list or a zero balance.
func validateGrowthState(path string, v any) error {
	invalid := errors.New("成长中心状态字段缺失或类型异常，已跳过此模块")
	switch path {
	case "/buddy/travel/status":
		state := text(Dig(v, "state"))
		if state != "idle" && state != "arrived" && state != "traveling" {
			return invalid
		}
	case "/tasks", "/buddy/travel/config":
		key := "tasks"
		if path == "/buddy/travel/config" {
			key = "locations"
		}
		if _, ok := Dig(v, key).([]any); !ok {
			return invalid
		}
	case "/streak":
		cards := Dig(v, "makeup_cards")
		if balance := Dig(cards, "balance"); balance != nil {
			cards = balance
		}
		if _, ok := cards.(json.Number); !ok {
			return invalid
		}
	case "/redeem/summary":
		for _, key := range []string{"starter_status", "advanced_status", "legendary_status"} {
			if text(Dig(v, key)) == "" {
				return invalid
			}
		}
	case "/lottery/chances":
		if _, ok := Dig(v, "balance").(json.Number); !ok {
			return invalid
		}
	case "/buddy/quota":
		if _, ok := Dig(v, "affordable").(json.Number); !ok {
			return invalid
		}
	}
	return nil
}
