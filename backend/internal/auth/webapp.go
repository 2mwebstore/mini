package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"attendance/backend/internal/apperr"
)

// Telegram Mini App authentication. The Mini App sends Telegram.WebApp.initData,
// which Telegram signs with a key derived from the bot token. A valid signature
// proves the numeric user ID came from Telegram, so it resolves through
// ActorFromTelegram to the linked employee.
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app

type WebAppUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type WebAppInit struct {
	User     WebAppUser
	AuthDate time.Time
}

const maxInitDataLen = 8 << 10

var (
	errInitDataInvalid = apperr.New(http.StatusUnauthorized, "INVALID_INIT_DATA", "Please open this app from the Telegram bot.")
	errInitDataExpired = apperr.New(http.StatusUnauthorized, "INIT_DATA_EXPIRED", "Your session has expired. Please close and reopen the app.")
)

// ValidateWebAppInitData checks the initData signature and age. maxAge <= 0 disables the age check.
func ValidateWebAppInitData(raw, botToken string, maxAge time.Duration, now time.Time) (*WebAppInit, error) {
	if raw == "" || len(raw) > maxInitDataLen || botToken == "" {
		return nil, errInitDataInvalid
	}
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return nil, errInitDataInvalid
	}
	got, err := hex.DecodeString(vals.Get("hash"))
	if err != nil || len(got) != sha256.Size {
		return nil, errInitDataInvalid
	}
	// data-check-string: every field except hash, sorted by key, "key=value" joined by \n.
	pairs := make([]string, 0, len(vals))
	for k, v := range vals {
		if k == "hash" {
			continue
		}
		if len(v) != 1 {
			return nil, errInitDataInvalid
		}
		pairs = append(pairs, k+"="+v[0])
	}
	sort.Strings(pairs)
	if !hmac.Equal(got, webAppSign(botToken, strings.Join(pairs, "\n"))) {
		return nil, errInitDataInvalid
	}

	ts, err := strconv.ParseInt(vals.Get("auth_date"), 10, 64)
	if err != nil || ts <= 0 {
		return nil, errInitDataInvalid
	}
	authDate := time.Unix(ts, 0).UTC()
	if authDate.After(now.Add(time.Minute)) {
		return nil, errInitDataInvalid
	}
	if maxAge > 0 && now.Sub(authDate) > maxAge {
		return nil, errInitDataExpired
	}

	var u WebAppUser
	if err := json.Unmarshal([]byte(vals.Get("user")), &u); err != nil || u.ID <= 0 || u.IsBot {
		return nil, errInitDataInvalid
	}
	return &WebAppInit{User: u, AuthDate: authDate}, nil
}

// webAppSign returns HMAC_SHA256(HMAC_SHA256("WebAppData", botToken), data).
func webAppSign(botToken, data string) []byte {
	k := hmac.New(sha256.New, []byte("WebAppData"))
	k.Write([]byte(botToken))
	m := hmac.New(sha256.New, k.Sum(nil))
	m.Write([]byte(data))
	return m.Sum(nil)
}
