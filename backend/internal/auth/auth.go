// Package auth resolves who is calling (admin panel JWT, or the Telegram user ID
// from the Mini App's signed initData) and decides what they may do (permissions.go).
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/models"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Service struct {
	db     *gorm.DB
	secret []byte
	ttl    time.Duration
}

func NewService(db *gorm.DB, secret string, ttl time.Duration) *Service {
	return &Service{db: db, secret: []byte(secret), ttl: ttl}
}

type Claims struct {
	EmployeeID uint64 `json:"eid"`
	jwt.RegisteredClaims
}

// A constant bcrypt hash used to keep login timing uniform when the email doesn't exist.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equaliser"), bcrypt.DefaultCost)

func HashPassword(pw string) (string, error) {
	if len(pw) < 10 {
		return "", apperr.BadRequest("WEAK_PASSWORD", "Password must be at least 10 characters.")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(h), err
}

type LoginResult struct {
	Token     string          `json:"token"`
	ExpiresAt time.Time       `json:"expires_at"`
	Employee  models.Employee `json:"employee"`
	Role      string          `json:"role"`
	Perms     []Permission    `json:"permissions"`
}

func (s *Service) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	invalid := apperr.New(401, "INVALID_CREDENTIALS", "Invalid email or password.")
	var u models.User
	err := s.db.WithContext(ctx).Where("email = ?", strings.ToLower(strings.TrimSpace(email))).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, invalid
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, invalid
	}
	if u.Status != models.EmployeeActive {
		return nil, invalid
	}
	emp, err := s.loadEmployee(ctx, u.EmployeeID)
	if err != nil {
		return nil, err
	}
	if !emp.IsActive() || !emp.IsSystemUser() || !PanelAccess(emp.RoleID) {
		// The admin panel is for system users; staff use the Mini App.
		return nil, apperr.Forbidden("Your account does not have admin panel access.")
	}
	now := time.Now()
	exp := now.Add(s.ttl)
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		EmployeeID: emp.ID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprint(u.ID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			Issuer:    "attendance-api",
		},
	})
	signed, err := tok.SignedString(s.secret)
	if err != nil {
		return nil, err
	}
	s.db.WithContext(ctx).Model(&models.User{}).Where("id = ?", u.ID).Update("last_login_at", now.UTC())
	return &LoginResult{Token: signed, ExpiresAt: exp, Employee: *emp, Role: RoleCode(emp.RoleID), Perms: PermissionsFor(emp.RoleID)}, nil
}

// ActorFromJWT validates the token and re-reads the employee, so deactivation or a
// role change takes effect immediately rather than when the token expires.
func (s *Service) ActorFromJWT(ctx context.Context, raw string) (*Actor, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		return s.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer("attendance-api"))
	if err != nil {
		return nil, apperr.ErrUnauthorized
	}
	var u models.User
	if err := s.db.WithContext(ctx).Where("employee_id = ?", claims.EmployeeID).First(&u).Error; err != nil || u.Status != models.EmployeeActive || fmt.Sprint(u.ID) != claims.Subject {
		return nil, apperr.ErrUnauthorized
	}
	emp, err := s.loadEmployee(ctx, claims.EmployeeID)
	if err != nil || !emp.IsActive() || !emp.IsSystemUser() {
		return nil, apperr.ErrUnauthorized
	}
	a := &Actor{Employee: *emp, Channel: ChannelDashboard}
	if !a.PanelAccess() {
		// Demoted since signing in (e.g. no longer a branch admin): the session ends now.
		return nil, apperr.ErrUnauthorized
	}
	if a.Branches, err = s.branchesOf(ctx, emp.ID); err != nil {
		return nil, err
	}
	if a.Team, err = ManagedBy(ctx, s.db, emp.ID); err != nil {
		return nil, err
	}
	return a, nil
}

// ActorFromTelegram resolves a Telegram user ID (the only trusted Telegram identity).
func (s *Service) ActorFromTelegram(ctx context.Context, tgUserID int64) (*Actor, error) {
	if tgUserID == 0 {
		return nil, apperr.ErrNotRegistered
	}
	var acc models.TelegramAccount
	err := s.db.WithContext(ctx).Where("telegram_user_id = ?", tgUserID).First(&acc).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.ErrNotRegistered
	}
	if err != nil {
		return nil, err
	}
	emp, err := s.loadEmployee(ctx, acc.EmployeeID)
	if err != nil {
		return nil, err
	}
	if emp.DeletedAt != nil {
		return nil, apperr.ErrNotRegistered
	}
	if emp.Status != models.EmployeeActive {
		return nil, apperr.ErrInactive
	}
	a := &Actor{Employee: *emp, Channel: ChannelTelegram}
	if a.Branches, err = s.branchesOf(ctx, emp.ID); err != nil {
		return nil, err
	}
	if a.Team, err = ManagedBy(ctx, s.db, emp.ID); err != nil {
		return nil, err
	}
	return a, nil
}

// TouchTelegramProfile keeps the informational username/name fields fresh.
func (s *Service) TouchTelegramProfile(ctx context.Context, tgUserID int64, username, first, last string) {
	s.db.WithContext(ctx).Model(&models.TelegramAccount{}).
		Where("telegram_user_id = ? AND (username <> ? OR first_name <> ? OR last_name <> ?)", tgUserID, username, first, last).
		Updates(map[string]any{"username": username, "first_name": first, "last_name": last})
}

// branchesOf lists the branches assigned to a system user (none = no restriction).
func (s *Service) branchesOf(ctx context.Context, empID uint64) ([]uint64, error) {
	return BranchesOf(ctx, s.db, empID)
}

// BranchesOf lists the branches assigned to a system user.
func BranchesOf(ctx context.Context, db *gorm.DB, empID uint64) ([]uint64, error) {
	var ids []uint64
	err := db.WithContext(ctx).Model(&models.UserBranch{}).Where("employee_id = ?", empID).Order("branch_id").Pluck("branch_id", &ids).Error
	return ids, err
}

func (s *Service) loadEmployee(ctx context.Context, id uint64) (*models.Employee, error) {
	refreshRoles(ctx, s.db)
	var e models.Employee
	err := s.db.WithContext(ctx).Preload("Department").Preload("Branch").Preload("WorkSchedule").Preload("Role").Preload("TelegramAccount").
		First(&e, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.ErrUnauthorized
	}
	return &e, err
}

// TeamIDs returns the active staff managed by managerID (one of their managers).
func TeamIDs(ctx context.Context, db *gorm.DB, managerID uint64) ([]uint64, error) {
	var ids []uint64
	err := db.WithContext(ctx).Model(&models.Employee{}).
		Where("id IN (SELECT employee_id FROM employee_managers WHERE manager_id = ?) AND deleted_at IS NULL AND kind = ?", managerID, models.KindStaff).
		Pluck("id", &ids).Error
	return ids, err
}

// ManagedBy lists everyone managerID manages, deactivated people too (their history).
func ManagedBy(ctx context.Context, db *gorm.DB, managerID uint64) ([]uint64, error) {
	ids := []uint64{}
	err := db.WithContext(ctx).Model(&models.EmployeeManager{}).Where("manager_id = ?", managerID).Pluck("employee_id", &ids).Error
	return ids, err
}

// ManagersOf lists the managers of employeeID.
func ManagersOf(ctx context.Context, db *gorm.DB, employeeID uint64) ([]uint64, error) {
	ids := []uint64{}
	err := db.WithContext(ctx).Model(&models.EmployeeManager{}).Where("employee_id = ?", employeeID).Order("manager_id").Pluck("manager_id", &ids).Error
	return ids, err
}

// Scope returns nil (everyone) or the employee IDs the actor is allowed to see.
func Scope(ctx context.Context, db *gorm.DB, a *Actor) ([]uint64, error) {
	if a.Has(PermAllView) && a.AllBranches() {
		return nil, nil
	}
	var team, branch []uint64
	if a.Has(PermTeamView) {
		var err error
		if team, err = TeamIDs(ctx, db, a.Employee.ID); err != nil {
			return nil, err
		}
	}
	if _, ids := a.BranchScope(); len(ids) > 0 {
		// Deactivated people stay in scope so their history remains in reports.
		if err := db.WithContext(ctx).Model(&models.Employee{}).
			Where("branch_id IN ? AND role_id NOT IN ? AND kind = ?", ids, models.CompanyWideRoleIDs(), models.KindStaff).
			Pluck("id", &branch).Error; err != nil {
			return nil, err
		}
	}
	return a.ScopeEmployeeIDs(team, branch), nil
}
