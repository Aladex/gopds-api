package sessions

import (
	uuid "github.com/satori/go.uuid"
	"gopds-api/models"
	"gopds-api/utils"
	"strings"
	"time"
)

// SetSessionKey запись сессии в Redis с продлением ее, если пользователь остается активным в течение суток
func SetSessionKey(lu models.LoggedInUser) {
	rdb.Set(*lu.Token, strings.ToLower(lu.User), time.Hour*24)
}

// CheckSessionKey структура проверки наличия сессии в Redis
func CheckSessionKey(lu models.LoggedInUser) bool {
	userSession := rdb.Get(*lu.Token)
	if userSession.Val() != strings.ToLower(lu.User) {
		return false
	}
	return true
}

// DeleteSessionKey функция удаления ключа при разлогине пользователя
func DeleteSessionKey(lu models.LoggedInUser) {
	userSession := rdb.Get(*lu.Token)
	if userSession.Val() == strings.ToLower(lu.User) {
		rdb.Del(*lu.Token)
	}
}

// DropAllSessions function for remove all jwt keys of user
func DropAllSessions(token string) {
	username, err := utils.CheckToken(token)
	if err != nil {
		customLog.Println(err)
	}
	keys := rdb.Keys("*")
	for _, k := range keys.Val() {
		checkedUser, err := utils.CheckToken(k)
		if err != nil {
			customLog.Println(err)
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
