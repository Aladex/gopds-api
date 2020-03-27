package models

// User структура пользователя в БД
type User struct {
	tableName struct{} `pg:"auth_user,discard_unknown_columns" json:"-"`
	ID        int64    `json:"-" form:"-"`
	Login     string   `form:"username" json:"username" pg:"username" binding:"required"`
	Password  string   `form:"password" json:"password" pg:"password" binding:"required"`
}

// LoggedInUser структура для возвращения логина и токена доступа
type LoggedInUser struct {
	User  string `json:"username"`
	Token string `json:"token"`
}
