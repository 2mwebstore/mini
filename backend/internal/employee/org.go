package employee

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/audit"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/models"

	mysqldrv "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// OrgItem is a department or position of a branch, with how many active employees have it.
type OrgItem struct {
	ID         uint64 `json:"id"`
	BranchID   uint64 `json:"branch_id"`
	BranchName string `json:"branch_name"`
	Name       string `json:"name"`
	Employees  int64  `json:"employees"`
}

// orgList describes one managed list (departments or positions).
type orgList struct {
	table, column, label string
	saveAction, delete   string
	newRow               func(branchID uint64, name string) any
}

var (
	departments = orgList{table: "departments", column: "department_id", label: "department", saveAction: audit.ActionDepartmentSave,
		delete: audit.ActionDepartmentDelete, newRow: func(b uint64, n string) any { return &models.Department{BranchID: b, Name: n} }}
	positions = orgList{table: "positions", column: "position_id", label: "position", saveAction: audit.ActionPositionSave,
		delete: audit.ActionPositionDelete, newRow: func(b uint64, n string) any { return &models.Position{BranchID: b, Name: n} }}
)

// DepartmentList / PositionList: the lists of the branches the actor reaches
// (branchID narrows to one).
func (s *Service) DepartmentList(ctx context.Context, actor *auth.Actor, branchID uint64) ([]OrgItem, error) {
	return s.orgItems(ctx, actor, departments, branchID)
}
func (s *Service) PositionList(ctx context.Context, actor *auth.Actor, branchID uint64) ([]OrgItem, error) {
	return s.orgItems(ctx, actor, positions, branchID)
}

// SaveDepartment / SavePosition create (id 0, in branchID) or rename an item.
func (s *Service) SaveDepartment(ctx context.Context, actor *auth.Actor, id, branchID uint64, name string) (*OrgItem, error) {
	return s.saveOrg(ctx, actor, departments, id, branchID, name)
}
func (s *Service) SavePosition(ctx context.Context, actor *auth.Actor, id, branchID uint64, name string) (*OrgItem, error) {
	return s.saveOrg(ctx, actor, positions, id, branchID, name)
}
func (s *Service) DeleteDepartment(ctx context.Context, actor *auth.Actor, id uint64) error {
	return s.deleteOrg(ctx, actor, departments, id)
}
func (s *Service) DeletePosition(ctx context.Context, actor *auth.Actor, id uint64) error {
	return s.deleteOrg(ctx, actor, positions, id)
}

func (s *Service) orgItems(ctx context.Context, actor *auth.Actor, l orgList, branchID uint64) ([]OrgItem, error) {
	out := []OrgItem{}
	q := s.db.WithContext(ctx).Table(l.table + " o").
		Select("o.id, o.branch_id, b.name AS branch_name, o.name, COUNT(e.id) AS employees").
		Joins("JOIN branches b ON b.id = o.branch_id").
		Joins(fmt.Sprintf("LEFT JOIN employees e ON e.%s = o.id AND e.deleted_at IS NULL", l.column))
	q = auth.InReach(q, actor, "o.branch_id")
	if branchID != 0 {
		q = q.Where("o.branch_id = ?", branchID)
	}
	err := q.Group("o.id, o.branch_id, b.name, o.name").Order("b.name, o.name").Scan(&out).Error
	return out, err
}

// canEditOrg: staff managers edit the lists of the branches they reach.
func canEditOrg(actor *auth.Actor, branchID uint64) bool {
	return actor.Has(auth.PermStaffManage) && actor.Reaches(branchID)
}

// orgBranch is an item's branch (0 = no such item).
func orgBranch(tx *gorm.DB, l orgList, id uint64) (uint64, string) {
	var row struct {
		BranchID uint64
		Name     string
	}
	tx.Table(l.table).Select("branch_id, name").Where("id = ?", id).Scan(&row)
	return row.BranchID, row.Name
}

func (s *Service) saveOrg(ctx context.Context, actor *auth.Actor, l orgList, id, branchID uint64, name string) (*OrgItem, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 {
		return nil, apperr.BadRequest("INVALID_NAME", "Name is required (up to 100 characters).")
	}
	if id != 0 {
		if branchID, _ = orgBranch(s.db.WithContext(ctx), l, id); branchID == 0 {
			return nil, apperr.NotFound("NOT_FOUND", fmt.Sprintf("Unknown %s.", l.label))
		}
	}
	if branchID == 0 {
		return nil, apperr.BadRequest("BRANCH_REQUIRED", fmt.Sprintf("Choose the branch this %s belongs to.", l.label))
	}
	if !canEditOrg(actor, branchID) {
		return nil, apperr.ErrForbidden
	}
	item := OrgItem{ID: id, BranchID: branchID, Name: name}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var before string
		if id == 0 {
			var n int64
			if tx.Model(&models.Branch{}).Where("id = ?", branchID).Count(&n); n == 0 {
				return apperr.BadRequest("INVALID_BRANCH", "Unknown branch.")
			}
			row := l.newRow(branchID, name)
			if err := tx.Table(l.table).Create(row).Error; err != nil {
				return orgWriteErr(err, l)
			}
			switch r := row.(type) {
			case *models.Department:
				item.ID = r.ID
			case *models.Position:
				item.ID = r.ID
			}
		} else {
			if err := tx.Table(l.table).Select("name").Where("id = ?", id).Scan(&before).Error; err != nil || before == "" {
				return apperr.NotFound("NOT_FOUND", fmt.Sprintf("Unknown %s.", l.label))
			}
			if err := tx.Table(l.table).Where("id = ?", id).Update("name", name).Error; err != nil {
				return orgWriteErr(err, l)
			}
			if l.table == "positions" { // employees keep the position's name for display
				if err := tx.Model(&models.Employee{}).Where("position_id = ?", id).Update("position", name).Error; err != nil {
					return err
				}
			}
		}
		tx.Model(&models.Employee{}).Where(l.column+" = ? AND deleted_at IS NULL", item.ID).Count(&item.Employees)
		tx.Model(&models.Branch{}).Select("name").Where("id = ?", branchID).Scan(&item.BranchName)
		return audit.Write(ctx, tx, actor, audit.Entry{Action: l.saveAction, EntityType: l.label, EntityID: item.ID,
			Details: map[string]any{"name": name, "before": before, "branch_id": branchID}})
	})
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Service) deleteOrg(ctx context.Context, actor *auth.Actor, l orgList, id uint64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		branchID, name := orgBranch(tx, l, id)
		if branchID == 0 {
			return apperr.NotFound("NOT_FOUND", fmt.Sprintf("Unknown %s.", l.label))
		}
		if !canEditOrg(actor, branchID) {
			return apperr.ErrForbidden
		}
		var n int64
		tx.Model(&models.Employee{}).Where(l.column+" = ? AND deleted_at IS NULL", id).Count(&n)
		if n > 0 {
			return apperr.Conflict("IN_USE", fmt.Sprintf("%d employee(s) are in %s. Move them first.", n, name))
		}
		// Deactivated people keep their history but lose the link.
		if err := tx.Model(&models.Employee{}).Where(l.column+" = ?", id).Update(l.column, nil).Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM "+l.table+" WHERE id = ?", id).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: l.delete, EntityType: l.label, EntityID: id, Details: map[string]any{"name": name}})
	})
}

func orgWriteErr(err error, l orgList) error {
	var me *mysqldrv.MySQLError
	if errors.As(err, &me) && me.Number == 1062 {
		return apperr.Conflict("NAME_TAKEN", fmt.Sprintf("Another %s in this branch already has this name.", l.label))
	}
	return err
}
