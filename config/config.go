package config

import (
	"github.com/spf13/viper"
	"log"
)

// LoadConfig явно загружает конфигурацию
func LoadConfig() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Fatal error config file: %s \n", err)
	}
}
