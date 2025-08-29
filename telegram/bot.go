package telegram

import (
	"fmt"
	"gopds-api/logging"
	"log"
	"net/http"
	"strings"
	"sync"

	"gopds-api/database"

	"github.com/gin-gonic/gin"
	tele "gopkg.in/telebot.v3"
)

// BotManager управляет Telegram ботами, связанными с пользователями системы
type BotManager struct {
	bots   map[string]*Bot // token -> Bot
	mutex  sync.RWMutex
	config *Config
}

// Bot представляет бот, связанный с пользователем системы
type Bot struct {
	token  string
	bot    *tele.Bot
	userID int64 // ID пользователя в нашей системе, владельца этого бота
}

// Config содержит настройки для ботов
type Config struct {
	BaseURL string // базовый URL для webhook'ов
}

// NewBotManager создает новый менеджер ботов
func NewBotManager(config *Config) *BotManager {
	return &BotManager{
		bots:   make(map[string]*Bot),
		config: config,
	}
}

// InitializeExistingBots инициализирует ботов для всех пользователей с токенами
func (bm *BotManager) InitializeExistingBots() error {
	// Получаем всех пользователей с токенами ботов
	users, err := database.GetUsersWithBotTokens()
	if err != nil {
		return fmt.Errorf("failed to get users with bot tokens: %v", err)
	}

	log.Printf("Found %d users with bot tokens, initializing bots...", len(users))

	for _, user := range users {
		err := bm.createBotForUser(user.BotToken, user.ID)
		if err != nil {
			log.Printf("Failed to initialize bot for user %d: %v", user.ID, err)
			continue
		}

		// Устанавливаем webhook
		err = bm.SetWebhook(user.BotToken)
		if err != nil {
			log.Printf("Failed to set webhook for user %d: %v", user.ID, err)
		}
	}

	log.Printf("Initialized %d telegram bots", len(bm.bots))
	return nil
}

// CreateBotForUser создает бота для конкретного пользователя
func (bm *BotManager) CreateBotForUser(token string, userID int64) error {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	// Проверяем, не существует ли уже бот с таким токеном
	if _, exists := bm.bots[token]; exists {
		return fmt.Errorf("bot with token already exists")
	}

	return bm.createBotForUser(token, userID)
}

// createBotForUser внутренняя функция создания бота (без мьютекса)
func (bm *BotManager) createBotForUser(token string, userID int64) error {
	bot, err := bm.createBotInstance(token, userID)
	if err != nil {
		return fmt.Errorf("failed to create bot: %v", err)
	}

	bm.bots[token] = bot
	logging.Infof("Bot created successfully for user %d", userID)
	return nil
}

// createBotInstance создает экземпляр бота
func (bm *BotManager) createBotInstance(token string, userID int64) (*Bot, error) {

	// Настройки бота для работы с webhook'ами
	settings := tele.Settings{
		Token: token,
		Poller: &tele.Webhook{
			Listen: "", // Мы будем обрабатывать webhook'и через gin роутер
		},
	}

	teleBot, err := tele.NewBot(settings)
	if err != nil {
		log.Printf("tele.NewBot failed for user %d: %v", userID, err)
		return nil, err
	}

	bot := &Bot{
		token:  token,
		bot:    teleBot,
		userID: userID,
	}

	// Настраиваем обработчики команд
	bot.setupHandlers()

	return bot, nil
}

// setupHandlers настраивает обработчики команд для бота
func (b *Bot) setupHandlers() {
	// Обработчик команды /start
	b.bot.Handle("/start", func(c tele.Context) error {
		// При первом /start связываем telegram_id с пользователем
		telegramID := c.Sender().ID

		// Проверяем, не привязан ли уже этот telegram_id к пользователю
		existingUser, err := database.GetUserByTelegramID(telegramID)
		if err == nil && existingUser.ID != 0 {
			return c.Send("Вы уже связаны с аккаунтом библиотеки!")
		}

		// Связываем telegram_id с пользователем через bot_token
		err = database.UpdateTelegramID(b.token, telegramID)
		if err != nil {
			log.Printf("Failed to update telegram_id for token %s: %v", maskToken(b.token), err)
			return c.Send("Ошибка при связывании аккаунта. Попробуйте позже.")
		}

		return c.Send("Добро пожаловать! Ваш аккаунт успешно связан с библиотекой.")
	})

	// Обработчик команды /search для поиска книг
	b.bot.Handle("/search", func(c tele.Context) error {
		// Проверяем, что пользователь связан
		telegramID := c.Sender().ID
		user, err := database.GetUserByTelegramID(telegramID)
		if err != nil {
			return c.Send("Сначала отправьте /start для связывания аккаунта.")
		}

		// Получаем поисковый запрос
		query := strings.TrimPrefix(c.Text(), "/search ")
		if query == "/search" || query == "" {
			return c.Send("Использование: /search <название книги или автор>")
		}

		// Здесь будет логика поиска книг
		// Пока заглушка
		return c.Send(fmt.Sprintf("Поиск книг по запросу: %s\nПользователь: %s", query, user.Login))
	})

	// Обработчик всех остальных сообщений
	b.bot.Handle(tele.OnText, func(c tele.Context) error {
		// Проверяем, что пользователь связан
		telegramID := c.Sender().ID
		_, err := database.GetUserByTelegramID(telegramID)
		if err != nil {
			return c.Send("Сначала отправьте /start для связывания аккаунта.")
		}

		// Если это не команда, игнорируем
		return c.Send("Используйте команды:\n/start - связать аккаунт\n/search <запрос> - поиск книг")
	})
}

// HandleWebhook обрабатывает входящие webhook'и от Telegram
func (bm *BotManager) HandleWebhook(c *gin.Context) {
	token := c.Param("token")

	bm.mutex.RLock()
	bot, exists := bm.bots[token]
	bm.mutex.RUnlock()

	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Bot not found"})
		return
	}

	// Читаем тело запроса для обработки update
	var update tele.Update
	if err := c.ShouldBindJSON(&update); err != nil {
		log.Printf("Error parsing webhook for token %s: %v", maskToken(token), err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid webhook data"})
		return
	}

	// Обрабатываем update через telebot
	bot.bot.ProcessUpdate(update)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// SetWebhook устанавливает webhook для бота
func (bm *BotManager) SetWebhook(token string) error {
	bm.mutex.RLock()
	bot, exists := bm.bots[token]
	bm.mutex.RUnlock()

	if !exists {
		return fmt.Errorf("bot with token not found")
	}

	webhookURL := fmt.Sprintf("%s/telegram/%s", bm.config.BaseURL, token)

	webhook := &tele.Webhook{
		Endpoint: &tele.WebhookEndpoint{
			PublicURL: webhookURL,
		},
	}

	err := bot.bot.SetWebhook(webhook)
	if err != nil {
		return fmt.Errorf("failed to set webhook: %v", err)
	}

	logging.Infof("Webhook set successfully for user %d: %s", bot.userID, webhookURL)
	return nil
}

// RemoveBot удаляет бота и очищает связь с пользователем
func (bm *BotManager) RemoveBot(token string) error {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	bot, exists := bm.bots[token]
	if !exists {
		return fmt.Errorf("bot with token not found")
	}

	// Удаляем webhook
	if err := bot.bot.RemoveWebhook(); err != nil {
		log.Printf("Warning: failed to remove webhook for bot %s: %v", maskToken(token), err)
	}

	// Останавливаем бота
	bot.bot.Stop()

	// Очищаем токен и telegram_id в базе данных
	err := database.ClearBotToken(bot.userID)
	if err != nil {
		log.Printf("Warning: failed to clear bot token for user %d: %v", bot.userID, err)
	}

	// Удаляем из карты
	delete(bm.bots, token)

	log.Printf("Bot removed successfully for user %d", bot.userID)
	return nil
}

// maskToken маскирует токен для логирования
func maskToken(token string) string {
	if len(token) < 10 {
		return "***"
	}
	return token[:10] + "***"
}
