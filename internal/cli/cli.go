// Package cli owns command contracts, presentation, and exit codes.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/spf13/cobra"
	"workbuddy-cli/internal/auth"
	"workbuddy-cli/internal/config"
	"workbuddy-cli/internal/workbuddy"
)

// Version may be set from a release tag using go build -ldflags -X.
var Version = "0.1.1"

type usageError struct{ error }

// Run is safe to invoke repeatedly in tests; only main terminates the process.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var configPath, authFile, runtimePath, output, timeout, budget string
	var runErr error
	var exitCode int
	root := &cobra.Command{Use: "workbuddy-cli", Short: "WorkBuddy 签到与成长中心命令行工具", SilenceUsage: true, SilenceErrors: true, CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true}}
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)
	f := root.PersistentFlags()
	f.StringVarP(&configPath, "config", "c", "", "配置文件（显式路径 > ./config.toml > XDG/~/.config/workbuddy-cli/config.toml）")
	f.StringVar(&authFile, "auth-file", "", "本机登录态文件路径")
	f.StringVar(&runtimePath, "runtime", "", "WorkBuddy 客户端可执行文件（加密凭据需要）")
	f.StringVarP(&output, "output", "o", "text", "输出格式：text 或 json")
	f.StringVar(&timeout, "timeout", "", "单请求超时，例如 30s（至多 10m）")
	f.StringVar(&budget, "budget", "", "整轮执行时限，例如 7m（至多 10m）")
	_ = root.RegisterFlagCompletionFunc("output", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"text", "json"}, cobra.ShellCompDirectiveNoFileComp
	})
	root.SetFlagErrorFunc(func(_ *cobra.Command, e error) error { return usageError{e} })
	add := func(parent *cobra.Command, name, description string, action string) {
		cmd := &cobra.Command{Use: name, Short: description, Args: func(_ *cobra.Command, a []string) error {
			if len(a) > 0 {
				return usageError{errors.New("此命令不接受位置参数")}
			}
			return nil
		}}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			if output != "text" && output != "json" {
				return usageError{errors.New("--output 仅支持 text 或 json")}
			}
			cfg, e := config.Load(configPath)
			if e != nil {
				runErr = e
				exitCode = 1
				return e
			}
			if cmd.Flags().Changed("auth-file") {
				cfg.AuthFile = authFile
			}
			if cmd.Flags().Changed("runtime") {
				cfg.Runtime = runtimePath
			}
			if cmd.Flags().Changed("timeout") {
				cfg.Timeout = timeout
			}
			if cmd.Flags().Changed("budget") {
				cfg.Budget = budget
			}
			requestTimeout, e := config.Duration(cfg.Timeout, "timeout")
			if e != nil {
				return usageError{e}
			}
			total, e := config.Duration(cfg.Budget, "budget")
			if e != nil {
				return usageError{e}
			}
			taskCtx, cancel := context.WithTimeout(ctx, total)
			defer cancel()
			path, e := auth.Find(cfg.AuthFile)
			if e != nil {
				runErr = e
				exitCode = 1
				return e
			}
			session, e := auth.Read(path)
			if e != nil {
				runErr = e
				exitCode = 1
				return e
			}
			var value any
			if action == "doctor" {
				value, e = session.Doctor(taskCtx, cfg.Runtime)
			} else {
				token, err := session.Token(taskCtx, cfg.Runtime)
				if err != nil {
					runErr = err
					exitCode = 1
					return err
				}
				client, err := workbuddy.New(session.Auth.Endpoint, token, session.Account.UID, session.Account.EnterpriseID, session.Auth.Domain, requestTimeout)
				if err != nil {
					runErr = err
					exitCode = 1
					return err
				}
				switch action {
				case "status":
					var data any
					data, e = client.Status(taskCtx)
					value = workbuddy.Result{Result: "STATUS", Data: data, NeedsAttention: e != nil}
				case "signin":
					value, e = client.Signin(taskCtx)
				case "growth-status":
					value, e = client.GrowthStatus(taskCtx)
				case "growth-run":
					value, e = client.Growth(taskCtx)
				case "auto":
					value, e = client.Auto(taskCtx)
				}
				if e != nil {
					if r, ok := value.(workbuddy.Result); ok {
						r.NeedsAttention = true
						value = r
					}
				}
				value = client.Sanitize(value)
			}
			if e != nil {
				runErr = e
				exitCode = 1
			}
			if e != nil && action == "doctor" {
				return e
			}
			if err := emit(stdout, output, value); err != nil {
				runErr = errors.New("输出失败")
				exitCode = 1
				return runErr
			}
			return e
		}
		parent.AddCommand(cmd)
	}
	add(root, "doctor", "离线检查凭据与运行时；不解密、不联网", "doctor")
	add(root, "status", "查询签到状态", "status")
	add(root, "signin", "先查状态，未签到才领取每日积分", "signin")
	add(root, "auto", "执行签到和一轮成长中心（含补登、兑换与盲盒）", "auto")
	growth := &cobra.Command{Use: "growth", Short: "成长中心状态与奖励"}
	root.AddCommand(growth)
	add(growth, "status", "查询旅行、任务、连登、抽奖与能量", "growth-status")
	add(growth, "run", "领取奖励、派 Buddy、接任务、补登、兑换并开盲盒", "growth-run")
	completion := &cobra.Command{Use: "completion [bash|zsh|fish]", Short: "生成 shell 补全脚本（不读取凭据、不联网）", ValidArgs: []string{"bash", "zsh", "fish"}, Args: func(_ *cobra.Command, a []string) error {
		if len(a) != 1 {
			return usageError{errors.New("必须指定 bash、zsh 或 fish")}
		}
		return nil
	}, RunE: func(cmd *cobra.Command, a []string) error {
		switch a[0] {
		case "bash":
			return root.GenBashCompletionV2(cmd.OutOrStdout(), true)
		case "zsh":
			return root.GenZshCompletion(cmd.OutOrStdout())
		case "fish":
			return root.GenFishCompletion(cmd.OutOrStdout(), true)
		}
		return usageError{errors.New("不支持的 shell")}
	}}
	root.AddCommand(completion)
	version := &cobra.Command{Use: "version", Short: "显示版本", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		if output == "json" {
			return emit(stdout, output, map[string]any{"name": "workbuddy-cli", "version": Version})
		}
		if output != "text" {
			return usageError{errors.New("--output 仅支持 text 或 json")}
		}
		_, e := fmt.Fprintln(stdout, "workbuddy-cli "+Version)
		return e
	}}
	root.AddCommand(version)
	e := root.ExecuteContext(ctx)
	if e == nil {
		return 0
	}
	var u usageError
	if errors.As(e, &u) || (runErr == nil && exitCode == 0) {
		exitCode = 2
	}
	fmt.Fprintln(stderr, "错误：", e)
	return exitCode
}

func emit(w io.Writer, format string, v any) error {
	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		return enc.Encode(v)
	}
	// Render sorted fields for readable, deterministic logs. Quote user-controlled
	// strings so escape sequences and newlines cannot alter the terminal display.
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	var normalized any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if e = dec.Decode(&normalized); e != nil {
		return e
	}
	var render func(any, string) error
	render = func(x any, prefix string) error {
		switch t := x.(type) {
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if e := render(t[k], prefix+k+"."); e != nil {
					return e
				}
			}
		case []any:
			for i, item := range t {
				if e := render(item, fmt.Sprintf("%s%d.", prefix, i+1)); e != nil {
					return e
				}
			}
		default:
			key := prefix
			if len(key) > 0 {
				key = key[:len(key)-1]
			}
			value := fmt.Sprint(x)
			if s, ok := x.(string); ok {
				value = strconv.Quote(s)
			}
			_, e := fmt.Fprintf(w, "%s: %s\n", strconv.Quote(key), value)
			return e
		}
		return nil
	}
	return render(normalized, "")
}
