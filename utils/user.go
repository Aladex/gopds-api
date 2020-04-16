package utils

import (
	uuid "github.com/satori/go.uuid"
	"gopds-api/sessions"
	"time"
)

func GenerateTokenPassword(user string) {
	passwordToken := uuid.NewV4().String()
	rdb := sessions.RedisConnection(1)
	rdb.Set(passwordToken, user, time.Minute*30)
}

func CheckTokenPassword(token string) string {
	rdb := sessions.RedisConnection(1)
	username := rdb.Get(token).String()
	return username
}
