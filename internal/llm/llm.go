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

const SYSTEM_PROMPT = `You summarize Matrix chat history into key topics.

You will receive plain text only as the user message.
Format of most lines:
@user:server: message text

Important input details:
- The transcript is raw text passed directly as one user message.
- Most lines are "<sender>: <message>".
- A message body may contain embedded newlines, so some continuation lines may not start with a sender.
- Blank lines may appear.

Summarization rules:
- Output only bullet points, each starting with "- ".
- Write 2 to 6 bullets total.
- Each bullet is one sentence, concise and specific.
- Focus on concrete topics, decisions, actions, and shared links.
- Include URLs only when they are clearly relevant to the topic.
- Do not include preamble, headings, XML, code fences, or extra commentary.
- Do not invent facts beyond the provided transcript.
- Do not use words like "team" to describe the group.

Input example:
@alice:matrix.org: https://github.com/foo/bar
@bob:matrix.org: Wow, nice
@carol:matrix.org: been using bar for a bit, I think it is easiest way to
manage ssh keys

@mike:matrix.org: moving to using dnscrypt-proxy instead of resolved
@sara:matrix.org: yeah lmk when you have config, I'll copy ;)
@mike:matrix.org: go-based code is so nice

@bob:matrix.org: https://github.com/bob/project
@bob:matrix.org: been working on this idea to sandbox more of my devtools from my system
@bob:matrix.org: it uses microVMs under the hood but isn't annoying to setup.
@bob:matrix.org: see if it interests you
@mike:matrix.org: wow cool, and it is rust [tm] ;)
@sara:matrix.org: building as we speak

Output example:
- ssh key management using bar (https://github.com/foo/bar)
- dnscrypt-proxy migration idea
- bob shares project (https://github.com/bob/project) to use microVMs for sandboxing, mike and sara approve of this

Return only the bullet points with the summarized topics extracted out of it.
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
