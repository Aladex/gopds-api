package sessions

import (
	"gopds-api/models"
	"gopds-api/utils"
	"log"
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

func DropAllSessions(token string) {
	username, err := utils.CheckToken(token)
	if err != nil {
		log.Println(err)
	}
	keys := rdb.Keys("*")
	for _, k := range keys.Val() {
		checkedUser, err := utils.CheckToken(k)
		if err != nil {
			log.Println(err)
		}
		if checkedUser == username {
			rdb.Del(k)
		}
	}
}
