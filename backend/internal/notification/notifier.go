// Package notification sends Telegram notifications to reviewers, employees and
// branch groups, and records each delivery in the notifications table. Sends happen
// after the business transaction commits and never fail it. Each branch has its own
// bot (set on the branch page): messages about a person go through their branch's
// bot, and are skipped while the branch has none.
package notification

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"attendance/backend/internal/auth"
	"attendance/backend/internal/bots"
	"attendance/backend/internal/models"
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
	db    *gorm.DB
	tg    *telegram.Client // its token is chosen per message (the branch's bot)
	bots  *bots.Registry
	async bool
}

func New(db *gorm.DB, tg *telegram.Client, b *bots.Registry) *Notifier {
	return &Notifier{db: db, tg: tg, bots: b, async: true}
}

// Sync makes sends blocking (tests).
func (n *Notifier) Sync() *Notifier { n.async = false; return n }

// sender sends through one branch's bot.
func (n *Notifier) sender(b *bots.Bot) Sender { return n.tg.WithToken(b.Token) }

// branchOfPerson is a person's branch id (nil: none, or not found).
func (n *Notifier) branchOfPerson(ctx context.Context, employeeID uint64) *uint64 {
	var e models.Employee
	if n.db.WithContext(ctx).Select("id", "branch_id").First(&e, employeeID).Error != nil {
		return nil
	}
	return e.BranchID
}

// openApp returns a one-button keyboard that opens the Mini App at page (e.g.
// "team"), or nil if no Mini App URL is set. Telegram only allows web_app buttons
// in private chats (positive chat IDs); groups get a t.me direct link, which needs
// the bot's Main Mini App to be set in @BotFather.
func openApp(bot *bots.Bot, chatID int64, text, page string) *telegram.InlineKeyboard {
	if bot.MiniAppURL == "" {
		return nil
	}
	b := telegram.InlineButton{Text: text}
	switch {
	case chatID > 0:
		b.WebApp = &telegram.WebAppInfo{URL: strings.TrimSuffix(bot.MiniAppURL, "/") + "/" + page}
	case bot.Username != "":
		b.URL = "https://t.me/" + bot.Username + "?startapp=" + page
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
//  1. the branch's review chat, else
//  2. DMs to the employee's manager and branch admin, else (none, or no Telegram)
//  3. DMs to everyone who reviews the branch (company-wide admins whose access
//     includes it, admins assigned to it).
func (n *Notifier) reviewers(ctx context.Context, bot *bots.Bot, emp *models.Employee) []recipient {
	if bot.ReviewChatID != 0 {
		return []recipient{{chatID: bot.ReviewChatID}}
	}
	ids, _ := auth.ManagersOf(ctx, n.db, emp.ID) // all of their managers
	var br models.Branch
	if n.db.WithContext(ctx).Select("id", "admin_employee_id").First(&br, bot.BranchID).Error == nil && br.AdminEmployeeID != nil {
		ids = append(ids, *br.AdminEmployeeID)
	}
	if len(ids) > 0 {
		if out := n.dms(ctx, emp.ID, "e.id IN ?", ids); len(out) > 0 {
			return out
		}
	}
	return n.branchReviewers(ctx, bot.BranchID, emp.ID)
}

// branchReviewers: people whose role reviews and whose access reaches the branch —
// company-wide reviewers with no branch limit or limited to include it, and
// branch-level reviewers of (or assigned to) it.
func (n *Notifier) branchReviewers(ctx context.Context, branchID, exclude uint64) []recipient {
	wide := intersect(auth.RolesWith(auth.PermAllReview), auth.RolesWith(auth.PermAllView))
	local := auth.RolesWith(auth.PermBranchReview)
	return n.dms(ctx, exclude, `(e.role_id IN ? AND (NOT EXISTS (SELECT 1 FROM user_branches ub WHERE ub.employee_id = e.id)
		OR EXISTS (SELECT 1 FROM user_branches ub WHERE ub.employee_id = e.id AND ub.branch_id = ?)))
		OR (e.role_id IN ? AND (e.branch_id = ? OR EXISTS (SELECT 1 FROM user_branches ub WHERE ub.employee_id = e.id AND ub.branch_id = ?)))`,
		wide, branchID, local, branchID, branchID)
}

// companyAdmins: company-wide admins who reach every branch (system alerts).
func (n *Notifier) companyAdmins(ctx context.Context, exclude uint64) []recipient {
	return n.dms(ctx, exclude, "e.role_id IN ? AND NOT EXISTS (SELECT 1 FROM user_branches ub WHERE ub.employee_id = e.id)", models.CompanyWideRoleIDs())
}

func intersect(a, b []int) []int {
	in := map[int]bool{}
	for _, x := range b {
		in[x] = true
	}
	out := []int{}
	for _, x := range a {
		if in[x] {
			out = append(out, x)
		}
	}
	return out
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
		bot := n.bots.ForBranch(ctx, ev.Employee.BranchID)
		if bot == nil {
			return
		}
		tg := n.sender(bot)
		caption := AttendanceReviewCaption(ev)
		typ := "attendance.submitted"
		fileID := ""
		for _, r := range n.reviewers(ctx, bot, &ev.Employee) {
			var kb *telegram.InlineKeyboard
			if ev.Pending {
				kb = openApp(bot, r.chatID, "✅ Review in app", "team")
			}
			var msg *telegram.Message
			var err error
			if len(ev.PhotoData) == 0 { // check-ins have no photo unless one was sent
				msg, err = tg.SendMessage(ctx, r.chatID, caption, kb)
			} else if fileID == "" {
				// The first send uploads the image; Telegram's file_id is reused for the
				// other reviewers and saved on the photo row.
				msg, err = tg.SendPhotoData(ctx, r.chatID, ev.PhotoData, caption, kb)
				if p := msg.LargestPhoto(); err == nil && p != nil {
					fileID = p.FileID
					n.rememberPhotoFile(ctx, ev.PhotoID, p)
				}
			} else {
				msg, err = tg.SendPhoto(ctx, r.chatID, fileID, caption, kb)
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
		if ev.Attendance.CheckInReason != "" {
			fmt.Fprintf(&b, "📝 Reason: %s\n", telegram.Esc(ev.Attendance.CheckInReason))
		}
	} else {
		fmt.Fprintf(&b, "🔴 Check-Out: %s\n", timeutil.FormatClock(ev.Attendance.CheckOutAt, ev.Loc))
		fmt.Fprintf(&b, "⏱️ Working Time: %s\n", timeutil.FormatMinutes(ev.Attendance.WorkingMinutes))
		if ev.Attendance.EarlyLeaveMinutes > 0 {
			fmt.Fprintf(&b, "⚠️ Early leave: %d min\n", ev.Attendance.EarlyLeaveMinutes)
		}
		if ev.Attendance.CheckOutReason != "" {
			fmt.Fprintf(&b, "📝 Reason: %s\n", telegram.Esc(ev.Attendance.CheckOutReason))
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
		branchID := n.branchOfPerson(ctx, att.EmployeeID)
		bot := n.bots.ForBranch(ctx, branchID)
		if bot == nil {
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
		msg, err := n.sender(bot).SendMessage(ctx, chat, text, openApp(bot, chat, "📱 Open app", ""))
		n.record(ctx, recipient{chatID: chat, employeeID: &att.EmployeeID}, "attendance.reviewed", "attendance", att.ID, msg, err)
	})
}

// ---------- leave ----------

func (n *Notifier) LeaveSubmitted(lr models.LeaveRequest, emp models.Employee, typeName string) {
	n.run(func(ctx context.Context) {
		bot := n.bots.ForBranch(ctx, emp.BranchID)
		if bot == nil {
			return
		}
		text := fmt.Sprintf("🏖️ <b>NEW LEAVE REQUEST</b>\n👤 %s (%s)\n📅 %s\n📌 %s\n📝 %s\nStatus: 🟡 Pending",
			telegram.Esc(emp.FullName), telegram.Esc(emp.EmployeeCode), LeaveSpan(lr),
			telegram.Esc(typeName), telegram.Esc(lr.Reason))
		for _, r := range n.reviewers(ctx, bot, &emp) {
			msg, err := n.sender(bot).SendMessage(ctx, r.chatID, text, openApp(bot, r.chatID, "✅ Review in app", "team"))
			n.record(ctx, r, "leave.submitted", "leave", lr.ID, msg, err)
		}
	})
}

func (n *Notifier) LeaveReviewed(lr models.LeaveRequest) {
	n.run(func(ctx context.Context) {
		bot := n.bots.ForBranch(ctx, n.branchOfPerson(ctx, lr.EmployeeID))
		if bot == nil {
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
		msg, err := n.sender(bot).SendMessage(ctx, chat, text, openApp(bot, chat, "📱 My leave", "leave"))
		n.record(ctx, recipient{chatID: chat, employeeID: &lr.EmployeeID}, "leave.reviewed", "leave", lr.ID, msg, err)
	})
}

// ---------- broadcast ----------

// BranchGroup posts to a branch's own group (its daily report), if it has one.
func (n *Notifier) BranchGroup(b models.Branch, typ, text string) {
	n.run(func(ctx context.Context) {
		bot := n.bots.ForBranch(ctx, &b.ID)
		if bot == nil || bot.GroupID == 0 {
			return
		}
		msg, err := n.sender(bot).SendMessage(ctx, bot.GroupID, text, nil)
		n.record(ctx, recipient{chatID: bot.GroupID}, typ, "branch", b.ID, msg, err)
	})
}

// Admins sends a system alert (e.g. backups) by DM to the company-wide admins who
// reach every branch, through any branch's bot (they must have started it).
func (n *Notifier) Admins(typ, text string) {
	n.run(func(ctx context.Context) {
		bot := n.bots.Any(ctx, nil)
		if bot == nil {
			return
		}
		for _, r := range n.companyAdmins(ctx, 0) {
			msg, err := n.sender(bot).SendMessage(ctx, r.chatID, text, nil)
			n.record(ctx, r, typ, "", 0, msg, err)
		}
	})
}

// BranchAdmins sends an alert about one branch to its review chat, or by DM to its
// admin — or, without one, to everyone who reviews the branch.
func (n *Notifier) BranchAdmins(b models.Branch, typ, text string) {
	n.run(func(ctx context.Context) {
		bot := n.bots.ForBranch(ctx, &b.ID)
		if bot == nil {
			return
		}
		var to []recipient
		switch {
		case bot.ReviewChatID != 0:
			to = []recipient{{chatID: bot.ReviewChatID}}
		case b.AdminEmployeeID != nil:
			to = n.dms(ctx, 0, "e.id = ?", *b.AdminEmployeeID)
		}
		if len(to) == 0 {
			to = n.branchReviewers(ctx, b.ID, 0)
		}
		for _, r := range to {
			msg, err := n.sender(bot).SendMessage(ctx, r.chatID, text, nil)
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
