package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
)

// Global config variables. These are kept here to centralise access. The
// configuration is loaded from disk at program startup and saved whenever
// modifications occur. Access is guarded by cfgMutex.
var (
	cfgFile  string
	cfg      *Config
	cfgMutex sync.Mutex
)

// defaultConfig returns an empty configuration with no jobs defined. It is
// used when the configuration file cannot be read or parsed.
func defaultConfig() *Config {
	return &Config{Jobs: []Job{}}
}

// configDir returns the application configuration directory. It uses
// os.UserConfigDir to determine a per-user directory and creates the
// subdirectory "shed_1_0" inside it. If the user configuration directory
// cannot be determined the current working directory is used instead.
func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	appDir := filepath.Join(dir, "shed_1_0")
	_ = os.MkdirAll(appDir, 0755)
	return appDir
}

// loadConfig reads the configuration from disk. If the file does not exist
// a default configuration is returned. Any parse errors are reported to
// stderr and the default configuration is returned.
func loadConfig() *Config {
	cfgMutex.Lock()
	defer cfgMutex.Unlock()

	if cfg != nil {
		return cfg
	}
	appDir := configDir()
	cfgFile = filepath.Join(appDir, "config.json")
	logPrintf("[config] Using config.json at: %s", cfgFile)

	data, err := ioutil.ReadFile(cfgFile)
	if err != nil {
		cfg = defaultConfig()
		return cfg
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not parse config, using defaults: %v\n", err)
		cfg = defaultConfig()
		return cfg
	}
	cfg = &c
	return cfg
}

// saveConfig writes the configuration to disk. It marshals the structure
// into human readable JSON. Errors are returned to the caller.
func saveConfig(c *Config) error {
	cfgMutex.Lock()
	defer cfgMutex.Unlock()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(cfgFile, b, 0644)
}
