package demo

import (
	"context"
	"fmt"

	"attendance/backend/internal/audit"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/models"

	"gorm.io/gorm"
)

// Cleared counts what Clear removed.
type Cleared struct {
	Branches, Employees, SystemUsers, Attendance, LeaveRequests int64
	Owners                                                      []string // kept: "Name <email>"
}

// clearTables lists every table of business data, children first. Kept: the
// schema and migrations, company roles and permissions (a branch's own roles go
// with it), company settings, the owner
// account(s) and the audit log (append-only by design).
var clearTables = []string{
	"attendance_confirmations", "attendance_locations", "attendance_photos", "attendance",
	"leave_requests", "leave_balances", "roster_shifts", "schedule_overrides", "notifications",
	"user_branches", "holidays", "work_schedules", "leave_types", "departments", "positions",
}

// Clear wipes all business data — branches, employees, system users other than
// the owner(s), attendance, leave, schedules and every branch's setup — so the
// system can be set up again from scratch (or filled with seed-demo). Photo files
// in storage are not removed. Never run it on a live database.
func Clear(ctx context.Context, db *gorm.DB) (*Cleared, error) {
	out := &Cleared{}
	var owners []models.Employee
	if err := db.WithContext(ctx).Where("kind = ? AND role_id = ? AND deleted_at IS NULL", models.KindSystem, models.RoleSuperAdmin).Find(&owners).Error; err != nil {
		return nil, err
	}
	keep := []uint64{0} // never empty for "NOT IN"
	for _, o := range owners {
		keep = append(keep, o.ID)
		var u models.User
		email := "no login"
		if db.WithContext(ctx).Where("employee_id = ?", o.ID).First(&u).Error == nil {
			email = u.Email
		}
		out.Owners = append(out.Owners, fmt.Sprintf("%s <%s>", o.FullName, email))
	}
	// One connection throughout: foreign key checks are switched off for this
	// session only, and always switched back on before it returns to the pool.
	err := db.WithContext(ctx).Connection(func(conn *gorm.DB) error {
		if err := conn.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
			return err
		}
		defer conn.Exec("SET FOREIGN_KEY_CHECKS = 1")
		return conn.Transaction(func(tx *gorm.DB) error { return clearAll(ctx, tx, keep, out) })
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func clearAll(ctx context.Context, tx *gorm.DB, keep []uint64, out *Cleared) error {
	count := func(table, where string, args ...any) int64 {
		var n int64
		tx.Table(table).Where(where, args...).Count(&n)
		return n
	}
	out.Branches = count("branches", "1 = 1")
	out.Employees = count("employees", "kind = ? AND id NOT IN ?", models.KindStaff, keep)
	out.SystemUsers = count("employees", "kind = ? AND id NOT IN ?", models.KindSystem, keep)
	out.Attendance = count("attendance", "1 = 1")
	out.LeaveRequests = count("leave_requests", "1 = 1")
	for _, t := range clearTables {
		if err := tx.Exec("DELETE FROM " + t).Error; err != nil {
			return fmt.Errorf("clear %s: %w", t, err)
		}
	}
	for _, q := range []string{
		"DELETE FROM users WHERE employee_id NOT IN ?",
		"DELETE FROM telegram_accounts WHERE employee_id NOT IN ?",
		"DELETE FROM employees WHERE id NOT IN ?",
	} {
		if err := tx.Exec(q, keep).Error; err != nil {
			return err
		}
	}
	if err := tx.Exec("UPDATE employees SET branch_id = NULL, department_id = NULL, position_id = NULL, work_schedule_id = NULL, manager_id = NULL").Error; err != nil {
		return err
	}
	// Roles a branch made go with it (nobody is left who has one).
	for _, q := range []string{
		"DELETE FROM role_permissions WHERE role_id IN (SELECT id FROM roles WHERE branch_id IS NOT NULL)",
		"DELETE FROM roles WHERE branch_id IS NOT NULL",
		"DELETE FROM branches",
	} {
		if err := tx.Exec(q).Error; err != nil {
			return err
		}
	}
	cli := &auth.Actor{Employee: models.Employee{RoleID: models.RoleSuperAdmin, FullName: "CLI"}, Channel: auth.ChannelCLI}
	return audit.Write(ctx, tx, cli, audit.Entry{Action: "data.clear", EntityType: "system", Details: map[string]any{
		"branches": out.Branches, "employees": out.Employees, "system_users": out.SystemUsers, "attendance": out.Attendance,
		"leave_requests": out.LeaveRequests, "owners_kept": out.Owners}})
}
