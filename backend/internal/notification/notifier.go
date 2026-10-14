// Package notification sends Telegram notifications to reviewers, employees and
// the company/branch groups, and records each delivery in the notifications table.
// Sends happen after the business transaction commits and never fail it. Chats,
// links and the bot token come from settings (managed in the admin panel); until a
// bot token is set, notifications are skipped.
package notification

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"attendance/backend/internal/models"
	"attendance/backend/internal/settings"
	"attendance/backend/internal/telegram"
	"attendance/backend/internal/timeutil"

	"gorm.io/gorm"
)

type Sender interface {
	SendMessage(ctx context.Context, chatID int64, text string, kb *telegram.InlineKeyboard) (*telegram.Message, error)
	SendPhoto(ctx context.Context, chatID int64, fileID, caption string, kb *telegram.InlineKeyboard) (*telegram.Message, error)
	SendPhotoData(ctx context.Context, chatID int64, data []byte, caption string, kb *telegram.InlineKeyboard) (*telegram.Message, error)
}

type Notifier struct {
	db       *gorm.DB
	tg       Sender
	settings *settings.Service
	async    bool
}

func New(db *gorm.DB, tg Sender, st *settings.Service) *Notifier {
	return &Notifier{db: db, tg: tg, settings: st, async: true}
}

// Sync makes sends blocking (tests).
func (n *Notifier) Sync() *Notifier { n.async = false; return n }

// config returns the current settings, or nil when Telegram isn't set up yet.
func (n *Notifier) config(ctx context.Context) *settings.Settings {
	st, err := n.settings.Get(ctx)
	if err != nil {
		slog.Error("notification: settings", "err", err)
		return nil
	}
	if !st.TelegramReady() {
		return nil
	}
	return st
}

// openApp returns a one-button keyboard that opens the Mini App at page (e.g.
// "team"), or nil if no Mini App URL is set. Telegram only allows web_app buttons
// in private chats (positive chat IDs); groups get a t.me direct link, which needs
// the bot's Main Mini App to be set in @BotFather.
func openApp(st *settings.Settings, chatID int64, text, page string) *telegram.InlineKeyboard {
	if st.MiniAppURL == "" {
		return nil
	}
	b := telegram.InlineButton{Text: text}
	switch {
	case chatID > 0:
		b.WebApp = &telegram.WebAppInfo{URL: strings.TrimSuffix(st.MiniAppURL, "/") + "/" + page}
	case st.TelegramBotUsername != "":
		b.URL = "https://t.me/" + st.TelegramBotUsername + "?startapp=" + page
	default:
		return nil
	}
	return &telegram.InlineKeyboard{InlineKeyboard: [][]telegram.InlineButton{{b}}}
}

type recipient struct {
	chatID     int64
	employeeID *uint64
}

// dms returns active employees (matching where/args) who have a linked Telegram account.
func (n *Notifier) dms(ctx context.Context, exclude uint64, where string, args ...any) []recipient {
	type row struct {
		EmployeeID     uint64
		TelegramUserID int64
	}
	var rows []row
	err := n.db.WithContext(ctx).Table("employees e").
		Select("e.id AS employee_id, t.telegram_user_id").
		Joins("JOIN telegram_accounts t ON t.employee_id = e.id").
		Where("e.status = ? AND e.deleted_at IS NULL AND e.id <> ?", models.EmployeeActive, exclude).
		Where(where, args...).Scan(&rows).Error
	if err != nil {
		slog.Error("notification: load recipients", "err", err)
		return nil
	}
	out := make([]recipient, 0, len(rows))
	for _, r := range rows {
		id := r.EmployeeID
		out = append(out, recipient{chatID: r.TelegramUserID, employeeID: &id})
	}
	return out
}

func (n *Notifier) branchOf(ctx context.Context, emp *models.Employee) *models.Branch {
	if emp.Branch != nil {
		return emp.Branch
	}
	if emp.BranchID == nil {
		return nil
	}
	var b models.Branch
	if n.db.WithContext(ctx).First(&b, *emp.BranchID).Error != nil {
		return nil
	}
	return &b
}

// reviewers decides who hears about emp's check-ins and leave requests:
//  1. the branch's review chat, else 2. the company review chat, else
//  3. DMs to the employee's manager and branch admin — or, when the branch has
//     neither (or they haven't linked Telegram), to HR and super admins.
func (n *Notifier) reviewers(ctx context.Context, st *settings.Settings, emp *models.Employee) []recipient {
	br := n.branchOf(ctx, emp)
	if br != nil && br.TelegramReviewChatID != 0 {
		return []recipient{{chatID: br.TelegramReviewChatID}}
	}
	if st.TelegramReviewChatID != 0 {
		return []recipient{{chatID: st.TelegramReviewChatID}}
	}
	var ids []uint64
	if emp.ManagerID != nil {
		ids = append(ids, *emp.ManagerID)
	}
	if br != nil && br.AdminEmployeeID != nil {
		ids = append(ids, *br.AdminEmployeeID)
	}
	if len(ids) > 0 {
		if out := n.dms(ctx, emp.ID, "e.id IN ?", ids); len(out) > 0 {
			return out
		}
	}
	return n.companyAdmins(ctx, st, emp.ID)
}

// companyAdmins: the company review chat, or DMs to HR and super admins.
func (n *Notifier) companyAdmins(ctx context.Context, st *settings.Settings, exclude uint64) []recipient {
	if st.TelegramReviewChatID != 0 {
		return []recipient{{chatID: st.TelegramReviewChatID}}
	}
	return n.dms(ctx, exclude, "e.role_id IN ?", models.CompanyWideRoleIDs())
}

func (n *Notifier) employeeChat(ctx context.Context, employeeID uint64) (int64, bool) {
	var acc models.TelegramAccount
	if err := n.db.WithContext(ctx).Where("employee_id = ?", employeeID).First(&acc).Error; err != nil {
		return 0, false
	}
	return acc.TelegramUserID, true
}

func (n *Notifier) run(fn func(ctx context.Context)) {
	if !n.async {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		fn(ctx)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				slog.Error("notification panic", "recover", r)
			}
		}()
		fn(ctx)
	}()
}

func (n *Notifier) record(ctx context.Context, r recipient, typ, refType string, refID uint64, msg *telegram.Message, err error) {
	row := models.Notification{RecipientChatID: r.chatID, RecipientEmployeeID: r.employeeID, Type: typ, RefType: refType, Status: "SENT", CreatedAt: time.Now().UTC()}
	if refID != 0 {
		row.RefID = &refID
	}
	if err != nil {
		row.Status = "FAILED"
		row.Error = truncate(err.Error(), 500)
		slog.Warn("telegram notification failed", "type", typ, "chat", r.chatID, "err", err)
	} else if msg != nil {
		id := msg.MessageID
		row.TelegramMessageID = &id
	}
	if e := n.db.WithContext(ctx).Create(&row).Error; e != nil {
		slog.Error("notification: record", "err", e)
	}
}

// ---------- attendance ----------

type AttendanceEvent struct {
	Attendance     models.Attendance
	Employee       models.Employee
	Type           string // CHECK_IN | CHECK_OUT
	ConfirmationID uint64
	Pending        bool
	PhotoID        uint64 // attendance_photos.id
	PhotoData      []byte
	LocationLabel  string
	Loc            *time.Location
}

func (n *Notifier) AttendanceSubmitted(ev AttendanceEvent) {
	n.run(func(ctx context.Context) {
		st := n.config(ctx)
		if st == nil {
			return
		}
		caption := AttendanceReviewCaption(ev)
		typ := "attendance.submitted"
		fileID := ""
		for _, r := range n.reviewers(ctx, st, &ev.Employee) {
			var kb *telegram.InlineKeyboard
			if ev.Pending {
				kb = openApp(st, r.chatID, "✅ Review in app", "team")
			}
			var msg *telegram.Message
			var err error
			if len(ev.PhotoData) == 0 { // check-ins have no photo unless one was sent
				msg, err = n.tg.SendMessage(ctx, r.chatID, caption, kb)
			} else if fileID == "" {
				// The first send uploads the image; Telegram's file_id is reused for the
				// other reviewers and saved on the photo row.
				msg, err = n.tg.SendPhotoData(ctx, r.chatID, ev.PhotoData, caption, kb)
				if p := msg.LargestPhoto(); err == nil && p != nil {
					fileID = p.FileID
					n.rememberPhotoFile(ctx, ev.PhotoID, p)
				}
			} else {
				msg, err = n.tg.SendPhoto(ctx, r.chatID, fileID, caption, kb)
			}
			n.record(ctx, r, typ, "attendance", ev.Attendance.ID, msg, err)
		}
	})
}

func (n *Notifier) rememberPhotoFile(ctx context.Context, photoID uint64, p *telegram.PhotoSize) {
	if photoID == 0 {
		return
	}
	err := n.db.WithContext(ctx).Model(&models.AttendancePhoto{}).Where("id = ? AND telegram_file_id = ''", photoID).
		Updates(map[string]any{"telegram_file_id": p.FileID, "telegram_file_unique_id": p.FileUniqueID}).Error
	if err != nil {
		slog.Error("notification: save photo file_id", "photo", photoID, "err", err)
	}
}

func AttendanceReviewCaption(ev AttendanceEvent) string {
	var b strings.Builder
	if ev.Pending {
		b.WriteString("🆕 <b>ATTENDANCE CONFIRMATION</b>\n")
	} else {
		b.WriteString("ℹ️ <b>ATTENDANCE RECORDED</b>\n")
	}
	fmt.Fprintf(&b, "👤 Staff: %s (%s)\n", telegram.Esc(ev.Employee.FullName), telegram.Esc(ev.Employee.EmployeeCode))
	fmt.Fprintf(&b, "📅 Date: %s\n", timeutil.FormatDate(ev.Attendance.WorkDate))
	if ev.Type == models.TypeCheckIn {
		fmt.Fprintf(&b, "🟢 Check-In: %s\n", timeutil.FormatClock(ev.Attendance.CheckInAt, ev.Loc))
		if ev.Attendance.LateMinutes > 0 {
			fmt.Fprintf(&b, "⏰ Late: %d min\n", ev.Attendance.LateMinutes)
		}
	} else {
		fmt.Fprintf(&b, "🔴 Check-Out: %s\n", timeutil.FormatClock(ev.Attendance.CheckOutAt, ev.Loc))
		fmt.Fprintf(&b, "⏱️ Working Time: %s\n", timeutil.FormatMinutes(ev.Attendance.WorkingMinutes))
		if ev.Attendance.EarlyLeaveMinutes > 0 {
			fmt.Fprintf(&b, "⚠️ Early leave: %d min\n", ev.Attendance.EarlyLeaveMinutes)
		}
	}
	if len(ev.PhotoData) > 0 {
		b.WriteString("📸 Photo attached\n")
	}
	if ev.LocationLabel != "" {
		fmt.Fprintf(&b, "📍 Location: %s\n", telegram.Esc(ev.LocationLabel))
	}
	if ev.Pending {
		b.WriteString("Status: 🟡 Pending")
	} else {
		b.WriteString("Status: ✅ Auto-confirmed")
	}
	return b.String()
}

func (n *Notifier) AttendanceReviewed(att models.Attendance, conf models.AttendanceConfirmation, reviewer models.Employee, loc *time.Location) {
	n.run(func(ctx context.Context) {
		st := n.config(ctx)
		if st == nil {
			return
		}
		chat, ok := n.employeeChat(ctx, att.EmployeeID)
		if !ok {
			return
		}
		label := "Check-In"
		at := att.CheckInAt
		if conf.Type == models.TypeCheckOut {
			label, at = "Check-Out", att.CheckOutAt
		}
		var text string
		if conf.Status == models.ConfConfirmed {
			text = fmt.Sprintf("✅ <b>Attendance confirmed.</b>\n📅 %s\n🕐 %s: %s\n👨‍💼 Confirmed by: %s",
				timeutil.FormatDate(att.WorkDate), label, timeutil.FormatClock(at, loc), telegram.Esc(reviewer.FullName))
		} else {
			text = fmt.Sprintf("❌ <b>Attendance rejected.</b>\n📅 %s\n🕐 %s: %s\nReason:\n%s",
				timeutil.FormatDate(att.WorkDate), label, timeutil.FormatClock(at, loc), telegram.Esc(conf.Reason))
			if conf.Type == models.TypeCheckIn && att.CheckOutAt == nil {
				text += "\n\nOpen the app to check in again."
			} else if conf.Type == models.TypeCheckOut {
				text += "\n\nOpen the app to check out again."
			}
		}
		msg, err := n.tg.SendMessage(ctx, chat, text, openApp(st, chat, "📱 Open app", ""))
		n.record(ctx, recipient{chatID: chat, employeeID: &att.EmployeeID}, "attendance.reviewed", "attendance", att.ID, msg, err)
	})
}

// ---------- leave ----------

func (n *Notifier) LeaveSubmitted(lr models.LeaveRequest, emp models.Employee, typeName string) {
	n.run(func(ctx context.Context) {
		st := n.config(ctx)
		if st == nil {
			return
		}
		text := fmt.Sprintf("🏖️ <b>NEW LEAVE REQUEST</b>\n👤 %s (%s)\n📅 %s\n📌 %s\n📝 %s\nStatus: 🟡 Pending",
			telegram.Esc(emp.FullName), telegram.Esc(emp.EmployeeCode), LeaveSpan(lr),
			telegram.Esc(typeName), telegram.Esc(lr.Reason))
		for _, r := range n.reviewers(ctx, st, &emp) {
			msg, err := n.tg.SendMessage(ctx, r.chatID, text, openApp(st, r.chatID, "✅ Review in app", "team"))
			n.record(ctx, r, "leave.submitted", "leave", lr.ID, msg, err)
		}
	})
}

func (n *Notifier) LeaveReviewed(lr models.LeaveRequest) {
	n.run(func(ctx context.Context) {
		st := n.config(ctx)
		if st == nil {
			return
		}
		chat, ok := n.employeeChat(ctx, lr.EmployeeID)
		if !ok {
			return
		}
		var text string
		if lr.Status == models.LeaveApproved {
			text = fmt.Sprintf("✅ <b>LEAVE APPROVED</b>\nYour leave has been approved.\n📅 %s", LeaveSpan(lr))
		} else {
			text = fmt.Sprintf("❌ <b>LEAVE REJECTED</b>\nYour leave was not approved.\n📅 %s", LeaveSpan(lr))
			if lr.ReviewNote != "" {
				text += "\nReason: " + telegram.Esc(lr.ReviewNote)
			}
		}
		msg, err := n.tg.SendMessage(ctx, chat, text, openApp(st, chat, "📱 My leave", "leave"))
		n.record(ctx, recipient{chatID: chat, employeeID: &lr.EmployeeID}, "leave.reviewed", "leave", lr.ID, msg, err)
	})
}

// ---------- broadcast ----------

// Group posts to the company group (daily report), falling back to the company review chat.
func (n *Notifier) Group(typ, text string) {
	n.run(func(ctx context.Context) {
		st := n.config(ctx)
		if st == nil {
			return
		}
		target := st.TelegramGroupID
		if target == 0 {
			target = st.TelegramReviewChatID
		}
		if target == 0 {
			slog.Info("no company Telegram group set; skipping group message", "type", typ)
			return
		}
		msg, err := n.tg.SendMessage(ctx, target, text, nil)
		n.record(ctx, recipient{chatID: target}, typ, "", 0, msg, err)
	})
}

// BranchGroup posts to a branch's own group (its daily report), if it has one.
func (n *Notifier) BranchGroup(b models.Branch, typ, text string) {
	if b.TelegramGroupID == 0 {
		return
	}
	n.run(func(ctx context.Context) {
		if n.config(ctx) == nil {
			return
		}
		msg, err := n.tg.SendMessage(ctx, b.TelegramGroupID, text, nil)
		n.record(ctx, recipient{chatID: b.TelegramGroupID}, typ, "branch", b.ID, msg, err)
	})
}

// Admins sends an operational alert company-wide (company review chat, or HR/super admin DMs).
func (n *Notifier) Admins(typ, text string) {
	n.run(func(ctx context.Context) {
		st := n.config(ctx)
		if st == nil {
			return
		}
		for _, r := range n.companyAdmins(ctx, st, 0) {
			msg, err := n.tg.SendMessage(ctx, r.chatID, text, nil)
			n.record(ctx, r, typ, "", 0, msg, err)
		}
	})
}

// BranchAdmins sends an alert about one branch to its review chat, or its admin by DM.
func (n *Notifier) BranchAdmins(b models.Branch, typ, text string) {
	n.run(func(ctx context.Context) {
		if n.config(ctx) == nil {
			return
		}
		var to []recipient
		switch {
		case b.TelegramReviewChatID != 0:
			to = []recipient{{chatID: b.TelegramReviewChatID}}
		case b.AdminEmployeeID != nil:
			to = n.dms(ctx, 0, "e.id = ?", *b.AdminEmployeeID)
		}
		for _, r := range to {
			msg, err := n.tg.SendMessage(ctx, r.chatID, text, nil)
			n.record(ctx, r, typ, "branch", b.ID, msg, err)
		}
	})
}

// LeaveSpan reads "20 Oct → 22 Oct 2026 (3 working days)", or for a half day
// "20 Oct 2026, first half 08:00–12:30 (½ day)".
func LeaveSpan(lr models.LeaveRequest) string {
	if lr.IsHalfDay() {
		half := "first half"
		if lr.DayType == models.LeaveSecondHalf {
			half = "second half"
		}
		if lr.HalfFrom != nil && lr.HalfTo != nil {
			half += fmt.Sprintf(" %s–%s", *lr.HalfFrom, *lr.HalfTo)
		}
		return fmt.Sprintf("%s, %s (½ day)", lr.StartDate.Format("02 Jan 2006"), half)
	}
	days := fmt.Sprintf("%g working day", lr.Days)
	if lr.Days != 1 {
		days += "s"
	}
	if lr.StartDate.Equal(lr.EndDate) {
		return fmt.Sprintf("%s (%s)", lr.StartDate.Format("02 Jan 2006"), days)
	}
	return fmt.Sprintf("%s → %s (%s)", lr.StartDate.Format("02 Jan"), lr.EndDate.Format("02 Jan 2006"), days)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
