package identity

import (
	"context"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Role struct {
	ID          string    `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Builtin     bool      `json:"builtin"`
	Version     int64     `json:"version"`
	Permissions []string  `json:"permissions"`
	UserCount   int64     `json:"user_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type RoleInput struct {
	Code            string   `json:"code"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Permissions     []string `json:"permissions"`
	ExpectedVersion int64    `json:"expected_version"`
}
type Provisioning struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code"`
}
type ManagedUser struct {
	ID               string        `json:"id"`
	DisplayName      string        `json:"display_name"`
	Email            string        `json:"email"`
	LoginEmail       string        `json:"login_email"`
	IdentitySource   string        `json:"identity_source"`
	Issuer           string        `json:"issuer"`
	Subject          string        `json:"subject"`
	WorkspaceOwnerID string        `json:"workspace_owner_id"`
	Status           string        `json:"status"`
	RoleID           string        `json:"role_id"`
	RoleCode         string        `json:"role_code"`
	RoleName         string        `json:"role_name"`
	Version          int64         `json:"version"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	LastLoginAt      *time.Time    `json:"last_login_at"`
	Provisioning     *Provisioning `json:"provisioning"`
	PasswordReset    *Provisioning `json:"password_reset"`
}
type UserCreateInput struct {
	DisplayName     string `json:"display_name"`
	Email           string `json:"email"`
	IdentitySource  string `json:"identity_source"`
	Subject         string `json:"subject,omitempty"`
	RoleID          string `json:"role_id"`
	InitialPassword string `json:"initial_password,omitempty"`
}
type UserEditInput struct {
	DisplayName     string `json:"display_name"`
	Email           string `json:"email"`
	ExpectedVersion int64  `json:"expected_version"`
}
type UserActionInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	RoleID          string `json:"role_id,omitempty"`
	Password        string `json:"password,omitempty"`
}

type UserDeleteInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	ConfirmedName   string `json:"confirmed_name"`
}
type ManagementListInput struct {
	Limit                         int
	Page                          int
	Cursor, Query, Status, RoleID string
}
type ManagementPage[T any] struct {
	Items         []T     `json:"items"`
	Total         int64   `json:"total"`
	NextCursor    *string `json:"next_cursor"`
	LocalAccounts bool    `json:"local_accounts"`
}

// LocalAccountProvider owns password policy and idempotent identity supply.
// Credential writes use their own transaction after the audited intent commits.
type LocalAccountProvider interface {
	Subject(string) string
	Ensure(context.Context, string, string, string, string) error
	ResetHash(context.Context, string, string, []byte) error
}

var roleCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,31}$`)
var managementKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

func managementInvalid(message string) error { return webapi.Fail(422, "IDENTITY_INVALID", message) }
func validLabel(value string, max int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) == value && value != "" && utf8.RuneCountInString(value) <= max && !strings.ContainsFunc(value, unicode.IsControl)
}
func validateRoleInput(in RoleInput) error {
	if !roleCodePattern.MatchString(in.Code) || in.Code == "admin" || in.Code == "member" || !validLabel(in.Name, 64) || !utf8.ValidString(in.Description) || utf8.RuneCountInString(in.Description) > 500 || in.ExpectedVersion < 0 || in.Permissions == nil || len(in.Permissions) > len(permissionCatalog) {
		return managementInvalid("角色编码、名称、说明、版本或权限集合不合法")
	}
	selected := map[string]bool{}
	for _, id := range in.Permissions {
		if selected[id] {
			return managementInvalid("权限不能重复")
		}
		selected[id] = true
	}
	for id := range selected {
		var found bool
		for _, permission := range permissionCatalog {
			if permission.ID != id {
				continue
			}
			found = permission.Assignable
			for _, dependency := range permission.Dependencies {
				if !selected[dependency] {
					return managementInvalid("权限 " + id + " 需要显式包含 " + dependency)
				}
			}
		}
		if !found {
			return managementInvalid("权限不在可分配目录中")
		}
	}
	return nil
}
func validateUserProfile(name, email string) error {
	parsed, err := mail.ParseAddress(email)
	if !validLabel(name, 100) || err != nil || parsed.Address != email || parsed.Name != "" || len(email) > 254 || strings.ContainsAny(email, "\r\n") {
		return managementInvalid("显示名或邮箱不合法")
	}
	return nil
}
func validateLocalPassword(password string) error {
	if !utf8.ValidString(password) || len(password) < 12 || len(password) > 72 || strings.ContainsFunc(password, unicode.IsControl) {
		return managementInvalid("本地账号密码须为12～72字节，不能包含控制字符")
	}
	return nil
}
