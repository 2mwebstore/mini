package settings

import "testing"

func TestFromMap(t *testing.T) {
	st := FromMap(map[string]string{"timezone": "Asia/Phnom_Penh", "confirmation_required": "true"})
	if st.Location.String() != "Asia/Phnom_Penh" || !st.ConfirmationRequired || st.DayCloseTime != "23:50" {
		t.Fatalf("settings: %+v", st)
	}
}

func TestValidators(t *testing.T) {
	bad := map[string]string{"daily_report_time": "25:00", "timezone": "Mars/Base", "confirmation_required": "yes"}
	for k, v := range bad {
		if validators[k](v) == nil {
			t.Errorf("%s=%q should be rejected", k, v)
		}
	}
	if validators["daily_report_time"]("18:00") != nil || validators["timezone"]("Asia/Phnom_Penh") != nil {
		t.Error("valid values rejected")
	}
	// Telegram and GPS belong to each branch now, not to company settings.
	for _, k := range []string{"telegram_bot_token", "miniapp_url", "telegram_group_id", "gps_enabled", "office_latitude"} {
		if _, editable := validators[k]; editable {
			t.Errorf("%s is a branch setting, not a company one", k)
		}
	}
}
