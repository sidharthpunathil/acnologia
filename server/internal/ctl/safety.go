package ctl

// Commands and flags for driving a server safely: the trash, backups and
// the server's version, plus two global flags for every write. --dry-run
// adds dry_run=1 to each POST, PUT, PATCH and DELETE under /api/admin, so
// the server says what would happen and changes nothing. --idempotency-key
// sends an Idempotency-Key header with them, so running the same command
// again returns the first answer instead of writing twice. run calls
// safety first (one line in ctl.go).

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

// safetyUsage is the help for these commands, printed by help after the
// rest.
const safetyUsage = `
Safety (any command):
  --dry-run                      writes say what they would do and change nothing (dry_run=1)
  --idempotency-key <key>        a retry with the same key returns the first answer, within 24 hours
  trash                          deleted keys, services and apps (kept 7 days)
  trash restore <id>             bring one back as it was
  trash purge <id>               delete one for good now (superadmin)
  backup [-o file]               download a copy of the database (superadmin); keys stay encrypted
  version --server               the server's version, the newest release and how to upgrade
`

// safety strips the global flags from args and sets them up, then runs the
// commands this file owns. done reports that it ran one.
func (c *cli) safety(args *[]string) (done bool, err error) {
	in := *args
	// mcp install has a --dry-run of its own, for the files it writes.
	if len(in) > 0 && in[0] == "mcp" {
		return false, nil
	}
	rest := in[:0:0]
	dry, key := false, ""
	for i := 0; i < len(in); i++ {
		a := in[i]
		switch {
		case a == "--dry-run" || a == "-dry-run":
			dry = true
		case a == "--idempotency-key" || a == "-idempotency-key":
			if i+1 >= len(in) {
				return true, usageErrorf("--idempotency-key needs a value")
			}
			key = in[i+1]
			i++
		case strings.HasPrefix(a, "--idempotency-key="):
			key = strings.TrimPrefix(a, "--idempotency-key=")
		default:
			rest = append(rest, a)
		}
	}
	*args = rest
	if dry || key != "" {
		hc := &http.Client{}
		if c.http != nil {
			cp := *c.http
			hc = &cp
		}
		base := hc.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		t := &safetyTransport{base: base, dry: dry, key: key}
		hc.Transport = t
		c.http = hc
		if dry && len(rest) > 0 && rest[0] != "help" {
			return true, c.runDry(rest, t)
		}
	}
	if len(rest) == 0 {
		rest = []string{"help"}
	}
	switch rest[0] {
	case "trash":
		return true, c.trash(rest[1:])
	case "backup":
		return true, c.backup(rest[1:])
	case "version":
		return true, c.serverVersion(rest[1:])
	case "help", "-h", "--help":
		fmt.Fprintf(c.out, usage, c.name())
		fmt.Fprint(c.out, safetyUsage)
		return true, nil
	}
	return false, nil
}

// runDry runs a command with --dry-run. The command prints for a real
// answer, so its output is held back: when the server answered dry runs,
// their "would" lines are printed instead (the answers themselves with
// --json); when nothing was a write, the output goes out as it is.
func (c *cli) runDry(args []string, t *safetyTransport) error {
	out := c.out
	var buf bytes.Buffer
	c.out = &buf
	err := c.run(args)
	c.out = out
	if len(t.answers) == 0 {
		_, _ = c.out.Write(buf.Bytes())
		return err
	}
	for _, a := range t.answers {
		if c.json {
			if perr := c.printJSON(a); perr != nil {
				return perr
			}
			continue
		}
		var d struct {
			Would string `json:"would"`
		}
		_ = json.Unmarshal(a, &d)
		fmt.Fprintln(c.out, "Dry run: would", clean(d.Would))
	}
	return nil
}

// safetyTransport adds dry_run=1 and the Idempotency-Key to admin writes,
// and keeps the server's dry run answers.
type safetyTransport struct {
	base    http.RoundTripper
	dry     bool
	key     string
	answers []json.RawMessage
}

func (t *safetyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !isWrite(r.Method) || !strings.HasPrefix(r.URL.Path, "/api/admin/") {
		return t.base.RoundTrip(r)
	}
	r = r.Clone(r.Context())
	if t.dry {
		q := r.URL.Query()
		q.Set("dry_run", "1")
		r.URL.RawQuery = q.Encode()
	}
	if t.key != "" {
		r.Header.Set("Idempotency-Key", t.key)
	}
	resp, err := t.base.RoundTrip(r)
	if err != nil || !t.dry || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(b))
	var d struct {
		DryRun bool `json:"dry_run"`
	}
	if json.Unmarshal(b, &d) == nil && d.DryRun {
		t.answers = append(t.answers, b)
	}
	return resp, nil
}

func isWrite(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

type trashItem struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Service   string    `json:"service"`
	Keys      int       `json:"keys"`
	DeletedBy string    `json:"deleted_by"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (c *cli) trash(args []string) error {
	if len(args) == 0 {
		var out struct {
			Items []trashItem `json:"items"`
		}
		if done, err := c.callJSON("GET", "/api/admin/trash", &out); done || err != nil {
			return err
		}
		if len(out.Items) == 0 {
			fmt.Fprintln(c.out, "The trash is empty.")
			return nil
		}
		tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tKIND\tNAME\tDELETED BY\tGOES FOR GOOD")
		for _, it := range out.Items {
			name := it.Name
			switch {
			case it.Kind == "key" && it.Service != "":
				name = it.Service + "/" + it.Name
			case it.Kind == "service":
				name = fmt.Sprintf("%s (%d keys)", it.Name, it.Keys)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", clean(it.ID), clean(it.Kind), clean(name), clean(it.DeletedBy), it.ExpiresAt.Local().Format("2006-01-02 15:04"))
		}
		return tw.Flush()
	}
	if len(args) != 2 || (args[0] != "restore" && args[0] != "purge") {
		return usageErrorf("usage: %s trash [restore|purge <id>]", c.name())
	}
	id := url.PathEscape(args[1])
	method, path, verb := "POST", "/api/admin/trash/"+id+"/restore", "Restored"
	if args[0] == "purge" {
		method, path, verb = "DELETE", "/api/admin/trash/"+id, "Deleted for good:"
	}
	var raw json.RawMessage
	if err := c.call(method, path, nil, &raw); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(raw)
	}
	var out struct {
		DryRun bool      `json:"dry_run"`
		Would  string    `json:"would"`
		Rest   trashItem `json:"restored"`
		Purged trashItem `json:"purged"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.DryRun {
		fmt.Fprintln(c.out, "Dry run: would", clean(out.Would))
		return nil
	}
	it := out.Rest
	if args[0] == "purge" {
		it = out.Purged
	}
	fmt.Fprintf(c.out, "%s %s %s\n", verb, clean(it.Kind), clean(it.Name))
	return nil
}

func (c *cli) serverVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(c.err)
	_ = fs.Bool("server", true, "the server's version (the default)")
	if _, err := parseInterleaved(fs, args); err != nil {
		return err
	}
	var out struct {
		Current         string  `json:"current"`
		Latest          *string `json:"latest"`
		UpdateAvailable *bool   `json:"update_available"`
		UpgradeCommand  string  `json:"upgrade_command"`
		ReleaseURL      *string `json:"release_url"`
		SchemaVersion   *int    `json:"schema_version"`
	}
	if done, err := c.callJSON("GET", "/api/admin/version", &out); done || err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Server   %s\n", clean(out.Current))
	if out.SchemaVersion != nil {
		fmt.Fprintf(c.out, "Schema   %d\n", *out.SchemaVersion)
	}
	if out.Latest != nil {
		fmt.Fprintf(c.out, "Latest   %s\n", clean(*out.Latest))
	} else {
		fmt.Fprintln(c.out, "Latest   unknown (the release check got no answer)")
	}
	if out.UpdateAvailable != nil && *out.UpdateAvailable {
		fmt.Fprintf(c.out, "Upgrade  %s\n", clean(out.UpgradeCommand))
	}
	return nil
}

// backup downloads a copy of the database. It streams to the file, so a
// big database never sits in memory.
func (c *cli) backup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(c.err)
	outPath := fs.String("o", "", "file to write (default: the server's name for it, in this folder)")
	if pos, err := parseInterleaved(fs, args); err != nil {
		return err
	} else if len(pos) > 0 {
		return usageErrorf("usage: %s backup [-o file]", c.name())
	}
	ctx, err := c.current()
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, ctx.URL+"/api/admin/backup", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+ctx.Token)
	req.Header.Set("X-Requested-With", "acnologia-ctl")
	if pw := accessPasswordFor(ctx.URL); pw != "" {
		req.Header.Set(accessHeader, pw)
	}
	resp, err := c.httpClient(30 * time.Minute).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var raw struct {
			Error map[string]any `json:"error"`
		}
		_ = json.Unmarshal(b, &raw)
		code, _ := raw.Error["code"].(string)
		msg, _ := raw.Error["message"].(string)
		return &httpError{Status: resp.StatusCode, Code: code, Message: msg, Fields: raw.Error}
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		// A dry run answers in JSON instead of the file.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return c.printJSON(b)
	}
	dest := *outPath
	if dest == "" {
		dest = "acnologia-backup.db"
		if _, p, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil && p["filename"] != "" {
			dest = filepath.Base(p["filename"])
		}
	}
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"file": dest, "bytes": n})
		return c.printJSON(b)
	}
	fmt.Fprintf(c.out, "Saved %s (%d bytes). Keys in it are encrypted: keep ACNOLOGIA_MASTER_KEY with it, separately.\n", clean(dest), n)
	return nil
}
