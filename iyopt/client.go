package iyopt

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/nu7hatch/gouuid"

	"encoding/json"
)

const (
	SUGGEST_URL_FMT = "/accounts/%d/labs/%s/suggest"
	REWARD_URL      = "/reward"
)

var (
	host                = "https://bandits.incisive.ly/v1"
	ErrEmptyUserId      = errors.New("empty user id")
	ErrEmptyRewardToken = errors.New("empty reward token")
	ErrNoRewardCookie   = errors.New("no reward cookie found")
)

type NullString struct {
	Valid  bool
	String string
}

func (s *NullString) UnmarshalJSON(data []byte) error {
	var val *string
	if err := json.Unmarshal(data, &val); err != nil {
		return err
	}

	if val == nil {
		s = &NullString{}
	} else {
		s.Valid, s.String = true, *val
	}
	return nil
}

type Suggestion struct {
	VariantCode    string     `json:"variant_id"`
	RewardToken    NullString `json:"reward_token"`
	Content        NullString `json:"content"`
	ExperimentCode string     `json:"experiment_id"`
}

type Reward struct {
	Token string
}

type Error struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func (e Error) Error() string { return fmt.Sprintf("[Code %d] %s", e.Code, e.Message) }

type Cookie struct {
	http.Cookie
	Duration time.Duration
	now      func() time.Time
}

func (c Cookie) New(value string) http.Cookie {
	c.Cookie.Value = value
	c.Cookie.Expires = c.now().Add(c.Duration)
	return c.Cookie
}

type Client struct {
	c            *http.Client
	uid          func() (string, error)
	sURL         string
	rURL         string
	UserCookie   Cookie
	RewardCookie Cookie
}

type Option func(*Client)

func WithDomain(domain string) func(*Client) {
	return func(c *Client) {
		c.UserCookie.Domain = domain
		c.RewardCookie.Domain = domain
	}
}

func WithHTTPClient(c *http.Client) func(*Client) {
	return func(client *Client) {
		client.c = c
	}
}

func NewClient(accountID int64, labID string, options ...Option) *Client {
	c := &Client{
		c:    http.DefaultClient,
		uid:  userID,
		sURL: fmt.Sprintf(host+SUGGEST_URL_FMT, accountID, labID),
		rURL: host + REWARD_URL,
		UserCookie: Cookie{
			Cookie: http.Cookie{
				Name: "iyV",
			},
			// Approximately three years.
			Duration: time.Hour * time.Duration(26297),
			now:      time.Now,
		},
		RewardCookie: Cookie{
			Cookie: http.Cookie{
				Name: "iyR-" + labID,
			},
			// Approximately three years.
			Duration: time.Hour * time.Duration(26297),
			now:      time.Now,
		},
	}

	for _, option := range options {
		option(c)
	}
	return c
}

type ResourceNotFoundError struct {
	s string
}

func (e ResourceNotFoundError) Error() string {
	return e.s
}

func newResourceNotFoundError(uri string) ResourceNotFoundError {
	format := "resource %s not found"
	return ResourceNotFoundError{s: fmt.Sprintf(format, uri)}
}

func userID() (string, error) {
	u, err := uuid.NewV4()
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (c *Client) Suggestion(user string) (s Suggestion, err error) {
	if user == "" {
		return s, ErrEmptyUserId
	}

	// Build URL for suggestion.
	params := url.Values{}
	params.Add("user", user)
	fullURL := c.sURL + "?" + params.Encode()

	resp, err := c.c.Get(fullURL)
	if err != nil {
		return s, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return s, newResourceNotFoundError(fullURL)
	} else if resp.StatusCode != http.StatusOK {
		var e Error
		if err = json.NewDecoder(resp.Body).Decode(&e); err != nil {
			return s, err
		}
		return s, e
	}
	err = json.NewDecoder(resp.Body).Decode(&s)
	return s, err
}

func (c *Client) SuggestionWithReq(w http.ResponseWriter, r *http.Request) (s Suggestion, err error) {
	var userID string

	// Check for existing user in cookie.
	uc, err := r.Cookie(c.UserCookie.Name)
	if err != nil {
		// Will only be ErrNoCookie, so let's generate a unique userID.
		if userID, err = c.uid(); err != nil {
			return s, err
		}

		// Set the new cookie.
		cookie := c.UserCookie.New(userID)
		http.SetCookie(w, &cookie)
	} else {
		userID = uc.Value
	}

	if s, err = c.Suggestion(userID); err != nil {
		return s, err
	}

	// If we got a reward_token we should write it out to a cookie.
	if s.RewardToken.Valid {
		cookie := c.RewardCookie.New(s.RewardToken.String)
		http.SetCookie(w, &cookie)
	}
	return s, nil
}

func (c *Client) Reward(r Reward) error {
	if r.Token == "" {
		return ErrEmptyRewardToken
	}

	data := url.Values{}
	data.Add("token", r.Token)
	resp, err := c.c.PostForm(c.rURL, data)
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	var e Error
	defer resp.Body.Close()
	if err = json.NewDecoder(resp.Body).Decode(&e); err != nil {
		return err
	}
	return e
}

func (c *Client) RewardWithReq(w http.ResponseWriter, r *http.Request) error {
	// Check for existing reward token in cookie.
	rc, err := r.Cookie(c.RewardCookie.Name)
	if err != nil {
		// Will only be ErrNoCookie: no reward_token available.
		return ErrNoRewardCookie
	}

	if err := c.Reward(Reward{Token: rc.Value}); err != nil {
		return err
	}

	// Expire the reward cookie.
	rc.Value, rc.Expires = "", time.Unix(1, 0)
	http.SetCookie(w, rc)
	return nil
}
