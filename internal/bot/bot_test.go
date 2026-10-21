package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type generatorFunc func(context.Context, Request) (Response, error)

func (f generatorFunc) Generate(ctx context.Context, r Request) (Response, error) { return f(ctx, r) }

func TestMain(m *testing.M) {
	if os.Getenv("GO_BOT_HELPER") == "1" {
		input, _ := io.ReadAll(os.Stdin)
		if path := os.Getenv("GO_BOT_CAPTURE"); path != "" {
			data, _ := json.Marshal(struct {
				Args  []string
				Input string
			}{os.Args[1:], string(input)})
			_ = os.WriteFile(path, data, 0600)
		}
		switch os.Getenv("GO_BOT_MODE") {
		case "sleep":
			time.Sleep(10 * time.Second)
		case "fail":
			io.WriteString(os.Stderr, "secret-host-details")
			os.Exit(9)
		case "overflow":
			io.WriteString(os.Stdout, strings.Repeat("x", 200000))
			os.Exit(0)
		case "invalid":
			io.WriteString(os.Stdout, "not json")
			os.Exit(0)
		case "error":
			io.WriteString(os.Stdout, `{"is_error":true,"result":"secret-host-details"}`)
			os.Exit(0)
		case "sidecar":
			_ = Serve(context.Background(), bytes.NewReader(input), os.Stdout, generatorFunc(func(_ context.Context, request Request) (Response, error) {
				provider := request.Provider
				if provider == "auto" {
					provider = "codex"
				}
				return Response{Provider: provider, Output: "reply:" + request.Prompt}, nil
			}))
			os.Exit(0)
		}
		output := "Hello from the host CLI 🌍"
		if os.Getenv("GO_BOT_MODE") == "long" {
			output = strings.Repeat("界", 20000)
		}
		claude := false
		for _, arg := range os.Args[1:] {
			if arg == "--print" {
				claude = true
			}
		}
		if claude {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"result": output, "is_error": false})
		} else {
			io.WriteString(os.Stdout, output)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func helperCLI(t *testing.T, mode string) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_BOT_HELPER", "1")
	t.Setenv("GO_BOT_MODE", mode)
	return path
}

func TestParseCommands(t *testing.T) {
	for _, tc := range []struct {
		text, provider, prompt string
		command, invalid       bool
	}{
		{"hello", "", "", false, false},
		{"/botanical tree", "", "", false, false},
		{"/bot", "", "", true, true},
		{"/bot claude", "", "", true, true},
		{" /bot  explain sockets\nwith an example ", "auto", "explain sockets\nwith an example", true, false},
		{"/bot codex --config x=$(touch marker)", "codex", "--config x=$(touch marker)", true, false},
		{"/bot\tclaude\thello", "claude", "hello", true, false},
	} {
		r, command, err := Parse(tc.text)
		if command != tc.command || (err != nil) != tc.invalid || (!tc.invalid && command && (r.Provider != tc.provider || r.Prompt != tc.prompt)) {
			t.Fatalf("%q: %+v, %v, %v", tc.text, r, command, err)
		}
	}
}

func TestCLIUsesLiteralStdinAndRestrictedFlags(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			path := helperCLI(t, "")
			capture := filepath.Join(t.TempDir(), "args.json")
			t.Setenv("GO_BOT_CAPTURE", capture)
			prompt := "--dangerously-bypass-approvals-and-sandbox $(touch marker)\n'quoted'"
			result, err := (CLI{CodexPath: path, ClaudePath: path}).Generate(context.Background(), Request{Provider: provider, Prompt: prompt})
			if err != nil || result.Provider != provider || result.Output != "Hello from the host CLI 🌍" {
				t.Fatalf("%+v, %v", result, err)
			}
			data, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			var captured struct {
				Args  []string
				Input string
			}
			if err := json.Unmarshal(data, &captured); err != nil {
				t.Fatal(err)
			}
			if captured.Input != prompt {
				t.Fatalf("prompt was changed: %q", captured.Input)
			}
			joined := strings.Join(captured.Args, " ")
			if strings.Contains(joined, "touch marker") || strings.Contains(joined, "dangerously") {
				t.Fatalf("prompt became command arguments: %q", joined)
			}
			if provider == "codex" && !strings.Contains(joined, "--sandbox read-only") {
				t.Fatal("missing sandbox")
			}
			if provider == "claude" && !strings.Contains(joined, "--permission-mode dontAsk") {
				t.Fatal("missing permission mode")
			}
		})
	}
}

func TestCLIAutoAndErrors(t *testing.T) {
	path := helperCLI(t, "")
	missing := filepath.Join(t.TempDir(), "missing")
	result, err := (CLI{CodexPath: missing, ClaudePath: path}).Generate(context.Background(), Request{Provider: "auto", Prompt: "Hello"})
	if err != nil || result.Provider != "claude" {
		t.Fatalf("auto fallback: %+v, %v", result, err)
	}
	result, err = (CLI{CodexPath: path, ClaudePath: missing}).Generate(context.Background(), Request{Provider: "claude", Prompt: "Hello"})
	if err != nil || result.Provider != "codex" {
		t.Fatalf("missing Claude did not fall back to Codex: %+v, %v", result, err)
	}
	if _, err := (CLI{CodexPath: missing, ClaudePath: missing}).Generate(context.Background(), Request{Provider: "claude", Prompt: "Hello"}); err == nil {
		t.Fatal("missing CLI accepted")
	}
	for _, mode := range []string{"fail", "invalid", "error", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			helperCLI(t, mode)
			_, err := (CLI{ClaudePath: path}).Generate(context.Background(), Request{Provider: "claude", Prompt: "Hello"})
			if err == nil || strings.Contains(err.Error(), "secret-host-details") {
				t.Fatalf("error leaked or was swallowed: %v", err)
			}
		})
	}
	helperCLI(t, "long")
	result, err = (CLI{CodexPath: path}).Generate(context.Background(), Request{Provider: "codex", Prompt: "Hello"})
	if err != nil || len([]rune(result.Output)) != MaxOutput || !strings.HasSuffix(result.Output, "[Output truncated]") {
		t.Fatalf("long output: %d, %v", len([]rune(result.Output)), err)
	}
}

func TestCLITimeout(t *testing.T) {
	path := helperCLI(t, "sleep")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := (CLI{CodexPath: path}).Generate(ctx, Request{Provider: "codex", Prompt: "Hello"})
	if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(started) > 3*time.Second {
		t.Fatalf("timeout: %v", err)
	}
}

func TestSidecarClientProtocol(t *testing.T) {
	path := helperCLI(t, "sidecar")
	result, err := (Client{Path: path, Provider: "claude"}).Generate(context.Background(), Request{Provider: "auto", Prompt: "Literal $() input"})
	if err != nil || result.Provider != "claude" || result.Output != "reply:Literal $() input" {
		t.Fatalf("sidecar: %+v, %v", result, err)
	}
	helperCLI(t, "invalid")
	if _, err := (Client{Path: path}).Generate(context.Background(), Request{Provider: "auto", Prompt: "Hello"}); err == nil {
		t.Fatal("invalid sidecar output accepted")
	}
}

func TestServeRejectsInvalidRequests(t *testing.T) {
	calls := 0
	generator := generatorFunc(func(context.Context, Request) (Response, error) { calls++; return Response{}, errors.New("unexpected") })
	for _, input := range []string{`{`, `null`, `{"provider":"shell","prompt":"hello"}`, `{"provider":"codex","prompt":""}`, `{"provider":"codex","prompt":"hello","args":["evil"]}`, `{"provider":"codex","prompt":"hello"} {}`} {
		var output bytes.Buffer
		if err := Serve(context.Background(), strings.NewReader(input), &output, generator); err != nil {
			t.Fatal(err)
		}
		var response Response
		if err := json.Unmarshal(output.Bytes(), &response); err != nil || response.Error == "" {
			t.Fatalf("invalid request accepted: %s", output.String())
		}
	}
	if calls != 0 {
		t.Fatal("invalid request reached provider")
	}
}

func TestChunksPreserveUnicodeAndWhitespace(t *testing.T) {
	text := strings.Repeat("🌍 ", 600)
	chunks := Chunks(text)
	if strings.Join(chunks, "") != text || len(chunks) != 3 {
		t.Fatal("chunks changed output")
	}
	for _, chunk := range chunks {
		if len([]rune(chunk)) > 500 {
			t.Fatal("chunk too long")
		}
	}
	if !reflect.DeepEqual(Chunks(""), []string(nil)) {
		t.Fatal("empty output made a message")
	}
}
