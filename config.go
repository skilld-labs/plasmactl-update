package plasmactlupdate

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/launchrctl/launchr"
	"gopkg.in/yaml.v3"
)

const (
	defaultPinnedReleaseTpl = "{{.URL}}/stable_release"
	defaultBinTpl           = "{{.URL}}/{{.Version}}/launchr_{{.OS}}_{{.Arch}}{{.Ext}}"
)

type config struct {
	RepositoryURL string `yaml:"repository_url"`
	PinnedRelease string `yaml:"pinned_release_file"`
	BinMask       string `yaml:"bin_mask"`
}

// Global variable for update config
var updateConfig *config

// SetUpdateConfig sets the global default configuration of update
func SetUpdateConfig(cfg *config) {
	updateConfig = cfg
}

// GetDefaultConfig returns the global default configuration copy
func getUpdateConfig() *config {
	if updateConfig == nil {
		return &config{}
	}
	cfgCopy := *updateConfig
	return &cfgCopy
}

// LoadConfigFromBytesAndSet parses the configuration from a byte slice and sets it as the global default configuration.
func LoadConfigFromBytesAndSet(data []byte) error {
	cfg, err := parseConfigFromBytes(data)
	if err != nil {
		launchr.Term().Error().Println("Failed to parse default config")
		// set empty config
		cfg = &config{}
	}

	SetUpdateConfig(cfg)
	return nil
}

// ParseConfigFromPath reads and parses YAML config from a file path
func parseConfigFromPath(path string) (*config, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	return parseConfigFromBytes(data)
}

// ParseConfigFromBytes parses YAML config from embedded []byte
func parseConfigFromBytes(data []byte) (*config, error) {
	var cfg config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config: %w", err)
	}

	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// validateConfig checks if all fields are filled and not empty
func validateConfig(cfg *config) error {
	if cfg.RepositoryURL == "" {
		return fmt.Errorf("field 'repository_url' is required and cannot be empty")
	}

	return nil
}

type templateVars struct {
	URL     string
	Version string
	OS      string
	Arch    string
	Ext     string
}

// formatURL formats a template string with the provided variables
func formatURL(templateStr string, vars templateVars) (string, error) {
	tmpl, err := template.New("url").Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, vars)
	if err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	result := strings.TrimSpace(buf.String())

	// Validate the resulting URL
	if err = validateURL(result); err != nil {
		return "", fmt.Errorf("invalid URL generated from template: %w", err)
	}

	return result, nil
}

// validateURL checks if the URL is valid and doesn't contain unwanted characters
func validateURL(rawURL string) error {
	// Check for newlines, tabs, and other control characters
	if strings.ContainsAny(rawURL, "\n\r\t\v\f") {
		return fmt.Errorf("URL contains invalid control characters")
	}

	// Parse the URL to ensure it's valid
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	// Check if scheme is present and valid
	if parsedURL.Scheme == "" {
		return fmt.Errorf("URL missing scheme (http/https)")
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("invalid URL scheme: %s (expected http or https)", parsedURL.Scheme)
	}

	// Check if host is present
	if parsedURL.Host == "" {
		return fmt.Errorf("URL missing host")
	}

	return nil
}
