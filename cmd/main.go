package main

import (
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"gopds-api/api"
	_ "gopds-api/docs"
	"gopds-api/middlewares"
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

	// Default group without auth
	{
		route.POST("/api/login", api.AuthCheck)
		route.POST("/api/register", api.Registration)
		route.GET("/api/logout", api.LogOut)
	}

	apiGroup := route.Group("/api")
	apiGroup.Use(middlewares.AuthMiddleware())

	booksGroup := apiGroup.Group("/books")
	adminGroup := apiGroup.Group("/admin")

	adminGroup.Use(middlewares.AdminMiddleware())

	// Books group for all users
	{
		booksGroup.GET("/list", api.GetBooks)
		booksGroup.GET("/self-user", api.SelfUser)
		booksGroup.GET("/authors", api.GetAuthors)
		// Метод скачивания файла
		booksGroup.POST("/file", api.GetBookFile)
	}

	// Admin group
	{
		adminGroup.POST("/users", api.GetUsers)
		adminGroup.POST("/user", api.GetUser)
		adminGroup.POST("/change-user", api.ChangeUser)
	}

	err := route.Run("0.0.0.0:8085")
	if err != nil {
		log.Fatalln(err)
	}
}
