package codexcmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
)

// Model describes a model returned by the Codex app-server model/list method.
type Model struct {
	ID                     string                  `json:"id"`
	Model                  string                  `json:"model"`
	DisplayName            string                  `json:"displayName"`
	Description            string                  `json:"description"`
	Hidden                 bool                    `json:"hidden"`
	DefaultReasoningEffort string                  `json:"defaultReasoningEffort"`
	SupportedReasoning     []ReasoningEffortOption `json:"supportedReasoningEfforts"`
}

// ReasoningEffortOption is one model-supported reasoning level.
type ReasoningEffortOption struct {
	ReasoningEffort string `json:"reasoningEffort"`
	Description     string `json:"description"`
}

type rpcEnvelope struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// ListModels asks the installed Codex app-server for the models available to
// its current authentication context. The caller controls the timeout via ctx.
func ListModels(ctx context.Context, command, workingDir string, env []string) ([]Model, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		command = "codex app-server"
	}
	if !strings.Contains(command, "--listen") && !strings.Contains(command, "--stdio") {
		command += " --listen stdio://"
	}

	cmd, direct, err := CommandContext(ctx, command)
	if err != nil {
		return nil, err
	}
	if !direct {
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, "cmd", "/D", "/S", "/C", command)
		} else {
			cmd = exec.CommandContext(ctx, "sh", "-lc", command)
		}
	}
	cmd.Dir = workingDir
	if len(env) > 0 {
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create Codex stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create Codex stdout: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Codex app-server: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	responses := make(chan rpcEnvelope, 8)
	errors := make(chan error, 1)
	go scanRPC(stdout, responses, errors)

	if err := writeRPC(stdin, 1, "initialize", map[string]interface{}{
		"clientInfo": map[string]string{"name": "simphony-model-catalog", "version": "0.1.0"},
	}); err != nil {
		return nil, err
	}
	if _, err := waitRPC(ctx, 1, responses, errors); err != nil {
		return nil, fmt.Errorf("initialize Codex app-server: %w%s", err, stderrSuffix(stderr.String()))
	}

	var models []Model
	var cursor interface{}
	for requestID := 2; ; requestID++ {
		params := map[string]interface{}{"limit": 100}
		if cursor != nil {
			params["cursor"] = cursor
		}
		if err := writeRPC(stdin, requestID, "model/list", params); err != nil {
			return nil, err
		}
		result, err := waitRPC(ctx, requestID, responses, errors)
		if err != nil {
			return nil, fmt.Errorf("list Codex models: %w%s", err, stderrSuffix(stderr.String()))
		}
		var page struct {
			Data       []Model     `json:"data"`
			NextCursor interface{} `json:"nextCursor"`
		}
		if err := json.Unmarshal(result, &page); err != nil {
			return nil, fmt.Errorf("decode Codex model list: %w", err)
		}
		models = append(models, page.Data...)
		if page.NextCursor == nil || fmt.Sprint(page.NextCursor) == "" {
			break
		}
		cursor = page.NextCursor
	}
	return models, nil
}

func scanRPC(r io.Reader, responses chan<- rpcEnvelope, errors chan<- error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		var msg rpcEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil || len(msg.ID) == 0 {
			continue
		}
		responses <- msg
	}
	if err := scanner.Err(); err != nil {
		errors <- err
		return
	}
	errors <- io.EOF
}

func writeRPC(w io.Writer, id int, method string, params interface{}) error {
	return json.NewEncoder(w).Encode(map[string]interface{}{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
}

func waitRPC(ctx context.Context, id int, responses <-chan rpcEnvelope, errors <-chan error) (json.RawMessage, error) {
	for {
		select {
		case msg := <-responses:
			var responseID int
			if err := json.Unmarshal(msg.ID, &responseID); err != nil || responseID != id {
				continue
			}
			if msg.Error != nil {
				return nil, fmt.Errorf("RPC %d: %s", msg.Error.Code, msg.Error.Message)
			}
			return msg.Result, nil
		case err := <-errors:
			return nil, err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func stderrSuffix(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return ": " + value
}
