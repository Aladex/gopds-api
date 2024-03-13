package main

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"gopds-api/api"
	"gopds-api/config"
	_ "gopds-api/docs"
	"gopds-api/logging"
	"gopds-api/middlewares"
	"gopds-api/opds"
	"log"
	"net/http"
	"os"
)

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

func HeadToGetMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		log.Printf("HeadToGetMiddleware: Original Method: %s, Path: %s", c.Request.Method, c.Request.URL.Path)
		if c.Request.Method == "HEAD" {
			c.Request.Method = "GET"
		}
		c.Next()
	}
}

// @title GOPDS API
// @version 1.0
// @description GOPDS API implementation to django service
// @contact.name API Support
// @contact.email aladex@gmail.com
// @BasePath /api
func main() {
	if !config.AppConfig.GetBool("app.devel_mode") {
		gin.SetMode(gin.ReleaseMode)
	}
	path := config.AppConfig.GetString("app.users_path")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		err := os.MkdirAll(path, 0755)
		if err != nil {
			log.Fatalln(err)
		}
	}
	route := gin.New()
	route.Use(logging.GinrusLogger(logging.CustomLog))
	if config.AppConfig.GetBool("app.devel_mode") {
		route.Use(Options)
	}
	route.Use(HeadToGetMiddleware())
	route.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	linkGen := route.Group("/files")
	linkGen.Use(middlewares.TokenMiddleware())
	linkGen.GET("/books/get/:format/:id", api.CdnBookGenerate)
	// Default group without auth
	{
		route.GET("/book-posters/:book", api.GetBookPoster)
		route.GET("/status", func(context *gin.Context) {
			context.JSON(200, struct {
				Status string `json:"status"`
			}{
				Status: "ok",
			})
		})
		route.POST("/api/login", api.AuthCheck)
		route.POST("/api/register", api.Registration)
		route.POST("/api/change-password", api.ChangeUserState)
		route.POST("/api/change-request", api.ChangeRequest)
		route.POST("/api/token", api.TokenValidation)
		route.GET("/api/logout", api.LogOut)
		route.GET("/api/drop-sessions", api.DropAllSessions)
		route.GET("/download/:format/:id", opds.DownloadBook)
	}

	// XML routes
	opdsGroup := route.Group("/opds")
	opdsGroup.Use(middlewares.BasicAuth())
	{
		opdsGroup.GET("/", func(c *gin.Context) {
			c.Redirect(http.StatusMovedPermanently, "/opds/new/0/0")
		})
		opdsGroup.GET("/new/:page/:author", opds.GetNewBooks)
		opdsGroup.GET("/favorites/:page", opds.GetNewBooks)
		opdsGroup.GET("/search", opds.Search)
		opdsGroup.GET("/books", opds.GetBooks)
		opdsGroup.GET("/search-author", opds.GetAuthor)
		opdsGroup.GET("/download/:format/:id", opds.DownloadBook)
		opdsGroup.GET("/get/:format/:id", api.CdnBookGenerate)
	}

	apiGroup := route.Group("/api")
	apiGroup.Use(middlewares.AuthMiddleware())

	booksGroup := apiGroup.Group("/books")
	adminGroup := apiGroup.Group("/admin")
	telegramGroup := route.Group("/telegram")

	adminGroup.Use(middlewares.AdminMiddleware())

	// Books group for all users
	{
		booksGroup.GET("/list", api.GetBooks)
		booksGroup.GET("/get/:format/:id", api.CdnBookGenerate)

		booksGroup.GET("/langs", api.GetLangs)
		booksGroup.GET("/self-user", api.SelfUser)
		booksGroup.POST("/change-me", api.ChangeUser)
		booksGroup.GET("/authors", api.GetAuthors)
		booksGroup.POST("/author", api.GetAuthor)
		booksGroup.POST("/upload-book", api.UploadBook)
		// Download book API endpoint
		booksGroup.POST("/file", api.GetBookFile)
		booksGroup.POST("/fav", api.FavBook)
	}

	// Admin group
	{
		adminGroup.POST("/users", api.GetUsers)
		adminGroup.GET("/scan", api.StartScan)
		adminGroup.GET("/covers", api.UpdateCovers)
		adminGroup.GET("/invites", api.GetInvites)
		adminGroup.POST("/invite", api.ChangeInvite)
		adminGroup.POST("/user", api.ActionUser)
		adminGroup.POST("/update-book", api.UpdateBook)
	}

	// Telegram Bot Group
	{
		telegramGroup.POST("/:id", api.TokenApiEndpoint)
	}

	err := route.Run("0.0.0.0:8085")
	if err != nil {
		log.Fatalln(err)
	}
}
