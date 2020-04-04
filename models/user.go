package models

import (
	"regexp"
	"time"
)

// User структура пользователя в БД
type User struct {
	tableName   struct{}  `pg:"auth_user,discard_unknown_columns" json:"-"`
	ID          int64     `pg:"id,pk" json:"id"`
	Login       string    `pg:"username" json:"username"`
	Password    string    `pg:"password" json:"password"`
	LastLogin   time.Time `pg:"last_login" json:"last_login"`
	IsSuperUser bool      `pg:"is_superuser" json:"is_superuser"`
	FirstName   string    `pg:"first_name" json:"first_name"`
	LastName    string    `pg:"last_name" json:"last_name"`
	Email       string    `pg:"email" json:"email"`
	DateJoined  time.Time `pg:"date_joined" json:"date_joined"`
}

// LoggedInUser структура для возвращения логина и токена доступа
type LoggedInUser struct {
	User        string  `json:"username"`
	Token       *string `json:"token,omitempty"`
	IsSuperuser *bool   `json:"is_superuser,omitempty"`
}

// LoginRequest структура для логина в библиотеку
type LoginRequest struct {
	Login    string `form:"username" json:"username" binding:"required"`
	Password string `form:"password" json:"password" binding:"required"`
}

// RegisterRequest структура формы регистрации
type RegisterRequest struct {
	Login    string `form:"username" json:"username" binding:"required"`
	Password string `form:"password" json:"password" binding:"required"`
	Email    string `form:"email" json:"email" binding:"required"`
	Invite   string `form:"invite" json:"invite" binding:"required"`
}

// Invite структура инвайта в бд
type Invite struct {
	tableName struct{} `pg:"invites,discard_unknown_columns" json:"-"`
	ID        int64    `pg:"id,pk"`
	Invite    string   `pg:"invite"`
}

// CheckValues проверка формы на валидность email, имени пользователя и длины пароля
func (r RegisterRequest) CheckValues() bool {
	emailRegex := regexp.MustCompile("^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+" +
		"@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9]" +
		"(?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")
	if !emailRegex.MatchString(r.Email) {
		return false
	}
	loginMatch, err := regexp.Match("[a-zA-Z0-9]", []byte(r.Login))
	if err != nil {
		return loginMatch
	}
	if len(r.Password) < 8 {
		return false
	}
	return true
}

// UserFilters фильтры для запроса списка пользователей
type UserFilters struct {
	Limit    int    `form:"limit" json:"limit"`
	Offset   int    `form:"offset" json:"offset"`
	Username string `form:"username" json:"username"`
	Email    string `form:"email" json:"email"`
	Order    string `form:"order" json:"order"`
	DESC     bool   `form:"desc" json:"desc"`
}
