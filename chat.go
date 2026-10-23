package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	// 画面へ送るイベント。どちらもどのディレクトリの話かを dir で示す
	chatMessageEvent = "chat-message"
	chatStateEvent   = "chat-state"
	// stream-json の 1 行の上限。ファイルの中身を読んだ結果などで長くなる
	maxStreamLine = 16 << 20
)

// ChatMessage は Claude パネルに出す 1 件
type ChatMessage struct {
	// user / assistant / tool（Claude が使ったツール）/ error
	Role string `json:"role"`
	Text string `json:"text"`
}

// ChatState は今のディレクトリの会話
type ChatState struct {
	Dir       string        `json:"dir"`
	SessionID string        `json:"sessionId"`
	Messages  []ChatMessage `json:"messages"`
	Running   bool          `json:"running"`
}

// chatSession はディレクトリ 1 つに対応する Claude Code のセッション
type chatSession struct {
	id       string
	messages []ChatMessage
	// 一度でも Claude Code に送ったか。送っていれば次からは --resume で続ける
	started bool
	cmd     *exec.Cmd
	stopped bool
}

type chatStore struct {
	mu       sync.Mutex
	sessions map[string]*chatSession
}

// session は dir のセッションを返す。初めてのディレクトリなら新しいセッション ID を割り当てて保存する
func (a *App) session(dir string) (*chatSession, error) {
	if s, ok := a.chats.sessions[dir]; ok {
		return s, nil
	}
	id := loadConfig().Sessions[dir]
	if id == "" {
		id = newUUID()
		if err := updateConfig(func(c *config) {
			if c.Sessions == nil {
				c.Sessions = map[string]string{}
			}
			c.Sessions[dir] = id
		}); err != nil {
			return nil, err
		}
	}
	s := &chatSession{id: id, messages: loadChatLog(id)}
	s.started = len(s.messages) > 0
	if a.chats.sessions == nil {
		a.chats.sessions = map[string]*chatSession{}
	}
	a.chats.sessions[dir] = s
	return s, nil
}

// GetChat は今開いているディレクトリの会話を返す
func (a *App) GetChat() (ChatState, error) {
	dir := a.dir()
	a.chats.mu.Lock()
	defer a.chats.mu.Unlock()
	s, err := a.session(dir)
	if err != nil {
		return ChatState{}, err
	}
	return ChatState{Dir: dir, SessionID: s.id, Messages: append([]ChatMessage{}, s.messages...), Running: s.cmd != nil}, nil
}

// SendChat は今開いているディレクトリのセッションで Claude Code に text を送る。
// 応答は届いた順にイベントで画面へ送るので、ここでは起動だけして戻る
func (a *App) SendChat(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("メッセージが空です")
	}
	dir := a.dir()
	a.chats.mu.Lock()
	defer a.chats.mu.Unlock()
	s, err := a.session(dir)
	if err != nil {
		return err
	}
	if s.cmd != nil {
		return fmt.Errorf("Claude がまだ返事をしています")
	}

	// auto モード: 安全な操作は自動で許可し、危なそうな操作は Claude Code が断る
	args := []string{"--print", "--output-format", "stream-json", "--verbose", "--permission-mode", "auto"}
	if s.started {
		args = append(args, "--resume", s.id)
	} else {
		args = append(args, "--session-id", s.id)
	}
	cmd := exec.Command("claude", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("claude コマンドを起動できませんでした: %w", err)
	}
	s.cmd, s.started, s.stopped = cmd, true, false
	a.addChatMessage(dir, s, ChatMessage{Role: "user", Text: text})
	runtime.EventsEmit(a.ctx, chatStateEvent, map[string]any{"dir": dir, "running": true})

	go a.readChat(dir, s, cmd, stdout, &stderr)
	return nil
}

// readChat は Claude Code の出力を 1 行ずつ読み、会話に足していく
func (a *App) readChat(dir string, s *chatSession, cmd *exec.Cmd, stdout io.Reader, stderr *bytes.Buffer) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxStreamLine)
	for scanner.Scan() {
		for _, m := range parseStreamLine(scanner.Bytes()) {
			a.chats.mu.Lock()
			a.addChatMessage(dir, s, m)
			a.chats.mu.Unlock()
		}
	}
	err := cmd.Wait()

	a.chats.mu.Lock()
	defer a.chats.mu.Unlock()
	switch {
	case s.stopped:
		a.addChatMessage(dir, s, ChatMessage{Role: "error", Text: "止めました"})
	case err != nil:
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		a.addChatMessage(dir, s, ChatMessage{Role: "error", Text: msg})
	}
	s.cmd = nil
	runtime.EventsEmit(a.ctx, chatStateEvent, map[string]any{"dir": dir, "running": false})
}

// StopChat は今開いているディレクトリで返事を書いている Claude Code を止める
func (a *App) StopChat() error {
	dir := a.dir()
	a.chats.mu.Lock()
	defer a.chats.mu.Unlock()
	s, ok := a.chats.sessions[dir]
	if !ok || s.cmd == nil {
		return nil
	}
	s.stopped = true
	return s.cmd.Process.Kill()
}

// addChatMessage は会話に 1 件足して保存し、画面へ送る。a.chats.mu を持って呼ぶ
func (a *App) addChatMessage(dir string, s *chatSession, m ChatMessage) {
	s.messages = append(s.messages, m)
	saveChatLog(s.id, s.messages)
	runtime.EventsEmit(a.ctx, chatMessageEvent, map[string]any{"dir": dir, "message": m})
}

// streamLine は stream-json の 1 行のうち、画面に出す部分
type streamLine struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
	} `json:"message"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
}

// parseStreamLine は Claude の文章と、使ったツールの要約を取り出す
func parseStreamLine(line []byte) []ChatMessage {
	var l streamLine
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	var out []ChatMessage
	switch l.Type {
	case "assistant":
		for _, c := range l.Message.Content {
			switch c.Type {
			case "text":
				if t := strings.TrimSpace(c.Text); t != "" {
					out = append(out, ChatMessage{Role: "assistant", Text: t})
				}
			case "tool_use":
				out = append(out, ChatMessage{Role: "tool", Text: summarizeTool(c.Name, c.Input)})
			}
		}
	case "result":
		if l.IsError {
			out = append(out, ChatMessage{Role: "error", Text: strings.TrimSpace(l.Result)})
		}
	}
	return out
}

// summarizeTool はツールの呼び出しを「Edit app.go」のような 1 行にする
func summarizeTool(name string, input map[string]any) string {
	for _, key := range []string{"file_path", "path", "command", "pattern", "url", "description"} {
		if v, ok := input[key].(string); ok && v != "" {
			v = strings.ReplaceAll(v, "\n", " ")
			if r := []rune(v); len(r) > 120 {
				v = string(r[:120]) + "…"
			}
			return name + " " + v
		}
	}
	return name
}

func chatLogPath(id string) (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "chats", id+".json"), nil
}

func loadChatLog(id string) []ChatMessage {
	messages := []ChatMessage{}
	path, err := chatLogPath(id)
	if err != nil {
		return messages
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &messages)
	}
	return messages
}

func saveChatLog(id string, messages []ChatMessage) {
	path, err := chatLogPath(id)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	if data, err := json.Marshal(messages); err == nil {
		_ = os.WriteFile(path, data, 0o600)
	}
}

// newUUID は Claude Code の --session-id に渡す UUID（バージョン 4）を作る
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
