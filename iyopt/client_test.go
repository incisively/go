package iyopt

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNullString(t *testing.T) {
	examples := []struct {
		In       string
		Expected NullString
		Err      error
	}{
		{In: `null`},
		{In: `""`, Expected: NullString{Valid: true}},
		{In: `"foo"`, Expected: NullString{Valid: true, String: "foo"}},
		{In: `{}`,
			Err: errors.New("json: cannot unmarshal object into Go value of type string"),
		},
	}

	for i, ex := range examples {
		var actual NullString
		err := json.Unmarshal([]byte(ex.In), &actual)
		if fmt.Sprintf("%s", err) != fmt.Sprintf("%s", ex.Err) {
			t.Errorf("[example %d] got %s, expected %s", i, err, ex.Err)
		}

		if actual != ex.Expected {
			t.Errorf("[example %d] got %#v, expected %#v", i, actual, ex.Expected)
		}
	}
}

func TestCookie(t *testing.T) {
	c := Cookie{
		Duration: time.Minute,
		now:      func() time.Time { return time.Date(2015, 2, 3, 0, 2, 0, 0, time.UTC) },
	}

	next := c.New("foo")
	expExpires := time.Date(2015, 2, 3, 0, 3, 0, 0, time.UTC)
	if !reflect.DeepEqual(next.Expires, expExpires) {
		t.Errorf("Expires was %v, expected %v", next.Expires, expExpires)
	}

	if next.Value != "foo" {
		t.Errorf("Value was %q, expected %q", next.Value, "foo")
	}
}

func TestWithDomain(t *testing.T) {
	expected := ".example.com"
	c := NewClient(0, "", WithDomain(expected))
	if c.UserCookie.Domain != expected {
		t.Errorf("UserCookie.Domain was %q, expected %q", c.UserCookie.Domain, expected)
	}
	if c.RewardCookie.Domain != expected {
		t.Errorf("RewardCookie.Domain was %q, expected %q", c.RewardCookie.Domain, expected)
	}
}

func TestWithHTTPClient(t *testing.T) {
	client := &http.Client{}
	client.Timeout = time.Second
	c := NewClient(0, "", WithHTTPClient(client))

	if c.c.Timeout != time.Second {
		t.Errorf("Timeout was %v, expected %v", c.c.Timeout, time.Second)
	}
}

func TestNewClient(t *testing.T) {
	c := NewClient(1234, "abcde")
	expSURL := host + "/accounts/1234/labs/abcde/suggest"
	if c.sURL != expSURL {
		t.Errorf("csURL was %q, expected %q", c.sURL, expSURL)
	}

	expRURL := host + "/reward"
	if c.rURL != expRURL {
		t.Errorf("csURL was %q, expected %q", c.rURL, expRURL)
	}

	if c.UserCookie.Cookie.Name != "iyV" {
		t.Errorf("UserCookie.Cookie.Name was %q, expected %q", c.UserCookie.Cookie.Name, "iyV")
	}

	if c.RewardCookie.Cookie.Name != "iyR-abcde" {
		t.Errorf("RewardCookie.Cookie.Name was %q, expected %q", c.RewardCookie.Cookie.Name, "iyR-abcde")
	}
}

func TestUserID(t *testing.T) {
	v, err := userID()
	if v == "" {
		t.Error("userID was empty")
	}
	if err != nil {
		t.Error(err)
	}
}

func TestSuggestion(t *testing.T) {
	c := NewClient(123, "l1")
	examples := []struct {
		User       string
		Suggestion Suggestion
		Err        error
	}{
		{User: "", Err: ErrEmptyUserId},
		{
			User: "abc123",
			Suggestion: Suggestion{
				VariantCode:    "v1",
				RewardToken:    NullString{Valid: true, String: "token1=="},
				Content:        NullString{Valid: true, String: `{"key": 22}`},
				ExperimentCode: "e1",
			},
		},
		{
			User: "badresponse",
			Err:  Error{Message: "problem", Code: http.StatusBadRequest},
		},
		{
			User: "willnotexist",
			Err:  newResourceNotFoundError(host + "/accounts/123/labs/l1/suggest?user=willnotexist"),
		},
	}

	for i, ex := range examples {
		suggestion, err := c.Suggestion(ex.User)
		if fmt.Sprintf("%s", err) != fmt.Sprintf("%s", ex.Err) {
			t.Errorf("[example %d] err was %q, expected %q", i, err, ex.Err)
		}

		if !reflect.DeepEqual(suggestion, ex.Suggestion) {
			t.Errorf("[example %d] suggestion was %#v, expected %#v", i, suggestion, ex.Suggestion)
		}
	}
}

func TestSuggestionWithReq(t *testing.T) {

	c := NewClient(123, "l1")
	// Make new userIDs deterministic.
	c.uid = func() (string, error) {
		return "newuserid", nil
	}

	examples := []struct {
		Cookie     *http.Cookie
		Suggestion Suggestion
		CookiesSet []string
	}{
		// New user example.
		{
			Cookie: nil,
			Suggestion: Suggestion{
				VariantCode:    "v1",
				RewardToken:    NullString{Valid: true, String: "token1=="},
				Content:        NullString{Valid: true, String: `{"key": 22}`},
				ExperimentCode: "e1",
			},
			CookiesSet: []string{"iyV=newuserid", "iyR-l1=token1=="},
		},
		// Existing user example.
		{
			Cookie: &http.Cookie{Name: "iyV", Value: "abc123"},
			Suggestion: Suggestion{
				VariantCode:    "v1",
				RewardToken:    NullString{Valid: true, String: "token1=="},
				Content:        NullString{Valid: true, String: `{"key": 22}`},
				ExperimentCode: "e1",
			},
			CookiesSet: []string{"iyR-l1=token1=="},
		},
		// Suggestion with no reward token
		{
			Cookie:     &http.Cookie{Name: "iyV", Value: "rewarded"},
			Suggestion: Suggestion{VariantCode: "v2", ExperimentCode: "e2"},
			CookiesSet: []string{},
		},
	}

	for i, ex := range examples {
		// Route doesn't matter since we're calling handler directly.
		r, err := http.NewRequest("GET", "/", nil)
		if err != nil {
			panic(err)
		}

		if ex.Cookie != nil {
			r.AddCookie(ex.Cookie)
		}

		w := httptest.NewRecorder()
		suggestion, err := c.SuggestionWithReq(w, r)
		if err != nil {
			t.Error(err)
		}

		if !reflect.DeepEqual(suggestion, ex.Suggestion) {
			t.Errorf("[example %d] suggestion was %#v, expected %#v", i, suggestion, ex.Suggestion)
		}

		// All cookies to be set in response.
		ch := w.HeaderMap["Set-Cookie"]
		if len(ch) != len(ex.CookiesSet) {
			t.Errorf("[example %d] Set-Cookie header contains %d cookies, expected %d", i, len(ch), len(ex.CookiesSet))
		}

		// Join all the cookies that have been set.
		chstr := strings.Join(ch, "\n")
		for _, kv := range ex.CookiesSet {
			if !strings.Contains(chstr, kv) {
				t.Errorf("[example %d] Set-Cookie headers %#v does not contain %q", i, ch, kv)
			}
		}
	}
}

func TestReward(t *testing.T) {
	c := NewClient(123, "l1")
	examples := []struct {
		Reward Reward
		Err    error
	}{
		{Reward: Reward{}, Err: ErrEmptyRewardToken},
		{
			Reward: Reward{Token: "notvalid"},
			Err:    Error{Message: "reward problem", Code: http.StatusBadRequest},
		},
		{Reward: Reward{Token: "secretToken=="}},
	}

	for i, ex := range examples {
		err := c.Reward(ex.Reward)
		if fmt.Sprintf("%s", err) != fmt.Sprintf("%s", ex.Err) {
			t.Errorf("[example %d] err was %q, expected %q", i, err, ex.Err)
		}
	}
}

func TestRewardWithRequest(t *testing.T) {
	c := NewClient(123, "l1")
	examples := []struct {
		Cookie     *http.Cookie
		CookiesSet []string
		Err        error
	}{
		// No reward token available in cookie.
		{Err: ErrNoRewardCookie},
		// Reward cookie but with invalid token.
		{
			Cookie: &http.Cookie{Name: "iyR-l1", Value: "badtoken"},
			Err:    Error{Message: "reward problem", Code: http.StatusBadRequest},
		},
		// Valid token in reward cookie.
		{
			Cookie:     &http.Cookie{Name: "iyR-l1", Value: "secretToken=="},
			CookiesSet: []string{"iyR-l1=; Expires=Thu, 01 Jan 1970 00:00:01 UTC"},
		},
	}

	for i, ex := range examples {
		// Route doesn't matter since we're calling handler directly.
		r, err := http.NewRequest("POST", "/", nil)
		if err != nil {
			panic(err)
		}

		if ex.Cookie != nil {
			r.AddCookie(ex.Cookie)
		}

		w := httptest.NewRecorder()
		if err = c.RewardWithReq(w, r); err != ex.Err {
			t.Errorf("[example %d] got err %q, expected %q", i, err, ex.Err)
		}

		ch := w.HeaderMap["Set-Cookie"]
		if len(ch) != len(ex.CookiesSet) {
			t.Errorf("[example %d] Set-Cookie header contains %d cookies, expected %d", i, len(ch), len(ex.CookiesSet))
		}

		// Join all the cookies that have been set.
		chstr := strings.Join(ch, "\n")
		for _, expires := range ex.CookiesSet {
			if !strings.Contains(chstr, expires) {
				t.Errorf("[example %d] Set-Cookie header %#v does not contain %q", i, chstr, expires)
			}
		}
	}
}

func TestMain(m *testing.M) {
	// Handler for stubbing out upstream Incisively service.
	h := func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.RequestURI {
		case "GET /accounts/123/labs/l1/suggest?user=abc123",
			"GET /accounts/123/labs/l1/suggest?user=newuserid":
			// Valid suggestion response.
			b := `{
			  "variant_id": "v1",
			  "experiment_id": "e1",
			  "content": "{\"key\": 22}",
			  "reward_token": "token1=="
			}`
			fmt.Fprint(w, b)
		case "GET /accounts/123/labs/l1/suggest?user=rewarded":
			// Suggestion response where there is no reward token.
			fmt.Fprint(w, `{"variant_id": "v2", "experiment_id": "e2"}`)
		case "GET /accounts/123/labs/l1/suggest?user=badresponse":
			// Bad request response.
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"message": "problem", "code": 400}`)
		case "POST /reward":
			if err := r.ParseForm(); err != nil {
				panic(err)
			}
			if r.FormValue("token") != "secretToken==" {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"message": "reward problem", "code": 400}`)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(h))
	defer srv.Close()
	// Set host to test server
	host = srv.URL

	os.Exit(m.Run())
}
