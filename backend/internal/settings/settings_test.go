package settings

import "testing"

func TestFromMapGPSDependsOnEnabled(t *testing.T) {
	st := FromMap(map[string]string{"gps_enabled": "false", "gps_required": "true", "timezone": "Asia/Phnom_Penh"})
	if st.GPSRequired {
		t.Fatal("gps_required must be ignored while GPS is disabled")
	}
	if st.Location.String() != "Asia/Phnom_Penh" {
		t.Fatalf("location=%s", st.Location)
	}
}

func TestValidators(t *testing.T) {
	bad := map[string]string{"daily_report_time": "25:00", "office_latitude": "123", "timezone": "Mars/Base",
		"confirmation_required": "yes", "allowed_radius_meters": "1"}
	for k, v := range bad {
		if validators[k](v) == nil {
			t.Errorf("%s=%q should be rejected", k, v)
		}
	}
	if validators["daily_report_time"]("18:00") != nil || validators["timezone"]("Asia/Phnom_Penh") != nil {
		t.Error("valid values rejected")
	}
}

func TestTelegramValidators(t *testing.T) {
	good := map[string]string{
		"telegram_bot_token":      "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw",
		"miniapp_url":             "https://attendance.example.com/app/",
		"telegram_group_id":       "-1001234567890",
		"telegram_review_chat_id": "0",
	}
	for k, v := range good {
		if err := validators[k](v); err != nil {
			t.Errorf("%s=%q rejected: %v", k, v, err)
		}
	}
	for _, k := range []string{"telegram_bot_token", "miniapp_url"} {
		if validators[k]("") != nil {
			t.Errorf("%s must accept empty (clear)", k)
		}
	}
	bad := map[string]string{
		"telegram_bot_token": "not-a-token",
		"miniapp_url":        "http://attendance.example.com/app/",
		"telegram_group_id":  "-100abc",
	}
	for k, v := range bad {
		if validators[k](v) == nil {
			t.Errorf("%s=%q should be rejected", k, v)
		}
	}
	if _, editable := validators["telegram_bot_username"]; editable {
		t.Error("the bot username is learned from Telegram, not editable")
	}
}
