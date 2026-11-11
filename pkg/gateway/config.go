package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"tokensaver/pkg/core"
)

type Config struct {
	Core                    core.Config `json:"core"`
	Adapter                 string      `json:"adapter"`
	Listen                  string      `json:"listen"`
	Upstream                string      `json:"upstream"`
	ManagementEnabled       bool        `json:"management_enabled"`
	ManagementTokenEnv      string      `json:"management_token_env"`
	AllowRemoteManagement   bool        `json:"allow_remote_management"`
	MaxRequestBodyBytes     int64       `json:"max_request_body_bytes"`
	ResponseHeaderTimeoutMS int         `json:"response_header_timeout_ms"`
	IdleConnTimeoutMS       int         `json:"idle_conn_timeout_ms"`
	MaxIdleConns            int         `json:"max_idle_conns"`
	MaxIdleConnsPerHost     int         `json:"max_idle_conns_per_host"`
	ShutdownTimeoutMS       int         `json:"shutdown_timeout_ms"`
}

func DefaultConfig() Config {
	return Config{
		Core:                    core.DefaultConfig(),
		Adapter:                 "generic",
		Listen:                  "127.0.0.1:9231",
		Upstream:                "",
		ManagementEnabled:       true,
		ManagementTokenEnv:      "TOKENSAVER_MANAGEMENT_TOKEN",
		AllowRemoteManagement:   false,
		MaxRequestBodyBytes:     32 << 20,
		ResponseHeaderTimeoutMS: 120000,
		IdleConnTimeoutMS:       90000,
		MaxIdleConns:            128,
		MaxIdleConnsPerHost:     64,
		ShutdownTimeoutMS:       10000,
	}
}

func (c *Config) ApplyDefaults() {
	d := DefaultConfig()
	if c.Adapter == "" {
		c.Adapter = d.Adapter
	}
	if c.Listen == "" {
		c.Listen = d.Listen
	}
	if c.ManagementTokenEnv == "" {
		c.ManagementTokenEnv = d.ManagementTokenEnv
	}
	if c.MaxRequestBodyBytes <= 0 {
		c.MaxRequestBodyBytes = d.MaxRequestBodyBytes
	}
	if c.ResponseHeaderTimeoutMS <= 0 {
		c.ResponseHeaderTimeoutMS = d.ResponseHeaderTimeoutMS
	}
	if c.IdleConnTimeoutMS <= 0 {
		c.IdleConnTimeoutMS = d.IdleConnTimeoutMS
	}
	if c.MaxIdleConns <= 0 {
		c.MaxIdleConns = d.MaxIdleConns
	}
	if c.MaxIdleConnsPerHost <= 0 {
		c.MaxIdleConnsPerHost = d.MaxIdleConnsPerHost
	}
	if c.ShutdownTimeoutMS <= 0 {
		c.ShutdownTimeoutMS = d.ShutdownTimeoutMS
	}
	c.Core.ApplyDefaults()
}

func (c Config) Validate() error {
	if c.MaxRequestBodyBytes < 1024 {
		return errors.New("max_request_body_bytes must be >= 1024")
	}
	if c.Upstream == "" {
		return errors.New("upstream is required")
	}
	u, err := url.Parse(c.Upstream)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid upstream URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("upstream scheme must be http or https")
	}
	if u.User != nil {
		return errors.New("upstream URL must not contain credentials")
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if err := c.Core.Validate(); err != nil {
		return fmt.Errorf("core config: %w", err)
	}
	return nil
}

func ListenIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg := DefaultConfig()
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, err
	}
	if err := ensureJSONEOF(dec); err != nil {
		return Config{}, err
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return errors.New("trailing JSON data")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func SaveConfig(path string, cfg Config) error {
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tokensaver-gateway-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
