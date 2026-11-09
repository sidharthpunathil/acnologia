package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Images in the Assistant: a user message can carry pictures (a screenshot
// of a provider's docs, an error, a pricing table). They go to Claude as
// image blocks and live in the thread, encrypted at rest like the rest of
// it. The dashboard scales them down before sending, so these caps are for
// a client that does not.

const (
	chatMaxImages = 4
	// chatMaxImageBytes caps one image, decoded. Four of them, base64
	// encoded, still fit the 4 MB request limit with a full message.
	chatMaxImageBytes = 700 << 10
	// chatModelImages is how many of the latest images every model call
	// carries; older ones are a short note, so a long chat stays cheap.
	chatModelImages = 6
)

var chatImageTypes = map[string]string{
	"image/png": "png", "image/jpeg": "jpeg", "image/gif": "gif", "image/webp": "webp",
}

type chatImageIn struct {
	MediaType string `json:"media_type"`
	// Data is base64, with or without a data: URL prefix.
	Data string `json:"data"`
}

// checkChatImages validates a message's images and returns them with the
// data: prefix stripped. msg is set when they cannot be sent.
func checkChatImages(in []chatImageIn) ([]chatImageIn, string) {
	if len(in) > chatMaxImages {
		return nil, fmt.Sprintf("send at most %d images in one message", chatMaxImages)
	}
	out := make([]chatImageIn, 0, len(in))
	for i, im := range in {
		data := strings.TrimSpace(im.Data)
		if j := strings.Index(data, ","); strings.HasPrefix(data, "data:") && j > 0 {
			data = data[j+1:]
		}
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, fmt.Sprintf("image %d is not base64", i+1)
		}
		if len(raw) > chatMaxImageBytes {
			return nil, fmt.Sprintf("image %d is larger than %d KB; scale it down", i+1, chatMaxImageBytes>>10)
		}
		sniffed := http.DetectContentType(raw)
		if _, ok := chatImageTypes[sniffed]; !ok {
			return nil, fmt.Sprintf("image %d must be a PNG, JPEG, GIF or WebP", i+1)
		}
		// The bytes decide the type: a mislabelled image is sent as what it is.
		out = append(out, chatImageIn{MediaType: sniffed, Data: data})
	}
	return out, ""
}

// recentImages names the latest n images in the history.
func recentImages(msgs []chatMsg, n int) map[string]bool {
	keep := map[string]bool{}
	for i := len(msgs) - 1; i >= 0 && len(keep) < n; i-- {
		bs := msgs[i].Blocks
		for j := len(bs) - 1; j >= 0 && len(keep) < n; j-- {
			if bs[j].Type == "image" {
				keep[bs[j].ImageID] = true
			}
		}
	}
	return keep
}

// chatImage serves one image of the user's own thread.
func (s *Server) chatImageGet(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	_, d, err := s.loadChat(r.Context(), id, userFrom(r).ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	want := chi.URLParam(r, "image")
	for _, m := range d.Messages {
		for _, b := range m.Blocks {
			if b.Type != "image" || b.ImageID != want {
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(b.Data)
			if err != nil {
				break
			}
			w.Header().Set("Content-Type", b.MediaType)
			w.Header().Set("Cache-Control", "private, max-age=86400")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Security-Policy", "default-src 'none'")
			_, _ = w.Write(raw)
			return
		}
	}
	writeErr(w, 404, "not_found", "no such image in this chat")
}
