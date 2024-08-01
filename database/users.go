package database

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gopds-api/models"
	"gopds-api/utils"
	"io"
	"net/http"
	"strings"
	"time"
)

func UserObject(search string) (models.User, error) {
	userDB := new(models.User)
	err := db.Model(userDB).
		WhereOr("username ILIKE ?", search).
		WhereOr("email ILIKE ?", search).
		First()
	if err != nil {
		return *userDB, err
	}
	return *userDB, nil
}

// CheckUser function for checking user in the database by login and password
func CheckUser(u models.LoginRequest) (bool, models.User, error) {
	var userDB models.User
	err := db.Model(&userDB).
		WhereOr("username ILIKE ?", strings.ToLower(u.Login)).
		WhereOr("email ILIKE ?", strings.ToLower(u.Login)).
		First()
	if err != nil {
		return false, userDB, err
	}

	// Check password
	pCheck, err := utils.CheckPbkdf2(u.Password, userDB.Password, sha256.Size, sha256.New)
	if err != nil || !pCheck {
		return false, userDB, nil
	}

	return true, userDB, nil
}

// LoginDateSet goroutine for update user login date
func LoginDateSet(u *models.User) {
	_, err := db.Model(u).Set("last_login = NOW()").WherePK().Update()
	if err != nil {
		logrus.Println(err)
	}
}

// CreateUser function creates a new user in database
func CreateUser(u models.RegisterRequest) error {
	userDB := models.User{
		Login:       u.Login,
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
	err := db.Model(&models.Invite{}).
		Where("invite = ?", i).
		Where("before_date > ?", time.Now()).
		First()
	if err != nil {
		return false, err
	}
	return true, nil
}

func ChangeInvite(request models.InviteRequest) error {
	var invite models.Invite

	switch request.Action {
	case "create":
		invite = request.Invite
		_, err := db.Model(&invite).Insert()
		if err != nil {
			return err
		}
		return nil
	case "delete":
		_, err := db.Model(&invite).Where("id = ?", request.Invite.ID).Delete()
		if err != nil {
			return err
		}
		return nil
	case "update":
		_, err := db.Model(&invite).
			Set("invite = ?", request.Invite.Invite).
			Set("before_date = ?", request.Invite.BeforeDate).
			Where("id = ?", request.Invite.ID).
			Update()
		if err != nil {
			return err
		}
		return nil
	default:
		return errors.New("invalid_action")
	}
}

// GetInvites returns a list of all invites in db
func GetInvites(invites *[]models.Invite) error {
	err := db.Model(invites).Select()
	if err != nil {
		return err
	}
	return nil
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
	count, err := db.Model(&users).
		Limit(filters.Limit).
		Offset(filters.Offset).
		WhereOr("username ILIKE ?", likeUser).
		WhereOr("email ILIKE ?", likeUser).
		Order(orderBy).
		SelectAndCount()
	if err != nil {
		return users, 0, err
	}
	return users, count, nil
}

func ActionUser(action models.AdminCommandToUser) (models.User, error) {
	var userToChange models.User
	err := db.Model(&userToChange).Where("id = ?", action.User.ID).Select()
	if err != nil {
		return userToChange, err
	}

	switch action.Action {
	case "get":
		return userToChange, nil
	case "update":
		// Update password only if a new non-empty password is provided
		if action.User.Password != "" {
			hashedPassword := utils.CreatePasswordHash(action.User.Password)
			userToChange.Password = hashedPassword
		}
		// Proceed with setting webhook if needed
		if err := setWebhookIfNeeded(action.User.BotToken); err != nil {
			return userToChange, err
		}
		// Update other user details
		userToChange = updateUserDetails(userToChange, action.User)
		if _, err := db.Model(&userToChange).WherePK().Update(); err != nil {
			return userToChange, err
		}
		return userToChange, nil
	default:
		return userToChange, errors.New("unknown action")
	}
}

// Helper function to set webhook if bot token is provided
func setWebhookIfNeeded(botToken string) error {
	if botToken != "" {
		webhookURL := fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook?url=%s/telegram/%s",
			botToken, viper.GetString("project_domain"), botToken)
		resp, err := http.Get(webhookURL)
		if err != nil {
			return err
		}
		defer func(Body io.ReadCloser) {
			err := Body.Close()
			if err != nil {
				logrus.Println(err)
			}
		}(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("failed to set webhook, status code: %d", resp.StatusCode)
		}
	}
	return nil
}

// Helper function to update user details
func updateUserDetails(userToChange, newUserDetails models.User) models.User {
	userToChange.Login = newUserDetails.Login
	userToChange.FirstName = newUserDetails.FirstName
	userToChange.LastName = newUserDetails.LastName
	userToChange.BooksLang = newUserDetails.BooksLang
	userToChange.Email = newUserDetails.Email
	userToChange.IsSuperUser = newUserDetails.IsSuperUser
	userToChange.BotToken = newUserDetails.BotToken
	userToChange.Active = newUserDetails.Active
	return userToChange
}

// DeleteUser function for deleting user by ID
func DeleteUser(id string) error {
	_, err := db.Model(&models.User{}).Where("id = ?", id).Delete()
	if err != nil {
		return err
	}
	return nil
}
