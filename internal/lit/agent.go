package lit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// An Agent turns a prompt into a reply. Two kinds are built in: a command that reads the prompt on standard
// input and writes the reply on standard output (any model or script will do), and the Anthropic Messages API
// over net/http. Tests (and other tools) can supply their own.
type Agent interface {
	Name() string
	Ask(ctx context.Context, system, prompt string) (string, error)
}

// AgentFunc adapts a function to an Agent.
type AgentFunc func(system, prompt string) (string, error)

func (f AgentFunc) Name() string { return "func" }
func (f AgentFunc) Ask(_ context.Context, system, prompt string) (string, error) {
	return f(system, prompt)
}

// CommandAgent runs a shell command. The system text and the prompt are written to its standard input, and
// whatever it prints is the reply.
type CommandAgent struct{ Cmd string }

func (a CommandAgent) Name() string { return "command: " + a.Cmd }

func (a CommandAgent) Ask(ctx context.Context, system, prompt string) (string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", a.Cmd)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", a.Cmd)
	}
	cmd.Stdin = strings.NewReader(system + "\n\n" + prompt)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 400 {
			msg = msg[:400] + "..."
		}
		return "", fmt.Errorf("agent command failed: %v: %s", err, msg)
	}
	return out.String(), nil
}

// APIAgent talks to the Anthropic Messages API.
type APIAgent struct {
	Key, Model, BaseURL string
	MaxTokens           int
	Client              *http.Client
}

func (a APIAgent) Name() string { return "anthropic " + a.Model }

func (a APIAgent) Ask(ctx context.Context, system, prompt string) (string, error) {
	base := a.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	max := a.MaxTokens
	if max == 0 {
		max = 8192
	}
	body, _ := json.Marshal(map[string]any{
		"model": a.Model, "max_tokens": max, "system": system,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", a.Key)
	req.Header.Set("anthropic-version", "2023-06-01")
	cl := a.Client
	if cl == nil {
		cl = &http.Client{Timeout: 5 * time.Minute}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		msg := string(data)
		if len(msg) > 400 {
			msg = msg[:400] + "..."
		}
		return "", fmt.Errorf("anthropic API: %s: %s", resp.Status, msg)
	}
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("anthropic API: unreadable reply: %v", err)
	}
	var sb strings.Builder
	for _, c := range r.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), nil
}

// DefaultModel is used with the API when no model is named.
const DefaultModel = "claude-sonnet-5-5"

// NewAgent builds an agent from a specification: "anthropic" or "anthropic:MODEL" for the API, anything else
// is a command. With no specification it looks at $LIT_AGENT, then at $ANTHROPIC_API_KEY.
func NewAgent(spec, model string) (Agent, error) {
	if spec == "" {
		spec = os.Getenv("LIT_AGENT")
	}
	if spec == "" && os.Getenv("ANTHROPIC_API_KEY") != "" {
		spec = "anthropic"
	}
	if spec == "" {
		return nil, errors.New("no agent: pass --agent 'COMMAND' (reads the prompt on stdin, prints the reply), or --agent anthropic with ANTHROPIC_API_KEY set; $LIT_AGENT works too")
	}
	if spec == "anthropic" || strings.HasPrefix(spec, "anthropic:") {
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, errors.New("ANTHROPIC_API_KEY is not set")
		}
		if i := strings.Index(spec, ":"); i >= 0 && model == "" {
			model = spec[i+1:]
		}
		if model == "" {
			model = os.Getenv("LIT_MODEL")
		}
		if model == "" {
			model = DefaultModel
		}
		return APIAgent{Key: key, Model: model, BaseURL: os.Getenv("ANTHROPIC_BASE_URL")}, nil
	}
	return CommandAgent{Cmd: spec}, nil
}
