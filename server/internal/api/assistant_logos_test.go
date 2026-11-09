package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
)

func sizedPNG(t *testing.T, n int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	img.Set(0, 0, c)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// stubLogos makes find_logo find a small and a clear icon on any site.
func stubLogos(t *testing.T, small, clear []byte) {
	t.Helper()
	oldC, oldD := logoCandidates, downloadLogo
	t.Cleanup(func() { logoCandidates, downloadLogo = oldC, oldD })
	logoCandidates = func(_ context.Context, site string, _ int) ([]catalog.LogoCandidate, error) {
		return []catalog.LogoCandidate{
			{URL: "https://" + site + "/apple-touch-icon.png", Data: clear, Mime: "image/png", Score: 180},
			{URL: "https://" + site + "/favicon.png", Data: small, Mime: "image/png", Score: 16},
		}, nil
	}
	downloadLogo = func(context.Context, string) ([]byte, string, error) { return nil, "", errors.New("not an image") }
}

// The Assistant finds logos and sees them, asks with pictures when it is
// not sure, and sets the one the user picks.
func TestAssistantFindsAsksAndSetsALogo(t *testing.T) {
	e := newEnv(t)
	small, clear := sizedPNG(t, 16, color.RGBA{1, 2, 3, 255}), sizedPNG(t, 180, color.RGBA{9, 8, 7, 255})
	stubLogos(t, small, clear)
	f := &fakeClaude{replies: []string{
		toolUse("tu_1", "find_logo", `{"service":"serpapi","sources":["serpapi.com"]}`),
		toolUse("tu_2", "ask_user", `{"kind":"logo","question":"Which logo?","logos":["logo1","logo2"]}`),
		toolUse("tu_3", "set_service_logo", `{"service":"serpapi","logo":"logo1"}`),
		text("Set."),
	}}
	e.api.chatModel = f.converse
	id := e.newChat(e.super)
	var out chatResp
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "find the serpapi logo"}, 200, &out)

	// The model saw the candidates as pictures inside the tool result.
	if len(f.sent) < 2 || !strings.Contains(f.sent[1], `"type":"image"`) || !strings.Contains(f.sent[1], "[logo1]") {
		t.Fatal("find_logo did not show the candidates to the model")
	}
	var ask *chatItem
	var found *chatItem
	for i := range out.Items {
		switch {
		case out.Items[i].Kind == "ask":
			ask = &out.Items[i]
		case out.Items[i].Tool == "find_logo":
			found = &out.Items[i]
		}
	}
	if found == nil || len(found.Logos) != 2 {
		t.Fatalf("the find_logo row has no thumbnails: %+v", found)
	}
	if ask == nil || ask.Ask.Kind != "logo" || len(ask.Ask.Logos) != 2 || !strings.Contains(ask.Ask.Logos[0].Label, "180px") {
		t.Fatalf("no logo question: %+v", ask)
	}
	// The form's pictures are served from the thread.
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/admin/chat/threads/"+itoa(int(id))+"/logos/logo1", nil)
	resp, err := e.super.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(body, clear) || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("logo not served: %d", resp.StatusCode)
	}

	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/answer", map[string]any{"ask_id": ask.Ask.ID, "choice": "logo1"}, 200, &out)
	if !strings.Contains(f.sent[len(f.sent)-2], "picked logo logo1") {
		t.Fatal("the answer did not reach the model")
	}
	sv, _ := e.st.GetServiceBySlug(context.Background(), "serpapi")
	data, _, err := e.st.ServiceLogo(context.Background(), sv.ID)
	if err != nil || !bytes.Equal(data, clear) {
		t.Fatalf("logo not set from the picked candidate: %v", err)
	}
}

// A logo the user sends as an image is set from the chat.
func TestAssistantSetsALogoFromASentImage(t *testing.T) {
	e := newEnv(t)
	logo := sizedPNG(t, 64, color.RGBA{5, 5, 5, 255})
	f := &fakeClaude{replies: []string{
		toolUse("tu_1", "set_service_logo", `{"service":"serpapi","image_id":"img1"}`),
		text("Set."),
	}}
	e.api.chatModel = f.converse
	id := e.newChat(e.super)
	var out chatResp
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "use this as the serpapi logo",
		"images": []map[string]any{{"media_type": "image/png", "data": base64.StdEncoding.EncodeToString(logo)}}}, 200, &out)
	if !strings.Contains(f.sent[0], "[image img1]") {
		t.Fatal("the model does not see the image id")
	}
	sv, _ := e.st.GetServiceBySlug(context.Background(), "serpapi")
	data, _, err := e.st.ServiceLogo(context.Background(), sv.ID)
	if err != nil || !bytes.Equal(data, logo) {
		t.Fatalf("logo not set from the sent image: %v", err)
	}
}

func TestIcoPNGAndQuality(t *testing.T) {
	p := sizedPNG(t, 48, color.RGBA{1, 1, 1, 255})
	ico := make([]byte, 22)
	binary.LittleEndian.PutUint16(ico[2:], 1)
	binary.LittleEndian.PutUint16(ico[4:], 1)
	ico[6] = 48
	binary.LittleEndian.PutUint32(ico[14:], uint32(len(p)))
	binary.LittleEndian.PutUint32(ico[18:], 22)
	ico = append(ico, p...)
	got, mt := icoPNG(ico, "image/x-icon")
	if mt != "image/png" || !bytes.Equal(got, p) {
		t.Fatal("PNG inside the ICO not found")
	}
	if logoSize(ico, "image/x-icon") != 48 {
		t.Fatal("ICO size")
	}
	for _, c := range []struct {
		mime  string
		score int
		want  string
	}{{"image/svg+xml", 0, "vector"}, {"image/png", 256, "clear"}, {"image/png", 64, "fair"}, {"image/png", 32, "small"}} {
		if q := logoQuality(c.mime, c.score); q != c.want {
			t.Fatalf("%v: %s", c, q)
		}
	}
}
