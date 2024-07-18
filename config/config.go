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
	// Log the configuration
	log.Printf("Using config file: %s\n", viper.ConfigFileUsed())
	// Log the configuration kv
	for _, key := range viper.AllKeys() {
		log.Printf("Key: %s, Value: %s\n", key, viper.Get(key))
	}
}
