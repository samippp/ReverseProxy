// Package config loads .env and the YAML server config.
package config

import (
	"log"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
	"github.com/joho/godotenv"
)

// ServerConfig is the YAML file at SERVER_CONFIG.
type ServerConfig struct {
	Server   Server               `yaml:"server"`
	Routes   []Route              `yaml:"routes"`
	Backends map[string][]Backend `yaml:"backends"`
}

// Backend is one backend replica.
type Backend struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// Server is where the gateway listens.
type Server struct {
	Hostname string `yaml:"hostname"`
	Port     int    `yaml:"port"`
	Endpoint string `yaml:"endpoint"`
}

// Route is parsed but not yet used by the router.
type Route struct {
	Service string `yaml:"service"`
	Value   string `yaml:"value"`
}

// EnvConfig holds settings from .env.
type EnvConfig struct {
	ServerConfig string
	DatabaseURL  string
	JwtSecret    string
	ProxySecret  string
}

// LoadEnv loads the nearest .env at or above the working directory.
// It exits if there is none.
func LoadEnv() *EnvConfig {
	dir, _ := os.Getwd()
	for {
		path := filepath.Join(dir, ".env")
		if _, err := os.Stat(path); err == nil {
			godotenv.Load(path)
			// relative to the .env, so it works from any cwd
			serverConfig := os.Getenv("SERVER_CONFIG")
			if serverConfig != "" && !filepath.IsAbs(serverConfig) {
				serverConfig = filepath.Join(dir, serverConfig)
			}
			return &EnvConfig{
				ServerConfig: serverConfig,
				DatabaseURL:  os.Getenv("DATABASE_URL"),
				JwtSecret:    os.Getenv("JWT_SECRET"),
				ProxySecret:  os.Getenv("PROXY_SECRET"),
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			log.Fatal("could not find .env file")
		}
		dir = parent
	}
}

// ImportServerConfig reads the YAML config, exiting on failure.
func ImportServerConfig() ServerConfig {
	serverConfigPath := LoadEnv().ServerConfig
	yamlFile, err := os.ReadFile(serverConfigPath)
	if err != nil {
		log.Fatalf("Cannot read serverConfig file: %v", err)
	}
	var config ServerConfig
	if err := yaml.Unmarshal([]byte(yamlFile), &config); err != nil {
		log.Fatalf("Cannot parse yaml config: %v", err)
	}
	return config
}
