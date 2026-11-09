package api

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func tinyPNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

// A message can carry images: Claude gets them as image blocks, the
// transcript names them, and the dashboard can load them back.
func TestAssistantTakesImages(t *testing.T) {
	e := newEnv(t)
	f := &fakeClaude{}
	e.api.chatModel = f.converse
	id := e.newChat(e.super)
	data := tinyPNG(t)
	var out chatResp
	// Labelled JPEG, really a PNG: the bytes decide. No text is fine.
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages",
		map[string]any{"text": "", "images": []map[string]any{{"media_type": "image/jpeg", "data": "data:image/jpeg;base64," + data}}}, 200, &out)
	if len(f.sent) == 0 || !strings.Contains(f.sent[0], `"type":"image"`) || !strings.Contains(f.sent[0], `"media_type":"image/png"`) || !strings.Contains(f.sent[0], data) {
		t.Fatalf("Claude did not get the image: %v", f.sent)
	}
	var ref *chatImage
	for _, it := range out.Items {
		if it.Kind == "user" && len(it.Images) == 1 {
			ref = &it.Images[0]
		}
	}
	if ref == nil {
		t.Fatalf("no image on the user item: %+v", out.Items)
	}
	resp := e.do(e.super, "GET", "/api/admin/chat/threads/"+itoa(int(id))+"/images/"+ref.ID, nil, 200, nil)
	if resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("served as %q", resp.Header.Get("Content-Type"))
	}
	// Someone else's chat image is not found for another user.
	other := client()
	e.do(e.super, "POST", "/api/admin/users", map[string]any{"email": "m@x.io", "password": "membersecret1", "role": "admin"}, 201, nil)
	e.do(other, "POST", "/api/auth/login", map[string]any{"email": "m@x.io", "password": "membersecret1"}, 200, nil)
	e.do(other, "GET", "/api/admin/chat/threads/"+itoa(int(id))+"/images/"+ref.ID, nil, 404, nil)

	// Not an image, too many, or too large: refused.
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages",
		map[string]any{"text": "x", "images": []map[string]any{{"media_type": "image/png", "data": base64.StdEncoding.EncodeToString([]byte("<svg/>"))}}}, 400, nil)
	five := []map[string]any{}
	for i := 0; i < 5; i++ {
		five = append(five, map[string]any{"media_type": "image/png", "data": data})
	}
	e.do(e.super, "POST", "/api/admin/chat/threads/"+itoa(int(id))+"/messages", map[string]any{"text": "x", "images": five}, 400, nil)
}

func TestOnlyRecentImagesGoToTheModel(t *testing.T) {
	msgs := []chatMsg{}
	for i := 0; i < 8; i++ {
		msgs = append(msgs, chatMsg{Role: "user", Blocks: []chatBlock{{Type: "image", ImageID: "img" + itoa(i), MediaType: "image/png", Data: "AAAA"}}})
	}
	keep := recentImages(msgs, chatModelImages)
	if len(keep) != chatModelImages || keep["img0"] || !keep["img7"] {
		t.Fatalf("keep: %v", keep)
	}
}
