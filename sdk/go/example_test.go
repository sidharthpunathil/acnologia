package acnologia_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	acnologia "github.com/sidharthpunathil/acnologia/sdk/go"
)

// Proxy mode: Acnologia injects the SerpAPI key and rotates on 429.
func ExampleProxy_Get() {
	c := acnologia.New("http://localhost:8080", os.Getenv("ACNOLOGIA_TOKEN"))
	resp, err := c.Proxy("serpapi").Get(context.Background(), "/search.json", url.Values{
		"engine": {"google"},
		"q":      {"coffee"},
	})
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	fmt.Println(resp.StatusCode, resp.Header.Get("X-Acnologia-Key-Id"))
}

// Proxy mode: ElevenLabs text to speech.
func ExampleProxy_PostJSON() {
	c := acnologia.New("http://localhost:8080", os.Getenv("ACNOLOGIA_TOKEN"))
	resp, err := c.Proxy("elevenlabs").PostJSON(context.Background(),
		"/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM",
		map[string]string{"text": "Hello from Acnologia", "model_id": "eleven_multilingual_v2"})
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	f, _ := os.Create("hello.mp3")
	defer f.Close()
	_, _ = io.Copy(f, resp.Body)
}

// Lease mode: call SerpAPI yourself and let WithKey rotate on rate limits.
func ExampleClient_WithKey() {
	c := acnologia.New("http://localhost:8080", os.Getenv("ACNOLOGIA_TOKEN"))
	ctx := context.Background()
	var results map[string]any
	err := c.WithKey(ctx, "serpapi", func(l *acnologia.Lease) error {
		req, _ := http.NewRequestWithContext(ctx, "GET",
			l.URL("/search.json", url.Values{"engine": {"google"}, "q": {"coffee"}}), nil)
		l.Apply(req) // adds ?api_key=...
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		switch {
		case resp.StatusCode == 429 && strings.Contains(string(body), "run out of searches"):
			return &acnologia.ErrQuotaExhausted{StatusCode: 429}
		case resp.StatusCode == 429:
			return &acnologia.ErrRateLimited{StatusCode: 429, RetryAfter: time.Hour}
		case resp.StatusCode == 401:
			return &acnologia.ErrInvalidKey{StatusCode: 401}
		case resp.StatusCode >= 400:
			return fmt.Errorf("serpapi: http %d", resp.StatusCode)
		}
		return json.Unmarshal(body, &results)
	})
	var nk *acnologia.ErrNoKeyAvailable
	if errors.As(err, &nk) {
		fmt.Println("pool empty, retry in", nk.RetryAfter)
	}
}

// Lease mode by hand: lease, call, report.
func ExampleClient_Lease() {
	c := acnologia.New("http://localhost:8080", os.Getenv("ACNOLOGIA_TOKEN"))
	ctx := context.Background()
	lease, err := c.Lease(ctx, "elevenlabs", &acnologia.LeaseOptions{TTL: 5 * time.Minute})
	if err != nil {
		panic(err)
	}
	payload := strings.NewReader(`{"text":"Hello","model_id":"eleven_multilingual_v2"}`)
	req, _ := http.NewRequestWithContext(ctx, "POST", lease.URL("/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM", nil), payload)
	req.Header.Set("Content-Type", "application/json")
	lease.Apply(req) // sets xi-api-key
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_, _ = lease.Report(ctx, acnologia.Report{Outcome: acnologia.OutcomeError, Message: err.Error()})
		return
	}
	defer resp.Body.Close()
	outcome := acnologia.OutcomeOK
	if resp.StatusCode == 429 {
		outcome = acnologia.OutcomeRateLimited
	}
	retry, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
	next, _ := lease.Report(ctx, acnologia.Report{
		Outcome: outcome, StatusCode: resp.StatusCode,
		RetryAfter: time.Duration(retry) * time.Second, Rotate: outcome != acnologia.OutcomeOK,
	})
	_ = next // use next for a retry when non-nil
}
