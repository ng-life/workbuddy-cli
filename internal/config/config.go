// Package config selects one configuration file; files are never implicitly merged.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	AuthFile string `toml:"auth_file"`
	Runtime  string `toml:"runtime"`
	Timeout  string `toml:"timeout"`
	Budget   string `toml:"budget"`
}

func Load(explicit string) (Config, error) {
	c := Config{Timeout: "30s", Budget: "7m"}
	paths := []string{explicit}
	if explicit == "" {
		root := os.Getenv("XDG_CONFIG_HOME")
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return c, errors.New("无法定位用户目录")
			}
			root = filepath.Join(home, ".config")
		}
		paths = []string{"config.toml", filepath.Join(root, "workbuddy-cli", "config.toml")}
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) && explicit == "" {
			continue
		}
		if err != nil {
			return c, errors.New("无法读取配置文件，请检查 --config 路径和权限")
		}
		if len(b) > 65536 {
			return c, errors.New("配置文件超过 64 KiB")
		}
		if toml.Unmarshal(b, &c) != nil {
			return c, errors.New("配置文件 TOML 格式无效")
		}
		break
	}
	if v := os.Getenv("WORKBUDDY_AUTH_FILE"); v != "" {
		c.AuthFile = v
	}
	if v := os.Getenv("WORKBUDDY_EXE"); v != "" {
		c.Runtime = v
	}
	return c, nil
}

func Duration(value, name string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 || d > 10*time.Minute {
		return 0, fmt.Errorf("%s 必须为大于 0、至多 10m 的时长", name)
	}
	return d, nil
}
