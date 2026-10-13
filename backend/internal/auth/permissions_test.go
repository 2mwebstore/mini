package auth

import (
	"testing"

	"attendance/backend/internal/models"
)

func emp(id uint64, role uint8, manager *uint64) *models.Employee {
	return &models.Employee{ID: id, RoleID: role, ManagerID: manager, Status: models.EmployeeActive}
}

func TestVisibilityAndReview(t *testing.T) {
	mgrID := uint64(10)
	staffA := emp(1, models.RoleStaff, &mgrID)
	staffB := emp(2, models.RoleStaff, nil)
	manager := &Actor{Employee: *emp(10, models.RoleManager, nil)}
	hr := &Actor{Employee: *emp(20, models.RoleHRAdmin, nil)}
	super := &Actor{Employee: *emp(30, models.RoleSuperAdmin, nil)}
	staff := &Actor{Employee: *staffA}

	// Staff: own data only (business rule 8).
	if !staff.CanView(staffA) || staff.CanView(staffB) {
		t.Error("staff must see only their own data")
	}
	if staff.IsReviewer() || staff.CanReview(staffB) {
		t.Error("staff must not review")
	}
	// Manager: assigned team only (rule 9).
	if !manager.CanView(staffA) || manager.CanView(staffB) {
		t.Error("manager must see only their team")
	}
	if !manager.CanReview(staffA) || manager.CanReview(staffB) {
		t.Error("manager must review only their team")
	}
	// Admins: everyone (rule 10).
	if !hr.CanView(staffB) || !hr.CanReview(staffB) {
		t.Error("HR must see and review all staff")
	}
	// No self-review except super admin.
	if hr.CanReview(&hr.Employee) || manager.CanReview(&manager.Employee) {
		t.Error("self-review must be blocked")
	}
	if !super.CanReview(&super.Employee) {
		t.Error("super admin may confirm own records")
	}
	// Only super admins hold settings/audit/admin-role permissions.
	if hr.Has(PermSettingsEdit) || hr.Has(PermAuditView) || hr.Has(PermAssignAdminRoles) {
		t.Error("HR must not edit settings, read audit logs or grant admin roles")
	}
	if !super.Has(PermAuditView) || manager.Has(PermStaffManage) || staff.Has(PermReportExport) {
		t.Error("permission matrix mismatch")
	}
	if ids := staff.ScopeEmployeeIDs(nil, nil); len(ids) != 1 || ids[0] != staffA.ID {
		t.Error("staff scope must be self")
	}
	if hr.ScopeEmployeeIDs(nil, nil) != nil {
		t.Error("admin scope must be unrestricted")
	}
}

func TestBranchAdminScope(t *testing.T) {
	branchA, branchB := uint64(1), uint64(2)
	inBranch := func(id uint64, role uint8, branch *uint64) *models.Employee {
		e := emp(id, role, nil)
		e.BranchID = branch
		return e
	}
	admin := &Actor{Employee: *inBranch(50, models.RoleBranchAdmin, &branchA)}
	staffA := inBranch(51, models.RoleStaff, &branchA)
	managerA := inBranch(52, models.RoleManager, &branchA)
	staffB := inBranch(53, models.RoleStaff, &branchB)
	hrNoBranch := inBranch(54, models.RoleHRAdmin, nil)

	for _, target := range []*models.Employee{staffA, managerA} {
		if !admin.CanView(target) || !admin.CanReview(target) || !admin.CanManage(target) {
			t.Errorf("branch admin must view/review/manage %d in their branch", target.ID)
		}
	}
	if admin.CanView(staffB) || admin.CanReview(staffB) || admin.CanManage(staffB) {
		t.Error("branch admin must not reach another branch")
	}
	if admin.CanView(hrNoBranch) || admin.CanManage(hrNoBranch) {
		t.Error("company-wide admins are outside any branch admin's scope")
	}
	if admin.CanReview(&admin.Employee) {
		t.Error("branch admins do not review themselves")
	}
	if !admin.IsReviewer() || !admin.BranchOnly() || admin.Has(PermBranchesManage) || admin.Has(PermAssignAdminRoles) {
		t.Error("branch admin: reviewer limited to one branch, cannot manage branches or grant admin roles")
	}
	hr := &Actor{Employee: *hrNoBranch}
	if !hr.CanManage(staffB) || hr.BranchOnly() || hr.Has(PermBranchesManage) {
		t.Error("HR admin manages every branch but does not assign branch admins")
	}
	owner := &Actor{Employee: *emp(60, models.RoleSuperAdmin, nil)}
	if !owner.Has(PermBranchesManage) {
		t.Error("only the owner manages branches")
	}
	if ids := admin.ScopeEmployeeIDs(nil, []uint64{51, 52}); len(ids) != 3 {
		t.Errorf("branch scope = self + branch, got %v", ids)
	}
	if RoleCode(models.RoleBranchAdmin) != "branch_admin" {
		t.Error("role code")
	}
}
