// Package access manages who may use the admin panel and what they may do there:
// roles and their permissions, system users (admin accounts, separate from
// employees), and each user's branch access ("no branches" = every branch).
package access

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/audit"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/models"

	mysqldrv "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

// ---------- roles ----------

type RoleView struct {
	models.Role
	Permissions []auth.Permission `json:"permissions"`
	Employees   int64             `json:"employees"`
	Users       int64             `json:"users"`  // with a panel login
	Locked      bool              `json:"locked"` // the owner role: always every permission
}

type RoleInput struct {
	Name        *string           `json:"name"`
	Description *string           `json:"description"`
	Permissions []auth.Permission `json:"permissions"`
}

func (s *Service) Roles(ctx context.Context) ([]RoleView, error) {
	if err := auth.LoadRoles(ctx, s.db); err != nil {
		return nil, err
	}
	var roles []models.Role
	if err := s.db.WithContext(ctx).Order("is_system DESC, id").Find(&roles).Error; err != nil {
		return nil, err
	}
	var counts []struct {
		RoleID uint8
		N      int64
		U      int64
	}
	if err := s.db.WithContext(ctx).Table("employees e").Select("e.role_id, COUNT(*) AS n, COUNT(u.id) AS u").
		Joins("LEFT JOIN users u ON u.employee_id = e.id").Where("e.deleted_at IS NULL").Group("e.role_id").Scan(&counts).Error; err != nil {
		return nil, err
	}
	out := make([]RoleView, len(roles))
	for i, r := range roles {
		out[i] = RoleView{Role: r, Permissions: auth.PermissionsFor(r.ID), Locked: r.ID == models.RoleSuperAdmin}
		for _, c := range counts {
			if c.RoleID == r.ID {
				out[i].Employees, out[i].Users = c.N, c.U
			}
		}
	}
	return out, nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func (s *Service) CreateRole(ctx context.Context, actor *auth.Actor, in RoleInput) (*models.Role, error) {
	if !actor.Has(auth.PermUsersManage) {
		return nil, apperr.ErrForbidden
	}
	if in.Name == nil {
		return nil, apperr.BadRequest("INVALID_NAME", "Role name is required.")
	}
	r := models.Role{Name: strings.TrimSpace(*in.Name)}
	if in.Description != nil {
		r.Description = strings.TrimSpace(*in.Description)
	}
	if err := checkRole(&r); err != nil {
		return nil, err
	}
	perms, err := checkPerms(in.Permissions)
	if err != nil {
		return nil, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var maxID int
		if err := tx.Model(&models.Role{}).Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
			return err
		}
		if maxID >= 255 {
			return apperr.Conflict("TOO_MANY_ROLES", "The maximum number of roles has been reached.")
		}
		r.ID = uint8(max(maxID+1, 6))
		base := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(r.Name), "_"), "_")
		if base == "" {
			base = "role"
		}
		r.Code = base
		for n := 2; ; n++ {
			var taken int64
			tx.Model(&models.Role{}).Where("code = ?", r.Code).Count(&taken)
			if taken == 0 {
				break
			}
			r.Code = fmt.Sprintf("%s_%d", base, n)
		}
		if err := tx.Create(&r).Error; err != nil {
			return roleWriteErr(err)
		}
		if err := setPerms(tx, r.ID, perms); err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionRoleCreate, EntityType: "role", EntityID: uint64(r.ID),
			Details: map[string]any{"name": r.Name, "permissions": perms}})
	})
	if err != nil {
		return nil, err
	}
	return &r, auth.LoadRoles(ctx, s.db)
}

func (s *Service) UpdateRole(ctx context.Context, actor *auth.Actor, id uint8, in RoleInput) (*models.Role, error) {
	if !actor.Has(auth.PermUsersManage) {
		return nil, apperr.ErrForbidden
	}
	var r models.Role
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&r, id).Error; err != nil {
			return apperr.NotFound("ROLE_NOT_FOUND", "Role not found.")
		}
		if r.ID == models.RoleSuperAdmin && in.Permissions != nil {
			return apperr.BadRequest("ROLE_LOCKED", "The owner role always has every permission.")
		}
		before := auth.PermissionsFor(r.ID)
		if in.Name != nil {
			r.Name = strings.TrimSpace(*in.Name)
		}
		if in.Description != nil {
			r.Description = strings.TrimSpace(*in.Description)
		}
		if err := checkRole(&r); err != nil {
			return err
		}
		if err := tx.Save(&r).Error; err != nil {
			return roleWriteErr(err)
		}
		details := map[string]any{"name": r.Name}
		if in.Permissions != nil {
			perms, err := checkPerms(in.Permissions)
			if err != nil {
				return err
			}
			if r.ID == actor.Employee.RoleID && !slices.Contains(perms, auth.PermUsersManage) {
				return apperr.BadRequest("SELF_LOCKOUT", "You cannot remove “Manage system users” from your own role.")
			}
			if err := setPerms(tx, r.ID, perms); err != nil {
				return err
			}
			details["before"], details["after"] = before, perms
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionRoleUpdate, EntityType: "role", EntityID: uint64(r.ID), Details: details})
	})
	if err != nil {
		return nil, err
	}
	return &r, auth.LoadRoles(ctx, s.db)
}

// DeleteRole removes a custom role nobody has.
func (s *Service) DeleteRole(ctx context.Context, actor *auth.Actor, id uint8) error {
	if !actor.Has(auth.PermUsersManage) {
		return apperr.ErrForbidden
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r models.Role
		if err := tx.First(&r, id).Error; err != nil {
			return apperr.NotFound("ROLE_NOT_FOUND", "Role not found.")
		}
		if r.IsSystem {
			return apperr.Conflict("ROLE_SYSTEM", "Built-in roles cannot be deleted.")
		}
		var n int64
		tx.Model(&models.Employee{}).Where("role_id = ?", id).Count(&n)
		if n > 0 {
			return apperr.Conflict("ROLE_IN_USE", fmt.Sprintf("%d employee(s) have this role. Give them another role first.", n))
		}
		if err := tx.Where("role_id = ?", id).Delete(&models.RolePermission{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&r).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionRoleDelete, EntityType: "role", EntityID: uint64(id), Details: map[string]any{"name": r.Name}})
	})
	if err != nil {
		return err
	}
	return auth.LoadRoles(ctx, s.db)
}

func checkRole(r *models.Role) error {
	if r.Name == "" || len([]rune(r.Name)) > 64 {
		return apperr.BadRequest("INVALID_NAME", "Role name is required (up to 64 characters).")
	}
	if len([]rune(r.Description)) > 255 {
		return apperr.BadRequest("DESCRIPTION_TOO_LONG", "Description: at most 255 characters.")
	}
	return nil
}

func checkPerms(in []auth.Permission) ([]auth.Permission, error) {
	known := map[auth.Permission]bool{}
	for _, p := range auth.Catalog {
		known[p.Code] = true
	}
	out := []auth.Permission{}
	for _, p := range in {
		if !known[p] {
			return nil, apperr.BadRequest("INVALID_PERMISSION", fmt.Sprintf("Unknown permission %q.", p))
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

func setPerms(tx *gorm.DB, roleID uint8, perms []auth.Permission) error {
	if err := tx.Where("role_id = ?", roleID).Delete(&models.RolePermission{}).Error; err != nil {
		return err
	}
	for _, p := range perms {
		if err := tx.Create(&models.RolePermission{RoleID: roleID, Permission: string(p)}).Error; err != nil {
			return err
		}
	}
	return nil
}

func roleWriteErr(err error) error {
	var me *mysqldrv.MySQLError
	if errors.As(err, &me) && me.Number == 1062 {
		return apperr.Conflict("ROLE_NAME_TAKEN", "Another role already has this name.")
	}
	return err
}
