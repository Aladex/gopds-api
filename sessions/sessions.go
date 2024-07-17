package sessions

import (
	"github.com/google/uuid"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/utils"
	"strings"
	"time"
)

// SetSessionKey sets a new session key for a user login.
func SetSessionKey(lu models.LoggedInUser) {
	rdb.Set(*lu.Token, strings.ToLower(lu.User), 24*time.Hour)
}

// CheckSessionKey checks if a session key exists for a user.
func CheckSessionKey(lu models.LoggedInUser) bool {
	return rdb.Get(*lu.Token).Val() == strings.ToLower(lu.User)
}

// DeleteSessionKey deletes a user's session key.
func DeleteSessionKey(lu models.LoggedInUser) {
	if CheckSessionKey(lu) {
		rdb.Del(*lu.Token)
	}
}

// DropAllSessions removes all session keys for a user.
func DropAllSessions(token string) {
	username, _, err := utils.CheckToken(token)
	if err != nil {
		logging.CustomLog.Println(err)
		return
	}
	keys, err := rdb.Keys("*").Result()
	if err != nil {
		logging.CustomLog.Println(err)
		return
	}
	for _, k := range keys {
		if checkedUser, _, err := utils.CheckToken(k); err == nil && checkedUser == username {
			rdb.Del(k)
		}
	}
}

// GenerateTokenPassword generates a temporary token for password change.
func GenerateTokenPassword(user string) string {
	passwordToken := uuid.New().String()
	rdbToken.Set(user, passwordToken, 90*time.Minute)
	return passwordToken
}

// CheckTokenPassword checks if a temporary token exists for a user.
func CheckTokenPassword(token string) string {
	keys, err := rdbToken.Keys("*").Result()
	if err != nil {
		logging.CustomLog.Println(err)
		return ""
	}
	for _, k := range keys {
		if rdbToken.Get(k).Val() == token {
			return k
		}
	}
	return ""
}

// DeleteTokenPassword removes a user's temporary token.
func DeleteTokenPassword(token string) {
	keys, err := rdbToken.Keys("*").Result()
	if err != nil {
		logging.CustomLog.Println(err)
		return
	}
	for _, k := range keys {
		if rdbToken.Get(k).Val() == token {
			rdbToken.Del(k)
		}
	}
}
