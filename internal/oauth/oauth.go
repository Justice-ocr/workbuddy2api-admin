// Package oauth shares the CLI device-authorization protocol with the admin UI.
package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"
)

const (
	GlobalBase = "https://www.workbuddy.ai"
	CNBase     = "https://copilot.tencent.com"
	CNOrigin   = "https://www.codebuddy.cn"
	UserAgent  = "CLI/2.63.2 CodeBuddy/2.63.2"
)

var ErrPending = errors.New("authorization pending")

type State struct {
	State   string `json:"state"`
	AuthURL string `json:"authUrl"`
}

type Token struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int64  `json:"expiresIn"`
	Domain       string `json:"domain"`
}

type Account struct {
	UID          string `json:"uid"`
	EnterpriseID string `json:"enterpriseId"`
	Nickname     string `json:"nickname"`
}

type BusinessError struct{ Code int }

func (e *BusinessError) Error() string { return fmt.Sprintf("oauth business code %d", e.Code) }

func Headers(origin string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/plain, */*")
		r.Header.Set("X-Requested-With", "XMLHttpRequest")
		r.Header.Set("Origin", origin)
		r.Header.Set("Referer", origin+"/")
		r.Header.Set("User-Agent", UserAgent)
	}
}

// NewClient rejects redirects, including a global-to-CN redirect.
func NewClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Timeout: 30 * time.Second, Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func JSON(ctx context.Context, client *http.Client, method, endpoint string, headers func(*http.Request), body io.Reader) (json.RawMessage, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, 0, errors.New("invalid oauth request")
	}
	headers(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, errors.New("oauth transport unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, errors.New("oauth upstream rejected request")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, resp.StatusCode, errors.New("invalid oauth response size")
	}
	var env struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return nil, resp.StatusCode, errors.New("invalid oauth response")
	}
	if env.Code != 0 {
		return nil, resp.StatusCode, &BusinessError{env.Code}
	}
	return env.Data, resp.StatusCode, nil
}

func Begin(ctx context.Context, client *http.Client, base, origin string) (State, error) {
	raw, _, err := JSON(ctx, client, http.MethodPost, base+"/v2/plugin/auth/state?platform=CLI", Headers(origin), bytes.NewReader([]byte("{}")))
	if err != nil {
		return State{}, err
	}
	var st State
	if json.Unmarshal(raw, &st) != nil || st.State == "" || st.AuthURL == "" || len(st.State) > 4096 || len(st.AuthURL) > 8192 {
		return State{}, errors.New("invalid oauth state")
	}
	return st, nil
}

func PollToken(ctx context.Context, client *http.Client, base, origin, state string) (Token, error) {
	raw, _, err := JSON(ctx, client, http.MethodGet, base+"/v2/plugin/auth/token?state="+url.QueryEscape(state), Headers(origin), nil)
	if err != nil {
		var business *BusinessError
		// The existing CLI treats a business rejection on this endpoint as pending.
		if errors.As(err, &business) {
			return Token{}, ErrPending
		}
		return Token{}, err
	}
	var tok Token
	if json.Unmarshal(raw, &tok) != nil {
		return Token{}, errors.New("invalid oauth token response")
	}
	if tok.AccessToken == "" {
		return Token{}, ErrPending
	}
	return tok, nil
}

func ReadAccount(ctx context.Context, client *http.Client, base, origin, state, bearer string) (Account, error) {
	h := func(r *http.Request) { Headers(origin)(r); r.Header.Set("Authorization", "Bearer "+bearer) }
	raw, _, err := JSON(ctx, client, http.MethodGet, base+"/v2/plugin/login/account?state="+url.QueryEscape(state), h, nil)
	if err != nil {
		return Account{}, err
	}
	var acct Account
	if json.Unmarshal(raw, &acct) != nil {
		return Account{}, errors.New("invalid oauth account response")
	}
	return acct, nil
}

func ValidGlobalURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "www.workbuddy.ai" && u.User == nil
}
