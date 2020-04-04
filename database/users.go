package database

import (
	"crypto/sha256"
	"gopds-api/models"
	"gopds-api/utils"
	"log"
	"time"
)

// CheckUser функция проверки пользователя и пароля в формате pbkdf2 (django)
func CheckUser(u models.LoginRequest) (bool, error) {
	userDB := new(models.User)
	err := db.Model(userDB).Where("username = ?", u.Login).First()
	if err != nil {
		return false, err
	}
	pCheck, err := utils.CheckPbkdf2(u.Password, userDB.Password, sha256.Size, sha256.New)
	if err != nil {
		return false, err
	}
	go loginDateSet(userDB)
	return pCheck, nil
}

// loginDateSet goroutine for update user login date
func loginDateSet(u *models.User) {
	_, err := db.Model(u).Set("last_login = NOW()").WherePK().Update()
	if err != nil {
		log.Println(err)
	}
}

// CreateUser function creates a new user in database
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

// CheckInvite check for invite in database
func CheckInvite(i string) (bool, error) {
	err := db.Model(&models.Invite{}).Where("invite = ?", i).First()
	if err != nil {
		return false, err
	}
	return true, nil
}
