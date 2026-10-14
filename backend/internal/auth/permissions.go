package auth

import (
	"attendance/backend/internal/models"

	"gorm.io/gorm"
)

type Permission string

const (
	PermAttendanceSelf   Permission = "attendance.self"    // check in/out, view own
	PermLeaveSelf        Permission = "leave.self"         // request / view own leave
	PermTeamView         Permission = "team.view"          // view assigned team records
	PermTeamReview       Permission = "team.review"        // confirm team attendance, approve team leave
	PermAllView          Permission = "all.view"           // view every employee
	PermAllReview        Permission = "all.review"         // confirm any attendance, approve any leave
	PermAttendanceEdit   Permission = "attendance.edit"    // manual create / edit attendance
	PermStaffManage      Permission = "staff.manage"       // register / edit employees
	PermReportExport     Permission = "report.export"      // download Excel / CSV
	PermSettingsView     Permission = "settings.view"      //
	PermSettingsEdit     Permission = "settings.edit"      //
	PermAuditView        Permission = "audit.view"         //
	PermAssignAdminRoles Permission = "roles.assign_admin" // grant hr_admin / super_admin
	PermBranchView       Permission = "branch.view"        // view everyone in own branch
	PermBranchReview     Permission = "branch.review"      // confirm attendance / approve leave in own branch
	PermBranchSettings   Permission = "branch.settings"    // own branch's office location and Telegram chats
	PermBranchesManage   Permission = "branches.manage"    // create branches, assign branch admins (owner)
	PermSchedulesManage  Permission = "schedules.manage"   // define shifts (company-wide)
)

var rolePermissions = map[uint8][]Permission{
	models.RoleStaff: {PermAttendanceSelf, PermLeaveSelf},
	models.RoleManager: {PermAttendanceSelf, PermLeaveSelf, PermTeamView, PermTeamReview,
		PermReportExport},
	models.RoleHRAdmin: {PermAttendanceSelf, PermLeaveSelf, PermTeamView, PermTeamReview,
		PermAllView, PermAllReview, PermAttendanceEdit, PermStaffManage, PermReportExport, PermSettingsView, PermSchedulesManage},
	models.RoleSuperAdmin: {PermAttendanceSelf, PermLeaveSelf, PermTeamView, PermTeamReview,
		PermAllView, PermAllReview, PermAttendanceEdit, PermStaffManage, PermReportExport,
		PermSettingsView, PermSettingsEdit, PermAuditView, PermAssignAdminRoles, PermBranchSettings, PermBranchesManage,
		PermSchedulesManage},
	// A branch admin has HR powers limited to the one branch they are assigned to.
	models.RoleBranchAdmin: {PermAttendanceSelf, PermLeaveSelf, PermTeamView, PermTeamReview,
		PermBranchView, PermBranchReview, PermAttendanceEdit, PermStaffManage, PermReportExport, PermBranchSettings},
}

var roleCodes = map[uint8]string{
	models.RoleSuperAdmin: "super_admin", models.RoleHRAdmin: "hr_admin", models.RoleBranchAdmin: "branch_admin",
	models.RoleManager: "manager", models.RoleStaff: "staff",
}

func RoleCode(id uint8) string { return roleCodes[id] }

func RoleIDFromCode(code string) (uint8, bool) {
	for id, c := range roleCodes {
		if c == code {
			return id, true
		}
	}
	return 0, false
}

func PermissionsFor(roleID uint8) []Permission { return rolePermissions[roleID] }

const (
	ChannelTelegram  = "TELEGRAM"
	ChannelMiniApp   = "MINIAPP"
	ChannelDashboard = "DASHBOARD"
	ChannelSystem    = "SYSTEM"
	ChannelCLI       = "CLI"
)

// Actor is the authenticated employee performing a request, from any channel.
// All authorization decisions are made here, in the API — never in a front end.
type Actor struct {
	Employee models.Employee
	Channel  string
	IP       string
}

func (a *Actor) ID() uint64 { return a.Employee.ID }

func (a *Actor) Has(p Permission) bool {
	for _, x := range rolePermissions[a.Employee.RoleID] {
		if x == p {
			return true
		}
	}
	return false
}

func (a *Actor) IsAdmin() bool { return a.Has(PermAllView) }

func (a *Actor) IsReviewer() bool {
	return a.Has(PermAllReview) || a.Has(PermTeamReview) || a.Has(PermBranchReview)
}

func (a *Actor) managesEmployee(target *models.Employee) bool {
	return target.ManagerID != nil && *target.ManagerID == a.Employee.ID
}

// inMyBranch: target works in the branch the actor belongs to (company-wide
// admins have no branch, so they are never inside a branch admin's scope).
func (a *Actor) inMyBranch(target *models.Employee) bool {
	return a.Employee.BranchID != nil && target.BranchID != nil && *a.Employee.BranchID == *target.BranchID &&
		!models.IsCompanyAdmin(target.RoleID)
}

// CanView: own data always; managers their team; branch admins their branch; admins everyone.
func (a *Actor) CanView(target *models.Employee) bool {
	if target.ID == a.Employee.ID {
		return true
	}
	if a.Has(PermAllView) {
		return true
	}
	if a.Has(PermBranchView) && a.inMyBranch(target) {
		return true
	}
	return a.Has(PermTeamView) && a.managesEmployee(target)
}

// CanManage decides who may edit or deactivate target's employee record.
func (a *Actor) CanManage(target *models.Employee) bool {
	if !a.Has(PermStaffManage) {
		return false
	}
	return a.Has(PermAllView) || (a.Has(PermBranchView) && a.inMyBranch(target))
}

// BranchOnly reports whether the actor's staff powers are limited to one branch.
func (a *Actor) BranchOnly() bool { return a.Has(PermBranchView) && !a.Has(PermAllView) }

// CanReview decides who may confirm attendance / approve leave for target.
// Nobody reviews their own records except a super admin (otherwise a company
// with a single admin could never get the admin's own attendance confirmed).
func (a *Actor) CanReview(target *models.Employee) bool {
	if target.ID == a.Employee.ID {
		return a.Employee.RoleID == models.RoleSuperAdmin
	}
	if a.Has(PermAllReview) {
		return true
	}
	if a.Has(PermBranchReview) && a.inMyBranch(target) {
		return true
	}
	return a.Has(PermTeamReview) && a.managesEmployee(target)
}

// ReviewScope limits a query that joins employees as "e" to the people whose
// attendance and leave the actor reviews (same rules as CanReview).
func ReviewScope(q *gorm.DB, a *Actor) *gorm.DB {
	switch {
	case a.Has(PermAllReview):
	case a.Has(PermBranchReview) && a.Employee.BranchID != nil:
		q = q.Where("(e.manager_id = ? OR (e.branch_id = ? AND e.role_id NOT IN ?))", a.ID(), *a.Employee.BranchID,
			models.CompanyAdminRoles)
	default:
		q = q.Where("e.manager_id = ?", a.ID())
	}
	if a.Employee.RoleID != models.RoleSuperAdmin {
		q = q.Where("e.id <> ?", a.ID()) // nobody reviews themselves except the owner
	}
	return q
}

// ScopeEmployeeIDs returns nil for "all employees", or the explicit set of IDs the actor may see.
func (a *Actor) ScopeEmployeeIDs(teamIDs, branchIDs []uint64) []uint64 {
	if a.Has(PermAllView) {
		return nil
	}
	ids := []uint64{a.Employee.ID}
	if a.Has(PermTeamView) {
		ids = append(ids, teamIDs...)
	}
	if a.Has(PermBranchView) {
		ids = append(ids, branchIDs...)
	}
	return ids
}
