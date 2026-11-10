package chatops

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"go.uber.org/zap"
	"zdxsv/pkg/zlog"
)

const (
	interactionPing               = 1
	interactionApplicationCommand = 2
	optionSubCommand              = 1

	responsePong                    = 1
	responseChannelMessage          = 4
	responseDeferredChannelMessage  = 5
	messageFlagEphemeral            = 64
	discordMessageMax               = 2000
	updateTimeout                   = 2 * time.Minute
	interactionBodyMax              = 1 << 20
	defaultDiscordAPIURL            = "https://discord.com/api/v10"
	discordSignatureHeader          = "X-Signature-Ed25519"
	discordSignatureTimestampHeader = "X-Signature-Timestamp"
)

// Discord serves a Discord application's interactions endpoint (POST). Each Commands entry is a
// slash command, "op master-up" for the subcommand master-up of /op: it answers at once with a deferred message, runs, and edits that message
// to the result. One command runs at a time.
type Discord struct {
	PublicKey ed25519.PublicKey
	// Allow holds the user and role ids that may run the commands; nobody when empty.
	Allow    map[string]bool
	Commands map[string]func(ctx context.Context) (string, error)
	// APIURL replaces https://discord.com/api/v10, for tests.
	APIURL string
	Client *http.Client

	running sync.Mutex
	wg      sync.WaitGroup
}

type interaction struct {
	Type          int    `json:"type"`
	ApplicationID string `json:"application_id"`
	Token         string `json:"token"`
	Data          struct {
		Name    string `json:"name"`
		Options []struct {
			Name string `json:"name"`
			Type int    `json:"type"`
		} `json:"options"`
	} `json:"data"`
	Member *struct {
		User  struct{ ID string } `json:"user"`
		Roles []string            `json:"roles"`
	} `json:"member"`
	User *struct{ ID string } `json:"user"`
}

func (d *Discord) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, interactionBodyMax))
	if err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	sig, err := hex.DecodeString(r.Header.Get(discordSignatureHeader))
	ts := r.Header.Get(discordSignatureTimestampHeader)
	if err != nil || len(d.PublicKey) != ed25519.PublicKeySize || !ed25519.Verify(d.PublicKey, append([]byte(ts), body...), sig) {
		http.Error(w, "invalid request signature", http.StatusUnauthorized)
		return
	}
	var in interaction
	if err := json.Unmarshal(body, &in); err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	switch in.Type {
	case interactionPing:
		writeJSON(w, map[string]int{"type": responsePong})
		return
	case interactionApplicationCommand:
	default:
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	name := in.commandName()
	run, ok := d.Commands[name]
	if !ok {
		reply(w, "unknown command "+name)
		return
	}
	if !d.allowed(&in) {
		reply(w, "not allowed")
		return
	}
	if !d.running.TryLock() {
		reply(w, "another command is running")
		return
	}
	zlog.Info("discord command", zap.Any("name", name), zap.Any("user_id", in.userID()))
	answered := make(chan struct{})
	defer close(answered)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer d.running.Unlock()
		// Discord rejects the edit of a message whose deferred answer it has not got yet.
		<-answered
		ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
		defer cancel()
		msg, err := run(ctx)
		if err != nil {
			zlog.Error("discord command failed", zap.Any("name", name), zap.Error(err))
			msg = name + " failed, nothing changed:\n" + err.Error()
		}
		d.editOriginal(ctx, &in, msg)
	}()
	writeJSON(w, map[string]int{"type": responseDeferredChannelMessage})
}

// Wait returns when the commands started so far have finished.
func (d *Discord) Wait() { d.wg.Wait() }

// commandName is the command's name, followed by " " and the subcommand's for a subcommand.
func (in *interaction) commandName() string {
	if len(in.Data.Options) == 1 && in.Data.Options[0].Type == optionSubCommand {
		return in.Data.Name + " " + in.Data.Options[0].Name
	}
	return in.Data.Name
}

func (in *interaction) userID() string {
	if in.Member != nil {
		return in.Member.User.ID
	}
	if in.User != nil {
		return in.User.ID
	}
	return ""
}

func (d *Discord) allowed(in *interaction) bool {
	if id := in.userID(); id != "" && d.Allow[id] {
		return true
	}
	if in.Member != nil {
		for _, r := range in.Member.Roles {
			if d.Allow[r] {
				return true
			}
		}
	}
	return false
}

func (d *Discord) editOriginal(ctx context.Context, in *interaction, msg string) {
	if r := []rune(msg); len(r) > discordMessageMax {
		msg = string(r[:discordMessageMax-1]) + "…"
	}
	body, _ := json.Marshal(map[string]string{"content": msg})
	api := d.APIURL
	if api == "" {
		api = defaultDiscordAPIURL
	}
	u := api + "/webhooks/" + url.PathEscape(in.ApplicationID) + "/" + url.PathEscape(in.Token) + "/messages/@original"
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u, bytes.NewReader(body))
	if err != nil {
		zlog.Error("discord edit message failed", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		zlog.Error("discord edit message failed", zap.Error(err))
		return
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		zlog.Error("discord edit message failed", zap.String("status", res.Status))
	}
}

func reply(w http.ResponseWriter, msg string) {
	writeJSON(w, map[string]interface{}{
		"type": responseChannelMessage,
		"data": map[string]interface{}{"content": msg, "flags": messageFlagEphemeral},
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
