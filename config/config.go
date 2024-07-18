package config

import (
	"github.com/spf13/viper"
	"log"
)

func init() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Fatal error config file: %s \n", err)
	}
}
