package auth

import (
	"slices"

	"attendance/backend/internal/models"

	"gorm.io/gorm"
)

type Permission string

const (
	PermAttendanceSelf   Permission = "attendance.self"    // check in/out, view own
	PermLeaveSelf        Permission = "leave.self"         // request / view own leave
	PermTeamView         Permission = "team.view"          // view assigned team records
	PermTeamReview       Permission = "team.review"        // confirm team attendance, approve team leave
	PermAllView          Permission = "all.view"           // view every employee (in the user's assigned branches, if any)
	PermAllReview        Permission = "all.review"         // confirm any attendance, approve any leave (same scope)
	PermAttendanceEdit   Permission = "attendance.edit"    // manual create / edit attendance
	PermStaffManage      Permission = "staff.manage"       // register / edit employees, departments, positions, roster
	PermReportExport     Permission = "report.export"      // download Excel / CSV
	PermSettingsView     Permission = "settings.view"      //
	PermSettingsEdit     Permission = "settings.edit"      //
	PermAuditView        Permission = "audit.view"         //
	PermAssignAdminRoles Permission = "roles.assign_admin" // grant roles with admin powers
	PermBranchView       Permission = "branch.view"        // view everyone in own (and assigned) branches
	PermBranchReview     Permission = "branch.review"      // confirm attendance / approve leave in those branches
	PermBranchSettings   Permission = "branch.settings"    // own branch's office location, GPS rules and Telegram bot
	PermBranchesManage   Permission = "branches.manage"    // every branch: create, edit, close, assign branch admins (owner)
	PermBranchesCreate   Permission = "branches.create"    // create branches (added to the creator's branch access) and edit those in reach
	PermSchedulesManage  Permission = "schedules.manage"   // shifts and holidays of the branches in reach
	PermLeaveManage      Permission = "leave.manage"       // leave types, allowances, balances
	PermUsersManage      Permission = "users.manage"       // system users, roles, branch access
	PermUsersBranch      Permission = "users.branch"       // admin accounts of their own branches (no more powers than their own)
)

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
	// Branches assigned to this system user (user_branches). For a company-wide
	// role, none means every branch; a branch-level role also reaches its own branch.
	Branches []uint64
}

func (a *Actor) ID() uint64 { return a.Employee.ID }

// Has: the actor's role grants p. System users are not staff: they never check in
// or take leave, whatever their role says.
func (a *Actor) Has(p Permission) bool {
	if a.Employee.IsSystemUser() && (p == PermAttendanceSelf || p == PermLeaveSelf) {
		return false
	}
	return RoleHas(a.Employee.RoleID, p)
}

func (a *Actor) IsAdmin() bool { return a.Has(PermAllView) }

func (a *Actor) IsReviewer() bool {
	return a.Has(PermAllReview) || a.Has(PermTeamReview) || a.Has(PermBranchReview)
}

// PanelAccess: may sign in to the admin panel.
func (a *Actor) PanelAccess() bool { return PanelAccess(a.Employee.RoleID) }

// BranchScope is where the actor's company/branch powers reach: every branch
// (all), or the listed branches. People with neither power see no branch.
func (a *Actor) BranchScope() (all bool, ids []uint64) {
	switch {
	case a.Has(PermAllView):
		return len(a.Branches) == 0, a.Branches
	case a.Has(PermBranchView):
		ids = slices.Clone(a.Branches)
		if b := a.Employee.BranchID; b != nil && !slices.Contains(ids, *b) {
			ids = append(ids, *b)
		}
		return false, ids
	}
	return false, nil
}

// Reach is where the actor works: every branch (all), or their branch access plus
// their own branch. Setup lists, branches and staff are limited to it.
func (a *Actor) Reach() (all bool, ids []uint64) {
	all, ids = a.BranchScope()
	if all {
		return true, nil
	}
	ids = slices.Clone(ids)
	if b := a.Employee.BranchID; b != nil && !slices.Contains(ids, *b) {
		ids = append(ids, *b)
	}
	return false, ids
}

// ManagesUsers: the actor manages admin panel accounts — all of them (users.manage)
// or those of their own branches (users.branch).
func (a *Actor) ManagesUsers() bool { return a.Has(PermUsersManage) || a.Has(PermUsersBranch) }

// ManagesUser: the actor may see and change this admin account (its person record
// and branch access). users.manage: any. users.branch: an account that works only in
// the actor's branches and has no more powers than the actor — never the owner, a
// company-wide role, or an account with no branch at all.
func (a *Actor) ManagesUser(target *models.Employee, branches []uint64) bool {
	if a.Has(PermUsersManage) {
		return true
	}
	if !a.Has(PermUsersBranch) || target.RoleID == models.RoleSuperAdmin || IsCompanyWide(target.RoleID) || !RoleWithin(target.RoleID, a) {
		return false
	}
	reach := slices.Clone(branches)
	if target.BranchID != nil {
		reach = append(reach, *target.BranchID)
	}
	if len(reach) == 0 {
		return false
	}
	for _, b := range reach {
		if !a.Reaches(b) {
			return false
		}
	}
	return true
}

// CanGrant: the actor may give this admin role. users.manage needs "Grant admin
// roles"; someone limited to their branches' accounts may give a branch-level role
// that grants nothing they don't have themselves.
func (a *Actor) CanGrant(roleID uint8) bool {
	if a.Has(PermUsersManage) {
		return a.Has(PermAssignAdminRoles)
	}
	if b := RoleBranch(roleID); b != nil && !a.Reaches(*b) {
		return false // another branch's role
	}
	return a.Has(PermUsersBranch) && roleID != models.RoleSuperAdmin && !IsCompanyWide(roleID) && RoleWithin(roleID, a)
}

// EditsRole: the actor may change this role — any but the owner's with users.manage;
// with users.branch, a role of their branches that has no more powers than their own.
func (a *Actor) EditsRole(roleID uint8) bool {
	if a.Has(PermUsersManage) {
		return roleID != models.RoleSuperAdmin
	}
	b := RoleBranch(roleID)
	return a.Has(PermUsersBranch) && b != nil && a.Reaches(*b) && RoleWithin(roleID, a)
}

// Reaches: the branch is within the actor's reach.
func (a *Actor) Reaches(branchID uint64) bool {
	all, ids := a.Reach()
	return all || slices.Contains(ids, branchID)
}

// InReach limits q to rows whose column (a branch id) is within the actor's reach.
func InReach(q *gorm.DB, a *Actor, column string) *gorm.DB {
	all, ids := a.Reach()
	if all {
		return q
	}
	if len(ids) == 0 {
		return q.Where("1 = 0")
	}
	return q.Where(column+" IN ?", ids)
}

// AllBranches: the actor's powers are not limited to some branches.
func (a *Actor) AllBranches() bool {
	all, _ := a.BranchScope()
	return all
}

// InBranch: the branch is within the actor's branch scope.
func (a *Actor) InBranch(branchID uint64) bool {
	all, ids := a.BranchScope()
	return all || slices.Contains(ids, branchID)
}

func (a *Actor) managesEmployee(target *models.Employee) bool {
	return target.ManagerID != nil && *target.ManagerID == a.Employee.ID
}

// inScope: target is reached by the actor's branch scope. Company-wide people
// (no branch) are only reached by someone whose scope is every branch.
func (a *Actor) inScope(target *models.Employee) bool {
	all, ids := a.BranchScope()
	if all {
		return true
	}
	return target.BranchID != nil && slices.Contains(ids, *target.BranchID) && !models.IsCompanyAdmin(target.RoleID)
}

// CanView: own data always; managers their team; branch / company powers their scope.
func (a *Actor) CanView(target *models.Employee) bool {
	if target.ID == a.Employee.ID {
		return true
	}
	if (a.Has(PermAllView) || a.Has(PermBranchView)) && a.inScope(target) {
		return true
	}
	return a.Has(PermTeamView) && a.managesEmployee(target)
}

// CanManage decides who may edit or deactivate target's employee record.
func (a *Actor) CanManage(target *models.Employee) bool {
	return a.Has(PermStaffManage) && (a.Has(PermAllView) || a.Has(PermBranchView)) && a.inScope(target)
}

// BranchOnly reports whether the actor's staff powers are limited to some branches.
func (a *Actor) BranchOnly() bool {
	return (a.Has(PermBranchView) || a.Has(PermAllView)) && !a.AllBranches()
}

// CanReview decides who may confirm attendance / approve leave for target.
// Nobody reviews their own records except a super admin (otherwise a company
// with a single admin could never get the admin's own attendance confirmed).
func (a *Actor) CanReview(target *models.Employee) bool {
	if target.ID == a.Employee.ID {
		return a.Employee.RoleID == models.RoleSuperAdmin
	}
	if a.Has(PermAllReview) && a.Has(PermAllView) && a.inScope(target) {
		return true
	}
	if a.Has(PermBranchReview) && a.inScope(target) {
		return true
	}
	return a.Has(PermTeamReview) && a.managesEmployee(target)
}

// ReviewScope limits a query that joins employees as "e" to the people whose
// attendance and leave the actor reviews (same rules as CanReview).
func ReviewScope(q *gorm.DB, a *Actor) *gorm.DB {
	all, ids := a.BranchScope()
	branchReview := (a.Has(PermAllReview) && a.Has(PermAllView)) || a.Has(PermBranchReview)
	switch {
	case branchReview && all:
	case branchReview && len(ids) > 0:
		q = q.Where("(e.manager_id = ? OR (e.branch_id IN ? AND e.role_id NOT IN ?))", a.ID(), ids, models.CompanyWideRoleIDs())
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
	if a.Has(PermAllView) && a.AllBranches() {
		return nil
	}
	ids := []uint64{a.Employee.ID}
	if a.Has(PermTeamView) {
		ids = append(ids, teamIDs...)
	}
	if a.Has(PermBranchView) || a.Has(PermAllView) {
		ids = append(ids, branchIDs...)
	}
	return ids
}
