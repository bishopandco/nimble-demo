package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"go-api/internal/process"
)

// CLI uses existing host authentication. Executable paths are host configuration.
type CLI struct {
	CodexPath  string
	ClaudePath string
	Dir        string
}

func (c CLI) Generate(ctx context.Context, request Request) (Response, error) {
	if err := validate(request); err != nil {
		return Response{}, err
	}
	return c.generatePrompt(ctx, request.Provider, request.Prompt, false)
}

// Expanded context and tool envelopes stay inside the sidecar, not the public request.
func (c CLI) generatePrompt(ctx context.Context, requested, prompt string, structured bool) (Response, error) {
	provider, path, err := c.provider(requested)
	if err != nil {
		return Response{}, err
	}
	var args []string
	if provider == "codex" {
		args = []string{"--ask-for-approval", "never", "exec", "--sandbox", "read-only", "--ephemeral", "--color", "never", "--skip-git-repo-check", "-"}
	} else {
		args = []string{"--print", "--output-format", "json", "--tools", "", "--permission-mode", "dontAsk", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--setting-sources", "", "--no-session-persistence"}
	}
	stdout, _, err := process.Run(ctx, path, args, strings.NewReader(prompt), c.Dir, 128<<10, 500*time.Millisecond)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return Response{}, errors.New("bot request timed out")
		}
		if errors.Is(err, context.Canceled) {
			return Response{}, err
		}
		if errors.Is(err, process.ErrOutputLimit) {
			return Response{}, errors.New("CLI output exceeded 128 KiB")
		}
		// Do not send host stderr (paths, credentials, internal context) into chat.
		return Response{}, fmt.Errorf("%s CLI failed; check its host login and configuration (%v)", provider, err)
	}
	output := string(stdout)
	if provider == "claude" {
		var result struct {
			Result  string `json:"result"`
			IsError bool   `json:"is_error"`
		}
		if err := json.Unmarshal(stdout, &result); err != nil {
			return Response{}, errors.New("claude CLI returned invalid JSON")
		}
		if result.IsError {
			return Response{}, errors.New("claude CLI reported an error; check its host login and configuration")
		}
		output = result.Result
	}
	output = strings.TrimSpace(output)
	if output == "" || !utf8.ValidString(output) {
		return Response{}, errors.New("CLI returned no valid text")
	}
	if !structured && utf8.RuneCountInString(output) > MaxOutput {
		runes := []rune(output)
		const suffix = "\n[Output truncated]"
		output = string(runes[:MaxOutput-len([]rune(suffix))]) + suffix
	}
	return Response{Provider: provider, Output: output}, nil
}

// provider falls back only when an executable is missing, never after a failed run.
func (c CLI) provider(requested string) (string, string, error) {
	if requested == "claude" {
		if path, err := findCLI(c.ClaudePath, "claude"); err == nil {
			return "claude", path, nil
		}
	}
	if path, err := findCLI(c.CodexPath, "codex"); err == nil {
		return "codex", path, nil
	}
	if requested == "codex" {
		return "", "", missingCLI("codex")
	}
	if requested == "auto" {
		if path, err := findCLI(c.ClaudePath, "claude"); err == nil {
			return "claude", path, nil
		}
	}
	return "", "", errors.New("neither Codex nor Claude CLI was found; set BOT_CODEX_BIN to the Codex executable")
}
