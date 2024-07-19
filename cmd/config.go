package main

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func init() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	if err := viper.ReadInConfig(); err != nil {
		logrus.Fatalf("Fatal error config file: %s \n", err)
	}

	// Initialize dist folders for static files
	if err := initializeDistFolders(); err != nil {
		logrus.Fatalf("Error initializing dist folders: %s \n", err)
	}

}
