package config

import (
	"github.com/spf13/viper"
	"log"
)

var AppConfig = viper.New()

func init() {
	AppConfig.SetConfigName("config")
	AppConfig.SetConfigType("yaml")
	AppConfig.AddConfigPath(".")

	err := AppConfig.ReadInConfig() // Find and read the config file
	if err != nil {                 // Handle errors reading the config file
		log.Fatalf("Fatal error config file: %s \n", err)
	}
}
