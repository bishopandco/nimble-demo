package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"go-api/internal/process"
)

// Client starts a sidecar binary per prompt. The API never executes provider CLIs.
type Client struct {
	Path     string
	Provider string
}

func (c Client) Generate(ctx context.Context, request Request) (Response, error) {
	if request.Provider == "auto" && c.Provider != "" {
		request.Provider = c.Provider
	}
	if err := validate(request); err != nil {
		return Response{}, err
	}
	input, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}
	stdout, _, err := process.Run(ctx, c.Path, nil, bytes.NewReader(input), "", 128<<10, 2*time.Second)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, errors.New("bot sidecar failed; run make sidecar and check BOT_SIDECAR_PATH")
	}
	var response Response
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	if err := decoder.Decode(&response); err != nil || decoder.Decode(new(any)) != io.EOF {
		return Response{}, errors.New("bot sidecar returned invalid JSON")
	}
	if response.Error != "" {
		return Response{}, errors.New(response.Error)
	}
	if response.Provider != "codex" && response.Provider != "claude" {
		return Response{}, errors.New("bot sidecar returned an invalid provider")
	}
	if strings.TrimSpace(response.Output) == "" || !utf8.ValidString(response.Output) || utf8.RuneCountInString(response.Output) > MaxOutput {
		return Response{}, errors.New("bot sidecar returned invalid output")
	}
	return response, nil
}
