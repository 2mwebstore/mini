package auth

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

const testBotToken = "123456:TEST-token"

// signInitData builds initData the way Telegram does, for tests.
func signInitData(token string, fields map[string]string) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	q := url.Values{}
	for i, k := range keys {
		pairs[i] = k + "=" + fields[k]
		q.Set(k, fields[k])
	}
	q.Set("hash", hex.EncodeToString(webAppSign(token, strings.Join(pairs, "\n"))))
	return q.Encode()
}

func TestValidateWebAppInitData(t *testing.T) {
	now := time.Date(2026, 10, 8, 2, 0, 0, 0, time.UTC)
	fields := func(authDate time.Time, user string) map[string]string {
		return map[string]string{
			"auth_date": fmt.Sprint(authDate.Unix()),
			"query_id":  "AAHdF6IQAAAAAN0XohDhrOrc",
			"user":      user,
			// Bot API 8.0+ adds an Ed25519 signature; it is part of the HMAC data-check-string.
			"signature": "abc_DEF-123",
		}
	}
	user := `{"id":1001,"first_name":"Dara","last_name":"Sok","username":"dara","language_code":"en"}`

	good := signInitData(testBotToken, fields(now.Add(-time.Hour), user))
	init, err := ValidateWebAppInitData(good, testBotToken, 24*time.Hour, now)
	if err != nil {
		t.Fatalf("valid initData rejected: %v", err)
	}
	if init.User.ID != 1001 || init.User.Username != "dara" {
		t.Fatalf("user not parsed: %+v", init.User)
	}

	cases := map[string]struct {
		raw  string
		code string
	}{
		"empty":         {"", "INVALID_INIT_DATA"},
		"wrong token":   {signInitData("999:other", fields(now, user)), "INVALID_INIT_DATA"},
		"tampered user": {strings.Replace(good, "1001", "1002", 1), "INVALID_INIT_DATA"},
		"no hash":       {strings.Split(good, "&hash=")[0], "INVALID_INIT_DATA"},
		"expired":       {signInitData(testBotToken, fields(now.Add(-25*time.Hour), user)), "INIT_DATA_EXPIRED"},
		"future":        {signInitData(testBotToken, fields(now.Add(time.Hour), user)), "INVALID_INIT_DATA"},
		"no user":       {signInitData(testBotToken, map[string]string{"auth_date": fmt.Sprint(now.Unix())}), "INVALID_INIT_DATA"},
		"bot user":      {signInitData(testBotToken, fields(now, `{"id":5,"is_bot":true}`)), "INVALID_INIT_DATA"},
		"duplicate key": {good + "&auth_date=1", "INVALID_INIT_DATA"},
	}
	for name, tc := range cases {
		_, err := ValidateWebAppInitData(tc.raw, testBotToken, 24*time.Hour, now)
		if err == nil || !strings.HasPrefix(err.Error(), tc.code) {
			t.Errorf("%s: want %s, got %v", name, tc.code, err)
		}
	}

	// maxAge 0 disables the age check.
	old := signInitData(testBotToken, fields(now.Add(-30*24*time.Hour), user))
	if _, err := ValidateWebAppInitData(old, testBotToken, 0, now); err != nil {
		t.Errorf("maxAge=0 should accept old initData: %v", err)
	}
}

// Known-answer test so the derivation can't silently drift from Telegram's spec:
// secret = HMAC_SHA256(key="WebAppData", msg=token); hash = HMAC_SHA256(key=secret, msg=data).
// Reference value computed independently with Python's hmac module.
func TestWebAppSignKnownAnswer(t *testing.T) {
	got := hex.EncodeToString(webAppSign("123456:ABC", "auth_date=1\nuser={\"id\":1}"))
	const want = "1bdd4575090fe601e65ef3d497e0cb43de3a86a4259bbc28aa2f6a6b51a0dcb6"
	if got != want {
		t.Fatalf("webAppSign = %s, want %s", got, want)
	}
}
