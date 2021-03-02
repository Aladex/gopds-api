package sessions

import (
	"fmt"
	"github.com/go-redis/redis"
	"github.com/spf13/viper"
	"gopds-api/logging"
)

var rdb *redis.Client
var rdbToken *redis.Client

func init() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig() // Find and read the config file
	if err != nil {             // Handle errors reading the config file
		logging.CustomLog.Fatalf("Fatal error config file: %s \n", err)
	}
	rdb = RedisConnection(0)
	rdbToken = RedisConnection(1)
}

// RedisConnection Connection to redis-master to DB
func RedisConnection(dbNum int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%v:%v", viper.GetString("redis.host"), viper.GetString("redis.port")),
		Password: "",    // no password set
		DB:       dbNum, // use default DB
	})
}
