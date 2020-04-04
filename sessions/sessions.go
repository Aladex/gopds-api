package sessions

import (
	"gopds-api/models"
	"time"
)

// SetSessionKey запись сессии в Redis с продлением ее, если пользователь остается активным в течение суток
func SetSessionKey(lu models.LoggedInUser) {
	rdb.Set(lu.User, *lu.Token, time.Hour*24)
}

// CheckSessionKey структура проверки наличия сессии в Redis
func CheckSessionKey(lu models.LoggedInUser) bool {
	userSession := rdb.Get(lu.User)
	if userSession.Val() != *lu.Token {
		return false
	}
	return true
}

// DeleteSessionKey функция удаления ключа при разлогине пользователя
func DeleteSessionKey(lu models.LoggedInUser) {
	userSession := rdb.Get(lu.User)
	if userSession.Val() == *lu.Token {
		rdb.Del(lu.User)
	}
}
