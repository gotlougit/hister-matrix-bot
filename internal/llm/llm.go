package llm

import (
	"bufio"
	"context"
	"fmt"
	openai "github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"log"
	"os"
	"strings"
)

const SYSTEM_PROMPT = `Extract topics from Matrix chat text.

You will receive plain text where most lines look like:
<sender>: <message>

Rules:
- Output only topic bullets, each starting with "- ".
- Topic bullets must be short noun phrases, not full sentences.
- Keep each bullet under 12 words.
- Include only topics grounded in the input.
- Include URLs only if central to a topic.
- No preamble, headings, code fences, or extra commentary.
- Return 1 to 6 bullets.
`

// const MODEL = "gemma3:270m"
const MODEL = "qwen3:0.6b"

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
	topics, err := ExtractTopicsFromChatsWithError(chats, client, ctx)
	if err != nil {
		log.Fatal(err)
	}
	return topics
}

func ExtractTopicsFromChatsWithError(chats string, client openai.Client, ctx context.Context) (string, error) {
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
		return "", fmt.Errorf("llm stream: %w", stream.Err())
	}

	return topics, nil
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
