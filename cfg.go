package main

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	HostID         string `yaml:"host_id"`
	RabbitMQURL    string `yaml:"rabbitmq_url"`
	ReservedRAMPct int    `yaml:"reserved_ram_percent"`
	CheckInterval  int    `yaml:"check_interval"`
}

func ReadConfig(configFile string) (Config, error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return Config{}, err
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return Config{}, err
	}

	return config, nil
}
