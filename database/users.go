package database

import (
	"crypto/sha256"
	"gopds-api/models"
	"gopds-api/utils"
	"time"
)

// CheckUser функция проверки пользователя и пароля в формате pbkdf2 (django)
func CheckUser(u models.LoginRequest) (bool, error) {
	userDB := new(models.User)
	err := db.Model(userDB).Where("username = ?", u.Login).First()
	if err != nil {
		return false, err
	}
	return utils.CheckPbkdf2(u.Password, userDB.Password, sha256.Size, sha256.New)
}

func CreateUser(u models.RegisterRequest) error {
	userDB := models.User{
		Login:       u.Login,
		Password:    utils.CreatePasswordHash(u.Password),
		IsSuperUser: false,
		Email:       u.Email,
		DateJoined:  time.Now(),
	}
	_, err := db.Model(&userDB).Insert()
	if err != nil {
		return err
	}
	return nil
}
