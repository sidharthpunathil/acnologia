package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Per endpoint evidence: proxied calls folded into counts per service,
// method and path template and UTC day, so the catalog can rank tools and
// routing can prefer the provider that answers. Only answers from the
// provider count (events of kind proxy): Acnologia's own refusals (limits,
// read-only, no key, an open breaker) write other kinds or nothing, and
// cache hits and idempotent replays never reached the provider. A 4xx is
// the caller's fault and does not count against the provider, except 405
// and 429.
//
// The fold is incremental: a cursor on events.id in meta, a batch at a
// time, on the worker's schedule. Latency is kept as a small histogram per
// row, so p50 and p95 over the window are read without the raw events.

// EvidenceWindowDays is the rolling window evidence covers.
const EvidenceWindowDays = 7

// MinEvidenceCalls is the sample an endpoint needs before its evidence is
// shown or used: calls that count for or against the provider.
const MinEvidenceCalls = 20

const endpointStatsCursor = "endpoint_stats_cursor"

const schemaEndpointStats = `
CREATE TABLE IF NOT EXISTS endpoint_stats(
  service_id INTEGER NOT NULL,
  method TEXT NOT NULL,
  path TEXT NOT NULL,
  day INTEGER NOT NULL,
  calls INTEGER NOT NULL DEFAULT 0,
  ok INTEGER NOT NULL DEFAULT 0,
  fail INTEGER NOT NULL DEFAULT 0,
  last_ok_at INTEGER,
  last_fail_at INTEGER,
  hist TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(service_id, method, path, day)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS example_responses(
  provider TEXT NOT NULL,
  example TEXT NOT NULL,
  method TEXT NOT NULL DEFAULT 'GET',
  path TEXT NOT NULL DEFAULT '',
  status INTEGER NOT NULL DEFAULT 0,
  content_type TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  verified_at INTEGER NOT NULL,
  PRIMARY KEY(provider, example)
) WITHOUT ROWID;
`

func migrateEndpointStats(db *sql.DB) error {
	_, err := db.Exec(schemaEndpointStats)
	return err
}

// latencyBounds are the histogram's bucket upper bounds in ms; one more
// bucket holds everything slower.
var latencyBounds = []int64{25, 50, 75, 100, 150, 200, 300, 400, 600, 800, 1000, 1500, 2000, 3000, 4000, 6000, 8000, 12000, 16000, 24000, 32000, 60000, 120000}

func latencyBucket(ms int64) int {
	return sort.Search(len(latencyBounds), func(i int) bool { return latencyBounds[i] >= ms })
}

type hist []int64

func parseHist(s string) hist {
	h := make(hist, len(latencyBounds)+1)
	if s == "" {
		return h
	}
	for i, f := range strings.Split(s, ",") {
		if i >= len(h) {
			break
		}
		h[i], _ = strconv.ParseInt(f, 10, 64)
	}
	return h
}

func (h hist) String() string {
	parts := make([]string, len(h))
	for i, v := range h {
		parts[i] = strconv.FormatInt(v, 10)
	}
	return strings.Join(parts, ",")
}

func (h hist) add(o hist) {
	for i := range h {
		if i < len(o) {
			h[i] += o[i]
		}
	}
}

// percentile reads a percentile (0 to 1) off the histogram, interpolating
// inside the bucket it falls in. 0 when empty.
func (h hist) percentile(p float64) int64 {
	var total int64
	for _, v := range h {
		total += v
	}
	if total == 0 {
		return 0
	}
	want := p * float64(total)
	var seen int64
	for i, v := range h {
		if v == 0 {
			continue
		}
		if float64(seen+v) >= want {
			lo := int64(0)
			if i > 0 {
				lo = latencyBounds[i-1]
			}
			if i >= len(latencyBounds) {
				return latencyBounds[len(latencyBounds)-1]
			}
			hi := latencyBounds[i]
			frac := (want - float64(seen)) / float64(v)
			return lo + int64(frac*float64(hi-lo))
		}
		seen += v
	}
	return latencyBounds[len(latencyBounds)-1]
}

// ProxyVerdict says how a proxied answer counts: for the provider (ok),
// against it (fail), or not at all (neither, the caller's fault).
func ProxyVerdict(status int) (ok, fail bool) {
	switch {
	case status <= 0:
		return false, false
	case status < 400:
		return true, false
	case status == 405 || status == 429 || status >= 500:
		return false, true
	}
	return false, false
}

// ParseProxyDetail splits a proxy event's detail ("GET /v1/x", "MCP tool",
// either with " (queued ...)" after it) into its method and path.
func ParseProxyDetail(detail string) (method, path string, ok bool) {
	method, rest, found := strings.Cut(detail, " ")
	if !found || method == "" {
		return "", "", false
	}
	path, _, _ = strings.Cut(rest, " ")
	if path == "" {
		return "", "", false
	}
	return strings.ToUpper(method), path, true
}

type statKey struct {
	service      int64
	method, path string
	day          int64
}

type statAgg struct {
	calls, ok, fail  int64
	lastOK, lastFail int64
	hist             hist
}

// EndpointTemplater maps a proxied call to its template, or "" to skip it
// (the service is gone).
type EndpointTemplater func(serviceID int64, method, path string) string

// FoldEndpointStats folds proxy events past the cursor into endpoint_stats,
// at most batches batches of size events, and returns how many events it
// read. It is meant to run on one goroutine (the worker).
func (s *Store) FoldEndpointStats(ctx context.Context, tmpl EndpointTemplater, size, batches int) (int, error) {
	if size <= 0 {
		size = 2000
	}
	cursor, err := s.endpointCursor(ctx)
	if err != nil {
		return 0, err
	}
	read := 0
	for b := 0; b < batches || batches <= 0; b++ {
		var maxID int64
		if err := s.rd.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM events`).Scan(&maxID); err != nil {
			return read, err
		}
		if maxID <= cursor {
			break
		}
		rows, err := s.rd.QueryContext(ctx, `SELECT id, ts, COALESCE(service_id,0), status_code, latency_ms, detail FROM events
			WHERE id>? AND id<=? AND kind='proxy' ORDER BY id LIMIT ?`, cursor, maxID, size)
		if err != nil {
			return read, err
		}
		aggs := map[statKey]*statAgg{}
		n := 0
		last := cursor
		for rows.Next() {
			var id, ts, sid, lat int64
			var status int
			var detail string
			if err := rows.Scan(&id, &ts, &sid, &status, &lat, &detail); err != nil {
				rows.Close()
				return read, err
			}
			n++
			last = id
			method, path, ok := ParseProxyDetail(detail)
			if !ok || sid == 0 {
				continue
			}
			t := tmpl(sid, method, path)
			if t == "" {
				continue
			}
			k := statKey{sid, method, t, ts / 86400}
			a := aggs[k]
			if a == nil {
				a = &statAgg{hist: make(hist, len(latencyBounds)+1)}
				aggs[k] = a
			}
			a.calls++
			good, bad := ProxyVerdict(status)
			switch {
			case good:
				a.ok++
				a.lastOK = max(a.lastOK, ts)
				if lat > 0 {
					a.hist[latencyBucket(lat)]++
				}
			case bad:
				a.fail++
				a.lastFail = max(a.lastFail, ts)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return read, err
		}
		next := last
		if n < size {
			next = maxID
		}
		if err := s.writeEndpointStats(ctx, aggs, next); err != nil {
			return read, err
		}
		cursor = next
		read += n
		if n < size {
			break
		}
	}
	if read > 0 {
		invalidateEvidence(s)
	}
	return read, nil
}

// endpointCursor is the last event id folded. The first fold starts at the
// window, not at the first event ever written.
func (s *Store) endpointCursor(ctx context.Context) (int64, error) {
	v, found, err := s.LookupMeta(ctx, endpointStatsCursor)
	if err != nil {
		return 0, err
	}
	if found {
		n, _ := strconv.ParseInt(v, 10, 64)
		return n, nil
	}
	var first sql.NullInt64
	since := time.Now().Add(-EvidenceWindowDays * 24 * time.Hour).Unix()
	if err := s.rd.QueryRowContext(ctx, `SELECT MIN(id) FROM events WHERE ts>=?`, since).Scan(&first); err != nil {
		return 0, err
	}
	if first.Valid {
		return first.Int64 - 1, nil
	}
	var maxID int64
	err = s.rd.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM events`).Scan(&maxID)
	return maxID, err
}

func (s *Store) writeEndpointStats(ctx context.Context, aggs map[statKey]*statAgg, cursor int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	nz := func(v int64) any {
		if v == 0 {
			return nil
		}
		return v
	}
	for k, a := range aggs {
		var cur string
		err := tx.QueryRowContext(ctx, `SELECT hist FROM endpoint_stats WHERE service_id=? AND method=? AND path=? AND day=?`,
			k.service, k.method, k.path, k.day).Scan(&cur)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		h := parseHist(cur)
		h.add(a.hist)
		if _, err := tx.ExecContext(ctx, `INSERT INTO endpoint_stats(service_id,method,path,day,calls,ok,fail,last_ok_at,last_fail_at,hist)
			VALUES(?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(service_id,method,path,day) DO UPDATE SET calls=calls+excluded.calls, ok=ok+excluded.ok, fail=fail+excluded.fail,
			last_ok_at=MAX(COALESCE(last_ok_at,0),COALESCE(excluded.last_ok_at,0)),
			last_fail_at=MAX(COALESCE(last_fail_at,0),COALESCE(excluded.last_fail_at,0)), hist=excluded.hist`,
			k.service, k.method, k.path, k.day, a.calls, a.ok, a.fail, nz(a.lastOK), nz(a.lastFail), h.String()); err != nil {
			return err
		}
	}
	// Rows past the window, and rows of deleted services, go.
	if _, err := tx.ExecContext(ctx, `DELETE FROM endpoint_stats WHERE day<? OR service_id NOT IN (SELECT id FROM services)`,
		time.Now().Unix()/86400-EvidenceWindowDays-1); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`,
		endpointStatsCursor, strconv.FormatInt(cursor, 10)); err != nil {
		return err
	}
	return tx.Commit()
}

// CountServiceTemplates counts the distinct templates a service has rows
// for, so a fold can cap the paths no catalog entry covers.
func (s *Store) CountServiceTemplates(ctx context.Context) (map[int64]int, error) {
	rows, err := s.rd.QueryContext(ctx, `SELECT service_id, COUNT(DISTINCT method||' '||path) FROM endpoint_stats GROUP BY service_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// EndpointStat is one endpoint's evidence over the window.
type EndpointStat struct {
	ServiceID int64  `json:"service_id"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	// Calls is every answer the provider gave; OK and Fail are the ones
	// that count for and against it (a caller's 4xx counts for neither).
	Calls    int64      `json:"calls"`
	OK       int64      `json:"ok"`
	Fail     int64      `json:"fail"`
	P50MS    int64      `json:"p50_ms"`
	P95MS    int64      `json:"p95_ms"`
	LastOK   *time.Time `json:"last_ok,omitempty"`
	LastFail *time.Time `json:"last_fail,omitempty"`
	hist     hist
}

// Counted is the calls that count for or against the provider.
func (e *EndpointStat) Counted() int64 { return e.OK + e.Fail }

// Enough reports whether the sample is big enough to show or rank by.
func (e *EndpointStat) Enough() bool { return e != nil && e.Counted() >= MinEvidenceCalls }

// SuccessRate is OK over the counted calls, 0 to 1.
func (e *EndpointStat) SuccessRate() float64 {
	if e == nil || e.Counted() == 0 {
		return 0
	}
	return float64(e.OK) / float64(e.Counted())
}

func (e *EndpointStat) merge(o *EndpointStat) {
	e.Calls += o.Calls
	e.OK += o.OK
	e.Fail += o.Fail
	if e.hist == nil {
		e.hist = make(hist, len(latencyBounds)+1)
	}
	e.hist.add(o.hist)
	if o.LastOK != nil && (e.LastOK == nil || o.LastOK.After(*e.LastOK)) {
		t := *o.LastOK
		e.LastOK = &t
	}
	if o.LastFail != nil && (e.LastFail == nil || o.LastFail.After(*e.LastFail)) {
		t := *o.LastFail
		e.LastFail = &t
	}
}

func (e *EndpointStat) finish() {
	e.P50MS = e.hist.percentile(0.5)
	e.P95MS = e.hist.percentile(0.95)
}

// Evidence is the window's stats, per service and endpoint, read at once.
type Evidence struct {
	At       time.Time
	byKey    map[int64]map[string]*EndpointStat
	byServ   map[int64]*EndpointStat
	services map[int64][]*EndpointStat
}

func evKey(method, path string) string { return strings.ToUpper(method) + " " + path }

// Endpoint is one endpoint's stats, or nil.
func (e *Evidence) Endpoint(serviceID int64, method, template string) *EndpointStat {
	if e == nil {
		return nil
	}
	return e.byKey[serviceID][evKey(method, template)]
}

// Service is every endpoint of a service summed, or nil.
func (e *Evidence) Service(serviceID int64) *EndpointStat {
	if e == nil {
		return nil
	}
	return e.byServ[serviceID]
}

// Endpoints lists a service's endpoints, the most called first.
func (e *Evidence) Endpoints(serviceID int64) []*EndpointStat {
	if e == nil {
		return nil
	}
	return e.services[serviceID]
}

// evidenceTTL is how long a read of the stats is reused. The fold runs
// every minute, so fresher reads would show nothing new.
var evidenceTTL = 30 * time.Second

type evidenceCache struct {
	mu sync.Mutex
	ev *Evidence
}

var evidenceCaches sync.Map // *Store -> *evidenceCache

func invalidateEvidence(s *Store) {
	if v, ok := evidenceCaches.Load(s); ok {
		c := v.(*evidenceCache)
		c.mu.Lock()
		c.ev = nil
		c.mu.Unlock()
	}
}

// Evidence returns the window's stats, read at most every evidenceTTL. It
// never fails: a failed read gives the last one, or none.
func (s *Store) Evidence(ctx context.Context) *Evidence {
	v, _ := evidenceCaches.LoadOrStore(s, &evidenceCache{})
	c := v.(*evidenceCache)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ev != nil && time.Since(c.ev.At) < evidenceTTL {
		return c.ev
	}
	ev, err := s.loadEvidence(ctx)
	if err != nil {
		if c.ev != nil {
			return c.ev
		}
		return &Evidence{At: time.Now()}
	}
	c.ev = ev
	return ev
}

func (s *Store) loadEvidence(ctx context.Context) (*Evidence, error) {
	since := time.Now().Unix()/86400 - (EvidenceWindowDays - 1)
	rows, err := s.rd.QueryContext(ctx, `SELECT service_id, method, path, calls, ok, fail, COALESCE(last_ok_at,0), COALESCE(last_fail_at,0), hist
		FROM endpoint_stats WHERE day>=?`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ev := &Evidence{At: time.Now(), byKey: map[int64]map[string]*EndpointStat{}, byServ: map[int64]*EndpointStat{}, services: map[int64][]*EndpointStat{}}
	for rows.Next() {
		var r EndpointStat
		var lok, lfail int64
		var h string
		if err := rows.Scan(&r.ServiceID, &r.Method, &r.Path, &r.Calls, &r.OK, &r.Fail, &lok, &lfail, &h); err != nil {
			return nil, err
		}
		r.hist = parseHist(h)
		if lok > 0 {
			t := time.Unix(lok, 0).UTC()
			r.LastOK = &t
		}
		if lfail > 0 {
			t := time.Unix(lfail, 0).UTC()
			r.LastFail = &t
		}
		m := ev.byKey[r.ServiceID]
		if m == nil {
			m = map[string]*EndpointStat{}
			ev.byKey[r.ServiceID] = m
		}
		k := evKey(r.Method, r.Path)
		if cur := m[k]; cur != nil {
			cur.merge(&r)
		} else {
			e := &EndpointStat{ServiceID: r.ServiceID, Method: r.Method, Path: r.Path}
			e.merge(&r)
			m[k] = e
			ev.services[r.ServiceID] = append(ev.services[r.ServiceID], e)
		}
		sv := ev.byServ[r.ServiceID]
		if sv == nil {
			sv = &EndpointStat{ServiceID: r.ServiceID, Method: "*", Path: "*"}
			ev.byServ[r.ServiceID] = sv
		}
		sv.merge(&r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for id, list := range ev.services {
		for _, e := range list {
			e.finish()
		}
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Calls != list[j].Calls {
				return list[i].Calls > list[j].Calls
			}
			return evKey(list[i].Method, list[i].Path) < evKey(list[j].Method, list[j].Path)
		})
		ev.byServ[id].finish()
	}
	return ev, nil
}

// ---- example responses ----

// ExampleResponse is the answer a safe example (or a parameterless GET)
// gave when a live check or a catalog verify last ran it: its shape, cut
// to SampleMax bytes and scrubbed of keys. Kept per provider and example
// name, outside the spec, so it survives spec updates.
type ExampleResponse struct {
	Provider    string    `json:"provider"`
	Example     string    `json:"example"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	Status      int       `json:"status"`
	ContentType string    `json:"content_type,omitempty"`
	Body        string    `json:"body"`
	VerifiedAt  time.Time `json:"verified_at"`
}

// SampleMax caps a stored response sample.
const SampleMax = 4 << 10

// maxSampleString cuts long string values in a JSON sample: the shape and
// the format of a value teach more than its full text.
const maxSampleString = 120

// SaveExampleResponse stores a response sample. secrets are the keys the
// call carried: every form of them (raw, query, path and JSON escaped) is
// replaced, then the store's redactor runs over the text, before and after
// the sample is cut, so no part of a key can survive a cut.
func (s *Store) SaveExampleResponse(ctx context.Context, r ExampleResponse, body []byte, secrets ...string) error {
	if r.Provider == "" || r.Example == "" {
		return nil
	}
	text := scrubForms(string(body), secrets)
	text = s.Scrub(ctx, text)
	text = SampleShape(r.ContentType, []byte(text))
	text = s.Scrub(ctx, scrubForms(text, secrets))
	text = TruncateUTF8(text, SampleMax)
	if r.Method == "" {
		r.Method = "GET"
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO example_responses(provider,example,method,path,status,content_type,body,verified_at) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(provider,example) DO UPDATE SET method=excluded.method, path=excluded.path, status=excluded.status,
		content_type=excluded.content_type, body=excluded.body, verified_at=excluded.verified_at`,
		r.Provider, truncate(r.Example, 200), strings.ToUpper(r.Method), truncate(r.Path, 500), r.Status,
		truncate(s.Scrub(ctx, r.ContentType), 100), text, time.Now().Unix())
	return err
}

// ExampleResponses lists a provider's stored samples by example name.
func (s *Store) ExampleResponses(ctx context.Context, provider string) ([]ExampleResponse, error) {
	rows, err := s.rd.QueryContext(ctx, `SELECT provider, example, method, path, status, content_type, body, verified_at
		FROM example_responses WHERE provider=? ORDER BY example`, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExampleResponse{}
	for rows.Next() {
		var r ExampleResponse
		var at int64
		if err := rows.Scan(&r.Provider, &r.Example, &r.Method, &r.Path, &r.Status, &r.ContentType, &r.Body, &at); err != nil {
			return nil, err
		}
		r.VerifiedAt = time.Unix(at, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// scrubForms replaces each secret in its raw, query, path and JSON escaped
// spellings.
func scrubForms(text string, secrets []string) string {
	for _, sec := range secrets {
		if len(sec) < 4 {
			continue
		}
		forms := []string{sec, url.QueryEscape(sec), url.PathEscape(sec)}
		if b, err := json.Marshal(sec); err == nil && len(b) >= 2 {
			js := string(b[1 : len(b)-1])
			forms = append(forms, js, strings.ReplaceAll(js, "/", `\/`))
		}
		sort.Slice(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })
		for _, f := range forms {
			if f != "" {
				text = strings.ReplaceAll(text, f, "[key]")
			}
		}
	}
	return text
}

// SampleShape turns a response body into a sample worth keeping: JSON keeps
// its structure with arrays cut to their first two items and long strings
// cut, indented; anything else is its text. The result may still exceed
// SampleMax; the caller cuts it.
func SampleShape(contentType string, body []byte) string {
	trim := bytes.TrimSpace(body)
	if len(trim) > 0 && (trim[0] == '{' || trim[0] == '[') {
		var v any
		dec := json.NewDecoder(bytes.NewReader(trim))
		dec.UseNumber()
		if dec.Decode(&v) == nil {
			var b bytes.Buffer
			enc := json.NewEncoder(&b)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			if enc.Encode(shapeValue(v, 0)) == nil {
				return strings.TrimRight(b.String(), "\n")
			}
		}
	}
	return strings.TrimSpace(string(body))
}

func shapeValue(v any, depth int) any {
	if depth > 12 {
		return "..."
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = shapeValue(val, depth+1)
		}
		return out
	case []any:
		n := min(len(x), 2)
		out := make([]any, 0, n+1)
		for _, val := range x[:n] {
			out = append(out, shapeValue(val, depth+1))
		}
		if len(x) > n {
			out = append(out, "... "+strconv.Itoa(len(x)-n)+" more")
		}
		return out
	case string:
		if len(x) > maxSampleString {
			return TruncateUTF8(x, maxSampleString) + "..."
		}
		return x
	}
	return v
}
