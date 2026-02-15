package llm

import (
	"bufio"
	"context"
	openai "github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"log"
	"os"
	"strings"
)

const SYSTEM_PROMPT = "You are a smart assistant designed to understand text conversations and extract the topics being discussed in a concise manner."
const MODEL = "gemma3:270m"

func loadEnvFile(filepath string) error {
	file, err := os.Open(filepath)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Parse KEY="value" or KEY=value format
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		// Remove quotes if present
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		os.Setenv(key, value)
	}
	return scanner.Err()
}

func ExtractTopicsFromChats(chats string, client openai.Client, ctx context.Context) string {
	topics := ""
	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(SYSTEM_PROMPT),
		openai.UserMessage(chats),
	}

	stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model:       MODEL,
		Messages:    messages,
		Temperature: openai.Float(0.1),
		TopP:        openai.Float(0.90),
	})

	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) > 0 {
			content := chunk.Choices[0].Delta.Content
			topics += content
		}
	}

	if stream.Err() != nil {
		log.Fatal(stream.Err())
	}

	return topics
}

func InitLLM() openai.Client {
	if err := loadEnvFile(".env"); err != nil {
		log.Printf("Warning: could not load .env file: %v", err)
	}

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable not set")
	}
	baseUrl := os.Getenv("OPENAI_BASE_URL")
	if baseUrl == "" {
		log.Fatal("OPENAI_BASE_URL environment variable not set")
	}
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseUrl),
	)
	return client
}
