// Package config used for importing .env and server Configs
package config

import (
	"log"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
	"github.com/joho/godotenv"
)

type ServerConfig struct {
	Server Server  `yaml:"server"`
	Routes []Route `yaml:"routes"`
}

type Server struct {
	Hostname string `yaml:"hostname"`
	Port     int    `yaml:"port"`
	Endpoint string `yaml:"endpoint"`
}

type Route struct {
	Service string `yaml:"service"`
	Value   string `yaml:"value"`
}

type EnvConfig struct {
	ServerConfig string
	DatabaseURL  string
	JwtSecret    string
}

func LoadEnv() *EnvConfig {
	dir, _ := os.Getwd()
	for {
		path := filepath.Join(dir, ".env")
		if _, err := os.Stat(path); err == nil {
			godotenv.Load(path)
			return &EnvConfig{
				ServerConfig: os.Getenv("SERVER_CONFIG"),
				DatabaseURL:  os.Getenv("DATABASE_URL"),
				JwtSecret:    os.Getenv("JWT_SECRET"),
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			log.Fatal("could not find .env file")
		}
		dir = parent
	}
}

func ImportConfig() ServerConfig {
	LoadEnv()
	serverConfigPath := os.Getenv("SERVER_CONFIG")
	yamlFile, err := os.ReadFile(serverConfigPath)
	if err != nil {
		log.Fatal("Cannot read serverConfig file, {}", err)
	}
	var config ServerConfig
	if err := yaml.Unmarshal([]byte(yamlFile), &config); err != nil {
		log.Fatal("Cannot parse yaml config")
	}
	return config
}
