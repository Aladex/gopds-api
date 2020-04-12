package database

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"gopds-api/models"
	"gopds-api/utils"
	"log"
	"strings"
	"time"
)

// CheckUser функция проверки пользователя и пароля в формате pbkdf2 (django)
func CheckUser(u models.LoginRequest) (bool, models.User, error) {
	userDB := new(models.User)
	err := db.Model(userDB).Where("username ILIKE ?", strings.ToLower(u.Login)).First()
	if err != nil {
		return false, *userDB, err
	}
	pCheck, err := utils.CheckPbkdf2(u.Password, userDB.Password, sha256.Size, sha256.New)
	if err != nil {
		return false, *userDB, err
	}
	return pCheck, *userDB, nil
}

// loginDateSet goroutine for update user login date
func LoginDateSet(u *models.User) {
	_, err := db.Model(u).Set("last_login = NOW()").WherePK().Update()
	if err != nil {
		log.Println(err)
	}
}

// CreateUser function creates a new user in database
func CreateUser(u models.RegisterRequest) error {
	userDB := models.User{
		Login:       strings.ToLower(u.Login),
		Password:    utils.CreatePasswordHash(u.Password),
		IsSuperUser: false,
		Email:       strings.ToLower(u.Email),
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

// GetUser function for return users object by username
func GetUser(u string) (models.User, error) {
	userDB := new(models.User)
	err := db.Model(userDB).Where("username ILIKE ?", u).First()
	if err != nil {
		return *userDB, err
	}
	return *userDB, nil
}

// GetUserList function returns an users list
func GetUserList(filters models.UserFilters) ([]models.User, int, error) {
	users := []models.User{}
	orderBy := "%s %s"
	if filters.Order == "" {
		orderBy = "id"
	} else {
		orderBy = filters.Order
	}
	if filters.DESC {
		orderBy += " DESC"
	} else {
		orderBy += " ASC"
	}
	likeUser := fmt.Sprintf("%%%s%%", filters.Username)
	likeEmail := fmt.Sprintf("%%%s%%", filters.Email)
	count, err := db.Model(&users).
		Limit(filters.Limit).
		Offset(filters.Offset).
		Where("username ILIKE ?", likeUser).
		Where("email ILIKE ?", likeEmail).
		Order(orderBy).
		SelectAndCount()
	if err != nil {
		return users, 0, err
	}
	return users, count, nil
}

// ActionUser function for change user from admin panel
func ActionUser(action models.AdminCommandToUser) (models.User, error) {
	var userToChange models.User
	var tmpPass string
	err := db.Model(&userToChange).Where("id = ?", action.User.ID).Select()
	if err != nil {
		return userToChange, err
	}
	switch action.Action {
	case "get":
		return userToChange, nil
	case "update":
		if action.User.Password != "" {
			tmpPass = utils.CreatePasswordHash(action.User.Password)
		} else {
			tmpPass = userToChange.Password
		}
		userToChange = action.User
		userToChange.Password = tmpPass
		err = db.Update(&userToChange)
		if err != nil {
			return userToChange, err
		}
		return userToChange, nil
	default:
		return userToChange, errors.New("unknown action")
	}
}
