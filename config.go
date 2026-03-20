package main

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	TargetURL         string     `yaml:"target_url"`
	Proxy             ProxyConfig `yaml:"proxy"`
	RequestTimeout    string     `yaml:"request_timeout"`
	ConcurrentWorkers int        `yaml:"concurrent_workers"`
	CheckInterval     string     `yaml:"check_interval"`
	HTTPSPort         string     `yaml:"https_port"`
}

type ProxyConfig struct {
	Host     string `yaml:"host"`
	Port     string `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type parsedConfig struct {
	RequestTimeout    time.Duration
	CheckInterval     time.Duration
}

var cfg *Config
var parsed *parsedConfig

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}

	if c.TargetURL == "" {
		c.TargetURL = "https://next-ws.bale.ai/bale.auth.v1.Auth/"
	}
	if c.Proxy.Host == "" {
		c.Proxy.Host = "p.webshare.io"
	}
	if c.Proxy.Port == "" {
		c.Proxy.Port = "80"
	}
	if c.RequestTimeout == "" {
		c.RequestTimeout = "15s"
	}
	if c.ConcurrentWorkers <= 0 {
		c.ConcurrentWorkers = 50
	}
	if c.CheckInterval == "" {
		c.CheckInterval = "1h"
	}
	if c.HTTPSPort == "" {
		c.HTTPSPort = ":443"
	}

	requestTimeout, err := time.ParseDuration(c.RequestTimeout)
	if err != nil {
		return nil, err
	}
	checkInterval, err := time.ParseDuration(c.CheckInterval)
	if err != nil {
		return nil, err
	}

	cfg = &c
	parsed = &parsedConfig{
		RequestTimeout: requestTimeout,
		CheckInterval:  checkInterval,
	}

	return &c, nil
}

func GetConfig() *Config {
	return cfg
}

func GetParsedConfig() *parsedConfig {
	return parsed
}
