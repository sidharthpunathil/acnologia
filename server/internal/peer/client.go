package peer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// RemoteError is an error answer from another server.
type RemoteError struct {
	Status  int
	Code    string
	Message string
}

func (e *RemoteError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("the other server answered %d %s", e.Status, e.Code)
}

// ShareState maps an owner refusal to the share status it means, or "".
func (e *RemoteError) ShareState() string {
	switch e.Code {
	case "share_revoked":
		return "revoked"
	case "share_paused":
		return "paused"
	case "share_expired":
		return "expired"
	}
	return ""
}

// AsRemote returns err as a *RemoteError when it is one.
func AsRemote(err error) *RemoteError {
	var re *RemoteError
	if errors.As(err, &re) {
		return re
	}
	return nil
}

// maxReply bounds what is read from another server.
const maxReply = 4 << 20

func call(ctx context.Context, c *http.Client, method, url, token string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the other server: %w", stripURL(err))
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		re := &RemoteError{Status: resp.StatusCode}
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil {
			re.Code, re.Message = e.Error.Code, e.Error.Message
		}
		if re.Code == "" {
			re.Code = "http_error"
		}
		return re
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return errors.New("the other server's answer is not what Acnologia expects")
	}
	return nil
}

// stripURL keeps transport errors short; they never carry a token, but the
// URL adds nothing a person needs.
func stripURL(err error) error {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		return errors.New(msg[i+2:])
	}
	return err
}

// FetchIdentity reads another server's /.well-known/acnologia and checks
// that its id matches its public key.
func FetchIdentity(ctx context.Context, c *http.Client, base string) (Identity, error) {
	var id Identity
	if err := call(ctx, c, http.MethodGet, strings.TrimRight(base, "/")+"/.well-known/acnologia", "", nil, &id); err != nil {
		return id, err
	}
	if err := CheckIdentity(id); err != nil {
		return id, err
	}
	return id, nil
}

// Preview asks the owner what an invite shares, without redeeming it.
func Preview(ctx context.Context, c *http.Client, inv Invite) (PreviewResponse, error) {
	var out PreviewResponse
	err := call(ctx, c, http.MethodPost, inv.URL+"/api/peer/v1/preview", "", PreviewRequest{Invite: inv.Secret, ShareID: inv.ShareID}, &out)
	return out, err
}

// Redeem redeems an invite and returns the share token.
func Redeem(ctx context.Context, c *http.Client, inv Invite, req RedeemRequest) (RedeemResponse, error) {
	var out RedeemResponse
	err := call(ctx, c, http.MethodPost, inv.URL+"/api/peer/v1/redeem", "", req, &out)
	return out, err
}

// FetchManifest reads a share's manifest with its token.
func FetchManifest(ctx context.Context, c *http.Client, base, token string) (Manifest, error) {
	var out Manifest
	err := call(ctx, c, http.MethodGet, strings.TrimRight(base, "/")+"/api/peer/v1/manifest", token, nil, &out)
	return out, err
}

// Leave tells the owner this server disconnected.
func Leave(ctx context.Context, c *http.Client, base, token string) error {
	return call(ctx, c, http.MethodPost, strings.TrimRight(base, "/")+"/api/peer/v1/leave", token, map[string]any{}, nil)
}
