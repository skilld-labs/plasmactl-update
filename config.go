package plasmactlupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/launchrctl/launchr"
	"gopkg.in/yaml.v3"
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

// GetDefaultConfig returns the global default configuration
func getUpdateConfig() *config {
	return updateConfig
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
	v := reflect.ValueOf(cfg).Elem()
	t := reflect.TypeOf(cfg).Elem()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		// Check if field is empty
		if field.Kind() == reflect.String && field.String() == "" {
			yamlTag := fieldType.Tag.Get("yaml")
			if yamlTag == "" {
				yamlTag = fieldType.Name
			}
			return fmt.Errorf("field '%s' is required and cannot be empty", yamlTag)
		}
	}

	return nil
}
