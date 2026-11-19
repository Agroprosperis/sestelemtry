package outages

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The e-svitlo cabinet, as the company's account sees it:
//
//	GET  /                                   → the session cookie
//	POST /registr_all_user/login_all_user    email=…&password=… → the cabinet's first page
//	GET  …/show_only_disconnections?eic=…    → {"planned_list_cab": [{"accidentid": 0,
//	     "acc_begin": "2026-11-19T06:00:00", "accend_plan": "2026-11-19T10:00:00", …}]}
//
// Times carry no zone: they are Kyiv wall-clock. accidentid 0 marks the
// hourly schedule. The first page links the outages list; a household
// cabinet keeps it under /account_household.

// userAgent names the console honestly to the operator's server.
const userAgent = "SESTelemetry/1.0 (+https://github.com/Agroprosperis/sestelemtry)"

// defaultOutagesPath is where a household cabinet lists a point's
// outages, used when the first page links none.
const defaultOutagesPath = "/account_household/show_only_disconnections"

// maxPage bounds what one cabinet page may weigh.
const maxPage = 4 << 20

// Errors the settings page puts into plain words.
var (
	ErrUnauthorized = errors.New("кабінет не прийняв логін або пароль")
	ErrBlocked      = errors.New("кабінет закрив доступ перевіркою на бота")
	ErrUnreachable  = errors.New("немає зʼєднання з кабінетом")
	ErrNoData       = errors.New("кабінет відповів не даними про відключення")
)

// errSignedOut is a page the cabinet answers once it has dropped the
// session: sign in again.
var errSignedOut = errors.New("outages: the cabinet dropped the session")

// BaseURL is the address of the cabinet at host.
func BaseURL(host string) string { return "https://" + host }

// Client signs in to one e-svitlo cabinet and reads planned outages by
// EIC. It keeps the session between calls and signs in again when the
// cabinet has dropped it. Not safe for concurrent use.
type Client struct {
	base     string
	login    string
	password string
	loc      *time.Location
	http     *http.Client

	signedIn bool
	// landing is the path the sign-in ended on; path is the outages list
	// the cabinet links there, "" when it links none.
	landing string
	path    string

	trace func(Exchange)
}

// Exchange is one answer of the cabinet, as a trace sees it.
type Exchange struct {
	Method string
	URL    *url.URL
	Status int
	// Final is where the answer came from after redirects.
	Final  *url.URL
	Header http.Header
	Body   []byte
}

// SetTrace hands every answer of the cabinet to fn, for a tool that
// records them. Request bodies are never handed over: the sign-in's
// carries the password.
func (c *Client) SetTrace(fn func(Exchange)) { c.trace = fn }

// Get fetches one page of the cabinet in the session, for a tool that
// looks around it.
func (c *Client) Get(ctx context.Context, path string) (string, *url.URL, error) {
	return c.fetch(ctx, http.MethodGet, path, nil)
}

// NewClient builds a client for the cabinet at base (BaseURL in
// production). Times are read in loc. A nil httpClient gets one with a
// 20-second timeout. The session lives in httpClient's cookie jar when it
// has one — so give each client its own — and in the client's own else.
func NewClient(base, login, password string, loc *time.Location, httpClient *http.Client) *Client {
	c := &http.Client{Timeout: 20 * time.Second}
	if httpClient != nil {
		cp := *httpClient
		c = &cp
	}
	if c.Jar == nil {
		jar, _ := cookiejar.New(nil)
		c.Jar = jar
	}
	if loc == nil {
		loc = time.UTC
	}
	return &Client{base: strings.TrimRight(base, "/"), login: login, password: password, loc: loc, http: c}
}

// SignIn opens a session with the account.
func (c *Client) SignIn(ctx context.Context) error {
	c.signedIn, c.landing, c.path = false, "", ""
	// The cabinet hands out its session cookie with the first page.
	if _, _, err := c.fetch(ctx, http.MethodGet, "/", nil); err != nil {
		return err
	}
	form := url.Values{"email": {c.login}, "password": {c.password}}
	page, final, err := c.fetch(ctx, http.MethodPost, "/registr_all_user/login_all_user", form)
	if err != nil {
		return err
	}
	if !signedInPage(page) {
		if msg := flash(page); msg != "" {
			return refusal{msg: msg}
		}
		return ErrUnauthorized
	}
	c.signedIn, c.landing, c.path = true, final.Path, outagesLink(page)
	return nil
}

// refusal is a sign-in the cabinet refused, in its own words.
type refusal struct{ msg string }

func (r refusal) Error() string { return ErrUnauthorized.Error() + ": " + r.msg }
func (r refusal) Unwrap() error { return ErrUnauthorized }

// Refusal is what the cabinet said when it refused the sign-in, "" when
// err is no refusal or the cabinet gave no reason.
func Refusal(err error) string {
	var r refusal
	if errors.As(err, &r) {
		return r.msg
	}
	return ""
}

// Outages returns the point's planned outages the cabinet lists, in time
// order, overlapping ones of a kind merged.
func (c *Client) Outages(ctx context.Context, eic string) ([]Interval, error) {
	if !c.signedIn {
		if err := c.SignIn(ctx); err != nil {
			return nil, err
		}
	}
	list, err := c.readOutages(ctx, eic)
	if errors.Is(err, errSignedOut) {
		if err := c.SignIn(ctx); err != nil {
			return nil, err
		}
		list, err = c.readOutages(ctx, eic)
	}
	if errors.Is(err, errSignedOut) {
		return nil, ErrUnauthorized
	}
	return list, err
}

func (c *Client) readOutages(ctx context.Context, eic string) ([]Interval, error) {
	page, final, err := c.fetch(ctx, http.MethodGet, c.outagesURL(eic), nil)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(final.Path, "/user/login") || loginPage(page) {
		return nil, errSignedOut
	}
	return parseOutages([]byte(page), c.loc)
}

// outagesURL is the outages list of one point: the link the cabinet gave
// with its eic set, else the household path.
func (c *Client) outagesURL(eic string) string {
	u, err := url.Parse(c.path)
	if c.path == "" || err != nil {
		u = &url.URL{Path: defaultOutagesPath}
	}
	q := u.Query()
	q.Set("eic", eic)
	u.RawQuery = q.Encode()
	return u.String()
}

// Probe is what one test of a cabinet found, for the settings page.
type Probe struct {
	SignedIn bool       `json:"signed_in"`
	Landing  string     `json:"landing,omitempty"`
	Path     string     `json:"path,omitempty"`
	Outages  []Interval `json:"outages"`
}

// Probe signs in and, given an EIC, reads that point's outages.
func (c *Client) Probe(ctx context.Context, eic string) (Probe, error) {
	p := Probe{Outages: []Interval{}}
	if err := c.SignIn(ctx); err != nil {
		return p, err
	}
	p.SignedIn, p.Landing = true, c.landing
	if eic == "" {
		return p, nil
	}
	p.Path = c.outagesURL(eic)
	list, err := c.Outages(ctx, eic)
	if list != nil {
		p.Outages = list
	}
	return p, err
}

// fetch sends one request and returns the page and where it ended up
// after redirects.
func (c *Client) fetch(ctx context.Context, method, path string, form url.Values) (string, *url.URL, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.8")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", c.base)
		req.Header.Set("Referer", c.base+"/")
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
		return "", nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxPage))
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if c.trace != nil {
		c.trace(Exchange{Method: method, URL: req.URL, Status: res.StatusCode, Final: res.Request.URL, Header: res.Header.Clone(), Body: raw})
	}
	page := string(raw)
	if challenged(res, page) {
		return "", nil, ErrBlocked
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return "", nil, fmt.Errorf("кабінет відповів %s", res.Status)
	}
	return page, res.Request.URL, nil
}

// challenged reports Cloudflare's bot check in place of the page.
func challenged(res *http.Response, page string) bool {
	if res.Header.Get("Cf-Mitigated") == "challenge" {
		return true
	}
	if res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusServiceUnavailable {
		return false
	}
	head := page[:min(len(page), 20000)]
	return strings.Contains(head, "Just a moment") || strings.Contains(head, "cf-chl") || strings.Contains(head, "challenge-platform")
}

// signedInPage reports a cabinet page only a signed-in user gets: the
// public pages carry no way out.
func signedInPage(page string) bool {
	return strings.Contains(page, "Вихід") || strings.Contains(page, "Вийти") || strings.Contains(strings.ToLower(page), "logout")
}

var flashRe = regexp.MustCompile(`(?s)<div[^>]*class="[^"]*w2p_flash[^"]*"[^>]*>(.*?)</div>`)

// flash is the message the cabinet shows above its page — why it refused
// a sign-in — in plain words, "" when there is none.
func flash(page string) string {
	m := flashRe.FindStringSubmatch(page)
	if m == nil {
		return ""
	}
	return strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(m[1], " "))), " ")
}

// loginPage reports the sign-in form in place of the page asked for.
func loginPage(page string) bool {
	return !signedInPage(page) && strings.Contains(page, `name="password"`)
}

var outagesLinkRe = regexp.MustCompile(`["'](/[^"'\s<>]*show_only_disconnections[^"'\s<>]*)["']`)

// outagesLink is the outages list the cabinet's page links, "" if none.
func outagesLink(page string) string {
	m := outagesLinkRe.FindStringSubmatch(page)
	if m == nil {
		return ""
	}
	return html.UnescapeString(m[1])
}

// localLayouts are the wall-clock forms the cabinet writes times in.
var localLayouts = []string{
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04",
	"2006-01-02 15:04",
	"02.01.2006 15:04:05",
	"02.01.2006 15:04",
}

// parseLocal reads a cabinet time: with a zone as written, without one
// as Kyiv wall-clock. A time at :59 seconds is the end of its minute.
func parseLocal(s string, loc *time.Location) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		for _, layout := range localLayouts {
			if t, err = time.ParseInLocation(layout, s, loc); err == nil {
				break
			}
		}
	}
	if err != nil {
		return time.Time{}, false
	}
	if t.Second() > 0 || t.Nanosecond() > 0 {
		t = t.Truncate(time.Minute).Add(time.Minute)
	}
	return t, true
}

// ParseOutages reads a cabinet's outages list, as Outages does, for a
// tool that fetched the page itself.
func ParseOutages(body []byte, loc *time.Location) ([]Interval, error) {
	return parseOutages(body, loc)
}

// parseOutages reads the cabinet's outages list. A list the cabinet
// changed the shape of is an error, not "no outages".
func parseOutages(body []byte, loc *time.Location) ([]Interval, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%w: %s", ErrNoData, snippet(trimmed))
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &resp); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoData, err)
	}
	raw, ok := resp["planned_list_cab"]
	if !ok {
		names := make([]string, 0, len(resp))
		for k := range resp {
			names = append(names, k)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("%w: немає planned_list_cab, є %s", ErrNoData, strings.Join(names, ", "))
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("%w: planned_list_cab: %v", ErrNoData, err)
	}
	out := make([]Interval, 0, len(items))
	for _, it := range items {
		begin, end := text(it["acc_begin"]), text(it["accend_plan"])
		start, ok1 := parseLocal(begin, loc)
		stop, ok2 := parseLocal(end, loc)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("%w: незрозумілий час %q – %q", ErrNoData, begin, end)
		}
		if !stop.After(start) {
			continue
		}
		kind := KindSchedule
		if id, ok := number(it["accidentid"]); ok && id != 0 {
			kind = KindOther
		}
		out = append(out, Interval{Start: start, End: stop, Kind: kind})
	}
	return merge(out), nil
}

// merge sorts intervals and joins overlapping or touching ones of a kind.
func merge(list []Interval) []Interval {
	sort.Slice(list, func(i, j int) bool {
		if !list[i].Start.Equal(list[j].Start) {
			return list[i].Start.Before(list[j].Start)
		}
		return list[i].Kind < list[j].Kind
	})
	out := make([]Interval, 0, len(list))
	for _, iv := range list {
		joined := false
		// Sorted by start, a kind's latest interval is the only one the
		// next can reach.
		for k := len(out) - 1; k >= 0; k-- {
			if out[k].Kind != iv.Kind {
				continue
			}
			if !iv.Start.After(out[k].End) {
				if iv.End.After(out[k].End) {
					out[k].End = iv.End
				}
				joined = true
			}
			break
		}
		if !joined {
			out = append(out, iv)
		}
	}
	return out
}

func text(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

func number(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, err == nil
	}
	return 0, false
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// snippet is the start of a page in plain words, for an error message.
func snippet(b []byte) string {
	s := strings.Join(strings.Fields(tagRe.ReplaceAllString(string(b), " ")), " ")
	if s == "" {
		return "порожня відповідь"
	}
	if utf8.RuneCountInString(s) > 160 {
		s = string([]rune(s)[:160]) + "…"
	}
	return s
}
