package main

import (
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"gopds-api/api"
	_ "gopds-api/docs"
	"gopds-api/models"
	"gopds-api/sessions"
	"gopds-api/utils"
	"log"
	"net/http"
)

func init() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig() // Find and read the config file
	if err != nil {             // Handle errors reading the config file
		log.Fatalf("Fatal error config file: %s \n", err)
	}
}

// AuthMiddleware Мидлварь для проверки токена пользователя в методах GET и POST
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		userToken := c.Request.Header.Get("Authorization")

		if userToken == "" {
			c.JSON(403, "Token is required")
			c.Abort()
			return
		}
		username, err := utils.CheckToken(userToken)
		if err != nil {
			c.JSON(403, "token is invalid")
			c.Abort()
			return
		}
		thisUser := models.LoggedInUser{
			User:  username,
			Token: userToken,
		}
		if !sessions.CheckSessionKey(thisUser) {
			c.JSON(403, "token is invalid")
			c.Abort()
			return
		}
		sessions.SetSessionKey(thisUser)
		c.Next()
	}
}

func Options(c *gin.Context) {
	if c.Request.Method != "OPTIONS" {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Next()
	} else {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "authorization, origin, content-type, accept, token")
		c.Header("Allow", "HEAD,GET,POST,PUT,PATCH,DELETE,OPTIONS")
		c.Header("Content-Type", "application/json")
		c.AbortWithStatus(http.StatusOK)
	}
}

// @title GOPDS API
// @version 1.0
// @description GOPDS API implementation to django service
// @contact.name API Support
// @contact.email aladex@gmail.com
// @BasePath /api
func main() {
	if !viper.GetBool("app.devel_mode") {
		gin.SetMode(gin.ReleaseMode)
	}
	route := gin.New()
	if viper.GetBool("app.devel_mode") {
		route.Use(Options)
	}
	route.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	booksGroup := route.Group("/api/books")
	booksGroup.Use(AuthMiddleware())
	booksGroup.GET("/list", api.GetBooks)
	booksGroup.GET("/self-user", api.SelfUser)
	booksGroup.GET("/authors", api.GetAuthors)
	// Метод скачивания файла
	booksGroup.POST("/file", api.GetBookFile)

	route.POST("/api/login", api.AuthCheck)
	route.GET("/api/logout", api.LogOut)
	err := route.Run("0.0.0.0:8085")
	if err != nil {
		log.Fatalln(err)
	}
}
