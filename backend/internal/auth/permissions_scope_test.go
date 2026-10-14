package auth

import (
	"testing"

	"attendance/backend/internal/models"
)

func TestBranchScope(t *testing.T) {
	b := func(id uint64) *uint64 { return &id }
	sr, bb, main := uint64(2), uint64(3), uint64(1)
	staffIn := func(branch uint64) *models.Employee {
		return &models.Employee{ID: 100 + branch, RoleID: models.RoleStaff, BranchID: b(branch)}
	}
	hrNoBranch := &models.Employee{ID: 50, RoleID: models.RoleHRAdmin}

	hr := &Actor{Employee: models.Employee{ID: 1, RoleID: models.RoleHRAdmin}}
	if all, _ := hr.BranchScope(); !all || !hr.CanManage(staffIn(sr)) || !hr.CanView(hrNoBranch) {
		t.Fatal("HR without assigned branches reaches every branch and company-wide people")
	}
	limited := &Actor{Employee: models.Employee{ID: 2, RoleID: models.RoleHRAdmin}, Branches: []uint64{sr, bb}}
	if all, ids := limited.BranchScope(); all || len(ids) != 2 {
		t.Fatalf("assigned branches limit HR: %v %v", all, ids)
	}
	if !limited.CanManage(staffIn(sr)) || limited.CanView(staffIn(main)) || limited.CanView(hrNoBranch) {
		t.Fatal("limited HR: Siem Reap yes, Main no, company-wide people no")
	}
	if !limited.BranchOnly() || !limited.InBranch(bb) || limited.InBranch(main) {
		t.Fatal("limited HR is branch-only")
	}
	ba := &Actor{Employee: models.Employee{ID: 3, RoleID: models.RoleBranchAdmin, BranchID: b(main)}, Branches: []uint64{sr}}
	if _, ids := ba.BranchScope(); len(ids) != 2 || !ba.CanManage(staffIn(sr)) || !ba.CanManage(staffIn(main)) || ba.CanView(staffIn(bb)) {
		t.Fatalf("a branch admin reaches their branch plus assigned ones: %v", ids)
	}
	mgr := &Actor{Employee: models.Employee{ID: 4, RoleID: models.RoleManager, BranchID: b(main)}, Branches: []uint64{sr}}
	if all, ids := mgr.BranchScope(); all || len(ids) != 0 || mgr.CanView(staffIn(sr)) {
		t.Fatal("managers reach their team, not branches")
	}
	if !PanelAccess(models.RoleManager) || PanelAccess(models.RoleStaff) {
		t.Fatal("panel access: managers yes, staff no")
	}
	if IsAdminRole(models.RoleManager) || !IsAdminRole(models.RoleHRAdmin) || !IsAdminRole(models.RoleBranchAdmin) {
		t.Fatal("admin roles")
	}
}
