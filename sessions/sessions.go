package sessions

import (
	uuid "github.com/satori/go.uuid"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/utils"
	"strings"
	"time"
)

// SetSessionKey creates a new session key in Redis for user login
func SetSessionKey(lu models.LoggedInUser) {
	rdb.Set(*lu.Token, strings.ToLower(lu.User), time.Hour*24)
}

// CheckSessionKey search for an user with tokens
func CheckSessionKey(lu models.LoggedInUser) bool {
	userSession := rdb.Get(*lu.Token)
	if userSession.Val() != strings.ToLower(lu.User) {
		return false
	}
	return true
}

// DeleteSessionKey deletes a session key in Redis for user logout
func DeleteSessionKey(lu models.LoggedInUser) {
	userSession := rdb.Get(*lu.Token)
	if userSession.Val() == strings.ToLower(lu.User) {
		rdb.Del(*lu.Token)
	}
}

// DropAllSessions function for remove all jwt keys of user
func DropAllSessions(token string) {
	username, _, err := utils.CheckToken(token)
	if err != nil {
		logging.CustomLog.Println(err)
	}
	keys := rdb.Keys("*")
	for _, k := range keys.Val() {
		checkedUser, _, err := utils.CheckToken(k)
		if err != nil {
			logging.CustomLog.Println(err)
		}
		if checkedUser == username {
			rdb.Del(k)
		}
	}
}

// GenerateTokenPassword generates and temporary token for password change
func GenerateTokenPassword(user string) string {
	passwordToken := uuid.NewV4().String()
	rdbToken.Set(user, passwordToken, time.Minute*90)
	return passwordToken
}

// CheckTokenPassword search for an user with tokens
func CheckTokenPassword(token string) string {
	keys := rdbToken.Keys("*")
	for _, k := range keys.Val() {
		if rdbToken.Get(k).Val() == token {
			return k
		}
	}
	return ""
}

// DeleteTokenPassword removes all temporary tokens for user
func DeleteTokenPassword(token string) {
	keys := rdbToken.Keys("*")
	for _, k := range keys.Val() {
		if rdbToken.Get(k).Val() == token {
			rdbToken.Del(k)
		}
	}
}
