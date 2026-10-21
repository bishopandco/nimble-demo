// Package bot implements the one-request JSON sidecar protocol and host CLI adapters.
package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxOutput = 16000
const Usage = "Use /bot [codex|claude] followed by a prompt."

type Request struct {
	Provider string `json:"provider"`
	Prompt   string `json:"prompt"`
}

type Response struct {
	Provider string `json:"provider,omitempty"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
}

type Generator interface {
	Generate(context.Context, Request) (Response, error)
}

// Parse only recognizes a complete /bot command token.
func Parse(text string) (Request, bool, error) {
	command, rest := token(strings.TrimSpace(text))
	if command != "/bot" {
		return Request{}, false, nil
	}
	provider, prompt := token(rest)
	if provider != "codex" && provider != "claude" {
		provider, prompt = "auto", rest
	}
	if strings.TrimSpace(prompt) == "" {
		return Request{}, true, errors.New(Usage)
	}
	return Request{Provider: provider, Prompt: strings.TrimSpace(prompt)}, true, nil
}

func token(text string) (string, string) {
	if i := strings.IndexFunc(text, unicode.IsSpace); i >= 0 {
		return text[:i], strings.TrimSpace(text[i:])
	}
	return text, ""
}

func validProvider(provider string) bool {
	return provider == "auto" || provider == "codex" || provider == "claude"
}

func validate(request Request) error {
	if !validProvider(request.Provider) {
		return errors.New("provider must be auto, codex, or claude")
	}
	if !utf8.ValidString(request.Prompt) || strings.TrimSpace(request.Prompt) == "" || utf8.RuneCountInString(request.Prompt) > 500 {
		return errors.New("prompt must contain 1–500 characters")
	}
	return nil
}

// Serve processes exactly one JSON request, with no command-line input from chat.
func Serve(ctx context.Context, input io.Reader, output io.Writer, generator Generator) error {
	data, err := io.ReadAll(io.LimitReader(input, (16<<10)+1))
	if err != nil {
		return err
	}
	if len(data) > 16<<10 {
		return json.NewEncoder(output).Encode(Response{Error: "sidecar request exceeds 16 KiB"})
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request Request
	err = decoder.Decode(&request)
	if err == nil && decoder.Decode(new(any)) != io.EOF {
		err = errors.New("expected one JSON object")
	}
	if err == nil {
		err = validate(request)
	}
	var response Response
	if err == nil {
		response, err = generator.Generate(ctx, request)
	}
	if err != nil {
		response = Response{Error: err.Error()}
	}
	return json.NewEncoder(output).Encode(response)
}

func missingCLI(provider string) error {
	return fmt.Errorf("%s CLI was not found; set BOT_CODEX_BIN to the Codex executable", provider)
}

// Chunks preserves every character while fitting existing 500-character messages.
func Chunks(output string) []string {
	runes := []rune(output)
	var chunks []string
	for len(runes) > 0 {
		n := min(500, len(runes))
		chunks = append(chunks, string(runes[:n]))
		runes = runes[n:]
	}
	return chunks
}
