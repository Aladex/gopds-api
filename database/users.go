package database

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/utils"
	"strings"
	"time"
)

func UserObject(search string) (models.User, error) {
	userDB := new(models.User)
	err := db.Model(userDB).
		WhereOr("lower(username) = lower(?)", search).
		WhereOr("lower(email) = lower(?)", search).
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
		WhereOr("lower(username) = lower(?)", u.Login).
		WhereOr("lower(email) = lower(?)", u.Login).
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
		logging.Error(err)
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
	err := db.Model(userDB).Where("lower(username) = lower(?)", u).First()
	if err != nil {
		return *userDB, err
	}

	// Fetch collections for the user
	var collections []models.BookCollection
	err = db.Model(&collections).Where("user_id = ?", userDB.ID).Select()
	if err != nil {
		return *userDB, err
	}

	userDB.Collections = collections

	return *userDB, nil
}

// GetUserList function returns an users list
func GetUserList(filters models.UserFilters) ([]models.User, int, error) {
	users := []models.User{}

	// Validate and whitelist allowed order fields
	allowedOrderFields := map[string]string{
		"id":          "id",
		"username":    "username",
		"email":       "email",
		"date_joined": "date_joined",
		"last_login":  "last_login",
	}

	orderBy := "id" // default
	if filters.Order != "" {
		if validField, exists := allowedOrderFields[filters.Order]; exists {
			orderBy = validField
		}
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

// UpdateUserProfile safely updates only profile fields that users can change themselves
func UpdateUserProfile(userID int64, updates models.SelfUserChangeRequest) (models.User, error) {
	var user models.User
	err := db.Model(&user).Where("id = ?", userID).Select()
	if err != nil {
		return user, err
	}

	// Only update fields that users should be able to change
	if updates.FirstName != "" {
		user.FirstName = updates.FirstName
	}
	if updates.LastName != "" {
		user.LastName = updates.LastName
	}
	if updates.BooksLang != "" {
		user.BooksLang = updates.BooksLang
	}
	if updates.NewPassword != "" {
		logging.Info("Updating password for user ", user.Login)
		hashedPassword := utils.CreatePasswordHash(updates.NewPassword)
		user.Password = hashedPassword
	}

	if _, err := db.Model(&user).WherePK().Update(); err != nil {
		return user, err
	}
	return user, nil
}

// UpdateUserByAdmin updates user data by admin (integrated with new Telegram architecture)
func UpdateUserByAdmin(action models.AdminCommandToUser) (models.User, error) {
	var userToChange models.User
	err := db.Model(&userToChange).Where("id = ?", action.User.ID).Select()
	if err != nil {
		logging.Errorf("Failed to select user %d: %v", action.User.ID, err)
		return userToChange, err
	}

	// Update password only if a new non-empty password is provided
	if action.User.Password != "" {
		logging.Info("Updating password for user ", userToChange.Login)
		hashedPassword := utils.CreatePasswordHash(action.User.Password)
		userToChange.Password = hashedPassword
	}

	// Check if BotToken has actually changed
	oldBotToken := userToChange.BotToken
	botTokenChanged := oldBotToken != action.User.BotToken

	logging.Infof("Bot token changed: %v (old='%s' -> new='%s')",
		botTokenChanged, oldBotToken, action.User.BotToken)

	// Handle bot token changes BEFORE updating database
	if botTokenChanged {
		logging.Info("Processing bot token change...")

		// Step 1: Remove old bot if exists (synchronously with timeout)
		if oldBotToken != "" {
			logging.Infof("Removing old bot with token: %s", oldBotToken)
			if err := removeBotFromManagerSync(oldBotToken); err != nil {
				logging.Error("Failed to remove old bot:", err)
				// Continue anyway - we want to clean up the database
			}
		}

		// Step 2: Clear old data from database
		if oldBotToken != "" {
			logging.Info("Clearing old bot data from database")
			err = ClearBotToken(userToChange.ID)
			if err != nil {
				logging.Error("Failed to clear old bot token:", err)
			}
		}
	}

	// Update user details (including new bot token)
	userToChange = updateUserDetails(userToChange, action.User)

	// Step 3: Update database with new token
	_, err = db.Model(&userToChange).WherePK().Update()
	if err != nil {
		logging.Errorf("Failed to update user in database: %v", err)
		return userToChange, err
	}

	// Step 4: Create new bot if token provided (after database is updated)
	if botTokenChanged && action.User.BotToken != "" {
		logging.Infof("Creating new bot with token: %s", action.User.BotToken)
		if err := createBotInManager(action.User.BotToken, userToChange.ID); err != nil {
			logging.Error("Failed to create new bot:", err)
			// Don't fail the update if bot creation fails - token is already saved in DB
		}
	}

	// Verify the update by reading the user again
	var verifyUser models.User
	err = db.Model(&verifyUser).Where("id = ?", userToChange.ID).Select()
	if err == nil {
		logging.Infof("Verification - user after update: ID=%d, BotToken=%s",
			verifyUser.ID, verifyUser.BotToken)
	}

	return userToChange, nil
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
		return UpdateUserByAdmin(action)
	default:
		return userToChange, errors.New("unknown action")
	}
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

// UpdateBotToken updates bot token for user
func UpdateBotToken(userID int64, botToken string) error {
	_, err := db.Model(&models.User{}).
		Set("bot_token = ?", botToken).
		Where("id = ?", userID).
		Update()
	return err
}

// GetUserByBotToken finds user by bot token
func GetUserByBotToken(botToken string) (models.User, error) {
	var user models.User
	err := db.Model(&user).Where("bot_token = ?", botToken).First()
	return user, err
}

// UpdateTelegramID updates Telegram ID for user with specified bot token
func UpdateTelegramID(botToken string, telegramID int64) error {
	_, err := db.Model(&models.User{}).
		Set("telegram_id = ?", telegramID).
		Where("bot_token = ? AND telegram_id IS NULL", botToken).
		Update()
	return err
}

// GetUserByTelegramID finds user by Telegram ID
func GetUserByTelegramID(telegramID int64) (models.User, error) {
	var user models.User
	err := db.Model(&user).Where("telegram_id = ?", telegramID).First()
	return user, err
}

// ClearBotToken clears bot token and Telegram ID for user
func ClearBotToken(userID int64) error {
	_, err := db.Model(&models.User{}).
		Set("bot_token = NULL, telegram_id = NULL").
		Where("id = ?", userID).
		Update()
	return err
}

// GetUsersWithBotTokens returns all users who have bot tokens
func GetUsersWithBotTokens() ([]models.User, error) {
	var users []models.User
	err := db.Model(&users).Where("bot_token IS NOT NULL AND bot_token != ''").Select()
	return users, err
}

// Telegram Bot Manager integration functions
var telegramBotManager interface {
	CreateBotForUser(token string, userID int64) error
	RemoveBot(token string) error
	SetWebhook(token string) error
}

// SetTelegramBotManager sets reference to BotManager for admin panel integration
func SetTelegramBotManager(manager interface {
	CreateBotForUser(token string, userID int64) error
	RemoveBot(token string) error
	SetWebhook(token string) error
}) {
	telegramBotManager = manager
}

// createBotInManager creates bot in BotManager and sets webhook
func createBotInManager(token string, userID int64) error {
	if telegramBotManager == nil {
		logging.Warn("Telegram BotManager not set, skipping bot creation")
		return nil
	}

	logging.Infof("Starting bot creation process for user %d", userID)
	logging.Infof("Bot token (masked): %s...%s", token[:5], token[len(token)-5:])

	// Create bot with timeout to avoid blocking admin operations
	done := make(chan error, 1)
	go func() {
		logging.Infof("Creating bot instance for user %d...", userID)
		err := telegramBotManager.CreateBotForUser(token, userID)
		if err != nil {
			logging.Errorf("Failed to create bot for user %d: %v", userID, err)
			// Check if this is an authorization error from Telegram
			if strings.Contains(err.Error(), "Unauthorized (401)") {
				logging.Errorf("Invalid bot token for user %d: %s. Please check the token in @BotFather", userID, err)
				done <- fmt.Errorf("invalid bot token - please verify the token with @BotFather")
				return
			}
			done <- err
			return
		}

		logging.Infof("Bot created successfully for user %d, now setting webhook...", userID)
		// Set webhook
		err = telegramBotManager.SetWebhook(token)
		if err != nil {
			logging.Errorf("Failed to set webhook for user %d: %v", userID, err)
		} else {
			logging.Infof("Webhook set successfully for user %d", userID)
		}
		done <- err
	}()

	// Wait for bot creation with timeout
	select {
	case err := <-done:
		if err != nil {
			logging.Errorf("Bot creation process failed for user %d: %v", userID, err)
		} else {
			logging.Infof("Bot creation process completed successfully for user %d", userID)
		}
		return err
	case <-time.After(15 * time.Second):
		logging.Errorf("Timeout creating bot for user %d - operation cancelled", userID)
		return fmt.Errorf("timeout creating bot - operation took too long")
	}
}

// removeBotFromManager removes bot from BotManager
func removeBotFromManager(token string) error {
	if telegramBotManager == nil {
		logging.Warn("Telegram BotManager not set, skipping bot removal")
		return nil
	}

	return telegramBotManager.RemoveBot(token)
}

// removeBotFromManagerSync removes bot from BotManager synchronously, blocking until done
func removeBotFromManagerSync(token string) error {
	if telegramBotManager == nil {
		logging.Warn("Telegram BotManager not set, skipping bot removal")
		return nil
	}

	// Directly call the RemoveBot function without goroutine
	return telegramBotManager.RemoveBot(token)
}
