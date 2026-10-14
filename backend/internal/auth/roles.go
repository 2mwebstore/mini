package auth

import (
	"context"
	"sort"
	"sync"
	"time"

	"attendance/backend/internal/models"

	"gorm.io/gorm"
)

// PermissionInfo describes a permission for the role editor.
type PermissionInfo struct {
	Code        Permission `json:"code"`
	Group       string     `json:"group"`
	Label       string     `json:"label"`
	Description string     `json:"description"`
}

// Catalog lists every permission, in the order the role editor shows them.
var Catalog = []PermissionInfo{
	{PermAttendanceSelf, "Own", "Check in / out", "Use the Mini App to check in and out and see their own attendance."},
	{PermLeaveSelf, "Own", "Request leave", "Request leave and see their own leave and balances."},
	{PermTeamView, "Team", "View team", "See the attendance and leave of people they manage."},
	{PermTeamReview, "Team", "Review team", "Confirm check-ins and approve leave of people they manage."},
	{PermBranchView, "Branch", "View own branch", "See everyone in their own branch (and branches assigned to them)."},
	{PermBranchReview, "Branch", "Review own branch", "Confirm check-ins and approve leave in their branch."},
	{PermBranchSettings, "Branch", "Branch settings", "Edit their branches' office location, GPS rules and Telegram bot and chats."},
	{PermBranchesCreate, "Branch", "Create branches", "Create new branches (added to their own branch access) and rename or close the branches they reach."},
	{PermUsersBranch, "Branch", "Admin accounts of their branches", "Create, edit and remove admin panel logins limited to their branches, with roles that have no more powers than their own."},
	{PermAllView, "Company", "View all branches", "See everyone — limited to the branches assigned to the user, if any."},
	{PermAllReview, "Company", "Review all branches", "Confirm check-ins and approve leave for everyone they can see."},
	{PermAttendanceEdit, "Records", "Edit attendance", "Add and correct attendance records (audited, reason required)."},
	{PermReportExport, "Records", "Export reports", "Download Excel / CSV reports and rosters."},
	{PermStaffManage, "Records", "Manage staff", "Add and edit employees, departments, positions, the roster and special schedules of their branches."},
	{PermSchedulesManage, "Records", "Manage shifts & holidays", "Create and edit their branches' shifts (hours, grace, work days) and holidays."},
	{PermLeaveManage, "Records", "Manage leave policy", "Edit their branches' leave types, allowances and employees' leave balances."},
	{PermSettingsView, "System", "View settings", "See company settings."},
	{PermSettingsEdit, "System", "Edit settings", "Change company settings (name, time zone, review, scheduled jobs)."},
	{PermAuditView, "System", "Audit log", "Read the audit log."},
	{PermBranchesManage, "System", "Manage all branches", "Create, edit and close any branch, and assign branch admins."},
	{PermUsersManage, "System", "Manage system users", "Create panel logins, reset passwords, assign roles and branch access, edit roles."},
	{PermAssignAdminRoles, "System", "Grant admin roles", "Give people roles with admin powers."},
}

// roleInfo is a role as the API sees it.
type roleInfo struct {
	id     uint8
	code   string
	name   string
	system bool
	perms  map[Permission]bool
}

var (
	regMu sync.RWMutex
	reg   = builtinRoles()
	regAt time.Time // when the registry was last read from the database
)

// rolesTTL: other API replicas pick up role edits within this time.
const rolesTTL = 30 * time.Second

func init() {
	// models has no database access of its own: it asks the registry.
	models.CompanyWideRoleIDs = CompanyWideRoleIDs
	models.IsCompanyAdmin = IsCompanyWide
	models.IsAdminRole = IsAdminRole
}

// builtinRoles: what the built-in roles may do before the database is read (tests, CLI).
func builtinRoles() map[uint8]*roleInfo {
	set := func(ps ...Permission) map[Permission]bool {
		m := map[Permission]bool{}
		for _, p := range ps {
			m[p] = true
		}
		return m
	}
	self := []Permission{PermAttendanceSelf, PermLeaveSelf}
	team := append(append([]Permission{}, self...), PermTeamView, PermTeamReview)
	m := map[uint8]*roleInfo{
		models.RoleStaff:   {id: models.RoleStaff, code: "staff", name: "Staff", system: true, perms: set(self...)},
		models.RoleManager: {id: models.RoleManager, code: "manager", name: "Manager", system: true, perms: set(append(team, PermReportExport)...)},
		models.RoleHRAdmin: {id: models.RoleHRAdmin, code: "hr_admin", name: "HR / Admin", system: true, perms: set(append(team, PermAllView, PermAllReview,
			PermAttendanceEdit, PermStaffManage, PermReportExport, PermSettingsView, PermSchedulesManage, PermLeaveManage)...)},
		models.RoleBranchAdmin: {id: models.RoleBranchAdmin, code: "branch_admin", name: "Branch Admin", system: true, perms: set(append(team, PermBranchView,
			PermBranchReview, PermAttendanceEdit, PermStaffManage, PermReportExport, PermBranchSettings, PermSchedulesManage, PermLeaveManage)...)},
		models.RoleSuperAdmin: {id: models.RoleSuperAdmin, code: "super_admin", name: "Super Admin", system: true, perms: map[Permission]bool{}},
	}
	for _, p := range Catalog {
		m[models.RoleSuperAdmin].perms[p.Code] = true
	}
	return m
}

// LoadRoles reads roles and their permissions from the database into the registry.
func LoadRoles(ctx context.Context, db *gorm.DB) error {
	var roles []models.Role
	if err := db.WithContext(ctx).Find(&roles).Error; err != nil {
		return err
	}
	var grants []models.RolePermission
	if err := db.WithContext(ctx).Find(&grants).Error; err != nil {
		return err
	}
	m := map[uint8]*roleInfo{}
	for _, r := range roles {
		m[r.ID] = &roleInfo{id: r.ID, code: r.Code, name: r.Name, system: r.IsSystem, perms: map[Permission]bool{}}
	}
	for _, g := range grants {
		if r := m[g.RoleID]; r != nil {
			r.perms[Permission(g.Permission)] = true
		}
	}
	if owner := m[models.RoleSuperAdmin]; owner != nil { // the owner can never be locked out
		for _, p := range Catalog {
			owner.perms[p.Code] = true
		}
	}
	regMu.Lock()
	reg, regAt = m, time.Now()
	regMu.Unlock()
	return nil
}

// refreshRoles reloads the registry when it is older than rolesTTL.
func refreshRoles(ctx context.Context, db *gorm.DB) {
	regMu.RLock()
	stale := time.Since(regAt) > rolesTTL
	regMu.RUnlock()
	if stale {
		_ = LoadRoles(ctx, db)
	}
}

func roleOf(id uint8) *roleInfo {
	regMu.RLock()
	defer regMu.RUnlock()
	return reg[id]
}

// RoleHas reports whether role id grants p.
func RoleHas(id uint8, p Permission) bool {
	r := roleOf(id)
	return r != nil && r.perms[p]
}

func RoleCode(id uint8) string {
	if r := roleOf(id); r != nil {
		return r.code
	}
	return ""
}

func RoleIDFromCode(code string) (uint8, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	for id, r := range reg {
		if r.code == code {
			return id, true
		}
	}
	return 0, false
}

// PermissionsFor lists a role's permissions in catalog order.
func PermissionsFor(id uint8) []Permission {
	out := []Permission{}
	for _, p := range Catalog {
		if RoleHas(id, p.Code) {
			out = append(out, p.Code)
		}
	}
	return out
}

// IsCompanyWide: the role sees all branches (unless the user is limited to some).
// Such people belong to no branch.
func IsCompanyWide(id uint8) bool { return RoleHas(id, PermAllView) }

// CompanyWideRoleIDs lists the company-wide roles (for SQL "IN ?"; []int, not []uint8).
func CompanyWideRoleIDs() []int {
	regMu.RLock()
	defer regMu.RUnlock()
	ids := []int{}
	for id, r := range reg {
		if r.perms[PermAllView] {
			ids = append(ids, int(id))
		}
	}
	sort.Ints(ids)
	if len(ids) == 0 {
		ids = append(ids, int(models.RoleSuperAdmin))
	}
	return ids
}

// RolesWith lists the roles that have p (for SQL "IN ?"; []int, not []uint8).
func RolesWith(p Permission) []int {
	regMu.RLock()
	defer regMu.RUnlock()
	ids := []int{}
	for id, r := range reg {
		if r.perms[p] {
			ids = append(ids, int(id))
		}
	}
	sort.Ints(ids)
	return ids
}

// basic permissions anyone may hand out (a manager is not an admin).
var basic = map[Permission]bool{PermAttendanceSelf: true, PermLeaveSelf: true, PermTeamView: true, PermTeamReview: true, PermReportExport: true}

// RoleWithin: the role grants nothing the actor doesn't have (own check-in and leave
// don't count: system users never use them).
func RoleWithin(roleID uint8, a *Actor) bool {
	r := roleOf(roleID)
	if r == nil {
		return false
	}
	for p := range r.perms {
		if p != PermAttendanceSelf && p != PermLeaveSelf && !a.Has(p) {
			return false
		}
	}
	return true
}

// IsAdminRole: the role has powers beyond a manager's, so only someone allowed to
// grant admin roles may give it (or edit people who have it).
func IsAdminRole(id uint8) bool {
	r := roleOf(id)
	if r == nil {
		return false
	}
	for p := range r.perms {
		if !basic[p] {
			return true
		}
	}
	return id == models.RoleSuperAdmin || id == models.RoleBranchAdmin
}

// PanelAccess: the role can use the admin panel (anything beyond one's own records).
func PanelAccess(id uint8) bool {
	r := roleOf(id)
	if r == nil {
		return false
	}
	for p := range r.perms {
		if p != PermAttendanceSelf && p != PermLeaveSelf {
			return true
		}
	}
	return false
}
