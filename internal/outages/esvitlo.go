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
//	GET  /account_legal                      → the legal accounts, each linked as
//	     /account_legal/view_main_details?a=<id>
//	GET  /account_household/show_only_disconnections?eic=&type_user=2&a=<id>
//	     → {"planned_list_cab": [{"acc_begin": "2026-11-19T06:00:00",
//	       "accend_plan": "2026-11-19T10:00:00", "eic": "62Z…", "typeid": …, …}],
//	       "current_list_cab": […], "fields": […], "keys_s": […]}
//
// A legal account's page asks for its outages by the account, not by
// EIC, and the answer names each item's point. A household cabinet asks
// by EIC and marks the hourly schedule with accidentid 0. Times carry no
// zone: they are Kyiv wall-clock.

// userAgent names the console honestly to the operator's server.
const userAgent = "SESTelemetry/1.0 (+https://github.com/Agroprosperis/sestelemtry)"

// outagesPath answers an account's outages, legal and household alike.
const outagesPath = "/account_household/show_only_disconnections"

// maxPage bounds what one cabinet page may weigh.
const maxPage = 4 << 20

// Errors the settings page puts into plain words.
var (
	ErrUnauthorized = errors.New("кабінет не прийняв логін або пароль")
	ErrBlocked      = errors.New("кабінет закрив доступ перевіркою на бота")
	ErrUnreachable  = errors.New("немає зʼєднання з кабінетом")
	ErrNoData       = errors.New("кабінет відповів не даними про відключення")
	ErrNoAccount    = errors.New("у кабінеті немає рахунку юридичної особи, а EIC не вказано")
)

// errSignedOut is a page the cabinet answers once it has dropped the
// session: sign in again.
var errSignedOut = errors.New("outages: the cabinet dropped the session")

// BaseURL is the address of the cabinet at host.
func BaseURL(host string) string { return "https://" + host }

// Client signs in to one e-svitlo cabinet and reads the outages it
// lists. It keeps the session between calls and signs in again when the
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
	// legal is the ids (a=) of the legal accounts the cabinet holds.
	legal []string

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

// SignIn opens a session with the account and finds the legal accounts
// the cabinet holds.
func (c *Client) SignIn(ctx context.Context) error {
	c.signedIn, c.landing, c.path, c.legal = false, "", "", nil
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
	landing, path := final.Path, outagesLink(page)
	legal, err := c.legalAccounts(ctx)
	if err != nil {
		return err
	}
	c.signedIn, c.landing, c.path, c.legal = true, landing, path, legal
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

var legalAccountRe = regexp.MustCompile(`/account_legal/view_main_details\?a=(\d+)`)

// legalAccounts is the ids of the legal accounts the cabinet lists, none
// for a household's.
func (c *Client) legalAccounts(ctx context.Context) ([]string, error) {
	page, final, err := c.fetch(ctx, http.MethodGet, "/account_legal", nil)
	if err != nil {
		if errors.Is(err, ErrBlocked) || errors.Is(err, ErrUnreachable) || ctx.Err() != nil {
			return nil, err
		}
		// A household cabinet may have no such section at all.
		return nil, nil
	}
	if strings.HasPrefix(final.Path, "/user/login") || loginPage(page) {
		return nil, ErrUnauthorized
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range legalAccountRe.FindAllStringSubmatch(page, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out, nil
}

// Outages returns the outages the cabinet lists — planned and under way —
// in time order, overlapping ones of a kind merged. A legal cabinet
// answers for each of its accounts, and eic, when given, keeps only that
// point's; a household cabinet is asked by eic.
func (c *Client) Outages(ctx context.Context, eic string) ([]Interval, error) {
	if !c.signedIn {
		if err := c.SignIn(ctx); err != nil {
			return nil, err
		}
	}
	list, err := c.readAll(ctx, eic)
	if errors.Is(err, errSignedOut) {
		if err := c.SignIn(ctx); err != nil {
			return nil, err
		}
		list, err = c.readAll(ctx, eic)
	}
	if errors.Is(err, errSignedOut) {
		return nil, ErrUnauthorized
	}
	return list, err
}

func (c *Client) readAll(ctx context.Context, eic string) ([]Interval, error) {
	if len(c.legal) == 0 {
		if eic == "" {
			return nil, ErrNoAccount
		}
		list, err := c.read(ctx, c.householdURL(eic), "")
		return merge(list), err
	}
	var all []Interval
	for _, a := range c.legal {
		list, err := c.read(ctx, legalURL(a), eic)
		if err != nil {
			return nil, err
		}
		all = append(all, list...)
	}
	return merge(all), nil
}

// read fetches one outages list. eic, when given, keeps the items of
// that point and those that name none.
func (c *Client) read(ctx context.Context, path, eic string) ([]Interval, error) {
	page, final, err := c.fetch(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(final.Path, "/user/login") || loginPage(page) {
		return nil, errSignedOut
	}
	list, err := parseItems([]byte(page), c.loc)
	if err != nil || eic == "" {
		return list, err
	}
	kept := list[:0]
	for _, iv := range list {
		if iv.EIC == "" || iv.EIC == eic {
			kept = append(kept, iv)
		}
	}
	return kept, nil
}

// legalURL is where a legal account's page reads its outages from.
func legalURL(account string) string {
	return outagesPath + "?" + url.Values{"eic": {""}, "type_user": {"2"}, "a": {account}}.Encode()
}

// householdURL is a household point's outages list: the link the
// cabinet gave with its eic set, else outagesPath.
func (c *Client) householdURL(eic string) string {
	u, err := url.Parse(c.path)
	if c.path == "" || err != nil {
		u = &url.URL{Path: outagesPath}
	}
	q := u.Query()
	q.Set("eic", eic)
	u.RawQuery = q.Encode()
	return u.String()
}

// Probe is what one test of a cabinet found, for the settings page.
type Probe struct {
	SignedIn bool   `json:"signed_in"`
	Landing  string `json:"landing,omitempty"`
	// Accounts is the legal accounts (a=) the cabinet holds.
	Accounts []string   `json:"accounts,omitempty"`
	Path     string     `json:"path,omitempty"`
	Outages  []Interval `json:"outages"`
}

// Probe signs in and reads the outages: a legal cabinet's for its
// accounts, kept to eic when given; a household's for eic.
func (c *Client) Probe(ctx context.Context, eic string) (Probe, error) {
	p := Probe{Outages: []Interval{}}
	if err := c.SignIn(ctx); err != nil {
		return p, err
	}
	p.SignedIn, p.Landing, p.Accounts = true, c.landing, c.legal
	switch {
	case len(c.legal) > 0:
		p.Path = legalURL(c.legal[0])
	case eic != "":
		p.Path = c.householdURL(eic)
	default:
		return p, nil
	}
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
// The cabinet's own page strips stray quotes from them, and so does this.
func parseLocal(s string, loc *time.Location) (time.Time, bool) {
	s = strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"`))
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

// parseOutages reads the cabinet's outages list, overlapping items of a
// kind merged.
func parseOutages(body []byte, loc *time.Location) ([]Interval, error) {
	list, err := parseItems(body, loc)
	if err != nil {
		return nil, err
	}
	return merge(list), nil
}

// outageLists are the lists one answer carries: the planned outages and
// those under way.
var outageLists = []string{"planned_list_cab", "current_list_cab"}

// parseItems reads every outage of an answer. An answer the cabinet
// changed the shape of is an error, not "no outages".
func parseItems(body []byte, loc *time.Location) ([]Interval, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%w: %s", ErrNoData, snippet(trimmed))
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &resp); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoData, err)
	}
	if _, ok := resp["planned_list_cab"]; !ok {
		names := make([]string, 0, len(resp))
		for k := range resp {
			names = append(names, k)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("%w: немає planned_list_cab, є %s", ErrNoData, strings.Join(names, ", "))
	}
	out := []Interval{}
	for _, key := range outageLists {
		raw, ok := resp[key]
		if !ok {
			continue
		}
		var items []map[string]any
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrNoData, key, err)
		}
		for _, it := range items {
			iv, ok, err := item(it, loc)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, iv)
			}
		}
	}
	return out, nil
}

// item reads one outage. One under way with no planned end yet is left
// out: there is no stretch ahead to shade.
func item(it map[string]any, loc *time.Location) (Interval, bool, error) {
	begin, end := text(it["acc_begin"]), text(it["accend_plan"])
	start, ok := parseLocal(begin, loc)
	if !ok {
		return Interval{}, false, fmt.Errorf("%w: незрозумілий час %q – %q", ErrNoData, begin, end)
	}
	if strings.Trim(strings.TrimSpace(end), `"`) == "" {
		return Interval{}, false, nil
	}
	stop, ok := parseLocal(end, loc)
	if !ok {
		return Interval{}, false, fmt.Errorf("%w: незрозумілий час %q – %q", ErrNoData, begin, end)
	}
	if !stop.After(start) {
		return Interval{}, false, nil
	}
	return Interval{Start: start, End: stop, Kind: kindOf(it), EIC: NormalizeEIC(text(it["eic"]))}, true, nil
}

// kindOf tells the hourly schedule from planned works and accidents. A
// household's list marks the schedule with accidentid 0; a legal
// account's names the reason in typeid.
func kindOf(it map[string]any) Kind {
	if id, ok := number(it["accidentid"]); ok {
		if id == 0 {
			return KindSchedule
		}
		return KindOther
	}
	reason := strings.ToLower(text(it["typeid"]))
	for _, w := range []string{"гпв", "погодин", "графік"} {
		if strings.Contains(reason, w) {
			return KindSchedule
		}
	}
	return KindOther
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
