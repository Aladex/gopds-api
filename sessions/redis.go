package sessions

import (
	"fmt"
	"github.com/go-redis/redis"
	"github.com/spf13/viper"
	"log"
)

var rdb *redis.Client

func init() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig() // Find and read the config file
	if err != nil {             // Handle errors reading the config file
		log.Fatalf("Fatal error config file: %s \n", err)
	}
	rdb = RedisConnection()
}

// RedisConnection Connection to redis-master to DB 30 for terminals section
func RedisConnection() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%v:%v", viper.GetString("redis.host"), viper.GetString("redis.port")),
		Password: "",                       // no password set
		DB:       viper.GetInt("redis.db"), // use default DB
	})
}
