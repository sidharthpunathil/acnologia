package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/notify"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Made up secrets; none is a real key.
const (
	gemKey1   = "AIzaSyA1b2C3d4E5f6G7h8I9j0KlMnOpQrStUv"
	gemKey2   = "AIzaSyZ9y8X7w6V5u4T3s2R1q0PoNmLkJiHgFe"
	urlKey    = "Zq81mXk20pLs9TaB"
	bearerKey = "abcDEF123456ghi"
	poolKeyA  = "serp_pool_key_8c21e"
)

// The UI contract for user items: key_list holds label and hint per key,
// text is the prose with the keys gone and blank runs collapsed, keys is
// the count. Keys keyparse misses (in a URL, after Bearer) are found by the
// pattern detector and vaulted too.
func TestUserItemKeyListContract(t *testing.T) {
	e := newEnv(t)
	f := &fakeClaude{replies: []string{text("Which service?")}}
	e.api.chatModel = f.converse
	id := e.newChat(e.super)
	msg := "Please add these to gemini.\n\n\nprod: " + gemKey1 + "\nstaging " + gemKey2 + "\n\n\n\n" +
		"Also this one https://example.com/v1/models?key=" + urlKey + " works\nBearer " + bearerKey + "\nthanks"
	e.say(id, msg)
	it := e.chatData(id).Items[0]
	want := []chatKeyRef{{"prod", "...StUv"}, {"staging", "...HgFe"}, {"", "...9TaB"}, {"", "...6ghi"}}
	if it.Keys != 4 || len(it.KeyList) != 4 {
		t.Fatalf("keys %d, key_list %+v", it.Keys, it.KeyList)
	}
	for i := range want {
		if it.KeyList[i] != want[i] {
			t.Fatalf("key_list[%d] = %+v, want %+v", i, it.KeyList[i], want[i])
		}
	}
	if wantText := "Please add these to gemini.\n\nAlso this one https://example.com/v1/models?key= works\nthanks"; it.Text != wantText {
		t.Fatalf("text %q\nwant %q", it.Text, wantText)
	}
	// The JSON field names are the contract.
	b, _ := json.Marshal(it)
	if !strings.Contains(string(b), `"key_list":[{"label":"prod","hint":"...StUv"}`) || !strings.Contains(string(b), `"keys":4`) {
		t.Fatalf("item JSON: %s", b)
	}
	for _, sec := range []string{gemKey1, gemKey2, urlKey, bearerKey} {
		if strings.Contains(f.sent[0], sec) || strings.Contains(string(b), sec) {
			t.Fatalf("%s reached the model or the transcript", sec)
		}
	}
	if !strings.Contains(f.sent[0], "?key=[key k3 ...9TaB]") || !strings.Contains(f.sent[0], "Bearer [key k4 ...6ghi]") {
		t.Fatalf("model text: %s", f.sent[0])
	}
	if d := e.chatData(id); len(d.Vault) != 4 {
		t.Fatalf("vault holds %d keys", len(d.Vault))
	}
	// A message without keys keeps its text and has no key_list.
	e.say(id, "and what about serpapi?")
	if it := e.chatData(id).Items; it[len(it)-2].Text != "and what about serpapi?" || it[len(it)-2].KeyList != nil {
		t.Fatalf("plain message: %+v", it[len(it)-2])
	}
}

// A tool result that carries a pool key (here the key's own notes, but an
// upstream error that echoes the key is the same) reaches the Anthropic API
// and the stored transcript redacted.
func TestToolResultWithPoolKeyReachesModelRedacted(t *testing.T) {
	e := newEnv(t)
	note := "backup copy " + poolKeyA
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{
		"keys": []map[string]any{{"secret": poolKeyA, "label": "main", "notes": note}}}, 201, nil)
	f := &fakeAnthropicHTTP{replies: []string{
		toolUse("tu_1", "list_keys", `{"service":"serpapi"}`),
		text("You have one key."),
	}}
	e.useFakeAnthropic(f)
	id := e.newChat(e.super)
	e.say(id, "list my serpapi keys")
	sent := f.all()
	if strings.Contains(sent, poolKeyA) {
		t.Fatal("a pool key reached the Anthropic API")
	}
	if !strings.Contains(sent, "backup copy [key ...c21e]") {
		t.Fatalf("tool result not sent as expected: %s", sent)
	}
	d := e.chatData(id)
	b, _ := json.Marshal(d.Messages)
	ib, _ := json.Marshal(d.Items)
	if strings.Contains(string(b), poolKeyA) || strings.Contains(string(ib), poolKeyA) {
		t.Fatal("the stored transcript holds a pool key")
	}
}

// Events, audit rows, key last_error and webhook payloads are scrubbed at
// the store and notifier, whatever the caller forgot.
func TestStoredTextAndWebhooksAreScrubbed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.do(e.super, "POST", "/api/admin/services/1/keys", map[string]any{
		"keys": []map[string]any{{"secret": poolKeyA, "label": "main"}}}, 201, nil)
	keys, _ := e.st.ListKeys(ctx, 1, true)
	echo := `401 {"error":"invalid key ` + poolKeyA + `","url":"/v1?api_key=0123abcd4567efgh"}`
	e.st.AddEvent(ctx, store.NewEvent{Kind: "invalid", ServiceID: 1, KeyID: keys[0].ID, Detail: echo})
	e.st.AddAudit(ctx, nil, "key.check", "serpapi", echo)
	msg := "invalid key: " + echo
	_ = e.st.ApplyEffect(ctx, keys[0].ID, store.KeyEffect{LastError: &msg})
	evs, _ := e.st.ListEvents(ctx, store.EventFilter{Limit: 10})
	aud, _ := e.st.ListAudit(ctx, 10)
	k, _ := e.st.GetKey(ctx, keys[0].ID)
	all, _ := json.Marshal([]any{evs, aud, k.LastError})
	for _, sec := range []string{poolKeyA, "0123abcd4567efgh"} {
		if strings.Contains(string(all), sec) {
			t.Fatalf("stored text holds %s: %s", sec, all)
		}
	}

	var mu sync.Mutex
	var got string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = string(b)
		mu.Unlock()
	}))
	defer hook.Close()
	enc, _ := e.api.Pool.Box.Encrypt(hook.URL)
	n := notify.New(e.st, e.api.Pool.Box, hook.Client())
	if _, err := n.Deliver(ctx, &store.Webhook{ID: 1, URLEnc: enc, Format: "json"}, notify.Event{Type: notify.KeyInvalid,
		Message: "key rejected: " + echo, Data: map[string]any{"detail": echo}}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got == "" || strings.Contains(got, poolKeyA) || strings.Contains(got, "0123abcd4567efgh") {
		t.Fatalf("webhook payload: %s", got)
	}
}
