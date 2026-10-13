// Package settings exposes typed, cached company settings stored in company_settings.
package settings

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/models"
	"attendance/backend/internal/secret"
	"attendance/backend/internal/timeutil"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Settings struct {
	CompanyName             string  `json:"company_name"`
	Timezone                string  `json:"timezone"`
	ConfirmationRequired    bool    `json:"confirmation_required"`
	GPSEnabled              bool    `json:"gps_enabled"`
	GPSRequired             bool    `json:"gps_required"`
	GPSRejectOutside        bool    `json:"gps_reject_outside"`
	OfficeLatitude          float64 `json:"office_latitude"`
	OfficeLongitude         float64 `json:"office_longitude"`
	AllowedRadiusMeters     float64 `json:"allowed_radius_meters"`
	DailyReportEnabled      bool    `json:"daily_report_enabled"`
	DailyReportTime         string  `json:"daily_report_time"`
	MissingCheckinAlertTime string  `json:"missing_checkin_alert_time"`
	DayCloseTime            string  `json:"day_close_time"`

	// Telegram, managed in the admin panel. The token is never sent to clients.
	TelegramBotToken     string `json:"-"`
	TelegramBotTokenSet  bool   `json:"telegram_bot_token_set"`
	TelegramBotUsername  string `json:"telegram_bot_username"`
	MiniAppURL           string `json:"miniapp_url"`
	TelegramGroupID      int64  `json:"telegram_group_id"`       // company daily report
	TelegramReviewChatID int64  `json:"telegram_review_chat_id"` // 0 = DM reviewers

	Location *time.Location `json:"-"`
}

// TelegramReady reports whether notifications and the Mini App can work.
func (s *Settings) TelegramReady() bool { return s.TelegramBotToken != "" }

// SecretKeys are encrypted at rest and never echoed back or written to audit logs.
var SecretKeys = map[string]bool{"telegram_bot_token": true}

var botTokenRe = regexp.MustCompile(`^\d{5,15}:[A-Za-z0-9_-]{30,64}$`)

// validators keep bad values out of the table; every editable key must be listed here.
var validators = map[string]func(string) error{
	"company_name":               nonEmpty,
	"timezone":                   func(v string) error { _, err := time.LoadLocation(v); return err },
	"confirmation_required":      isBool,
	"gps_enabled":                isBool,
	"gps_required":               isBool,
	"gps_reject_outside":         isBool,
	"office_latitude":            floatRange(-90, 90),
	"office_longitude":           floatRange(-180, 180),
	"allowed_radius_meters":      floatRange(10, 100000),
	"daily_report_enabled":       isBool,
	"daily_report_time":          isHHMM,
	"missing_checkin_alert_time": isHHMM,
	"day_close_time":             isHHMM,
	"telegram_bot_token":         optional(matches(botTokenRe, "must look like 123456789:AA… (from @BotFather)")),
	"miniapp_url":                optional(isHTTPSURL),
	"telegram_group_id":          isInt,
	"telegram_review_chat_id":    isInt,
}

type Service struct {
	db     *gorm.DB
	box    *secret.Box // encrypts SecretKeys
	mu     sync.RWMutex
	cached *Settings
	expiry time.Time
	ttl    time.Duration
}

func NewService(db *gorm.DB, box *secret.Box) *Service {
	return &Service{db: db, box: box, ttl: 30 * time.Second}
}

func (s *Service) Get(ctx context.Context) (*Settings, error) {
	s.mu.RLock()
	if s.cached != nil && time.Now().Before(s.expiry) {
		c := s.cached
		s.mu.RUnlock()
		return c, nil
	}
	s.mu.RUnlock()

	var rows []models.CompanySetting
	if err := s.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	for k := range SecretKeys {
		v, err := s.box.Open(m[k])
		if err != nil {
			slog.Warn("settings: stored secret is unreadable (JWT_SECRET changed?); treating it as unset", "key", k)
		}
		m[k] = v
	}
	st := FromMap(m)
	s.mu.Lock()
	s.cached, s.expiry = st, time.Now().Add(s.ttl)
	s.mu.Unlock()
	return st, nil
}

// MustGet is used where settings are needed but DB failure already surfaced elsewhere.
func (s *Service) Location(ctx context.Context) *time.Location {
	if st, err := s.Get(ctx); err == nil {
		return st.Location
	}
	return timeutil.LoadLocation("Asia/Phnom_Penh")
}

func FromMap(m map[string]string) *Settings {
	b := func(k string) bool { return strings.EqualFold(m[k], "true") }
	f := func(k string) float64 { v, _ := strconv.ParseFloat(m[k], 64); return v }
	i := func(k string) int64 { v, _ := strconv.ParseInt(m[k], 10, 64); return v }
	st := &Settings{
		CompanyName:             m["company_name"],
		Timezone:                or(m["timezone"], "Asia/Phnom_Penh"),
		ConfirmationRequired:    b("confirmation_required"),
		GPSEnabled:              b("gps_enabled"),
		GPSRequired:             b("gps_required"),
		GPSRejectOutside:        b("gps_reject_outside"),
		OfficeLatitude:          f("office_latitude"),
		OfficeLongitude:         f("office_longitude"),
		AllowedRadiusMeters:     f("allowed_radius_meters"),
		DailyReportEnabled:      b("daily_report_enabled"),
		DailyReportTime:         or(m["daily_report_time"], "18:00"),
		MissingCheckinAlertTime: or(m["missing_checkin_alert_time"], "10:00"),
		DayCloseTime:            or(m["day_close_time"], "23:50"),
		TelegramBotToken:        m["telegram_bot_token"],
		TelegramBotUsername:     m["telegram_bot_username"],
		MiniAppURL:              m["miniapp_url"],
		TelegramGroupID:         i("telegram_group_id"),
		TelegramReviewChatID:    i("telegram_review_chat_id"),
	}
	st.TelegramBotTokenSet = st.TelegramBotToken != ""
	// GPS "required" only makes sense when GPS is enabled.
	if !st.GPSEnabled {
		st.GPSRequired, st.GPSRejectOutside = false, false
	}
	st.Location = timeutil.LoadLocation(st.Timezone)
	return st
}

// Update validates and upserts the given keys. Returns the previous values for auditing.
func (s *Service) Update(ctx context.Context, actorID uint64, changes map[string]string) (map[string]string, error) {
	if len(changes) == 0 {
		return nil, apperr.BadRequest("NO_CHANGES", "No settings provided.")
	}
	for k, v := range changes {
		validate, ok := validators[k]
		if !ok {
			return nil, apperr.BadRequest("UNKNOWN_SETTING", fmt.Sprintf("Unknown setting %q.", k))
		}
		if err := validate(strings.TrimSpace(v)); err != nil {
			return nil, apperr.BadRequest("INVALID_SETTING", fmt.Sprintf("Invalid value for %s: %v", k, err))
		}
	}
	prev := map[string]string{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []models.CompanySetting
		if err := tx.Find(&rows).Error; err != nil {
			return err
		}
		for _, r := range rows {
			if _, ok := changes[r.Key]; ok {
				prev[r.Key] = r.Value
			}
		}
		for k, v := range changes {
			v = strings.TrimSpace(v)
			if SecretKeys[k] {
				sealed, err := s.box.Seal(v)
				if err != nil {
					return err
				}
				v = sealed
			}
			row := models.CompanySetting{Key: k, Value: v, UpdatedBy: &actorID}
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()
	return prev, nil
}

// SetBotUsername records the bot's @username (learned from Telegram, not user-editable).
func (s *Service) SetBotUsername(ctx context.Context, username string) error {
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).
		Create(&models.CompanySetting{Key: "telegram_bot_username", Value: username}).Error
	if err == nil {
		s.mu.Lock()
		s.cached = nil
		s.mu.Unlock()
	}
	return err
}

func or(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func nonEmpty(v string) error {
	if v == "" {
		return fmt.Errorf("must not be empty")
	}
	return nil
}

func isBool(v string) error {
	if v != "true" && v != "false" {
		return fmt.Errorf("must be true or false")
	}
	return nil
}

func isHHMM(v string) error { _, _, err := timeutil.ParseHHMM(v); return err }

func isInt(v string) error {
	if _, err := strconv.ParseInt(v, 10, 64); err != nil {
		return fmt.Errorf("must be a whole number (Telegram chat IDs of groups start with -)")
	}
	return nil
}

func isHTTPSURL(v string) error {
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("must be an https:// URL (Telegram only opens Mini Apps over HTTPS)")
	}
	return nil
}

func matches(re *regexp.Regexp, msg string) func(string) error {
	return func(v string) error {
		if !re.MatchString(v) {
			return fmt.Errorf("%s", msg)
		}
		return nil
	}
}

// optional allows "" (clears the setting) or a value passing check.
func optional(check func(string) error) func(string) error {
	return func(v string) error {
		if v == "" {
			return nil
		}
		return check(v)
	}
}

func floatRange(min, max float64) func(string) error {
	return func(v string) error {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < min || f > max {
			return fmt.Errorf("must be a number between %v and %v", min, max)
		}
		return nil
	}
}
