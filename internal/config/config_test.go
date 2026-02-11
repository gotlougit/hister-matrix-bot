package config

import "testing"

func TestParse_AppliesDefaults(t *testing.T) {
	raw := []byte(`
matrix:
  homeserver_url: https://matrix.example.org
  user_id: "@bot:example.org"
  access_token: token
  bot_display_name: bot
  allowed_room_ids:
    - "!abc:example.org"
hister:
  base_url: http://localhost:8080
`)

	cfg, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if cfg.Bot.MaxResults != 5 {
		t.Fatalf("expected default max_results=5, got %d", cfg.Bot.MaxResults)
	}
	if cfg.Hister.AddPath != "/add" {
		t.Fatalf("expected default add_path, got %q", cfg.Hister.AddPath)
	}
}

func TestValidate_RejectsInvalid(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = ""
	cfg.Matrix.UserID = ""
	cfg.Matrix.AccessToken = ""
	cfg.Matrix.BotDisplayName = ""
	cfg.Matrix.AllowedRoomIDs = nil
	cfg.Hister.BaseURL = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}
