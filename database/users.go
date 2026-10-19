package database

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/utils"

	"github.com/go-pg/pg/v10"
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

// GetUser returns the user by login with their collections.
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

// IsSuperUserByID reads the user's current superuser flag. The WebSocket hub
// calls it at subscribe time and on its revalidation ticks, so a demotion
// takes effect without waiting for the token to expire.
func IsSuperUserByID(id int64) (bool, error) {
	var isSuperUser bool
	_, err := db.QueryOne(pg.Scan(&isSuperUser),
		"SELECT is_superuser FROM auth_user WHERE id = ?", id)
	if err != nil {
		return false, err
	}
	return isSuperUser, nil
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
	if attachErr := attachTelegram(users); attachErr != nil {
		return users, 0, attachErr
	}
	return users, count, nil
}

// attachTelegram fills in each user's Telegram connection for the admin
// list: the bot's username and the state, never anything secret.
func attachTelegram(users []models.User) error {
	if len(users) == 0 {
		return nil
	}
	ids := make([]int64, len(users))
	for i := range users {
		ids[i] = users[i].ID
	}
	var rows []struct {
		UserID    int64
		Username  string
		Status    string
		BlockedAt *time.Time
	}
	if _, err := db.Query(&rows, `
		SELECT b.user_id, t.username, t.status, b.blocked_at
		FROM telegram_binding b JOIN telegram_bot t ON t.id = b.bot_ref
		WHERE b.status = 'active' AND b.user_id IN (?)`, pg.In(ids)); err != nil {
		return err
	}
	byUser := make(map[int64]*models.UserTelegram, len(rows))
	for _, r := range rows {
		state := "connected"
		switch {
		case r.Status == models.TelegramBotTokenRejected:
			state = "token_rejected"
		case r.BlockedAt != nil:
			state = "blocked"
		}
		byUser[r.UserID] = &models.UserTelegram{BotUsername: r.Username, State: state}
	}
	for i := range users {
		users[i].Telegram = byUser[users[i].ID]
	}
	return nil
}

// profileReadHook runs between reading the user and writing the update.
// Tests use it to land a concurrent write in exactly that gap.
var profileReadHook = func() {}

// UpdateUserProfile applies what the reader changed in their profile.
//
// Only the columns the request names are written. It used to write the whole
// row it had read, so two requests in flight — the two card switches flipped
// one after the other, or a name saved while the Kindle address was cleared —
// each put back the other's column as it found it, and the second success
// undid the first.
func UpdateUserProfile(userID int64, updates models.SelfUserChangeRequest) (models.User, error) {
	var user models.User
	err := db.Model(&user).Where("id = ?", userID).Select()
	if err != nil {
		return user, err
	}
	profileReadHook()

	var columns []string
	if updates.FirstName != "" {
		user.FirstName = updates.FirstName
		columns = append(columns, "first_name")
	}
	if updates.LastName != "" {
		user.LastName = updates.LastName
		columns = append(columns, "last_name")
	}
	if updates.BooksLang != "" {
		user.BooksLang = updates.BooksLang
		columns = append(columns, "books_lang")
	}
	if updates.InterfaceLang != "" {
		user.InterfaceLang = updates.InterfaceLang
		columns = append(columns, "interface_lang")
	}
	if updates.ShowKindleButton != nil {
		user.ShowKindleButton = *updates.ShowKindleButton
		columns = append(columns, "show_kindle_button")
	}
	if updates.ShowTelegramButton != nil {
		user.ShowTelegramButton = *updates.ShowTelegramButton
		columns = append(columns, "show_telegram_button")
	}
	if updates.NewPassword != "" {
		logging.Info("Updating password for user ", user.Login)
		hashedPassword := utils.CreatePasswordHash(updates.NewPassword)
		user.Password = hashedPassword
		columns = append(columns, "password")
	}
	if len(columns) == 0 {
		return user, nil
	}

	// RETURNING the whole row, so the caller sees what the database holds —
	// including whatever another request wrote to the columns left alone.
	if _, err := db.Model(&user).Column(columns...).WherePK().Returning("*").Update(); err != nil {
		return user, err
	}
	return user, nil
}

// SetKindleEmail stores the reader's Send-to-Kindle address, or clears it
// when address is "". The caller has already validated and normalized it.
//
// One column, written on its own rather than through UpdateUserProfile: that
// one reads an empty field as "leave it alone", which is exactly the value
// that has to mean "clear it" here.
func SetKindleEmail(userID int64, address string) (models.User, error) {
	var user models.User
	res, err := db.Model(&user).
		Set("kindle_email = ?", address).
		Where("id = ?", userID).
		Returning("*").
		Update()
	if err != nil {
		return user, err
	}
	if res.RowsAffected() == 0 {
		return user, pg.ErrNoRows
	}
	return user, nil
}

// KindleTarget is what sending a book to a reader's Kindle needs to know
// about them, read fresh at the moment of sending.
type KindleTarget struct {
	Email    string
	SenderID string
}

// GetKindleTarget reads the reader's Kindle address and sender id.
func GetKindleTarget(userID int64) (KindleTarget, error) {
	var target KindleTarget
	_, err := db.QueryOne(&target, `
		SELECT kindle_email AS email, kindle_sender_id::text AS sender_id
		FROM auth_user WHERE id = ?`, userID)
	return target, err
}

// UpdateUserByAdmin updates user data by admin. The Telegram connection is
// the reader's own and is not changed here: the admin sees its state, and the
// bot columns are written back as they were read.
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

	userToChange = updateUserDetails(userToChange, action.User)

	_, err = db.Model(&userToChange).WherePK().Update()
	if err != nil {
		logging.Errorf("Failed to update user in database: %v", err)
		return userToChange, err
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
	userToChange.Active = newUserDetails.Active
	return userToChange
}

// DeleteUser removes a user and everything that belongs only to them.
//
// Three tables reference auth_user without a delete rule — favorites, votes
// and collections — so deleting a row on its own fails for anyone who has ever
// used the service. It reported a raw constraint violation and the account
// stayed.
//
// What goes with the user is what is theirs alone: their favorites, the votes
// they cast, and their own collections along with the books in them and any
// votes those collections received. Curated collections are library content
// and are not owned by anyone — their user_id is already null — so nothing
// public is touched. book_match_decisions and book_rescan_pending name the user
// who acted rather than belonging to them, and their foreign keys already set
// the reference to null.
//
// All of it in one transaction: a half-deleted user is worse than one that
// would not delete.
func DeleteUser(id string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// The user's own collections, named once and used three times below.
	ownCollections := tx.Model((*models.BookCollection)(nil)).
		Column("id").
		Where("user_id = ?", id)

	// Votes on those collections were cast by other people, but the collection
	// is going, so they go with it.
	if _, err = tx.Model((*models.CollectionVote)(nil)).
		Where("collection_id IN (?)", ownCollections).
		Delete(); err != nil {
		return err
	}

	if _, err = tx.Exec(
		`DELETE FROM book_collection_books WHERE book_collection_id IN (SELECT id FROM book_collections WHERE user_id = ?)`,
		id,
	); err != nil {
		return err
	}

	if _, err = tx.Model((*models.BookCollection)(nil)).
		Where("user_id = ?", id).
		Delete(); err != nil {
		return err
	}

	// Votes the user cast on anybody's collection.
	if _, err = tx.Model((*models.CollectionVote)(nil)).
		Where("user_id = ?", id).
		Delete(); err != nil {
		return err
	}

	if _, err = tx.Exec(`DELETE FROM favorite_books WHERE user_id = ?`, id); err != nil {
		return err
	}

	if _, err = tx.Model(&models.User{}).Where("id = ?", id).Delete(); err != nil {
		return err
	}

	return tx.Commit()
}

// GetUserByTelegramID finds the library account a Telegram user is bound to:
// the active binding decides, not the old auth_user.telegram_id.
func GetUserByTelegramID(telegramID int64) (models.User, error) {
	var user models.User
	err := db.Model(&user).
		Where("id = (SELECT user_id FROM telegram_binding WHERE tg_user_id = ? AND status = 'active')", telegramID).
		Select()
	return user, err
}
