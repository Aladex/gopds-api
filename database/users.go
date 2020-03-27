package database

import (
	"crypto/sha256"
	"gopds-api/models"
	"gopds-api/utils"
)

// CheckUser функция проверки пользователя и пароля в формате pbkdf2 (django)
func CheckUser(u models.User) (bool, error) {
	userDB := new(models.User)
	err := db.Model(userDB).Where("username = ?", u.Login).First()
	if err != nil {
		return false, err
	}
	return utils.CheckPbkdf2(u.Password, userDB.Password, sha256.Size, sha256.New)
}
