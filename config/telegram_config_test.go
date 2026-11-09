package config

import "testing"

// The Managed Bots manager's token comes from the environment, as the
// deployment mounts it from Vault (plans/telegram-bot/infra-p1-1.md).

// TestTelegramManagerTokenFromEnv pins GOPDS_TELEGRAM_MANAGER_TOKEN reaching
// telegram.manager.token.
func TestTelegramManagerTokenFromEnv(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)
	setEnv(t, map[string]string{"GOPDS_TELEGRAM_MANAGER_TOKEN": "123:manager-token"})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.Telegram.Manager.Token != "123:manager-token" {
		t.Errorf("Telegram.Manager.Token = %q, want the env value", cfg.Telegram.Manager.Token)
	}
}

// TestTelegramManagerOffByDefault pins that without the variable the token
// is empty: the Managed path stays off.
func TestTelegramManagerOffByDefault(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.Telegram.Manager.Token != "" {
		t.Errorf("Telegram.Manager.Token = %q, want empty", cfg.Telegram.Manager.Token)
	}
}

// TestTelegramCardsDefaultsOn pins that the bots list cards unless told not
// to.
func TestTelegramCardsDefaultsOn(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if !cfg.Telegram.Cards {
		t.Error("Telegram.Cards = false, want true by default")
	}
}

// TestTelegramCardsEnvOff pins GOPDS_TELEGRAM_CARDS=false switching them off:
// the rollback without a release.
func TestTelegramCardsEnvOff(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)
	setEnv(t, map[string]string{"GOPDS_TELEGRAM_CARDS": "false"})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.Telegram.Cards {
		t.Error("Telegram.Cards = true, want the env's false")
	}
}
