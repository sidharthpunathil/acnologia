package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// A file dropped on the Assistant is read in the browser and sent as text
// after what the user typed (web/src/pages/Assistant.tsx joins them with a
// blank line). Whatever its format, the keys in it must reach neither the
// model nor the transcript nor the stored conversation.
func TestAssistantDroppedFilesNeverReachTheModel(t *testing.T) {
	// Every value below is fake and only shaped like the real thing.
	cases := []struct {
		name    string
		file    string
		secrets []string
	}{
		{
			name: ".env file",
			file: "ANTHROPIC_API_KEY=sk-ant-api03-FAKEfakeFAKEfake0123456789abcdefABCDEF\n" +
				"GEMINI_API_KEY=AIzaSyFAKEfake0123456789abcdefghijklmno\n" +
				"# comment line\nTAVILY_API_KEY=\"tvly-dev-FAKE0123456789abcdefghij\"\n",
			secrets: []string{
				"sk-ant-api03-FAKEfakeFAKEfake0123456789abcdefABCDEF",
				"AIzaSyFAKEfake0123456789abcdefghijklmno",
				"tvly-dev-FAKE0123456789abcdefghij",
			},
		},
		{
			name:    "JSON export",
			file:    `[{"label":"acct-1","key":"serp_FAKE_0123456789abcdef"},{"label":"acct-2","api_key":"serp_FAKE_fedcba9876543210"}]`,
			secrets: []string{"serp_FAKE_0123456789abcdef", "serp_FAKE_fedcba9876543210"},
		},
		{
			name:    "CSV with a header",
			file:    "label,secret,notes\nteam-a,ghp_FAKEfake0123456789abcdefABCDEF012345,primary\nteam-b,xoxb-FAKE-0123456789-abcdefghijkl,backup\n",
			secrets: []string{"ghp_FAKEfake0123456789abcdefABCDEF012345", "xoxb-FAKE-0123456789-abcdefghijkl"},
		},
		{
			name: "notes with a key in a URL and a header",
			file: "curl 'https://api.example.test/v1/search?q=x&api_key=FAKEurlKEY0123456789abcd'\n" +
				"Authorization: Bearer FAKEbearerTOKEN0123456789abcdefgh\n",
			secrets: []string{"FAKEurlKEY0123456789abcd", "FAKEbearerTOKEN0123456789abcdefgh"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			f := &fakeAnthropicHTTP{replies: []string{text("Got them.")}}
			e.useFakeAnthropic(f)
			id := e.newChat(e.super)

			// Exactly what the browser sends for a drop: typed text, a blank line, the file.
			e.say(id, "add these keys\n\n"+tc.file)

			sent := f.all()
			if sent == "" {
				t.Fatal("no request reached the fake model")
			}
			var got struct{ Items []chatItem }
			e.do(e.super, "GET", "/api/admin/chat/threads/"+itoa(int(id)), nil, 200, &got)
			shown, _ := json.Marshal(got.Items)
			stored, _ := json.Marshal(e.chatData(id))
			for _, s := range tc.secrets {
				if strings.Contains(sent, s) {
					t.Errorf("a dropped secret reached the model: %s", s)
				}
				if strings.Contains(string(shown), s) {
					t.Errorf("a dropped secret is in the transcript: %s", s)
				}
				if strings.Contains(string(stored), s) {
					t.Errorf("a dropped secret is stored in the conversation: %s", s)
				}
			}
		})
	}
}
