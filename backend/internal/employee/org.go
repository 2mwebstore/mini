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

// OrgItem is a department or position with how many active employees have it.
type OrgItem struct {
	ID        uint64 `json:"id"`
	Name      string `json:"name"`
	Employees int64  `json:"employees"`
}

// orgList describes one managed list (departments or positions).
type orgList struct {
	table, column, label string
	saveAction, delete   string
	newRow               func(name string) any
}

var (
	departments = orgList{table: "departments", column: "department_id", label: "department", saveAction: audit.ActionDepartmentSave,
		delete: audit.ActionDepartmentDelete, newRow: func(n string) any { return &models.Department{Name: n} }}
	positions = orgList{table: "positions", column: "position_id", label: "position", saveAction: audit.ActionPositionSave,
		delete: audit.ActionPositionDelete, newRow: func(n string) any { return &models.Position{Name: n} }}
)

func (s *Service) DepartmentList(ctx context.Context) ([]OrgItem, error) {
	return s.orgItems(ctx, departments)
}
func (s *Service) PositionList(ctx context.Context) ([]OrgItem, error) {
	return s.orgItems(ctx, positions)
}

func (s *Service) SaveDepartment(ctx context.Context, actor *auth.Actor, id uint64, name string) (*OrgItem, error) {
	return s.saveOrg(ctx, actor, departments, id, name)
}
func (s *Service) SavePosition(ctx context.Context, actor *auth.Actor, id uint64, name string) (*OrgItem, error) {
	return s.saveOrg(ctx, actor, positions, id, name)
}
func (s *Service) DeleteDepartment(ctx context.Context, actor *auth.Actor, id uint64) error {
	return s.deleteOrg(ctx, actor, departments, id)
}
func (s *Service) DeletePosition(ctx context.Context, actor *auth.Actor, id uint64) error {
	return s.deleteOrg(ctx, actor, positions, id)
}

func (s *Service) orgItems(ctx context.Context, l orgList) ([]OrgItem, error) {
	out := []OrgItem{}
	err := s.db.WithContext(ctx).Table(l.table + " o").
		Select("o.id, o.name, COUNT(e.id) AS employees").
		Joins(fmt.Sprintf("LEFT JOIN employees e ON e.%s = o.id AND e.deleted_at IS NULL", l.column)).
		Group("o.id, o.name").Order("o.name").Scan(&out).Error
	return out, err
}

// The lists are company-wide: only staff managers who reach every branch change them.
func canEditOrg(actor *auth.Actor) bool {
	return actor.Has(auth.PermStaffManage) && actor.Has(auth.PermAllView) && actor.AllBranches()
}

func (s *Service) saveOrg(ctx context.Context, actor *auth.Actor, l orgList, id uint64, name string) (*OrgItem, error) {
	if !canEditOrg(actor) {
		return nil, apperr.ErrForbidden
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 {
		return nil, apperr.BadRequest("INVALID_NAME", "Name is required (up to 100 characters).")
	}
	item := OrgItem{ID: id, Name: name}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var before string
		if id == 0 {
			row := l.newRow(name)
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
		return audit.Write(ctx, tx, actor, audit.Entry{Action: l.saveAction, EntityType: l.label, EntityID: item.ID,
			Details: map[string]any{"name": name, "before": before}})
	})
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Service) deleteOrg(ctx context.Context, actor *auth.Actor, l orgList, id uint64) error {
	if !canEditOrg(actor) {
		return apperr.ErrForbidden
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var name string
		if err := tx.Table(l.table).Select("name").Where("id = ?", id).Scan(&name).Error; err != nil || name == "" {
			return apperr.NotFound("NOT_FOUND", fmt.Sprintf("Unknown %s.", l.label))
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
		return apperr.Conflict("NAME_TAKEN", fmt.Sprintf("Another %s already has this name.", l.label))
	}
	return err
}
